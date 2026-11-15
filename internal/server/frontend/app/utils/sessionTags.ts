/**
 * Your own session tags (the sidebar's "Add tag", the Zen name plate, the
 * metrics sidebar, the WebLLM auto-tagger). This module is the only writer.
 *
 * They live in localStorage under `cct-session-tags` as { sessionId: "tag" };
 * several tags are kept as one comma-separated value (`sessionTagList` splits
 * it; the sidebar shows one chip per tag). The server keeps them too (`session_tags`, so the iOS app shows
 * them): every write is sent with `PUT /api/agent/sessions/:id/tags`, and on
 * load `syncSessionTags()` merges `GET /api/agent/session-tags` in (the server
 * wins, except for writes that have not reached it yet).
 */
import { ref } from 'vue'

export const SESSION_TAGS_STORAGE_KEY = 'cct-session-tags'
/** Session ids whose latest local write has not reached the server yet. */
export const SESSION_TAGS_PENDING_KEY = 'cct-session-tags-pending'
/** Set after the first sync, which uploads tags that only this browser had. */
export const SESSION_TAGS_UPLOADED_KEY = 'cct-session-tags-uploaded-v1'

/** Bumped on every write from this tab; computeds that read tags depend on it. */
export const sessionTagsVersion = ref(0)

export type SessionTagFetcher = (url: string, init?: RequestInit) => Promise<Response>
let fetcher: SessionTagFetcher | null = null

/** Sets the authenticated fetch used to reach the server (plugins/session-tags.client.ts); null turns syncing off. */
export function configureSessionTagSync(f: SessionTagFetcher | null): void {
  fetcher = f
}

export function readSessionTags(): Record<string, string> {
  try {
    const raw = JSON.parse(localStorage.getItem(SESSION_TAGS_STORAGE_KEY) || '{}')
    return raw && typeof raw === 'object' ? raw : {}
  } catch {
    return {}
  }
}

function storeSessionTags(all: Record<string, string>): void {
  try {
    localStorage.setItem(SESSION_TAGS_STORAGE_KEY, JSON.stringify(all))
  } catch { /* storage full or blocked: nothing to save to */ }
  sessionTagsVersion.value++
  // Views that only listen for cross-tab changes (the Zen name plate) hear this too.
  try {
    window.dispatchEvent(new StorageEvent('storage', { key: SESSION_TAGS_STORAGE_KEY }))
  } catch { /* old browsers without the StorageEvent constructor */ }
}

function readPending(): Set<string> {
  try {
    const raw = JSON.parse(localStorage.getItem(SESSION_TAGS_PENDING_KEY) || '[]')
    return new Set(Array.isArray(raw) ? raw.filter(x => typeof x === 'string') : [])
  } catch {
    return new Set()
  }
}

function setPending(id: string, pending: boolean): void {
  const all = readPending()
  if (pending === all.has(id)) return
  if (pending) all.add(id)
  else all.delete(id)
  try {
    if (all.size) localStorage.setItem(SESSION_TAGS_PENDING_KEY, JSON.stringify([...all]))
    else localStorage.removeItem(SESSION_TAGS_PENDING_KEY)
  } catch { /* blocked storage */ }
}

/** "a, b ,c" → ["a", "b", "c"], blanks and duplicates (case-insensitive) dropped. */
export function splitTags(value: string | null | undefined): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  for (const part of (value || '').split(',')) {
    const t = part.trim().replace(/\s+/g, ' ')
    if (!t || seen.has(t.toLowerCase())) continue
    seen.add(t.toLowerCase())
    out.push(t)
  }
  return out
}

export function sessionTagList(sessionId: string): string[] {
  return splitTags(readSessionTags()[sessionId])
}

/** Saves a session's tags (an empty list removes the entry), tells the other tag views and sends them to the server. */
export function writeSessionTagList(sessionId: string, tags: string[]): void {
  const all = readSessionTags()
  const value = splitTags(tags.join(',')).join(', ')
  if (value) all[sessionId] = value
  else delete all[sessionId]
  storeSessionTags(all)
  if (fetcher) {
    setPending(sessionId, true)
    void pushSessionTags(sessionId)
  }
}

/**
 * Sends a session's local tags to the server. On success the server's
 * normalised list (it keeps at most 8 tags of 40 characters) replaces the
 * local value, unless the tags changed again meanwhile. A 404 (the session is
 * gone) also ends the attempt; other failures leave it pending for the next sync.
 */
export async function pushSessionTags(sessionId: string): Promise<boolean> {
  if (!fetcher) return false
  const sent = readSessionTags()[sessionId] || ''
  try {
    const res = await fetcher(`/api/agent/sessions/${encodeURIComponent(sessionId)}/tags`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tags: splitTags(sent) }),
    })
    if (res.status === 404) {
      setPending(sessionId, false)
      return false
    }
    if (!res.ok) return false
    const body = await res.json().catch(() => null) as { tags?: string[] } | null
    const all = readSessionTags()
    if ((all[sessionId] || '') !== sent) return true // edited again: that write sends itself
    setPending(sessionId, false)
    const serverTags = body?.tags
    if (Array.isArray(serverTags)) {
      const stored = splitTags(serverTags.join(',')).join(', ')
      if (stored !== sent) {
        if (stored) all[sessionId] = stored
        else delete all[sessionId]
        storeSessionTags(all)
      }
    }
    return true
  } catch {
    return false
  }
}

/**
 * Merges the server's tags into localStorage. The server wins for sessions it
 * has tags for; local writes that have not reached it are sent instead. On the
 * first sync, sessions tagged only in this browser are uploaded (existing
 * desktop tags move to the server); after that a session missing on the server
 * has had its tags removed elsewhere, so its local tags go too.
 */
export async function syncSessionTags(): Promise<boolean> {
  if (!fetcher) return false
  let server: Record<string, string[]>
  try {
    const res = await fetcher('/api/agent/session-tags', { method: 'GET' })
    if (!res.ok) return false
    const body = await res.json() as { tags?: Record<string, string[]> }
    server = body?.tags && typeof body.tags === 'object' ? body.tags : {}
  } catch {
    return false
  }

  const local = readSessionTags()
  const pending = readPending()
  let firstSync = false
  try { firstSync = !localStorage.getItem(SESSION_TAGS_UPLOADED_KEY) } catch { /* blocked storage */ }

  const next: Record<string, string> = {}
  const uploads: string[] = []
  for (const [id, tags] of Object.entries(server)) {
    if (pending.has(id)) continue
    const value = splitTags((Array.isArray(tags) ? tags : []).join(',')).join(', ')
    if (value) next[id] = value
  }
  for (const [id, value] of Object.entries(local)) {
    if (pending.has(id)) {
      next[id] = value
    } else if (!(id in server) && firstSync && splitTags(value).length) {
      next[id] = value
      uploads.push(id)
    }
  }
  for (const id of pending) uploads.push(id)

  const changed = JSON.stringify(Object.entries(next).sort()) !== JSON.stringify(Object.entries(local).sort())
  if (changed) storeSessionTags(next)
  for (const id of uploads) setPending(id, true)
  await Promise.all([...new Set(uploads)].map(id => pushSessionTags(id)))
  if (firstSync) {
    try { localStorage.setItem(SESSION_TAGS_UPLOADED_KEY, '1') } catch { /* blocked storage */ }
  }
  return true
}

/** Adds tags to a session (existing ones kept, duplicates ignored). Returns the new list. */
export function addSessionTags(sessionId: string, tags: string[]): string[] {
  const next = splitTags([...sessionTagList(sessionId), ...tags].join(','))
  writeSessionTagList(sessionId, next)
  return next
}

export function removeSessionTag(sessionId: string, tag: string): string[] {
  const next = sessionTagList(sessionId).filter(t => t.toLowerCase() !== tag.toLowerCase())
  writeSessionTagList(sessionId, next)
  return next
}

/** Every tag used on any session, most used first, then alphabetical. */
export function knownSessionTags(): string[] {
  const counts = new Map<string, { tag: string; n: number }>()
  for (const value of Object.values(readSessionTags())) {
    for (const t of splitTags(value)) {
      const k = t.toLowerCase()
      const c = counts.get(k)
      if (c) c.n++
      else counts.set(k, { tag: t, n: 1 })
    }
  }
  return [...counts.values()].sort((a, b) => b.n - a.n || a.tag.localeCompare(b.tag)).map(c => c.tag)
}

/** Autocomplete: known tags that start with (then contain) the query, minus the ones in `exclude`. */
export function suggestSessionTags(query: string, known: string[], exclude: string[] = [], limit = 6): string[] {
  const q = query.trim().toLowerCase()
  const skip = new Set(exclude.map(t => t.toLowerCase()))
  const pool = known.filter(t => !skip.has(t.toLowerCase()))
  if (!q) return pool.slice(0, limit)
  const starts = pool.filter(t => t.toLowerCase().startsWith(q))
  const contains = pool.filter(t => !t.toLowerCase().startsWith(q) && t.toLowerCase().includes(q))
  return [...starts, ...contains].slice(0, limit)
}

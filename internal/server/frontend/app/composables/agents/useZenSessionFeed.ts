/**
 * useZenSessionFeed — what the *other* sessions are doing, for zen mode.
 *
 * Produces:
 *  - `notices`: calm cards for sessions that just finished a turn ("done") or
 *    are blocked on the user ("needs-you": a pending permission request).
 *  - `working`: the other sessions currently processing (ambient dots).
 *
 * Transitions are detected from the session store, so nothing new is needed
 * on the server. Notices for the session you are looking at are dropped.
 */
import { ref, computed, watch, type Ref } from 'vue'

export type ZenNoticeKind = 'done' | 'needs-you'

export interface ZenSessionNotice {
  key: string
  sessionId: string
  kind: ZenNoticeKind
  name: string
  project: string
  projectColor?: string
  avatarImage?: string | null
  avatarColor?: string | null
  snippet: string
  at: number
}

export interface ZenWorkingSession {
  sessionId: string
  name: string
  project: string
  projectColor?: string
}

export interface SessionLike {
  id: string
  status: string
  context_summary?: string
  git_branch?: string
  project_id?: string
  options?: { working_directory?: string } & Record<string, any>
}

interface Options {
  sessions: Ref<SessionLike[]>
  activeSessionId: Ref<string | null | undefined>
  /** sessionId → pending permission requests */
  permissions: Ref<Map<string, any[]>>
  /** Display details for a session (project name/colour, avatar) */
  describe: (s: SessionLike) => { project: string; projectColor?: string; avatarImage?: string | null; avatarColor?: string | null }
  /** Last assistant text we have for a session, if its messages are loaded */
  lastAssistantText: (sessionId: string) => string
}

const DONE_TTL_MS = 90_000
const MAX_NOTICES = 4

function truncate(s: string, n: number) {
  const t = s.replace(/\s+/g, ' ').trim()
  return t.length > n ? t.slice(0, n - 1) + '…' : t
}

export function sessionName(s: SessionLike): string {
  if (s.context_summary) return truncate(s.context_summary, 48)
  if (s.git_branch) return s.git_branch
  const wd = s.options?.working_directory
  if (wd) return wd.split('/').filter(Boolean).pop() || wd
  return s.id.slice(0, 8)
}

export function useZenSessionFeed(opts: Options) {
  const notices = ref<ZenSessionNotice[]>([])
  const now = ref(Date.now())
  let seq = 0

  // Tick so "done" notices expire and relative times stay fresh
  const timer = setInterval(() => {
    now.value = Date.now()
    const before = notices.value.length
    const kept = notices.value.filter(n => n.kind !== 'done' || now.value - n.at < DONE_TTL_MS)
    if (kept.length !== before) notices.value = kept
  }, 5000)

  function push(s: SessionLike, kind: ZenNoticeKind, snippet: string) {
    const d = opts.describe(s)
    const notice: ZenSessionNotice = {
      key: `${s.id}:${kind}:${++seq}`,
      sessionId: s.id,
      kind,
      name: sessionName(s),
      project: d.project,
      projectColor: d.projectColor,
      avatarImage: d.avatarImage,
      avatarColor: d.avatarColor,
      snippet: truncate(snippet, 140),
      at: Date.now(),
    }
    // One card per session: the newest state replaces the old one
    const rest = notices.value.filter(n => n.sessionId !== s.id)
    notices.value = [notice, ...rest].slice(0, MAX_NOTICES)
  }

  function dismiss(key: string) {
    notices.value = notices.value.filter(n => n.key !== key)
  }

  function clearSession(sessionId: string) {
    notices.value = notices.value.filter(n => n.sessionId !== sessionId)
  }

  // Finished turns: processing → idle on a session you are not looking at
  watch(
    () => opts.sessions.value.map(s => [s.id, s.status] as const),
    (next, prev) => {
      if (!prev) return
      const before = new Map(prev)
      for (const [id, status] of next) {
        if (id === opts.activeSessionId.value) continue
        const was = before.get(id)
        if (was === 'processing' && (status === 'idle' || status === 'active')) {
          const s = opts.sessions.value.find(x => x.id === id)
          if (!s) continue
          // A pending permission wins over "done"
          if ((opts.permissions.value.get(id) || []).length) continue
          push(s, 'done', opts.lastAssistantText(id) || 'Finished — waiting for your next message')
        } else if (status === 'processing') {
          // Working again: its old "done" card is stale
          notices.value = notices.value.filter(n => !(n.sessionId === id && n.kind === 'done'))
        }
      }
    },
  )

  // Blocked on the user: pending permission requests in other sessions
  watch(
    () => [...opts.permissions.value.entries()].map(([id, list]) => [id, (list || []).length] as const),
    (next) => {
      const counts = new Map(next)
      for (const [id, count] of counts) {
        if (id === opts.activeSessionId.value || count === 0) continue
        if (notices.value.some(n => n.sessionId === id && n.kind === 'needs-you')) continue
        const s = opts.sessions.value.find(x => x.id === id)
        if (!s) continue
        const p = opts.permissions.value.get(id)?.[0]
        const what = p?.tool ? `Wants to use ${p.tool}${p.details?.command ? `: ${p.details.command}` : p.details?.file_path ? ` on ${p.details.file_path}` : ''}` : 'Waiting for your approval'
        push(s, 'needs-you', what)
      }
      // Resolved elsewhere: drop the card
      notices.value = notices.value.filter(n => n.kind !== 'needs-you' || (counts.get(n.sessionId) || 0) > 0)
    },
    // Not deep: the getter returns fresh [id, count] pairs, which already
    // track exactly the permission lists' sizes.
  )

  watch(opts.activeSessionId, (id) => { if (id) clearSession(id) })

  const working = computed<ZenWorkingSession[]>(() =>
    opts.sessions.value
      .filter(s => s.status === 'processing' && s.id !== opts.activeSessionId.value)
      .slice(0, 8)
      .map(s => {
        const d = opts.describe(s)
        return { sessionId: s.id, name: sessionName(s), project: d.project, projectColor: d.projectColor }
      }),
  )

  function stop() {
    clearInterval(timer)
  }

  return { notices, working, now, dismiss, clearSession, stop }
}

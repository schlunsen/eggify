/**
 * The video and audio files of each session (GET /api/agent/sessions/:id/media),
 * shared by the chat's inline players and the Media tab. Module-level cache:
 * a session's list is loaded when it is opened and reloaded (debounced) when a
 * live tool call names a media file, show_media is called, or a turn ends.
 * While an iPhone copy is being made the list is polled until it is done.
 */
import { reactive } from 'vue'
import { useAuthenticatedFetch } from '~/composables/useAuthenticatedFetch'
import { anyConverting, sortMedia, type MediaItem, type MediaListResponse } from '~/utils/agents/sessionMedia'

export const MEDIA_REFRESH_DEBOUNCE_MS = 1500
export const MEDIA_CONVERT_POLL_MS = 3000

export interface SessionMediaState {
  items: MediaItem[]
  ffmpeg: boolean
  loaded: boolean
  loading: boolean
  error: string
}

const states = reactive<Record<string, SessionMediaState>>({})
const inFlight = new Map<string, Promise<void>>()
const refreshTimers = new Map<string, ReturnType<typeof setTimeout>>()
const pollTimers = new Map<string, ReturnType<typeof setTimeout>>()
// Last loaded list per session as JSON, so an unchanged reload keeps the same
// array (the chat's lookup, and so every bubble, only updates on real changes).
const lastJson = new Map<string, string>()

function ensureState(sessionId: string): SessionMediaState {
  if (!states[sessionId]) {
    states[sessionId] = { items: [], ffmpeg: false, loaded: false, loading: false, error: '' }
  }
  return states[sessionId]!
}

function setItems(sessionId: string, items: MediaItem[]) {
  const state = ensureState(sessionId)
  const sorted = sortMedia(items)
  const json = JSON.stringify(sorted)
  if (json === lastJson.get(sessionId) && state.loaded) return
  lastJson.set(sessionId, json)
  state.items = sorted
}

function schedulePoll(sessionId: string, fetcher: () => Promise<void>) {
  if (pollTimers.has(sessionId)) return
  pollTimers.set(sessionId, setTimeout(() => {
    pollTimers.delete(sessionId)
    void fetcher()
  }, MEDIA_CONVERT_POLL_MS))
}

export function useSessionMedia() {
  const { fetchWithAuth } = useAuthenticatedFetch()

  /** The cached list (undefined until the session's list was asked for). */
  function stateFor(sessionId: string | null | undefined): SessionMediaState | undefined {
    return sessionId ? states[sessionId] : undefined
  }

  function load(sessionId: string | null | undefined): Promise<void> {
    if (!sessionId) return Promise.resolve()
    const running = inFlight.get(sessionId)
    if (running) return running
    const state = ensureState(sessionId)
    state.loading = true
    const job = (async () => {
      try {
        const res = await fetchWithAuth(`/api/agent/sessions/${encodeURIComponent(sessionId)}/media`)
        if (res.status === 404) {
          setItems(sessionId, [])
          state.error = ''
        } else if (!res.ok) {
          state.error = "Couldn't load this session's media."
        } else {
          const body = (await res.json()) as Partial<MediaListResponse> | null
          state.ffmpeg = !!body?.ffmpeg
          setItems(sessionId, Array.isArray(body?.items) ? body!.items : [])
          state.error = ''
        }
        state.loaded = true
      } catch {
        state.error = "Couldn't load this session's media."
      } finally {
        state.loading = false
        inFlight.delete(sessionId)
      }
      if (anyConverting(state.items)) schedulePoll(sessionId, () => load(sessionId))
    })()
    inFlight.set(sessionId, job)
    return job
  }

  /**
   * Reload a session's list after a short pause, so a burst of tool calls makes
   * one request. Sessions whose list was never asked for are left alone.
   */
  function refresh(sessionId: string | null | undefined) {
    if (!sessionId || !states[sessionId]) return
    const pending = refreshTimers.get(sessionId)
    if (pending) clearTimeout(pending)
    refreshTimers.set(sessionId, setTimeout(() => {
      refreshTimers.delete(sessionId)
      void load(sessionId)
    }, MEDIA_REFRESH_DEBOUNCE_MS))
  }

  /** Make an iPhone-friendly copy (H.264 MP4 / AAC M4A). Returns an error to show, or ''. */
  async function convert(sessionId: string, item: MediaItem): Promise<string> {
    const state = ensureState(sessionId)
    let res: Response
    try {
      res = await fetchWithAuth(
        `/api/agent/sessions/${encodeURIComponent(sessionId)}/media/${encodeURIComponent(item.id)}/convert`,
        { method: 'POST' },
      )
    } catch {
      return "Couldn't reach the server."
    }
    if (res.status === 501) {
      state.ffmpeg = false
      return 'ffmpeg is not installed on the computer running Eggify.'
    }
    if (res.status === 409) {
      void load(sessionId)
      return 'The file changed. Try again.'
    }
    if (!res.ok) return "Couldn't start the conversion."
    let body: { state?: string; compatible_url?: string } | null = null
    try { body = await res.json() } catch { /* empty body: treat as running */ }
    const compatible = body?.state === 'ready' ? 'ready' : 'running'
    setItems(sessionId, state.items.map(i => i.id === item.id
      ? { ...i, compatible, compatible_url: body?.compatible_url || i.compatible_url, compatible_error: undefined }
      : i))
    if (compatible === 'running') schedulePoll(sessionId, () => load(sessionId))
    return ''
  }

  return { stateFor, load, refresh, convert }
}

/** Tests: forget every cached list and timer. */
export function resetSessionMedia() {
  for (const t of refreshTimers.values()) clearTimeout(t)
  for (const t of pollTimers.values()) clearTimeout(t)
  refreshTimers.clear()
  pollTimers.clear()
  inFlight.clear()
  lastJson.clear()
  for (const key of Object.keys(states)) delete states[key]
}

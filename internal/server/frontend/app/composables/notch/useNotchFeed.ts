import { onUnmounted, ref, shallowRef } from 'vue'
import {
  buildScene,
  noteTransitions,
  type NotchScene,
  type NotchSession,
  type PendingPermission,
  type PendingQuestion,
  type Recent,
} from '~/utils/notch/notchScene'

/**
 * Data for the Notch Theatre: the session list and what waits for the user,
 * over REST (GET /api/agent/sessions?status=all, GET /api/agent/pending). The
 * notch has no agent WebSocket of its own, so it polls: every 1.5 s while a
 * card waits (an approval is denied after 60 s), 2.5 s while something works,
 * 6 s otherwise. The scene clock ticks every second for countdowns.
 *
 * Requests carry the session cookie (so the lists are the signed-in user's)
 * and never redirect to the login page, which can't fit in the notch: a 401
 * shows "Sign in to Eggify" on the stage. Only when user auth is off (the
 * API-key endpoint then answers without a session) is the API key sent.
 */
export const NOTCH_POLL_NEEDS_MS = 1500
export const NOTCH_POLL_BUSY_MS = 2500
export const NOTCH_POLL_IDLE_MS = 6000

export function useNotchFeed(opts: { onDone?: (ids: string[]) => void; onFailed?: (ids: string[]) => void; onNewCue?: () => void } = {}) {
  const sessions = shallowRef<NotchSession[]>([])
  const permissions = shallowRef<PendingPermission[]>([])
  const questions = shallowRef<PendingQuestion[]>([])
  const now = ref(Date.now())
  const scene = shallowRef<NotchScene>(buildScene([], [], [], { doneAt: {}, failedAt: {} }, Date.now()))
  const error = ref('')
  const signedOut = ref(false)
  let apiKey: string | null = null
  const loaded = ref(false)
  const recent: Recent = { doneAt: {}, failedAt: {} }
  let previous = new Map<string, { status: string; error?: string | null }>()
  let seenCues = new Set<string>()
  let timer: ReturnType<typeof setTimeout> | undefined
  let clock: ReturnType<typeof setInterval> | undefined
  let running = false
  let inFlight = false

  function rebuild() {
    now.value = Date.now()
    scene.value = buildScene(sessions.value, permissions.value, questions.value, recent, now.value)
  }

  async function getJson(url: string) {
    const headers: Record<string, string> = apiKey ? { Authorization: `Bearer ${apiKey}` } : {}
    let r = await fetch(url, { credentials: 'same-origin', headers })
    if (r.status === 401 && !apiKey) {
      const key = await fetch('/api/config/api-key', { credentials: 'same-origin' })
        .then(k => k.ok ? k.json() : null).catch(() => null)
      if (key?.apiKey) {
        apiKey = key.apiKey
        r = await fetch(url, { credentials: 'same-origin', headers: { Authorization: `Bearer ${apiKey}` } })
      }
    }
    if (r.status === 401 || r.status === 403) throw Object.assign(new Error('signed out'), { signedOut: true })
    if (!r.ok) throw new Error(`HTTP ${r.status}`)
    return r.json()
  }

  function schedule() {
    if (timer) clearTimeout(timer)
    timer = undefined
    if (!running) return
    const mood = scene.value.mood
    const ms = mood === 'needs' ? NOTCH_POLL_NEEDS_MS : (mood === 'working' || mood === 'bow' || mood === 'oops') ? NOTCH_POLL_BUSY_MS : NOTCH_POLL_IDLE_MS
    timer = setTimeout(() => { void refresh() }, ms)
  }

  async function refresh() {
    if (inFlight || !running) return
    inFlight = true
    try {
      const [list, pending] = await Promise.all([
        getJson('/api/agent/sessions?status=all'),
        getJson('/api/agent/pending'),
      ])
      if (!running) return
      const next: NotchSession[] = list.sessions || []
      const t = Date.now()
      if (loaded.value) {
        const { done, failed } = noteTransitions(previous, next, recent, t)
        if (done.length) opts.onDone?.(done)
        if (failed.length) opts.onFailed?.(failed)
      }
      previous = new Map(next.map(s => [s.id, { status: s.status, error: s.error_message }]))
      sessions.value = next
      permissions.value = pending.permissions || []
      questions.value = pending.questions || []
      rebuild()
      const cues = new Set(scene.value.cues.map(c => c.key))
      if (loaded.value && [...cues].some(k => !seenCues.has(k))) opts.onNewCue?.()
      seenCues = cues
      error.value = ''
      signedOut.value = false
      loaded.value = true
    } catch (e: any) {
      signedOut.value = !!e?.signedOut
      error.value = signedOut.value ? 'Sign in to Eggify' : 'Eggify is not answering'
    } finally {
      inFlight = false
      schedule()
    }
  }

  /** Drop an answered card at once (the next poll confirms it). */
  function forgetCue(key: string) {
    if (key.startsWith('p:')) permissions.value = permissions.value.filter(p => `p:${p.permission_id}` !== key)
    else questions.value = questions.value.filter(q => `q:${q.question_id}` !== key)
    rebuild()
  }

  function start() {
    if (running) return
    running = true
    clock = setInterval(rebuild, 1000)
    void refresh()
  }

  function stop() {
    running = false
    if (timer) clearTimeout(timer)
    if (clock) clearInterval(clock)
    timer = clock = undefined
  }

  onUnmounted(stop)

  return { scene, now, error, signedOut, loaded, start, stop, refresh, forgetCue }
}

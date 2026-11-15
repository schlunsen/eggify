import { onUnmounted, ref, shallowRef } from 'vue'
import { useAuthenticatedFetch } from '../useAuthenticatedFetch'
import type { BackgroundAgent } from './useBackgroundAgents'
import type { AllAgentsSession } from '~/utils/agents/allAgentsTree'

/**
 * Data for the All agents overlay (⇧⌘S): every session (GET /api/agent/sessions)
 * and every subagent of the last 24 h across them (GET /api/agent/subagents).
 *
 * Subagent events only reach WebSocket connections subscribed to their
 * session, so the overlay polls: every 2 s while something runs, every 6 s
 * otherwise, and not at all while the tab is hidden.
 */
export const ALL_AGENTS_POLL_BUSY_MS = 2000
export const ALL_AGENTS_POLL_IDLE_MS = 6000
/** How far back finished subagents are listed. */
export const ALL_AGENTS_WINDOW_MS = 24 * 60 * 60 * 1000

export function useAllAgents() {
  const { fetchWithAuth } = useAuthenticatedFetch()
  const sessions = shallowRef<AllAgentsSession[]>([])
  const subagents = shallowRef<BackgroundAgent[]>([])
  const projects = shallowRef<Record<string, { name: string; color?: string }>>({})
  const loading = ref(false)
  const loaded = ref(false)
  const error = ref('')
  let timer: ReturnType<typeof setTimeout> | undefined
  let running = false
  let inFlight = false
  let lastSessionsJson = ''
  let lastSubagentsJson = ''

  async function getJson(url: string) {
    const response = await fetchWithAuth(url)
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    return response.json()
  }

  function busy() {
    return subagents.value.some(a => a.status === 'running' || a.status === 'waiting') ||
      sessions.value.some(s => s.status === 'processing' || s.status === 'active')
  }

  function schedule() {
    if (timer) clearTimeout(timer)
    timer = undefined
    if (!running) return
    if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return
    timer = setTimeout(() => { void refresh() }, busy() ? ALL_AGENTS_POLL_BUSY_MS : ALL_AGENTS_POLL_IDLE_MS)
  }

  async function refresh() {
    if (inFlight || !running) return
    inFlight = true
    loading.value = true
    try {
      const since = new Date(Date.now() - ALL_AGENTS_WINDOW_MS).toISOString()
      const [sessionResult, subagentResult] = await Promise.all([
        getJson('/api/agent/sessions?status=all'),
        getJson(`/api/agent/subagents?status=all&since=${encodeURIComponent(since)}`)
      ])
      if (!running) return
      // Replace the lists only when they changed, so a quiet poll re-renders nothing.
      const nextSessions = JSON.stringify(sessionResult.sessions || [])
      if (nextSessions !== lastSessionsJson) { lastSessionsJson = nextSessions; sessions.value = sessionResult.sessions || [] }
      const nextSubagents = JSON.stringify(subagentResult.subagents || [])
      if (nextSubagents !== lastSubagentsJson) { lastSubagentsJson = nextSubagents; subagents.value = subagentResult.subagents || [] }
      error.value = ''
      loaded.value = true
    } catch {
      if (running) error.value = loaded.value ? 'Updates paused: the server did not answer. Retrying…' : 'Could not load agents. Retrying…'
    } finally {
      inFlight = false
      loading.value = false
      schedule()
    }
  }

  async function loadProjects() {
    try {
      const result = await getJson('/api/projects')
      if (running) projects.value = Object.fromEntries((result.projects || []).map((p: any) => [p.id, { name: p.name, color: p.color }]))
    } catch { /* Project names are optional; the folder name is shown instead. */ }
  }

  /** Every subagent of one session with its full kept output (for the detail pane). */
  async function loadSessionSubagents(sessionId: string): Promise<BackgroundAgent[] | null> {
    try {
      const result = await getJson(`/api/agent/sessions/${sessionId}/subagents`)
      return result.subagents || []
    } catch { return null }
  }

  function onVisibility() {
    if (!running) return
    if (document.visibilityState === 'visible') void refresh()
    else if (timer) { clearTimeout(timer); timer = undefined }
  }

  function start() {
    if (running) return
    running = true
    document.addEventListener('visibilitychange', onVisibility)
    void refresh()
    void loadProjects()
  }

  function stop() {
    running = false
    if (timer) clearTimeout(timer)
    timer = undefined
    document.removeEventListener('visibilitychange', onVisibility)
  }

  onUnmounted(stop)

  return { sessions, subagents, projects, loading, loaded, error, start, stop, refresh, loadSessionSubagents }
}

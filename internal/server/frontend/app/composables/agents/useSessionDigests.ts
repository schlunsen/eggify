import { ref } from 'vue'
import { useAuthenticatedFetch } from '../useAuthenticatedFetch'
import type { OverviewDigest } from './useAgentOverview'

/**
 * Shared, cached map of session id → AI digest (one-line summary + topic tags),
 * for the sessions sidebar. Uses the same endpoint as the agent overview
 * (POST /api/agent/overview/digests); the server only regenerates a digest once
 * a session has moved on, so asking again is cheap.
 */
export interface DigestSession {
  id: string
  status: string
  message_count?: number
}

const digests = ref<Record<string, OverviewDigest>>({})
let inFlight = false
let timer: ReturnType<typeof setTimeout> | null = null
let lastKey = ''
let lastAt = 0

export const SESSION_DIGEST_DEBOUNCE_MS = 800
const RECHECK_MS = 30000
const MAX_SESSIONS = 40

const versionOf = (s: DigestSession) => `${s.message_count || 0}:${s.status}`

export function useSessionDigests() {
  const { fetchWithAuth } = useAuthenticatedFetch()

  async function load(sessions: DigestSession[], force = false) {
    const shown = sessions.filter(s => s.id).slice(0, MAX_SESSIONS)
    if (!shown.length || inFlight) return
    const key = shown.map(s => `${s.id}@${versionOf(s)}`).join('|')
    const waiting = shown.some(s => digests.value[s.id]?.pending)
    if (!force && key === lastKey && !waiting && Date.now() - lastAt < RECHECK_MS) return
    inFlight = true
    try {
      const response = await fetchWithAuth('/api/agent/overview/digests', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ sessions: shown.map(s => ({ id: s.id, version: versionOf(s) })), generate: true })
      })
      if (!response.ok) return
      const result = await response.json()
      digests.value = { ...digests.value, ...(result.digests || {}) }
      lastKey = key
      lastAt = Date.now()
    } catch {
      /* Summaries are optional; the sidebar works without them. */
    } finally {
      inFlight = false
    }
  }

  /** Ask for what is already written for one session; never queues a new (paid) generation. */
  async function loadCached(session: DigestSession) {
    if (!session.id || digests.value[session.id]) return
    try {
      const response = await fetchWithAuth('/api/agent/overview/digests', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ sessions: [{ id: session.id, version: versionOf(session) }], generate: false })
      })
      if (!response.ok) return
      const result = await response.json()
      digests.value = { ...digests.value, ...(result.digests || {}) }
    } catch { /* optional */ }
  }

  /** Debounced: call whenever the session list changes. */
  function scheduleLoad(sessions: DigestSession[], delay = SESSION_DIGEST_DEBOUNCE_MS) {
    if (timer) clearTimeout(timer)
    timer = setTimeout(() => { timer = null; void load(sessions) }, delay)
  }

  const getDigest = (id: string): OverviewDigest | null => {
    const d = digests.value[id]
    return d && (d.summary || d.tags?.length) ? d : null
  }

  return { digests, load, loadCached, scheduleLoad, getDigest }
}

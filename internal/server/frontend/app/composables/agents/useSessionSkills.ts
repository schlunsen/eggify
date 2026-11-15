/**
 * Skills of each session (GET/PUT /api/agent/sessions/:id/skills), shared by
 * the right sidebar's Skills row and the Skills panel. Module-level cache: a
 * session's list is loaded when one of them asks for it, and reloaded when the
 * server says the session's skills changed (session_skills_updated, routed
 * here by useAgentWebSocket). Changing the list needs a signed-in user
 * (403 user_required shows the sign-in toast).
 */
import { reactive } from 'vue'
import { useAuthenticatedFetch } from '~/composables/useAuthenticatedFetch'
import { checkResponseForUserRequired } from '~/utils/userRequired'
import { nextAddedSkillIds, parseSessionSkills, type SessionSkillsState } from '~/utils/agents/sessionSkills'

export interface SessionSkillsCache {
  data: SessionSkillsState | null
  loaded: boolean
  loading: boolean
  saving: boolean
  error: string
}

export interface SetSkillsResult {
  ok: boolean
  error?: string
  userRequired?: boolean
}

type Fetcher = ReturnType<typeof useAuthenticatedFetch>['fetchWithAuth']

const states = reactive<Record<string, SessionSkillsCache>>({})
const inFlight = new Map<string, Promise<void>>()
// The fetcher of the first caller, for reloads triggered by the WebSocket
let sharedFetch: Fetcher | null = null

function ensureState(sessionId: string): SessionSkillsCache {
  if (!states[sessionId]) {
    states[sessionId] = { data: null, loaded: false, loading: false, saving: false, error: '' }
  }
  return states[sessionId]!
}

const skillsUrl = (sessionId: string) => `/api/agent/sessions/${encodeURIComponent(sessionId)}/skills`

function loadWith(fetchWithAuth: Fetcher, sessionId: string): Promise<void> {
  const running = inFlight.get(sessionId)
  if (running) return running
  const state = ensureState(sessionId)
  state.loading = true
  const job = (async () => {
    try {
      const res = await fetchWithAuth(skillsUrl(sessionId))
      if (!res.ok) {
        state.error = "Couldn't load this session's skills."
      } else {
        state.data = parseSessionSkills(await res.json().catch(() => null), sessionId)
        state.error = ''
      }
      state.loaded = true
    } catch {
      state.error = "Couldn't load this session's skills."
    } finally {
      state.loading = false
      inFlight.delete(sessionId)
    }
  })()
  inFlight.set(sessionId, job)
  return job
}

/**
 * The server changed a session's skills: reload its list if one was ever
 * asked for (the sidebar row or the panel shows it).
 */
export function refreshSessionSkills(sessionId: string | null | undefined): void {
  if (!sessionId || !states[sessionId] || !sharedFetch) return
  void loadWith(sharedFetch, sessionId)
}

export function useSessionSkills() {
  const { fetchWithAuth } = useAuthenticatedFetch()
  if (!sharedFetch) sharedFetch = fetchWithAuth

  /** The cached list (undefined until the session's list was asked for). */
  function stateFor(sessionId: string | null | undefined): SessionSkillsCache | undefined {
    return sessionId ? states[sessionId] : undefined
  }

  function load(sessionId: string | null | undefined): Promise<void> {
    if (!sessionId) return Promise.resolve()
    return loadWith(fetchWithAuth, sessionId)
  }

  /** Replace the session's added list (PUT sends the full list). */
  async function setAdded(sessionId: string, skillIds: string[]): Promise<SetSkillsResult> {
    const state = ensureState(sessionId)
    state.saving = true
    try {
      const res = await fetchWithAuth(skillsUrl(sessionId), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ skill_ids: skillIds }),
      })
      if (!res.ok) {
        const userRequired = await checkResponseForUserRequired(res)
        if (userRequired) return { ok: false, userRequired, error: "Only a signed-in user can change a session's skills." }
        let message = ''
        try { message = (await res.json())?.error || '' } catch { /* not JSON */ }
        return { ok: false, error: message || "Couldn't change this session's skills." }
      }
      state.data = parseSessionSkills(await res.json().catch(() => null), sessionId)
      state.loaded = true
      state.error = ''
      return { ok: true }
    } catch {
      return { ok: false, error: "Couldn't reach the server." }
    } finally {
      state.saving = false
    }
  }

  /** Add one skill to, or remove it from, the session's added list. */
  function toggle(sessionId: string, skillId: string, add: boolean): Promise<SetSkillsResult> {
    const skills = states[sessionId]?.data?.skills || []
    return setAdded(sessionId, nextAddedSkillIds(skills, skillId, add))
  }

  return { stateFor, load, setAdded, toggle }
}

/** Tests: forget every cached list. */
export function resetSessionSkills() {
  inFlight.clear()
  sharedFetch = null
  for (const key of Object.keys(states)) delete states[key]
}

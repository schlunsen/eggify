import { ref, computed } from 'vue'
import { useAuthenticatedFetch } from './useAuthenticatedFetch'
import { isDesktop } from '~/utils/desktop'

/**
 * Agent CLI onboarding state (GET /api/agent/runtimes and the login flows).
 *
 * Shared across the app (module-level refs) so the first-run wizard, the
 * "no agent is ready" nudge in Create Session and Settings → Agents & logins
 * all see the same status and the same running login.
 *
 * Starting, steering or cancelling a login needs a signed-in user; with only
 * the API key the server answers 403 user_required.
 */

export type RuntimeId = 'claude' | 'codex'
export type AuthState = 'logged_in' | 'not_logged_in' | 'unknown'
export type FlowState = 'starting' | 'waiting' | 'succeeded' | 'failed' | 'cancelled' | 'timed_out'

export interface RuntimeStep {
  kind: 'install' | 'update' | 'login' | 'add_to_path' | 'provider_key'
  text: string
  command?: string
}

export interface RuntimeStatus {
  id: RuntimeId
  name: string
  installed: boolean
  path?: string
  on_path: boolean
  version?: string
  min_version?: string
  recommended_version?: string
  version_ok: boolean
  meets_recommended: boolean
  auth: { state: AuthState, method?: string, detail?: string, plan?: string, account?: string }
  provider_keys?: string[]
  ready: boolean
  login_command: string
  install: { commands: string[], docs_url: string }
  next_steps: RuntimeStep[] | null
  error?: string
  checked_at: string
}

export interface LoginFlow {
  id: string
  runtime: RuntimeId
  mode: string
  state: FlowState
  auth_url?: string
  browser_opened: boolean
  accepts_code: boolean
  user_code?: string
  verification_url?: string
  message?: string
  started_at: string
  expires_at: string
}

export const ONBOARDING_SEEN_KEY = 'eggify-agent-onboarding-seen-v1'

export const flowDone = (s?: FlowState) => s === 'succeeded' || s === 'failed' || s === 'cancelled' || s === 'timed_out'

const runtimes = ref<RuntimeStatus[]>([])
const logins = ref<Partial<Record<RuntimeId, LoginFlow>>>({})
const loading = ref(false)
const loaded = ref(false)
const error = ref<string | null>(null)
const actionError = ref<Partial<Record<RuntimeId, string>>>({})
const busy = ref<Partial<Record<RuntimeId, boolean>>>({})
const wizardOpen = ref(false)
const wizardReason = ref<'first-run' | 'nudge' | 'settings' | 'manual'>('manual')
let pollTimer: ReturnType<typeof setInterval> | null = null

function readSeen(): boolean {
  try {
    return localStorage.getItem(ONBOARDING_SEEN_KEY) === '1'
  } catch {
    return false
  }
}

function writeSeen() {
  try {
    localStorage.setItem(ONBOARDING_SEEN_KEY, '1')
  } catch { /* private mode: show again next time */ }
}

/** Test hook: reset the shared state. */
export function __resetAgentRuntimesState() {
  runtimes.value = []
  logins.value = {}
  loading.value = false
  loaded.value = false
  error.value = null
  actionError.value = {}
  busy.value = {}
  wizardOpen.value = false
  if (pollTimer) clearInterval(pollTimer)
  pollTimer = null
}

export function useAgentRuntimes() {
  const { fetchWithAuth } = useAuthenticatedFetch()

  const anyReady = computed(() => runtimes.value.some(r => r.ready))
  const byId = (id: RuntimeId) => runtimes.value.find(r => r.id === id)

  const readError = async (res: Response, fallback: string) => {
    try {
      const body = await res.json()
      if (body?.code === 'user_required') return 'Signing an agent CLI in requires you to be signed in to Eggify.'
      return body?.error || fallback
    } catch {
      return fallback
    }
  }

  const load = async (refresh = false): Promise<RuntimeStatus[]> => {
    loading.value = true
    error.value = null
    try {
      const res = await fetchWithAuth(`/api/agent/runtimes${refresh ? '?refresh=1' : ''}`)
      if (!res.ok) {
        error.value = await readError(res, `Could not check the agent CLIs (${res.status})`)
        return runtimes.value
      }
      const body = await res.json()
      runtimes.value = body.runtimes || []
      const next: Partial<Record<RuntimeId, LoginFlow>> = { ...logins.value }
      for (const [id, flow] of Object.entries(body.logins || {})) next[id as RuntimeId] = flow as LoginFlow
      logins.value = next
      loaded.value = true
      if (Object.values(next).some(f => f && !flowDone(f.state))) ensurePolling()
      return runtimes.value
    } catch (e: any) {
      error.value = e?.message || 'Could not check the agent CLIs'
      return runtimes.value
    } finally {
      loading.value = false
    }
  }

  const setFlow = (id: RuntimeId, flow?: LoginFlow) => {
    logins.value = { ...logins.value, [id]: flow }
  }

  const pollOnce = async () => {
    const running = (Object.values(logins.value) as LoginFlow[]).filter(f => f && !flowDone(f.state))
    if (!running.length) {
      stopPolling()
      return
    }
    let finished = false
    for (const f of running) {
      try {
        const res = await fetchWithAuth(`/api/agent/runtimes/${f.runtime}/login`)
        if (!res.ok) continue
        const body = await res.json()
        setFlow(f.runtime, body.flow)
        if (flowDone(body.flow?.state)) finished = true
      } catch { /* keep polling */ }
    }
    if (finished) await load(true)
  }

  function ensurePolling() {
    if (pollTimer || typeof window === 'undefined') return
    pollTimer = setInterval(pollOnce, 1500)
  }

  function stopPolling() {
    if (pollTimer) clearInterval(pollTimer)
    pollTimer = null
  }

  const startLogin = async (id: RuntimeId, mode?: string): Promise<LoginFlow | null> => {
    busy.value = { ...busy.value, [id]: true }
    actionError.value = { ...actionError.value, [id]: undefined }
    try {
      const res = await fetchWithAuth(`/api/agent/runtimes/${id}/login`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(mode ? { mode } : {}),
      })
      if (!res.ok) {
        actionError.value = { ...actionError.value, [id]: await readError(res, `Could not start the login (${res.status})`) }
        return null
      }
      const body = await res.json()
      setFlow(id, body.flow)
      ensurePolling()
      return body.flow
    } catch (e: any) {
      actionError.value = { ...actionError.value, [id]: e?.message || 'Could not start the login' }
      return null
    } finally {
      busy.value = { ...busy.value, [id]: false }
    }
  }

  const cancelLogin = async (id: RuntimeId) => {
    busy.value = { ...busy.value, [id]: true }
    try {
      const res = await fetchWithAuth(`/api/agent/runtimes/${id}/login/cancel`, { method: 'POST' })
      if (res.ok) {
        const flow: LoginFlow | undefined = (await res.json()).flow
        // A cancelled login just returns the card to its normal state.
        setFlow(id, flow?.state === 'cancelled' ? undefined : flow)
      }
    } finally {
      busy.value = { ...busy.value, [id]: false }
    }
  }

  const submitCode = async (id: RuntimeId, code: string): Promise<boolean> => {
    actionError.value = { ...actionError.value, [id]: undefined }
    const res = await fetchWithAuth(`/api/agent/runtimes/${id}/login/code`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ code }),
    })
    if (!res.ok) {
      actionError.value = { ...actionError.value, [id]: await readError(res, 'Could not send the code') }
      return false
    }
    setFlow(id, (await res.json()).flow)
    return true
  }

  /** Clears a finished flow so the card shows the runtime's status again. */
  const dismissFlow = (id: RuntimeId) => {
    const f = logins.value[id]
    if (f && flowDone(f.state)) setFlow(id, undefined)
  }

  const openWizard = (reason: typeof wizardReason.value = 'manual') => {
    wizardReason.value = reason
    wizardOpen.value = true
    load(true)
  }

  const closeWizard = () => {
    wizardOpen.value = false
    writeSeen()
  }

  /** The desktop app shows the wizard once, on first startup. */
  const shouldShowFirstRun = () => isDesktop() && !readSeen()

  return {
    runtimes, logins, loading, loaded, error, actionError, busy, anyReady, byId,
    wizardOpen, wizardReason,
    load, startLogin, cancelLogin, submitCode, dismissFlow,
    openWizard, closeWizard, shouldShowFirstRun, stopPolling,
  }
}

import { copyText } from '~/utils/clipboard'
import { ref, onUnmounted } from 'vue'

export type TunnelState = 'disabled' | 'disconnected' | 'connecting' | 'connected' | 'error'

export type ProtectionMode = 'none' | 'oauth' | 'basic'

export interface TunnelProtection {
  mode: ProtectionMode
  oauth_provider?: string
  allow_emails?: string[]
  allow_domains?: string[]
  basic_user?: string
  basic_password_set?: boolean
}

/** Remote access settings as the server shows them: never the token itself. */
export interface TunnelSettingsView {
  token_set: boolean
  token_hint?: string
  token_source?: 'settings' | 'env'
  domain?: string
  env_domain?: string
  autostart: boolean
  protection: TunnelProtection
  load_error?: string
  /** An ngrok API key is saved (only its last 4 characters are ever sent). */
  api_key_set?: boolean
  api_key_hint?: string
  /** ngrok, or Eggify's own relay (end-to-end encrypted, for the iOS app). */
  mode?: TunnelMode
  /** The stored relay ("" = default_relay_addr). */
  relay_addr?: string
  default_relay_addr?: string
  relay_available?: boolean
  /** This install's certificate pin in relay mode (public; paired phones check it). */
  relay_pin?: string
}

export type TunnelMode = 'ngrok' | 'relay'

/** A connected ngrok agent (one of the account's simultaneous agent sessions). */
export interface NgrokAgentSession {
  id: string
  agent_version?: string
  ip?: string
  metadata?: string
  os?: string
  region?: string
  started_at?: string
  transport?: string
  /** Started by an Eggify (any install), with its host name. */
  eggify: boolean
  eggify_host?: string
  /** Started by this Eggify install; current: the session remote access uses now. */
  this_install: boolean
  current: boolean
}

/** ngrok's "account is limited to N simultaneous agent sessions" error. */
export const NGROK_AGENT_LIMIT = 'ERR_NGROK_108'

export function isAgentLimitError(s?: { error_code?: string, error?: string } | null): boolean {
  return !!s && (s.error_code === NGROK_AGENT_LIMIT || !!s.error?.includes(NGROK_AGENT_LIMIT))
}

export interface TunnelStatus {
  status: TunnelState
  provider: string
  public_url?: string
  domain?: string
  region?: string
  connected_since?: string
  error?: string
  error_code?: string
  message?: string
  next_retry_at?: string
  attempt?: number
  running?: boolean
  settings?: TunnelSettingsView
  can_start?: boolean
  blocked?: { code: string, message: string }
}

export interface TunnelSettingsUpdate {
  authtoken?: string
  domain?: string
  autostart?: boolean
  protection?: TunnelProtection & { basic_password?: string }
  mode?: TunnelMode
  relay_addr?: string
}

export interface TunnelActionResult {
  ok: boolean
  error?: string
  code?: string
}

export function useTunnel() {
  const status = ref<TunnelStatus | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)
  const urlCopied = ref(false)
  const busy = ref(false)

  let pollTimer: ReturnType<typeof setTimeout> | null = null
  let pollMs = 5000
  let polling = false

  const { fetchWithAuth } = useAuthenticatedFetch()

  // Fetch tunnel status
  async function fetchStatus() {
    loading.value = true
    error.value = null
    try {
      const response = await fetchWithAuth('/api/tunnel/status', {
        method: 'GET',
      })

      if (response.ok) {
        const data = await response.json()
        status.value = data as TunnelStatus
      } else {
        error.value = `Failed to fetch tunnel status: ${response.statusText}`
      }
    } catch (err: any) {
      error.value = err.message || 'Failed to fetch tunnel status'
    } finally {
      loading.value = false
    }
  }

  async function act(method: string, path: string, body?: unknown): Promise<TunnelActionResult> {
    busy.value = true
    try {
      const response = await fetchWithAuth(path, {
        method,
        headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
        body: body !== undefined ? JSON.stringify(body) : undefined,
      })
      let data: any = null
      try { data = await response.json() } catch { /* empty body */ }
      if (!response.ok) {
        return { ok: false, error: data?.error || response.statusText || 'Request failed', code: data?.code }
      }
      // start/stop/token return the status; settings wrap it in `tunnel`.
      const next = data?.tunnel ?? data
      if (next && typeof next.status === 'string') status.value = next as TunnelStatus
      return { ok: true }
    } catch (err: any) {
      return { ok: false, error: err?.message || 'Request failed' }
    } finally {
      busy.value = false
      schedule()
    }
  }

  const saveSettings = (u: TunnelSettingsUpdate) => act('PUT', '/api/tunnel/settings', u)
  const start = () => act('POST', '/api/tunnel/start')
  const stop = () => act('POST', '/api/tunnel/stop')
  const deleteToken = () => act('DELETE', '/api/tunnel/token')
  const setApiKey = (apiKey: string) => act('PUT', '/api/tunnel/api-key', { api_key: apiKey })
  const deleteApiKey = () => act('DELETE', '/api/tunnel/api-key')

  /** Lists the account's connected ngrok agents (needs the API key). */
  async function listAgentSessions(): Promise<TunnelActionResult & { sessions?: NgrokAgentSession[] }> {
    try {
      const response = await fetchWithAuth('/api/tunnel/sessions', { method: 'GET' })
      let data: any = null
      try { data = await response.json() } catch { /* empty body */ }
      if (!response.ok) return { ok: false, error: data?.error || response.statusText || 'Request failed', code: data?.code }
      return { ok: true, sessions: (data?.sessions ?? []) as NgrokAgentSession[] }
    } catch (err: any) {
      return { ok: false, error: err?.message || 'Request failed' }
    }
  }

  /** Asks ngrok to stop one agent session. */
  async function stopAgentSession(id: string): Promise<TunnelActionResult> {
    try {
      const response = await fetchWithAuth(`/api/tunnel/sessions/${encodeURIComponent(id)}/stop`, { method: 'POST' })
      let data: any = null
      try { data = await response.json() } catch { /* empty body */ }
      if (!response.ok) return { ok: false, error: data?.error || response.statusText || 'Request failed', code: data?.code }
      return { ok: true }
    } catch (err: any) {
      return { ok: false, error: err?.message || 'Request failed' }
    }
  }

  // Copy public URL to clipboard
  async function copyUrl() {
    if (!status.value?.public_url) return
    try {
      await copyText(status.value.public_url)
      urlCopied.value = true
      setTimeout(() => {
        urlCopied.value = false
      }, 2000)
    } catch (err) {
      console.error('Failed to copy URL:', err)
    }
  }

  // Poll faster while a connection attempt is in flight.
  function schedule() {
    if (!polling) return
    if (pollTimer) clearTimeout(pollTimer)
    const ms = status.value?.status === 'connecting' ? Math.min(pollMs, 1500) : pollMs
    pollTimer = setTimeout(async () => {
      await fetchStatus()
      schedule()
    }, ms)
  }

  // Auto-poll with interval
  function startPolling(intervalMs = 5000) {
    stopPolling()
    pollMs = intervalMs
    polling = true
    fetchStatus().then(schedule)
  }

  function stopPolling() {
    polling = false
    if (pollTimer) {
      clearTimeout(pollTimer)
      pollTimer = null
    }
  }

  // Auto-cleanup on unmount
  onUnmounted(() => {
    stopPolling()
  })

  return {
    status, loading, error, urlCopied, busy,
    fetchStatus, copyUrl, startPolling, stopPolling,
    saveSettings, start, stop, deleteToken,
    setApiKey, deleteApiKey, listAgentSessions, stopAgentSession,
  }
}

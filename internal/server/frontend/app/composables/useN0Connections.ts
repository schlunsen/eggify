import { ref, computed } from 'vue'
import { useAuthenticatedFetch } from './useAuthenticatedFetch'
import { desktopOpenExternal, desktopShell, isDesktop } from '../utils/desktop'

/**
 * n0 connector over MCP + OAuth.
 *
 * Wraps the /api/connectors/n0 REST endpoints: listing OAuth connections,
 * the browser-based connect flow (start → open authorize URL → poll status),
 * disconnect / reconnect, per-session binding and the legacy API key.
 */

export type N0ConnectionStatus = 'active' | 'needs_reauth' | 'revoked'

export interface N0Connection {
  id: number
  host: string
  workspace_name: string
  app_subdomain: string
  app_name: string
  status: N0ConnectionStatus | string
  status_message: string
  scopes: string[]
  last_used_at: string | null
  created_at: string
  connected_tools_url: string
}

export interface N0Host {
  id: string
  label: string
  url: string
  default?: boolean
}

export interface N0Legacy {
  configured: boolean
  api_key_masked?: string
  domain?: string
}

export type N0RedirectMode = 'auto' | 'loopback' | 'public'

export type N0ConnectPhase = 'idle' | 'starting' | 'waiting' | 'connected' | 'error'

export interface N0ConnectFlow {
  phase: N0ConnectPhase
  state?: string
  reconnectId?: number
  connectionId?: number
  authorizeUrl?: string
  error?: string
  errorCode?: string
  hint?: string
}

export interface N0StartOptions {
  host: string
  reconnectId?: number
  redirectMode?: N0RedirectMode
  returnTo?: string
}

export interface N0SessionBinding {
  enabled: boolean
  connection_id: number | null
  connection: N0Connection | null
}

export interface UseN0ConnectionsOptions {
  /** Called once a connect/reconnect flow finishes successfully (after the list was refreshed). */
  onConnected?: (connectionId: number | undefined, connection: N0Connection | null) => void
  /** Called when a connect/reconnect flow fails. */
  onError?: (flow: N0ConnectFlow) => void
  /** Override how the authorize URL is opened (tests). */
  openUrl?: (url: string, preopened: Window | null) => void | Promise<void>
}

export const N0_POLL_INTERVAL_MS = 2000
export const N0_POLL_TIMEOUT_MS = 10 * 60 * 1000

/** Fallback host list, used until GET /api/connectors/n0 answered. */
export const N0_DEFAULT_HOSTS: N0Host[] = [
  { id: 'nzero', label: 'n0', url: 'https://nzero.pro', default: true },
]

const ts = (s: string | null | undefined): number => {
  if (!s) return 0
  const t = new Date(s).getTime()
  return Number.isFinite(t) ? t : 0
}

/**
 * Default connection for a new session: the active connection with the most
 * recent last_used_at; when none was used yet, the newest created_at.
 */
export function pickDefaultN0Connection(connections: N0Connection[] | null | undefined): N0Connection | null {
  const active = (connections || []).filter(c => c.status === 'active')
  if (active.length === 0) return null
  const used = active.filter(c => ts(c.last_used_at) > 0)
  if (used.length > 0) {
    return used.reduce((best, c) => (ts(c.last_used_at) > ts(best.last_used_at) ? c : best))
  }
  return active.reduce((best, c) => (ts(c.created_at) > ts(best.created_at) ? c : best))
}

/** Short human label for a connection: app subdomain, else app name, else a placeholder. */
export function n0ConnectionLabel(c: Pick<N0Connection, 'app_subdomain' | 'app_name'> | null | undefined): string {
  if (!c) return ''
  return c.app_subdomain || c.app_name || 'New app (not created yet)'
}

/** Host without scheme, e.g. "nzero.pro". */
export function n0HostLabel(host: string): string {
  return (host || '').replace(/^https?:\/\//, '').replace(/\/+$/, '')
}

/** Validates a custom host URL (must be https://…). Returns an error message or null. */
export function validateN0CustomHost(url: string): string | null {
  const v = (url || '').trim()
  if (!v) return 'Enter the n0 host URL'
  let parsed: URL
  try {
    parsed = new URL(v)
  } catch {
    return 'Not a valid URL'
  }
  if (parsed.protocol !== 'https:') return 'The n0 host must use https://'
  return null
}

/** True inside a desktop shell (Wails or Tauri) that opens links in the system browser. */
export function isTauriApp(): boolean {
  return isDesktop()
}

/**
 * Opens an external URL: in the desktop app (Wails or Tauri) in the system
 * browser, otherwise in a new tab. `preopened` is a blank window
 * opened synchronously in the click handler so popup blockers don't eat it.
 */
export async function openN0ExternalUrl(url: string, preopened: Window | null = null): Promise<void> {
  if (isDesktop()) {
    try {
      if (await desktopOpenExternal(url)) return
    } catch (err) {
      console.error('[n0] Failed to open URL in system browser:', err)
    }
  }
  if (preopened && !preopened.closed) {
    preopened.location.href = url
    return
  }
  window.open(url, '_blank')
}

async function readError(response: Response): Promise<{ error: string; error_code?: string; hint?: string }> {
  try {
    const data = await response.json()
    return {
      error: data?.error || `HTTP ${response.status}${response.statusText ? `: ${response.statusText}` : ''}`,
      error_code: data?.error_code,
      hint: data?.hint,
    }
  } catch {
    return { error: `HTTP ${response.status}${response.statusText ? `: ${response.statusText}` : ''}` }
  }
}

export function useN0Connections(options: UseN0ConnectionsOptions = {}) {
  const { fetchWithAuth } = useAuthenticatedFetch()

  const hosts = ref<N0Host[]>([...N0_DEFAULT_HOSTS])
  const connections = ref<N0Connection[]>([])
  const legacy = ref<N0Legacy>({ configured: false })
  const browserIsLocal = ref(true)
  const loading = ref(false)
  const loaded = ref(false)
  const error = ref('')
  const busyIds = ref<number[]>([])
  const flow = ref<N0ConnectFlow>({ phase: 'idle' })

  let pollTimer: ReturnType<typeof setTimeout> | null = null
  let pollStartedAt = 0
  let pollToken = 0

  const defaultConnection = computed(() => pickDefaultN0Connection(connections.value))
  const defaultHost = computed(() => hosts.value.find(h => h.default) || hosts.value[0] || N0_DEFAULT_HOSTS[0]!)
  const isConnecting = computed(() => flow.value.phase === 'starting' || flow.value.phase === 'waiting')

  const fetchConnections = async (): Promise<boolean> => {
    loading.value = true
    error.value = ''
    try {
      const response = await fetchWithAuth('/api/connectors/n0', { method: 'GET' })
      if (!response.ok) {
        error.value = (await readError(response)).error
        return false
      }
      const data = await response.json()
      if (Array.isArray(data?.hosts) && data.hosts.length > 0) hosts.value = data.hosts
      connections.value = Array.isArray(data?.connections) ? data.connections : []
      legacy.value = data?.legacy || { configured: false }
      browserIsLocal.value = data?.browser_is_local !== false
      loaded.value = true
      return true
    } catch (err: any) {
      error.value = err?.message || 'Failed to load n0 connections'
      return false
    } finally {
      loading.value = false
    }
  }

  const stopPolling = () => {
    pollToken++
    if (pollTimer) {
      clearTimeout(pollTimer)
      pollTimer = null
    }
  }

  const fail = (patch: Omit<N0ConnectFlow, 'phase'>) => {
    stopPolling()
    flow.value = { ...flow.value, ...patch, phase: 'error' }
    options.onError?.(flow.value)
  }

  const pollOnce = async (state: string, token: number): Promise<void> => {
    if (token !== pollToken) return
    if (Date.now() - pollStartedAt >= N0_POLL_TIMEOUT_MS) {
      fail({ error: 'Timed out waiting for n0 authorization. Start the connection again.' })
      return
    }
    let data: any = null
    try {
      const response = await fetchWithAuth(`/api/connectors/n0/oauth/status?state=${encodeURIComponent(state)}`, { method: 'GET' })
      if (response.ok) data = await response.json()
    } catch {
      // transient network error — keep polling
    }
    if (token !== pollToken) return

    const status = data?.status
    if (status === 'connected') {
      stopPolling()
      flow.value = { ...flow.value, phase: 'connected', connectionId: data.connection_id }
      await fetchConnections()
      const conn = connections.value.find(c => c.id === data.connection_id) || null
      options.onConnected?.(data.connection_id, conn)
      return
    }
    if (status === 'error') {
      fail({
        error: data.error_description || data.error || 'n0 authorization failed',
        errorCode: data.error,
        hint: data.hint,
      })
      return
    }
    if (status === 'unknown') {
      fail({ error: 'The n0 authorization request expired or is unknown (the server may have restarted). Start the connection again.', errorCode: 'unknown_state' })
      return
    }
    // pending (or unreadable response): poll again
    pollTimer = setTimeout(() => { void pollOnce(state, token) }, N0_POLL_INTERVAL_MS)
  }

  /**
   * Starts the OAuth connect flow. Call it directly from a click handler so the
   * browser popup can be opened synchronously.
   */
  const startConnect = async (opts: N0StartOptions): Promise<boolean> => {
    stopPolling()
    flow.value = { phase: 'starting', reconnectId: opts.reconnectId }

    // Open the tab now (still inside the user gesture) and navigate it once we
    // have the authorize URL. Not needed in Tauri (system browser) or when the
    // caller supplies its own opener.
    let preopened: Window | null = null
    if (!options.openUrl && !isTauriApp() && typeof window !== 'undefined') {
      try {
        preopened = window.open('', '_blank')
        // Don't hand the n0 page a handle back to Eggify.
        if (preopened) preopened.opener = null
      } catch { preopened = null }
    }
    const closePreopened = () => { try { preopened?.close() } catch { /* ignore */ } }

    const body: Record<string, unknown> = {
      host: opts.host,
      // The consent page opens in the system browser. From the desktop app,
      // send it back to the app (deep link): the browser has no session on the
      // app's loopback server and would land on a login page.
      return_to: opts.returnTo ?? (desktopShell() === 'wails' ? 'eggify://connectors' : typeof window !== 'undefined' ? `${window.location.origin}/connectors` : undefined),
      redirect_mode: opts.redirectMode || 'auto',
    }
    if (opts.reconnectId != null) body.reconnect_id = opts.reconnectId

    let data: any
    try {
      const response = await fetchWithAuth('/api/connectors/n0/oauth/start', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (!response.ok) {
        closePreopened()
        const err = await readError(response)
        fail({ error: err.error, errorCode: err.error_code, hint: err.hint })
        return false
      }
      data = await response.json()
    } catch (err: any) {
      closePreopened()
      fail({ error: err?.message || 'Failed to start n0 connection' })
      return false
    }

    if (!data?.authorize_url || !data?.state) {
      closePreopened()
      fail({ error: 'The server did not return an authorization URL' })
      return false
    }

    flow.value = { phase: 'waiting', state: data.state, reconnectId: opts.reconnectId, authorizeUrl: data.authorize_url }
    try {
      await (options.openUrl || openN0ExternalUrl)(data.authorize_url, preopened)
    } catch (err) {
      console.error('[n0] Failed to open authorize URL:', err)
    }

    pollStartedAt = Date.now()
    const token = ++pollToken
    pollTimer = setTimeout(() => { void pollOnce(data.state, token) }, N0_POLL_INTERVAL_MS)
    return true
  }

  const reconnect = (conn: N0Connection, opts: Omit<N0StartOptions, 'host' | 'reconnectId'> = {}) =>
    startConnect({ ...opts, host: conn.host, reconnectId: conn.id })

  const cancelConnect = () => {
    stopPolling()
    flow.value = { phase: 'idle' }
  }

  const resetFlow = () => {
    if (!isConnecting.value) flow.value = { phase: 'idle' }
  }

  const withBusy = async <T>(id: number, fn: () => Promise<T>): Promise<T> => {
    busyIds.value = [...busyIds.value, id]
    try {
      return await fn()
    } finally {
      busyIds.value = busyIds.value.filter(x => x !== id)
    }
  }

  /** Revokes (best effort at n0) and deletes a connection. */
  const disconnect = (id: number): Promise<{ ok: boolean; revokedAtN0?: boolean; error?: string }> =>
    withBusy(id, async () => {
      try {
        const response = await fetchWithAuth(`/api/connectors/n0/connections/${id}`, { method: 'DELETE' })
        if (!response.ok) {
          return { ok: false, error: (await readError(response)).error }
        }
        let revoked: boolean | undefined
        try { revoked = (await response.json())?.revoked_at_n0 } catch { /* empty body */ }
        connections.value = connections.value.filter(c => c.id !== id)
        return { ok: true, revokedAtN0: revoked }
      } catch (err: any) {
        return { ok: false, error: err?.message || 'Failed to disconnect' }
      }
    })

  /** Removes the legacy API key connection. */
  const removeLegacy = async (): Promise<{ ok: boolean; error?: string }> => {
    try {
      const response = await fetchWithAuth('/api/connectors/n0/disconnect', { method: 'DELETE' })
      if (!response.ok) return { ok: false, error: (await readError(response)).error }
      legacy.value = { configured: false }
      return { ok: true }
    } catch (err: any) {
      return { ok: false, error: err?.message || 'Failed to remove legacy key' }
    }
  }

  const getSessionBinding = async (sessionId: string): Promise<N0SessionBinding | null> => {
    try {
      const response = await fetchWithAuth(`/api/connectors/n0/sessions/${encodeURIComponent(sessionId)}`, { method: 'GET' })
      if (!response.ok) return null
      return await response.json()
    } catch {
      return null
    }
  }

  /** Binds a connection to a session (null disables n0 for the session). */
  const setSessionConnection = async (sessionId: string, connectionId: number | null): Promise<{ ok: boolean; binding?: N0SessionBinding; error?: string }> => {
    try {
      const response = await fetchWithAuth(`/api/connectors/n0/sessions/${encodeURIComponent(sessionId)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ connection_id: connectionId }),
      })
      if (!response.ok) return { ok: false, error: (await readError(response)).error }
      return { ok: true, binding: await response.json() }
    } catch (err: any) {
      return { ok: false, error: err?.message || 'Failed to update n0 connection' }
    }
  }

  const isBusy = (id: number) => busyIds.value.includes(id)

  return {
    hosts,
    connections,
    legacy,
    browserIsLocal,
    loading,
    loaded,
    error,
    flow,
    defaultConnection,
    defaultHost,
    isConnecting,
    fetchConnections,
    startConnect,
    reconnect,
    cancelConnect,
    resetFlow,
    stopPolling,
    disconnect,
    removeLegacy,
    getSessionBinding,
    setSessionConnection,
    isBusy,
  }
}

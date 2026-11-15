import {
  useN0Connections,
  pickDefaultN0Connection,
  validateN0CustomHost,
  n0ConnectionLabel,
  N0_POLL_INTERVAL_MS,
  N0_POLL_TIMEOUT_MS,
  type N0Connection,
} from '../../composables/useN0Connections'

const { fetchWithAuth } = vi.hoisted(() => ({ fetchWithAuth: vi.fn() }))
vi.mock('../../composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => ({ fetchWithAuth }) }))

const json = (body: unknown, status = 200) => ({
  ok: status >= 200 && status < 300,
  status,
  statusText: status === 200 ? 'OK' : 'Error',
  json: async () => body,
})

const conn = (over: Partial<N0Connection> = {}): N0Connection => ({
  id: 1,
  host: 'https://nzero.pro',
  workspace_name: 'Acme',
  app_subdomain: 'shop',
  app_name: 'Shop',
  status: 'active',
  status_message: '',
  scopes: ['mcp'],
  last_used_at: null,
  created_at: '2026-09-01T10:00:00Z',
  connected_tools_url: 'https://nzero.pro/settings/security/connected-tools',
  ...over,
})

const listResponse = (connections: N0Connection[], extra: Record<string, unknown> = {}) => json({
  hosts: [
    { id: 'nzero', label: 'n0', url: 'https://nzero.pro', default: true },
    { id: 'other', label: 'Other', url: 'https://n0.example.com' },
  ],
  connections,
  legacy: { configured: false },
  browser_is_local: true,
  ...extra,
})

/** Routes fetchWithAuth calls by method + path prefix. */
function routeFetch(routes: Record<string, (url: string, init?: any) => any>) {
  fetchWithAuth.mockImplementation(async (url: string, init?: any) => {
    const method = init?.method || 'GET'
    const key = Object.keys(routes).find(k => {
      const [m, p] = k.split(' ')
      return m === method && url.startsWith(p!)
    })
    if (!key) throw new Error(`unexpected fetch ${method} ${url}`)
    return routes[key]!(url, init)
  })
}

const callsTo = (method: string, prefix: string) =>
  fetchWithAuth.mock.calls.filter(([url, init]) => (init?.method || 'GET') === method && String(url).startsWith(prefix))

beforeEach(() => {
  fetchWithAuth.mockReset()
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('useN0Connections — connect flow', () => {
  it('start → open authorize URL → poll pending → connected refreshes the list', async () => {
    const statuses = ['pending', 'connected']
    let connected = false
    routeFetch({
      'POST /api/connectors/n0/oauth/start': () => json({
        authorize_url: 'https://nzero.pro/oauth/authorize?x=1', state: 'st-1', redirect_uri: 'https://eggify/cb', expires_in: 600,
      }),
      'GET /api/connectors/n0/oauth/status': () => {
        const status = statuses.shift()
        if (status === 'connected') connected = true
        return json(status === 'connected' ? { status, connection_id: 7 } : { status })
      },
      'GET /api/connectors/n0': () => listResponse(connected ? [conn({ id: 7, app_subdomain: 'blog' })] : []),
    })
    const openUrl = vi.fn()
    const onConnected = vi.fn()
    const n0 = useN0Connections({ openUrl, onConnected })

    const ok = await n0.startConnect({ host: 'https://nzero.pro', returnTo: 'https://eggify.local/connectors' })
    expect(ok).toBe(true)
    expect(openUrl).toHaveBeenCalledWith('https://nzero.pro/oauth/authorize?x=1', null)
    expect(n0.flow.value.phase).toBe('waiting')
    expect(n0.isConnecting.value).toBe(true)

    const body = JSON.parse(callsTo('POST', '/api/connectors/n0/oauth/start')[0]![1].body)
    expect(body).toEqual({ host: 'https://nzero.pro', return_to: 'https://eggify.local/connectors', redirect_mode: 'auto' })

    await vi.advanceTimersByTimeAsync(N0_POLL_INTERVAL_MS)
    expect(n0.flow.value.phase).toBe('waiting')
    expect(callsTo('GET', '/api/connectors/n0/oauth/status')[0]![0]).toBe('/api/connectors/n0/oauth/status?state=st-1')

    await vi.advanceTimersByTimeAsync(N0_POLL_INTERVAL_MS)
    expect(n0.flow.value.phase).toBe('connected')
    expect(n0.flow.value.connectionId).toBe(7)
    expect(n0.connections.value.map(c => c.id)).toEqual([7])
    expect(onConnected).toHaveBeenCalledWith(7, expect.objectContaining({ app_subdomain: 'blog' }))

    // polling stopped
    await vi.advanceTimersByTimeAsync(N0_POLL_INTERVAL_MS * 3)
    expect(callsTo('GET', '/api/connectors/n0/oauth/status')).toHaveLength(2)
  })

  it('surfaces invalid_redirect_uri verbatim with the server hint and does not poll', async () => {
    routeFetch({
      'POST /api/connectors/n0/oauth/start': () => json({
        error: 'redirect_uri https://eggify.example/api/connectors/n0/oauth/callback is not allowed by n0',
        error_code: 'invalid_redirect_uri',
        hint: 'Ask the n0 admin to add https://eggify.example/ to OAUTH_DCR_EXTRA_REDIRECT_PREFIXES.',
      }, 400),
    })
    const openUrl = vi.fn()
    const onError = vi.fn()
    const n0 = useN0Connections({ openUrl, onError })

    const ok = await n0.startConnect({ host: 'https://nzero.pro', redirectMode: 'loopback' })
    expect(ok).toBe(false)
    expect(openUrl).not.toHaveBeenCalled()
    expect(n0.flow.value).toMatchObject({
      phase: 'error',
      errorCode: 'invalid_redirect_uri',
      error: 'redirect_uri https://eggify.example/api/connectors/n0/oauth/callback is not allowed by n0',
      hint: 'Ask the n0 admin to add https://eggify.example/ to OAUTH_DCR_EXTRA_REDIRECT_PREFIXES.',
    })
    expect(onError).toHaveBeenCalledTimes(1)
    expect(JSON.parse(callsTo('POST', '/api/connectors/n0/oauth/start')[0]![1].body).redirect_mode).toBe('loopback')

    await vi.advanceTimersByTimeAsync(N0_POLL_INTERVAL_MS * 3)
    expect(callsTo('GET', '/api/connectors/n0/oauth/status')).toHaveLength(0)
  })

  it('stops polling and reports the error when the status endpoint returns error', async () => {
    routeFetch({
      'POST /api/connectors/n0/oauth/start': () => json({ authorize_url: 'https://n0/a', state: 'st-2', redirect_uri: 'x', expires_in: 600 }),
      'GET /api/connectors/n0/oauth/status': () => json({
        status: 'error', error: 'access_denied', error_description: 'The user denied the request', hint: 'Try again',
      }),
    })
    const onError = vi.fn()
    const n0 = useN0Connections({ openUrl: vi.fn(), onError })
    await n0.startConnect({ host: 'https://nzero.pro' })

    await vi.advanceTimersByTimeAsync(N0_POLL_INTERVAL_MS)
    expect(n0.flow.value).toMatchObject({ phase: 'error', error: 'The user denied the request', errorCode: 'access_denied', hint: 'Try again' })
    expect(onError).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(N0_POLL_INTERVAL_MS * 3)
    expect(callsTo('GET', '/api/connectors/n0/oauth/status')).toHaveLength(1)
  })

  it('treats an unknown state as an error', async () => {
    routeFetch({
      'POST /api/connectors/n0/oauth/start': () => json({ authorize_url: 'https://n0/a', state: 'st-3', redirect_uri: 'x', expires_in: 600 }),
      'GET /api/connectors/n0/oauth/status': () => json({ status: 'unknown' }),
    })
    const n0 = useN0Connections({ openUrl: vi.fn() })
    await n0.startConnect({ host: 'https://nzero.pro' })
    await vi.advanceTimersByTimeAsync(N0_POLL_INTERVAL_MS)
    expect(n0.flow.value.phase).toBe('error')
    expect(n0.flow.value.errorCode).toBe('unknown_state')
  })

  it('gives up after the poll timeout', async () => {
    routeFetch({
      'POST /api/connectors/n0/oauth/start': () => json({ authorize_url: 'https://n0/a', state: 'st-4', redirect_uri: 'x', expires_in: 600 }),
      'GET /api/connectors/n0/oauth/status': () => json({ status: 'pending' }),
    })
    const n0 = useN0Connections({ openUrl: vi.fn() })
    await n0.startConnect({ host: 'https://nzero.pro' })
    await vi.advanceTimersByTimeAsync(N0_POLL_TIMEOUT_MS + N0_POLL_INTERVAL_MS * 2)
    expect(n0.flow.value.phase).toBe('error')
    expect(n0.flow.value.error).toMatch(/Timed out/)
    const count = callsTo('GET', '/api/connectors/n0/oauth/status').length
    await vi.advanceTimersByTimeAsync(N0_POLL_INTERVAL_MS * 3)
    expect(callsTo('GET', '/api/connectors/n0/oauth/status')).toHaveLength(count)
  })

  it('cancel stops polling', async () => {
    routeFetch({
      'POST /api/connectors/n0/oauth/start': () => json({ authorize_url: 'https://n0/a', state: 'st-5', redirect_uri: 'x', expires_in: 600 }),
      'GET /api/connectors/n0/oauth/status': () => json({ status: 'pending' }),
    })
    const n0 = useN0Connections({ openUrl: vi.fn() })
    await n0.startConnect({ host: 'https://nzero.pro' })
    n0.cancelConnect()
    await vi.advanceTimersByTimeAsync(N0_POLL_INTERVAL_MS * 3)
    expect(callsTo('GET', '/api/connectors/n0/oauth/status')).toHaveLength(0)
    expect(n0.flow.value.phase).toBe('idle')
  })

  it('reconnect sends reconnect_id and the connection host', async () => {
    routeFetch({
      'POST /api/connectors/n0/oauth/start': () => json({ authorize_url: 'https://n0/a', state: 'st-6', redirect_uri: 'x', expires_in: 600 }),
      'GET /api/connectors/n0/oauth/status': () => json({ status: 'pending' }),
    })
    const n0 = useN0Connections({ openUrl: vi.fn() })
    await n0.reconnect(conn({ id: 42, host: 'https://n0.example.com', status: 'needs_reauth' }), { returnTo: 'https://eggify/connectors' })
    const body = JSON.parse(callsTo('POST', '/api/connectors/n0/oauth/start')[0]![1].body)
    expect(body).toMatchObject({ host: 'https://n0.example.com', reconnect_id: 42 })
    expect(n0.flow.value).toMatchObject({ phase: 'waiting', reconnectId: 42 })
    n0.cancelConnect()
  })
})

describe('useN0Connections — list & disconnect', () => {
  it('loads hosts, connections, legacy and browser locality', async () => {
    routeFetch({
      'GET /api/connectors/n0': () => listResponse([conn()], {
        legacy: { configured: true, api_key_masked: 'n0_…abcd', domain: 'nzero.pro' },
        browser_is_local: false,
      }),
    })
    const n0 = useN0Connections()
    expect(await n0.fetchConnections()).toBe(true)
    expect(n0.hosts.value).toHaveLength(2)
    expect(n0.defaultHost.value.url).toBe('https://nzero.pro')
    expect(n0.connections.value).toHaveLength(1)
    expect(n0.legacy.value.configured).toBe(true)
    expect(n0.browserIsLocal.value).toBe(false)
  })

  it('disconnect removes the row', async () => {
    routeFetch({
      'GET /api/connectors/n0': () => listResponse([conn({ id: 1 }), conn({ id: 2, app_subdomain: 'blog' })]),
      'DELETE /api/connectors/n0/connections/': () => json({ status: 'success', revoked_at_n0: true }),
    })
    const n0 = useN0Connections()
    await n0.fetchConnections()
    const result = await n0.disconnect(1)
    expect(result).toEqual({ ok: true, revokedAtN0: true })
    expect(callsTo('DELETE', '/api/connectors/n0/connections/')[0]![0]).toBe('/api/connectors/n0/connections/1')
    expect(n0.connections.value.map(c => c.id)).toEqual([2])
    expect(n0.isBusy(1)).toBe(false)
  })

  it('failed disconnect keeps the row and returns the error', async () => {
    routeFetch({
      'GET /api/connectors/n0': () => listResponse([conn({ id: 1 })]),
      'DELETE /api/connectors/n0/connections/': () => json({ error: 'boom' }, 500),
    })
    const n0 = useN0Connections()
    await n0.fetchConnections()
    expect(await n0.disconnect(1)).toEqual({ ok: false, error: 'boom' })
    expect(n0.connections.value).toHaveLength(1)
  })

  it('setSessionConnection PUTs the connection id (null disables)', async () => {
    routeFetch({
      'PUT /api/connectors/n0/sessions/': (_url, init) => {
        const { connection_id } = JSON.parse(init.body)
        return json({ enabled: connection_id !== null, connection_id, connection: null })
      },
    })
    const n0 = useN0Connections()
    const on = await n0.setSessionConnection('sess-1', 3)
    expect(on).toMatchObject({ ok: true, binding: { enabled: true, connection_id: 3 } })
    const off = await n0.setSessionConnection('sess-1', null)
    expect(off).toMatchObject({ ok: true, binding: { enabled: false, connection_id: null } })
    expect(callsTo('PUT', '/api/connectors/n0/sessions/')[0]![0]).toBe('/api/connectors/n0/sessions/sess-1')
  })
})

describe('pickDefaultN0Connection', () => {
  it('prefers the active connection used most recently', () => {
    const picked = pickDefaultN0Connection([
      conn({ id: 1, last_used_at: '2026-09-20T10:00:00Z' }),
      conn({ id: 2, last_used_at: '2026-09-24T10:00:00Z', status: 'needs_reauth' }),
      conn({ id: 3, last_used_at: '2026-09-22T10:00:00Z' }),
      conn({ id: 4, last_used_at: null, created_at: '2026-09-25T10:00:00Z' }),
    ])
    expect(picked?.id).toBe(3)
  })

  it('falls back to the newest created_at when nothing was used yet', () => {
    const picked = pickDefaultN0Connection([
      conn({ id: 1, created_at: '2026-09-01T10:00:00Z' }),
      conn({ id: 2, created_at: '2026-09-10T10:00:00Z' }),
      conn({ id: 3, created_at: '2026-09-20T10:00:00Z', status: 'revoked' }),
    ])
    expect(picked?.id).toBe(2)
  })

  it('returns null without active connections', () => {
    expect(pickDefaultN0Connection([conn({ status: 'revoked' })])).toBeNull()
    expect(pickDefaultN0Connection([])).toBeNull()
  })

  it('is exposed as defaultConnection on the composable', async () => {
    routeFetch({
      'GET /api/connectors/n0': () => listResponse([
        conn({ id: 1, last_used_at: '2026-09-20T10:00:00Z' }),
        conn({ id: 2, last_used_at: '2026-09-21T10:00:00Z' }),
      ]),
    })
    const n0 = useN0Connections()
    await n0.fetchConnections()
    expect(n0.defaultConnection.value?.id).toBe(2)
  })
})

describe('helpers', () => {
  it('labels connections by subdomain, name, then placeholder', () => {
    expect(n0ConnectionLabel({ app_subdomain: 'shop', app_name: 'Shop' })).toBe('shop')
    expect(n0ConnectionLabel({ app_subdomain: '', app_name: 'Shop' })).toBe('Shop')
    expect(n0ConnectionLabel({ app_subdomain: '', app_name: '' })).toBe('New app (not created yet)')
  })

  it('requires https custom hosts', () => {
    expect(validateN0CustomHost('https://n0.example.com')).toBeNull()
    expect(validateN0CustomHost('http://n0.example.com')).toMatch(/https/)
    expect(validateN0CustomHost('not a url')).toBeTruthy()
    expect(validateN0CustomHost('')).toBeTruthy()
  })
})

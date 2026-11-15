import { useN0Bindings, N0_BINDINGS_DEBOUNCE_MS } from '../../composables/useN0Bindings'

const { fetchWithAuth } = vi.hoisted(() => ({ fetchWithAuth: vi.fn() }))
vi.mock('../../composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => ({ fetchWithAuth }) }))

beforeEach(() => {
  fetchWithAuth.mockReset()
  fetchWithAuth.mockResolvedValue({
    ok: true,
    json: async () => ({
      bindings: {
        'sess-1': { connection_id: 3, host: 'https://nzero.pro', app_subdomain: 'shop', app_name: 'Shop', workspace_name: 'Acme', status: 'active' },
      },
    }),
  })
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

it('debounces refreshes and shares one cached map across callers', async () => {
  const a = useN0Bindings()
  const b = useN0Bindings()
  a.scheduleRefresh()
  b.scheduleRefresh()
  a.scheduleRefresh()
  expect(fetchWithAuth).not.toHaveBeenCalled()
  await vi.advanceTimersByTimeAsync(N0_BINDINGS_DEBOUNCE_MS)
  expect(fetchWithAuth).toHaveBeenCalledTimes(1)
  expect(fetchWithAuth.mock.calls[0]![0]).toBe('/api/connectors/n0/bindings')
  expect(b.getBinding('sess-1')?.app_subdomain).toBe('shop')
  expect(a.getBinding('other')).toBeNull()
})

it('dedupes concurrent refresh calls', async () => {
  const { refresh } = useN0Bindings()
  await Promise.all([refresh(), refresh(), refresh()])
  expect(fetchWithAuth).toHaveBeenCalledTimes(1)
})

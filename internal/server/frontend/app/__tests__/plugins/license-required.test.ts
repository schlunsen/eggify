vi.hoisted(() => { (globalThis as any).defineNuxtPlugin = (p: unknown) => p })
import { installLicenseInterceptors } from '../../plugins/license-required.client'
import { _resetLicenseThrottles, activationNeedsLogin } from '../../utils/license'


describe('license_required interceptors', () => {
  const navigate = vi.fn()
  beforeEach(() => {
    _resetLicenseThrottles()
    navigate.mockReset()
    vi.stubGlobal('navigateTo', navigate)
    window.history.replaceState({}, '', '/agents')
  })
  afterEach(() => vi.unstubAllGlobals())

  it('opens /activate when a 402 license_required comes through $fetch / useFetch', async () => {
    // A tiny stand-in for ofetch: create() returns a fetcher that runs onResponseError.
    let hooks: any
    const base: any = {
      create(opts: any) {
        hooks = opts
        return { ...base, wrapped: true }
      },
    }
    vi.stubGlobal('$fetch', base)
    installLicenseInterceptors({ fetch: vi.fn(), } as any)
    expect((globalThis as any).$fetch.wrapped).toBe(true)

    hooks.onResponseError({ response: { status: 402, _data: { code: 'license_required' } } })
    expect(navigate).toHaveBeenCalledWith('/activate')

    navigate.mockReset()
    _resetLicenseThrottles()
    hooks.onResponseError({ response: { status: 402, _data: { code: 'other' } } })
    hooks.onResponseError({ response: { status: 500, _data: { code: 'license_required' } } })
    expect(navigate).not.toHaveBeenCalled()
  })

  it('still wraps window.fetch', async () => {
    const res = new Response(JSON.stringify({ code: 'license_required' }), { status: 402 })
    const win: any = { fetch: vi.fn().mockResolvedValue(res) }
    installLicenseInterceptors(win)
    expect(await win.fetch('/api/x')).toBe(res)
    await new Promise(r => setTimeout(r, 0))
    expect(navigate).toHaveBeenCalledWith('/activate')
  })

  it('falls back to a full navigation when navigateTo rejects', async () => {
    const assign = vi.fn()
    vi.stubGlobal('location', { pathname: '/agents', assign })
    navigate.mockRejectedValue(new Error('no router'))
    const { goToActivation } = await import('../../utils/license')
    goToActivation()
    await new Promise(r => setTimeout(r, 0))
    expect(assign).toHaveBeenCalledWith('/activate')
  })
})

describe('activationNeedsLogin', () => {
  it('covers signed-out, MFA-pending, first run and auth off', () => {
    expect(activationNeedsLogin({ enabled: true, authenticated: false })).toBe(true)
    expect(activationNeedsLogin({ enabled: true, authenticated: true, mfa_required: true, mfa_verified: false })).toBe(true)
    expect(activationNeedsLogin({ enabled: true, authenticated: true, mfa_required: true, mfa_verified: true })).toBe(false)
    expect(activationNeedsLogin({ enabled: true, authenticated: false, needs_setup: true })).toBe(false)
    expect(activationNeedsLogin({ enabled: false, authenticated: false })).toBe(false)
  })
})

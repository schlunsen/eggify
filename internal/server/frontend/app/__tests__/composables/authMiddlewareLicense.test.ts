const state = vi.hoisted(() => ({ status: {} as any, navigate: vi.fn() }))

vi.stubGlobal('defineNuxtRouteMiddleware', (fn: unknown) => fn)
vi.stubGlobal('navigateTo', state.navigate)
vi.stubGlobal('useAuth', () => {
  const s = state.status
  return {
    checkAuthStatus: async () => s,
    authEnabled: { value: s.enabled },
    isAuthenticated: { value: s.authenticated },
    needsSetup: { value: !!s.needs_setup },
    licenseRequired: { value: !!s.license_required },
    mfaRequired: { value: !!s.mfa_required },
    mfaVerified: { value: !!s.mfa_verified },
  }
})

async function run(path: string) {
  const mod: any = await import('../../middleware/auth.global')
  return mod.default({ path, fullPath: path }, {})
}

beforeEach(() => {
  state.navigate.mockReset().mockImplementation((to: unknown) => to)
  ;(process as any).server = false
})

it('first run with a license required sends the visitor to /activate before /setup', async () => {
  state.status = { enabled: true, authenticated: false, needs_setup: true, license_required: true }
  expect(await run('/')).toBe('/activate')
})

it('first run without a license requirement goes to /setup', async () => {
  state.status = { enabled: true, authenticated: false, needs_setup: true }
  expect(await run('/')).toBe('/setup')
})

it('/activate is open on first run (no session needed)', async () => {
  state.status = { enabled: true, authenticated: false, needs_setup: true, license_required: true }
  expect(await run('/activate')).toBeUndefined()
  expect(state.navigate).not.toHaveBeenCalled()
})

it('/activate sends a signed-out visitor and an MFA-pending user to /login', async () => {
  const to = { path: '/login', query: { redirect: '/activate' } }
  state.status = { enabled: true, authenticated: false }
  expect(await run('/activate')).toEqual(to)
  state.status = { enabled: true, authenticated: true, mfa_required: true, mfa_verified: false }
  expect(await run('/activate')).toEqual(to)
})

it('/activate is open to a signed-in user', async () => {
  state.status = { enabled: true, authenticated: true, mfa_required: true, mfa_verified: true }
  expect(await run('/activate')).toBeUndefined()
})

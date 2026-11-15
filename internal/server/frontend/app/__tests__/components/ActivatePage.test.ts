import { mount, flushPromises } from '@vue/test-utils'
import { ref } from 'vue'
import ActivatePage from '../../pages/activate.vue'

const mocks = vi.hoisted(() => ({
  checkAuthStatus: vi.fn(),
  replace: vi.fn(),
  push: vi.fn(),
}))

vi.mock('../../components/license/LicenseActivation.vue', () => ({
  default: { template: '<div data-testid="card" />' },
}))

function mountPage() {
  return mount(ActivatePage, { global: { stubs: { EggLoader: { template: '<i data-testid="loader" />' } } } })
}

beforeEach(() => {
  mocks.checkAuthStatus.mockReset()
  mocks.replace.mockReset().mockResolvedValue(undefined)
  mocks.push.mockReset()
  vi.stubGlobal('definePageMeta', () => {})
  vi.stubGlobal('useRouter', () => ({ replace: mocks.replace, push: mocks.push }))
  vi.stubGlobal('useAuth', () => ({ checkAuthStatus: mocks.checkAuthStatus }))
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }))
})
afterEach(() => vi.unstubAllGlobals())

it('never shows the form to a signed-out visitor, and redirects once', async () => {
  mocks.checkAuthStatus.mockResolvedValue({ enabled: true, authenticated: false })
  const w = mountPage()
  expect(w.find('[data-testid="card"]').exists()).toBe(false)
  expect(w.find('[data-testid="loader"]').exists()).toBe(true)
  await flushPromises()
  expect(w.find('[data-testid="card"]').exists()).toBe(false)
  expect(mocks.replace).toHaveBeenCalledTimes(1)
  expect(mocks.replace).toHaveBeenCalledWith({ path: '/login', query: { redirect: '/activate' } })
})

it('sends an MFA-pending user to login', async () => {
  mocks.checkAuthStatus.mockResolvedValue({ enabled: true, authenticated: true, mfa_required: true, mfa_verified: false })
  const w = mountPage()
  await flushPromises()
  expect(w.find('[data-testid="card"]').exists()).toBe(false)
  expect(mocks.replace).toHaveBeenCalledWith({ path: '/login', query: { redirect: '/activate' } })
})

it('first run: no account yet (needs_setup) shows the form without a session', async () => {
  mocks.checkAuthStatus.mockResolvedValue({ enabled: true, authenticated: false, needs_setup: true, license_required: true })
  const w = mountPage()
  await flushPromises()
  expect(w.find('[data-testid="card"]').exists()).toBe(true)
  expect(mocks.replace).not.toHaveBeenCalled()
})

it('a signed-in user sees the form', async () => {
  mocks.checkAuthStatus.mockResolvedValue({ enabled: true, authenticated: true, mfa_required: true, mfa_verified: true })
  const w = mountPage()
  await flushPromises()
  expect(w.find('[data-testid="card"]').exists()).toBe(true)
  expect(mocks.replace).not.toHaveBeenCalled()
})

it('shows the form when the auth check fails (the card reports its own errors)', async () => {
  mocks.checkAuthStatus.mockRejectedValue(new Error('down'))
  const w = mountPage()
  await flushPromises()
  expect(w.find('[data-testid="card"]').exists()).toBe(true)
})

// keep the import used for the ref-based helpers of sibling tests
void ref

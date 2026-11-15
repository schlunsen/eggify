import { mount, flushPromises } from '@vue/test-utils'
import { ref } from 'vue'
import SettingsLicense from '../../components/settings/License.vue'
import SettingsGlyph from '../../components/settings/Glyph.vue'

const mocks = vi.hoisted(() => ({
  status: null as any,
  busy: null as any,
  loading: null as any,
  refresh: vi.fn(),
  deactivate: vi.fn(),
  checkNow: vi.fn(),
  goToActivation: vi.fn(),
}))

vi.mock('../../composables/useLicense', () => ({
  useLicense: () => ({
    status: mocks.status, busy: mocks.busy, loading: mocks.loading,
    refresh: mocks.refresh, deactivate: mocks.deactivate, checkNow: mocks.checkNow,
  }),
}))
vi.mock('../../utils/license', async (orig) => ({
  ...(await orig<typeof import('../../utils/license')>()),
  goToActivation: mocks.goToActivation,
}))

const active = {
  required: true,
  state: 'active',
  serial_hint: 'EGG-…-V5N0T',
  machine: 'Studio',
  plan: 'standard',
  expires_at: '2026-11-10T12:00:00Z',
  seats: 3,
  seats_used: 2,
}

function mountWith(status: any) {
  mocks.status.value = status
  return mount(SettingsLicense, { global: { components: { SettingsGlyph }, stubs: { EggLoader: true, NuxtLink: { template: '<a><slot /></a>' } } } })
}

beforeEach(() => {
  mocks.status = ref(null)
  mocks.busy = ref(false)
  mocks.loading = ref(false)
  for (const fn of [mocks.refresh, mocks.deactivate, mocks.checkNow, mocks.goToActivation]) fn.mockReset()
})

it('shows the license: serial hint, machine, plan and offline date', () => {
  const w = mountWith(active)
  expect(w.get('[data-testid="license-state"]').text()).toContain('Activated')
  expect(w.get('[data-testid="license-serial-hint"]').text()).toBe('EGG-…-V5N0T')
  expect(w.get('[data-testid="license-machine"]').text()).toBe('Studio')
  expect(w.text()).toContain('Standard')
  expect(w.text()).toContain('2 of 3 in use')
  expect(w.get('[data-testid="license-line"]').text()).toMatch(/^Renews automatically; works offline until .*2026/)
})

it('warns when renewals fail but keeps it active', () => {
  const w = mountWith({ ...active, renew_error: "Couldn't reach the license server." })
  expect(w.get('[data-testid="license-renew-note"]').text()).toContain("Couldn't reach the license server.")
})

it('deactivates only after confirming, then opens the activation page', async () => {
  mocks.deactivate.mockResolvedValue({ ok: true, status: { required: true, state: 'unlicensed' } })
  const w = mountWith(active)
  await w.get('[data-testid="license-deactivate"]').trigger('click')
  expect(mocks.deactivate).not.toHaveBeenCalled()
  expect(w.get('[data-testid="license-confirm"]').text()).toContain('Deactivate Eggify on this Mac?')
  await w.get('[data-testid="license-confirm-deactivate"]').trigger('click')
  await flushPromises()
  expect(mocks.deactivate).toHaveBeenCalledOnce()
  expect(mocks.goToActivation).toHaveBeenCalled()
})

it('reports a failed deactivation in plain words', async () => {
  mocks.deactivate.mockResolvedValue({ ok: false, status: active, error: "Couldn't reach the license server." })
  const w = mountWith(active)
  await w.get('[data-testid="license-deactivate"]').trigger('click')
  await w.get('[data-testid="license-confirm-deactivate"]').trigger('click')
  await flushPromises()
  expect(w.get('[data-testid="license-action-error"]').text()).toContain("Couldn't reach the license server.")
  expect(mocks.goToActivation).not.toHaveBeenCalled()
})

it('checks now', async () => {
  mocks.checkNow.mockResolvedValue({ ok: true, status: active })
  const w = mountWith(active)
  await w.get('[data-testid="license-check"]').trigger('click')
  await flushPromises()
  expect(mocks.checkNow).toHaveBeenCalled()
  expect(w.get('[data-testid="license-action-note"]').text()).toContain('just now')
})

it('offers the serial field when not activated', () => {
  const w = mountWith({ required: true, state: 'unlicensed' })
  expect(w.get('[data-testid="license-state"]').text()).toContain('Not activated')
  expect(w.find('[data-testid="license-go-activate"]').exists()).toBe(true)
  expect(w.find('[data-testid="license-deactivate"]').exists()).toBe(false)
})

it('the deactivate confirm is a real <dialog> that Escape and Cancel close', async () => {
  const w = mountWith(active)
  await w.get('[data-testid="license-deactivate"]').trigger('click')
  await flushPromises()
  const dlg = w.get('dialog[data-testid="license-confirm"]')
  expect(dlg.attributes('role')).toBeUndefined()
  await dlg.trigger('cancel') // Escape
  expect(w.find('[data-testid="license-confirm"]').exists()).toBe(false)
  await w.get('[data-testid="license-deactivate"]').trigger('click')
  await w.get('[data-testid="license-confirm-cancel"]').trigger('click')
  expect(w.find('[data-testid="license-confirm"]').exists()).toBe(false)
  expect(mocks.deactivate).not.toHaveBeenCalled()
})

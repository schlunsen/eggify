import { mount, flushPromises } from '@vue/test-utils'
import { ref, nextTick } from 'vue'
import LicenseActivation from '../../components/license/LicenseActivation.vue'

const mocks = vi.hoisted(() => ({
  status: null as any,
  busy: null as any,
  loading: null as any,
  refresh: vi.fn(),
  activate: vi.fn(),
  openExternal: vi.fn(),
}))

vi.mock('../../composables/useLicense', () => ({
  useLicense: () => ({
    status: mocks.status,
    busy: mocks.busy,
    loading: mocks.loading,
    refresh: mocks.refresh,
    activate: mocks.activate,
  }),
}))
vi.mock('../../utils/desktop', () => ({ desktopOpenExternal: mocks.openExternal }))

function mountCard(opts: Record<string, unknown> = {}) {
  return mount(LicenseActivation, { ...opts, global: { stubs: { EggifyLogo: true, EggLoader: true } } })
}

async function type(w: ReturnType<typeof mount>, text: string) {
  const input = w.get('[data-testid="license-serial"]')
  ;(input.element as HTMLInputElement).value = text
  await input.trigger('input')
}

beforeEach(() => {
  mocks.status = ref({ required: true, state: 'unlicensed' })
  mocks.busy = ref(false)
  mocks.loading = ref(false)
  mocks.refresh.mockReset().mockResolvedValue(null)
  mocks.activate.mockReset()
  mocks.openExternal.mockReset().mockResolvedValue(true)
})

it('formats the serial as you type and enables Activate only for a valid one', async () => {
  const w = mountCard()
  await type(w, 'egg7h3k9q2m4x')
  expect((w.get('[data-testid="license-serial"]').element as HTMLInputElement).value).toBe('EGG-7H3K9-Q2M4X')
  expect(w.get('[data-testid="license-activate"]').attributes('disabled')).toBeDefined()

  await type(w, '7h3k9 q2m4x 8r6t1 v5n0t')
  expect((w.get('[data-testid="license-serial"]').element as HTMLInputElement).value).toBe('EGG-7H3K9-Q2M4X-8R6T1-V5N0T')
  expect(w.get('[data-testid="license-serial-hint"]').text()).toBe('Looks right.')
  expect(w.get('[data-testid="license-activate"]').attributes('disabled')).toBeUndefined()
})

it('catches a typo before sending anything', async () => {
  const w = mountCard()
  await type(w, 'EGG-7H3K9-Q2M4X-8R6T1-V5N0A')
  expect(w.get('[data-testid="license-serial-hint"]').text()).toContain('typo')
  expect(w.get('[data-testid="license-activate"]').attributes('disabled')).toBeDefined()
  await w.get('form').trigger('submit')
  expect(mocks.activate).not.toHaveBeenCalled()
})

it('activates and says it is done', async () => {
  mocks.activate.mockImplementation(async () => {
    mocks.status.value = { required: true, state: 'active', machine: 'Studio' }
    return { ok: true, status: mocks.status.value }
  })
  const w = mountCard()
  await type(w, 'EGG-7H3K9-Q2M4X-8R6T1-V5N0T')
  await w.get('form').trigger('submit')
  await flushPromises()
  expect(mocks.activate).toHaveBeenCalledWith('EGG-7H3K9-Q2M4X-8R6T1-V5N0T')
  expect(w.emitted('done')).toBeTruthy()
  expect(w.get('[data-testid="license-activated"]').text()).toContain('activated on Studio')
})

it('lists the machines when every seat is taken', async () => {
  mocks.activate.mockResolvedValue({
    ok: false,
    status: mocks.status.value,
    code: 'seats_full',
    error: 'This serial number is already active on the most computers it allows: Studio, Laptop, Work. Deactivate it on one of them (Settings → License on that Mac), then try again.',
    activations: [{ machine: 'Studio' }, { machine: 'Laptop' }, { machine: 'Work' }],
  })
  const w = mountCard()
  await type(w, 'EGG-7H3K9-Q2M4X-8R6T1-V5N0T')
  await w.get('form').trigger('submit')
  await flushPromises()
  expect(w.get('[data-testid="license-error"]').text()).toContain('Settings → License on that Mac')
  expect(w.findAll('[data-testid="license-machines"] li').map(li => li.text())).toEqual(['Studio', 'Laptop', 'Work'])
  expect(w.emitted('done')).toBeFalsy()
})

it('shows why a license went away', () => {
  mocks.status.value = { required: true, state: 'unlicensed', error: 'This serial number has been revoked.' }
  const w = mountCard()
  expect(w.get('[data-testid="license-notice"]').text()).toContain('revoked')
})

it('opens the shop through the desktop adapter', async () => {
  const w = mountCard()
  expect(w.get('[data-testid="license-buy"]').text()).toBe('Buy a license')
  await w.get('[data-testid="license-buy"]').trigger('click')
  expect(mocks.openExternal).toHaveBeenCalledWith('https://eggify.xyz')
})

it('says so when a 200 activation leaves the license not active', async () => {
  mocks.activate.mockImplementation(async () => {
    mocks.status.value = { required: true, state: 'expired', error: 'This license has expired.' }
    return { ok: true, status: mocks.status.value }
  })
  const w = mountCard()
  await type(w, 'EGG-7H3K9-Q2M4X-8R6T1-V5N0T')
  await w.get('form').trigger('submit')
  await flushPromises()
  expect(w.emitted('done')).toBeFalsy()
  expect(w.get('[data-testid="license-error"]').text()).toBe('This license has expired.')

  mocks.activate.mockImplementation(async () => {
    mocks.status.value = { required: true, state: 'unlicensed' }
    return { ok: true, status: mocks.status.value }
  })
  await w.get('form').trigger('submit')
  await flushPromises()
  expect(w.get('[data-testid="license-error"]').text()).toBe('Activation did not complete.')
})

it("doesn't claim activation when no license is required", async () => {
  mocks.status.value = { required: false, state: 'unlicensed' }
  const w = mountCard()
  await flushPromises()
  expect(w.get('[data-testid="license-activated"]').text()).not.toContain('is activated')
})

it('keeps the caret in place when editing in the middle', async () => {
  const w = mountCard({ attachTo: document.body })
  const input = w.get('[data-testid="license-serial"]').element as HTMLInputElement
  await type(w, 'EGG-7H3K9-Q2M4X')
  // type "X" after "EGG-7H3" (caret at 7)
  input.value = 'EGG-7H3X' + 'K9-Q2M4X'
  input.setSelectionRange(8, 8)
  await w.get('[data-testid="license-serial"]').trigger('input')
  await nextTick()
  expect(input.value).toBe('EGG-7H3XK-9Q2M4-X')
  expect(input.selectionStart).toBe(8)
  w.unmount()
})

it('uses readonly + aria-busy while activating and the hint is a live region', async () => {
  mocks.busy.value = true
  const w = mountCard()
  const input = w.get('[data-testid="license-serial"]')
  expect(input.attributes('readonly')).toBeDefined()
  expect(input.attributes('disabled')).toBeUndefined()
  expect(input.attributes('aria-busy')).toBe('true')
  expect(w.get('#serial-hint').attributes('aria-live')).toBe('polite')
})

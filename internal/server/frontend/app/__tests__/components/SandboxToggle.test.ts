import { mount, flushPromises } from '@vue/test-utils'
import SandboxToggle from '../../components/agents/SandboxToggle.vue'

const { load } = vi.hoisted(() => ({ load: vi.fn() }))
vi.mock('../../composables/useSandboxSettings', () => ({ useSandboxSettings: () => ({ load }) }))

const mountToggle = (props: Record<string, unknown>) => mount(SandboxToggle, { props })

beforeEach(() => {
  load.mockReset()
})

it('shows the global default while the session follows it', async () => {
  load.mockResolvedValue({ default_enabled: true, allowed_domains: [], default_allowed_domains: [] })
  const wrapper = mountToggle({ modelValue: null, provider: 'claude' })
  await flushPromises()
  const box = wrapper.get('[data-testid="sandbox-toggle"]').element as HTMLInputElement
  expect(box.checked).toBe(true)
  expect(box.disabled).toBe(false)
  expect(wrapper.text()).toContain('default')
  expect(wrapper.emitted('update:modelValue')).toBeUndefined()
})

it('emits an explicit choice when toggled', async () => {
  load.mockResolvedValue({ default_enabled: false, allowed_domains: [], default_allowed_domains: [] })
  const wrapper = mountToggle({ modelValue: null, provider: 'codex' })
  await flushPromises()
  const box = wrapper.get('[data-testid="sandbox-toggle"]')
  expect((box.element as HTMLInputElement).checked).toBe(false)
  ;(box.element as HTMLInputElement).checked = true
  await box.trigger('change')
  expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([true])
})

it('an explicit value wins over the default', async () => {
  load.mockResolvedValue({ default_enabled: true, allowed_domains: [], default_allowed_domains: [] })
  const wrapper = mountToggle({ modelValue: false, provider: 'claude' })
  await flushPromises()
  expect((wrapper.get('[data-testid="sandbox-toggle"]').element as HTMLInputElement).checked).toBe(false)
  expect(wrapper.text()).not.toContain('default')
})

it('is disabled and pinned off for providers that cannot be sandboxed', async () => {
  load.mockResolvedValue({ default_enabled: true, allowed_domains: [], default_allowed_domains: [] })
  const wrapper = mountToggle({ modelValue: null, provider: 'ollama' })
  await flushPromises()
  const box = wrapper.get('[data-testid="sandbox-toggle"]').element as HTMLInputElement
  expect(box.disabled).toBe(true)
  expect(box.checked).toBe(false)
  expect(wrapper.text()).toContain('Not available for this provider')
  expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([false])

  // Back to a supported provider: follow the default again.
  await wrapper.setProps({ modelValue: false, provider: 'claude' })
  expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([null])
})

it('falls back to off when the settings cannot be loaded', async () => {
  load.mockResolvedValue(null)
  const wrapper = mountToggle({ modelValue: null, provider: 'claude' })
  await flushPromises()
  expect((wrapper.get('[data-testid="sandbox-toggle"]').element as HTMLInputElement).checked).toBe(false)
})

it('keeps Codex networking off by default and clearly separate from filesystem protection', async () => {
  load.mockResolvedValue({ default_enabled: true, can_authorize_codex_network: true })
  const wrapper = mountToggle({ modelValue: null, provider: 'codex' })
  await flushPromises()
  const network = wrapper.get<HTMLInputElement>('[data-testid="codex-network-toggle"]')
  expect(network.element.checked).toBe(false)
  expect(network.element.disabled).toBe(false)
  expect(wrapper.text()).toContain('Filesystem sandbox')
  expect(wrapper.text()).toContain('unrestricted network')
  expect(wrapper.text()).toContain('including local and private services')
  expect(wrapper.text()).toContain('Eggify applies no domain allowlist')
  expect(wrapper.text()).toContain('start a new session to change it')
  await network.setValue(true)
  expect(wrapper.emitted('update:networkAccess')?.at(-1)).toEqual([true])
  wrapper.unmount()
})

it('requires both authentication capability and filesystem protection', async () => {
  load.mockResolvedValue({ default_enabled: true, can_authorize_codex_network: false })
  const wrapper = mountToggle({ modelValue: true, provider: 'codex' })
  await flushPromises()
  expect(wrapper.get<HTMLInputElement>('[data-testid="codex-network-toggle"]').element.disabled).toBe(true)
  wrapper.unmount()

  load.mockResolvedValue({ default_enabled: true, can_authorize_codex_network: true })
  const withoutSandbox = mountToggle({ modelValue: false, provider: 'codex' })
  await flushPromises()
  expect(withoutSandbox.get<HTMLInputElement>('[data-testid="codex-network-toggle"]').element.disabled).toBe(true)
  withoutSandbox.unmount()
})

it('clears network consent when disabling the sandbox or switching providers', async () => {
  load.mockResolvedValue({ default_enabled: true, can_authorize_codex_network: true })
  const wrapper = mountToggle({ modelValue: true, networkAccess: true, provider: 'codex' })
  await flushPromises()
  await wrapper.get('[data-testid="sandbox-toggle"]').setValue(false)
  expect(wrapper.emitted('update:networkAccess')?.at(-1)).toEqual([false])
  await wrapper.setProps({ provider: 'claude' })
  expect(wrapper.find('[data-testid="codex-network-toggle"]').exists()).toBe(false)
  expect(wrapper.emitted('update:networkAccess')?.at(-1)).toEqual([false])
  expect(wrapper.text()).toContain('allowlist in Settings')
  wrapper.unmount()
})

it('shows an implicit default as off for Codex, on for Claude', async () => {
  load.mockResolvedValue({ default_enabled: true, default_implicit: true, allowed_domains: [], default_allowed_domains: [] })
  const codex = mountToggle({ modelValue: null, provider: 'codex' })
  await flushPromises()
  expect((codex.get('[data-testid="sandbox-toggle"]').element as HTMLInputElement).checked).toBe(false)
  const claude = mountToggle({ modelValue: null, provider: 'claude' })
  await flushPromises()
  expect((claude.get('[data-testid="sandbox-toggle"]').element as HTMLInputElement).checked).toBe(true)
  load.mockResolvedValue({ default_enabled: true, default_implicit: false, allowed_domains: [], default_allowed_domains: [] })
  const saved = mountToggle({ modelValue: null, provider: 'codex' })
  await flushPromises()
  expect((saved.get('[data-testid="sandbox-toggle"]').element as HTMLInputElement).checked).toBe(true)
})

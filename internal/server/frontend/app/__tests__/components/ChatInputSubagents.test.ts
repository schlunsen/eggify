import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ChatInput from '../../components/agents/ChatInput.vue'

beforeEach(() => {
  setActivePinia(createPinia())
  vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth: vi.fn(async () => ({ ok: false, json: async () => ({}) })) }))
})
afterEach(() => vi.unstubAllGlobals())

const mountInput = (props: Record<string, unknown>) => mount(ChatInput, {
  props: { inputMessage: '', connected: true, hasActiveSession: true, ...props },
  global: { stubs: { MessageHistoryDropdown: true, transition: false } },
})

it('shows the subagents still working while the main agent is idle, and opens them', async () => {
  const wrapper = mountInput({ subagentsWorking: 2, busy: false })
  const chip = wrapper.get('.subagents-chip')
  expect(chip.text()).toBe('2 subagents working')
  await chip.trigger('click')
  expect(wrapper.emitted('open-subagents')).toHaveLength(1)
})

it('leaves the chip out while the main agent works or none are running', () => {
  expect(mountInput({ subagentsWorking: 2, busy: true }).find('.subagents-chip').exists()).toBe(false)
  expect(mountInput({ subagentsWorking: 0, busy: false }).find('.subagents-chip').exists()).toBe(false)
})

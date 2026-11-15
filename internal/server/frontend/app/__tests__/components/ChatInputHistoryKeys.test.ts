import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ChatInput from '../../components/agents/ChatInput.vue'

beforeEach(() => {
  setActivePinia(createPinia())
  vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth: vi.fn(async () => ({ ok: false, json: async () => ({}) })) }))
})
afterEach(() => vi.unstubAllGlobals())

// Message history opens on a bare ↑ only: ⌥⌘↑/↓ (Alt+↑/↓ elsewhere) moves to the
// session above/below, and used to open the history instead while the composer
// had focus, swallowing the shortcut.
async function composerWithHistory() {
  const wrapper = mount(ChatInput, {
    props: { inputMessage: 'an earlier prompt', connected: true, hasActiveSession: true },
    global: { stubs: { MessageHistoryDropdown: true, transition: false } },
  })
  await wrapper.get('textarea').trigger('keydown', { key: 'Enter' })
  await wrapper.setProps({ inputMessage: '' })
  return wrapper
}

const historyOpen = (wrapper: Awaited<ReturnType<typeof composerWithHistory>>) =>
  wrapper.findComponent({ name: 'MessageHistoryDropdown' }).props('visible')

it.each([
  ['⌥⌘↑', { metaKey: true, altKey: true }],
  ['Alt+↑', { altKey: true }],
  ['⇧↑', { shiftKey: true }],
  ['⌘↑', { metaKey: true }],
])('%s leaves the history closed and the key unhandled', async (_label, mods) => {
  const wrapper = await composerWithHistory()
  const event = new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true, cancelable: true, ...mods })
  wrapper.get('textarea').element.dispatchEvent(event)
  await wrapper.vm.$nextTick()
  expect(historyOpen(wrapper)).toBe(false)
  expect(event.defaultPrevented).toBe(false)
})

it('a bare ↑ on the first line still opens the history', async () => {
  const wrapper = await composerWithHistory()
  await wrapper.get('textarea').trigger('keydown', { key: 'ArrowUp' })
  expect(historyOpen(wrapper)).toBe(true)
})

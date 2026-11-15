import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { useChatWidth } from '../../composables/useChatWidth'

function mountPreference() {
  let preference!: ReturnType<typeof useChatWidth>
  const wrapper = mount(defineComponent({
    setup() {
      preference = useChatWidth()
      return () => null
    }
  }))
  return { wrapper, preference }
}

beforeEach(() => {
  vi.stubGlobal('localStorage', { getItem: vi.fn(), setItem: vi.fn() })
})
afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

it('saves width changes and restores them on the next mount', () => {
  let saved: string | null = null
  vi.spyOn(window.localStorage, 'getItem').mockImplementation(() => saved)
  const write = vi.spyOn(window.localStorage, 'setItem').mockImplementation((_key, value) => { saved = value })
  const first = mountPreference()
  expect(first.preference.chatWidth.value).toBe('full')
  first.preference.setChatWidth('reading')
  expect(write).toHaveBeenCalledWith('eggify-chat-width', 'reading')
  first.wrapper.unmount()
  const second = mountPreference()
  expect(second.preference.chatWidth.value).toBe('reading')
  second.preference.setChatWidth('full')
  expect(write).toHaveBeenLastCalledWith('eggify-chat-width', 'full')
  second.wrapper.unmount()
})

it('ignores invalid saved preferences', () => {
  vi.spyOn(window.localStorage, 'getItem').mockReturnValue('invalid')
  const { wrapper, preference } = mountPreference()
  expect(preference.chatWidth.value).toBe('full')
  wrapper.unmount()
})

it('still changes width when browser storage is blocked', () => {
  vi.spyOn(window.localStorage, 'getItem').mockImplementation(() => { throw new Error('blocked') })
  vi.spyOn(window.localStorage, 'setItem').mockImplementation(() => { throw new Error('blocked') })
  const { wrapper, preference } = mountPreference()
  expect(preference.chatWidth.value).toBe('full')
  preference.setChatWidth('reading')
  expect(preference.chatWidth.value).toBe('reading')
  wrapper.unmount()
})

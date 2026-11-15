import { ref } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'

// The voice composables are big; give ChatArea something that answers every
// property with a callable ref (value 0 / '' for text) so it renders.
const fake = vi.hoisted(() => () => {
  const cache: Record<string, any> = {}
  return new Proxy({}, {
    get(_t, key: string) {
      if (!(key in cache)) {
        const fn: any = () => undefined
        fn.value = /text|error|transcript/i.test(key) ? '' : 0
        cache[key] = fn
      }
      return cache[key]
    }
  })
})
vi.mock('../../composables/useVoiceRecording', () => ({ useVoiceRecording: () => fake() }))
vi.mock('../../composables/useWhisperTranscription', () => ({ useWhisperTranscription: () => fake() }))
vi.mock('../../composables/useParakeetTranscription', () => ({ useParakeetTranscription: () => fake(), preloadParakeetModel: vi.fn() }))
vi.mock('../../composables/agents/useZenAnimation', () => ({ preloadZenScene: vi.fn() }))

import ChatArea from '../../components/agents/ChatArea.vue'
import { installShortcutDispatcher, __resetShortcuts, __setShortcutEnv } from '../../composables/useKeyboardShortcuts'

let uninstall: () => void
beforeEach(() => {
  __resetShortcuts()
  uninstall = installShortcutDispatcher(window)
  vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth: vi.fn(async () => ({ ok: false, json: async () => ({}) })) }))
  vi.stubGlobal('requestIdleCallback', () => 0)
})
afterEach(() => {
  uninstall()
  __setShortcutEnv({})
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

function mountChat(props: Record<string, unknown> = {}) {
  return mount(ChatArea, {
    attachTo: document.body,
    props: { hasActiveSession: true, inputMessage: '', connected: true, isThinking: false, isProcessing: true, sessionId: 's1', projectId: 'p1', ...props },
    global: {
      stubs: {
        ChatHeader: true, InitialPromptBanner: true, WorkspaceTrustBanner: true, ChatMessages: true,
        FullscreenEditorModal: true, RecordingModal: true, ZenVoiceBar: true, SlashCommandAutocomplete: true,
        ZenMode: true, SessionTerminal: true, FileExplorer: true, ImageGallery: true, Teleport: true,
        ChatInput: { template: '<textarea class="composer" />' }
      }
    }
  })
}

const press = (init: KeyboardEventInit, target: EventTarget) => {
  const e = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init })
  target.dispatchEvent(e)
  return e
}

it('⌘. stops the agent, also while typing in the composer', async () => {
  __setShortcutEnv({ platform: 'mac', shell: 'web' })
  const w = mountChat()
  await flushPromises()
  const composer = w.get('.composer').element
  press({ key: '.', code: 'Period', metaKey: true }, composer)
  expect(w.emitted('interrupt')).toHaveLength(1)
  w.unmount()
})

it('Escape stops a running turn, also while typing in the composer', async () => {
  __setShortcutEnv({ platform: 'mac', shell: 'web' })
  const w = mountChat()
  await flushPromises()
  const e = press({ key: 'Escape', code: 'Escape' }, w.get('.composer').element)
  expect(w.emitted('interrupt')).toHaveLength(1)
  expect(e.defaultPrevented).toBe(true)
  press({ key: 'Escape', code: 'Escape' }, document.body)
  expect(w.emitted('interrupt')).toHaveLength(2)
  w.unmount()
})

it('Escape also stops a turn the session list reports as processing', async () => {
  __setShortcutEnv({ platform: 'mac', shell: 'web' })
  const w = mountChat({ isProcessing: false, isThinking: false, sessionStatus: 'processing' })
  await flushPromises()
  const e = press({ key: 'Escape', code: 'Escape' }, w.get('.composer').element)
  expect(w.emitted('interrupt')).toHaveLength(1)
  expect(e.defaultPrevented).toBe(true)
  w.unmount()
})

it('Escape does nothing when the agent is idle', async () => {
  __setShortcutEnv({ platform: 'mac', shell: 'web' })
  const w = mountChat({ isProcessing: false, isThinking: false })
  await flushPromises()
  const e = press({ key: 'Escape', code: 'Escape' }, w.get('.composer').element)
  expect(w.emitted('interrupt')).toBeUndefined()
  expect(e.defaultPrevented).toBe(false)
  w.unmount()
})

it('Escape closes an open dialog first and does not stop the agent', async () => {
  __setShortcutEnv({ platform: 'mac', shell: 'web' })
  const { pushClosable } = await import('../../composables/useKeyboardShortcuts')
  const close = vi.fn()
  const off = pushClosable(close)
  const w = mountChat()
  await flushPromises()
  press({ key: 'Escape', code: 'Escape' }, document.body)
  expect(close).toHaveBeenCalledOnce()
  expect(w.emitted('interrupt')).toBeUndefined()
  off()
  w.unmount()
})

it('Ctrl+. stops the agent on Windows/Linux', async () => {
  __setShortcutEnv({ platform: 'other', shell: 'web' })
  const w = mountChat()
  await flushPromises()
  press({ key: '.', code: 'Period', ctrlKey: true }, document.body)
  expect(w.emitted('interrupt')).toHaveLength(1)
  w.unmount()
})

it('in the desktop app ⌘. is left to the Session → Stop Agent menu item', async () => {
  __setShortcutEnv({ platform: 'mac', shell: 'wails' })
  const w = mountChat()
  await flushPromises()
  const e = press({ key: '.', code: 'Period', metaKey: true }, document.body)
  expect(w.emitted('interrupt')).toBeUndefined()
  expect(e.defaultPrevented).toBe(false)
  const { runShortcut } = await import('../../composables/useKeyboardShortcuts')
  runShortcut('agent.stop', { source: 'menu' })
  expect(w.emitted('interrupt')).toHaveLength(1)
  w.unmount()
})

it('⇧⌘Enter switches Zen ↔ Live and ⇧⌘E toggles the Files tab', async () => {
  __setShortcutEnv({ platform: 'mac', shell: 'web' })
  const w = mountChat({ viewMode: 'live' })
  await flushPromises()
  press({ key: 'Enter', code: 'Enter', metaKey: true, shiftKey: true }, document.body)
  expect(w.emitted('update:view-mode')?.[0]).toEqual(['zen'])
  press({ key: 'E', code: 'KeyE', metaKey: true, shiftKey: true }, document.body)
  await flushPromises()
  expect(w.find('.files-view').exists()).toBe(true)
  w.unmount()
})

it('registers nothing without an open session', async () => {
  __setShortcutEnv({ platform: 'mac', shell: 'web' })
  const w = mountChat({ hasActiveSession: false })
  await flushPromises()
  const e = press({ key: '.', code: 'Period', metaKey: true }, document.body)
  expect(e.defaultPrevented).toBe(false)
  expect(w.emitted('interrupt')).toBeUndefined()
  w.unmount()
})

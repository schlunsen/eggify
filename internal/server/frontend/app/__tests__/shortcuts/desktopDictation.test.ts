import { ref } from 'vue'

// Capture the callback the dispatcher registers for the native menu's
// `eggify:shortcut` event, so the test can play the menu.
const menu = vi.hoisted(() => ({ cb: null as null | ((id: string) => void) }))
vi.mock('../../utils/desktop', async (orig) => ({
  ...(await orig<typeof import('../../utils/desktop')>()),
  desktopShell: () => 'wails',
  onDesktopShortcut: async (cb: (id: string) => void) => { menu.cb = cb; return () => { menu.cb = null } }
}))

import {
  installShortcutDispatcher, registerShortcutHandler, pushClosable, __resetShortcuts, __setShortcutEnv
} from '../../composables/useKeyboardShortcuts'

// The same registrations as ChatArea.vue: a session-scope toggle, plus modal-scope
// finish handlers that apply only while the recording dialog is recording.
function setup() {
  const modalOpen = ref(false)
  const recording = ref(false)
  const log: string[] = []
  registerShortcutHandler('voice.dictate', () => {
    if (recording.value) log.push('finish')
    else if (!modalOpen.value) { log.push('start'); modalOpen.value = true; recording.value = true }
  })
  registerShortcutHandler('voice.dictate', () => log.push('finish'), { scope: 'modal', when: () => modalOpen.value && recording.value })
  registerShortcutHandler('voice.finish', () => log.push('finish'), { scope: 'modal', when: () => modalOpen.value && recording.value })
  pushClosable(() => log.push('cancel'), { modal: true, when: () => modalOpen.value })
  return { modalOpen, recording, log }
}

let uninstall: () => void
beforeEach(async () => {
  __resetShortcuts()
  __setShortcutEnv({ platform: 'mac', shell: 'wails' })
  uninstall = installShortcutDispatcher(window)
  await Promise.resolve(); await Promise.resolve()
})
afterEach(() => { uninstall(); __setShortcutEnv({}) })

const key = (init: KeyboardEventInit) => window.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init }))

describe('dictation in the desktop app (native menu)', () => {
  it('menu event starts, then the modal handler finishes', () => {
    const { log } = setup()
    expect(menu.cb).toBeTypeOf('function')
    menu.cb!('voice.dictate')
    menu.cb!('voice.dictate')
    expect(log).toEqual(['start', 'finish'])
  })

  it('the ⌘D keydown itself is left to the menu (no double fire)', () => {
    const { log } = setup()
    key({ key: 'd', metaKey: true })
    expect(log).toEqual([])
    menu.cb!('voice.dictate')
    expect(log).toEqual(['start'])
  })

  it('Space finishes while the dialog records (a plain DOM key, not a menu item)', () => {
    const { log } = setup()
    menu.cb!('voice.dictate')
    key({ key: ' ' })
    expect(log).toEqual(['start', 'finish'])
  })

  it('Space does nothing when no recording is running', () => {
    const { log } = setup()
    key({ key: ' ' })
    expect(log).toEqual([])
  })

  it('the menu does not start a second recording while the dialog is open but not recording yet', () => {
    const { log, modalOpen } = setup()
    modalOpen.value = true // model loading / mic permission pending
    menu.cb!('voice.dictate')
    expect(log).toEqual([])
  })

  it('Escape cancels through the modal closable', () => {
    const { log } = setup()
    menu.cb!('voice.dictate')
    key({ key: 'Escape' })
    expect(log).toEqual(['start', 'cancel'])
  })
})

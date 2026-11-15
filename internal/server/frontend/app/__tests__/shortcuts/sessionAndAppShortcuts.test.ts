import { defineComponent, h, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { useSessionNavShortcuts } from '../../composables/agents/useSessionNavShortcuts'
import { useAppShortcuts, NEW_PROJECT_ROUTE } from '../../composables/useAppShortcuts'
import { installShortcutDispatcher, __resetShortcuts, __setShortcutEnv } from '../../composables/useKeyboardShortcuts'

let uninstall: () => void
beforeEach(() => {
  __resetShortcuts()
  uninstall = installShortcutDispatcher(window)
})
afterEach(() => {
  uninstall()
  __setShortcutEnv({})
})

const fire = (init: KeyboardEventInit, target: EventTarget = document.body) =>
  target.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init }))

function mountNav() {
  const sessions = ref([{ id: 's1' }, { id: 's2' }, { id: 's2' }, { id: 's3' }])
  const active = ref<string | null>('s1')
  const select = vi.fn((id: string) => { active.value = id })
  const w = mount(defineComponent({ setup() { useSessionNavShortcuts(sessions, active, select); return () => h('div') } }))
  return { w, select, active }
}

it('⌘1…⌘9 select sessions in the sidebar order (desktop app: plain ⌘)', () => {
  __setShortcutEnv({ platform: 'mac', shell: 'wails' })
  const { w, select } = mountNav()
  const composer = document.createElement('textarea')
  document.body.append(composer)
  fire({ key: '3', code: 'Digit3', metaKey: true }, composer) // works while typing
  expect(select).toHaveBeenLastCalledWith('s3') // duplicate s2 counted once, like the sidebar
  fire({ key: '2', code: 'Digit2', metaKey: true })
  expect(select).toHaveBeenLastCalledWith('s2')
  fire({ key: '9', code: 'Digit9', metaKey: true }) // no 9th session
  expect(select).toHaveBeenCalledTimes(2)
  composer.remove()
  w.unmount()
})

it('in a browser tab ⌘1…⌘9 belong to the browser, so ⌥⌘1…⌥⌘9 select', () => {
  __setShortcutEnv({ platform: 'mac', shell: 'web' })
  const { w, select } = mountNav()
  const plain = new KeyboardEvent('keydown', { key: '2', code: 'Digit2', metaKey: true, bubbles: true, cancelable: true })
  document.body.dispatchEvent(plain)
  expect(select).not.toHaveBeenCalled()
  expect(plain.defaultPrevented).toBe(false)
  fire({ key: '™', code: 'Digit2', metaKey: true, altKey: true })
  expect(select).toHaveBeenCalledWith('s2')
  w.unmount()
})

it('⌘[ / ⌘] step through sessions and wrap; Ctrl on Windows/Linux', () => {
  __setShortcutEnv({ platform: 'other', shell: 'web' })
  const { w, active } = mountNav()
  fire({ key: ']', code: 'BracketRight', ctrlKey: true })
  expect(active.value).toBe('s2')
  fire({ key: ']', code: 'BracketRight', ctrlKey: true })
  fire({ key: ']', code: 'BracketRight', ctrlKey: true })
  expect(active.value).toBe('s1')
  fire({ key: '[', code: 'BracketLeft', ctrlKey: true })
  expect(active.value).toBe('s3')
  w.unmount()
})

it('bare Option no longer toggles anything', () => {
  __setShortcutEnv({ platform: 'mac', shell: 'web' })
  const { w, select } = mountNav()
  for (const e of [{ key: 'Alt', code: 'AltLeft', altKey: true }, { key: '@', code: 'Digit2', altKey: true }, { key: 'ArrowDown', code: 'ArrowDown' }]) {
    const ev = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...e })
    document.body.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(false)
  }
  expect(select).not.toHaveBeenCalled()
  w.unmount()
})

function mountApp(enabled = true) {
  const navigate = vi.fn()
  const openProjectSelector = vi.fn()
  const w = mount(defineComponent({ setup() { useAppShortcuts({ navigate, openProjectSelector, enabled: () => enabled }); return () => h('div') } }))
  return { w, navigate, openProjectSelector }
}

it('⇧⌘N opens the new-project flow and ⇧⌘P the project switcher (desktop app)', () => {
  __setShortcutEnv({ platform: 'mac', shell: 'tauri' })
  const { w, navigate, openProjectSelector } = mountApp()
  fire({ key: 'N', code: 'KeyN', metaKey: true, shiftKey: true })
  expect(navigate).toHaveBeenCalledWith(NEW_PROJECT_ROUTE)
  expect(NEW_PROJECT_ROUTE).toBe('/projects?new=1')
  fire({ key: 'P', code: 'KeyP', metaKey: true, shiftKey: true })
  expect(openProjectSelector).toHaveBeenCalledTimes(1)
  w.unmount()
})

it('⇧⌘N is Chrome’s incognito window, so a browser tab uses ⌥⇧⌘N (Ctrl+Alt+Shift+N)', () => {
  __setShortcutEnv({ platform: 'other', shell: 'web' })
  const { w, navigate } = mountApp()
  fire({ key: 'N', code: 'KeyN', ctrlKey: true, shiftKey: true })
  expect(navigate).not.toHaveBeenCalled()
  fire({ key: 'N', code: 'KeyN', ctrlKey: true, shiftKey: true, altKey: true })
  expect(navigate).toHaveBeenCalledWith('/projects?new=1')
  w.unmount()
})

it('does nothing while signed out', () => {
  __setShortcutEnv({ platform: 'mac', shell: 'tauri' })
  const { w, navigate } = mountApp(false)
  fire({ key: 'N', code: 'KeyN', metaKey: true, shiftKey: true })
  expect(navigate).not.toHaveBeenCalled()
  w.unmount()
})

it('⌥⌘↓ / ⌥⌘↑ step through sessions from the composer in a browser tab; Alt+↓/↑ elsewhere; the desktop menu owns them in the app', () => {
  {
    // Desktop app: Go → Session Above/Below has the key and sends the id back.
    __setShortcutEnv({ platform: 'mac', shell: 'wails' })
    const { w, active } = mountNav()
    const down = new KeyboardEvent('keydown', { key: 'ArrowDown', code: 'ArrowDown', metaKey: true, altKey: true, bubbles: true, cancelable: true })
    document.body.dispatchEvent(down)
    expect(active.value).toBe('s1')
    expect(down.defaultPrevented).toBe(false)
    w.unmount()
    __resetShortcuts()
    uninstall(); uninstall = installShortcutDispatcher(window)
  }
  {
    __setShortcutEnv({ platform: 'mac', shell: 'web' })
    const { w, active } = mountNav()
    const composer = document.createElement('textarea')
    document.body.append(composer)
    const down = new KeyboardEvent('keydown', { key: 'ArrowDown', code: 'ArrowDown', metaKey: true, altKey: true, bubbles: true, cancelable: true })
    composer.dispatchEvent(down)
    expect(active.value).toBe('s2')
    expect(down.defaultPrevented).toBe(true)
    fire({ key: 'ArrowDown', code: 'ArrowDown', metaKey: true, altKey: true }, composer)
    expect(active.value).toBe('s3')
    fire({ key: 'ArrowUp', code: 'ArrowUp', metaKey: true, altKey: true }, composer)
    expect(active.value).toBe('s2')
    fire({ key: 'ArrowUp', code: 'ArrowUp', altKey: true }, composer) // ⌥↑ alone stays with the text field
    expect(active.value).toBe('s2')
    composer.remove()
    w.unmount()
    __resetShortcuts()
    uninstall(); uninstall = installShortcutDispatcher(window)
  }
  __setShortcutEnv({ platform: 'other', shell: 'web' })
  const { w, active } = mountNav()
  fire({ key: 'ArrowDown', code: 'ArrowDown', altKey: true })
  expect(active.value).toBe('s2')
  w.unmount()
})

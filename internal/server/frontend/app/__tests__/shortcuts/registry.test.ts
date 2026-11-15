import { defineComponent, h, ref, nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import {
  registerShortcutHandler, dispatchKeydown, runShortcut, pushClosable, pushEscapeAction, closeTopmost, isModalOpen,
  installShortcutDispatcher, useClosable, __setShortcutEnv, __resetShortcuts
} from '../../composables/useKeyboardShortcuts'

const press = (init: KeyboardEventInit, target: EventTarget = document.body) => {
  const e = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init })
  Object.defineProperty(e, 'target', { value: target })
  dispatchKeydown(e)
  return e
}

beforeEach(() => {
  __resetShortcuts()
  __setShortcutEnv({ platform: 'mac', shell: 'wails' })
})
afterEach(() => {
  __setShortcutEnv({})
})

describe('handlers and scopes', () => {
  it('runs the highest-priority handler only (modal > zen > session > global)', () => {
    const global = vi.fn(), session = vi.fn(), zen = vi.fn()
    registerShortcutHandler('view.fullscreen', global, { scope: 'global' })
    registerShortcutHandler('view.fullscreen', zen, { scope: 'zen' })
    registerShortcutHandler('view.fullscreen', session, { scope: 'session' })
    __setShortcutEnv({ platform: 'mac', shell: 'web' }) // the desktop menu owns ⌃⌘F
    press({ key: 'f', code: 'KeyF', metaKey: true, ctrlKey: true })
    expect(zen).toHaveBeenCalledTimes(1)
    expect(global).not.toHaveBeenCalled()
    expect(session).not.toHaveBeenCalled()
  })

  it('falls back when the higher handler’s guard fails', () => {
    const global = vi.fn(), zen = vi.fn()
    registerShortcutHandler('view.fullscreen', global)
    registerShortcutHandler('view.fullscreen', zen, { scope: 'zen', when: () => false })
    expect(runShortcut('view.fullscreen')).toBe(true)
    expect(global).toHaveBeenCalledTimes(1)
    expect(zen).not.toHaveBeenCalled()
  })

  it('blocks non-modal shortcuts while a modal is open', () => {
    const newSession = vi.fn(), closePalette = vi.fn()
    registerShortcutHandler('session.new', newSession)
    registerShortcutHandler('palette.open', closePalette, { scope: 'modal' })
    const pop = pushClosable(() => {}, { modal: true })
    expect(isModalOpen()).toBe(true)
    press({ key: 'n', code: 'KeyN', metaKey: true })
    expect(newSession).not.toHaveBeenCalled()
    __setShortcutEnv({ platform: 'mac', shell: 'web' })
    press({ key: 'k', code: 'KeyK', metaKey: true })
    expect(closePalette).toHaveBeenCalledTimes(1)
    pop()
    expect(isModalOpen()).toBe(false)
  })

  it('unregisters', () => {
    const fn = vi.fn()
    const off = registerShortcutHandler('sidebar.toggle', fn)
    off()
    expect(runShortcut('sidebar.toggle')).toBe(false)
  })

  it('rejects unknown ids', () => {
    expect(() => registerShortcutHandler('nope' as any, () => {})).toThrow()
  })
})

describe('keyboard dispatch', () => {
  beforeEach(() => __setShortcutEnv({ platform: 'mac', shell: 'web' }))

  it('runs ⌘ chords while typing, but not bare keys', () => {
    const palette = vi.fn(), fly = vi.fn()
    registerShortcutHandler('palette.open', palette)
    registerShortcutHandler('zen.fly', fly, { scope: 'zen' })
    const textarea = document.createElement('textarea')
    const e = press({ key: 'k', code: 'KeyK', metaKey: true }, textarea)
    expect(palette).toHaveBeenCalledTimes(1)
    expect(e.defaultPrevented).toBe(true)
    // ⌥F types "ƒ" in the composer…
    const typed = press({ key: 'ƒ', code: 'KeyF', altKey: true }, textarea)
    expect(fly).not.toHaveBeenCalled()
    expect(typed.defaultPrevented).toBe(false)
    // …and flies outside it
    press({ key: 'ƒ', code: 'KeyF', altKey: true })
    expect(fly).toHaveBeenCalledTimes(1)
  })

  it('ignores IME composition, handled events and key repeat', () => {
    const fn = vi.fn()
    registerShortcutHandler('palette.open', fn)
    press({ key: 'k', code: 'KeyK', metaKey: true, isComposing: true })
    const handled = new KeyboardEvent('keydown', { key: 'k', code: 'KeyK', metaKey: true, cancelable: true })
    handled.preventDefault()
    dispatchKeydown(handled)
    const repeat = press({ key: 'k', code: 'KeyK', metaKey: true, repeat: true })
    expect(fn).not.toHaveBeenCalled()
    expect(repeat.defaultPrevented).toBe(true) // still swallowed, so the browser doesn't act
  })

  it('leaves keys with no handler to the browser (⌘F with no open session)', () => {
    const e = press({ key: 'f', code: 'KeyF', metaKey: true })
    expect(e.defaultPrevented).toBe(false)
  })

  it('uses the ⌥ fallback for browser-reserved keys in a browser tab', () => {
    const fn = vi.fn()
    registerShortcutHandler('session.new', fn)
    const plain = press({ key: 'n', code: 'KeyN', metaKey: true })
    expect(fn).not.toHaveBeenCalled()
    expect(plain.defaultPrevented).toBe(false)
    press({ key: '˜', code: 'KeyN', metaKey: true, altKey: true })
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('passes the digit for ⌘1…⌘9', () => {
    const fn = vi.fn()
    registerShortcutHandler('session.jump', fn)
    __setShortcutEnv({ platform: 'mac', shell: 'tauri' })
    press({ key: '3', code: 'Digit3', metaKey: true })
    expect(fn).toHaveBeenCalledWith(expect.objectContaining({ digit: 3, source: 'key' }))
  })

  it('leaves Ctrl+letter to the terminal on Windows/Linux', () => {
    __setShortcutEnv({ platform: 'other', shell: 'web' })
    const dictate = vi.fn()
    registerShortcutHandler('voice.dictate', dictate)
    const term = document.createElement('div'); term.className = 'xterm'
    const ta = document.createElement('textarea'); term.append(ta)
    press({ key: 'd', code: 'KeyD', ctrlKey: true }, ta)
    expect(dictate).not.toHaveBeenCalled()
    press({ key: 'd', code: 'KeyD', ctrlKey: true })
    expect(dictate).toHaveBeenCalledTimes(1)
  })
})

describe('desktop app menu', () => {
  it('leaves menu-owned keys to the native menu and runs the id it sends back', () => {
    __setShortcutEnv({ platform: 'mac', shell: 'wails' })
    const stop = vi.fn(), jump = vi.fn()
    registerShortcutHandler('agent.stop', stop)
    registerShortcutHandler('session.jump', jump)
    const e = press({ key: '.', code: 'Period', metaKey: true })
    expect(stop).not.toHaveBeenCalled()
    expect(e.defaultPrevented).toBe(false) // so the menu accelerator fires
    expect(runShortcut('agent.stop', { source: 'menu' })).toBe(true)
    expect(stop).toHaveBeenCalledTimes(1)
    // No menu item for ⌘1…⌘9: the page handles them
    press({ key: '1', code: 'Digit1', metaKey: true })
    expect(jump).toHaveBeenCalledTimes(1)
  })
})

describe('Escape stack', () => {
  beforeEach(() => __setShortcutEnv({ platform: 'mac', shell: 'web' }))

  it('closes the most recently opened thing first, one per press', () => {
    const order: string[] = []
    pushClosable(() => order.push('fullscreen'))
    pushClosable(() => order.push('dialog'), { modal: true })
    const e = press({ key: 'Escape', code: 'Escape' })
    expect(order).toEqual(['dialog'])
    expect(e.defaultPrevented).toBe(true)
  })

  it('skips closables whose guard fails', () => {
    const a = vi.fn(), b = vi.fn()
    pushClosable(a)
    pushClosable(b, { when: () => false })
    expect(closeTopmost()).toBe(true)
    expect(a).toHaveBeenCalled()
    expect(b).not.toHaveBeenCalled()
  })

  it('does not run the ⌘. stop handler itself; only an Escape action runs, when nothing is open', () => {
    const stop = vi.fn()
    registerShortcutHandler('agent.stop', stop)
    const e = press({ key: 'Escape', code: 'Escape' }, document.createElement('textarea'))
    expect(stop).not.toHaveBeenCalled()
    expect(e.defaultPrevented).toBe(false)
    press({ key: '.', code: 'Period', metaKey: true })
    expect(stop).toHaveBeenCalledTimes(1)
  })

  it('runs the Escape action only when nothing is open and its condition holds', () => {
    const act = vi.fn()
    let usable = false
    const off = pushEscapeAction(act, () => usable)
    press({ key: 'Escape', code: 'Escape' })
    expect(act).not.toHaveBeenCalled()
    usable = true
    const close = vi.fn()
    const offClose = pushClosable(close)
    press({ key: 'Escape', code: 'Escape' })
    expect(close).toHaveBeenCalledOnce()
    expect(act).not.toHaveBeenCalled()
    offClose()
    const e = press({ key: 'Escape', code: 'Escape' })
    expect(act).toHaveBeenCalledOnce()
    expect(e.defaultPrevented).toBe(true)
    off()
  })

  it('leaves Escape alone when a component already handled it', () => {
    const close = vi.fn()
    pushClosable(close)
    const e = new KeyboardEvent('keydown', { key: 'Escape', cancelable: true })
    e.preventDefault()
    dispatchKeydown(e)
    expect(close).not.toHaveBeenCalled()
  })

  it('useClosable follows an open ref and cleans up on unmount', async () => {
    const open = ref(false)
    const close = vi.fn(() => { open.value = false })
    const Comp = defineComponent({ setup() { useClosable(open, close, { modal: true }); return () => h('div') } })
    const w = mount(Comp)
    expect(isModalOpen()).toBe(false)
    open.value = true
    await nextTick()
    expect(isModalOpen()).toBe(true)
    expect(closeTopmost()).toBe(true)
    expect(close).toHaveBeenCalled()
    expect(isModalOpen()).toBe(false)
    open.value = true
    w.unmount()
    expect(isModalOpen()).toBe(false)
  })

  it('installs exactly one listener', () => {
    const add = vi.spyOn(window, 'addEventListener')
    const off1 = installShortcutDispatcher(window)
    const off2 = installShortcutDispatcher(window)
    expect(add.mock.calls.filter(c => c[0] === 'keydown')).toHaveLength(1)
    expect(off1).toBe(off2)
    off1()
    add.mockRestore()
  })
})

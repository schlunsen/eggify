import { nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import {
  specFromEvent, overrideProblem, browserMayReserve, sanitizeOverrides, setShortcutOverride, resetShortcutOverrides,
  shortcutOverrides, setShortcutOverrides, onShortcutOverridesChange, canonicalSpec, __resetShortcutOverrides,
  SHORTCUT_OVERRIDES_STORAGE_KEY
} from '../../utils/shortcuts/overrides'
import { SHORTCUT_OVERRIDES_PENDING_KEY } from '../../utils/shortcuts/overridesSync'
import {
  SHORTCUTS, effectiveChord, shortcutDef, shortcutLabel, shortcutAria, isCustomizable, desktopMenuOverrides,
  ownedByDesktopMenu, setDesktopMenuHasOwnKeys
} from '../../utils/shortcuts/catalog'
import {
  registerShortcutHandler, dispatchKeydown, useShortcutLabels, __setShortcutEnv, __resetShortcuts
} from '../../composables/useKeyboardShortcuts'
import { startShortcutOverridesSync, loadShortcutOverrides } from '../../utils/shortcuts/overridesSync'
import KeyboardShortcuts from '../../components/settings/KeyboardShortcuts.vue'

const ev = (init: Partial<KeyboardEvent>) => ({ key: '', code: '', metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...init })
const press = (init: KeyboardEventInit, target: EventTarget = document.body) => {
  const e = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init })
  Object.defineProperty(e, 'target', { value: target })
  dispatchKeydown(e)
  return e
}

beforeEach(() => {
  __resetShortcuts()
  __resetShortcutOverrides()
  setDesktopMenuHasOwnKeys(true)
  localStorage.clear()
  __setShortcutEnv({ platform: 'mac', shell: 'web' })
})
afterEach(() => {
  __setShortcutEnv({})
})

describe('recording a key', () => {
  it('records ⌘ as mod and ⌃ as ctrl on macOS', () => {
    expect(specFromEvent(ev({ key: 'k', code: 'KeyK', metaKey: true, shiftKey: true }), 'mac')).toBe('shift+mod+k')
    expect(specFromEvent(ev({ key: 'j', code: 'KeyJ', ctrlKey: true }), 'mac')).toBe('ctrl+j')
    // Option changes the character; the physical key wins
    expect(specFromEvent(ev({ key: '˜', code: 'KeyN', metaKey: true, altKey: true }), 'mac')).toBe('alt+mod+n')
    expect(specFromEvent(ev({ key: 'ArrowUp', code: 'ArrowUp', metaKey: true }), 'mac')).toBe('mod+up')
    expect(specFromEvent(ev({ key: 'F5', code: 'F5' }), 'mac')).toBe('f5')
  })

  it('records Ctrl as mod elsewhere and refuses the Windows key', () => {
    expect(specFromEvent(ev({ key: 'k', code: 'KeyK', ctrlKey: true }), 'other')).toBe('mod+k')
    expect(specFromEvent(ev({ key: 'k', code: 'KeyK', metaKey: true }), 'other')).toBeNull()
  })

  it('ignores a lone modifier and unsupported keys', () => {
    expect(specFromEvent(ev({ key: 'Meta', code: 'MetaLeft', metaKey: true }), 'mac')).toBeNull()
    expect(specFromEvent(ev({ key: 'Insert', code: 'Insert', metaKey: true }), 'mac')).toBeNull()
    // ⌘Tab records, but is the app switcher and can't be used
    expect(overrideProblem(specFromEvent(ev({ key: 'Tab', code: 'Tab', metaKey: true }), 'mac')!, 'mac')).toEqual({ kind: 'blocked' })
  })

  it('refuses keys that type, edit or belong to the system', () => {
    expect(overrideProblem('k', 'mac')).toEqual({ kind: 'needs-modifier' })
    expect(overrideProblem('shift+k', 'mac')).toEqual({ kind: 'needs-modifier' })
    expect(overrideProblem('f5', 'mac')).toBeNull()
    expect(overrideProblem('mod+c', 'mac')).toEqual({ kind: 'blocked' })
    expect(overrideProblem('mod+q', 'mac')).toEqual({ kind: 'blocked' })
    expect(overrideProblem('mod+q', 'other')).toBeNull()
    expect(overrideProblem('mod+0', 'other')).toEqual({ kind: 'blocked' })
    expect(overrideProblem('mod+j', 'mac')).toBeNull()
    expect(overrideProblem('mod+r', 'mac')).toEqual({ kind: 'blocked' }) // reload (View menu, browsers)
    expect(overrideProblem('alt+shift+mod+v', 'mac')).toEqual({ kind: 'blocked' })
    expect(browserMayReserve('mod+t')).toBe(true)
    expect(browserMayReserve('shift+mod+n')).toBe(true)
    expect(browserMayReserve('mod+j')).toBe(false)
  })

  it('normalises modifier order', () => {
    expect(canonicalSpec('mod+shift+alt+k')).toBe(canonicalSpec('alt+shift+mod+k'))
  })
})

describe('applying overrides', () => {
  it('uses the user’s key, exactly as recorded (no browser fallback added)', () => {
    expect(shortcutLabel('session.new', 'mac', 'web')).toBe('⌥⌘N')
    setShortcutOverride('session.new', 'mod+j', 'mac')
    expect(shortcutLabel('session.new', 'mac', 'web')).toBe('⌘J')
    expect(shortcutLabel('session.new', 'mac', 'wails')).toBe('⌘J')
    // Windows/Linux keep the default
    expect(shortcutLabel('session.new', 'other', 'web')).toBe('Ctrl+Alt+N')
  })

  it('removes a key ("") and leaves labels and aria empty', () => {
    setShortcutOverride('overview.toggle', '', 'mac')
    expect(effectiveChord(shortcutDef('overview.toggle'), 'mac', 'web')).toBeNull()
    expect(shortcutLabel('overview.toggle', 'mac', 'web')).toBe('')
    expect(shortcutAria('overview.toggle', 'mac', 'web')).toBeUndefined()
    expect(useShortcutLabels().title('Agent overview', 'overview.toggle')).toBe('Agent overview')
  })

  it('never overrides fixed shortcuts', () => {
    expect(isCustomizable('session.jump')).toBe(false)
    expect(isCustomizable('palette.open')).toBe(true)
    const doc = sanitizeOverrides({ mac: { 'session.jump': 'mod+j', 'nope.id': 'mod+k', 'palette.open': 'mod+j', 'agent.stop': 'k', 'overview.toggle': '' } }, isCustomizable)
    expect(doc.mac).toEqual({ 'palette.open': 'mod+j', 'overview.toggle': '' })
    expect(sanitizeOverrides('garbage', isCustomizable)).toEqual({ version: 1, mac: {}, other: {} })
  })

  it('takes the key from another shortcut when asked', () => {
    setShortcutOverride('session.new', 'mod+k', 'mac', ['palette.open'])
    expect(shortcutLabel('session.new', 'mac', 'wails')).toBe('⌘K')
    expect(shortcutLabel('palette.open', 'mac', 'wails')).toBe('')
    resetShortcutOverrides('mac')
    expect(shortcutLabel('palette.open', 'mac', 'wails')).toBe('⌘K')
  })

  it('the dispatcher runs the shortcut on its new key and not on the old one', () => {
    const run = vi.fn()
    registerShortcutHandler('overview.toggle', run)
    setShortcutOverride('overview.toggle', 'mod+j', 'mac')
    press({ key: 'o', code: 'KeyO', metaKey: true })
    expect(run).not.toHaveBeenCalled()
    press({ key: 'j', code: 'KeyJ', metaKey: true })
    expect(run).toHaveBeenCalledTimes(1)
  })

  it('a key without ⌘/Ctrl does not fire while typing', () => {
    const run = vi.fn()
    registerShortcutHandler('overview.toggle', run)
    setShortcutOverride('overview.toggle', 'alt+j', 'mac')
    const input = document.createElement('input')
    press({ key: '∆', code: 'KeyJ', altKey: true }, input)
    expect(run).not.toHaveBeenCalled()
    press({ key: '∆', code: 'KeyJ', altKey: true })
    expect(run).toHaveBeenCalledTimes(1)
  })

  it('default keys still follow the catalogue in text fields (Alt+↑ on Windows/Linux)', () => {
    __setShortcutEnv({ platform: 'other', shell: 'web' })
    const run = vi.fn()
    registerShortcutHandler('session.up', run)
    press({ key: 'ArrowUp', code: 'ArrowUp', altKey: true }, document.createElement('textarea'))
    expect(run).toHaveBeenCalledTimes(1)
  })

  it('stores to localStorage and tells listeners', () => {
    const fn = vi.fn()
    onShortcutOverridesChange(fn)
    setShortcutOverride('agent.stop', 'mod+alt+.', 'mac')
    expect(fn).toHaveBeenCalledTimes(1)
    expect(JSON.parse(localStorage.getItem(SHORTCUT_OVERRIDES_STORAGE_KEY)!).mac['agent.stop']).toBe('alt+mod+.')
  })
})

describe('desktop menu', () => {
  it('sends only changed menu shortcuts', () => {
    setShortcutOverride('session.new', 'mod+j', 'mac')
    setShortcutOverride('zen.fly', 'mod+alt+k', 'mac') // not a menu item
    setShortcutOverride('settings.open', '', 'mac')
    expect(desktopMenuOverrides('mac')).toEqual({ 'session.new': 'mod+j', 'settings.open': '' })
  })

  it('a menu key without ⌘/Ctrl goes to the page, which skips text fields', () => {
    __setShortcutEnv({ platform: 'mac', shell: 'wails' })
    setShortcutOverride('overview.toggle', 'alt+2', 'mac')
    expect(desktopMenuOverrides('mac')).toEqual({ 'overview.toggle': '' })
    expect(ownedByDesktopMenu(shortcutDef('overview.toggle'), 'wails', 'mac')).toBe(false)
    const run = vi.fn()
    registerShortcutHandler('overview.toggle', run)
    const typing = press({ key: '@', code: 'Digit2', altKey: true }, document.createElement('textarea'))
    expect(run).not.toHaveBeenCalled()
    expect(typing.defaultPrevented).toBe(false)
    press({ key: '@', code: 'Digit2', altKey: true })
    expect(run).toHaveBeenCalledTimes(1)
  })

  it('the page handles changed keys itself when the menu could not take them', () => {
    const def = shortcutDef('session.new')
    setShortcutOverride('session.new', 'mod+j', 'mac')
    expect(ownedByDesktopMenu(def, 'wails', 'mac')).toBe(true)
    setDesktopMenuHasOwnKeys(false)
    expect(ownedByDesktopMenu(def, 'wails', 'mac')).toBe(false)
    expect(ownedByDesktopMenu(shortcutDef('palette.open'), 'wails', 'mac')).toBe(true)
  })
})

describe('sync with the server', () => {
  const okJSON = (body: unknown) => new Response(JSON.stringify(body), { status: 200 })

  it('the server’s copy wins over the cache; changes are saved back', async () => {
    localStorage.setItem(SHORTCUT_OVERRIDES_STORAGE_KEY, JSON.stringify({ version: 1, mac: { 'palette.open': 'mod+j' }, other: {} }))
    const calls: Array<[string, RequestInit | undefined]> = []
    const fetcher = vi.fn(async (url: string, init?: RequestInit) => {
      calls.push([url, init])
      if (init?.method === 'GET') return okJSON({ value: JSON.stringify({ version: 1, mac: { 'palette.open': 'mod+l' }, other: {} }) })
      return okJSON({})
    })
    const menu = vi.fn(async () => true)
    const stop = startShortcutOverridesSync({ fetcher, platform: 'mac', shell: 'wails', setMenuShortcuts: menu })
    expect(shortcutLabel('palette.open', 'mac', 'wails')).toBe('⌘J') // cache, at once
    expect(await loadShortcutOverrides()).toBe(true)
    expect(shortcutLabel('palette.open', 'mac', 'wails')).toBe('⌘L')
    expect(menu).toHaveBeenLastCalledWith({ 'palette.open': 'mod+l' })
    expect(calls.some(([, i]) => i?.method === 'PUT')).toBe(false) // loading doesn't save

    setShortcutOverride('palette.open', null, 'mac')
    await Promise.resolve()
    const put = calls.find(([, i]) => i?.method === 'PUT')!
    expect(put[0]).toBe('/api/settings/keyboard_shortcuts')
    expect(JSON.parse(JSON.parse(put[1]!.body as string).value)).toEqual({ version: 1, mac: {}, other: {} })
    stop()
  })

  it('a change made while the server read is in flight is kept', async () => {
    let answer!: (r: Response) => void
    const fetcher = vi.fn((_url: string, init?: RequestInit) => init?.method === 'GET'
      ? new Promise<Response>((r) => { answer = r })
      : Promise.resolve(okJSON({})))
    const stop = startShortcutOverridesSync({ fetcher, platform: 'mac', shell: 'web' })
    const loading = loadShortcutOverrides()
    setShortcutOverride('palette.open', 'mod+j', 'mac')
    answer(okJSON({ value: JSON.stringify({ version: 1, mac: {}, other: {} }) }))
    await loading
    expect(shortcutLabel('palette.open', 'mac', 'web')).toBe('⌘J')
    stop()
  })

  it('a change whose save failed is sent again instead of being replaced', async () => {
    let fail = true
    const fetcher = vi.fn(async (_url: string, init?: RequestInit) => {
      if (init?.method === 'GET') return okJSON({ value: JSON.stringify({ version: 1, mac: {}, other: {} }) })
      return fail ? new Response('{}', { status: 500 }) : okJSON({})
    })
    const stop = startShortcutOverridesSync({ fetcher, platform: 'mac', shell: 'web' })
    setShortcutOverride('palette.open', 'mod+j', 'mac')
    await new Promise(r => setTimeout(r, 0))
    expect(localStorage.getItem(SHORTCUT_OVERRIDES_PENDING_KEY)).toBe('1')
    fail = false
    expect(await loadShortcutOverrides()).toBe(true)
    expect(shortcutLabel('palette.open', 'mac', 'web')).toBe('⌘J')
    expect(fetcher.mock.calls.some(([, i]) => i?.method === 'GET')).toBe(false)
    expect(localStorage.getItem(SHORTCUT_OVERRIDES_PENDING_KEY)).toBeNull()
    stop()
  })

  it('desktop menu updates run one at a time, newest keys last', async () => {
    const seen: Array<Record<string, string>> = []
    let release!: () => void
    const menu = vi.fn((o: Record<string, string>) => {
      seen.push(o)
      return seen.length === 1 ? new Promise<boolean>((r) => { release = () => r(true) }) : Promise.resolve(true)
    })
    const fetcher = vi.fn(async () => okJSON({}))
    const stop = startShortcutOverridesSync({ fetcher, platform: 'mac', shell: 'wails', setMenuShortcuts: menu })
    setShortcutOverride('palette.open', 'mod+j', 'mac')
    setShortcutOverride('palette.open', 'mod+l', 'mac')
    await new Promise(r => setTimeout(r, 0))
    expect(menu).toHaveBeenCalledTimes(1) // the first (startup) call is still running
    release()
    await new Promise(r => setTimeout(r, 0))
    expect(seen[seen.length - 1]).toEqual({ 'palette.open': 'mod+l' })
    stop()
  })

  it('no stored setting (404) means defaults', async () => {
    setShortcutOverrides({ version: 1, mac: { 'palette.open': 'mod+j' }, other: {} }, { notify: false })
    const fetcher = vi.fn(async () => new Response('{}', { status: 404 }))
    const stop = startShortcutOverridesSync({ fetcher, platform: 'mac', shell: 'web' })
    await loadShortcutOverrides()
    expect(shortcutOverrides().mac).toEqual({})
    stop()
  })
})

describe('Settings → Keyboard shortcuts', () => {
  const keyButton = (w: ReturnType<typeof mount>, label: string) =>
    w.findAll('button.ks-key').find(b => (b.attributes('aria-label') || '').startsWith(`${label}:`))!

  const record = async (init: KeyboardEventInit) => {
    window.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init }))
    await nextTick()
  }

  it('records a new key', async () => {
    const w = mount(KeyboardShortcuts, { attachTo: document.body })
    await keyButton(w, 'Agent overview').trigger('click')
    expect(w.text()).toContain('Press keys…')
    await record({ key: 'j', code: 'KeyJ', metaKey: true })
    expect(shortcutLabel('overview.toggle', 'mac', 'web')).toBe('⌘J')
    expect(keyButton(w, 'Agent overview').text()).toBe('⌘J')
    expect(w.text()).toContain('Default: ⌘O')
    w.unmount()
  })

  it('asks before taking another shortcut’s key, then removes it there', async () => {
    const w = mount(KeyboardShortcuts, { attachTo: document.body })
    await keyButton(w, 'Agent overview').trigger('click')
    await record({ key: 'k', code: 'KeyK', metaKey: true })
    expect(w.text()).toContain('is used by Command palette')
    expect(shortcutLabel('overview.toggle', 'mac', 'web')).toBe('⌘O') // not yet
    await w.findAll('button.ks-link').find(b => b.text().startsWith('Use it here'))!.trigger('click')
    expect(shortcutLabel('overview.toggle', 'mac', 'web')).toBe('⌘K')
    expect(shortcutLabel('palette.open', 'mac', 'web')).toBe('')
    w.unmount()
  })

  it('checks the desktop app’s keys too when recording in a browser tab', async () => {
    // In a tab New session is ⌥⌘N, but ⌘N in the desktop app
    const w = mount(KeyboardShortcuts, { attachTo: document.body })
    await keyButton(w, 'Command palette').trigger('click')
    await record({ key: 'n', code: 'KeyN', metaKey: true })
    expect(w.text()).toContain('is used by New session')
    w.unmount()
  })

  it('explains a refused key and Escape cancels', async () => {
    const w = mount(KeyboardShortcuts, { attachTo: document.body })
    await keyButton(w, 'Agent overview').trigger('click')
    await record({ key: 'c', code: 'KeyC', metaKey: true })
    expect(w.find('.ks-error').text()).toContain('editing or the system')
    await record({ key: 'Escape', code: 'Escape' })
    expect(w.text()).not.toContain('Press keys…')
    expect(shortcutLabel('overview.toggle', 'mac', 'web')).toBe('⌘O')
    w.unmount()
  })

  it('recording the default again drops the override', async () => {
    setShortcutOverride('overview.toggle', 'mod+j', 'mac')
    const w = mount(KeyboardShortcuts, { attachTo: document.body })
    await keyButton(w, 'Agent overview').trigger('click')
    await record({ key: 'o', code: 'KeyO', metaKey: true })
    expect(shortcutOverrides().mac).toEqual({})
    w.unmount()
  })

  it('lists every shortcut, fixed ones without a key button', () => {
    const w = mount(KeyboardShortcuts)
    const fixed = SHORTCUTS.filter(s => s.customizable === false).length
    expect(w.findAll('button.ks-key').length).toBe(SHORTCUTS.length - fixed)
    expect(w.findAll('.ks-key--fixed').length).toBe(fixed)
    w.unmount()
  })
})

describe('Tab keys', () => {
  it('records ⌃Tab and refuses Tab with ⌘, ⌥ or alone', () => {
    expect(specFromEvent(ev({ key: 'Tab', code: 'Tab', ctrlKey: true }), 'mac')).toBe('ctrl+tab')
    expect(overrideProblem('ctrl+tab', 'mac')).toBeNull()
    expect(overrideProblem('ctrl+shift+tab', 'mac')).toBeNull()
    expect(overrideProblem('mod+tab', 'mac')).toEqual({ kind: 'blocked' })
    expect(overrideProblem('ctrl+alt+tab', 'mac')).toEqual({ kind: 'blocked' })
    expect(overrideProblem('shift+tab', 'mac')).toEqual({ kind: 'needs-modifier' })
    // Ctrl is "mod" off macOS
    expect(specFromEvent(ev({ key: 'Tab', code: 'Tab', ctrlKey: true }), 'other')).toBe('mod+tab')
    expect(overrideProblem('mod+tab', 'other')).toBeNull()
    expect(overrideProblem('alt+tab', 'other')).toEqual({ kind: 'blocked' })
  })
})

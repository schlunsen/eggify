import { computed, defineComponent, h, readonly, ref, onMounted } from 'vue'
import { mount } from '@vue/test-utils'
import { availableThemes, cycleThemeId, pickerThemes, type Theme } from '../../composables/useTheme'
import { installShortcutDispatcher, __resetShortcuts, __setShortcutEnv } from '../../composables/useKeyboardShortcuts'
import { shortcutLabel } from '../../utils/shortcuts/catalog'

// useTheme relies on Nuxt auto-imports; provide them for the test
const states = new Map<string, ReturnType<typeof ref>>()
let uninstall: () => void
beforeEach(() => {
  states.clear()
  vi.stubGlobal('useState', (key: string, init: () => unknown) => {
    if (!states.has(key)) states.set(key, ref(init()))
    return states.get(key)
  })
  vi.stubGlobal('computed', computed)
  vi.stubGlobal('readonly', readonly)
  vi.stubGlobal('onMounted', onMounted)
  __resetShortcuts()
  uninstall = installShortcutDispatcher(window)
})
afterEach(() => {
  uninstall()
  __setShortcutEnv({})
  vi.unstubAllGlobals()
  localStorage.clear()
  document.documentElement.removeAttribute('data-theme')
})

const fire = (init: KeyboardEventInit, target: EventTarget = document.body) =>
  target.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init }))

const t = (id: string, hidden = false): Theme =>
  ({ id: id as Theme['id'], name: id, description: '', isDark: false, fontFamily: '', fontDescription: '', hidden })

describe('cycleThemeId', () => {
  const list = [t('a'), t('b'), t('c')]

  it('goes to the next and previous theme and wraps around', () => {
    expect(cycleThemeId('a', 1, list)).toBe('b')
    expect(cycleThemeId('c', 1, list)).toBe('a')
    expect(cycleThemeId('a', -1, list)).toBe('c')
    expect(cycleThemeId('b', -1, list)).toBe('a')
  })

  it('starts from the applied theme when the current one is not in the list (auto / hidden)', () => {
    expect(cycleThemeId('auto', 1, list, 'b')).toBe('c')
    expect(cycleThemeId(null, -1, list, 'b')).toBe('a')
    // Nothing known: next is the first, previous the last
    expect(cycleThemeId('auto', 1, list, 'zzz')).toBe('a')
    expect(cycleThemeId(undefined, -1, list)).toBe('c')
  })

  it('uses the pickers\' list and order, without hidden themes', () => {
    expect(pickerThemes.map(x => x.id)).toEqual(availableThemes.filter(x => !x.hidden).map(x => x.id))
    expect(cycleThemeId(pickerThemes[0]!.id, 1)).toBe(pickerThemes[1]!.id)
    const withHidden = [t('a'), t('x', true), t('b')].filter(x => !x.hidden)
    expect(cycleThemeId('a', 1, withHidden)).toBe('b')
  })

  it('goes through every picker theme once before coming back', () => {
    let id: string = pickerThemes[0]!.id
    const seen = new Set<string>()
    for (let i = 0; i < pickerThemes.length; i++) { seen.add(id); id = cycleThemeId(id, 1) }
    expect(seen.size).toBe(pickerThemes.length)
    expect(id).toBe(pickerThemes[0]!.id)
  })
})

describe('useTheme().cycleTheme', () => {
  it('applies and saves the next theme like the Settings picker', async () => {
    const { useTheme } = await import('../../composables/useTheme')
    const theme = useTheme()
    expect(theme.currentTheme.value).toBe(pickerThemes[0]!.id)
    const next = theme.cycleTheme(1)
    expect(next.id).toBe(pickerThemes[1]!.id)
    expect(theme.currentTheme.value).toBe(pickerThemes[1]!.id)
    expect(document.documentElement.getAttribute('data-theme')).toBe(pickerThemes[1]!.id)
    expect(localStorage.getItem('eggify-theme')).toBe(pickerThemes[1]!.id)
    theme.cycleTheme(-1)
    theme.cycleTheme(-1)
    expect(localStorage.getItem('eggify-theme')).toBe(pickerThemes[pickerThemes.length - 1]!.id)
  })

  it('cross-fades with a view transition when the browser has one', async () => {
    const startViewTransition = vi.fn((cb: () => void) => cb())
    ;(document as any).startViewTransition = startViewTransition
    try {
      const { useTheme } = await import('../../composables/useTheme')
      useTheme().cycleTheme(1)
      expect(startViewTransition).toHaveBeenCalledTimes(1)
      expect(localStorage.getItem('eggify-theme')).toBe(pickerThemes[1]!.id)
    } finally {
      delete (document as any).startViewTransition
    }
  })
})

describe('theme shortcuts', () => {
  async function mountShortcuts() {
    const { useThemeShortcuts, themeToast } = await import('../../composables/useThemeShortcuts')
    const w = mount(defineComponent({ setup() { useThemeShortcuts(); return () => h('div') } }))
    return { w, themeToast }
  }

  it('⌥⌘] / ⌥⌘[ cycle themes, also while typing, and show the name', async () => {
    __setShortcutEnv({ platform: 'mac', shell: 'web' })
    const { w, themeToast } = await mountShortcuts()
    const composer = document.createElement('textarea')
    document.body.append(composer)
    const ev = new KeyboardEvent('keydown', { key: '‘', code: 'BracketRight', metaKey: true, altKey: true, bubbles: true, cancelable: true })
    composer.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(true)
    expect(localStorage.getItem('eggify-theme')).toBe(pickerThemes[1]!.id)
    expect(themeToast.value?.theme.name).toBe(pickerThemes[1]!.name)
    fire({ key: '“', code: 'BracketLeft', metaKey: true, altKey: true })
    fire({ key: '“', code: 'BracketLeft', metaKey: true, altKey: true })
    expect(localStorage.getItem('eggify-theme')).toBe(pickerThemes[pickerThemes.length - 1]!.id)
    composer.remove()
    w.unmount()
  })

  it('uses Ctrl+Alt+Y on Windows/Linux (Ctrl+Alt+T opens a terminal there)', async () => {
    __setShortcutEnv({ platform: 'other', shell: 'web' })
    expect(shortcutLabel('theme.next', 'other', 'web')).toBe('Ctrl+Alt+Y')
    expect(shortcutLabel('theme.previous', 'other', 'web')).toBe('Ctrl+Alt+Shift+Y')
    expect(shortcutLabel('theme.next', 'mac', 'web')).toBe('⌥⌘]')
    const { w } = await mountShortcuts()
    fire({ key: 'y', code: 'KeyY', ctrlKey: true, altKey: true })
    expect(localStorage.getItem('eggify-theme')).toBe(pickerThemes[1]!.id)
    w.unmount()
  })

  it('leaves the key to the native menu in the desktop app', async () => {
    __setShortcutEnv({ platform: 'mac', shell: 'wails' })
    const { w } = await mountShortcuts()
    const ev = new KeyboardEvent('keydown', { key: '‘', code: 'BracketRight', metaKey: true, altKey: true, bubbles: true, cancelable: true })
    document.body.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(false) // the menu accelerator gets it
    expect(document.documentElement.getAttribute('data-theme')).not.toBe(pickerThemes[1]!.id)
    const { runShortcut } = await import('../../composables/useKeyboardShortcuts')
    expect(runShortcut('theme.next', { source: 'menu' })).toBe(true)
    expect(localStorage.getItem('eggify-theme')).toBe(pickerThemes[1]!.id)
    w.unmount()
  })
})

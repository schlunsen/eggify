import { resolveChord, withBrowserFallback, formatChord, chordParts, matchChord, eventKeys, ariaKeyshortcuts, detectPlatform, parseSpec } from '../../utils/shortcuts/keys'
import { SHORTCUTS, effectiveChord, defaultChord, shortcutLabel } from '../../utils/shortcuts/catalog'
import type { Platform, Shell } from '../../utils/shortcuts/keys'

const key = (init: KeyboardEventInit) => new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init })

describe('mod mapping', () => {
  it('is Command on macOS and Ctrl on Windows/Linux', () => {
    expect(resolveChord('mod+k', 'mac')).toEqual({ meta: true, ctrl: false, alt: false, shift: false, key: 'k' })
    expect(resolveChord('mod+k', 'other')).toEqual({ meta: false, ctrl: true, alt: false, shift: false, key: 'k' })
    expect(resolveChord('ctrl+mod+f', 'mac')).toMatchObject({ meta: true, ctrl: true })
  })

  it('labels per platform', () => {
    expect(formatChord(resolveChord('mod+shift+n', 'mac'), 'mac')).toBe('⇧⌘N')
    expect(formatChord(resolveChord('mod+shift+n', 'other'), 'other')).toBe('Ctrl+Shift+N')
    expect(formatChord(resolveChord('mod+shift+enter', 'mac'), 'mac')).toBe('⇧⌘↩')
    expect(formatChord(resolveChord('mod+.', 'other'), 'other')).toBe('Ctrl+.')
    expect(chordParts(resolveChord('ctrl+mod+f', 'mac'), 'mac')).toEqual(['⌃', '⌘', 'F'])
    expect(formatChord(resolveChord('mod+1-9', 'mac'), 'mac')).toBe('⌘1…9')
    expect(ariaKeyshortcuts(resolveChord('mod+shift+m', 'mac'))).toBe('Shift+Meta+M')
    expect(ariaKeyshortcuts(resolveChord('mod+shift+m', 'other'))).toBe('Control+Shift+M')
  })

  it('detects the platform', () => {
    expect(detectPlatform({ platform: 'MacIntel' })).toBe('mac')
    expect(detectPlatform({ userAgentData: { platform: 'Windows' } })).toBe('other')
    expect(detectPlatform({ platform: 'Linux x86_64' })).toBe('other')
  })

  it('rejects unknown modifiers', () => {
    expect(() => parseSpec('hyper+k')).toThrow()
  })
})

describe('browser fallback', () => {
  it('adds Option/Alt to browser-reserved keys only in a browser tab', () => {
    const def = SHORTCUTS.find(s => s.id === 'session.new')!
    expect(effectiveChord(def, 'mac', 'wails')).toMatchObject({ meta: true, alt: false, key: 'n' })
    expect(effectiveChord(def, 'mac', 'web')).toMatchObject({ meta: true, alt: true, key: 'n' })
    expect(shortcutLabel('session.new', 'mac', 'web')).toBe('⌥⌘N')
    expect(shortcutLabel('session.new', 'other', 'web')).toBe('Ctrl+Alt+N')
    expect(shortcutLabel('session.new', 'mac', 'wails')).toBe('⌘N')
    expect(shortcutLabel('session.jump', 'mac', 'web')).toBe('⌥⌘1…9')
    // Not reserved: same everywhere
    expect(shortcutLabel('palette.open', 'mac', 'web')).toBe('⌘K')
    expect(withBrowserFallback(resolveChord('mod+t', 'mac')).alt).toBe(true)
  })
})

describe('matching events', () => {
  it('matches ⌘K and ignores extra or missing modifiers', () => {
    const c = resolveChord('mod+k', 'mac')
    expect(matchChord(c, key({ key: 'k', code: 'KeyK', metaKey: true }), 'mac')).toEqual({})
    expect(matchChord(c, key({ key: 'k', code: 'KeyK', metaKey: true, shiftKey: true }), 'mac')).toBeNull()
    expect(matchChord(c, key({ key: 'k', code: 'KeyK' }), 'mac')).toBeNull()
    expect(matchChord(c, key({ key: 'k', code: 'KeyK', ctrlKey: true }), 'mac')).toBeNull()
  })

  it('uses the physical key when Option changes the character (⌥⌘N gives "˜")', () => {
    const c = withBrowserFallback(resolveChord('mod+n', 'mac'))
    expect(matchChord(c, key({ key: '˜', code: 'KeyN', metaKey: true, altKey: true }), 'mac')).toEqual({})
    const digits = withBrowserFallback(resolveChord('mod+1-9', 'mac'))
    expect(matchChord(digits, key({ key: '¡', code: 'Digit1', metaKey: true, altKey: true }), 'mac')).toEqual({ digit: 1 })
  })

  it('reports the digit for ⌘1…⌘9 and ignores ⌘0', () => {
    const c = resolveChord('mod+1-9', 'mac')
    expect(matchChord(c, key({ key: '7', code: 'Digit7', metaKey: true }), 'mac')).toEqual({ digit: 7 })
    expect(matchChord(c, key({ key: '0', code: 'Digit0', metaKey: true }), 'mac')).toBeNull()
  })

  it('matches ⌘/ where "/" needs Shift (Nordic/German layouts)', () => {
    const c = resolveChord('mod+/', 'mac')
    expect(matchChord(c, key({ key: '/', code: 'Digit7', metaKey: true, shiftKey: true }), 'mac')).toEqual({})
    expect(matchChord(c, key({ key: '/', code: 'Slash', metaKey: true }), 'mac')).toEqual({})
  })

  it('prefers the layout letter over the physical key (AZERTY ⌘A)', () => {
    expect(eventKeys({ key: 'a', code: 'KeyQ' })).toEqual(['a'])
    expect(eventKeys({ key: 'ƒ', code: 'KeyF' })).toEqual(['f'])
  })

  it('never treats AltGr typing as a Ctrl+Alt shortcut on Windows/Linux', () => {
    const c = withBrowserFallback(resolveChord('mod+1-9', 'other'))
    // German AltGr+7 types "{"
    expect(matchChord(c, key({ key: '{', code: 'Digit7', ctrlKey: true, altKey: true }), 'other')).toBeNull()
    // Polish AltGr+N types "ń"
    const altGr = key({ key: 'ń', code: 'KeyN', ctrlKey: true, altKey: true })
    expect(matchChord(withBrowserFallback(resolveChord('mod+n', 'other')), altGr, 'other')).toBeNull()
    expect(matchChord(withBrowserFallback(resolveChord('mod+n', 'other')), key({ key: 'n', code: 'KeyN', ctrlKey: true, altKey: true }), 'other')).toEqual({})
  })

  it('Option+digit (typing @ or { on a Nordic Mac) matches nothing', () => {
    for (const ev of [key({ key: '@', code: 'Digit2', altKey: true }), key({ key: '{', code: 'Digit8', altKey: true, shiftKey: true })]) {
      for (const def of SHORTCUTS) {
        expect(matchChord(defaultChord(def, 'mac', 'web'), ev, 'mac')).toBeNull()
      }
    }
  })
})

describe('catalogue', () => {
  const combos: Array<[Platform, Shell]> = [['mac', 'wails'], ['mac', 'web'], ['other', 'web'], ['other', 'tauri']]
  it.each(combos)('has no two shortcuts on the same keys (%s, %s)', (platform, shell) => {
    const seen = new Map<string, string>()
    for (const def of SHORTCUTS) {
      const label = formatChord(defaultChord(def, platform, shell), platform)
      expect(seen.get(label), `${def.id} and ${seen.get(label)} both use ${label}`).toBeUndefined()
      seen.set(label, def.id)
    }
  })

  it('lets bare keys fire only outside text fields, chords everywhere', () => {
    for (const def of SHORTCUTS) {
      const chord = defaultChord(def, 'mac', 'wails')
      const isChord = chord.meta || chord.ctrl
      if (def.id === 'voice.finish') continue // modal-only, while recording
      expect(def.allowInInputs, def.id).toBe(isChord)
    }
  })

  it('never binds Escape (it only closes things)', () => {
    expect(SHORTCUTS.some(s => /escape/.test(s.keys.mac) || /escape/.test(s.keys.other || ''))).toBe(false)
  })
})

describe('arrow keys', () => {
  it('parses, labels and matches ⌥⌘↑ / Alt+↓', () => {
    const up = resolveChord('mod+alt+up', 'mac')
    expect(formatChord(up, 'mac')).toBe('⌥⌘↑')
    expect(ariaKeyshortcuts(up)).toBe('Alt+Meta+ArrowUp')
    expect(formatChord(resolveChord('alt+down', 'other'), 'other')).toBe('Alt+↓')
    const ev = (o: Partial<KeyboardEvent>) => new KeyboardEvent('keydown', { key: 'ArrowUp', code: 'ArrowUp', ...o })
    expect(matchChord(up, ev({ metaKey: true, altKey: true }), 'mac')).toEqual({})
    expect(matchChord(up, ev({ metaKey: true }), 'mac')).toBeNull()
    expect(matchChord(up, ev({ metaKey: true, altKey: true, key: 'ArrowDown', code: 'ArrowDown' }), 'mac')).toBeNull()
  })

  it('parses, labels and matches bare ←/→ and Home/End (switching subagents)', () => {
    expect(shortcutLabel('subagent.prev', 'mac', 'web')).toBe('←')
    expect(shortcutLabel('subagent.next', 'other', 'web')).toBe('→')
    expect(shortcutLabel('subagent.first', 'mac', 'wails')).toBe('Home')
    expect(shortcutLabel('subagent.last', 'other', 'tauri')).toBe('End')
    expect(ariaKeyshortcuts(resolveChord('home', 'mac'))).toBe('Home')
    const home = resolveChord('home', 'mac')
    expect(matchChord(home, key({ key: 'Home', code: 'Home' }), 'mac')).toEqual({})
    expect(matchChord(home, key({ key: 'End', code: 'End' }), 'mac')).toBeNull()
    expect(matchChord(home, key({ key: 'Home', code: 'Home', metaKey: true }), 'mac')).toBeNull()
  })
})

describe('Tab (switch machine)', () => {
  it('matches ⌃Tab and ⌃⇧Tab and labels them with ⇥', () => {
    const next = resolveChord('ctrl+tab', 'mac')
    const prev = resolveChord('ctrl+shift+tab', 'mac')
    expect(matchChord(next, key({ key: 'Tab', code: 'Tab', ctrlKey: true }), 'mac')).toEqual({})
    expect(matchChord(next, key({ key: 'Tab', code: 'Tab', ctrlKey: true, shiftKey: true }), 'mac')).toBeNull()
    expect(matchChord(prev, key({ key: 'Tab', code: 'Tab', ctrlKey: true, shiftKey: true }), 'mac')).toEqual({})
    expect(eventKeys({ key: 'Tab', code: 'Tab' })).toEqual(['tab'])
    expect(formatChord(next, 'mac')).toBe('⌃⇥')
    expect(formatChord(prev, 'other')).toBe('Ctrl+Shift+Tab')
    expect(ariaKeyshortcuts(prev)).toBe('Control+Shift+Tab')
  })
})

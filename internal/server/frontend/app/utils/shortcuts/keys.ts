/**
 * Key chords: parsing, matching keyboard events and per-platform labels.
 *
 * Pure functions (no Vue, no DOM globals beyond what is passed in), so the
 * rules are unit-tested in app/__tests__/utils/shortcutKeys.test.ts.
 *
 * A key spec is a string like "mod+shift+n":
 *   mod   – Command on macOS, Ctrl on Windows/Linux
 *   ctrl  – the Control key itself (⌃ on macOS)
 *   alt   – Option on macOS, Alt elsewhere
 *   shift
 * and a final key: a letter, a digit, "1-9" (any digit 1–9), one of
 * [ ] . / , or a name: enter, space, escape, up, down, left, right, home, end,
 * f1…f12.
 */

export type Platform = 'mac' | 'other'
export type Shell = 'wails' | 'tauri' | 'web'

export interface Chord {
  meta: boolean
  ctrl: boolean
  alt: boolean
  shift: boolean
  /** Normalised key: "k", "1", "1-9", "[", ".", "enter", "space", "f11"… */
  key: string
}

export interface KeySpec {
  mod: boolean
  ctrl: boolean
  alt: boolean
  shift: boolean
  key: string
}

export function detectPlatform(nav: { platform?: string; userAgent?: string; userAgentData?: { platform?: string } } | undefined =
  typeof navigator !== 'undefined' ? (navigator as any) : undefined): Platform {
  if (!nav) return 'mac'
  const p = nav.userAgentData?.platform || nav.platform || nav.userAgent || ''
  return /mac|iphone|ipad|ipod/i.test(p) ? 'mac' : 'other'
}

export function parseSpec(spec: string): KeySpec {
  const parts = spec.toLowerCase().split('+')
  // "mod++" is not used; a trailing empty part would mean the "+" key
  const key = parts.pop() || ''
  const out: KeySpec = { mod: false, ctrl: false, alt: false, shift: false, key }
  for (const p of parts) {
    if (p === 'mod') out.mod = true
    else if (p === 'ctrl') out.ctrl = true
    else if (p === 'alt' || p === 'option') out.alt = true
    else if (p === 'shift') out.shift = true
    else throw new Error(`unknown modifier "${p}" in "${spec}"`)
  }
  if (!key) throw new Error(`no key in "${spec}"`)
  return out
}

/** The concrete chord for a spec on a platform (mod → ⌘ or Ctrl). */
export function resolveChord(spec: string | KeySpec, platform: Platform): Chord {
  const s = typeof spec === 'string' ? parseSpec(spec) : spec
  return {
    meta: platform === 'mac' ? s.mod : false,
    ctrl: platform === 'mac' ? s.ctrl : (s.mod || s.ctrl),
    alt: s.alt,
    shift: s.shift,
    key: s.key
  }
}

/** Browser-reserved keys get Option/Alt added in a plain browser tab. */
export function withBrowserFallback(chord: Chord): Chord {
  return { ...chord, alt: true }
}

const CODE_KEYS: Record<string, string> = {
  BracketLeft: '[', BracketRight: ']', Period: '.', Slash: '/', Comma: ',',
  Enter: 'enter', NumpadEnter: 'enter', Space: 'space', Escape: 'escape',
  Minus: '-', Equal: '=', Semicolon: ';', Quote: "'", Backquote: '`', Backslash: '\\',
  ArrowUp: 'up', ArrowDown: 'down', ArrowLeft: 'left', ArrowRight: 'right', Home: 'home', End: 'end', Tab: 'tab'
}
const PUNCTUATION = new Set(['[', ']', '.', '/', ','])

/**
 * The keys an event could mean: the character the layout produced and the
 * physical key. Option changes event.key on macOS (⌥⌘N gives "˜"), so letters
 * and digits fall back to event.code; punctuation is matched by character
 * first, so ⌘/ works where "/" is Shift+7.
 */
export function eventKeys(e: Pick<KeyboardEvent, 'key' | 'code'>): string[] {
  const out: string[] = []
  const k = (e.key || '').toLowerCase()
  if (k === ' ' || k === 'spacebar') out.push('space')
  else if (k === 'esc') out.push('escape')
  else if (/^arrow(up|down|left|right)$/.test(k)) out.push(k.slice(5))
  else if (/^[a-z0-9]$/.test(k) || PUNCTUATION.has(k) || /^f\d{1,2}$/.test(k) || k === 'enter' || k === 'escape' || k === 'home' || k === 'end' || k === 'tab') out.push(k)
  const code = e.code || ''
  let c = ''
  if (/^Key[A-Z]$/.test(code)) c = code.slice(3).toLowerCase()
  else if (/^Digit\d$/.test(code)) c = code.slice(5)
  else if (/^Numpad\d$/.test(code)) c = code.slice(6)
  else if (CODE_KEYS[code]) c = CODE_KEYS[code]!
  // A layout letter wins over the physical key (AZERTY, Dvorak), but only
  // when the layout actually produced a letter.
  if (c && !(out.length && /^[a-z]$/.test(out[0]!) && /^[a-z]$/.test(c))) out.push(c)
  return [...new Set(out)]
}

export interface MatchResult { digit?: number }

/**
 * Does the event press this chord? Returns null when it does not, otherwise
 * { digit } for "1-9" chords.
 */
export function matchChord(chord: Chord, e: KeyboardEvent, platform: Platform): MatchResult | null {
  if (e.metaKey !== chord.meta || e.ctrlKey !== chord.ctrl || e.altKey !== chord.alt) return null
  // On Windows/Linux Ctrl+Alt is AltGr, which types characters ("{", "ń"):
  // a Ctrl+Alt chord matches only when the key still produced its own
  // character (browsers report AltGraph for plain Ctrl+Alt too, so that flag
  // can't tell them apart). See the e.key checks below.
  const keys = eventKeys(e)
  if (chord.key === '1-9') {
    if (e.shiftKey !== chord.shift) return null
    const d = keys.find(k => /^[1-9]$/.test(k))
    if (!d) return null
    if (platform === 'other' && chord.ctrl && chord.alt && e.key.length === 1 && e.key !== d) return null
    return { digit: Number(d) }
  }
  if (!keys.includes(chord.key)) return null
  if (e.shiftKey !== chord.shift) {
    // "/" is Shift+7 on many layouts: a punctuation chord without Shift still
    // matches when Shift was only needed to type the character.
    if (!(PUNCTUATION.has(chord.key) && !chord.shift && e.key === chord.key)) return null
  }
  if (platform === 'other' && chord.ctrl && chord.alt && e.key.length === 1 && e.key.toLowerCase() !== chord.key) return null
  return {}
}

const ARROW_LABELS: Record<string, string> = { up: '↑', down: '↓', left: '←', right: '→', home: 'Home', end: 'End' }
const MAC_KEY_LABELS: Record<string, string> = { tab: '⇥', enter: '↩', space: 'Space', escape: 'Esc', '1-9': '1…9', ...ARROW_LABELS }
const OTHER_KEY_LABELS: Record<string, string> = { tab: 'Tab', enter: 'Enter', space: 'Space', escape: 'Esc', '1-9': '1…9', ...ARROW_LABELS }

export function keyLabel(key: string, platform: Platform): string {
  const map = platform === 'mac' ? MAC_KEY_LABELS : OTHER_KEY_LABELS
  if (map[key]) return map[key]!
  return key.length === 1 ? key.toUpperCase() : key.toUpperCase()
}

/** The parts of a chord label, e.g. ["⇧", "⌘", "N"] or ["Ctrl", "Shift", "N"]. */
export function chordParts(chord: Chord, platform: Platform): string[] {
  const parts: string[] = []
  if (platform === 'mac') {
    if (chord.ctrl) parts.push('⌃')
    if (chord.alt) parts.push('⌥')
    if (chord.shift) parts.push('⇧')
    if (chord.meta) parts.push('⌘')
  } else {
    if (chord.ctrl) parts.push('Ctrl')
    if (chord.alt) parts.push('Alt')
    if (chord.shift) parts.push('Shift')
    if (chord.meta) parts.push('Win')
  }
  parts.push(keyLabel(chord.key, platform))
  return parts
}

/** "⇧⌘N" on macOS, "Ctrl+Shift+N" elsewhere. */
export function formatChord(chord: Chord, platform: Platform): string {
  const parts = chordParts(chord, platform)
  return platform === 'mac' ? parts.join('') : parts.join('+')
}

const ARIA_KEYS: Record<string, string> = { tab: 'Tab', enter: 'Enter', space: 'Space', escape: 'Escape', '1-9': '1', up: 'ArrowUp', down: 'ArrowDown', left: 'ArrowLeft', right: 'ArrowRight', home: 'Home', end: 'End' }

/** Value for aria-keyshortcuts, e.g. "Shift+Meta+N". */
export function ariaKeyshortcuts(chord: Chord): string {
  const parts: string[] = []
  if (chord.ctrl) parts.push('Control')
  if (chord.alt) parts.push('Alt')
  if (chord.shift) parts.push('Shift')
  if (chord.meta) parts.push('Meta')
  const k = ARIA_KEYS[chord.key] ?? (chord.key.length === 1 ? chord.key.toUpperCase() : chord.key.toUpperCase())
  parts.push(k)
  return parts.join('+')
}

/** Text the user is typing into: inputs, textareas, selects, contenteditable. */
export function isEditableTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName
  if (tag === 'TEXTAREA' || tag === 'SELECT') return true
  if (tag === 'INPUT') {
    const type = ((el as HTMLInputElement).type || 'text').toLowerCase()
    return !['button', 'checkbox', 'radio', 'submit', 'reset', 'range', 'color', 'file', 'image'].includes(type)
  }
  return !!el.isContentEditable
}

/** Focus is in an xterm.js terminal (which owns Ctrl+letter on Windows/Linux). */
export function isTerminalTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  return !!el?.closest?.('.xterm')
}

export const MODIFIER_KEYS = new Set(['Shift', 'Control', 'Alt', 'Meta', 'OS', 'AltGraph', 'CapsLock', 'Fn', 'Hyper', 'Super'])

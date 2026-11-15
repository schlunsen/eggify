/**
 * Your own keyboard shortcuts: overrides of the catalogue's default keys.
 *
 * Kept per platform (a macOS chord means nothing on Windows), as key specs in
 * the catalogue's syntax ("mod+shift+k"); "" means the shortcut has no key.
 * A shortcut without an override uses its default (catalog.json).
 *
 * Stored in the server setting `keyboard_shortcuts` (so the desktop app and
 * every browser tab share them) and cached in localStorage for the first
 * paint. Settings → Keyboard shortcuts is the editor
 * (components/settings/KeyboardShortcuts.vue).
 */
import { shallowRef } from 'vue'
import { parseSpec, resolveChord } from './keys'
import type { Chord, Platform, Shell } from './keys'

export const SHORTCUT_OVERRIDES_SETTING = 'keyboard_shortcuts'
export const SHORTCUT_OVERRIDES_STORAGE_KEY = 'eggify-shortcut-overrides'

export interface ShortcutOverridesDoc {
  version: 1
  mac: Record<string, string>
  other: Record<string, string>
}

const empty = (): ShortcutOverridesDoc => ({ version: 1, mac: {}, other: {} })

/** Reactive: renders that read a label re-run when overrides change. */
const doc = shallowRef<ShortcutOverridesDoc>(empty())

/** Keys a recorded chord may end in (the dispatcher can match all of them). */
const RECORDABLE_KEY = /^([a-z0-9]|f([1-9]|1[0-2])|[[\].\/,;'`\\=-]|enter|space|tab|up|down|left|right)$/

/** Never assignable: editing keys, zoom, and keys the OS or app keeps (⌘Q, ⌘W, ⌘H…). */
const COMMON_BLOCKED = ['mod+c', 'mod+v', 'mod+x', 'mod+a', 'mod+z', 'mod+shift+z', 'mod+w', 'mod+0', 'mod+-', 'mod+=',
  // reload (browser, and the desktop app's View menu); paste and match style (Edit menu)
  'mod+r', 'mod+shift+r', 'mod+alt+shift+v']
const BLOCKED: Record<Platform, string[]> = {
  mac: [...COMMON_BLOCKED, 'mod+q', 'mod+h', 'mod+m', 'mod+alt+h', 'mod+space', 'ctrl+space'],
  other: [...COMMON_BLOCKED, 'mod+y', 'alt+f4']
}

/** Kept by browsers in a plain tab (the page never sees them there). */
const BROWSER_RESERVED = ['mod+n', 'mod+shift+n', 'mod+t', 'mod+shift+t', 'mod+l', 'mod+shift+w', 'mod+shift+p', 'mod+1', 'mod+2', 'mod+3', 'mod+4', 'mod+5', 'mod+6', 'mod+7', 'mod+8', 'mod+9', 'mod+0']

/** Normalised form of a spec: modifiers in a fixed order, then the key. */
export function canonicalSpec(spec: string): string {
  const s = parseSpec(spec)
  const parts: string[] = []
  if (s.ctrl) parts.push('ctrl')
  if (s.alt) parts.push('alt')
  if (s.shift) parts.push('shift')
  if (s.mod) parts.push('mod')
  parts.push(s.key)
  return parts.join('+')
}

/**
 * The spec a key press records, or null for a lone modifier or a key that
 * can't be a shortcut. On macOS ⌘ is "mod" and ⌃ is "ctrl"; elsewhere Ctrl
 * is "mod" and the Windows key is refused (null).
 */
export function specFromEvent(e: Pick<KeyboardEvent, 'key' | 'code' | 'metaKey' | 'ctrlKey' | 'altKey' | 'shiftKey'>, platform: Platform): string | null {
  const key = recordedKey(e)
  if (!key) return null
  const parts: string[] = []
  if (platform === 'mac') {
    if (e.ctrlKey) parts.push('ctrl')
  } else if (e.metaKey) {
    return null
  }
  if (e.altKey) parts.push('alt')
  if (e.shiftKey) parts.push('shift')
  if (platform === 'mac' ? e.metaKey : e.ctrlKey) parts.push('mod')
  parts.push(key)
  return parts.join('+')
}

const CODE_KEY: Record<string, string> = {
  BracketLeft: '[', BracketRight: ']', Period: '.', Slash: '/', Comma: ',', Semicolon: ';', Quote: "'",
  Backquote: '`', Backslash: '\\', Minus: '-', Equal: '=', Enter: 'enter', NumpadEnter: 'enter', Space: 'space',
  ArrowUp: 'up', ArrowDown: 'down', ArrowLeft: 'left', ArrowRight: 'right', Tab: 'tab'
}

/**
 * The key part of a recorded chord. Letters and digits come from the layout
 * when it produced one (AZERTY, Dvorak), else from the physical key (Option
 * turns ⌥N into "˜" on macOS); punctuation and named keys from the physical key.
 */
function recordedKey(e: Pick<KeyboardEvent, 'key' | 'code'>): string | null {
  const k = (e.key || '').toLowerCase()
  const code = e.code || ''
  let key = ''
  if (/^[a-z0-9]$/.test(k)) key = k
  else if (/^f([1-9]|1[0-2])$/.test(k)) key = k
  else if (/^Key[A-Z]$/.test(code)) key = code.slice(3).toLowerCase()
  else if (/^(Digit|Numpad)\d$/.test(code)) key = code.slice(-1)
  else if (CODE_KEY[code]) key = CODE_KEY[code]!
  return RECORDABLE_KEY.test(key) ? key : null
}

export type OverrideProblem =
  | { kind: 'needs-modifier' }
  | { kind: 'blocked' }
  | { kind: 'escape' }

/** Why a recorded spec can't be used, or null when it can. */
export function overrideProblem(spec: string, platform: Platform): OverrideProblem | null {
  const s = parseSpec(spec)
  if (s.key === 'escape') return { kind: 'escape' }
  // Bare keys and Shift+key type text; F-keys are fine on their own.
  if (!s.mod && !s.ctrl && !s.alt && !/^f\d+$/.test(s.key)) return { kind: 'needs-modifier' }
  // ⌘Tab / Alt+Tab switch apps and windows; Tab goes with Ctrl (⌃ on a Mac).
  if (s.key === 'tab' && (s.alt || (platform === 'mac' ? s.mod || !s.ctrl : !s.mod && !s.ctrl))) return { kind: 'blocked' }
  const c = canonicalSpec(spec)
  if (BLOCKED[platform].some(b => canonicalSpec(b) === c)) return { kind: 'blocked' }
  return null
}

/** The browser keeps this key in a plain tab (a warning; the desktop app gets it). */
export function browserMayReserve(spec: string): boolean {
  const c = canonicalSpec(spec)
  return BROWSER_RESERVED.some(b => canonicalSpec(b) === c)
}

/** Drops anything that isn't a usable override (bad JSON, unknown ids, bad specs). */
export function sanitizeOverrides(raw: unknown, isCustomizable: (id: string) => boolean): ShortcutOverridesDoc {
  const out = empty()
  if (!raw || typeof raw !== 'object') return out
  for (const platform of ['mac', 'other'] as const) {
    const src = (raw as any)[platform]
    if (!src || typeof src !== 'object') continue
    for (const [id, spec] of Object.entries(src)) {
      if (!isCustomizable(id) || typeof spec !== 'string') continue
      if (spec === '') { out[platform][id] = ''; continue }
      try {
        if (overrideProblem(spec, platform)) continue
        out[platform][id] = canonicalSpec(spec)
      } catch {
        // unparseable spec: ignore
      }
    }
  }
  return out
}

// ── state ───────────────────────────────────────────────────────────────────

export function shortcutOverrides(): ShortcutOverridesDoc {
  return doc.value
}

/** The override for a shortcut on a platform: a spec, "" (no key) or undefined (default). */
export function overrideFor(id: string, platform: Platform): string | undefined {
  return doc.value[platform][id]
}

export function hasOverrides(platform: Platform): boolean {
  return Object.keys(doc.value[platform]).length > 0
}

/** The chord an override gives, null for "no key", undefined when there is none. */
export function overrideChord(id: string, platform: Platform): Chord | null | undefined {
  const spec = overrideFor(id, platform)
  if (spec === undefined) return undefined
  if (spec === '') return null
  return resolveChord(spec, platform)
}

type Listener = (doc: ShortcutOverridesDoc) => void
const listeners = new Set<Listener>()

/** Called after every change (saving, the desktop menu). Returns an unsubscribe function. */
export function onShortcutOverridesChange(fn: Listener): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

/** Replaces the whole state. notify: false when it came from the server or storage. */
export function setShortcutOverrides(next: ShortcutOverridesDoc, opts: { notify?: boolean } = {}): void {
  doc.value = next
  try { localStorage.setItem(SHORTCUT_OVERRIDES_STORAGE_KEY, JSON.stringify(next)) } catch { /* private mode */ }
  if (opts.notify !== false) for (const fn of listeners) fn(next)
}

/**
 * Sets one shortcut's keys on a platform: a spec, "" for no key, or null to
 * go back to the default. Other shortcuts given in `unassign` lose their key
 * (when the user takes it for this one).
 */
export function setShortcutOverride(id: string, spec: string | null, platform: Platform, unassign: string[] = []): void {
  const nextPlatform = { ...doc.value[platform] }
  if (spec === null) delete nextPlatform[id]
  else nextPlatform[id] = spec === '' ? '' : canonicalSpec(spec)
  for (const other of unassign) if (other !== id) nextPlatform[other] = ''
  setShortcutOverrides({ ...doc.value, [platform]: nextPlatform })
}

/** Back to the defaults on one platform. */
export function resetShortcutOverrides(platform: Platform): void {
  setShortcutOverrides({ ...doc.value, [platform]: {} })
}

/** Reads the localStorage cache (first paint, before the server answers). */
export function loadCachedShortcutOverrides(isCustomizable: (id: string) => boolean): void {
  try {
    const raw = localStorage.getItem(SHORTCUT_OVERRIDES_STORAGE_KEY)
    if (raw) doc.value = sanitizeOverrides(JSON.parse(raw), isCustomizable)
  } catch {
    // no storage or bad JSON: defaults
  }
}

/** Tests only. */
export function __resetShortcutOverrides(): void {
  doc.value = empty()
  listeners.clear()
}

export type { Chord, Platform, Shell }

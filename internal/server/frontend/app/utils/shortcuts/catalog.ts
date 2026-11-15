/**
 * The typed shortcut catalogue (data lives in catalog.json, which the desktop
 * app's Go tests also read so its native menu cannot drift from the page).
 * The keys there are defaults: the user's own keys (overrides.ts) win.
 */
import data from './catalog.json'
import { resolveChord, withBrowserFallback, formatChord, chordParts, ariaKeyshortcuts } from './keys'
import { overrideChord, overrideFor } from './overrides'
import type { Chord, Platform, Shell } from './keys'

export type ShortcutScope = 'global' | 'session' | 'zen' | 'modal'

export type ShortcutId =
  | 'palette.open' | 'session.new' | 'project.new' | 'settings.open'
  | 'overview.toggle' | 'allagents.toggle' | 'project.switch' | 'file.goto' | 'session.jump' | 'subagent.prev' | 'subagent.next' | 'subagent.first' | 'subagent.last' | 'session.prev' | 'session.next' | 'session.up' | 'session.down'
  | 'messages.search' | 'model.change' | 'subagents.toggle' | 'tab.terminal' | 'tab.files' | 'session.tag'
  | 'voice.dictate' | 'voice.finish' | 'agent.stop'
  | 'sidebar.toggle' | 'sidebar.right.toggle' | 'view.zen' | 'view.fullscreen' | 'theme.next' | 'theme.previous' | 'zen.fly'
  | 'help.shortcuts'

export interface ShortcutDef {
  id: ShortcutId
  label: string
  description?: string
  group: string
  /** Where the shortcut normally applies (handlers may register in another scope). */
  scope: ShortcutScope
  /** Key specs, see keys.ts. `other` overrides the Windows/Linux keys. */
  keys: { mac: string; other?: string }
  /** Fires while typing in an input. True for ⌘/Ctrl chords, false for bare keys. */
  allowInInputs: boolean
  /** The browser reserves the key: add Option/Alt in a plain browser tab. */
  browserFallback?: boolean
  /** The desktop app's native menu owns the key (the page skips it there). */
  desktopMenu?: boolean
  /** Listed as an action in the command palette (default true). */
  palette?: boolean
  /** The user can change its keys in Settings (default true). */
  customizable?: boolean
}

export const SHORTCUT_GROUPS: string[] = data.groups
export const SHORTCUTS: ShortcutDef[] = data.shortcuts as ShortcutDef[]
const BY_ID = new Map(SHORTCUTS.map(s => [s.id, s]))

export function shortcutDef(id: ShortcutId): ShortcutDef {
  const def = BY_ID.get(id)
  if (!def) throw new Error(`unknown shortcut ${id}`)
  return def
}

export function isCustomizable(id: string): boolean {
  const def = BY_ID.get(id as ShortcutId)
  return !!def && def.customizable !== false
}

/** The catalogue's chord for this platform and shell, ignoring the user's keys. */
export function defaultChord(def: ShortcutDef, platform: Platform, shell: Shell): Chord {
  const spec = platform === 'other' && def.keys.other ? def.keys.other : def.keys.mac
  const chord = resolveChord(spec, platform)
  return shell === 'web' && def.browserFallback ? withBrowserFallback(chord) : chord
}

/**
 * The chord that applies for this shortcut on this platform and shell: the
 * user's own (exactly as recorded, no browser fallback added), else the
 * default. null when the user removed its key.
 */
export function effectiveChord(def: ShortcutDef, platform: Platform, shell: Shell): Chord | null {
  if (def.customizable !== false) {
    const own = overrideChord(def.id, platform)
    if (own !== undefined) return own
  }
  return defaultChord(def, platform, shell)
}

/** The user picked this key (it isn't the catalogue default). */
function hasOwnKey(def: ShortcutDef, platform: Platform): boolean {
  return def.customizable !== false && overrideFor(def.id, platform) !== undefined
}

/**
 * Fires while typing in a text field. Defaults follow the catalogue; a user's
 * key does only when it is a ⌘/Ctrl chord (⌥2 types "@" on a Nordic Mac).
 */
export function effectiveAllowInInputs(def: ShortcutDef, chord: Chord, platform: Platform): boolean {
  if (!def.allowInInputs) return false
  return !hasOwnKey(def, platform) || chord.meta || chord.ctrl
}

/** A user key without ⌘/Ctrl stays with the page, so it can skip text fields. */
function ownKeyNeedsPage(def: ShortcutDef, platform: Platform): boolean {
  const spec = overrideFor(def.id, platform)
  if (!spec || def.customizable === false) return false
  const c = resolveChord(spec, platform)
  return !c.meta && !c.ctrl
}

/** "" when the shortcut has no key. */
export function shortcutLabel(id: ShortcutId, platform: Platform, shell: Shell): string {
  const chord = effectiveChord(shortcutDef(id), platform, shell)
  return chord ? formatChord(chord, platform) : ''
}

export function shortcutParts(id: ShortcutId, platform: Platform, shell: Shell): string[] {
  const chord = effectiveChord(shortcutDef(id), platform, shell)
  return chord ? chordParts(chord, platform) : []
}

/** undefined when the shortcut has no key (so the attribute is left out). */
export function shortcutAria(id: ShortcutId, platform: Platform, shell: Shell): string | undefined {
  const chord = effectiveChord(shortcutDef(id), platform, shell)
  return chord ? ariaKeyshortcuts(chord) : undefined
}

/**
 * The native menu items' keys for the desktop app: every desktopMenu
 * shortcut the user changed, as a spec ("" = no key). The app rebuilds its
 * menu with them (cmd/eggify-desktop/shortcut_menu.go, SetMenuShortcuts).
 */
export function desktopMenuOverrides(platform: Platform): Record<string, string> {
  const out: Record<string, string> = {}
  for (const def of SHORTCUTS) {
    if (!def.desktopMenu || def.customizable === false) continue
    const spec = overrideFor(def.id, platform)
    if (spec === undefined) continue
    // The page handles keys without ⌘/Ctrl itself (see ownedByDesktopMenu).
    out[def.id] = ownKeyNeedsPage(def, platform) ? '' : spec
  }
  return out
}

let menuHasOwnKeys = true
/**
 * Whether the desktop app's menu carries the user's keys. When pushing them
 * failed, the page handles changed shortcuts itself instead of waiting for a
 * menu item that still has the default key.
 */
export function setDesktopMenuHasOwnKeys(ok: boolean): void {
  menuHasOwnKeys = ok
}

/** True when the page should leave the key to the desktop app's menu. */
export function ownedByDesktopMenu(def: ShortcutDef, shell: Shell, platform?: Platform): boolean {
  if (shell !== 'wails' || !def.desktopMenu) return false
  if (platform && hasOwnKey(def, platform) && (!menuHasOwnKeys || ownKeyNeedsPage(def, platform))) return false
  return true
}

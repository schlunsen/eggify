/**
 * The keyboard shortcut registry.
 *
 * - The shortcuts themselves (keys, labels, groups) are a static catalogue:
 *   app/utils/shortcuts/catalog.json. The sheet, tooltips and the desktop
 *   app's native menu are generated from it.
 * - Components register *handlers* for catalogue ids while they are mounted
 *   (useShortcut). A handler has a scope (modal > zen > session > global) and
 *   an optional when() guard; one keydown runs at most one handler, the
 *   highest-priority one whose guard passes.
 * - Exactly one window keydown listener (installShortcutDispatcher, started by
 *   plugins/shortcuts.client.ts). It ignores events that are already handled
 *   (defaultPrevented) and IME composition, and runs bare-key shortcuts only
 *   outside text fields.
 * - Escape is one stack of "closables": the most recently opened modal,
 *   overlay or panel closes first. Only when nothing is open does Escape stop a
 *   running agent turn (an "Escape action", see useEscapeAction); ⌘. always does.
 * - In the desktop app, keys owned by a native menu item are left to the
 *   menu, which sends the id back as the `eggify:shortcut` event.
 * - Users can change the keys (Settings → Keyboard shortcuts,
 *   utils/shortcuts/overrides.ts); effectiveChord applies their keys, and the
 *   labels re-render when they change.
 */
import { ref, readonly, getCurrentInstance, onUnmounted, watch, computed } from 'vue'
import type { WatchSource } from 'vue'
import { SHORTCUTS, shortcutDef, effectiveChord, effectiveAllowInInputs, ownedByDesktopMenu, shortcutLabel, shortcutParts, shortcutAria } from '~/utils/shortcuts/catalog'
import type { ShortcutId, ShortcutScope } from '~/utils/shortcuts/catalog'
import { detectPlatform, matchChord, isEditableTarget, isTerminalTarget, MODIFIER_KEYS } from '~/utils/shortcuts/keys'
import type { Platform, Shell } from '~/utils/shortcuts/keys'
import { desktopShell, onDesktopShortcut } from '~/utils/desktop'

export type { ShortcutId, ShortcutScope } from '~/utils/shortcuts/catalog'

export const SCOPE_PRIORITY: Record<ShortcutScope, number> = { modal: 3, zen: 2, session: 1, global: 0 }

export interface ShortcutContext {
  /** 1–9 for session.jump */
  digit?: number
  source: 'key' | 'menu' | 'palette'
  event?: KeyboardEvent
}

export interface HandlerOptions {
  scope?: ShortcutScope
  when?: () => boolean
}

interface HandlerEntry {
  id: ShortcutId
  run: (ctx: ShortcutContext) => void
  scope: ShortcutScope
  when?: () => boolean
  seq: number
}

interface Closable {
  key: number
  close: () => void
  modal: boolean
  when?: () => boolean
}

// ── module state ────────────────────────────────────────────────────────────
const handlers = new Map<ShortcutId, HandlerEntry[]>()
let seq = 0
const escStack: Closable[] = []
const sheetOpen = ref(false)
let envOverride: { platform?: Platform; shell?: Shell } = {}

export function shortcutPlatform(): Platform {
  return envOverride.platform ?? detectPlatform()
}
export function shortcutShell(): Shell {
  return envOverride.shell ?? desktopShell()
}
/** Tests only: pretend to be on another platform / shell. */
export function __setShortcutEnv(env: { platform?: Platform; shell?: Shell }) {
  envOverride = env
}
/** Tests only: drop all handlers and closables. */
export function __resetShortcuts() {
  handlers.clear()
  escStack.length = 0
  sheetOpen.value = false
}

// ── handlers ────────────────────────────────────────────────────────────────
export function registerShortcutHandler(id: ShortcutId, run: (ctx: ShortcutContext) => void, opts: HandlerOptions = {}): () => void {
  shortcutDef(id) // throws on unknown ids
  const entry: HandlerEntry = { id, run, scope: opts.scope ?? shortcutDef(id).scope, when: opts.when, seq: ++seq }
  const list = handlers.get(id) ?? []
  list.push(entry)
  handlers.set(id, list)
  return () => {
    const l = handlers.get(id)
    if (!l) return
    const i = l.indexOf(entry)
    if (i >= 0) l.splice(i, 1)
    if (!l.length) handlers.delete(id)
  }
}

/** Registers a handler for the lifetime of the calling component. */
export function useShortcut(id: ShortcutId, run: (ctx: ShortcutContext) => void, opts: HandlerOptions = {}): () => void {
  const off = registerShortcutHandler(id, run, opts)
  if (getCurrentInstance()) onUnmounted(off)
  return off
}

/**
 * The handler that would run now, or null. ignoreModal: as if no modal were
 * open (the command palette asks this while it is itself open).
 */
export function resolveHandler(id: ShortcutId, opts: { ignoreModal?: boolean } = {}): HandlerEntry | null {
  const list = handlers.get(id)
  if (!list?.length) return null
  const modalOpen = !opts.ignoreModal && isModalOpen()
  let best: HandlerEntry | null = null
  for (const h of list) {
    if (modalOpen && h.scope !== 'modal') continue
    if (h.when && !h.when()) continue
    if (!best || SCOPE_PRIORITY[h.scope] > SCOPE_PRIORITY[best.scope] ||
        (SCOPE_PRIORITY[h.scope] === SCOPE_PRIORITY[best.scope] && h.seq > best.seq)) best = h
  }
  return best
}

export function hasShortcutHandler(id: ShortcutId, opts: { ignoreModal?: boolean } = {}): boolean {
  return resolveHandler(id, opts) !== null
}

/** Runs a shortcut by id (menu items, the command palette). */
export function runShortcut(id: ShortcutId, ctx: ShortcutContext = { source: 'palette' }): boolean {
  const h = resolveHandler(id)
  if (!h) return false
  h.run(ctx)
  return true
}

// ── Escape stack ────────────────────────────────────────────────────────────
let closableKey = 0
export function pushClosable(close: () => void, opts: { modal?: boolean; when?: () => boolean } = {}): () => void {
  const entry: Closable = { key: ++closableKey, close, modal: !!opts.modal, when: opts.when }
  escStack.push(entry)
  return () => {
    const i = escStack.indexOf(entry)
    if (i >= 0) escStack.splice(i, 1)
  }
}

/**
 * Puts `close` on the Escape stack while `isOpen` is true (pushed when it
 * opens, so the newest open thing is on top). modal: while open, only
 * modal-scope shortcuts run.
 */
export function useClosable(isOpen: WatchSource<boolean | undefined | null>, close: () => void, opts: { modal?: boolean; when?: () => boolean } = {}): void {
  let pop: (() => void) | null = null
  const stop = watch(isOpen, (open) => {
    if (open && !pop) pop = pushClosable(close, opts)
    else if (!open && pop) { pop(); pop = null }
  }, { immediate: true, flush: 'sync' })
  if (getCurrentInstance()) onUnmounted(() => { stop(); pop?.(); pop = null })
}

// ── Escape action: what Escape does when nothing is open to close ────────────
interface EscapeAction { key: number; run: () => void; when?: () => boolean }
const escapeActions: EscapeAction[] = []
let escapeActionKey = 0

/** Registers a fallback for Escape (the newest registered, usable one runs). Returns an unregister function. */
export function pushEscapeAction(run: () => void, when?: () => boolean): () => void {
  const entry: EscapeAction = { key: ++escapeActionKey, run, when }
  escapeActions.push(entry)
  return () => {
    const i = escapeActions.indexOf(entry)
    if (i >= 0) escapeActions.splice(i, 1)
  }
}

export function useEscapeAction(run: () => void, when?: () => boolean): void {
  const off = pushEscapeAction(run, when)
  if (getCurrentInstance()) onUnmounted(off)
}

/** Runs the Escape fallback. Returns false when there is none (or a modal is open). */
export function runEscapeAction(): boolean {
  if (isModalOpen()) return false
  for (let i = escapeActions.length - 1; i >= 0; i--) {
    const a = escapeActions[i]!
    if (a.when && !a.when()) continue
    a.run()
    return true
  }
  return false
}

/** Closes the topmost open thing. Returns false when nothing was open. */
export function closeTopmost(): boolean {
  for (let i = escStack.length - 1; i >= 0; i--) {
    const c = escStack[i]!
    if (c.when && !c.when()) continue
    c.close()
    return true
  }
  return false
}

export function isModalOpen(): boolean {
  return escStack.some(c => c.modal && (!c.when || c.when()))
}

export function openClosableCount(): number {
  return escStack.length
}

// ── dispatcher ──────────────────────────────────────────────────────────────
export function dispatchKeydown(e: KeyboardEvent): void {
  if (e.defaultPrevented || e.isComposing || e.keyCode === 229) return
  if (MODIFIER_KEYS.has(e.key)) return

  if (e.key === 'Escape' || e.key === 'Esc') {
    if (e.metaKey || e.ctrlKey || e.altKey) return
    if (closeTopmost() || runEscapeAction()) {
      e.preventDefault()
      e.stopPropagation()
    }
    return
  }

  const platform = shortcutPlatform()
  const shell = shortcutShell()
  const editable = isEditableTarget(e.target)
  const terminal = isTerminalTarget(e.target)

  for (const def of SHORTCUTS) {
    const chord = effectiveChord(def, platform, shell)
    if (!chord) continue // the user removed its key
    const m = matchChord(chord, e, platform)
    if (!m) continue
    // The desktop app's menu item owns this key: leave the event alone so the
    // menu fires (it calls back with the id).
    if (ownedByDesktopMenu(def, shell, platform)) return
    if (editable && !effectiveAllowInInputs(def, chord, platform)) continue
    // Ctrl+letter belongs to the terminal on Windows/Linux (Ctrl+D, Ctrl+K…).
    if (terminal && platform === 'other' && !chord.shift && !chord.alt) continue
    const h = resolveHandler(def.id)
    if (!h) continue
    e.preventDefault()
    if (!e.repeat) h.run({ digit: m.digit, source: 'key', event: e })
    return
  }
}

let uninstall: (() => void) | null = null

/** Starts the one keydown listener (idempotent). */
export function installShortcutDispatcher(target: Window = window): () => void {
  if (uninstall) return uninstall
  const onKey = (e: Event) => dispatchKeydown(e as KeyboardEvent)
  target.addEventListener('keydown', onKey)
  let offMenu: (() => void) | null = null
  let disposed = false
  void onDesktopShortcut((id) => {
    if (SHORTCUTS.some(s => s.id === id)) runShortcut(id as ShortcutId, { source: 'menu' })
  }).then((off) => { if (disposed) off(); else offMenu = off }).catch(() => {})
  uninstall = () => {
    disposed = true
    target.removeEventListener('keydown', onKey)
    offMenu?.()
    uninstall = null
  }
  return uninstall
}

// ── shortcuts sheet + labels ────────────────────────────────────────────────
export function useShortcutsSheet() {
  return {
    open: readonly(sheetOpen),
    openSheet: () => { sheetOpen.value = true },
    closeSheet: () => { sheetOpen.value = false },
    toggleSheet: () => { sheetOpen.value = !sheetOpen.value }
  }
}

/** Labels for the binding that applies in this platform/shell. */
export function useShortcutLabels() {
  const platform = shortcutPlatform()
  const shell = shortcutShell()
  return {
    platform,
    shell,
    label: (id: ShortcutId) => shortcutLabel(id, platform, shell),
    parts: (id: ShortcutId) => shortcutParts(id, platform, shell),
    aria: (id: ShortcutId) => shortcutAria(id, platform, shell),
    /** "Agent overview (⌘O)", or just the text when the shortcut has no key */
    title: (text: string, id: ShortcutId) => {
      const keys = shortcutLabel(id, platform, shell)
      return keys ? `${text} (${keys})` : text
    }
  }
}

/** Reactive label (for templates that render before the shell is known). */
export function useShortcutLabel(id: ShortcutId) {
  return computed(() => shortcutLabel(id, shortcutPlatform(), shortcutShell()))
}

/** Backwards-compatible facade for the shortcuts dialog. */
export const useKeyboardShortcuts = () => {
  const sheet = useShortcutsSheet()
  return {
    showDialog: sheet.open,
    openDialog: sheet.openSheet,
    closeDialog: sheet.closeSheet,
    toggleDialog: sheet.toggleSheet
  }
}

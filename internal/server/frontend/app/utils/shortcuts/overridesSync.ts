/**
 * Keeps the user's shortcut keys (overrides.ts) in step with the server
 * setting `keyboard_shortcuts` and, in the desktop app, with its native menu.
 *
 * Started by plugins/shortcuts.client.ts: the localStorage cache applies at
 * once, the server's copy replaces it when it arrives (the server wins), and
 * every change made here is saved back. A change whose save failed is kept
 * as pending (localStorage) and sent again on the next load instead of being
 * replaced by the server's copy. The plugin reloads when the window comes
 * back, so a desktop app and browser tabs pick up each other's changes.
 * In the desktop app the menu items get the user's keys (SetMenuShortcuts,
 * one call at a time, newest last); if that fails, the page handles the
 * changed keys itself (setDesktopMenuHasOwnKeys).
 */
import { desktopMenuOverrides, isCustomizable, setDesktopMenuHasOwnKeys } from './catalog'
import {
  SHORTCUT_OVERRIDES_SETTING, loadCachedShortcutOverrides, onShortcutOverridesChange, sanitizeOverrides,
  setShortcutOverrides, shortcutOverrides
} from './overrides'
import type { Platform, Shell } from './keys'
import { desktopSetMenuShortcuts } from '~/utils/desktop'

/** Set while a local change has not reached the server. */
export const SHORTCUT_OVERRIDES_PENDING_KEY = 'eggify-shortcut-overrides-pending'

function pending(): boolean {
  try { return localStorage.getItem(SHORTCUT_OVERRIDES_PENDING_KEY) === '1' } catch { return false }
}
function setPending(on: boolean): void {
  try {
    if (on) localStorage.setItem(SHORTCUT_OVERRIDES_PENDING_KEY, '1')
    else localStorage.removeItem(SHORTCUT_OVERRIDES_PENDING_KEY)
  } catch { /* no storage */ }
}

export type OverridesFetcher = (url: string, init?: RequestInit) => Promise<Response>

interface SyncEnv {
  fetcher: OverridesFetcher
  platform: Platform
  shell: Shell
  /** Desktop menu bridge (tests replace it). */
  setMenuShortcuts?: (overrides: Record<string, string>) => Promise<boolean>
}

let env: SyncEnv | null = null
let offChange: (() => void) | null = null
/** Bumped by every local change, so a slow server read can't undo it. */
let localEdits = 0

/** Starts syncing. Returns a stop function. */
export function startShortcutOverridesSync(e: SyncEnv): () => void {
  env = e
  loadCachedShortcutOverrides(isCustomizable)
  void pushToDesktopMenu()
  offChange?.()
  offChange = onShortcutOverridesChange(() => {
    localEdits++
    void saveShortcutOverrides()
    void pushToDesktopMenu()
  })
  return () => {
    offChange?.()
    offChange = null
    env = null
  }
}

/** Reads the server's copy (it wins over the cache). Returns false when the request failed. */
export async function loadShortcutOverrides(): Promise<boolean> {
  if (!env) return false
  // An unsaved local change wins: send it instead of reading.
  if (pending()) {
    const ok = await saveShortcutOverrides()
    await pushToDesktopMenu()
    return ok
  }
  const edits = localEdits
  try {
    const res = await env.fetcher(`/api/settings/${SHORTCUT_OVERRIDES_SETTING}`, { method: 'GET' })
    if (edits !== localEdits) return true // changed here meanwhile; that save wins
    if (res.status === 404) {
      setShortcutOverrides(sanitizeOverrides(null, isCustomizable), { notify: false })
    } else if (res.ok) {
      const setting = await res.json()
      if (edits !== localEdits) return true
      let raw: unknown = null
      try { raw = JSON.parse(setting?.value || 'null') } catch { raw = null }
      setShortcutOverrides(sanitizeOverrides(raw, isCustomizable), { notify: false })
    } else {
      return false
    }
    await pushToDesktopMenu()
    return true
  } catch {
    return false
  }
}

/** Saves the current keys to the server. */
export async function saveShortcutOverrides(): Promise<boolean> {
  if (!env) return false
  setPending(true)
  const edits = localEdits
  try {
    const res = await env.fetcher(`/api/settings/${SHORTCUT_OVERRIDES_SETTING}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        value: JSON.stringify(shortcutOverrides()),
        value_type: 'json',
        description: 'Your own keyboard shortcut keys (per platform); missing ones use the defaults'
      })
    })
    // A newer change is on its way; that save clears the flag.
    if (res.ok && edits === localEdits) setPending(false)
    return res.ok
  } catch {
    return false
  }
}

let menuQueue: Promise<void> = Promise.resolve()

/** Sends the current keys to the desktop menu, after any earlier send has finished. */
function pushToDesktopMenu(): Promise<void> {
  if (!env || env.shell !== 'wails') return Promise.resolve()
  const set = env.setMenuShortcuts ?? desktopSetMenuShortcuts
  const platform = env.platform
  menuQueue = menuQueue.then(async () => {
    let ok = false
    try {
      ok = await set(desktopMenuOverrides(platform)) // read when its turn comes: the newest keys
    } catch {
      ok = false
    }
    setDesktopMenuHasOwnKeys(ok)
  })
  return menuQueue
}

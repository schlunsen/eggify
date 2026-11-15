/**
 * Desktop shell adapter.
 *
 * The dashboard runs in three places:
 *  - the Wails desktop app (cmd/eggify-desktop): native calls go through the
 *    Wails runtime (`/wails/runtime.js`, served by the desktop app's loopback
 *    listener) to the Go `DesktopService`;
 *  - the Tauri desktop app (src-tauri): native calls go through
 *    `window.__TAURI_INTERNALS__.invoke`;
 *  - a plain browser: no native calls, callers fall back to web behaviour.
 *
 * Wails is preferred when both are somehow present.
 */
import { parseUsageMap, type SystemUsage } from '~/utils/systemUsage'

export type DesktopShell = 'wails' | 'tauri' | 'web'

/** Cookie set by the Wails desktop app's bootstrap route (not HttpOnly). */
const WAILS_MARKER_COOKIE = 'eggify_desktop=wails'

/** Fully-qualified name of the Go service bound in cmd/eggify-desktop. */
const WAILS_SERVICE = 'main.DesktopService'

export function desktopShell(): DesktopShell {
  if (typeof window === 'undefined') return 'web'
  const w = window as any
  // `_wails.invoke` is injected by Wails after navigation finishes; the marker
  // cookie is there from the first script, so check both.
  if (w._wails?.invoke) return 'wails'
  if (typeof document !== 'undefined' && document.cookie.split('; ').includes(WAILS_MARKER_COOKIE)) return 'wails'
  if (w.__TAURI_INTERNALS__) return 'tauri'
  return 'web'
}

export function isDesktop(): boolean {
  return desktopShell() !== 'web'
}

let wailsRuntime: Promise<any> | null = null

/** Loads Wails' JS runtime (also installs its --wails-draggable handlers). */
export function loadWailsRuntime(): Promise<any> {
  if (!wailsRuntime) {
    const path = '/wails/runtime.js'
    wailsRuntime = import(/* @vite-ignore */ path).catch((err) => {
      wailsRuntime = null
      throw err
    })
  }
  return wailsRuntime
}

async function callWails<T>(method: string, ...args: unknown[]): Promise<T> {
  const rt = await loadWailsRuntime()
  return (await rt.Call.ByName(`${WAILS_SERVICE}.${method}`, ...args)) as T
}

function tauriInvoke(cmd: string, args?: Record<string, unknown>): Promise<unknown> {
  return (window as any).__TAURI_INTERNALS__.invoke(cmd, args)
}

/** Native folder picker. Resolves null when cancelled or not on desktop. */
export async function desktopSelectFolder(): Promise<string | null> {
  switch (desktopShell()) {
    case 'wails': {
      const path = await callWails<string>('SelectFolder')
      return path || null
    }
    case 'tauri':
      return ((await tauriInvoke('select_folder')) as string | null) || null
    default:
      return null
  }
}

/** Whether the shell can reveal a folder in Finder / the file manager. */
export function canRevealFolder(): boolean {
  return desktopShell() === 'wails'
}

/**
 * Shows a folder in Finder (Explorer / the file manager elsewhere). Resolves
 * false when the shell cannot (browser, Tauri).
 */
export async function desktopRevealFolder(path: string): Promise<boolean> {
  if (desktopShell() !== 'wails') return false
  await callWails<void>('RevealFolder', path)
  return true
}

/**
 * Opens url in the system browser. Resolves false when not on desktop (the
 * caller should then use window.open / a normal link).
 */
export async function desktopOpenExternal(url: string): Promise<boolean> {
  switch (desktopShell()) {
    case 'wails':
      await callWails<void>('OpenExternal', url)
      return true
    case 'tauri':
      await tauriInvoke('plugin:shell|open', { path: url, with: null })
      return true
    default:
      return false
  }
}

/** Native notification. Resolves false when not on desktop. */
export async function desktopNotify(title: string, body = ''): Promise<boolean> {
  switch (desktopShell()) {
    case 'wails':
      await callWails<void>('Notify', title, body)
      return true
    case 'tauri':
      await tauriInvoke('plugin:notification|notify', { options: { title, body } })
      return true
    default:
      return false
  }
}

// ── Self-update (Wails desktop app only) ────────────────────────────────────
// The Go side (cmd/eggify-desktop/updater.go) checks
// the build's update manifest (latest.json) on launch and every 6 hours, verifies
// downloads (SHA-256, code signature, team id, Gatekeeper) and swaps the .app.
// The page only reads the state and asks it to install; it never supplies a URL.

export interface DesktopUpdateState {
  current_version: string
  latest_version: string
  update_available: boolean
  published_at?: string
  release_url?: string
  checked_at?: string
  check_error?: string
  blocked?: string
  can_install: boolean
  phase: 'idle' | 'checking' | 'downloading' | 'verifying' | 'installing' | 'restarting' | 'error' | string
  percent: number
  error?: string
  /** "app_management": macOS App Management blocked the install. */
  error_code?: string
  /** The signed DMG of this release, for installing it by hand. */
  download_url?: string
}

/** Wails event the Go updater emits on every state change. */
export const DESKTOP_UPDATE_EVENT = 'eggify:update'

/**
 * Opens System Settings → Privacy & Security → App Management, where the user
 * lets Eggify update itself. Resolves false outside the Wails desktop app.
 */
export async function desktopOpenAppManagementSettings(): Promise<boolean> {
  if (desktopShell() !== 'wails') return false
  await callWails<void>('OpenAppManagementSettings')
  return true
}

/** Current update state, or null outside the Wails app. */
export async function desktopUpdateState(): Promise<DesktopUpdateState | null> {
  if (desktopShell() !== 'wails') return null
  return await callWails<DesktopUpdateState>('UpdateState')
}

/** Checks the manifest now. Null outside the Wails app. */
export async function desktopCheckForUpdates(): Promise<DesktopUpdateState | null> {
  if (desktopShell() !== 'wails') return null
  return await callWails<DesktopUpdateState>('CheckForUpdates')
}

/** Downloads, verifies and installs the pending update, then the app restarts. */
export async function desktopInstallUpdate(): Promise<boolean> {
  if (desktopShell() !== 'wails') return false
  await callWails<void>('InstallUpdate')
  return true
}

/** Subscribes to update state changes. Returns an unsubscribe function (no-op outside Wails). */
export async function onDesktopUpdate(cb: (state: DesktopUpdateState) => void): Promise<() => void> {
  if (desktopShell() !== 'wails') return () => {}
  const rt = await loadWailsRuntime()
  if (!rt?.Events?.On) return () => {}
  return rt.Events.On(DESKTOP_UPDATE_EVENT, (ev: any) => {
    const data = ev && typeof ev === 'object' && 'data' in ev ? ev.data : ev
    if (data && typeof data === 'object') cb(data as DesktopUpdateState)
  })
}

/** The tray's "Check for Updates…" asks the dashboard to reopen its update dialog. */
export const DESKTOP_UPDATE_SHOW_EVENT = 'eggify:update-show'

/** In-page twin of the event above (the command palette's "Check for updates"). */
const LOCAL_UPDATE_SHOW_EVENT = 'eggify:update-show-local'

export function requestDesktopUpdateDialog(): void {
  if (typeof window !== 'undefined') window.dispatchEvent(new CustomEvent(LOCAL_UPDATE_SHOW_EVENT))
}

export function onLocalUpdateShow(cb: () => void): () => void {
  if (typeof window === 'undefined') return () => {}
  const h = () => cb()
  window.addEventListener(LOCAL_UPDATE_SHOW_EVENT, h)
  return () => window.removeEventListener(LOCAL_UPDATE_SHOW_EVENT, h)
}

export async function onDesktopUpdateShow(cb: () => void): Promise<() => void> {
  if (desktopShell() !== 'wails') return () => {}
  const rt = await loadWailsRuntime()
  if (!rt?.Events?.On) return () => {}
  return rt.Events.On(DESKTOP_UPDATE_SHOW_EVENT, () => cb())
}

/** What the update toast should show for a state (pure, unit-tested). */
export type UpdateToastMode = 'hidden' | 'available' | 'manual' | 'progress' | 'error'

export function updateToastMode(s: DesktopUpdateState | null, dismissedVersion: string | null): UpdateToastMode {
  if (!s) return 'hidden'
  if (s.phase === 'error' && s.error) return 'error'
  if (['downloading', 'verifying', 'installing', 'restarting'].includes(s.phase)) return 'progress'
  if (!s.update_available) return 'hidden'
  if (dismissedVersion && dismissedVersion === s.latest_version) return 'hidden'
  return s.can_install ? 'available' : 'manual'
}

// ── Native menu shortcuts (Wails desktop app only) ─────────────────────────
// Menu items in cmd/eggify-desktop/shortcut_menu.go own their accelerators and
// send the shortcut id (app/utils/shortcuts/catalog.json) to the page, which
// runs it through the shortcut registry.

/**
 * Gives the desktop app's menu items the user's own keys: shortcut id → key
 * spec ("mod+shift+k"; "" = no key). Ids left out keep their default key.
 * Returns false outside the Wails app or when the app refused them.
 */
export async function desktopSetMenuShortcuts(overrides: Record<string, string>): Promise<boolean> {
  if (desktopShell() !== 'wails') return false
  try {
    await callWails('SetMenuShortcuts', overrides)
    return true
  } catch {
    return false
  }
}

/** Wails event a native menu item emits with a shortcut id. */
export const DESKTOP_SHORTCUT_EVENT = 'eggify:shortcut'

/**
 * Help → What's New sends this id on the same event. It is not a catalogue
 * shortcut (it has no key), so the layout listens for it itself.
 * Must match whatsNewMenuID in cmd/eggify-desktop/shortcut_menu.go.
 */
export const DESKTOP_WHATS_NEW_ID = 'changelog.open'

export async function onDesktopShortcut(cb: (id: string) => void): Promise<() => void> {
  if (desktopShell() !== 'wails') return () => {}
  const rt = await loadWailsRuntime()
  if (!rt?.Events?.On) return () => {}
  return rt.Events.On(DESKTOP_SHORTCUT_EVENT, (ev: any) => {
    const data = ev && typeof ev === 'object' && 'data' in ev ? ev.data : ev
    const id = Array.isArray(data) ? data[0] : data
    if (typeof id === 'string') cb(id)
  })
}

// ── Machines (Wails desktop app only, ADR 014) ─────────────────────────────
// The window can show another of the owner's Macs (cmd/eggify-desktop
// machine_switch.go). That Mac's page is served through this Mac's machine
// proxy, whose /wails/* is answered by this Mac's app, so these calls give
// the right answer on either page.

export interface DesktopMachine {
  /** "" for this Mac */
  id: string
  name: string
  /** colour hue, 0..359 */
  hue: number
  /** this Mac */
  here: boolean
}

export interface DesktopMachines {
  /** id of the machine the window shows ("" = this Mac) */
  current: string
  machines: DesktopMachine[]
}

/** Wails event the app emits with DesktopMachines when the list or the machine shown changes. */
export const DESKTOP_MACHINES_EVENT = 'eggify:machines'

function asMachines(v: any): DesktopMachines | null {
  if (!v || typeof v !== 'object' || !Array.isArray(v.machines)) return null
  return {
    current: typeof v.current === 'string' ? v.current : '',
    machines: v.machines
      .filter((m: any) => m && typeof m.name === 'string')
      .map((m: any) => ({ id: typeof m.id === 'string' ? m.id : '', name: m.name, hue: Number(m.hue) || 0, here: !!m.here })),
  }
}

/** The machines, or null outside the Wails app (or an app too old to know them). */
export async function desktopMachines(): Promise<DesktopMachines | null> {
  if (desktopShell() !== 'wails') return null
  try {
    return asMachines(await callWails('Machines'))
  } catch {
    return null
  }
}

/**
 * CPU and memory per machine id ("" = the app's own Mac) from the desktop app,
 * which asks the paired Macs through the relay; null outside the Wails app or
 * when the app is too old to know the binding.
 */
export async function desktopMachineUsage(): Promise<Record<string, SystemUsage> | null> {
  if (desktopShell() !== 'wails') return null
  try {
    return parseUsageMap(await callWails('MachineUsage'))
  } catch {
    return null
  }
}

/** Shows the machine with id ("" = this Mac) in the window, with the switch transition. */
export async function desktopSwitchMachine(id: string): Promise<boolean> {
  if (desktopShell() !== 'wails') return false
  await callWails<void>('SwitchMachine', id)
  return true
}

/** Same as the tray's "Add a Mac from Clipboard…" (the app asks first). */
export async function desktopAddMachineFromClipboard(): Promise<boolean> {
  if (desktopShell() !== 'wails') return false
  await callWails<void>('AddMachineFromClipboard')
  return true
}

export async function onDesktopMachines(cb: (m: DesktopMachines) => void): Promise<() => void> {
  if (desktopShell() !== 'wails') return () => {}
  const rt = await loadWailsRuntime()
  if (!rt?.Events?.On) return () => {}
  return rt.Events.On(DESKTOP_MACHINES_EVENT, (ev: any) => {
    const data = ev && typeof ev === 'object' && 'data' in ev ? ev.data : ev
    const m = asMachines(Array.isArray(data) ? data[0] : data)
    if (m) cb(m)
  })
}

/** Notch Theatre (desktop): grow the notch window into its stage, or shrink it to the pill. */
export async function desktopNotchSetExpanded(expanded: boolean): Promise<boolean> {
  if (desktopShell() !== 'wails') return false
  await callWails<void>('NotchSetExpanded', expanded)
  return true
}

/** Notch Theatre: show a session in the main window (this Mac). */
export async function desktopNotchOpenSession(sessionId: string): Promise<boolean> {
  if (desktopShell() !== 'wails') return false
  await callWails<void>('NotchOpenSession', sessionId)
  return true
}

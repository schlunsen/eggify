/**
 * "How should it work?": the one choice that replaces the permission mode and
 * the sandbox switch for sessions on the Claude CLI.
 *
 *   autopilot  prompts off inside the sandbox (YOLO + sandboxed)
 *   ask        prompts on (the sandbox switch stays available under it)
 *   full       prompts off, no sandbox (YOLO, not sandboxed)
 *
 * The session itself keeps its two fields (permission_mode / YOLO flags and
 * sandboxed); this only maps between them and the three options.
 */
import { sandboxSupportForProvider } from '~/utils/agents/sandbox'

export type WorkMode = 'autopilot' | 'ask' | 'full'

export const WORK_MODES: readonly WorkMode[] = ['autopilot', 'ask', 'full']

export const WORK_MODE_STORAGE_KEY = 'eggify-work-mode'

export interface WorkModeInfo {
  id: WorkMode
  label: string
  description: string
  risky?: boolean
}

export const WORK_MODE_INFO: Record<WorkMode, WorkModeInfo> = {
  autopilot: {
    id: 'autopilot',
    label: 'Autopilot',
    description: 'No permission prompts. Works inside a sandbox: only this project, the sites you allow, and what you give it access to.',
  },
  ask: {
    id: 'ask',
    label: 'Ask first',
    description: 'Asks before it changes files or runs commands.',
  },
  full: {
    id: 'full',
    label: 'Full access',
    description: 'No permission prompts and no sandbox. It can read and change anything you can on this Mac.',
    risky: true,
  },
}

export type WorkModeRuntime = 'claude' | 'codex' | 'direct' | 'other'

/** Which runtime a provider runs on, as far as this choice is concerned. */
export function workModeRuntime(provider?: string | null): WorkModeRuntime {
  const p = (provider || 'claude').trim().toLowerCase()
  if (p === 'codex') return 'codex'
  const support = sandboxSupportForProvider(p)
  if (support === 'unsupported') return 'direct'
  if (support === 'depends') return 'other'
  return 'claude'
}

/** Autopilot holds only where the Claude CLI's sandbox and Eggify's permission callback apply. */
export function autopilotAvailable(provider?: string | null): boolean {
  return workModeRuntime(provider) === 'claude'
}

/** Why the three options are not offered, for the muted line under today's controls. */
export function workModeUnavailableReason(provider?: string | null): string {
  switch (workModeRuntime(provider)) {
    case 'codex':
      return 'Autopilot is for the Claude CLI. Codex has its own sandbox profile: set the permission mode and the filesystem sandbox below.'
    case 'direct':
      return 'Autopilot needs a sandbox, and this provider runs its tools inside Eggify, which has none. Permission mode below applies.'
    case 'other':
      return 'Autopilot needs a provider on the Claude CLI. Whether this custom provider runs on it depends on its URL, so the permission mode and sandbox switch below apply.'
    default:
      return ''
  }
}

/** The form fields a choice sets. `sandboxed: null` follows the server default. */
export function workModeFields(mode: WorkMode): { permissionMode: string; sandboxed: boolean | null } {
  switch (mode) {
    case 'autopilot': return { permissionMode: 'yolo', sandboxed: true }
    case 'full': return { permissionMode: 'yolo', sandboxed: false }
    default: return { permissionMode: 'default', sandboxed: null }
  }
}

/**
 * The option a form's two fields amount to, or null when they match none
 * (Allow All, Read Only, or YOLO with the sandbox left to the server default).
 */
export function workModeOfForm(permissionMode: string | null | undefined, sandboxed: boolean | null | undefined): WorkMode | null {
  const pm = permissionMode || 'default'
  if (pm === 'default') return 'ask'
  if (pm === 'yolo') {
    if (sandboxed === true) return 'autopilot'
    if (sandboxed === false) return 'full'
  }
  return null
}

export function readSavedWorkMode(): WorkMode | null {
  try {
    const v = localStorage.getItem(WORK_MODE_STORAGE_KEY)
    return (WORK_MODES as readonly string[]).includes(v || '') ? (v as WorkMode) : null
  } catch {
    return null
  }
}

export function saveWorkMode(mode: WorkMode): void {
  try {
    localStorage.setItem(WORK_MODE_STORAGE_KEY, mode)
  } catch {
    // storage unavailable: the choice just is not remembered
  }
}

/**
 * The option a new session starts on: the user's last choice, else Autopilot.
 * null where Autopilot is not offered (today's controls apply there).
 */
export function initialWorkMode(provider: string | null | undefined, saved: WorkMode | null): WorkMode | null {
  if (!autopilotAvailable(provider)) return null
  return saved ?? 'autopilot'
}

export interface SessionWorkModeInput {
  options?: { dangerously_skip_permissions?: boolean | null; sandboxed?: boolean | null } | null
}

/** What a running session is: prompts off or on, sandboxed or not. */
export function workModeOfSession(session?: SessionWorkModeInput | null): WorkMode {
  const promptsOff = session?.options?.dangerously_skip_permissions === true
  const sandboxed = session?.options?.sandboxed === true
  if (!promptsOff) return 'ask'
  return sandboxed ? 'autopilot' : 'full'
}

/** The name the sidebar shows for a session's combination. */
export function workModeSummary(session?: SessionWorkModeInput | null): string {
  const mode = workModeOfSession(session)
  if (mode === 'autopilot') return 'Autopilot · sandboxed'
  if (mode === 'full') return 'Full access · not sandboxed'
  return 'Ask first'
}

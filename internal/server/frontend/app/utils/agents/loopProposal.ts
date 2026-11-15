/**
 * Loop sessions proposed by an agent (start_loop_session). The server asks the
 * user on a confirmation with `kind: "loop_session"` and the settings in
 * `proposal`; UserQuestionModal shows it as a dedicated card. "Edit first"
 * opens Create Session with these settings (applyLoopProposal).
 * Answers must match internal/server/agents/loop_spawn.go.
 */
import type { UserQuestion } from '~/stores/session/types'

export const LOOP_SESSION_KIND = 'loop_session'
export const LOOP_START = 'Start'
export const LOOP_EDIT = 'Edit first'
export const LOOP_DENY = 'Deny'

export interface LoopSessionProposal {
  goal: string
  verify_command?: string
  working_directory: string
  max_iterations: number
  timeout_minutes: number
  model: string
  provider: string
  context?: string
  yolo: boolean
  sandboxed: boolean
  parent_session_id: string
  project_id?: string
}

/** The proposal of a loop-session card, or null for any other question. */
export function loopProposalOf(q: UserQuestion | null | undefined): LoopSessionProposal | null {
  if (!q || q.kind !== LOOP_SESSION_KIND || !q.requires_user) return null
  const p = q.proposal as LoopSessionProposal | undefined
  if (!p || typeof p.goal !== 'string' || typeof p.working_directory !== 'string') return null
  return p
}

/**
 * Fills the Create Session form from a proposal: loop mode with its goal,
 * check and limits, YOLO, not sandboxed, linked to the proposing session (so
 * the loop still reports back to it). The model is kept only when that
 * provider is available here.
 */
/** The Create Session form fields applyLoopProposal sets. */
export const LOOP_PROPOSAL_FIELDS = [
  'workingDirectory', 'sessionMode', 'loopGoal', 'loopVerifyCommand', 'loopMaxIterations', 'loopTimeoutMinutes',
  'permissionMode', 'sandboxed', 'parentSessionId', 'spawnedBySessionId', 'contextSummary', 'modelProvider', 'model',
] as const

/**
 * The values of those fields, to put back once the proposal's Create Session
 * closes: the form is shared, and the next New Session must not start in YOLO
 * loop mode linked to the proposer.
 */
export function snapshotLoopFields(form: Record<string, any>): Record<string, any> {
  return Object.fromEntries(LOOP_PROPOSAL_FIELDS.map(key => [key, form[key]]))
}

export function applyLoopProposal(
  form: Record<string, any>,
  p: LoopSessionProposal,
  availableProviderIds: string[] = [],
): void {
  form.workingDirectory = p.working_directory
  form.sessionMode = 'loop'
  form.loopGoal = p.goal
  form.loopVerifyCommand = p.verify_command || ''
  form.loopMaxIterations = p.max_iterations
  form.loopTimeoutMinutes = p.timeout_minutes
  form.permissionMode = p.yolo ? 'yolo' : form.permissionMode
  form.sandboxed = p.sandboxed
  form.parentSessionId = p.parent_session_id
  form.spawnedBySessionId = p.parent_session_id
  form.contextSummary = p.context || ''
  if (p.provider && availableProviderIds.includes(p.provider)) {
    form.modelProvider = p.provider
    if (p.model) form.model = p.model
  }
}

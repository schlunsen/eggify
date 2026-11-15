/**
 * Worker sessions proposed by an agent (start_worker_session): another model
 * builds or reviews. The server asks the user on a confirmation with
 * `kind: "worker_session"` and the settings in `proposal`; UserQuestionModal
 * shows it as a dedicated card. Team sessions start workers without a card.
 * Answers must match internal/server/agents/worker_spawn.go.
 */
import type { UserQuestion } from '~/stores/session/types'

export const WORKER_SESSION_KIND = 'worker_session'
export const WORKER_START = 'Start'
export const WORKER_DENY = 'Deny'

export interface WorkerSessionProposal {
  task: string
  mode: 'build' | 'review'
  working_directory: string
  model: string
  provider: string
  /** claude-sdk | codex */
  runtime: string
  context?: string
  sandboxed: boolean
  read_only: boolean
  parent_session_id: string
  project_id?: string
  // Build mode with its own worktree.
  worktree_path?: string
  branch?: string
  base_branch?: string
  // Review mode linked to a build worker.
  reviews_session_id?: string
  review_round?: number
  max_review_rounds?: number
  team?: boolean
}

/** The proposal of a worker-session card, or null for any other question. */
export function workerProposalOf(q: UserQuestion | null | undefined): WorkerSessionProposal | null {
  if (!q || q.kind !== WORKER_SESSION_KIND || !q.requires_user) return null
  const p = q.proposal as WorkerSessionProposal | undefined
  if (!p || typeof p.task !== 'string' || typeof p.working_directory !== 'string') return null
  return p
}

export function workerProposalTitle(p: Pick<WorkerSessionProposal, 'mode'>): string {
  return p.mode === 'review' ? 'Start a review worker?' : 'Start a build worker?'
}

export interface WorkerBadge {
  text: string
  title: string
  /** Something the user should notice (the server always sandboxes; shown if it ever does not). */
  danger?: boolean
}

/**
 * The card's badges: Sandboxed; Read-only for a review; the worktree and
 * branch of a build; the review round when the review is linked to a builder.
 */
export function workerProposalBadges(p: WorkerSessionProposal): WorkerBadge[] {
  const badges: WorkerBadge[] = [p.sandboxed
    ? { text: 'Sandboxed', title: 'Commands run in the sandbox; permission prompts are off inside it' }
    : { text: 'Not sandboxed', title: 'This worker would run outside the sandbox', danger: true }]
  if (p.mode === 'review' || p.read_only) {
    badges.push({ text: 'Read-only', title: 'It cannot change files in its working directory' })
  }
  if (p.mode !== 'review' && p.branch) {
    badges.push({
      text: `Own worktree · ${p.branch}${p.base_branch ? ` from ${p.base_branch}` : ''}`,
      title: p.worktree_path ? `Its own git worktree: ${p.worktree_path}` : 'Its own git worktree and branch',
    })
  }
  if (p.reviews_session_id && p.review_round && p.max_review_rounds) {
    badges.push({
      text: `Review round ${p.review_round} of ${p.max_review_rounds}`,
      title: `Reviews build worker ${p.reviews_session_id.slice(0, 8)}`,
    })
  }
  return badges
}

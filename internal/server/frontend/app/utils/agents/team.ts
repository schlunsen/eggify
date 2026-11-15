/**
 * Worker sessions and teams (server: agents/worker_spawn.go, team.go).
 *
 * A worker is a session another session started to build or review
 * (`options.worker` + `options.spawned_by_session_id`). A team orchestrator is
 * a session with `options.team`: it plans, hands the building to a build
 * worker and the checking to a review worker, and merges once the review
 * approves. The review state lives on the build worker (`review_rounds`,
 * `last_verdict`); a review worker names the builder it reviews.
 *
 * Pure functions: the team strip, the worker header note, the sidebar and the
 * All agents overlay all read from here (`__tests__/utils/team.test.ts`).
 */
import { modelLabel } from './modelLabel'
import { hasLimitWait, type LimitWait } from './limitWait'

export type WorkerMode = 'build' | 'review'
export type Verdict = 'approve' | 'changes_requested'

export const DEFAULT_MAX_REVIEW_ROUNDS = 3
export const TEAM_MAX_REVIEW_ROUNDS = 5

export interface WorkerConfig {
  task?: string
  mode?: string
  worktree_path?: string
  branch?: string
  base_branch?: string
  reviews_session_id?: string | null
  review_rounds?: number
  max_review_rounds?: number
  last_verdict?: string
}

export interface TeamMember {
  provider: string
  model: string
}

export interface TeamConfig {
  builder: TeamMember
  reviewer: TeamMember
  max_review_rounds?: number
  brief_sent?: boolean
}

/** What these functions read from a listed session. */
export interface TeamSession {
  id: string
  status: string
  created_at?: string
  updated_at?: string
  model_name?: string
  subagents_running?: number
  limit_wait?: LimitWait | null
  options?: {
    model?: string | null
    spawned_by_session_id?: string | null
    worker?: WorkerConfig | null
    read_only?: boolean
    team?: TeamConfig | null
  } | null
}

/** working: its turn runs (or its subagents do); waiting: for the usage limit. */
export type WorkerStatus = 'working' | 'waiting' | 'idle' | 'ended'

export const WORKER_STATUS_LABELS: Record<WorkerStatus, string> = {
  working: 'Working',
  waiting: 'Waiting for the usage limit',
  idle: 'Idle',
  ended: 'Ended',
}

export const WORKER_ROLE_LABELS: Record<WorkerMode, string> = { build: 'Build', review: 'Review' }

export const VERDICT_LABELS: Record<Verdict, string> = {
  approve: 'Approved',
  changes_requested: 'Changes requested',
}

/** The worker settings of a session another session started, else null. */
export function workerOf(session: TeamSession | null | undefined): WorkerConfig | null {
  const worker = session?.options?.worker
  return worker && typeof worker === 'object' && session?.options?.spawned_by_session_id ? worker : null
}

export function isWorkerSession(session: TeamSession | null | undefined): boolean {
  return workerOf(session) !== null
}

/** The session that started this worker, '' for any other session. */
export function workerParentId(session: TeamSession | null | undefined): string {
  return workerOf(session) ? String(session!.options!.spawned_by_session_id) : ''
}

export function workerMode(session: TeamSession | null | undefined): WorkerMode {
  return workerOf(session)?.mode === 'review' ? 'review' : 'build'
}

export function isTeamSession(session: TeamSession | null | undefined): boolean {
  const team = session?.options?.team
  return !!team && typeof team === 'object'
}

function verdictOf(value: unknown): Verdict | null {
  return value === 'approve' || value === 'changes_requested' ? value : null
}

/** The latest review verdict of a build worker, null before its first review. */
export function workerVerdict(session: TeamSession | null | undefined): Verdict | null {
  return verdictOf(workerOf(session)?.last_verdict)
}

/**
 * Working the way the sidebar counts it: the session's turn runs, or it is
 * idle while its subagents carry on. `subagentsWorking` overrides the listed
 * count (the sidebar knows the live one).
 */
export function workerStatus(session: TeamSession, subagentsWorking?: number): WorkerStatus {
  if (session.status === 'ended') return 'ended'
  if (session.status === 'processing') return 'working'
  if ((subagentsWorking ?? session.subagents_running ?? 0) > 0) return 'working'
  return hasLimitWait(session.limit_wait) ? 'waiting' : 'idle'
}

const createdAt = (session: TeamSession) => Date.parse(session.created_at || '') || 0

/** The workers `parentId` started, oldest first (duplicate ids once). */
export function workersOf<T extends TeamSession>(sessions: readonly T[] | null | undefined, parentId: string | null | undefined): T[] {
  if (!parentId) return []
  const seen = new Set<string>()
  const out: T[] = []
  for (const session of sessions || []) {
    if (!session?.id || session.id === parentId || seen.has(session.id)) continue
    if (workerParentId(session) !== parentId) continue
    seen.add(session.id)
    out.push(session)
  }
  return out.sort((a, b) => createdAt(a) - createdAt(b) || a.id.localeCompare(b.id))
}

/** One worker as the UI shows it. */
export interface TeamWorkerView {
  sessionId: string
  role: WorkerMode
  /** The model id it runs on ('' when unknown). */
  model: string
  /** "Sonnet 5.5", "deepseek-flash", '' when unknown. */
  modelLabel: string
  status: WorkerStatus
  /** Builder: its latest review. Reviewer: the verdict it gave (none while it works). */
  verdict: Verdict | null
  branch: string
  /** The builder and reviewer the strip's steps are about. */
  current: boolean
}

export type TeamStage =
  | 'planning'   // no workers yet
  | 'building'   // the builder works
  | 'reviewing'  // the reviewer works
  | 'fixing'     // the builder works after a changes-requested verdict
  | 'approved'   // the review approved: the orchestrator merges
  | 'stopped'    // last round used up with changes still requested
  | 'idle'       // nobody works and none of the above

export type TeamStep = 'plan' | 'build' | 'review' | 'merge'
export const TEAM_STEPS: readonly TeamStep[] = ['plan', 'build', 'review', 'merge']

export const TEAM_STAGE_LABELS: Record<TeamStage, string> = {
  planning: 'Planning',
  building: 'Building',
  reviewing: 'Reviewing',
  fixing: 'Fixing review findings',
  approved: 'Approved',
  stopped: 'Stopped: changes still requested',
  idle: 'Idle',
}

export interface TeamProgress {
  stage: TeamStage
  /** The step the strip marks. */
  step: TeamStep
  builder: TeamWorkerView | null
  reviewer: TeamWorkerView | null
  /** Every worker, oldest first. */
  workers: TeamWorkerView[]
  /** The review round in progress or last finished (0 before the first review). */
  round: number
  maxRounds: number
  lastVerdict: Verdict | null
}

function modelOf(session: TeamSession): string {
  return (session.options?.model || session.model_name || '').trim()
}

function clampRounds(value: unknown): number {
  const n = typeof value === 'number' && Number.isFinite(value) ? Math.floor(value) : 0
  return n > 0 ? Math.min(n, TEAM_MAX_REVIEW_ROUNDS) : 0
}

/**
 * Where a session's build–review work stands. With several builders the
 * newest one counts, with the reviewers that review it; reviews of an older
 * builder never decide the stage.
 */
export function teamProgress(parent: TeamSession | null | undefined, workers: readonly TeamSession[]): TeamProgress {
  const builders = workers.filter(w => workerMode(w) === 'build')
  const builder = builders[builders.length - 1] ?? null
  const reviewers = workers.filter(w => workerMode(w) === 'review')
  const ofBuilder = builder
    ? reviewers.filter(r => workerOf(r)?.reviews_session_id === builder.id)
    : reviewers
  const reviewer = ofBuilder[ofBuilder.length - 1] ?? null

  const config = workerOf(builder)
  const lastVerdict = workerVerdict(builder)
  const done = Math.max(0, Math.floor(config?.review_rounds || 0))
  const maxRounds = clampRounds(config?.max_review_rounds)
    || clampRounds(parent?.options?.team?.max_review_rounds)
    || DEFAULT_MAX_REVIEW_ROUNDS

  const builderStatus = builder ? workerStatus(builder) : null
  const reviewerStatus = reviewer ? workerStatus(reviewer) : null
  const reviewing = reviewerStatus === 'working'

  let stage: TeamStage
  let step: TeamStep
  if (!builder && !reviewer) {
    stage = 'planning'
    step = 'plan'
  } else if (reviewing) {
    stage = 'reviewing'
    step = 'review'
  } else if (builderStatus === 'working') {
    stage = lastVerdict === 'changes_requested' ? 'fixing' : 'building'
    step = 'build'
  } else if (lastVerdict === 'approve') {
    stage = 'approved'
    step = 'merge'
  } else if (lastVerdict === 'changes_requested' && done >= maxRounds) {
    stage = 'stopped'
    step = 'review'
  } else {
    stage = 'idle'
    // Findings wait for the builder; otherwise the next thing is the review.
    step = lastVerdict === 'changes_requested' || !reviewer ? 'build' : 'review'
  }

  // A running review is the round after the ones that already have a verdict.
  const round = Math.min(maxRounds, reviewing ? done + 1 : done || (reviewer ? 1 : 0))

  const view = (session: TeamSession): TeamWorkerView => {
    const role = workerMode(session)
    const status = workerStatus(session)
    const model = modelOf(session)
    let verdict: Verdict | null = null
    if (role === 'build') verdict = workerVerdict(session)
    else if (status !== 'working') {
      const reviewed = workers.find(w => w.id === workerOf(session)?.reviews_session_id)
      // Only the newest reviewer of a builder gave its latest verdict.
      const latest = reviewed ? reviewers.filter(r => workerOf(r)?.reviews_session_id === reviewed.id).pop() : null
      if (reviewed && latest?.id === session.id) verdict = workerVerdict(reviewed)
    }
    return {
      sessionId: session.id,
      role,
      model,
      modelLabel: modelLabel(model),
      status,
      verdict,
      branch: workerOf(session)?.branch || '',
      current: session.id === builder?.id || session.id === reviewer?.id,
    }
  }
  const views = workers.map(view)

  return {
    stage,
    step,
    builder: views.find(v => v.sessionId === builder?.id) ?? null,
    reviewer: views.find(v => v.sessionId === reviewer?.id) ?? null,
    workers: views,
    round,
    maxRounds,
    lastVerdict,
  }
}

/** Whether a session's chat shows the team strip: a team, or anything with workers. */
export function showsTeamStrip(session: TeamSession | null | undefined, workers: readonly TeamSession[]): boolean {
  if (!session || isWorkerSession(session)) return false
  return isTeamSession(session) || workers.length > 0
}

/** "Review (round 2 of 3)" once a review round exists, else "Review". */
export function reviewStepLabel(progress: Pick<TeamProgress, 'round' | 'maxRounds'>): string {
  return progress.round > 0 ? `Review (round ${progress.round} of ${progress.maxRounds})` : 'Review'
}

/**
 * The quiet line at the top of a worker's own chat:
 * "Build worker of <parent> · branch worker/x" / "Review worker of <parent> · read-only".
 */
export function workerNote(session: TeamSession | null | undefined, parentName: string): { lead: string; parentName: string; detail: string } | null {
  const worker = workerOf(session)
  if (!worker) return null
  const review = workerMode(session) === 'review'
  const details: string[] = []
  if (review || session?.options?.read_only) details.push('read-only')
  if (!review && worker.branch) details.push(`branch ${worker.branch}`)
  return {
    lead: review ? 'Review worker of' : 'Build worker of',
    parentName: parentName || `Session ${workerParentId(session).slice(0, 8)}`,
    detail: details.join(' · '),
  }
}

// ── Create Session → Team ──

export const TEAM_SETUP_KEY = 'eggify-team-setup'

export interface TeamSetup {
  enabled: boolean
  builder: TeamMember
  reviewer: TeamMember
  maxReviewRounds: number
}

export function defaultTeamSetup(): TeamSetup {
  return {
    enabled: false,
    builder: { provider: '', model: '' },
    reviewer: { provider: '', model: '' },
    maxReviewRounds: DEFAULT_MAX_REVIEW_ROUNDS,
  }
}

/** Review rounds as the server accepts them: a whole number from 1 to 5, default 3. */
export function normalizeReviewRounds(value: unknown): number {
  const n = typeof value === 'number' ? value : Number.parseInt(String(value ?? ''), 10)
  if (!Number.isFinite(n) || n < 1) return DEFAULT_MAX_REVIEW_ROUNDS
  return Math.min(Math.floor(n), TEAM_MAX_REVIEW_ROUNDS)
}

function memberOf(value: unknown): TeamMember {
  const m = value && typeof value === 'object' ? value as Record<string, unknown> : {}
  return {
    provider: typeof m.provider === 'string' ? m.provider.trim() : '',
    model: typeof m.model === 'string' ? m.model.trim() : '',
  }
}

/** A stored or half-filled setup made safe to use. */
export function normalizeTeamSetup(value: unknown): TeamSetup {
  const v = value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
  return {
    enabled: v.enabled === true,
    builder: memberOf(v.builder),
    reviewer: memberOf(v.reviewer),
    maxReviewRounds: normalizeReviewRounds(v.maxReviewRounds),
  }
}

/** The setup with members cleared whose model no provider offers (any more). */
export function offeredTeamSetup(setup: TeamSetup, providers: readonly { id: string; models: readonly string[] }[]): TeamSetup {
  const offered = (m: TeamMember) => providers.some(p => p.id === m.provider && p.models.includes(m.model))
  const keep = (m: TeamMember): TeamMember => offered(m) ? m : { provider: '', model: '' }
  return { ...setup, builder: keep(setup.builder), reviewer: keep(setup.reviewer) }
}

/** The last team setup of this browser (the defaults when none or unreadable). */
export function loadTeamSetup(): TeamSetup {
  try {
    const raw = localStorage.getItem(TEAM_SETUP_KEY)
    if (raw) return normalizeTeamSetup(JSON.parse(raw))
  } catch { /* blocked storage or bad JSON */ }
  return defaultTeamSetup()
}

export function saveTeamSetup(setup: TeamSetup): void {
  try {
    localStorage.setItem(TEAM_SETUP_KEY, JSON.stringify(normalizeTeamSetup(setup)))
  } catch { /* blocked storage: the setup lasts for this dialog */ }
}

/** `options.team` for create_session, or null when the team is off or incomplete. */
export function teamOptions(setup: TeamSetup | null | undefined): TeamConfig | null {
  if (!setup?.enabled) return null
  const { builder, reviewer } = normalizeTeamSetup(setup)
  if (!builder.provider || !builder.model || !reviewer.provider || !reviewer.model) return null
  return { builder, reviewer, max_review_rounds: normalizeReviewRounds(setup.maxReviewRounds) }
}

/** What is missing before a team can be created, '' when it is complete. */
export function teamSetupProblem(setup: TeamSetup | null | undefined): string {
  if (!setup?.enabled) return ''
  const { builder, reviewer } = normalizeTeamSetup(setup)
  if (!builder.provider || !builder.model) return 'Pick a model for the builder.'
  if (!reviewer.provider || !reviewer.model) return 'Pick a model for the reviewer.'
  return ''
}

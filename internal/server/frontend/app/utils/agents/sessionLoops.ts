/**
 * Everything that keeps a session going on its own, as one list for the
 * "All agents" overlay (⇧⌘S):
 *
 * - **cron**: Claude Code cron jobs (CronCreate), `scheduled_jobs` on the
 *   session listing. They run the prompt as a new turn on their schedule.
 * - **wakeup**: Claude Code's /loop (ScheduleWakeup), `scheduled_loop`.
 * - **eggify**: Eggify's own Loop mode (goal + verify command), `loop_state`.
 *
 * Cron jobs and wakeups live in the session's Claude CLI and the server
 * forgets them when that CLI closes (claude_cron.go, claude_loop.go).
 */
import { loopPhase, type ScheduledLoop } from './scheduledLoop'

export interface ScheduledJob {
  id: string
  cron: string
  /** The CLI's own wording: "Every 30 minutes". */
  schedule?: string
  prompt?: string
  recurring: boolean
  durable?: boolean
  created_at: string
  expires_at?: string
  next_at: string
  last_run_at?: string
}

export interface LoopState {
  type: string
  iteration: number
  max_iterations: number
  goal?: string
  verify_command?: string
  verify_passed?: boolean
  verify_output?: string
  reason?: string
}

export interface LoopSource {
  id: string
  status: string
  scheduled_loop?: (ScheduledLoop & { kind?: string }) | null
  scheduled_jobs?: ScheduledJob[] | null
  loop_state?: LoopState | null
}

export type SessionLoopKind = 'cron' | 'wakeup' | 'eggify'
/** waiting: until next_at · running: an iteration runs now · due: next_at passed, not started yet. */
export type SessionLoopPhase = 'waiting' | 'running' | 'due'

export interface SessionLoop {
  /** Unique within the session. */
  id: string
  kind: SessionLoopKind
  phase: SessionLoopPhase
  /** What it does, one line. */
  title: string
  /** How often: "Every 30 minutes", "/loop", "Iteration 3 of 20". */
  schedule: string
  /** Cron expression (cron jobs only). */
  cron?: string
  nextAt?: number
  lastRunAt?: number
  createdAt?: number
  expiresAt?: number
  recurring?: boolean
  prompt?: string
  /** Eggify loops. */
  iteration?: number
  maxIterations?: number
  verifyCommand?: string
  verifyPassed?: boolean
  verifyOutput?: string
}

export const LOOP_KIND_LABELS: Record<SessionLoopKind, string> = {
  cron: 'Scheduled job',
  wakeup: 'Claude Code /loop',
  eggify: 'Eggify loop'
}

/** Eggify loop events that mean it still runs. */
const EGGIFY_LOOP_ACTIVE = new Set(['loop_started', 'loop_iteration', 'loop_verifying'])

/** How long after its slot a cron job still counts as due (the CLI starts late). */
const CRON_DUE_GRACE_MS = 60 * 60000

const parse = (value?: string | null) => {
  const t = Date.parse(value || '')
  return Number.isFinite(t) ? t : undefined
}

function firstLine(text: string | undefined, max = 140): string {
  const line = (text || '').split('\n').map(l => l.trim()).find(Boolean) || ''
  return line.length > max ? `${line.slice(0, max - 1)}…` : line
}

function cronPhase(nextAt: number | undefined, processing: boolean, now: number): SessionLoopPhase {
  if (processing) return 'running'
  if (nextAt === undefined || nextAt > now) return 'waiting'
  return now - nextAt < CRON_DUE_GRACE_MS ? 'due' : 'waiting'
}

/** A session's active loops: Eggify loop first, then soonest first. */
export function sessionLoops(session: LoopSource, now = Date.now()): SessionLoop[] {
  const processing = session.status === 'processing' || session.status === 'active'
  const loops: SessionLoop[] = []

  const state = session.loop_state
  if (state && EGGIFY_LOOP_ACTIVE.has(state.type) && session.status !== 'ended') {
    loops.push({
      id: 'eggify',
      kind: 'eggify',
      phase: processing || state.type === 'loop_verifying' ? 'running' : 'waiting',
      title: firstLine(state.goal) || 'Loop until the check passes',
      schedule: state.max_iterations ? `Iteration ${state.iteration} of ${state.max_iterations}` : `Iteration ${state.iteration}`,
      iteration: state.iteration,
      maxIterations: state.max_iterations,
      prompt: state.goal,
      verifyCommand: state.verify_command,
      verifyPassed: state.verify_passed,
      verifyOutput: state.verify_output
    })
  }

  const timed: SessionLoop[] = []
  for (const job of session.scheduled_jobs || []) {
    if (!job?.id) continue
    const expiresAt = parse(job.expires_at)
    if (expiresAt !== undefined && expiresAt <= now) continue
    const nextAt = parse(job.next_at)
    timed.push({
      id: `cron:${job.id}`,
      kind: 'cron',
      phase: cronPhase(nextAt, processing, now),
      title: firstLine(job.prompt) || job.cron,
      schedule: job.schedule || job.cron,
      cron: job.cron,
      nextAt,
      lastRunAt: parse(job.last_run_at),
      createdAt: parse(job.created_at),
      expiresAt,
      recurring: job.recurring,
      prompt: job.prompt
    })
  }

  // The listing's scheduled_loop is the soonest of the wakeup and the cron
  // jobs (kind "cron"); older servers send wakeups only, without a kind.
  const wakeup = session.scheduled_loop
  if (wakeup && wakeup.kind !== 'cron') {
    const phase = loopPhase(wakeup, session.status, now)
    if (phase) {
      timed.push({
        id: 'wakeup',
        kind: 'wakeup',
        phase,
        title: firstLine(wakeup.reason) || 'Runs /loop again',
        schedule: '/loop',
        nextAt: parse(wakeup.next_at),
        createdAt: parse(wakeup.scheduled_at)
      })
    }
  }

  timed.sort((a, b) => (a.nextAt ?? Infinity) - (b.nextAt ?? Infinity))
  return loops.concat(timed)
}

function waitText(ms: number): string {
  const minutes = Math.round(ms / 60000)
  if (minutes < 1) return 'under a minute'
  if (minutes < 60) return `${minutes} min`
  const hours = Math.floor(minutes / 60)
  if (hours < 48) return minutes % 60 ? `${hours} h ${minutes % 60} min` : `${hours} h`
  return `${Math.round(hours / 24)} days`
}

/** "next in 14 min", "running now", "due now", "check running". */
export function loopWhen(loop: SessionLoop, now: number): string {
  if (loop.kind === 'eggify') return loop.phase === 'running' ? 'iteration running' : 'between iterations'
  if (loop.phase === 'running') return 'running now'
  if (loop.phase === 'due') return 'due now'
  if (loop.nextAt === undefined) return 'no next run'
  return `next in ${waitText(loop.nextAt - now)}`
}

/** Short form for the row's time column: "14m", "2h", "now". */
export function loopWhenShort(loop: SessionLoop, now: number): string {
  if (loop.kind === 'eggify') return loop.maxIterations ? `${loop.iteration}/${loop.maxIterations}` : `#${loop.iteration}`
  if (loop.phase !== 'waiting' || loop.nextAt === undefined) return 'now'
  const minutes = Math.max(1, Math.round((loop.nextAt - now) / 60000))
  if (minutes < 60) return `${minutes}m`
  const hours = Math.round(minutes / 60)
  return hours < 48 ? `${hours}h` : `${Math.round(hours / 24)}d`
}

export { waitText as loopWaitText }

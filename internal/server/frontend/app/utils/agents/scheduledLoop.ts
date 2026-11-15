/**
 * Claude Code's own loops: the session's agent scheduled its next run with
 * ScheduleWakeup (/loop) or a CronCreate job. The server keeps the soonest
 * one per live session (`scheduled_loop` on session listings and
 * session_updated events, `kind` "wakeup" or "cron"; every cron job is in
 * `scheduled_jobs`) and drops it when the loop ends or the session's CLI closes.
 */
import { onBeforeUnmount, onMounted, ref, type Ref } from 'vue'

export interface ScheduledLoop {
  next_at: string
  delay_seconds: number
  reason?: string
  scheduled_at?: string
  kind?: 'wakeup' | 'cron'
}

/** How long after its due time a wakeup still counts as about to run. */
const DUE_GRACE_MS = 5 * 60000
/** Cron runs start up to several minutes late (the CLI spreads them out). */
const CRON_DUE_GRACE_MS = 60 * 60000

function waitText(ms: number): string {
  const minutes = Math.round(ms / 60000)
  if (minutes < 1) return 'under a minute'
  if (minutes < 60) return `${minutes} min`
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  return rest ? `${hours} h ${rest} min` : `${hours} h`
}

export type LoopPhase = 'waiting' | 'running' | 'due'

/** Where a session's loop is, or null when there is none (or it went stale). */
export function loopPhase(loop: ScheduledLoop | null | undefined, status: string | undefined, now: number): LoopPhase | null {
  if (!loop) return null
  const next = Date.parse(loop.next_at)
  if (!Number.isFinite(next)) return null
  if (next > now) return status === 'processing' ? 'running' : 'waiting'
  if (status === 'processing') return 'running'
  return now - next < (loop.kind === 'cron' ? CRON_DUE_GRACE_MS : DUE_GRACE_MS) ? 'due' : null
}

/** "Loop · next in 25 min", "Loop · iteration running", "Loop · due now"; '' when none. */
export function loopLabel(loop: ScheduledLoop | null | undefined, status: string | undefined, now: number): string {
  switch (loopPhase(loop, status, now)) {
    case 'waiting': return `Loop · next in ${waitText(Date.parse(loop!.next_at) - now)}`
    case 'running': return 'Loop · iteration running'
    case 'due': return 'Loop · due now'
    default: return ''
  }
}

/** Short form for tight rows: "loop 25m", "loop now". */
export function loopShortLabel(loop: ScheduledLoop | null | undefined, status: string | undefined, now: number): string {
  const phase = loopPhase(loop, status, now)
  if (!phase) return ''
  if (phase !== 'waiting') return 'loop now'
  const minutes = Math.max(1, Math.round((Date.parse(loop!.next_at) - now) / 60000))
  return minutes < 60 ? `loop ${minutes}m` : `loop ${Math.floor(minutes / 60)}h`
}

// One clock for every loop label, ticking while anything shows one.
const loopNow = ref(Date.now())
let users = 0
let timer: ReturnType<typeof setInterval> | null = null

/** The current time, updated every 30 s while a component using it is mounted. */
export function useLoopClock(): Ref<number> {
  onMounted(() => {
    if (users++ === 0) {
      loopNow.value = Date.now()
      timer = setInterval(() => { loopNow.value = Date.now() }, 30000)
    }
  })
  onBeforeUnmount(() => {
    if (--users === 0 && timer) { clearInterval(timer); timer = null }
  })
  return loopNow
}

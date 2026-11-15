/**
 * Times of a subagent's steps (from the per-line times the server keeps, see
 * BackgroundAgent.recent_output_at): a compact clock time per step, a tooltip
 * with the full date and how long ago, and how long a step took.
 */
import { activityAgo } from './subagentActivity'

const pad = (n: number) => String(n).padStart(2, '0')

/** "14:03:27" in local time; '' when unknown. */
export function clockTime(ms: number | undefined): string {
  if (!ms || !Number.isFinite(ms)) return ''
  const d = new Date(ms)
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

/** "Sat, 11 Oct 2026, 14:03:27 · 3m ago" (the step's tooltip); '' when unknown. */
export function stepTimeTitle(ms: number | undefined, now: number): string {
  if (!ms || !Number.isFinite(ms)) return ''
  const full = new Date(ms).toLocaleString(undefined, {
    weekday: 'short', day: 'numeric', month: 'short', year: 'numeric',
    hour: '2-digit', minute: '2-digit', second: '2-digit'
  })
  return `${full} · ${activityAgo(ms, now)}`
}

/** "850 ms", "2.3 s", "41 s", "3 min 5 s", "1 h 4 min"; '' when unknown. */
export function stepDuration(ms: number | undefined): string {
  if (ms === undefined || !Number.isFinite(ms) || ms < 0) return ''
  if (ms < 1000) return `${Math.round(ms)} ms`
  if (ms < 10_000) return `${(Math.round(ms / 100) / 10).toFixed(1)} s`
  const seconds = Math.round(ms / 1000)
  if (seconds < 60) return `${seconds} s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return seconds % 60 ? `${minutes} min ${seconds % 60} s` : `${minutes} min`
  return `${Math.floor(minutes / 60)} h ${minutes % 60} min`
}

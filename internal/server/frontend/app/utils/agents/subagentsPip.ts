/**
 * The floating subagents card (PiP) over the chat: which subagents it lists
 * and whether it shows at all. Running agents come first (oldest first, so
 * rows don't jump), then the ones that finished in the last few minutes
 * (newest first), so the user sees each one land before it leaves.
 */
import type { SubagentDisplayStatus } from './subagentActivity'

/** How long a finished subagent stays in the card. */
export const PIP_RECENT_MS = 3 * 60 * 1000
/** Rows shown at most; the rest fold into "+N more". */
export const PIP_MAX_ROWS = 5

export interface PipCandidate {
  id: string
  status: SubagentDisplayStatus
  startedAt: number
  endedAt: number
}

export interface PipSelection<T> {
  rows: T[]
  hidden: number
  running: number
  finished: number
  failed: number
}

export function pipSelection<T extends PipCandidate>(items: readonly T[], now: number, max = PIP_MAX_ROWS): PipSelection<T> {
  const running = items.filter(i => i.status === 'running')
    .sort((a, b) => (a.startedAt || 0) - (b.startedAt || 0))
  const recent = items.filter(i => i.status !== 'running' && Number.isFinite(i.endedAt) && now - i.endedAt < PIP_RECENT_MS)
    .sort((a, b) => b.endedAt - a.endedAt)
  const all = [...running, ...recent]
  return {
    rows: all.slice(0, max),
    hidden: Math.max(0, all.length - max),
    running: running.length,
    finished: recent.filter(i => i.status === 'done').length,
    failed: recent.filter(i => i.status === 'failed' || i.status === 'interrupted').length
  }
}

/**
 * The order the subagent modal steps through (‹ ›): the card's own order
 * (running oldest first, then recently finished newest first, none cut off),
 * then every other subagent of the session as the Subagents view lists them
 * (running oldest first, finished newest first).
 */
export function pipNavOrder<T extends PipCandidate & { created_at: string }>(items: readonly T[], now: number): T[] {
  const first = pipSelection(items, now, Infinity).rows
  const seen = new Set(first.map(i => i.id))
  const rest = items.filter(i => !seen.has(i.id))
  const running = rest.filter(i => i.status === 'running')
    .sort((a, b) => (Date.parse(a.created_at) || 0) - (Date.parse(b.created_at) || 0))
  const finished = rest.filter(i => i.status !== 'running')
    .sort((a, b) => (Number.isFinite(b.endedAt) ? b.endedAt : 0) - (Number.isFinite(a.endedAt) ? a.endedAt : 0))
  return [...first, ...running, ...finished]
}

/** "2 running · 1 done", "1 done · 1 failed". */
export function pipSummary(sel: Pick<PipSelection<unknown>, 'running' | 'finished' | 'failed'>): string {
  const parts: string[] = []
  if (sel.running) parts.push(`${sel.running} running`)
  if (sel.finished) parts.push(`${sel.finished} done`)
  if (sel.failed) parts.push(`${sel.failed} failed`)
  return parts.join(' · ')
}

const COLLAPSED_KEY = 'eggify-subagents-pip-collapsed'

export function loadPipCollapsed(): boolean {
  try { return localStorage.getItem(COLLAPSED_KEY) === '1' } catch { return false }
}

export function savePipCollapsed(collapsed: boolean): void {
  try {
    if (collapsed) localStorage.setItem(COLLAPSED_KEY, '1')
    else localStorage.removeItem(COLLAPSED_KEY)
  } catch { /* private mode: not remembered */ }
}

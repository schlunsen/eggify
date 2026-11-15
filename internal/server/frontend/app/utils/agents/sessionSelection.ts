// Sidebar select mode: pick sessions with checkboxes, then end or delete
// them in one go (SessionsSidebar.vue).

export interface SelectableSession {
  id: string
  status?: string
  pending_permissions?: number
  pending_questions?: number
}

/** A copy of `selected` with `id` flipped. */
export function toggleSelected(selected: ReadonlySet<string>, id: string): Set<string> {
  const next = new Set(selected)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  return next
}

/**
 * Shift-click: every row between the last clicked one (`anchor`) and `id`, in
 * on-screen order, takes the state `id` gets. Without an anchor on screen it's
 * a plain toggle.
 */
export function selectRange(selected: ReadonlySet<string>, order: readonly string[], anchor: string | null, id: string): Set<string> {
  const from = anchor ? order.indexOf(anchor) : -1
  const to = order.indexOf(id)
  if (from < 0 || to < 0) return toggleSelected(selected, id)
  const on = !selected.has(id)
  const next = new Set(selected)
  const [lo, hi] = from < to ? [from, to] : [to, from]
  for (let i = lo; i <= hi; i++) {
    const rowId = order[i]!
    if (on) next.add(rowId)
    else next.delete(rowId)
  }
  return next
}

export type GroupCheckState = 'none' | 'some' | 'all'

export function groupCheckState(ids: readonly string[], selected: ReadonlySet<string>): GroupCheckState {
  let n = 0
  for (const id of ids) if (selected.has(id)) n++
  if (n === 0) return 'none'
  return n === ids.length ? 'all' : 'some'
}

/** Group header checkbox: select every row of the group, or clear them all once it's full. */
export function toggleGroupSelected(selected: ReadonlySet<string>, ids: readonly string[]): Set<string> {
  const next = new Set(selected)
  const all = groupCheckState(ids, selected) === 'all'
  for (const id of ids) {
    if (all) next.delete(id)
    else next.add(id)
  }
  return next
}

/** Drop ids that are no longer in the list (deleted elsewhere). */
export function pruneSelected(selected: ReadonlySet<string>, present: Iterable<string>): Set<string> {
  const keep = new Set(present)
  const next = new Set<string>()
  for (const id of selected) if (keep.has(id)) next.add(id)
  return next.size === selected.size ? (selected as Set<string>) : next
}

export interface SelectionSummary {
  /** Selected sessions still in the list */
  ids: string[]
  /** Those not ended yet (what End acts on) */
  endable: string[]
  /** Those working or waiting on you right now */
  busy: number
}

export function summarizeSelection(sessions: readonly SelectableSession[], selected: ReadonlySet<string>): SelectionSummary {
  const ids: string[] = []
  const endable: string[] = []
  let busy = 0
  for (const s of sessions) {
    if (!selected.has(s.id)) continue
    ids.push(s.id)
    if (s.status !== 'ended') endable.push(s.id)
    if (s.status === 'processing' || (s.pending_permissions || 0) > 0 || (s.pending_questions || 0) > 0) busy++
  }
  return { ids, endable, busy }
}

/** "3 sessions" / "1 session" */
export function sessionCountLabel(n: number): string {
  return `${n} session${n === 1 ? '' : 's'}`
}

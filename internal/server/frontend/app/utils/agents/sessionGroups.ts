/**
 * The agents page sidebar groups sessions by what they need from you:
 *
 *   Needs you  a permission request or AskUserQuestion is waiting
 *   Working    the session's turn is running, its subagents still work while
 *              it is idle, or a Claude Code /loop is scheduled
 *   Today      everything else with activity since local midnight
 *   Earlier    the rest (collapsed by default)
 *
 * Needs you and Working show full rows; so does an idle session whose last
 * activity is under FULL_ROW_AFTER_IDLE_MS old, so an agent that just finished
 * stays readable before it shrinks to one line (ended sessions stay small).
 *
 * Ended sessions only go into Today / Earlier. Within a group the newest
 * activity comes first. Keyboard navigation (list ↑/↓, ⌥⌘↑/↓, ⌘1…9, ⌘[ ⌘])
 * follows `visibleSessionOrder`, the order on screen without collapsed groups.
 *
 * Worker sessions (utils/agents/team.ts) are not rows of their own: each hangs
 * under the session that started it, in that session's group, oldest first. A
 * working worker keeps its parent in Working. A worker whose parent is not in
 * the list, or has ended, is an ordinary row; so is one that waits for you
 * (it belongs in Needs you, in sight).
 */
import { loopPhase, type ScheduledLoop } from './scheduledLoop'
import { hasLimitWait, type LimitWait } from './limitWait'
import { teamProgress, workerParentId, type TeamSession, type TeamWorkerView } from './team'

export type SessionGroupId = 'needs' | 'working' | 'today' | 'earlier'

export const SESSION_GROUP_LABELS: Record<SessionGroupId, string> = {
  needs: 'Needs you',
  working: 'Working',
  today: 'Today',
  earlier: 'Earlier',
}

/** Groups whose header collapses them on click. */
export const COLLAPSIBLE_GROUPS: readonly SessionGroupId[] = ['today', 'earlier']
export const DEFAULT_COLLAPSED: Record<string, boolean> = { today: false, earlier: true }

/** How long an idle session keeps its full row after its last activity. */
export const FULL_ROW_AFTER_IDLE_MS = 12 * 3600_000

export interface GroupableSession {
  id: string
  status: string
  created_at?: string
  updated_at?: string
  scheduled_loop?: ScheduledLoop | null
  /** Waiting for the usage limit to reset (utils/agents/limitWait.ts). */
  limit_wait?: LimitWait | null
  model_name?: string
  subagents_running?: number
  /** `worker` + `spawned_by_session_id` make it a worker (utils/agents/team.ts). */
  options?: TeamSession['options']
}

/** A worker session under its parent's row: role, model, status, verdict. */
export interface WorkerRow<T extends GroupableSession = GroupableSession> {
  session: T
  view: TeamWorkerView
}

export interface SessionRow<T extends GroupableSession = GroupableSession> {
  session: T
  group: SessionGroupId
  /** What the session waits for ("Allow gh pr merge 1124?"); '' when nothing. */
  waitingFor: string
  /** Subagents still working while the session itself is idle. */
  subagents: number
  activityAt: number
  /** Full row (summary, tags, context) rather than one line. */
  full: boolean
  /** The worker sessions it started, oldest first. */
  workers: WorkerRow<T>[]
  /** How many of them are working. */
  workersWorking: number
}

export interface SessionGroup<T extends GroupableSession = GroupableSession> {
  id: SessionGroupId
  label: string
  collapsible: boolean
  rows: SessionRow<T>[]
}

export interface GroupInputs<T> {
  now: number
  /** What the session waits for from the user, '' when nothing. */
  waitingFor?: (session: T) => string
  /** How many of its subagents are still working. */
  subagentsWorking?: (session: T) => number
}

/** Last activity: updated_at, else created_at, else 0. */
export function sessionActivityAt(session: GroupableSession): number {
  const updated = Date.parse(session.updated_at || '')
  if (Number.isFinite(updated)) return updated
  const created = Date.parse(session.created_at || '')
  return Number.isFinite(created) ? created : 0
}

/** Local midnight of the day `now` falls on. */
export function startOfLocalDay(now: number): number {
  const d = new Date(now)
  d.setHours(0, 0, 0, 0)
  return d.getTime()
}

/**
 * Which sessions hang under another one: workers whose parent is in the list,
 * has not ended and is a row of its own. Keyed by the parent, oldest first.
 * A worker `needsUser` says waits for the user stays a row of its own.
 */
export function nestedWorkers<T extends GroupableSession>(sessions: readonly T[], needsUser?: (session: T) => boolean): Map<string, T[]> {
  const byId = new Map<string, T>()
  for (const session of sessions) if (session?.id && !byId.has(session.id)) byId.set(session.id, session)
  const parentOf = (session: T): T | undefined => {
    const parentId = workerParentId(session)
    return parentId && parentId !== session.id ? byId.get(parentId) : undefined
  }
  const nested = new Map<string, T[]>()
  for (const session of byId.values()) {
    const parent = parentOf(session)
    if (!parent || parent.status === 'ended' || parentOf(parent)) continue
    if (session.status !== 'ended' && needsUser?.(session)) continue
    const list = nested.get(parent.id)
    if (list) list.push(session)
    else nested.set(parent.id, [session])
  }
  const created = (session: T) => Date.parse(session.created_at || '') || 0
  for (const list of nested.values()) list.sort((a, b) => created(a) - created(b) || a.id.localeCompare(b.id))
  return nested
}

/** Puts every session (duplicate ids once) into its group. All four groups are returned, empty ones too. */
export function groupSessions<T extends GroupableSession>(sessions: readonly T[] | null | undefined, inputs: GroupInputs<T>): SessionGroup<T>[] {
  const groups: Record<SessionGroupId, SessionRow<T>[]> = { needs: [], working: [], today: [], earlier: [] }
  const midnight = startOfLocalDay(inputs.now)
  const seen = new Set<string>()
  const nested = nestedWorkers(sessions || [], s => !!inputs.waitingFor?.(s))
  const isNested = new Set<string>()
  for (const list of nested.values()) for (const worker of list) isNested.add(worker.id)
  for (const session of sessions || []) {
    if (!session?.id || seen.has(session.id)) continue
    seen.add(session.id)
    if (isNested.has(session.id)) continue
    const workerSessions = nested.get(session.id) || []
    const views = workerSessions.length ? teamProgress(session, workerSessions).workers : []
    const workers: WorkerRow<T>[] = workerSessions.map((worker, i) => ({ session: worker, view: views[i]! }))
    const workersWorking = workers.filter(w => w.view.status === 'working').length
    const ended = session.status === 'ended'
    const waitingFor = ended ? '' : (inputs.waitingFor?.(session) || '')
    const subagents = ended ? 0 : Math.max(0, inputs.subagentsWorking?.(session) || 0)
    const activityAt = sessionActivityAt(session)
    let group: SessionGroupId
    if (waitingFor) group = 'needs'
    else if (!ended && (session.status === 'processing' || subagents > 0 || workersWorking > 0 ||
      loopPhase(session.scheduled_loop, session.status, inputs.now) !== null ||
      hasLimitWait(session.limit_wait))) group = 'working'
    else group = activityAt >= midnight ? 'today' : 'earlier'
    const full = group === 'needs' || group === 'working' ||
      (!ended && activityAt > 0 && inputs.now - activityAt < FULL_ROW_AFTER_IDLE_MS)
    groups[group].push({ session, group, waitingFor, subagents: session.status === 'idle' ? subagents : 0, activityAt, full, workers, workersWorking })
  }
  return (Object.keys(groups) as SessionGroupId[]).map(id => ({
    id,
    label: SESSION_GROUP_LABELS[id],
    collapsible: COLLAPSIBLE_GROUPS.includes(id),
    rows: groups[id].sort((a, b) => b.activityAt - a.activityAt || a.session.id.localeCompare(b.session.id)),
  }))
}

export function isGroupCollapsed(group: Pick<SessionGroup, 'id' | 'collapsible'>, collapsed: Record<string, boolean>): boolean {
  return group.collapsible && (collapsed[group.id] ?? DEFAULT_COLLAPSED[group.id] ?? false)
}

/** A row's session followed by the workers under it. */
function rowSessions<T extends GroupableSession>(row: SessionRow<T>): T[] {
  return [row.session, ...row.workers.map(w => w.session)]
}

/** Every session of a group, workers included, top to bottom. */
export function groupSessionIds(group: Pick<SessionGroup, 'rows'>): string[] {
  return group.rows.flatMap(row => rowSessions(row).map(s => s.id))
}

/** The sessions as shown, top to bottom (a worker right after its parent), leaving out collapsed groups. */
export function visibleSessionOrder<T extends GroupableSession>(groups: SessionGroup<T>[], collapsed: Record<string, boolean>): T[] {
  return groups.flatMap(g => isGroupCollapsed(g, collapsed) ? [] : g.rows.flatMap(rowSessions))
}

/** "1 worker working", "2 workers working". */
export function workersWorkingLabel(count: number): string {
  return `${count} worker${count === 1 ? '' : 's'} working`
}

const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/** "now", "4m", "2h" (today), "Mon" (this week), "3 Oct" (older), '' without a time. */
export function relativeTime(at: number, now: number): string {
  if (!at) return ''
  const diff = now - at
  if (diff < 60000) return 'now'
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m`
  if (at >= startOfLocalDay(now)) return `${Math.floor(diff / 3600000)}h`
  const d = new Date(at)
  if (diff < 6 * 86400000) return WEEKDAYS[d.getDay()]!
  return `${d.getDate()} ${MONTHS[d.getMonth()]}`
}

interface PendingPermission {
  status?: string
  tool?: string
  toolName?: string
  details?: unknown
}
interface PendingQuestion {
  status?: string
  kind?: string
}

function shorten(text: string, max: number): string {
  const flat = text.replace(/\s+/g, ' ').trim()
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat
}

/** "Allow gh pr merge 1124?", "Allow Edit app.vue?", "Allow WebFetch?" */
export function permissionWaitLabel(permission: PendingPermission): string {
  const tool = permission.tool || permission.toolName || 'tool'
  const d = permission.details && typeof permission.details === 'object' ? permission.details as Record<string, unknown> : {}
  if (typeof d.command === 'string' && d.command.trim()) return `Allow ${shorten(d.command, 60)}?`
  const target = [d.file_path, d.path, d.url, d.pattern].find(v => typeof v === 'string' && v.trim()) as string | undefined
  if (target) {
    const name = /^https?:/.test(target) ? target.replace(/^https?:\/\//, '') : (target.split('/').filter(Boolean).pop() || target)
    return `Allow ${tool} ${shorten(name, 48)}?`
  }
  return `Allow ${tool}?`
}

/**
 * What a session waits for. Requests this tab has heard of win (they go away
 * as soon as they are answered); for kinds it has no record of, the counts the
 * server sent with the session list are used.
 */
export function waitingLabel(
  permissions: readonly PendingPermission[] | undefined,
  questions: readonly PendingQuestion[] | undefined,
  listed: { pending_permissions?: number; pending_questions?: number } = {},
): string {
  const question = questions?.find(q => q.status === 'pending')
  if (question) {
    if (question.kind === 'loop_session') return 'Loop proposal waiting'
    return question.kind === 'worker_session' ? 'Worker proposal waiting' : 'Question waiting'
  }
  const permission = permissions?.find(p => p.status === 'pending')
  if (permission) return permissionWaitLabel(permission)
  if (!questions && (listed.pending_questions || 0) > 0) return 'Question waiting'
  if (!permissions && (listed.pending_permissions || 0) > 0) return 'Approval needed'
  return ''
}

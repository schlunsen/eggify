/**
 * The "All agents" overlay (⇧⌘S): every session and every subagent in one
 * tree. Pure functions, so grouping, ordering, nesting and filters are tested
 * without the component (`__tests__/agents/allAgentsTree.test.ts`).
 *
 * - Sessions are grouped Needs you (a pending approval or question), Working
 *   (its turn runs, it is idle with running subagents, or a loop keeps it
 *   going), Recent (anything else active in the last 2 h).
 * - Under each session first its loops (cron jobs, /loop, Eggify loop;
 *   `sessionLoops.ts`), then the worker sessions it started (`team.ts`; a
 *   worker is not a session row of its own, and its subagents hang under it),
 *   then its subagents. A working worker keeps its parent in Working.
 * - Under each session its subagents: running first (oldest first), then
 *   finished (newest first). A subagent launched by another one sits under it.
 *   Finished subagents are folded into an "N finished" row when none of the
 *   session's subagents run, when there are more than a few of them, and
 *   always under the Running filter.
 */
import type { BackgroundAgent } from '~/composables/agents/useBackgroundAgents'
import { subagentDisplayStatus, type SubagentDisplayStatus } from './subagentActivity'
import { sessionLoops, type LoopSource, type SessionLoop } from './sessionLoops'
import { nestedWorkers } from './sessionGroups'
import { teamProgress, workerOf, WORKER_ROLE_LABELS, type TeamSession, type TeamWorkerView } from './team'

export type AllAgentsFilter = 'running' | 'all' | 'failed'
export type AllAgentsGroupKey = 'needs' | 'working' | 'recent'

export const ALL_AGENTS_FILTERS: AllAgentsFilter[] = ['running', 'all', 'failed']
export const ALL_AGENTS_GROUP_TITLES: Record<AllAgentsGroupKey, string> = {
  needs: 'Needs you',
  working: 'Working',
  recent: 'Recent'
}
/** How long an idle session stays in Recent. */
export const RECENT_WINDOW_MS = 2 * 60 * 60 * 1000
/** Finished subagents beside running ones stay unfolded up to this many. */
export const FINISHED_SHOWN_BESIDE_RUNNING = 3

export interface AllAgentsSession extends LoopSource {
  id: string
  status: string
  created_at?: string
  updated_at?: string
  pending_permissions?: number
  pending_questions?: number
  subagents_running?: number
  subagents_total?: number
  project_id?: string | null
  model_name?: string
  git_branch?: string
  message_count?: number
  error_message?: string | null
  view_mode?: 'live' | 'zen'
  tags?: string[]
  selected_avatar?: { id?: number; name?: string; color?: string; image_path?: string; image_url?: string } | null
  options?: { agent_name?: string; model?: string; working_directory?: string; workspace?: { branch?: string; worktree_path?: string } }
    & Pick<NonNullable<TeamSession['options']>, 'spawned_by_session_id' | 'worker' | 'read_only' | 'team'>
}

export interface SubagentNode {
  agent: BackgroundAgent
  status: SubagentDisplayStatus
  /** Running, or something below it still runs. */
  active: boolean
  children: SubagentNode[]
}

/** A worker session under the session that started it. */
export interface WorkerNode {
  session: AllAgentsSession
  /** Role, model, status and verdict (working also while its subagents run). */
  view: TeamWorkerView
  /** Its own subagents, nested like a session's: running first, then finished. */
  subagents: SubagentNode[]
  subagentsRunning: number
}

export interface SessionNode {
  session: AllAgentsSession
  group: AllAgentsGroupKey
  name: string
  tags: string[]
  /** What keeps the session going on its own (cron jobs, /loop, Eggify loop). */
  loops: SessionLoop[]
  /** The worker sessions it started, oldest first. */
  workers: WorkerNode[]
  /** Top-level subagents that run (or have running ones below), oldest first. */
  running: SubagentNode[]
  /** Top-level finished subagents, newest first. */
  finished: SubagentNode[]
  /** Whether `finished` is folded into an "N finished" row by default. */
  foldFinished: boolean
  counts: Record<SubagentDisplayStatus, number> & { total: number }
}

export interface AllAgentsGroup {
  key: AllAgentsGroupKey
  title: string
  sessions: SessionNode[]
}

export interface AllAgentsStats {
  sessionsWorking: number
  subagentsRunning: number
  finishedLastHour: number
  failedLastHour: number
  loopsActive: number
}

export interface BuildOptions {
  sessions: AllAgentsSession[]
  subagents: BackgroundAgent[]
  filter: AllAgentsFilter
  query?: string
  now?: number
  nameOf?: (session: AllAgentsSession) => string
  tagsOf?: (session: AllAgentsSession) => string[]
}

const time = (value?: string | null) => Date.parse(value || '') || 0
const finishedAt = (agent: BackgroundAgent) => time(agent.completed_at || agent.updated_at || agent.created_at)

export const defaultSessionName = (session: AllAgentsSession) =>
  session.selected_avatar?.name || session.options?.agent_name || `Session ${session.id.slice(0, 8)}`

/** Which group a session belongs in, or null when it is too old to show. */
export function sessionGroup(session: AllAgentsSession, runningSubagents: number, now = Date.now(), loops = sessionLoops(session, now)): AllAgentsGroupKey | null {
  if (session.pending_permissions || session.pending_questions) return 'needs'
  if (session.status === 'processing' || session.status === 'active') return 'working'
  if (runningSubagents > 0 || (session.subagents_running || 0) > 0) return 'working'
  if (loops.length) return 'working'
  const updated = time(session.updated_at)
  return updated && now - updated <= RECENT_WINDOW_MS ? 'recent' : null
}

function compareNodes(a: SubagentNode, b: SubagentNode) {
  if (a.active !== b.active) return a.active ? -1 : 1
  if (a.active) return time(a.agent.created_at) - time(b.agent.created_at)
  return finishedAt(b.agent) - finishedAt(a.agent)
}

/**
 * Nests a session's subagents: one launched by another subagent goes under
 * it (when that parent is in the list), the rest at the top.
 */
export function nestSubagents(agents: BackgroundAgent[]): SubagentNode[] {
  const nodes = new Map<string, SubagentNode>()
  for (const agent of agents) {
    const status = subagentDisplayStatus(agent)
    nodes.set(agent.agent_id, { agent, status, active: status === 'running', children: [] })
  }
  const roots: SubagentNode[] = []
  for (const node of nodes.values()) {
    const parentId = node.agent.parent_agent_id
    const parent = parentId && parentId !== node.agent.agent_id ? nodes.get(parentId) : undefined
    // Guard against cycles: walk up from the parent; if we meet this node, treat it as a root.
    let cyclic = false
    for (let p = parent, hops = 0; p && hops < 64; hops++) {
      if (p === node) { cyclic = true; break }
      const up = p.agent.parent_agent_id
      p = up ? nodes.get(up) : undefined
    }
    if (parent && !cyclic) parent.children.push(node)
    else roots.push(node)
  }
  const settle = (node: SubagentNode): boolean => {
    let below = false
    for (const child of node.children) below = settle(child) || below
    node.active = node.status === 'running' || below
    node.children.sort(compareNodes)
    return node.active
  }
  for (const root of roots) settle(root)
  return roots.sort(compareNodes)
}

function subagentPasses(agent: BackgroundAgent, filter: AllAgentsFilter) {
  const status = subagentDisplayStatus(agent)
  if (filter === 'failed') return status === 'failed' || status === 'interrupted'
  return true
}

function matches(text: string | undefined | null, query: string) {
  return !!text && text.toLowerCase().includes(query)
}

/** Keeps the agents that match the query, plus the subagents they sit under. */
function keepMatching(agents: BackgroundAgent[], query: string): BackgroundAgent[] {
  const byId = new Map(agents.map(a => [a.agent_id, a]))
  const keep = new Set<string>()
  for (const agent of agents) {
    if (!matches(agent.description, query) && !matches(agent.subagent_type, query)) continue
    let current: BackgroundAgent | undefined = agent
    for (let hops = 0; current && !keep.has(current.agent_id) && hops < 64; hops++) {
      keep.add(current.agent_id)
      current = current.parent_agent_id ? byId.get(current.parent_agent_id) : undefined
    }
  }
  return agents.filter(a => keep.has(a.agent_id))
}

export function buildAllAgentsTree(opts: BuildOptions): AllAgentsGroup[] {
  const now = opts.now ?? Date.now()
  const query = (opts.query || '').trim().toLowerCase()
  const nameOf = opts.nameOf ?? defaultSessionName
  const tagsOf = opts.tagsOf ?? ((s: AllAgentsSession) => s.tags || [])

  const bySession = new Map<string, BackgroundAgent[]>()
  for (const agent of opts.subagents) {
    const list = bySession.get(agent.parent_session_id)
    if (list) list.push(agent)
    else bySession.set(agent.parent_session_id, [agent])
  }

  // Workers hang under the session that started them (one that waits for
  // the user stays a session of its own, in Needs you).
  const nested = nestedWorkers(opts.sessions, s => !!(s.pending_permissions || s.pending_questions))
  const nestedIds = new Set<string>()
  for (const list of nested.values()) for (const worker of list) nestedIds.add(worker.id)
  const workerNode = (worker: AllAgentsSession, view: TeamWorkerView): WorkerNode => {
    const all = bySession.get(worker.id) || []
    const subagentsRunning = all.filter(agent => subagentDisplayStatus(agent) === 'running').length
    let subagents = nestSubagents(all.filter(agent => subagentPasses(agent, opts.filter)))
    if (opts.filter === 'running') subagents = subagents.filter(node => node.active)
    const working = view.status === 'working' || (view.status !== 'ended' && subagentsRunning > 0)
    return { session: worker, view: working ? { ...view, status: 'working' } : view, subagents, subagentsRunning }
  }
  const workerPasses = (worker: WorkerNode) => opts.filter === 'all' ? true
    : opts.filter === 'running' ? worker.view.status === 'working'
      : worker.subagents.length > 0 || worker.session.status === 'error'
  const workerMatches = (worker: WorkerNode) =>
    matches(`${WORKER_ROLE_LABELS[worker.view.role]} worker`, query) || matches(worker.view.model, query) ||
    matches(worker.view.modelLabel, query) || matches(worker.view.branch, query) || matches(workerOf(worker.session)?.task, query) ||
    (bySession.get(worker.session.id) || []).some(agent => matches(agent.description, query) || matches(agent.subagent_type, query))

  const groups: Record<AllAgentsGroupKey, SessionNode[]> = { needs: [], working: [], recent: [] }
  for (const session of opts.sessions) {
    if (nestedIds.has(session.id)) continue
    const all = bySession.get(session.id) || []
    const counts = { running: 0, done: 0, failed: 0, interrupted: 0, total: all.length }
    for (const agent of all) counts[subagentDisplayStatus(agent)]++
    let loops = sessionLoops(session, now)
    let group = sessionGroup(session, counts.running, now, loops)

    const workerSessions = nested.get(session.id) || []
    const views = workerSessions.length ? teamProgress(session, workerSessions).workers : []
    const allWorkers = workerSessions.map((worker, i) => workerNode(worker, views[i]!))
    // A working worker keeps its parent in Working.
    if (group !== 'needs' && allWorkers.some(w => w.view.status === 'working')) group = 'working'
    let workers = allWorkers.filter(workerPasses)

    let shown = all.filter(agent => subagentPasses(agent, opts.filter))
    if (!group) {
      // An old session still shows up when it has subagents in the list, or a worker that is recent itself.
      if (!shown.length && !allWorkers.some(w => sessionGroup(w.session, w.subagentsRunning, now) !== null)) continue
      group = 'recent'
    }

    if (opts.filter === 'running' && group === 'recent') continue
    // Loops don't fail: the Failed filter is about subagents.
    if (opts.filter === 'failed') loops = []
    if (opts.filter === 'failed' && !shown.length && !workers.length && session.status !== 'error') continue

    const name = nameOf(session)
    const tags = tagsOf(session)
    if (query) {
      const sessionMatches = matches(name, query) || tags.some(tag => matches(tag, query))
      if (!sessionMatches) {
        shown = keepMatching(shown, query)
        loops = loops.filter(loop => matches(loop.title, query) || matches(loop.prompt, query) || matches(loop.schedule, query))
        workers = workers.filter(workerMatches)
        if (!shown.length && !loops.length && !workers.length) continue
      }
    }

    const roots = nestSubagents(shown)
    const running = roots.filter(node => node.active)
    const finished = roots.filter(node => !node.active)
    // Running: finished ones are always folded. All: folded when nothing
    // runs, or when there are many. Failed: they are the point, never folded.
    const foldFinished = opts.filter !== 'failed' && finished.length > 0 &&
      (opts.filter === 'running' || running.length === 0 || finished.length > FINISHED_SHOWN_BESIDE_RUNNING)
    groups[group].push({ session, group, name, tags, loops, workers, running, finished, foldFinished, counts })
  }

  groups.needs.sort((a, b) => time(b.session.updated_at) - time(a.session.updated_at))
  // Working sessions keep their place (oldest first) so polling doesn't reshuffle them.
  groups.working.sort((a, b) => time(a.session.created_at) - time(b.session.created_at) || a.session.id.localeCompare(b.session.id))
  groups.recent.sort((a, b) => time(b.session.updated_at) - time(a.session.updated_at))

  return (Object.keys(groups) as AllAgentsGroupKey[])
    .filter(key => groups[key].length)
    .map(key => ({ key, title: ALL_AGENTS_GROUP_TITLES[key], sessions: groups[key] }))
}

/** The header's numbers, over everything loaded (filters ignored). */
export function allAgentsStats(sessions: AllAgentsSession[], subagents: BackgroundAgent[], now = Date.now()): AllAgentsStats {
  const runningBySession = new Map<string, number>()
  let subagentsRunning = 0
  let finishedLastHour = 0
  let failedLastHour = 0
  let loopsActive = 0
  const loopsBySession = new Map<string, SessionLoop[]>()
  for (const session of sessions) {
    const loops = sessionLoops(session, now)
    loopsBySession.set(session.id, loops)
    loopsActive += loops.length
  }
  for (const agent of subagents) {
    const status = subagentDisplayStatus(agent)
    if (status === 'running') {
      subagentsRunning++
      runningBySession.set(agent.parent_session_id, (runningBySession.get(agent.parent_session_id) || 0) + 1)
      continue
    }
    if (now - finishedAt(agent) > 60 * 60 * 1000) continue
    if (status === 'done') finishedLastHour++
    else failedLastHour++
  }
  const sessionsWorking = sessions.filter(s => sessionGroup(s, runningBySession.get(s.id) || 0, now, loopsBySession.get(s.id)) === 'working').length
  return { sessionsWorking, subagentsRunning, finishedLastHour, failedLastHour, loopsActive }
}

// ---------------------------------------------------------------------------
// Rows: the tree as the list shows it, for rendering and the keyboard
// ---------------------------------------------------------------------------

export type AllAgentsRow =
  | { kind: 'group'; key: string; group: AllAgentsGroupKey; title: string; count: number }
  | { kind: 'session'; key: string; node: SessionNode; expanded: boolean; hasChildren: boolean }
  | { kind: 'subagent'; key: string; node: SubagentNode; sessionId: string; depth: number; parentKey: string; last: boolean }
  | { kind: 'loop'; key: string; loop: SessionLoop; sessionId: string; parentKey: string; last: boolean }
  /** A worker session; `sessionId` is the session that started it. */
  | { kind: 'worker'; key: string; worker: WorkerNode; sessionId: string; parentKey: string; last: boolean }
  | { kind: 'more'; key: string; sessionId: string; count: number; expanded: boolean; parentKey: string }

export interface RowState {
  /** Sessions the user folded (←). */
  collapsed: Set<string>
  /** Sessions whose finished subagents the user unfolded. */
  expandedFinished: Set<string>
}

export const sessionRowKey = (id: string) => `s:${id}`
export const subagentRowKey = (id: string) => `a:${id}`
export const moreRowKey = (sessionId: string) => `m:${sessionId}`
export const loopRowKey = (sessionId: string, loopId: string) => `l:${sessionId}:${loopId}`
export const workerRowKey = (id: string) => `w:${id}`

export function flattenAllAgents(groups: AllAgentsGroup[], state: RowState): AllAgentsRow[] {
  const rows: AllAgentsRow[] = []
  const pushNodes = (nodes: SubagentNode[], sessionId: string, depth: number, parentKey: string) => {
    nodes.forEach((node, i) => {
      const key = subagentRowKey(node.agent.agent_id)
      rows.push({ kind: 'subagent', key, node, sessionId, depth, parentKey, last: i === nodes.length - 1 })
      pushNodes(node.children, sessionId, depth + 1, key)
    })
  }
  for (const group of groups) {
    rows.push({ kind: 'group', key: `g:${group.key}`, group: group.key, title: group.title, count: group.sessions.length })
    for (const node of group.sessions) {
      const id = node.session.id
      const key = sessionRowKey(id)
      const hasChildren = node.loops.length + node.workers.length + node.running.length + node.finished.length > 0
      const expanded = hasChildren && !state.collapsed.has(id)
      rows.push({ kind: 'session', key, node, expanded, hasChildren })
      if (!expanded) continue
      const subagentsBelow = node.running.length + node.finished.length > 0
      node.loops.forEach((loop, i) => {
        rows.push({ kind: 'loop', key: loopRowKey(id, loop.id), loop, sessionId: id, parentKey: key, last: !subagentsBelow && !node.workers.length && i === node.loops.length - 1 })
      })
      node.workers.forEach((worker, i) => {
        const workerKey = workerRowKey(worker.session.id)
        rows.push({ kind: 'worker', key: workerKey, worker, sessionId: id, parentKey: key, last: !subagentsBelow && i === node.workers.length - 1 })
        // Its own subagents belong to the worker's session.
        pushNodes(worker.subagents, worker.session.id, 2, workerKey)
      })
      pushNodes(node.running, id, 1, key)
      if (!node.finished.length) continue
      const finishedOpen = !node.foldFinished || state.expandedFinished.has(id)
      if (node.foldFinished) rows.push({ kind: 'more', key: moreRowKey(id), sessionId: id, count: node.finished.length, expanded: finishedOpen, parentKey: key })
      if (finishedOpen) pushNodes(node.finished, id, 1, key)
    }
  }
  return rows
}

/** Rows the keyboard cursor can stop on (not group headings). */
export const selectableRows = (rows: AllAgentsRow[]) => rows.filter(row => row.kind !== 'group')

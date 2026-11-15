import { computed, onMounted, onUnmounted, ref, toRaw, watch } from 'vue'
import type { ScheduledLoop } from '~/utils/agents/scheduledLoop'
import { useAuthenticatedFetch } from '../useAuthenticatedFetch'
import { useMetricsStore } from '../../stores/metrics/metricsStore'
import { overviewLog, type OverviewSubagent } from '../../utils/agents/overview'
import { hasMeasuredContext } from '../../utils/contextUsage'
import { overviewState, overviewCandidates, readOverviewReviews, reviewVersion, markOverviewReviewed, justWentIdle, type OverviewFilter, activelyWorking } from '../../utils/agents/overviewState'

export interface OverviewAgent {
  id: string
  status: string
  updated_at?: string
  num_turns?: number
  pending_permissions?: number
  pending_questions?: number
  error_message?: string
  project_id?: string
  model_name?: string
  git_branch?: string
  message_count?: number
  cost_usd?: number
  subagents_running?: number
  subagents_total?: number
  /** A pending Claude Code /loop wakeup (see utils/agents/scheduledLoop.ts) */
  scheduled_loop?: ScheduledLoop | null
  /** Waiting for the usage limit to reset (see utils/agents/limitWait.ts) */
  limit_wait?: import('~/utils/agents/limitWait').LimitWait | null
  view_mode?: 'live' | 'zen'
  selected_avatar?: { name?: string; color?: string }
  options?: { agent_name?: string; model?: string; working_directory?: string; workspace?: { working_directory: string; worktree_path: string; branch: string } }
}
/** An AI-written one-line summary and topic tags of a session (see session_digest.go). */
export interface OverviewDigest {
  summary: string
  tags: string[]
  updated_at?: string
  pending?: boolean
}
export type OverviewLayout = 'columns' | 'list'

const SUMMARIES_KEY = 'eggify:overview-summaries'
const LAYOUT_KEY = 'eggify:overview-layout'
function readPreference(key: string) {
  try { return localStorage.getItem(key) } catch { return null }
}
function writePreference(key: string, value: string) {
  try { localStorage.setItem(key, value) } catch { /* Private mode or full storage. */ }
}
/** What a session looked like, so its digest is only rewritten once it moved on. */
export const digestVersion = (agent: OverviewAgent) => `${agent.message_count || 0}:${agent.status}`

interface AgentDetail {
  logs: ReturnType<typeof overviewLog>
  git?: any
  context?: any
  logError?: string
  gitError?: string
  contextError?: string
  metricsAt?: number
  logVersion?: string
  logsAt?: number
  subagents?: OverviewSubagent[]
  subagentVersion?: string
}

/** `next` if it differs from `prev` element-wise, otherwise `prev` (keeps computed results stable). */
function sameOr<T>(prev: T[] | undefined, next: T[]): T[] {
  return prev && prev.length === next.length && prev.every((item, i) => item === next[i]) ? prev : next
}

/**
 * Merge a fresh session list into the current one: an agent whose data did not
 * change keeps its existing object, so a quiet poll doesn't invalidate
 * everything that renders it. Returns `current` itself when nothing changed.
 */
export function reconcileAgents(current: OverviewAgent[], incoming: OverviewAgent[], snapshots: Map<string, string>): OverviewAgent[] {
  const byId = new Map(current.map(agent => [agent.id, agent]))
  const seen = new Set<string>()
  const next = incoming.map(agent => {
    seen.add(agent.id)
    const snapshot = JSON.stringify(agent)
    const existing = byId.get(agent.id)
    if (existing && snapshots.get(agent.id) === snapshot) return existing
    snapshots.set(agent.id, snapshot)
    return agent
  })
  for (const id of snapshots.keys()) if (!seen.has(id)) snapshots.delete(id)
  return sameOr(current, next)
}

export function useAgentOverview() {
  const { fetchWithAuth } = useAuthenticatedFetch()
  const metrics = useMetricsStore()
  const allAgents = ref<OverviewAgent[]>([])
  const agentSnapshots = new Map<string, string>()
  const workingOrder = new Map<string, number>()
  const filter = ref<OverviewFilter>('working')
  const recentHours = ref(24)
  const recentLimit = ref(12)
  const reviews = ref(readOverviewReviews())
  const now = ref(Date.now())
  // Each agent's state, computed once per list change instead of several
  // times per card on every render.
  // `now` moves on every poll, so loop countdowns update even when no agent changed.
  const states = computed(() => new Map(allAgents.value.map(agent => [agent.id, overviewState(agent, now.value)])))
  const stateOf = (agent: OverviewAgent) => states.value.get(agent.id) ?? overviewState(agent)
  // The computeds below keep their previous array when the contents are the
  // same, so a poll that changed nothing doesn't re-render the overview.
  const candidates = computed<OverviewAgent[]>(prev => sameOr(prev, overviewCandidates(allAgents.value, now.value, recentHours.value)))
  const justIdleIds = computed<string[]>(prev => sameOr(prev, allAgents.value.filter(agent => justWentIdle(agent, now.value)).map(agent => agent.id)))
  const isJustIdle = (agent: OverviewAgent) => justIdleIds.value.includes(agent.id)
  const needsReview = (agent: OverviewAgent) => stateOf(agent).category === 'recent' && reviews.value[agent.id] !== reviewVersion(agent)
  const counts = computed(() => {
    const result = { all: candidates.value.length, attention: 0, working: 0, justIdle: 0, recent: 0, unreviewed: 0 }
    for (const agent of candidates.value) {
      const category = stateOf(agent).category
      result[category]++
      if (isJustIdle(agent)) result.justIdle++
      if (needsReview(agent)) result.unreviewed++
    }
    return result
  })
  // The agents of the selected tab, before any tag filter.
  const tabAgents = computed<OverviewAgent[]>(prev => sameOr(prev, tabAgentList()))
  function tabAgentList() {
    const rank = { attention: 0, working: 1, recent: 2 }
    let recentCount = 0
    // Working also keeps agents whose turn ended moments ago, after the ones
    // still working, so an agent doesn't vanish the moment it goes quiet.
    const shown = (a: OverviewAgent) => filter.value === 'all' || stateOf(a).category === filter.value ||
      (filter.value === 'working' && isJustIdle(a))
    return candidates.value.filter(shown)
      .slice().sort((a, b) => {
        const aState = stateOf(a).category, bState = stateOf(b).category
        if (aState !== bState) return rank[aState] - rank[bState]
        if (aState === 'working') return (workingOrder.get(a.id) || 0) - (workingOrder.get(b.id) || 0)
        return Number(needsReview(b)) - Number(needsReview(a)) || Date.parse(b.updated_at || '') - Date.parse(a.updated_at || '')
      })
      .filter(a => stateOf(a).category !== 'recent' || ++recentCount <= recentLimit.value)
  }
  // Summaries and tags: layout, on/off, and the tag being filtered on.
  const layout = ref<OverviewLayout>(readPreference(LAYOUT_KEY) === 'list' ? 'list' : 'columns')
  const summariesOn = ref(readPreference(SUMMARIES_KEY) !== 'off')
  const tagFilter = ref('')
  const digests = ref<Record<string, OverviewDigest>>({})
  watch(layout, value => writePreference(LAYOUT_KEY, value))
  watch(summariesOn, value => { writePreference(SUMMARIES_KEY, value ? 'on' : 'off'); if (!value) tagFilter.value = ''; else void loadDigests(true) })
  const agents = computed<OverviewAgent[]>(prev => {
    if (!tagFilter.value || !summariesOn.value) return tabAgents.value
    return sameOr(prev, tabAgents.value.filter(agent => digests.value[agent.id]?.tags.includes(tagFilter.value)))
  })
  /** The tags of the sessions in this tab, most used first. */
  const topTags = computed(() => {
    const counts = new Map<string, number>()
    for (const agent of tabAgents.value) for (const tag of digests.value[agent.id]?.tags || []) counts.set(tag, (counts.get(tag) || 0) + 1)
    return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([tag, count]) => ({ tag, count }))
  })
  const hiddenRecentCount = computed(() => Math.max(0, counts.value.recent - recentLimit.value))
  const markReviewed = (agent: OverviewAgent) => { reviews.value = markOverviewReviewed(agent) }
  const projects = ref<Record<string, { name: string; color?: string }>>({})
  const details = ref<Record<string, AgentDetail>>({})
  // Bumped whenever an agent's activity log is replaced; watched instead of
  // deep-watching `details`.
  const logsVersion = ref(0)
  const loading = ref(true)
  const error = ref('')
  const lastUpdated = ref<Date | null>(null)
  const scope = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  let refreshing = false
  const metricsInFlight = new Set<string>()
  const subagentsInFlight = new Set<string>()
  const logsInFlight = new Set<string>()
  // Per-agent detail loads run in the background with bounded concurrency, so
  // one slow agent never delays the next poll of the others.
  const queue = new Map<string, OverviewAgent>()
  let active = 0
  const MAX_PARALLEL = 4
  // Poll quickly while something is working, slower when all is quiet, and not
  // at all while the tab is hidden.
  const POLL_WORKING_MS = 1500
  const POLL_IDLE_MS = 4000

  async function post(url: string, body: unknown) {
    const controller = new AbortController()
    const abort = () => controller.abort()
    scope.signal.addEventListener('abort', abort, { once: true })
    const timeout = setTimeout(abort, 10000)
    try {
      const response = await fetchWithAuth(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal: controller.signal })
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      return await response.json()
    } finally {
      clearTimeout(timeout)
      scope.signal.removeEventListener('abort', abort)
    }
  }

  // Digests are fetched for the agents in the current tab. The server writes a
  // new one only when a session has moved on (and at most every few minutes),
  // so asking often is cheap; it is skipped while nothing changed.
  let digestKey = ''
  let digestsAt = 0
  let digestsInFlight = false
  async function loadDigests(force = false) {
    if (!summariesOn.value || digestsInFlight || scope.signal.aborted) return
    const shown = tabAgents.value.slice(0, 60)
    if (!shown.length) return
    const key = shown.map(agent => `${agent.id}@${digestVersion(agent)}`).join('|')
    const waiting = shown.some(agent => digests.value[agent.id]?.pending)
    if (!force && key === digestKey && !waiting && Date.now() - digestsAt < 30000) return
    digestsInFlight = true
    try {
      const result = await post('/api/agent/overview/digests', { sessions: shown.map(agent => ({ id: agent.id, version: digestVersion(agent) })), generate: true })
      if (scope.signal.aborted) return
      digests.value = { ...digests.value, ...(result.digests || {}) }
      digestKey = key
      digestsAt = Date.now()
    } catch { /* Summaries are optional; the overview works without them. */ } finally { digestsInFlight = false }
  }

  async function get(url: string) {
    const controller = new AbortController()
    const abort = () => controller.abort()
    scope.signal.addEventListener('abort', abort, { once: true })
    const timeout = setTimeout(abort, 10000)
    try {
      const response = await fetchWithAuth(url, { signal: controller.signal })
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      return await response.json()
    } finally {
      clearTimeout(timeout)
      scope.signal.removeEventListener('abort', abort)
    }
  }

  async function loadMetrics(agent: OverviewAgent) {
    const previous = details.value[agent.id]
    if (metricsInFlight.has(agent.id) || metricsInFlight.size >= 4 || (previous?.metricsAt && Date.now() - previous.metricsAt < 10000)) return
    metricsInFlight.add(agent.id)
    try {
      const base = `/api/agent/sessions/${agent.id}`
      const [git, context] = await Promise.allSettled([get(`${base}/git-status`), get(`${base}/context-usage`)])
      if (scope.signal.aborted || !agents.value.some(current => current.id === agent.id)) return
      details.value[agent.id] = {
        ...details.value[agent.id],
        logs: details.value[agent.id]?.logs || [],
        git: git.status === 'fulfilled' ? git.value : undefined,
        gitError: git.status === 'rejected' ? 'Git status unavailable' : undefined,
        context: context.status === 'fulfilled' && !context.value?.direct_provider && hasMeasuredContext(context.value, agent.message_count) ? context.value : undefined,
        contextError: context.status === 'rejected' ? 'Context unavailable' : undefined,
        metricsAt: Date.now()
      }
    } finally { metricsInFlight.delete(agent.id) }
  }

  async function loadSubagents(agent: OverviewAgent) {
    if (!agent.subagents_total) {
      if (details.value[agent.id]?.subagents) details.value[agent.id] = { ...details.value[agent.id], subagents: undefined, subagentVersion: undefined }
      return
    }
    // Poll while any are running; otherwise fetch only when the counts change.
    const version = `${agent.subagents_total}:${agent.subagents_running || 0}`
    if ((!agent.subagents_running && details.value[agent.id]?.subagentVersion === version) || subagentsInFlight.has(agent.id)) return
    subagentsInFlight.add(agent.id)
    try {
      const result = await get(`/api/agent/sessions/${agent.id}/subagents`)
      if (!scope.signal.aborted) details.value[agent.id] = { ...details.value[agent.id], logs: details.value[agent.id]?.logs || [], subagents: result.subagents || [], subagentVersion: version }
    } catch { /* Keep the last list; the counts in the header stay current. */ } finally { subagentsInFlight.delete(agent.id) }
  }

  async function loadAgent(agent: OverviewAgent) {
    // Context lookups can be slow; never hold up the activity feed for them.
    void loadMetrics(agent)
    void loadSubagents(agent)
    // The session list reports live message counts and update times, so the
    // feed only needs refetching when the agent changed. A working agent is
    // also rechecked every few seconds, in case a message was counted before
    // it was saved.
    const version = `${reviewVersion(agent)}:${agent.updated_at || ''}`
    const detail = details.value[agent.id]
    const recheck = activelyWorking(agent) && Date.now() - (detail?.logsAt || 0) > 5000
    if ((detail?.logVersion === version && !recheck) || logsInFlight.has(agent.id)) return
    logsInFlight.add(agent.id)
    try {
      const messages = await get(`/api/agent/sessions/${agent.id}/messages?limit=30`)
      if (!scope.signal.aborted) { details.value[agent.id] = { ...details.value[agent.id], logs: overviewLog(messages.messages || []), logError: undefined, logVersion: version, logsAt: Date.now() }; logsVersion.value++ }
    } catch {
      if (!scope.signal.aborted) { details.value[agent.id] = { ...details.value[agent.id], logs: details.value[agent.id]?.logs || [], logError: 'Activity temporarily unavailable' }; logsVersion.value++ }
    } finally { logsInFlight.delete(agent.id) }
  }

  function pump() {
    while (active < MAX_PARALLEL && queue.size && !scope.signal.aborted) {
      const [id, agent] = queue.entries().next().value as [string, OverviewAgent]
      queue.delete(id)
      active++
      void loadAgent(agent).finally(() => { active--; pump() })
    }
  }

  function schedule() {
    if (timer) clearTimeout(timer)
    if (scope.signal.aborted) return
    const working = allAgents.value.some(agent => activelyWorking(agent))
    timer = setTimeout(refresh, working ? POLL_WORKING_MS : POLL_IDLE_MS)
  }

  function onVisibility() {
    if (document.visibilityState === 'visible') { if (timer) clearTimeout(timer); void refresh() }
  }

  async function refresh() {
    if (refreshing || scope.signal.aborted) return
    if (typeof document !== 'undefined' && document.visibilityState === 'hidden') { schedule(); return }
    refreshing = true
    try {
      const result = await get('/api/agent/sessions?status=all')
      if (scope.signal.aborted) return
      // Never use the editor's project-filtered session list or change selection.
      for (const agent of result.sessions || []) {
        if (!workingOrder.has(agent.id)) workingOrder.set(agent.id, workingOrder.size)
      }
      // Keep unchanged agents' objects (and the array itself if nothing changed)
      const nextAgents = reconcileAgents(toRaw(allAgents.value), result.sessions || [], agentSnapshots)
      if (nextAgents !== toRaw(allAgents.value)) allAgents.value = nextAgents
      now.value = Date.now()
      // Queue each visible agent with its newest list entry; agents already
      // queued keep their place.
      for (const agent of agents.value) queue.set(agent.id, agent)
      pump()
      void loadDigests()
      const ids = new Set(allAgents.value.map(agent => agent.id))
      for (const id of Object.keys(details.value)) if (!ids.has(id)) delete details.value[id]
      lastUpdated.value = new Date()
      error.value = ''
    } catch {
      if (!scope.signal.aborted) error.value = 'Could not refresh agents. Retrying automatically…'
    } finally {
      loading.value = false
      refreshing = false
      schedule()
    }
  }

  // Agents a new filter reveals load right away rather than on the next poll.
  watch([filter, recentHours, recentLimit], () => {
    for (const agent of agents.value) if (!details.value[agent.id]) queue.set(agent.id, agent)
    pump()
    void loadDigests()
  })

  function contextFor(agent: OverviewAgent) {
    const live = metrics.getSessionContextUsage(agent.id)
    const detail = details.value[agent.id]
    if (hasMeasuredContext(live, agent.message_count) && (!detail?.context || live!.lastUpdateTime > (detail.metricsAt || 0))) return live
    return detail?.context
  }
  const projectCount = computed(() => new Set(agents.value.map(agent => agent.project_id || 'unassigned')).size)

  onMounted(() => {
    void get('/api/projects').then(result => {
      if (!scope.signal.aborted) projects.value = Object.fromEntries((result.projects || []).map((project: any) => [project.id, project]))
    }).catch(() => {})
    document.addEventListener('visibilitychange', onVisibility)
    void refresh()
  })
  onUnmounted(() => { scope.abort(); queue.clear(); if (timer) clearTimeout(timer); document.removeEventListener('visibilitychange', onVisibility) })
  return { agents, projects, details, logsVersion, loading, error, lastUpdated, projectCount, contextFor, filter, recentHours, recentLimit, counts, needsReview, markReviewed, hiddenRecentCount, isJustIdle, stateOf, layout, summariesOn, tagFilter, digests, topTags }
}

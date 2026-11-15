import type { OverviewAgent } from '../../composables/agents/useAgentOverview'
import { loopLabel, loopPhase } from './scheduledLoop'
import { limitWaitLabel } from './limitWait'
export type OverviewCategory = 'attention' | 'working' | 'recent'
export type OverviewFilter = 'all' | OverviewCategory
const REVIEW_KEY = 'eggify:overview-reviewed:v1'

export function overviewState(agent: OverviewAgent, now = Date.now()): { category: OverviewCategory; label: string } {
  if (agent.pending_questions) return { category: 'attention', label: 'Question waiting' }
  if (agent.pending_permissions) return { category: 'attention', label: 'Approval needed' }
  if (agent.status === 'error') return { category: 'attention', label: 'Failed' }
  // A Claude Code /loop keeps the session going between its turns.
  const loop = loopLabel(agent.scheduled_loop, agent.status, now)
  if (loop) {
    const subagents = agent.subagents_running ? ` · ${agent.subagents_running} subagent${agent.subagents_running === 1 ? '' : 's'}` : ''
    return { category: 'working', label: loop + subagents }
  }
  if (agent.status === 'processing' || agent.status === 'active') return { category: 'working', label: 'Working' }
  // Waiting for the usage limit to reset: it continues by itself.
  const limit = limitWaitLabel(agent.limit_wait, now)
  if (limit && agent.status !== 'ended') return { category: 'working', label: limit }
  // Background subagents keep running after the parent's turn has ended.
  if (agent.subagents_running) return { category: 'working', label: agent.subagents_running === 1 ? '1 subagent running' : `${agent.subagents_running} subagents running` }
  return { category: 'recent', label: agent.status === 'ended' ? 'Stopped' : agent.num_turns ? 'Turn finished' : 'Idle' }
}
/**
 * Whether the agent is doing something right now (its turn, subagents or a
 * loop iteration). A loop that is only waiting for its next run counts as
 * working in the overview, but needs no fast polling.
 */
export function activelyWorking(agent: OverviewAgent, now = Date.now()): boolean {
  return agent.status === 'processing' || agent.status === 'active' || !!agent.subagents_running ||
    loopPhase(agent.scheduled_loop, agent.status, now) === 'running'
}
/** How long an agent that finished a turn stays in the Working view. */
export const JUST_IDLE_MINUTES = 15
/**
 * A turn that ended moments ago: the agent may be waiting on a background
 * subagent or about to pick up again, so it stays in the Working view for a
 * while instead of vanishing the moment it goes quiet.
 */
export function justWentIdle(agent: OverviewAgent, now: number) {
  if (overviewState(agent).category !== 'recent' || agent.status !== 'idle' || !(agent.message_count || 0)) return false
  const idleFor = now - Date.parse(agent.updated_at || '')
  return idleFor >= 0 && idleFor < JUST_IDLE_MINUTES * 60000
}
export function reviewVersion(agent: OverviewAgent) {
  return `${agent.status}:${agent.num_turns || 0}:${agent.message_count || 0}:${agent.subagents_total || 0}:${agent.subagents_running || 0}`
}
export function readOverviewReviews(): Record<string, string> {
  try {
    const value = JSON.parse(localStorage.getItem(REVIEW_KEY) || '{}')
    return value && typeof value === 'object' && !Array.isArray(value) ? value : {}
  } catch { return {} }
}
export function markOverviewReviewed(agent: OverviewAgent) {
  const reviews = readOverviewReviews()
  delete reviews[agent.id]
  reviews[agent.id] = reviewVersion(agent)
  const bounded = Object.fromEntries(Object.entries(reviews).slice(-200))
  try { localStorage.setItem(REVIEW_KEY, JSON.stringify(bounded)) } catch { /* Private mode or full storage. */ }
  return bounded
}
export function overviewCandidates(agents: OverviewAgent[], now: number, hours: number) {
  return agents.filter(agent => {
    const state = overviewState(agent)
    if (state.category === 'working' || agent.pending_questions || agent.pending_permissions) return true
    return ((agent.message_count || 0) > 0 || agent.status === 'error') && Date.parse(agent.updated_at || '') >= now - hours * 3600000
  })
}

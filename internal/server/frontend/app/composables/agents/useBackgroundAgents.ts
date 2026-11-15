/**
 * Background Agents Composable
 *
 * Tracks and displays background agents spawned via the Task tool.
 * Provides real-time updates on agent status, progress, and output.
 */

import { ref, computed, shallowRef, triggerRef, markRaw } from 'vue'

// Types for background agents
export interface BackgroundAgent {
  agent_id: string
  parent_session_id: string
  subagent_type: string
  description: string
  status: 'running' | 'completed' | 'failed' | 'waiting'
  progress: number // 0.0 to 1.0
  last_output: string
  output_lines: number
  created_at: string
  updated_at: string
  completed_at?: string
  error_message?: string
  parent_agent_id?: string // set when another subagent launched this one
  background?: boolean // launched with run_in_background
  recent_output?: string[] // last output lines kept by the server
  /** When each recent_output line was produced (unix ms; 0 = unknown). Absent on older rows. */
  recent_output_at?: number[]
  interrupted?: boolean // failed because it was cut off (app restart, stopped), not on its own
  prompt?: string // what it was asked to do (the Agent call's prompt, capped)
  /** The model it runs on (e.g. claude-haiku-4-5-20251001, or "sonnet" as requested). */
  model?: string
  /** "actual" (from its own messages) or "requested" (the launch's model input). */
  model_source?: 'actual' | 'requested'
}

export interface BackgroundAgentEvent {
  type: string
  session_id: string
  agent_id?: string
  agent?: BackgroundAgent
  status?: string
  progress?: number
  output?: string
  /** Every line of a batched background_agent_output event (output is the last one) */
  lines?: string[]
  /** When the (last) line was produced, unix ms (background_agent_output). */
  at?: number
  /** When each of `lines` was produced, unix ms. */
  lines_at?: number[]
  final_output?: string
  error_message?: string
  is_error?: boolean
  completed_at?: string
  interrupted?: boolean
}

// Singleton state shared across components
const backgroundAgents = ref<Map<string, BackgroundAgent>>(new Map())
// Output lines are append-only text: keep them out of deep reactivity (a
// shallowRef'd Map of plain arrays) and trigger readers manually. Appending
// replaces the agent's array so computeds that return it see a new value.
const outputHistory = shallowRef<Map<string, string[]>>(new Map())
// When each kept line was produced (unix ms, 0 = unknown), parallel to
// outputHistory: same length, replaced together.
const outputTimes = new Map<string, number[]>()
// One shared empty list, so "no times" keeps its identity between reads.
const NO_TIMES: number[] = markRaw([])

/** Times for `count` lines: the newest of `times`, padded at the front with 0 (unknown). */
export function alignTimes(times: readonly number[] | undefined, count: number): number[] {
  const src = times ?? []
  const kept = src.length > count ? src.slice(src.length - count) : src
  const out = kept.length < count ? new Array<number>(count - kept.length).fill(0).concat(kept) : [...kept]
  return out.map(t => (typeof t === 'number' && Number.isFinite(t) && t > 0 ? t : 0))
}

function setOutput(agentId: string, lines: string[], times?: readonly number[]) {
  outputTimes.set(agentId, markRaw(alignTimes(times, lines.length)))
  outputHistory.value.set(agentId, markRaw(lines))
  triggerRef(outputHistory)
}

/** Lines kept per agent, the same as the server keeps (maxRecentOutputLines). */
export const MAX_OUTPUT_LINES = 300

function appendOutput(agentId: string, lines: string[], times?: readonly number[]) {
  const history = outputHistory.value.get(agentId) || []
  const historyTimes = outputTimes.get(agentId) || []
  // Copy only the lines that stay: an uncapped history grew for the life of
  // the tab and was copied in full on every event.
  const keepOld = Math.max(0, MAX_OUTPUT_LINES - lines.length)
  const kept = history.length > keepOld ? history.slice(history.length - keepOld) : history
  const keptTimes = alignTimes(historyTimes, history.length).slice(history.length - kept.length)
  const added = lines.length > MAX_OUTPUT_LINES ? lines.slice(-MAX_OUTPUT_LINES) : lines
  // Lines without a server time get the time they arrived.
  const now = Date.now()
  const addedTimes = alignTimes(times, lines.length).slice(lines.length - added.length).map(t => t || now)
  setOutput(agentId, kept.concat(added), keptTimes.concat(addedTimes))
}

function deleteOutput(agentId: string) {
  outputTimes.delete(agentId)
  if (outputHistory.value.delete(agentId)) triggerRef(outputHistory)
}

// Server-sent recent output is replaced wholesale, never edited in place, so
// deep watchers on the agents don't need to walk it.
function withRawOutput(agent: BackgroundAgent): BackgroundAgent {
  if (Array.isArray(agent.recent_output)) agent.recent_output = markRaw(agent.recent_output)
  if (Array.isArray(agent.recent_output_at)) agent.recent_output_at = markRaw(agent.recent_output_at)
  return agent
}
const dismissedAgents = ref<Set<string>>(new Set())
// Sessions whose whole subagent list was loaded (background_agents_list). For
// the others the store only holds agents seen in events since the page loaded.
const fullyListed = ref<Set<string>>(new Set())
// Sessions whose list was asked for, so an event asks only once.
const listRequested = new Set<string>()
// Track agents that recently finished (completed/failed) for visual notification
const recentlyFinished = ref<Map<string, number>>(new Map()) // agentId → timestamp

export function useBackgroundAgents() {
  /**
   * Get all background agents for a session
   */
  const getAgentsForSession = (sessionId: string): BackgroundAgent[] => {
    const agents: BackgroundAgent[] = []
    backgroundAgents.value.forEach((agent) => {
      if (agent.parent_session_id === sessionId) {
        agents.push(agent)
      }
    })
    return agents.sort(
      (a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
    )
  }

  /**
   * Get running agents for a session
   */
  const getRunningAgentsForSession = (sessionId: string): BackgroundAgent[] => {
    return getAgentsForSession(sessionId).filter((a) => a.status === 'running')
  }

  /**
   * Get a specific agent by ID
   */
  const getAgent = (agentId: string): BackgroundAgent | undefined => {
    return backgroundAgents.value.get(agentId)
  }

  /**
   * Get output history for an agent
   */
  const getAgentOutput = (agentId: string): string[] => {
    return outputHistory.value.get(agentId) || []
  }

  /**
   * When each line of getAgentOutput(agentId) was produced (unix ms, 0 =
   * unknown), the same length as it. Replaced together with the lines, so
   * reading getAgentOutput tracks both.
   */
  const getAgentOutputTimes = (agentId: string): number[] => {
    return outputTimes.get(agentId) || NO_TIMES
  }

  /**
   * The lines to show for an agent and their times: the live history, else
   * what the server kept, else its last line (time unknown).
   */
  const agentOutputWithTimes = (agent: BackgroundAgent): { lines: string[]; times: number[] } => {
    const lines = getAgentOutput(agent.agent_id)
    if (lines.length) return { lines, times: getAgentOutputTimes(agent.agent_id) }
    if (agent.recent_output?.length) {
      const at = agent.recent_output_at
      return { lines: agent.recent_output, times: at && at.length === agent.recent_output.length ? at : NO_TIMES }
    }
    return { lines: agent.last_output ? [agent.last_output] : [], times: NO_TIMES }
  }

  /**
   * Check if session has any running background agents
   */
  const hasRunningAgents = (sessionId: string): boolean => {
    return getRunningAgentsForSession(sessionId).length > 0
  }

  /**
   * Get count of running agents
   */
  const runningAgentCount = (sessionId: string): number => {
    return getRunningAgentsForSession(sessionId).length
  }

  /**
   * Handle background agent started event
   */
  const handleAgentStarted = (event: BackgroundAgentEvent) => {
    if (!event.agent) return

    console.log('🤖 Background agent started:', event.agent.agent_id)
    const known = backgroundAgents.value.has(event.agent.agent_id)
    backgroundAgents.value.set(event.agent.agent_id, withRawOutput(event.agent))
    // A known agent re-announced (e.g. its prompt arrived late) keeps its output.
    if (event.agent.recent_output?.length) setOutput(event.agent.agent_id, [...event.agent.recent_output], event.agent.recent_output_at)
    else if (!known) setOutput(event.agent.agent_id, [])
  }

  /**
   * Handle background agent progress event
   */
  const handleAgentProgress = (event: BackgroundAgentEvent) => {
    if (!event.agent_id) return

    const agent = backgroundAgents.value.get(event.agent_id)
    if (agent) {
      agent.progress = event.progress ?? agent.progress
      agent.status = (event.status as BackgroundAgent['status']) ?? agent.status
      agent.updated_at = new Date().toISOString()
      if (agent.status === 'running') {
        // A finished agent the main agent messaged runs again (the server
        // reopens it with this event): drop what said it had ended.
        agent.completed_at = undefined
        agent.error_message = undefined
        agent.interrupted = false
        recentlyFinished.value.delete(event.agent_id)
      }

      if (event.output) {
        agent.last_output = event.output
        agent.output_lines++

        // Add to output history
        appendOutput(event.agent_id, [event.output])
      }

      console.log(
        `📊 Agent progress: ${event.agent_id} - ${Math.round((event.progress ?? 0) * 100)}%`
      )
    }
  }

  /**
   * Handle background agent output event
   */
  const handleAgentOutput = (event: BackgroundAgentEvent) => {
    if (!event.agent_id || !event.output) return
    // The server batches the lines of one subagent message into one event.
    const lines = event.lines?.length ? event.lines : [event.output]
    const times = event.lines?.length ? event.lines_at : event.at ? [event.at] : undefined

    const agent = backgroundAgents.value.get(event.agent_id)
    if (agent) {
      agent.last_output = event.output
      agent.output_lines += lines.length
      agent.updated_at = new Date().toISOString()

      // Add to output history
      appendOutput(event.agent_id, lines, times)
    }
  }

  /**
   * Handle background agent completed event
   */
  const handleAgentCompleted = (event: BackgroundAgentEvent) => {
    if (!event.agent_id) return

    const agent = backgroundAgents.value.get(event.agent_id)
    if (agent) {
      agent.status = 'completed'
      agent.progress = 1.0
      agent.completed_at = event.completed_at || new Date().toISOString()
      agent.updated_at = agent.completed_at

      if (event.final_output) {
        agent.last_output = event.final_output
      }

      // Track as recently finished for visual notification
      recentlyFinished.value.set(event.agent_id, Date.now())

      console.log(`✅ Agent completed: ${event.agent_id}`)
    }
  }

  /**
   * Handle background agent failed event
   */
  const handleAgentFailed = (event: BackgroundAgentEvent) => {
    if (!event.agent_id) return

    const agent = backgroundAgents.value.get(event.agent_id)
    if (agent) {
      agent.status = 'failed'
      agent.error_message = event.error_message
      agent.interrupted = !!event.interrupted
      agent.updated_at = new Date().toISOString()

      // Track as recently finished for visual notification
      recentlyFinished.value.set(event.agent_id, Date.now())

      console.error(`❌ Agent failed: ${event.agent_id}`, event.error_message)
    }
  }

  /**
   * Process a WebSocket message for background agent events
   */
  const processWebSocketMessage = (message: BackgroundAgentEvent) => {
    switch (message.type) {
      case 'background_agent_started':
        handleAgentStarted(message)
        break
      case 'background_agent_progress':
        handleAgentProgress(message)
        break
      case 'background_agent_output':
        handleAgentOutput(message)
        break
      case 'background_agent_completed':
        handleAgentCompleted(message)
        break
      case 'background_agent_failed':
        handleAgentFailed(message)
        break
      case 'background_agents_list':
        // Handle list response - treat as authoritative source for this session
        // First clear any stale agents for this session, then add the fresh list
        // This ensures project switching + app restarts don't leave ghost agents
        if (Array.isArray((message as any).agents)) {
          const sessionId = (message as any).session_id
          if (sessionId) {
            fullyListed.value.add(sessionId)
            // Remove all existing agents for this session before replacing
            // Collect IDs first to avoid mutating the map during iteration
            const toRemove: string[] = []
            backgroundAgents.value.forEach((agent, id) => {
              if (agent.parent_session_id === sessionId) {
                toRemove.push(id)
              }
            })
            for (const id of toRemove) {
              backgroundAgents.value.delete(id)
              deleteOutput(id)
            }
          }
          ;(message as any).agents.forEach((agent: BackgroundAgent) => {
            backgroundAgents.value.set(agent.agent_id, withRawOutput(agent))
            // The server keeps recent output, so the modal can show what an
            // agent did after a reload or when opened from another tab.
            if (agent.recent_output?.length) {
              setOutput(agent.agent_id, agent.recent_output.slice(-MAX_OUTPUT_LINES), agent.recent_output_at)
            }
          })
        }
        break
    }
  }

  /**
   * Clear completed/failed agents older than specified minutes
   */
  const clearOldAgents = (maxAgeMinutes: number = 30) => {
    const cutoff = Date.now() - maxAgeMinutes * 60 * 1000

    backgroundAgents.value.forEach((agent, id) => {
      if (agent.status !== 'running') {
        const updatedAt = new Date(agent.updated_at).getTime()
        if (updatedAt < cutoff) {
          backgroundAgents.value.delete(id)
          deleteOutput(id)
        }
      }
    })
  }

  /**
   * Clear all agents for a session
   */
  const clearSessionAgents = (sessionId: string) => {
    backgroundAgents.value.forEach((agent, id) => {
      if (agent.parent_session_id === sessionId) {
        backgroundAgents.value.delete(id)
        deleteOutput(id)
      }
    })
  }

  /**
   * Get displayable agents for toast notifications
   * Returns running agents + recently completed/failed (last 5 seconds)
   */
  const getDisplayableAgents = (sessionId: string): BackgroundAgent[] => {
    return getAgentsForSession(sessionId).filter(agent => {
      // Exclude manually dismissed
      if (dismissedAgents.value.has(agent.agent_id)) return false

      // Include running
      if (agent.status === 'running') return true

      // Include recently completed/failed (last 5 seconds)
      const updatedAt = new Date(agent.updated_at).getTime()
      const fiveSecondsAgo = Date.now() - 5000
      return updatedAt > fiveSecondsAgo
    })
  }

  /**
   * Format elapsed time for an agent
   */
  const formatElapsedTime = (agent: BackgroundAgent): string => {
    const start = new Date(agent.created_at).getTime()
    const now = Date.now()
    const seconds = Math.floor((now - start) / 1000)

    if (seconds < 60) return `${seconds}s`
    const minutes = Math.floor(seconds / 60)
    const remainingSeconds = seconds % 60
    return `${minutes}m ${remainingSeconds}s`
  }

  /**
   * Dismiss an agent from toast display
   */
  const dismissAgent = (agentId: string) => {
    dismissedAgents.value.add(agentId)
  }

  /**
   * Clear dismissed agents tracking
   */
  const clearDismissed = () => {
    dismissedAgents.value.clear()
  }

  /**
   * Check if an agent recently finished (within the last 10 seconds)
   * Used for triggering visual flash/notification animations
   */
  const isRecentlyFinished = (agentId: string): boolean => {
    const finishedAt = recentlyFinished.value.get(agentId)
    if (!finishedAt) return false
    const elapsed = Date.now() - finishedAt
    if (elapsed > 10000) {
      // Clean up old entries
      recentlyFinished.value.delete(agentId)
      return false
    }
    return true
  }

  /**
   * Get count of finished (completed + failed) agents for a session
   */
  const finishedAgentCount = (sessionId: string): number => {
    return getAgentsForSession(sessionId).filter(
      a => a.status === 'completed' || a.status === 'failed'
    ).length
  }

  /** Whether the store holds every subagent of the session, not just recent events. */
  const hasFullList = (sessionId: string): boolean => fullyListed.value.has(sessionId)

  /**
   * Whether to ask the server for a session's subagent list after an event
   * for it: while its list is not loaded and not yet asked for. Without it,
   * an agent finishing in another session would leave its count stale.
   */
  const needsList = (sessionId: string | undefined): sessionId is string =>
    !!sessionId && !fullyListed.value.has(sessionId) && !listRequested.has(sessionId)

  /** Records that the list of sessionId was sent for (call only after a successful send). */
  const markListRequested = (sessionId: string) => { listRequested.add(sessionId) }

  /**
   * Forgets which lists are loaded, on every (re)connect: events missed while
   * disconnected, or agents the server marked interrupted on a restart (no
   * event), would otherwise keep a stale live count winning over the server's.
   */
  const resetListTracking = () => {
    fullyListed.value.clear()
    listRequested.clear()
  }

  return {
    // State
    backgroundAgents: computed(() => backgroundAgents.value),

    // Getters
    getAgentsForSession,
    getRunningAgentsForSession,
    getAgent,
    getAgentOutput,
    getAgentOutputTimes,
    agentOutputWithTimes,
    hasRunningAgents,
    runningAgentCount,
    finishedAgentCount,
    getDisplayableAgents,
    formatElapsedTime,
    isRecentlyFinished,
    hasFullList,
    needsList,
    markListRequested,
    resetListTracking,

    // Event handlers
    processWebSocketMessage,
    handleAgentStarted,
    handleAgentProgress,
    handleAgentOutput,
    handleAgentCompleted,
    handleAgentFailed,

    // Actions
    clearOldAgents,
    clearSessionAgents,
    dismissAgent,
    clearDismissed
  }
}

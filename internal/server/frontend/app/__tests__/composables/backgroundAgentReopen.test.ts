import { describe, expect, it } from 'vitest'
import { useBackgroundAgents, type BackgroundAgent } from '~/composables/agents/useBackgroundAgents'

describe('useBackgroundAgents reopen', () => {
  it('a running progress event brings a finished agent back to running', () => {
    const { processWebSocketMessage, getAgent } = useBackgroundAgents()
    const agent: BackgroundAgent = {
      agent_id: 'toolu_reopen',
      parent_session_id: 's1',
      subagent_type: 'general-purpose',
      description: 'Open the PR',
      status: 'failed',
      progress: 1,
      last_output: 'done',
      output_lines: 1,
      created_at: '2026-10-05T10:00:00Z',
      updated_at: '2026-10-05T10:05:00Z',
      completed_at: '2026-10-05T10:05:00Z',
      error_message: 'App restarted while agent was running',
      interrupted: true,
      recent_output: ['done'],
    }
    processWebSocketMessage({ type: 'background_agents_list', session_id: 's1', agents: [agent] } as any)

    processWebSocketMessage({ type: 'background_agent_progress', session_id: 's1', agent_id: 'toolu_reopen', status: 'running', progress: 0 })

    const reopened = getAgent('toolu_reopen')!
    expect(reopened.status).toBe('running')
    expect(reopened.completed_at).toBeUndefined()
    expect(reopened.error_message).toBeUndefined()
    expect(reopened.interrupted).toBe(false)
  })
})

describe('useBackgroundAgents list tracking', () => {
  it('asks once for the list of a session it only knows from events', () => {
    const { processWebSocketMessage, hasFullList, needsList, markListRequested } = useBackgroundAgents()
    expect(hasFullList('s-other')).toBe(false)
    expect(needsList('s-other')).toBe(true)
    markListRequested('s-other')
    expect(needsList('s-other')).toBe(false)

    processWebSocketMessage({ type: 'background_agents_list', session_id: 's-listed', agents: [] } as any)
    expect(hasFullList('s-listed')).toBe(true)
    expect(needsList('s-listed')).toBe(false)
    expect(needsList(undefined)).toBe(false)
  })

  it('forgets loaded lists on reconnect, so the server count applies until they reload', () => {
    const { processWebSocketMessage, hasFullList, needsList, markListRequested, resetListTracking } = useBackgroundAgents()
    processWebSocketMessage({ type: 'background_agents_list', session_id: 's-reset', agents: [] } as any)
    markListRequested('s-asked')
    resetListTracking()
    expect(hasFullList('s-reset')).toBe(false)
    expect(needsList('s-asked')).toBe(true)
  })
})

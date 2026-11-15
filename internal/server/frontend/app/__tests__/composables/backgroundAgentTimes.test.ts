import { describe, expect, it, vi } from 'vitest'
import { alignTimes, MAX_OUTPUT_LINES, useBackgroundAgents, type BackgroundAgent } from '~/composables/agents/useBackgroundAgents'

const agent = (id: string, extra: Partial<BackgroundAgent> = {}): BackgroundAgent => ({
  agent_id: id, parent_session_id: 'times', subagent_type: 'Explore', description: 'x', status: 'running', progress: 0,
  last_output: '', output_lines: 0, created_at: '2026-10-11T10:00:00Z', updated_at: '2026-10-11T10:00:00Z', ...extra
})

describe('output times', () => {
  it('aligns times to the newest lines, unknown ones as 0', () => {
    expect(alignTimes([1, 2, 3], 2)).toEqual([2, 3])
    expect(alignTimes([5], 3)).toEqual([0, 0, 5])
    expect(alignTimes(undefined, 2)).toEqual([0, 0])
    expect(alignTimes([NaN, -1, 7], 3)).toEqual([0, 0, 7])
  })

  it('keeps a time per line from the list, events and the receive time', () => {
    const store = useBackgroundAgents()
    store.processWebSocketMessage({ type: 'background_agents_list', session_id: 'times', agents: [
      agent('a', { recent_output: ['TOOL:Read: a', 'hello'], recent_output_at: [1000, 2000] }),
      agent('old', { recent_output: ['from before times'] })
    ] } as any)
    expect(store.getAgentOutputTimes('a')).toEqual([1000, 2000])
    expect(store.getAgentOutputTimes('old')).toEqual([0])

    store.processWebSocketMessage({ type: 'background_agent_output', session_id: 'times', agent_id: 'a', output: 'c', lines: ['b', 'c'], lines_at: [3000, 3100] })
    store.processWebSocketMessage({ type: 'background_agent_output', session_id: 'times', agent_id: 'a', output: 'd', at: 4000 })
    vi.useFakeTimers({ now: 9_000 })
    store.processWebSocketMessage({ type: 'background_agent_output', session_id: 'times', agent_id: 'a', output: 'e' }) // older server: no time
    vi.useRealTimers()
    expect(store.getAgentOutput('a')).toEqual(['TOOL:Read: a', 'hello', 'b', 'c', 'd', 'e'])
    expect(store.getAgentOutputTimes('a')).toEqual([1000, 2000, 3000, 3100, 4000, 9000])

    const { lines, times } = store.agentOutputWithTimes(store.getAgent('a')!)
    expect(lines).toHaveLength(6)
    expect(times).toBe(store.getAgentOutputTimes('a'))
  })

  it('trims times with the lines', () => {
    const store = useBackgroundAgents()
    store.processWebSocketMessage({ type: 'background_agents_list', session_id: 'times', agents: [agent('cap')] } as any)
    const lines = Array.from({ length: MAX_OUTPUT_LINES + 5 }, (_, i) => `line ${i}`)
    const at = lines.map((_, i) => 10_000 + i)
    store.processWebSocketMessage({ type: 'background_agent_output', session_id: 'times', agent_id: 'cap', output: lines.at(-1)!, lines, lines_at: at })
    store.processWebSocketMessage({ type: 'background_agent_output', session_id: 'times', agent_id: 'cap', output: 'last', at: 99_999 })
    expect(store.getAgentOutput('cap')).toHaveLength(MAX_OUTPUT_LINES)
    expect(store.getAgentOutputTimes('cap')).toHaveLength(MAX_OUTPUT_LINES)
    expect(store.getAgentOutputTimes('cap').at(-1)).toBe(99_999)
    expect(store.getAgentOutputTimes('cap')[0]).toBe(10_006)
  })

  it('uses the server-kept times when no live output arrived', () => {
    const store = useBackgroundAgents()
    const a = agent('kept', { recent_output: ['x', 'y'], recent_output_at: [5, 6] })
    expect(store.agentOutputWithTimes(a)).toEqual({ lines: ['x', 'y'], times: [5, 6] })
    const b = agent('nokept', { recent_output: ['x'] })
    const first = store.agentOutputWithTimes(b).times
    expect(first).toEqual([])
    expect(store.agentOutputWithTimes(b).times).toBe(first) // stable, so items aren't rebuilt
  })
})

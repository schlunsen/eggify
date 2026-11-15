import { createPinia, setActivePinia } from 'pinia'
import { describe, expect, it, beforeEach } from 'vitest'
import { useSessionStore, KEEP_MESSAGES_FOR_SESSIONS } from '../../stores/session/sessionStore'
import { useBackgroundAgents, MAX_OUTPUT_LINES, type BackgroundAgent } from '~/composables/agents/useBackgroundAgents'

const msg = (sessionId: string, id: string): any => ({ id, sessionId, role: 'assistant', content: id, timestamp: new Date(0) })

describe('session store keeps messages of recent sessions only', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('forgets the messages of sessions opened longer ago, keeps their permissions', () => {
    const store = useSessionStore()
    const ids = Array.from({ length: KEEP_MESSAGES_FOR_SESSIONS + 2 }, (_, i) => `s${i}`)
    store.addPermission('s0', { id: 'p1', sessionId: 's0', toolName: 'Bash', input: {}, timestamp: new Date(0) } as any)
    for (const id of ids) {
      store.setActiveSession(id)
      store.addMessages(id, [msg(id, `${id}-m`)])
      store.markMessagesLoaded(id)
    }

    const kept = ids.slice(-KEEP_MESSAGES_FOR_SESSIONS)
    expect(Object.keys(store.messages).sort()).toEqual([...kept].sort())
    expect(store.areMessagesLoaded('s0')).toBe(false)
    expect(store.areMessagesLoaded('s1')).toBe(false)
    expect(store.areMessagesLoaded(ids.at(-1)!)).toBe(true)
    expect(store.permissions.s0).toHaveLength(1)
  })

  it('re-opening a session moves it to the front instead of counting it twice', () => {
    const store = useSessionStore()
    for (const id of ['a', 'b', 'a', 'c', 'a', 'd', 'e']) {
      store.setActiveSession(id)
      store.addMessages(id, [msg(id, `${id}-m`)])
    }
    expect(store.recentSessionIds).toEqual(['e', 'd', 'a', 'c', 'b'])
    store.setActiveSession('f')
    expect(store.messages.b).toBeUndefined()
    expect(store.messages.a).toHaveLength(1)
  })

  it('drops messages that arrived for a session that was never opened', () => {
    const store = useSessionStore()
    store.addMessages('background', [msg('background', 'x')])
    store.setActiveSession('open')
    expect(store.messages.background).toBeUndefined()
  })
})

describe('background agent output is capped', () => {
  const agent = (id: string, recent: string[] = []): BackgroundAgent => ({
    agent_id: id, parent_session_id: 'cap', subagent_type: 'general-purpose', description: 'd',
    status: 'running', progress: 0, last_output: '', output_lines: 0,
    created_at: '2026-10-09T09:00:00Z', updated_at: '2026-10-09T09:00:00Z', recent_output: recent,
  })

  it('keeps only the newest MAX_OUTPUT_LINES lines across many events', () => {
    const { processWebSocketMessage, getAgentOutput, getAgent } = useBackgroundAgents()
    processWebSocketMessage({ type: 'background_agents_list', session_id: 'cap', agents: [agent('toolu_cap')] } as any)
    for (let i = 0; i < MAX_OUTPUT_LINES * 3; i++) {
      processWebSocketMessage({ type: 'background_agent_output', session_id: 'cap', agent_id: 'toolu_cap', output: `line ${i}`, lines: [`line ${i}`] })
    }
    const out = getAgentOutput('toolu_cap')
    expect(out).toHaveLength(MAX_OUTPUT_LINES)
    expect(out[0]).toBe(`line ${MAX_OUTPUT_LINES * 2}`)
    expect(out.at(-1)).toBe(`line ${MAX_OUTPUT_LINES * 3 - 1}`)
    // The running count still counts every line.
    expect(getAgent('toolu_cap')!.output_lines).toBe(MAX_OUTPUT_LINES * 3)
  })

  it('caps one oversized batch and a long server list', () => {
    const { processWebSocketMessage, getAgentOutput } = useBackgroundAgents()
    const long = Array.from({ length: MAX_OUTPUT_LINES + 50 }, (_, i) => `r${i}`)
    processWebSocketMessage({ type: 'background_agents_list', session_id: 'cap', agents: [agent('toolu_big', long)] } as any)
    expect(getAgentOutput('toolu_big')).toHaveLength(MAX_OUTPUT_LINES)
    expect(getAgentOutput('toolu_big')[0]).toBe('r50')

    const batch = Array.from({ length: MAX_OUTPUT_LINES + 10 }, (_, i) => `b${i}`)
    processWebSocketMessage({ type: 'background_agent_output', session_id: 'cap', agent_id: 'toolu_big', output: batch.at(-1), lines: batch })
    const out = getAgentOutput('toolu_big')
    expect(out).toHaveLength(MAX_OUTPUT_LINES)
    expect(out[0]).toBe('b10')
  })
})

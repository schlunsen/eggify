import { createPinia, setActivePinia } from 'pinia'
import { useSessionStore } from '../../stores/session/sessionStore'
import { reconcileAgents, type OverviewAgent } from '../../composables/agents/useAgentOverview'

const msg = (id: string, content = id): any => ({ id, sessionId: 's1', role: 'assistant', content, timestamp: new Date(0) })

describe('session store message upserts', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('addMessages gives the same result as addMessage per message', () => {
    const one = useSessionStore()
    const batch = [msg('a'), msg('b'), msg('a', 'a2'), msg('c')]
    for (const m of batch) one.addMessage('s1', m)

    setActivePinia(createPinia())
    const two = useSessionStore()
    two.addMessages('s1', batch)

    expect(two.messages.s1!.map(m => [m.id, m.content])).toEqual(one.messages.s1!.map(m => [m.id, m.content]))
    expect(two.messages.s1!.map(m => [m.id, m.content])).toEqual([['a', 'a2'], ['b', 'b'], ['c', 'c']])
  })

  it('replaces the array on every change and finds messages after prepends and removals', () => {
    const store = useSessionStore()
    store.addMessages('s1', [msg('b'), msg('c')])
    const before = store.messages.s1
    store.addMessage('s1', msg('d'))
    expect(store.messages.s1).not.toBe(before)

    store.prependMessages('s1', [msg('a')])
    store.updateMessage('s1', 'c', { content: 'c2' })
    store.removeMessage('s1', 'b')
    store.updateMessage('s1', 'd', { content: 'd2' })
    store.addMessage('s1', msg('a', 'a2'))
    store.addMessage('s1', msg('e'))

    expect(store.messages.s1!.map(m => [m.id, m.content])).toEqual([['a', 'a2'], ['c', 'c2'], ['d', 'd2'], ['e', 'e']])
  })

  it('updateMessage ignores unknown ids', () => {
    const store = useSessionStore()
    store.addMessages('s1', [msg('a')])
    const before = store.messages.s1
    store.updateMessage('s1', 'missing', { content: 'x' })
    expect(store.messages.s1).toBe(before)
  })
})

describe('reconcileAgents', () => {
  const agent = (id: string, status = 'idle'): OverviewAgent => ({ id, status, updated_at: '2026-01-01T00:00:00Z' })

  it('keeps unchanged agents and the list itself when nothing changed', () => {
    const snapshots = new Map<string, string>()
    const first = reconcileAgents([], [agent('a'), agent('b')], snapshots)
    const same = reconcileAgents(first, [agent('a'), agent('b')], snapshots)
    expect(same).toBe(first)

    const changed = reconcileAgents(first, [agent('a'), agent('b', 'processing')], snapshots)
    expect(changed).not.toBe(first)
    expect(changed[0]).toBe(first[0])
    expect(changed[1]).not.toBe(first[1])
    expect(changed[1]!.status).toBe('processing')

    const removed = reconcileAgents(changed, [agent('b', 'processing')], snapshots)
    expect(removed).toEqual([changed[1]])
    expect(snapshots.has('a')).toBe(false)
  })
})

import { pipNavOrder, pipSelection, pipSummary, PIP_RECENT_MS } from '../../utils/agents/subagentsPip'

const now = 1_000_000
const item = (id: string, status: any, startedAt: number, endedAt = NaN) => ({ id, status, startedAt, endedAt })

it('lists running subagents oldest first, then recently finished newest first', () => {
  const sel = pipSelection([
    item('late', 'running', now - 10),
    item('early', 'running', now - 100),
    item('old-done', 'done', 0, now - PIP_RECENT_MS - 1),
    item('done', 'done', 0, now - 5000),
    item('failed', 'failed', 0, now - 1000),
  ], now)
  expect(sel.rows.map(r => r.id)).toEqual(['early', 'late', 'failed', 'done'])
  expect(pipSummary(sel)).toBe('2 running · 1 done · 1 failed')
})

it('folds rows past the limit into a count', () => {
  const sel = pipSelection(Array.from({ length: 7 }, (_, i) => item(`a${i}`, 'running', i)), now, 5)
  expect(sel.rows).toHaveLength(5)
  expect(sel.hidden).toBe(2)
})

it('has nothing to show when every subagent finished long ago', () => {
  expect(pipSelection([item('x', 'done', 0, now - PIP_RECENT_MS)], now).rows).toEqual([])
})

it('steps through the card’s order, past its limit, then every older subagent', () => {
  const at = (ms: number) => new Date(ms).toISOString()
  const withCreated = (i: ReturnType<typeof item>) => ({ ...i, created_at: at(i.startedAt) })
  const order = pipNavOrder([
    item('older-done', 'done', 0, now - PIP_RECENT_MS - 50_000),
    item('late', 'running', now - 10),
    item('old-done', 'done', 0, now - PIP_RECENT_MS - 1),
    ...Array.from({ length: 6 }, (_, i) => item(`r${i}`, 'running', now - 1000 + i)),
    item('done', 'done', 0, now - 5000)
  ].map(withCreated), now)
  expect(order.map(i => i.id)).toEqual(['r0', 'r1', 'r2', 'r3', 'r4', 'r5', 'late', 'done', 'old-done', 'older-done'])
})

import { overviewState, overviewCandidates, markOverviewReviewed, readOverviewReviews, reviewVersion, justWentIdle } from '../../utils/agents/overviewState'
const now = Date.parse('2026-09-16T14:00:00Z')
const agent = { id: 'one', status: 'processing', message_count: 10, num_turns: 1, updated_at: new Date(now).toISOString() }
beforeEach(() => localStorage.clear())
it('keeps blockers ahead of working status and distinguishes finished turns from idle sessions', () => {
  expect(overviewState({ ...agent, pending_questions: 1 })).toEqual({ category: 'attention', label: 'Question waiting' })
  expect(overviewState({ ...agent, pending_permissions: 1 }).category).toBe('attention')
  expect(overviewState({ ...agent, status: 'error' }).category).toBe('attention')
  expect(overviewState({ ...agent, status: 'idle' }).label).toBe('Turn finished')
  expect(overviewState({ ...agent, status: 'idle', num_turns: 0 }).label).toBe('Idle')
  expect(overviewState({ ...agent, status: 'ended' }).label).toBe('Stopped')
})
it('retains recent results and every running or blocked agent, but omits old history', () => {
  const old = new Date(now - 48 * 3600000).toISOString()
  const sessions = [agent, { ...agent, id: 'done', status: 'idle' }, { ...agent, id: 'old', status: 'idle', updated_at: old },
    { ...agent, id: 'blocked', pending_permissions: 1, updated_at: old }, { ...agent, id: 'failed', status: 'error', message_count: 0 }]
  expect(overviewCandidates(sessions, now, 24).map(a => a.id)).toEqual(['one', 'done', 'blocked', 'failed'])
  expect(overviewCandidates(sessions, now, 168)).toHaveLength(5)
})
it('marks the current result reviewed locally and flags the next finished turn again', () => {
  const done = { ...agent, status: 'idle' }
  markOverviewReviewed(done)
  expect(readOverviewReviews()[done.id]).toBe(reviewVersion(done))
  expect(readOverviewReviews()[done.id]).not.toBe(reviewVersion({ ...done, num_turns: 2, message_count: 14 }))
  expect(overviewState({ ...agent, pending_permissions: 1 }).category).toBe('attention')
})
it('keeps a parent whose background subagents are still running in the working view', () => {
  const idle = { ...agent, status: 'idle' }
  expect(overviewState({ ...idle, subagents_running: 1 })).toEqual({ category: 'working', label: '1 subagent running' })
  expect(overviewState({ ...idle, subagents_running: 3 }).label).toBe('3 subagents running')
  expect(overviewState({ ...idle, subagents_running: 2, pending_permissions: 1 }).category).toBe('attention')
  expect(reviewVersion({ ...idle, subagents_total: 2 })).not.toBe(reviewVersion({ ...idle, subagents_total: 3 }))
})

it('treats a turn that ended moments ago as just gone idle', () => {
  const ago = (minutes: number) => new Date(now - minutes * 60000).toISOString()
  expect(justWentIdle({ ...agent, status: 'idle', updated_at: ago(2) }, now)).toBe(true)
  expect(justWentIdle({ ...agent, status: 'idle', updated_at: ago(20) }, now)).toBe(false)
  expect(justWentIdle({ ...agent, status: 'processing', updated_at: ago(2) }, now)).toBe(false) // still working
  expect(justWentIdle({ ...agent, status: 'ended', updated_at: ago(2) }, now)).toBe(false) // stopped on purpose
  expect(justWentIdle({ ...agent, status: 'idle', pending_questions: 1, updated_at: ago(2) }, now)).toBe(false) // needs attention instead
  expect(justWentIdle({ ...agent, status: 'idle', message_count: 0, updated_at: ago(2) }, now)).toBe(false) // never did anything
})

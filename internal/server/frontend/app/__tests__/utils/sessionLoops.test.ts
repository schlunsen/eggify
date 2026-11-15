import { describe, expect, it } from 'vitest'
import { loopWhen, loopWhenShort, sessionLoops } from '../../utils/agents/sessionLoops'

const NOW = Date.parse('2026-10-09T15:00:00Z')
const at = (minutes: number) => new Date(NOW + minutes * 60000).toISOString()
const job = (extra: Record<string, unknown> = {}) => ({
  id: '8841f567', cron: '*/30 * * * *', schedule: 'Every 30 minutes', prompt: 'check the review\nand report',
  recurring: true, created_at: at(-60), expires_at: at(7 * 24 * 60), next_at: at(14), ...extra
})

describe('sessionLoops', () => {
  it('reads a cron job', () => {
    const [loop] = sessionLoops({ id: 's', status: 'idle', scheduled_jobs: [job()] }, NOW)
    expect(loop).toMatchObject({ id: 'cron:8841f567', kind: 'cron', phase: 'waiting', title: 'check the review', schedule: 'Every 30 minutes', cron: '*/30 * * * *' })
    expect(loopWhen(loop!, NOW)).toBe('next in 14 min')
    expect(loopWhenShort(loop!, NOW)).toBe('14m')
  })

  it('marks a run in progress, a passed slot as due, and drops expired jobs', () => {
    expect(sessionLoops({ id: 's', status: 'processing', scheduled_jobs: [job()] }, NOW)[0]!.phase).toBe('running')
    expect(sessionLoops({ id: 's', status: 'idle', scheduled_jobs: [job({ next_at: at(-5) })] }, NOW)[0]!.phase).toBe('due')
    expect(sessionLoops({ id: 's', status: 'idle', scheduled_jobs: [job({ expires_at: at(-1) })] }, NOW)).toEqual([])
  })

  it('orders the Eggify loop first, then soonest first', () => {
    const loops = sessionLoops({
      id: 's', status: 'idle',
      scheduled_jobs: [job({ id: 'late', next_at: at(50) }), job({ id: 'soon', next_at: at(5) })],
      scheduled_loop: { next_at: at(20), delay_seconds: 1200, reason: 'wait for CI' },
      loop_state: { type: 'loop_started', iteration: 1, max_iterations: 5, goal: 'fix it' }
    }, NOW)
    expect(loops.map(l => l.id)).toEqual(['eggify', 'cron:soon', 'wakeup', 'cron:late'])
  })

  it('ignores finished Eggify loops and stale wakeups', () => {
    expect(sessionLoops({ id: 's', status: 'idle', loop_state: { type: 'loop_completed', iteration: 4, max_iterations: 5 } }, NOW)).toEqual([])
    expect(sessionLoops({ id: 's', status: 'idle', scheduled_loop: { next_at: at(-30), delay_seconds: 600 } }, NOW)).toEqual([])
  })
})

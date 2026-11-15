import { describe, expect, it } from 'vitest'
import { loopLabel, loopPhase, loopShortLabel } from '~/utils/agents/scheduledLoop'
import { overviewState } from '~/utils/agents/overviewState'

const now = Date.parse('2026-10-05T12:00:00Z')
const at = (minutes: number) => ({ next_at: new Date(now + minutes * 60000).toISOString(), delay_seconds: 1800 })

describe('loop labels', () => {
  it('counts down to the next run while idle', () => {
    expect(loopLabel(at(25), 'idle', now)).toBe('Loop · next in 25 min')
    expect(loopLabel(at(90), 'idle', now)).toBe('Loop · next in 1 h 30 min')
    expect(loopLabel(at(0.2), 'idle', now)).toBe('Loop · next in under a minute')
    expect(loopShortLabel(at(25), 'idle', now)).toBe('loop 25m')
    expect(loopShortLabel(at(120), 'idle', now)).toBe('loop 2h')
  })

  it('says an iteration runs while the session works, and is due just after its time', () => {
    expect(loopLabel(at(-1), 'processing', now)).toBe('Loop · iteration running')
    expect(loopLabel(at(-1), 'idle', now)).toBe('Loop · due now')
    expect(loopShortLabel(at(-1), 'idle', now)).toBe('loop now')
  })

  it('drops a wakeup long overdue, and shows nothing without one', () => {
    expect(loopPhase(at(-10), 'idle', now)).toBeNull()
    expect(loopLabel(null, 'idle', now)).toBe('')
    expect(loopLabel({ next_at: 'nope', delay_seconds: 60 }, 'idle', now)).toBe('')
  })

  it('keeps a cron run due for longer, since the CLI starts it late', () => {
    expect(loopPhase({ ...at(-10), kind: 'cron' }, 'idle', now)).toBe('due')
    expect(loopPhase({ ...at(-90), kind: 'cron' }, 'idle', now)).toBeNull()
  })
})

describe('overviewState with a loop', () => {
  it('keeps a looping idle session among the working ones', () => {
    expect(overviewState({ id: 'a', status: 'idle', scheduled_loop: at(25), subagents_running: 1 }, now))
      .toEqual({ category: 'working', label: 'Loop · next in 25 min · 1 subagent' })
  })

  it('still asks for attention first', () => {
    expect(overviewState({ id: 'a', status: 'idle', scheduled_loop: at(25), pending_questions: 1 }, now).category).toBe('attention')
  })
})

describe('activelyWorking', () => {
  it('does not count a loop that is only waiting', async () => {
    const { activelyWorking } = await import('~/utils/agents/overviewState')
    expect(activelyWorking({ id: 'a', status: 'idle', scheduled_loop: at(25) }, now)).toBe(false)
    expect(activelyWorking({ id: 'a', status: 'processing', scheduled_loop: at(-1) }, now)).toBe(true)
    expect(activelyWorking({ id: 'a', status: 'idle', subagents_running: 1 }, now)).toBe(true)
  })
})

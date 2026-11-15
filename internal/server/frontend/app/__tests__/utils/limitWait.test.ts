import { hasLimitWait, limitWaitDetail, limitWaitLabel, limitWaitShortLabel, usageLimitSummary, type LimitWait } from '~/utils/agents/limitWait'
import { groupSessions } from '~/utils/agents/sessionGroups'
import { overviewState } from '~/utils/agents/overviewState'
import { isInterruptionMessage, subagentDisplayStatus, subagentProblem } from '~/utils/agents/subagentActivity'

const now = new Date(2026, 9, 8, 22, 30).getTime()
const at = (h: number, m = 0, dayOffset = 0) => new Date(2026, 9, 8 + dayOffset, h, m).toISOString()
const wait = (over: Partial<LimitWait> = {}): LimitWait => ({
  why: 'turn', since: at(22, 0), resume_at: at(2, 1, 1), resets_at: at(2, 0, 1), limit_type: 'five_hour', ...over,
})

describe('limit wait labels', () => {
  it('says when the session continues', () => {
    expect(limitWaitLabel(wait(), now)).toMatch(/^Usage limit · resumes 0?2:01(\s?[AP]M)? tomorrow \(in 3 h 31 min\)$/)
    expect(limitWaitShortLabel(wait(), now)).toMatch(/^⏸ 0?2:01(\s?[AP]M)?$/)
    expect(limitWaitLabel(wait({ resume_at: at(22, 0) }), now)).toBe('Usage limit · resuming now')
    expect(limitWaitLabel(null, now)).toBe('')
    expect(hasLimitWait({ ...wait(), resume_at: 'nope' })).toBe(false)
  })

  it('describes what the wait covers', () => {
    expect(limitWaitDetail(wait())).toBe('Claude usage limit reached (5-hour window). The last turn was cut off.')
    expect(limitWaitDetail(wait({ why: 'subagents', subagents: [{ description: 'a' }, { description: 'b' }], resets_at: null })))
      .toBe('Claude usage limit reached (5-hour window). 2 subagents were paused. No reset time was given, so it tries again periodically.')
  })

  it('summarises the account usage for the meter', () => {
    expect(usageLimitSummary({ status: 'allowed_warning', limit_type: 'five_hour', utilization: 0.9, resets_at: at(2, 0, 1) }, now))
      .toMatch(/^Session limit: 90% used · resets 0?2:00(\s?[AP]M)? tomorrow$/)
    expect(usageLimitSummary({ status: 'rejected', limit_type: 'seven_day' }, now)).toBe('Weekly limit reached')
    expect(usageLimitSummary(null, now)).toBe('')
  })
})

describe('waiting sessions', () => {
  it('are in the Working group of the sidebar', () => {
    const groups = groupSessions([
      { id: 'w', status: 'idle', updated_at: at(20, 0), limit_wait: wait() },
      { id: 'i', status: 'idle', updated_at: at(20, 0) },
    ], { now })
    expect(groups.find(g => g.id === 'working')!.rows.map(r => r.session.id)).toEqual(['w'])
  })

  it('are working in the overview', () => {
    expect(overviewState({ id: 'w', status: 'idle', limit_wait: wait() } as any, now)).toEqual({
      category: 'working', label: expect.stringMatching(/^Usage limit · resumes/),
    })
  })
})

describe('subagents paused by the limit', () => {
  it('read as interrupted, with a plain explanation', () => {
    const agent = { status: 'failed', error_message: 'Agent paused: usage limit reached' }
    expect(isInterruptionMessage(agent.error_message)).toBe(true)
    expect(subagentDisplayStatus(agent)).toBe('interrupted')
    expect(subagentProblem(agent)).toContain('usage limit stopped it')
  })
})

describe('Codex usage', () => {
  it('names windows and picks the tightest one', async () => {
    const { windowLabel, codexTightestWindow, codexUsageSummary, isCodexRuntime, usageLevel } = await import('../../utils/agents/limitWait')
    expect(windowLabel(10080)).toBe('Weekly limit')
    expect(windowLabel(300)).toBe('5-hour limit')
    expect(windowLabel(2880)).toBe('2-day limit')
    const info = { limit_reached: false, observed_at: '', windows: [{ used_percent: 20, window_minutes: 300 }, { used_percent: 64, window_minutes: 10080 }] }
    expect(codexTightestWindow(info)?.window_minutes).toBe(10080)
    expect(codexUsageSummary(info, Date.now())).toBe('Weekly limit: 64% used')
    expect(codexUsageSummary({ ...info, limit_reached: true }, Date.now())).toBe('Weekly limit reached')
    expect(codexUsageSummary(null, Date.now())).toBe('')
    expect(isCodexRuntime('codex', 'gpt-6-sol')).toBe(true)
    expect(isCodexRuntime('openai', 'gpt-5-codex')).toBe(true)
    expect(isCodexRuntime('claude', 'claude-opus-5-5')).toBe(false)
    expect(usageLevel(50, false)).toBe('calm')
    expect(usageLevel(80, false)).toBe('warn')
    expect(usageLevel(10, true)).toBe('limited')
  })
})

import {
  groupSessionIds,
  groupSessions,
  nestedWorkers,
  permissionWaitLabel,
  relativeTime,
  startOfLocalDay,
  visibleSessionOrder,
  waitingLabel,
  workersWorkingLabel,
  type GroupableSession,
} from '../../utils/agents/sessionGroups'

// A fixed local "now": Tuesday 6 Oct 2026, 15:00 local time.
const NOW = new Date(2026, 9, 6, 15, 0, 0).getTime()
const at = (h: number, m = 0, dayOffset = 0) => new Date(2026, 9, 6 + dayOffset, h, m).toISOString()

type S = GroupableSession & { subagents?: number; waiting?: string }
const s = (id: string, status: string, updated_at: string, extra: Partial<S> = {}): S => ({ id, status, updated_at, ...extra })

const group = (sessions: S[]) => groupSessions(sessions, {
  now: NOW,
  waitingFor: x => x.waiting || '',
  subagentsWorking: x => x.subagents || 0,
})
const ids = (groups: ReturnType<typeof group>, id: string) => groups.find(g => g.id === id)!.rows.map(r => r.session.id)

describe('groupSessions', () => {
  it('sorts sessions into Needs you, Working, Today and Earlier, newest first', () => {
    const groups = group([
      s('old', 'idle', at(10, 0, -2)),
      s('q', 'idle', at(9), { waiting: 'Question waiting' }),
      s('busy', 'processing', at(14)),
      s('sub', 'idle', at(11), { subagents: 2 }),
      s('t1', 'idle', at(8)),
      s('t2', 'idle', at(13)),
      s('perm', 'processing', at(14, 30), { waiting: 'Allow git push?' }),
    ])
    expect(groups.map(g => g.id)).toEqual(['needs', 'working', 'today', 'earlier'])
    expect(groups.map(g => g.label)).toEqual(['Needs you', 'Working', 'Today', 'Earlier'])
    expect(ids(groups, 'needs')).toEqual(['perm', 'q'])
    expect(ids(groups, 'working')).toEqual(['busy', 'sub'])
    expect(ids(groups, 'today')).toEqual(['t2', 't1'])
    expect(ids(groups, 'earlier')).toEqual(['old'])
    const needs = groups[0]!.rows
    expect(needs[0]!.waitingFor).toBe('Allow git push?')
    expect(groups[1]!.rows.find(r => r.session.id === 'sub')!.subagents).toBe(2)
  })

  it('splits Today from Earlier at local midnight', () => {
    const groups = group([
      s('just-after', 'idle', new Date(startOfLocalDay(NOW) + 1000).toISOString()),
      s('just-before', 'idle', new Date(startOfLocalDay(NOW) - 1000).toISOString()),
    ])
    expect(ids(groups, 'today')).toEqual(['just-after'])
    expect(ids(groups, 'earlier')).toEqual(['just-before'])
  })

  it('keeps an idle session full-size for 12 hours after its last activity', () => {
    const groups = group([
      s('busy', 'processing', at(14)),
      s('just-idle', 'idle', at(14, 30)),
      s('idle-11h', 'idle', at(3, 30)),
      s('idle-13h', 'idle', at(2)),
      s('late-yesterday', 'idle', at(23, 0, -1)),
      s('ended-now', 'ended', at(14, 45)),
    ])
    const full = Object.fromEntries(groups.flatMap(g => g.rows).map(r => [r.session.id, r.full]))
    expect(full).toEqual({
      'busy': true,
      'just-idle': true,
      'idle-11h': true,
      'idle-13h': false,
      'late-yesterday': false, // 16 h before 15:00
      'ended-now': false,
    })
  })

  it('puts ended sessions in Today/Earlier only, whatever else they report', () => {
    const groups = group([
      s('e1', 'ended', at(12), { waiting: 'Question waiting', subagents: 3 }),
      s('e2', 'ended', at(12, 0, -1)),
    ])
    expect(ids(groups, 'needs')).toEqual([])
    expect(ids(groups, 'working')).toEqual([])
    expect(ids(groups, 'today')).toEqual(['e1'])
    expect(ids(groups, 'earlier')).toEqual(['e2'])
    expect(groups[2]!.rows[0]!.waitingFor).toBe('')
  })

  it('counts a scheduled Claude Code loop as working', () => {
    const next = new Date(NOW + 20 * 60000).toISOString()
    const groups = group([s('loop', 'idle', at(9), { scheduled_loop: { next_at: next, delay_seconds: 1200 } })])
    expect(ids(groups, 'working')).toEqual(['loop'])
  })

  it('falls back to created_at and drops duplicate ids', () => {
    const groups = group([
      { id: 'a', status: 'idle', created_at: at(12) },
      { id: 'a', status: 'idle', created_at: at(12) },
      { id: 'b', status: 'idle' },
    ])
    expect(ids(groups, 'today')).toEqual(['a'])
    expect(ids(groups, 'earlier')).toEqual(['b'])
  })
})

describe('visibleSessionOrder', () => {
  const groups = group([
    s('old', 'idle', at(10, 0, -2)),
    s('busy', 'processing', at(14)),
    s('t1', 'idle', at(8)),
    s('q', 'idle', at(9), { waiting: 'Question waiting' }),
  ])

  it('follows the groups top to bottom and leaves Earlier out by default', () => {
    expect(visibleSessionOrder(groups, {}).map(x => x.id)).toEqual(['q', 'busy', 't1'])
  })

  it('skips any collapsed group and includes an expanded Earlier', () => {
    expect(visibleSessionOrder(groups, { today: true, earlier: false }).map(x => x.id)).toEqual(['q', 'busy', 'old'])
  })

  it('never collapses Needs you or Working', () => {
    expect(visibleSessionOrder(groups, { needs: true, working: true, earlier: false }).map(x => x.id)).toEqual(['q', 'busy', 't1', 'old'])
  })
})

describe('relativeTime', () => {
  it('shows now, minutes, hours today, the weekday this week and a date before that', () => {
    expect(relativeTime(NOW - 20000, NOW)).toBe('now')
    expect(relativeTime(NOW - 4 * 60000, NOW)).toBe('4m')
    expect(relativeTime(NOW - 2 * 3600000, NOW)).toBe('2h')
    expect(relativeTime(new Date(2026, 9, 5, 23, 0).getTime(), NOW)).toBe('Mon')
    expect(relativeTime(new Date(2026, 8, 20, 12, 0).getTime(), NOW)).toBe('20 Sep')
    expect(relativeTime(0, NOW)).toBe('')
  })
})

describe('waiting labels', () => {
  it('says which command or file a permission request is for', () => {
    expect(permissionWaitLabel({ tool: 'Bash', details: { command: 'gh pr merge 1124' } })).toBe('Allow gh pr merge 1124?')
    expect(permissionWaitLabel({ toolName: 'Edit', details: { file_path: '/repo/app/app.vue' } })).toBe('Allow Edit app.vue?')
    expect(permissionWaitLabel({ tool: 'WebFetch' })).toBe('Allow WebFetch?')
    expect(permissionWaitLabel({ tool: 'Bash', details: { command: 'x'.repeat(100) } })).toMatch(/^Allow x+…\?$/)
  })

  it('prefers questions, then permissions, and ignores answered ones', () => {
    expect(waitingLabel([{ status: 'pending', tool: 'Bash', details: { command: 'ls' } }], [{ status: 'pending' }])).toBe('Question waiting')
    expect(waitingLabel([{ status: 'pending', tool: 'Bash', details: { command: 'ls' } }], [])).toBe('Allow ls?')
    expect(waitingLabel([{ status: 'approved', tool: 'Bash' }], [{ status: 'answered' }])).toBe('')
    expect(waitingLabel(undefined, [{ status: 'pending', kind: 'loop_session' }])).toBe('Loop proposal waiting')
    expect(waitingLabel(undefined, [{ status: 'pending', kind: 'worker_session' }])).toBe('Worker proposal waiting')
  })

  it('uses the listed counts only for kinds this tab has no record of', () => {
    expect(waitingLabel(undefined, undefined, { pending_permissions: 1 })).toBe('Approval needed')
    expect(waitingLabel(undefined, undefined, { pending_questions: 2 })).toBe('Question waiting')
    // The request was answered here: the stale count no longer counts.
    expect(waitingLabel([], [], { pending_permissions: 1, pending_questions: 1 })).toBe('')
  })
})

describe('worker sessions', () => {
  const worker = (id: string, parent: string, mode: 'build' | 'review', status: string, updated_at: string, extra: Record<string, unknown> = {}, created = updated_at): S => ({
    id, status, updated_at, created_at: created, model_name: mode === 'build' ? 'claude-sonnet-5-5' : 'gpt-6-astra',
    options: { spawned_by_session_id: parent, worker: { mode, ...extra } },
  })
  const row = (groups: ReturnType<typeof group>, id: string) => groups.flatMap(g => g.rows).find(r => r.session.id === id)!

  it('hangs workers under their parent, oldest first, instead of rows of their own', () => {
    const groups = group([
      worker('r1', 'lead', 'review', 'idle', at(14, 30), { reviews_session_id: 'b1' }, at(13)),
      s('lead', 'idle', at(12)),
      worker('b1', 'lead', 'build', 'idle', at(14), { last_verdict: 'changes_requested', review_rounds: 1 }, at(12, 30)),
      s('other', 'idle', at(11)),
    ])
    expect(ids(groups, 'today')).toEqual(['lead', 'other'])
    expect(row(groups, 'lead').workers.map(w => w.session.id)).toEqual(['b1', 'r1'])
    expect(row(groups, 'lead').workers.map(w => [w.view.role, w.view.modelLabel, w.view.status, w.view.verdict])).toEqual([
      ['build', 'Sonnet 5.5', 'idle', 'changes_requested'],
      ['review', 'gpt-6-astra', 'idle', 'changes_requested'],
    ])
    expect(row(groups, 'lead').workersWorking).toBe(0)
    expect(row(groups, 'other').workers).toEqual([])
  })

  it('counts a parent with a working worker as Working', () => {
    const groups = group([
      s('lead', 'idle', at(9, 0, -1)),
      worker('b1', 'lead', 'build', 'processing', at(14)),
    ])
    expect(ids(groups, 'working')).toEqual(['lead'])
    expect(row(groups, 'lead')).toMatchObject({ workersWorking: 1, full: true })
    expect(ids(groups, 'earlier')).toEqual([])
    expect(workersWorkingLabel(1)).toBe('1 worker working')
    expect(workersWorkingLabel(2)).toBe('2 workers working')
  })

  it('keeps a waiting parent in Needs you, with its workers', () => {
    const groups = group([
      s('lead', 'idle', at(12), { waiting: 'Worker proposal waiting' }),
      worker('b1', 'lead', 'build', 'processing', at(14)),
    ])
    expect(ids(groups, 'needs')).toEqual(['lead'])
    expect(row(groups, 'lead').workers).toHaveLength(1)
  })

  it('shows a worker as a normal row when its parent is missing or has ended', () => {
    const orphan = group([worker('b1', 'gone', 'build', 'processing', at(14)), s('x', 'idle', at(13))])
    expect(ids(orphan, 'working')).toEqual(['b1'])
    expect(row(orphan, 'b1').workers).toEqual([])

    const ended = group([s('lead', 'ended', at(12)), worker('b1', 'lead', 'build', 'processing', at(14))])
    expect(ids(ended, 'working')).toEqual(['b1'])
    expect(ids(ended, 'today')).toEqual(['lead'])
    expect(row(ended, 'lead').workers).toEqual([])
  })

  it('never nests under a session that is itself nested, and survives a cycle', () => {
    const chain = group([
      s('lead', 'idle', at(12)),
      worker('b1', 'lead', 'build', 'idle', at(13)),
      worker('deep', 'b1', 'build', 'idle', at(14)),
    ])
    expect(row(chain, 'lead').workers.map(w => w.session.id)).toEqual(['b1'])
    expect(ids(chain, 'today')).toEqual(['deep', 'lead'])

    const cycle = group([worker('a', 'b', 'build', 'idle', at(13)), worker('b', 'a', 'build', 'idle', at(14))])
    expect(ids(cycle, 'today')).toEqual(['b', 'a'])
    expect(nestedWorkers([worker('self', 'self', 'build', 'idle', at(13))]).size).toBe(0)
  })

  it('puts workers right after their parent in the on-screen order and in the group ids', () => {
    const groups = group([
      s('busy', 'processing', at(14, 30)),
      s('lead', 'idle', at(12)),
      worker('r1', 'lead', 'review', 'processing', at(14), { reviews_session_id: 'b1' }, at(13)),
      worker('b1', 'lead', 'build', 'idle', at(13, 30), {}, at(12, 30)),
      s('t1', 'idle', at(8)),
      s('old', 'idle', at(10, 0, -2)),
      worker('old-b', 'old', 'build', 'idle', at(11, 0, -2)),
    ])
    expect(visibleSessionOrder(groups, {}).map(x => x.id)).toEqual(['busy', 'lead', 'b1', 'r1', 't1'])
    expect(visibleSessionOrder(groups, { earlier: false }).map(x => x.id)).toEqual(['busy', 'lead', 'b1', 'r1', 't1', 'old', 'old-b'])
    expect(groupSessionIds(groups.find(g => g.id === 'working')!)).toEqual(['busy', 'lead', 'b1', 'r1'])
    expect(groupSessionIds(groups.find(g => g.id === 'earlier')!)).toEqual(['old', 'old-b'])
  })
})

describe('a worker that waits for the user', () => {
  it('is a row of its own in Needs you, not hidden under its parent', () => {
    const asking: S = {
      id: 'b1', status: 'idle', updated_at: at(14), waiting: 'Question waiting',
      options: { spawned_by_session_id: 'lead', worker: { mode: 'build' } },
    }
    const groups = group([s('lead', 'idle', at(12)), asking])
    expect(ids(groups, 'needs')).toEqual(['b1'])
    expect(ids(groups, 'today')).toEqual(['lead'])
    expect(groups.flatMap(g => g.rows).every(r => r.workers.length === 0)).toBe(true)
  })
})

import { describe, expect, it } from 'vitest'
import type { BackgroundAgent } from '../../composables/agents/useBackgroundAgents'
import {
  allAgentsStats, buildAllAgentsTree, flattenAllAgents, nestSubagents, sessionGroup,
  type AllAgentsRow, type AllAgentsSession
} from '../../utils/agents/allAgentsTree'

const NOW = Date.parse('2026-10-06T12:00:00Z')
const ago = (minutes: number) => new Date(NOW - minutes * 60000).toISOString()

function session(id: string, extra: Partial<AllAgentsSession> = {}): AllAgentsSession {
  return { id, status: 'idle', created_at: ago(300), updated_at: ago(5), selected_avatar: { name: id }, ...extra }
}
function sub(id: string, sessionId: string, extra: Partial<BackgroundAgent> = {}): BackgroundAgent {
  return {
    agent_id: id, parent_session_id: sessionId, subagent_type: 'general-purpose', description: id,
    status: 'running', progress: 0, last_output: '', output_lines: 0, created_at: ago(30), updated_at: ago(1), ...extra
  }
}
const done = (minutesAgo: number): Partial<BackgroundAgent> => ({ status: 'completed', completed_at: ago(minutesAgo), updated_at: ago(minutesAgo) })
const describeRow = (row: AllAgentsRow) =>
  row.kind === 'group' ? `# ${row.title}`
    : row.kind === 'loop' ? `  ↻ ${row.loop.title} [${row.loop.schedule}]`
    : row.kind === 'session' ? `${row.node.name}${row.hasChildren ? row.expanded ? ' ▾' : ' ▸' : ''}`
      : row.kind === 'more' ? `${'  '}+${row.count} finished${row.expanded ? ' ▾' : ''}`
        : `${'  '.repeat(row.depth)}${row.node.agent.agent_id} (${row.node.status})`
const rowsOf = (opts: Parameters<typeof buildAllAgentsTree>[0], state = { collapsed: new Set<string>(), expandedFinished: new Set<string>() }) =>
  flattenAllAgents(buildAllAgentsTree({ now: NOW, ...opts }), state).map(describeRow)

describe('sessionGroup', () => {
  it('puts pending requests first, then working, then recently active', () => {
    expect(sessionGroup(session('a', { status: 'processing', pending_permissions: 1 }), 0, NOW)).toBe('needs')
    expect(sessionGroup(session('a', { pending_questions: 1 }), 0, NOW)).toBe('needs')
    expect(sessionGroup(session('a', { status: 'processing' }), 0, NOW)).toBe('working')
    // Idle, but its subagents still run.
    expect(sessionGroup(session('a'), 2, NOW)).toBe('working')
    expect(sessionGroup(session('a', { subagents_running: 1 }), 0, NOW)).toBe('working')
    expect(sessionGroup(session('a', { updated_at: ago(90) }), 0, NOW)).toBe('recent')
    expect(sessionGroup(session('a', { updated_at: ago(150) }), 0, NOW)).toBeNull()
  })
})

describe('nestSubagents', () => {
  it('nests a subagent under the one that launched it, running first, finished newest first', () => {
    const roots = nestSubagents([
      sub('old-done', 's', done(50)),
      sub('new-done', 's', done(5)),
      sub('runner', 's', { created_at: ago(20) }),
      sub('first', 's', { created_at: ago(40) }),
      sub('child', 's', { parent_agent_id: 'runner' }),
      sub('orphan', 's', { parent_agent_id: 'gone', created_at: ago(10) })
    ])
    expect(roots.map(n => n.agent.agent_id)).toEqual(['first', 'runner', 'orphan', 'new-done', 'old-done'])
    expect(roots[1]!.children.map(n => n.agent.agent_id)).toEqual(['child'])
  })

  it('keeps a finished parent with a running child among the running ones', () => {
    const roots = nestSubagents([sub('parent', 's', done(3)), sub('kid', 's', { parent_agent_id: 'parent' }), sub('other', 's', done(1))])
    expect(roots.map(n => [n.agent.agent_id, n.active])).toEqual([['parent', true], ['other', false]])
  })

  it('survives a parent cycle', () => {
    const roots = nestSubagents([sub('a', 's', { parent_agent_id: 'b' }), sub('b', 's', { parent_agent_id: 'a' })])
    expect(roots.length).toBeGreaterThan(0)
  })
})

describe('buildAllAgentsTree', () => {
  const sessions = [
    session('Asking', { status: 'idle', pending_questions: 1, updated_at: ago(1) }),
    session('Busy', { status: 'processing', created_at: ago(200) }),
    session('Waiting', { status: 'idle', created_at: ago(100) }),
    session('Quiet', { status: 'idle', updated_at: ago(30) }),
    session('Ancient', { status: 'idle', updated_at: ago(600) })
  ]
  const subagents = [
    sub('busy-run', 'Busy'),
    sub('busy-done', 'Busy', done(10)),
    sub('wait-run', 'Waiting', { created_at: ago(15) }),
    sub('wait-kid', 'Waiting', { parent_agent_id: 'wait-run', subagent_type: 'code-reviewer' }),
    sub('quiet-1', 'Quiet', done(40)),
    sub('quiet-2', 'Quiet', { status: 'failed', error_message: 'exit status 1', updated_at: ago(35) }),
    sub('quiet-3', 'Quiet', { status: 'failed', error_message: 'App restarted while agent was running', updated_at: ago(36) })
  ]

  it('Running: needs-you and working sessions, finished subagents folded', () => {
    expect(rowsOf({ sessions, subagents, filter: 'running' })).toEqual([
      '# Needs you', 'Asking',
      '# Working', 'Busy ▾', '  busy-run (running)', '  +1 finished',
      'Waiting ▾', '  wait-run (running)', '    wait-kid (running)'
    ])
  })

  it('All: adds recent sessions; all-finished sessions fold into "N finished", newest first when unfolded', () => {
    expect(rowsOf({ sessions, subagents, filter: 'all' })).toEqual([
      '# Needs you', 'Asking',
      '# Working', 'Busy ▾', '  busy-run (running)', '  busy-done (done)',
      'Waiting ▾', '  wait-run (running)', '    wait-kid (running)',
      '# Recent', 'Quiet ▾', '  +3 finished'
    ])
    const open = rowsOf({ sessions, subagents, filter: 'all' }, { collapsed: new Set(['Busy']), expandedFinished: new Set(['Quiet']) })
    expect(open.slice(3)).toEqual([
      'Busy ▸', 'Waiting ▾', '  wait-run (running)', '    wait-kid (running)',
      '# Recent', 'Quiet ▾', '  +3 finished ▾', '  quiet-2 (failed)', '  quiet-3 (interrupted)', '  quiet-1 (done)'
    ])
  })

  it('Failed: only sessions with failed or interrupted subagents, unfolded', () => {
    expect(rowsOf({ sessions, subagents, filter: 'failed' })).toEqual([
      '# Recent', 'Quiet ▾', '  quiet-2 (failed)', '  quiet-3 (interrupted)'
    ])
  })

  it('folds finished subagents beside running ones only when there are many', () => {
    const many = [sub('r', 'Busy'), ...[1, 2, 3, 4].map(i => sub(`d${i}`, 'Busy', done(i)))]
    expect(rowsOf({ sessions: [sessions[1]!], subagents: many, filter: 'all' })).toEqual(['# Working', 'Busy ▾', '  r (running)', '  +4 finished'])
    const few = [sub('r', 'Busy'), sub('d1', 'Busy', done(1))]
    expect(rowsOf({ sessions: [sessions[1]!], subagents: few, filter: 'all' })).toEqual(['# Working', 'Busy ▾', '  r (running)', '  d1 (done)'])
  })

  it('shows an old session when it still has subagents in the list', () => {
    expect(rowsOf({ sessions: [sessions[4]!], subagents: [sub('late', 'Ancient', done(200))], filter: 'all' })).toEqual(['# Recent', 'Ancient ▾', '  +1 finished'])
    expect(rowsOf({ sessions: [sessions[4]!], subagents: [], filter: 'all' })).toEqual([])
  })

  it('text filter matches session names, user tags, and subagent description or type', () => {
    expect(rowsOf({ sessions, subagents, filter: 'all', query: 'busy' })).toEqual(['# Working', 'Busy ▾', '  busy-run (running)', '  busy-done (done)'])
    const tagged = rowsOf({ sessions, subagents, filter: 'all', query: 'auth', tagsOf: s => s.id === 'Quiet' ? ['Auth bug'] : [] })
    expect(tagged).toEqual(['# Recent', 'Quiet ▾', '  +3 finished'])
    // Only the matching subagent (and the one it sits under) stays.
    expect(rowsOf({ sessions, subagents, filter: 'all', query: 'REVIEWER' })).toEqual(['# Working', 'Waiting ▾', '  wait-run (running)', '    wait-kid (running)'])
    expect(rowsOf({ sessions, subagents, filter: 'all', query: 'nothing matches' })).toEqual([])
  })
})

describe('allAgentsStats', () => {
  it('counts working sessions, running subagents, and what finished or failed in the last hour', () => {
    const stats = allAgentsStats(
      [session('a', { status: 'processing' }), session('b'), session('c')],
      [sub('1', 'b'), sub('2', 'a'), sub('3', 'c', done(10)), sub('4', 'c', done(90)), sub('5', 'c', { status: 'failed', updated_at: ago(5) })],
      NOW
    )
    expect(stats).toEqual({ sessionsWorking: 2, subagentsRunning: 2, finishedLastHour: 1, failedLastHour: 1, loopsActive: 0 })
  })
})

const cronJob = (id: string, prompt: string, nextInMinutes: number) => ({
  id, cron: '*/30 * * * *', schedule: 'Every 30 minutes', prompt, recurring: true,
  created_at: ago(60), expires_at: new Date(NOW + 6 * 24 * 3600e3).toISOString(),
  next_at: new Date(NOW + nextInMinutes * 60000).toISOString()
})

describe('loops', () => {
  it('keeps an idle session with a cron job in Working, its loops above its subagents', () => {
    const sessions = [
      session('Cron', { updated_at: ago(400), scheduled_jobs: [cronJob('8841f567', 'check TestFlight review\nthen report', 14)] }),
      session('Idle')
    ]
    expect(sessionGroup(sessions[0]!, 0, NOW)).toBe('working')
    expect(rowsOf({ sessions, subagents: [sub('s1', 'Cron')], filter: 'running' })).toEqual([
      '# Working', 'Cron ▾', '  ↻ check TestFlight review [Every 30 minutes]', '  s1 (running)'
    ])
    // The Failed filter is about subagents only.
    expect(rowsOf({ sessions, subagents: [], filter: 'failed' })).toEqual([])
  })

  it('shows /loop wakeups and Eggify loops, and filters loops by prompt', () => {
    const sessions = [
      session('Waker', { scheduled_loop: { next_at: new Date(NOW + 25 * 60000).toISOString(), delay_seconds: 1800, reason: 'waiting for CI', kind: 'wakeup' } }),
      session('Looper', { loop_state: { type: 'loop_iteration', iteration: 3, max_iterations: 20, goal: 'make the tests pass' } }),
      session('Stale', { updated_at: ago(400), scheduled_loop: { next_at: ago(30), delay_seconds: 600 } })
    ]
    expect(rowsOf({ sessions, subagents: [], filter: 'running' })).toEqual([
      '# Working', 'Looper ▾', '  ↻ make the tests pass [Iteration 3 of 20]', 'Waker ▾', '  ↻ waiting for CI [/loop]'
    ])
    expect(rowsOf({ sessions, subagents: [], filter: 'all', query: 'tests pass' })).toEqual(['# Working', 'Looper ▾', '  ↻ make the tests pass [Iteration 3 of 20]'])
    expect(allAgentsStats(sessions, [], NOW)).toMatchObject({ sessionsWorking: 2, loopsActive: 2 })
  })

  it("doesn't count the cron entry in scheduled_loop twice", () => {
    const job = cronJob('a', 'check', 10)
    const s = session('Both', { scheduled_jobs: [job], scheduled_loop: { next_at: job.next_at, delay_seconds: 1800, kind: 'cron' } })
    expect(rowsOf({ sessions: [s], subagents: [], filter: 'running' })).toEqual(['# Working', 'Both ▾', '  ↻ check [Every 30 minutes]'])
  })
})

describe('worker sessions', () => {
  const worker = (id: string, parent: string, mode: 'build' | 'review', extra: Partial<AllAgentsSession> = {}, config: Record<string, unknown> = {}): AllAgentsSession => session(id, {
    model_name: mode === 'build' ? 'claude-sonnet-5-5' : 'gpt-6-astra',
    ...extra,
    options: { spawned_by_session_id: parent, worker: { mode, task: `${mode} the export`, ...config } },
  })
  const describeAny = (row: AllAgentsRow) => row.kind === 'worker'
    ? `  ⚒ ${row.worker.view.role} ${row.worker.view.modelLabel} (${row.worker.view.status}${row.worker.view.verdict ? `, ${row.worker.view.verdict}` : ''})`
    : describeRow(row)
  const rows = (opts: Parameters<typeof buildAllAgentsTree>[0], state = { collapsed: new Set<string>(), expandedFinished: new Set<string>() }) =>
    flattenAllAgents(buildAllAgentsTree({ now: NOW, ...opts }), state)

  const lead = session('lead', { created_at: ago(200) })
  const builder = worker('b1', 'lead', 'build', { status: 'processing', created_at: ago(60) }, { branch: 'worker/export', review_rounds: 1, last_verdict: 'changes_requested' })
  const reviewer = worker('r1', 'lead', 'review', { created_at: ago(30) }, { reviews_session_id: 'b1' })

  it('hangs workers under the session that started them, which counts as Working', () => {
    const list = rows({ sessions: [reviewer, lead, builder], subagents: [], filter: 'all' })
    expect(list.map(describeAny)).toEqual([
      '# Working',
      'lead ▾',
      '  ⚒ build Sonnet 5.5 (working, changes_requested)',
      '  ⚒ review gpt-6-astra (idle, changes_requested)',
    ])
    const workerRows = list.filter(r => r.kind === 'worker')
    expect(workerRows.map(r => [r.key, r.kind === 'worker' && r.sessionId, r.kind === 'worker' && r.parentKey, r.kind === 'worker' && r.last])).toEqual([
      ['w:b1', 'lead', 's:lead', false],
      ['w:r1', 'lead', 's:lead', true],
    ])
  })

  it('Running shows only the workers that work; folding the parent hides them', () => {
    expect(rows({ sessions: [lead, builder, reviewer], subagents: [], filter: 'running' }).map(describeAny)).toEqual([
      '# Working', 'lead ▾', '  ⚒ build Sonnet 5.5 (working, changes_requested)',
    ])
    expect(rows({ sessions: [lead, builder, reviewer], subagents: [], filter: 'running' }, { collapsed: new Set(['lead']), expandedFinished: new Set() }).map(describeAny)).toEqual([
      '# Working', 'lead ▸',
    ])
    // Nobody works: the parent is merely recent, so Running leaves it out.
    const idle = { ...builder, status: 'idle' }
    expect(rows({ sessions: [lead, idle, reviewer], subagents: [], filter: 'running' })).toEqual([])
    expect(rows({ sessions: [lead, idle, reviewer], subagents: [], filter: 'all' }).map(describeAny)).toEqual([
      '# Recent', 'lead ▾', '  ⚒ build Sonnet 5.5 (idle, changes_requested)', '  ⚒ review gpt-6-astra (idle, changes_requested)',
    ])
  })

  it("puts a worker's own subagents under it, and they keep it working", () => {
    const idle = { ...builder, status: 'idle' }
    const list = rows({ sessions: [lead, idle], subagents: [sub('helper', 'b1'), sub('mine', 'lead', done(3))], filter: 'all' })
    expect(list.map(describeAny)).toEqual([
      '# Working',
      'lead ▾',
      '  ⚒ build Sonnet 5.5 (working, changes_requested)',
      '    helper (running)',
      '  +1 finished',
    ])
    const helper = list.find(r => r.kind === 'subagent')
    // The subagent belongs to the worker's session (that is where its output is loaded from).
    expect(helper).toMatchObject({ sessionId: 'b1', depth: 2, parentKey: 'w:b1' })
  })

  it('keeps loops above workers and subagents below', () => {
    const looping = session('lead', { scheduled_loop: { next_at: new Date(NOW + 600000).toISOString(), delay_seconds: 600, reason: 'check CI' } as any })
    const kinds = rows({ sessions: [looping, builder], subagents: [sub('x', 'lead')], filter: 'all' }).map(r => r.kind)
    expect(kinds).toEqual(['group', 'session', 'loop', 'worker', 'subagent'])
  })

  it('shows a worker as its own session when its parent is gone, ended, or it waits for the user', () => {
    expect(rows({ sessions: [builder], subagents: [], filter: 'all' }).map(describeAny)).toEqual(['# Working', 'b1'])
    const ended = { ...lead, status: 'ended' }
    expect(rows({ sessions: [ended, builder], subagents: [], filter: 'all' }).map(describeAny)).toEqual(['# Working', 'b1', '# Recent', 'lead'])
    const asking = { ...builder, pending_questions: 1 }
    expect(rows({ sessions: [lead, asking], subagents: [], filter: 'all' }).map(describeAny)).toEqual(['# Needs you', 'b1', '# Recent', 'lead'])
  })

  it('keeps an old parent in the list while a worker of it is recent', () => {
    const old = session('lead', { updated_at: ago(300) })
    const idle = { ...builder, status: 'idle', updated_at: ago(10) }
    expect(rows({ sessions: [old, idle], subagents: [], filter: 'all' }).map(describeAny)).toEqual([
      '# Recent', 'lead ▾', '  ⚒ build Sonnet 5.5 (idle, changes_requested)',
    ])
    const stale = { ...idle, updated_at: ago(300) }
    expect(rows({ sessions: [old, stale], subagents: [], filter: 'all' })).toEqual([])
  })

  it('Failed keeps a worker only for its failed subagents', () => {
    const failed = sub('boom', 'b1', { status: 'failed', completed_at: ago(2), error_message: 'exit status 1' })
    expect(rows({ sessions: [lead, builder, reviewer], subagents: [], filter: 'failed' })).toEqual([])
    expect(rows({ sessions: [lead, builder, reviewer], subagents: [failed], filter: 'failed' }).map(describeAny)).toEqual([
      '# Working', 'lead ▾', '  ⚒ build Sonnet 5.5 (working, changes_requested)', '    boom (failed)',
    ])
  })

  it('the text filter matches a worker by role, model, branch or task', () => {
    const find = (query: string) => rows({ sessions: [lead, builder, reviewer, session('other')], subagents: [], filter: 'all', query }).map(describeAny)
    expect(find('review worker')).toEqual(['# Working', 'lead ▾', '  ⚒ review gpt-6-astra (idle, changes_requested)'])
    expect(find('sonnet')).toEqual(['# Working', 'lead ▾', '  ⚒ build Sonnet 5.5 (working, changes_requested)'])
    expect(find('worker/export')).toEqual(['# Working', 'lead ▾', '  ⚒ build Sonnet 5.5 (working, changes_requested)'])
    expect(find('the export')).toHaveLength(4)
    // The session's own name keeps all its workers.
    expect(find('lead')).toHaveLength(4)
    expect(find('nothing-like-this')).toEqual([])
  })

  it('counts workers that work among the working sessions', () => {
    expect(allAgentsStats([lead, builder, reviewer], [], NOW).sessionsWorking).toBe(1)
  })
})

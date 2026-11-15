import { beforeEach, describe, expect, it } from 'vitest'
import {
  TEAM_SETUP_KEY,
  defaultTeamSetup,
  isTeamSession,
  isWorkerSession,
  loadTeamSetup,
  normalizeReviewRounds,
  reviewStepLabel,
  saveTeamSetup,
  showsTeamStrip,
  teamOptions,
  teamProgress,
  teamSetupProblem,
  workerNote,
  workerParentId,
  workerStatus,
  workersOf,
  type TeamSession,
} from '../../utils/agents/team'

const at = (minute: number) => new Date(Date.UTC(2026, 10, 14, 10, minute)).toISOString()

const parent: TeamSession = {
  id: 'parent',
  status: 'idle',
  created_at: at(0),
  options: {
    model: 'claude-fable-5-2',
    team: { builder: { provider: 'deepseek', model: 'deepseek-flash' }, reviewer: { provider: 'codex', model: 'gpt-6-astra' }, max_review_rounds: 4 },
  },
}

function builder(id: string, minute: number, extra: Partial<TeamSession> = {}, worker: Record<string, unknown> = {}): TeamSession {
  return {
    id,
    status: 'idle',
    created_at: at(minute),
    model_name: 'deepseek-flash',
    ...extra,
    options: {
      spawned_by_session_id: 'parent',
      worker: { task: 'build it', mode: 'build', branch: `worker/${id}`, base_branch: 'main', ...worker },
      ...extra.options,
    },
  }
}

function reviewer(id: string, minute: number, reviews: string | null, extra: Partial<TeamSession> = {}): TeamSession {
  return {
    id,
    status: 'idle',
    created_at: at(minute),
    model_name: 'gpt-6-astra',
    ...extra,
    options: {
      spawned_by_session_id: 'parent',
      read_only: true,
      worker: { task: 'review it', mode: 'review', ...(reviews ? { reviews_session_id: reviews } : {}) },
      ...extra.options,
    },
  }
}

describe('workersOf', () => {
  it('lists the workers a session started, oldest first', () => {
    const sessions = [
      reviewer('r1', 9, 'b1'),
      parent,
      builder('b1', 3),
      { id: 'other', status: 'idle', created_at: at(1) },
      // Started by another session.
      { ...builder('b9', 2), options: { spawned_by_session_id: 'someone-else', worker: { mode: 'build' } } },
      // A loop session the parent started is not a worker.
      { id: 'loop', status: 'processing', created_at: at(4), options: { spawned_by_session_id: 'parent' } },
    ]
    expect(workersOf(sessions, 'parent').map(s => s.id)).toEqual(['b1', 'r1'])
    expect(workersOf(sessions, 'someone-else').map(s => s.id)).toEqual(['b9'])
  })

  it('handles duplicates, a missing parent and empty lists', () => {
    const b = builder('b1', 3)
    expect(workersOf([b, b], 'parent').map(s => s.id)).toEqual(['b1'])
    expect(workersOf([b], '')).toEqual([])
    expect(workersOf(null, 'parent')).toEqual([])
    // A session never counts as its own worker.
    const self = { ...builder('parent', 1) }
    expect(workersOf([self], 'parent')).toEqual([])
  })

  it('breaks creation-time ties by id so the order is stable', () => {
    expect(workersOf([builder('b2', 3), builder('b1', 3)], 'parent').map(s => s.id)).toEqual(['b1', 'b2'])
  })
})

describe('worker basics', () => {
  it('needs both the worker settings and the session that started it', () => {
    expect(isWorkerSession(builder('b1', 1))).toBe(true)
    expect(workerParentId(builder('b1', 1))).toBe('parent')
    expect(isWorkerSession({ id: 'x', status: 'idle', options: { worker: { mode: 'build' } } })).toBe(false)
    expect(isWorkerSession({ id: 'x', status: 'idle', options: { spawned_by_session_id: 'parent' } })).toBe(false)
    expect(isWorkerSession({ id: 'x', status: 'idle' })).toBe(false)
    expect(workerParentId(parent)).toBe('')
    expect(isTeamSession(parent)).toBe(true)
    expect(isTeamSession(builder('b1', 1))).toBe(false)
  })

  it('counts working the way the sidebar does', () => {
    expect(workerStatus({ id: 'a', status: 'processing' })).toBe('working')
    expect(workerStatus({ id: 'a', status: 'idle' })).toBe('idle')
    expect(workerStatus({ id: 'a', status: 'ended', subagents_running: 2 })).toBe('ended')
    // Idle while its subagents carry on.
    expect(workerStatus({ id: 'a', status: 'idle', subagents_running: 1 })).toBe('working')
    // The live count wins over the listed one.
    expect(workerStatus({ id: 'a', status: 'idle', subagents_running: 1 }, 0)).toBe('idle')
    expect(workerStatus({ id: 'a', status: 'idle', limit_wait: { since: at(1), resume_at: at(30), why: 'turn' } as any })).toBe('waiting')
  })
})

describe('teamProgress', () => {
  it('is planning before any worker exists', () => {
    const p = teamProgress(parent, [])
    expect(p).toMatchObject({ stage: 'planning', step: 'plan', builder: null, reviewer: null, workers: [], round: 0, maxRounds: 4, lastVerdict: null })
    expect(reviewStepLabel(p)).toBe('Review')
  })

  it('falls back to 3 rounds without a team setting, and caps at 5', () => {
    expect(teamProgress({ id: 'p', status: 'idle' }, []).maxRounds).toBe(3)
    expect(teamProgress(null, []).maxRounds).toBe(3)
    expect(teamProgress(parent, [builder('b1', 1, {}, { max_review_rounds: 9 })]).maxRounds).toBe(5)
    // The builder's own cap wins over the team's.
    expect(teamProgress(parent, [builder('b1', 1, {}, { max_review_rounds: 2 })]).maxRounds).toBe(2)
  })

  it('is building while the builder works', () => {
    const p = teamProgress(parent, [builder('b1', 1, { status: 'processing' })])
    expect(p).toMatchObject({ stage: 'building', step: 'build', round: 0, lastVerdict: null })
    expect(p.builder).toMatchObject({ sessionId: 'b1', role: 'build', model: 'deepseek-flash', modelLabel: 'deepseek-flash', status: 'working', verdict: null, branch: 'worker/b1', current: true })
    expect(p.reviewer).toBeNull()
  })

  it('is idle on the build step when the builder is done and no review has started', () => {
    expect(teamProgress(parent, [builder('b1', 1)])).toMatchObject({ stage: 'idle', step: 'build', round: 0 })
  })

  it('is reviewing in the round after the ones with a verdict', () => {
    const first = teamProgress(parent, [builder('b1', 1), reviewer('r1', 5, 'b1', { status: 'processing' })])
    expect(first).toMatchObject({ stage: 'reviewing', step: 'review', round: 1, maxRounds: 4 })
    expect(first.reviewer).toMatchObject({ sessionId: 'r1', role: 'review', status: 'working', verdict: null, current: true })
    expect(reviewStepLabel(first)).toBe('Review (round 1 of 4)')

    const second = teamProgress(parent, [
      builder('b1', 1, {}, { review_rounds: 1, last_verdict: 'changes_requested', max_review_rounds: 4 }),
      reviewer('r1', 5, 'b1', { status: 'processing' }),
    ])
    expect(second).toMatchObject({ stage: 'reviewing', round: 2, lastVerdict: 'changes_requested' })
    // The verdict on the chip is the previous round's until the new one is in.
    expect(second.reviewer?.verdict).toBeNull()
  })

  it('never shows a round past the cap', () => {
    const p = teamProgress(parent, [
      builder('b1', 1, {}, { review_rounds: 4, last_verdict: 'changes_requested', max_review_rounds: 4 }),
      reviewer('r1', 5, 'b1', { status: 'processing' }),
    ])
    expect(p.round).toBe(4)
  })

  it('is fixing when the builder works after changes were requested', () => {
    const p = teamProgress(parent, [
      builder('b1', 1, { status: 'processing' }, { review_rounds: 1, last_verdict: 'changes_requested' }),
      reviewer('r1', 5, 'b1'),
    ])
    expect(p).toMatchObject({ stage: 'fixing', step: 'build', round: 1, lastVerdict: 'changes_requested' })
    expect(p.builder?.verdict).toBe('changes_requested')
    expect(p.reviewer?.verdict).toBe('changes_requested')
  })

  it('waits on the build step when findings are not sent back yet', () => {
    const p = teamProgress(parent, [
      builder('b1', 1, {}, { review_rounds: 1, last_verdict: 'changes_requested' }),
      reviewer('r1', 5, 'b1'),
    ])
    expect(p).toMatchObject({ stage: 'idle', step: 'build', round: 1 })
  })

  it('is approved once the review approves, also after the workers ended', () => {
    const workers = [
      builder('b1', 1, { status: 'ended' }, { review_rounds: 2, last_verdict: 'approve' }),
      reviewer('r1', 5, 'b1', { status: 'ended' }),
    ]
    const p = teamProgress(parent, workers)
    expect(p).toMatchObject({ stage: 'approved', step: 'merge', round: 2, lastVerdict: 'approve' })
    expect(p.builder?.status).toBe('ended')
    expect(p.reviewer?.verdict).toBe('approve')
  })

  it('is stopped when the last round still asks for changes', () => {
    const p = teamProgress(parent, [
      builder('b1', 1, {}, { review_rounds: 4, last_verdict: 'changes_requested', max_review_rounds: 4 }),
      reviewer('r1', 5, 'b1'),
    ])
    expect(p).toMatchObject({ stage: 'stopped', step: 'review', round: 4, maxRounds: 4 })
  })

  it('uses the newest builder and only the reviewers that review it', () => {
    const workers = workersOf([
      // The first attempt was approved, then a second builder was started.
      builder('b1', 1, { status: 'ended' }, { review_rounds: 1, last_verdict: 'approve' }),
      reviewer('r1', 3, 'b1', { status: 'processing' }),
      builder('b2', 6, { status: 'idle' }, { review_rounds: 1, last_verdict: 'changes_requested' }),
      reviewer('r2', 8, 'b2'),
      reviewer('r3', 9, 'b2'),
    ], 'parent')
    const p = teamProgress(parent, workers)
    // r1 still works, but it reviews the old builder: it does not make this "reviewing".
    expect(p.stage).toBe('idle')
    expect(p.builder?.sessionId).toBe('b2')
    expect(p.reviewer?.sessionId).toBe('r3')
    expect(p.lastVerdict).toBe('changes_requested')
    expect(p.workers.map(w => [w.sessionId, w.current])).toEqual([['b1', false], ['r1', false], ['b2', true], ['r2', false], ['r3', true]])
    // Only the newest reviewer of a builder carries its latest verdict.
    expect(p.workers.find(w => w.sessionId === 'r2')?.verdict).toBeNull()
    expect(p.workers.find(w => w.sessionId === 'r3')?.verdict).toBe('changes_requested')
    expect(p.workers.find(w => w.sessionId === 'b1')?.verdict).toBe('approve')
  })

  it('a new builder without a reviewer yet starts over at the build step', () => {
    const p = teamProgress(parent, [
      builder('b1', 1, {}, { review_rounds: 2, last_verdict: 'approve' }),
      reviewer('r1', 3, 'b1'),
      builder('b2', 6, { status: 'processing' }),
    ])
    expect(p).toMatchObject({ stage: 'building', step: 'build', round: 0, lastVerdict: null })
    expect(p.reviewer).toBeNull()
  })

  it('handles a review worker without a builder', () => {
    const p = teamProgress({ id: 'parent', status: 'idle' }, [reviewer('r1', 2, null, { status: 'processing' })])
    expect(p).toMatchObject({ stage: 'reviewing', step: 'review', builder: null, round: 1, maxRounds: 3 })
    expect(p.reviewer?.sessionId).toBe('r1')
    const done = teamProgress({ id: 'parent', status: 'idle' }, [reviewer('r1', 2, null)])
    expect(done).toMatchObject({ stage: 'idle', step: 'review' })
    expect(done.reviewer?.verdict).toBeNull()
  })

  it('labels models with the shared model labels', () => {
    const p = teamProgress(parent, [builder('b1', 1, { model_name: 'claude-sonnet-5-5' }), reviewer('r1', 2, 'b1', { model_name: '', options: { model: 'claude-haiku-4-5-20251001' } as any })])
    expect(p.builder?.modelLabel).toBe('Sonnet 5.5')
    // options.model wins over model_name.
    expect(p.reviewer?.modelLabel).toBe('Haiku 4.5')
  })

  it('ignores unknown verdicts', () => {
    expect(teamProgress(parent, [builder('b1', 1, {}, { last_verdict: 'maybe' })]).lastVerdict).toBeNull()
  })
})

describe('strip and worker note', () => {
  it('shows the strip for a team, or any session with workers, never for a worker', () => {
    expect(showsTeamStrip(parent, [])).toBe(true)
    expect(showsTeamStrip({ id: 'p', status: 'idle' }, [])).toBe(false)
    expect(showsTeamStrip({ id: 'p', status: 'idle' }, [builder('b1', 1)])).toBe(true)
    expect(showsTeamStrip(builder('b1', 1), [])).toBe(false)
    expect(showsTeamStrip(null, [])).toBe(false)
  })

  it('writes the worker note', () => {
    expect(workerNote(builder('b1', 1), 'Mittens')).toEqual({ lead: 'Build worker of', parentName: 'Mittens', detail: 'branch worker/b1' })
    expect(workerNote(builder('b1', 1, {}, { branch: '' }), 'Mittens')).toEqual({ lead: 'Build worker of', parentName: 'Mittens', detail: '' })
    expect(workerNote(reviewer('r1', 1, 'b1'), 'Mittens')).toEqual({ lead: 'Review worker of', parentName: 'Mittens', detail: 'read-only' })
    expect(workerNote(reviewer('r1', 1, 'b1'), '')?.parentName).toBe('Session parent')
    expect(workerNote(parent, 'x')).toBeNull()
  })
})

describe('team setup (Create Session)', () => {
  beforeEach(() => localStorage.clear())

  it('keeps review rounds between 1 and 5, default 3', () => {
    expect(normalizeReviewRounds(undefined)).toBe(3)
    expect(normalizeReviewRounds('')).toBe(3)
    expect(normalizeReviewRounds(0)).toBe(3)
    expect(normalizeReviewRounds(-2)).toBe(3)
    expect(normalizeReviewRounds(1)).toBe(1)
    expect(normalizeReviewRounds('4')).toBe(4)
    expect(normalizeReviewRounds(2.9)).toBe(2)
    expect(normalizeReviewRounds(12)).toBe(5)
  })

  it('remembers the last setup', () => {
    expect(loadTeamSetup()).toEqual(defaultTeamSetup())
    const setup = { enabled: true, builder: { provider: 'deepseek', model: 'deepseek-flash' }, reviewer: { provider: 'codex', model: 'gpt-6-astra' }, maxReviewRounds: 2 }
    saveTeamSetup(setup)
    expect(JSON.parse(localStorage.getItem(TEAM_SETUP_KEY)!)).toEqual(setup)
    expect(loadTeamSetup()).toEqual(setup)
  })

  it('survives broken or foreign stored values', () => {
    localStorage.setItem(TEAM_SETUP_KEY, '{not json')
    expect(loadTeamSetup()).toEqual(defaultTeamSetup())
    localStorage.setItem(TEAM_SETUP_KEY, JSON.stringify({ enabled: 'yes', builder: 'x', reviewer: { provider: 7, model: ' m ' }, maxReviewRounds: 99 }))
    expect(loadTeamSetup()).toEqual({ enabled: false, builder: { provider: '', model: '' }, reviewer: { provider: '', model: 'm' }, maxReviewRounds: 5 })
    localStorage.setItem(TEAM_SETUP_KEY, '[]')
    expect(loadTeamSetup()).toEqual(defaultTeamSetup())
  })

  it('builds options.team only for a complete, enabled setup', () => {
    const setup = { enabled: true, builder: { provider: 'deepseek', model: 'deepseek-flash' }, reviewer: { provider: 'codex', model: 'gpt-6-astra' }, maxReviewRounds: 9 }
    expect(teamOptions(setup)).toEqual({ builder: setup.builder, reviewer: setup.reviewer, max_review_rounds: 5 })
    expect(teamSetupProblem(setup)).toBe('')
    expect(teamOptions({ ...setup, enabled: false })).toBeNull()
    expect(teamSetupProblem({ ...setup, enabled: false })).toBe('')
    expect(teamOptions({ ...setup, builder: { provider: 'deepseek', model: '' } })).toBeNull()
    expect(teamSetupProblem({ ...setup, builder: { provider: 'deepseek', model: '' } })).toBe('Pick a model for the builder.')
    expect(teamSetupProblem({ ...setup, reviewer: { provider: '', model: 'x' } })).toBe('Pick a model for the reviewer.')
    expect(teamOptions(null)).toBeNull()
  })
})

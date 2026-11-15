import { buildScene, buildCues, countdown, noteTransitions, CHEER_MS, SWEAT_AFTER_MS, STAGE_SEATS, type NotchSession, type Recent } from '../../../utils/notch/notchScene'
import { isNotchPath } from '../../../utils/notch/notchRoute'

const NOW = Date.parse('2026-11-07T12:00:00Z')
const ago = (ms: number) => new Date(NOW - ms).toISOString()
const fresh = (): Recent => ({ doneAt: {}, failedAt: {} })
const s = (id: string, extra: Partial<NotchSession> = {}): NotchSession => ({ id, status: 'idle', updated_at: ago(60_000), ...extra })

describe('notch scene', () => {
  it('sleeps with nothing running and ignores ended sessions', () => {
    const scene = buildScene([s('a'), s('b', { status: 'ended', pending_permissions: 1 })], [], [], fresh(), NOW)
    expect(scene.mood).toBe('asleep')
    expect(scene.stage).toEqual([])
    expect(scene.headline).toMatch(/resting, not dead/i)
  })

  it('puts a working session on stage and makes it sweat on a long run', () => {
    const scene = buildScene([
      s('quick', { status: 'processing', updated_at: ago(2 * 60_000) }),
      s('long', { status: 'processing', updated_at: ago(SWEAT_AFTER_MS + 1000), subagents_running: 2 }),
    ], [], [], fresh(), NOW)
    expect(scene.mood).toBe('working')
    expect(scene.badge).toBe('2')
    const long = scene.actors.find(a => a.id === 'long')!
    expect(long.state).toBe('sweating')
    expect(long.subagents).toBe(2)
    expect(long.line).toContain('2 subagents')
    expect(scene.actors.find(a => a.id === 'quick')!.line).toBe('Working · 2 min')
  })

  it('an idle session whose subagents still work counts as working', () => {
    const scene = buildScene([s('a', { subagents_running: 1 })], [], [], fresh(), NOW)
    expect(scene.mood).toBe('working')
    expect(scene.actors[0]!.line).toBe('Subagents working · 1')
  })

  it('needs you wins and turns the pending requests into cue cards, oldest first', () => {
    const scene = buildScene(
      [s('w', { status: 'processing' }), s('n', { status: 'processing', pending_permissions: 1 })],
      [{ session_id: 'n', session_name: 'Builder', permission_id: 'p1', tool_name: 'Bash', details: { command: 'git push origin main' }, asked_at: ago(5000) }],
      [{ session_id: 'w', question_id: 'q1', question: 'Which DB?', options: [{ label: 'SQLite (Recommended)' }, { label: 'Postgres' }], recommended: [0], asked_at: ago(9000) }],
      fresh(), NOW,
    )
    expect(scene.mood).toBe('needs')
    expect(scene.cues.map(c => c.key)).toEqual(['q:q1', 'p:p1'])
    expect(scene.cues[1]!.title).toBe('Allow git push origin main?')
    expect(scene.cues[0]!.options).toEqual([{ label: 'SQLite (Recommended)', recommended: true }, { label: 'Postgres', recommended: false }])
    // Both sessions wait for the user now.
    expect(scene.actors.every(a => a.state === 'needs')).toBe(true)
  })

  it('leaves multi-select, loop proposals and confirmation cards to the main window', () => {
    const base = { session_id: 'a', question: 'q', options: [{ label: 'x' }] }
    const cues = buildCues([], [
      { ...base, question_id: 'multi', multi_select: true },
      { ...base, question_id: 'loop', kind: 'loop_session' },
      { ...base, question_id: 'confirm', requires_user: true },
      { ...base, question_id: 'plain' },
    ])
    expect(Object.fromEntries(cues.map(c => [c.question!.question_id, c.openInApp]))).toEqual({ multi: true, loop: true, confirm: true, plain: false })
    expect(cues.find(c => c.question!.question_id === 'loop')!.title).toBe('Start a loop session?')
  })

  it('drops cues for sessions it does not know (another user\'s, or ended)', () => {
    const scene = buildScene([s('mine')], [{ session_id: 'other', permission_id: 'p', tool_name: 'Edit' }], [], fresh(), NOW)
    expect(scene.cues).toEqual([])
    expect(scene.mood).toBe('asleep')
  })

  it('bows when a turn ends and cracks when it fails, then settles', () => {
    const recent = fresh()
    const before = new Map([['ok', { status: 'processing' }], ['bad', { status: 'processing' }]])
    const { done, failed } = noteTransitions(before, [s('ok'), s('bad', { error_message: 'boom' })], recent, NOW)
    expect(done).toEqual(['ok'])
    expect(failed).toEqual(['bad'])
    let scene = buildScene([s('ok'), s('bad', { error_message: 'boom' })], [], [], recent, NOW + 1000)
    expect(scene.mood).toBe('oops')
    expect(scene.actors.map(a => a.state)).toEqual(['cracked', 'bowing'])
    scene = buildScene([s('ok')], [], [], recent, NOW + CHEER_MS + 1)
    expect(scene.mood).toBe('asleep')
    noteTransitions(new Map(), [], recent, NOW + CHEER_MS + 1)
    expect(recent).toEqual(fresh())
  })

  it('shows an intermission for a usage-limit wait, with its countdown', () => {
    const scene = buildScene([s('a', { limit_wait: { why: 'turn', since: ago(1000), resume_at: new Date(NOW + 121_000).toISOString() } })], [], [], fresh(), NOW)
    expect(scene.mood).toBe('intermission')
    expect(scene.badge).toBe('⏸ 02:01')
  })

  it('a scheduled /loop closes the curtains with the next show time', () => {
    const scene = buildScene([s('a', { scheduled_loop: { next_at: new Date(NOW + 14 * 60_000).toISOString(), delay_seconds: 840 } })], [], [], fresh(), NOW)
    expect(scene.mood).toBe('loop')
    expect(scene.badge).toBe('↻ 14m')
    expect(scene.headline).toBe('Next show in 14 min')
  })

  it('seats at most three eggs and counts the rest', () => {
    const many = Array.from({ length: 5 }, (_, i) => s(`s${i}`, { status: 'processing' }))
    const scene = buildScene(many, [], [], fresh(), NOW)
    expect(scene.stage).toHaveLength(STAGE_SEATS)
    expect(scene.extra).toBe(2)
  })

  it('names sessions like the sidebar does', () => {
    const scene = buildScene([
      s('a', { status: 'processing', options: { agent_name: 'Fixer' } }),
      s('b', { status: 'processing', options: { working_directory: '/Users/x/projects/eggify' } }),
    ], [], [], fresh(), NOW)
    expect(scene.actors.map(a => a.name).sort()).toEqual(['Fixer', 'eggify'])
  })

  it('formats countdowns', () => {
    expect(countdown(NOW + 5_000, NOW)).toBe('00:05')
    expect(countdown(NOW + 3_725_000, NOW)).toBe('1:02')
    expect(countdown(NOW - 1, NOW)).toBe('now')
  })

  it('knows its route', () => {
    expect(isNotchPath('/notch')).toBe(true)
    expect(isNotchPath('/notch/')).toBe(true)
    expect(isNotchPath('/agents')).toBe(false)
  })
})

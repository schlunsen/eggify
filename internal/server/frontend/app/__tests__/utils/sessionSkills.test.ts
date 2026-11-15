import {
  activeSkillCount, addedSkillIds, canRemoveSkill, composerWithSkill, filterSessionSkills, groupSessionSkills,
  nextAddedSkillIds, parseSessionSkills, reachLabel, sessionSkillsNote, skillSlashText, type SessionSkill,
} from '../../utils/agents/sessionSkills'

const skill = (over: Partial<SessionSkill>): SessionSkill => ({
  id: 'id', name: 'name', scope: 'personal', user_invocable: true, reach: 'everywhere', added: false, ...over,
})

const skills: SessionSkill[] = [
  skill({ id: 'a', name: 'commit', description: 'Write a commit message' }),
  skill({ id: 'b', name: 'deploy', scope: 'project', reach: 'folder' }),
  skill({ id: 'c', name: 'review', scope: 'project', reach: 'added', added: true }),
  skill({ id: 'd', name: 'release', scope: 'project', reach: '', description: 'Ship the desktop app' }),
  skill({ id: 'e', name: 'triage', scope: 'project', reach: '' }),
]

describe('parseSessionSkills', () => {
  it('reads the server answer and drops malformed rows', () => {
    const state = parseSessionSkills({
      session_id: 's1', runtime: 'claude-sdk', restart_pending: true, subagents_running: 2,
      skills: [{ id: 'a', name: 'commit', scope: 'personal', user_invocable: true, reach: 'everywhere', added: false }, { name: 'no-id' }, null],
    })
    expect(state).toEqual({
      session_id: 's1', runtime: 'claude-sdk', restart_pending: true, subagents_running: 2,
      skills: [{ id: 'a', name: 'commit', description: undefined, scope: 'personal', argument_hint: undefined, user_invocable: true, reach: 'everywhere', added: false }],
    })
  })

  it('falls back to empty values for a missing or odd body', () => {
    expect(parseSessionSkills({}, 's2')).toEqual({ session_id: 's2', runtime: '', skills: [], restart_pending: false, subagents_running: 0 })
    expect(parseSessionSkills(null).skills).toEqual([])
    expect(parseSessionSkills({ runtime: 'weird', skills: [{ id: 'x', name: 'x', reach: 'mars' }] }).skills[0]!.reach).toBe('')
  })
})

describe('grouping and labels', () => {
  it('splits skills the session gets from other projects\' skills', () => {
    const { active, other } = groupSessionSkills(skills)
    expect(active.map(s => s.name)).toEqual(['commit', 'deploy', 'review'])
    expect(other.map(s => s.name)).toEqual(['release', 'triage'])
    expect(activeSkillCount(skills)).toBe(3)
    expect(activeSkillCount(null)).toBe(0)
  })

  it('says why the session gets a skill', () => {
    expect(reachLabel('everywhere')).toBe('Everywhere')
    expect(reachLabel('folder')).toBe('This project')
    expect(reachLabel('added')).toBe('Added')
    expect(reachLabel('')).toBe('')
  })

  it('lets only added skills be removed', () => {
    expect(skills.filter(canRemoveSkill).map(s => s.name)).toEqual(['review'])
    // Added but applying on its own anyway: removing would change nothing
    expect(canRemoveSkill(skill({ added: true, reach: 'everywhere' }))).toBe(false)
  })

  it('searches names and descriptions', () => {
    expect(filterSessionSkills(skills, 'DESKTOP').map(s => s.name)).toEqual(['release'])
    expect(filterSessionSkills(skills, 'comm').map(s => s.name)).toEqual(['commit'])
    expect(filterSessionSkills(skills, '  ')).toHaveLength(skills.length)
  })
})

describe('the added list', () => {
  it('sends the full list with one skill added or removed', () => {
    const withExtra = [...skills, skill({ id: 'f', name: 'old', reach: 'everywhere', added: true })]
    expect(addedSkillIds(withExtra)).toEqual(['c', 'f'])
    expect(nextAddedSkillIds(withExtra, 'd', true)).toEqual(['c', 'f', 'd'])
    expect(nextAddedSkillIds(withExtra, 'c', true)).toEqual(['c', 'f'])
    expect(nextAddedSkillIds(withExtra, 'c', false)).toEqual(['f'])
    expect(nextAddedSkillIds(skills, 'c', false)).toEqual([])
  })
})

describe('sessionSkillsNote', () => {
  it('says a change reaches the Claude CLI with the next message', () => {
    expect(sessionSkillsNote({ runtime: 'claude-sdk', restart_pending: true, subagents_running: 0 })).toBe('Takes effect with your next message.')
    expect(sessionSkillsNote({ runtime: 'claude-sdk', restart_pending: true, subagents_running: 1 }))
      .toBe('Takes effect with your next message. The agent restarts then, which stops 1 running subagent.')
    expect(sessionSkillsNote({ runtime: 'claude-sdk', restart_pending: true, subagents_running: 3 })).toContain('stops 3 running subagents.')
    expect(sessionSkillsNote({ runtime: 'claude-sdk', restart_pending: false, subagents_running: 3 })).toBe('')
  })

  it('explains how Codex and direct sessions see skills', () => {
    for (const runtime of ['codex', 'direct'] as const) {
      expect(sessionSkillsNote({ runtime, restart_pending: false, subagents_running: 0 })).toContain('through its skill tools')
    }
    expect(sessionSkillsNote({ runtime: '', restart_pending: true, subagents_running: 0 })).toBe('')
    expect(sessionSkillsNote(null)).toBe('')
  })
})

describe('Use', () => {
  it('types the skill as a slash command', () => {
    expect(skillSlashText('commit')).toBe('/commit ')
    expect(composerWithSkill('', 'commit')).toBe('/commit ')
    expect(composerWithSkill('fix the tests', 'commit')).toBe('/commit fix the tests')
    expect(composerWithSkill('/review 12', 'commit')).toBe('/commit 12')
    expect(composerWithSkill('/review', 'commit')).toBe('/commit ')
  })
})

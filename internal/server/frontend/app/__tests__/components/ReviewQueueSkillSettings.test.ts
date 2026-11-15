import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ReviewQueue from '~/components/review/ReviewQueue.vue'
import { skillSettings, skillToQueueItem } from '~/types/review'
import type { PendingSkillItem } from '~/types/review'

// gstack's "careful" skill: a PreToolUse hook that runs a script on every Bash call.
const carefulFrontmatter = JSON.stringify({
  name: 'careful',
  description: 'Safety guardrails',
  version: '0.1.0',
  'allowed-tools': ['Bash', 'Read'],
  hooks: {
    PreToolUse: [{ matcher: 'Bash', hooks: [{ type: 'command', command: 'bash ${CLAUDE_SKILL_DIR}/bin/check-careful.sh' }] }]
  }
})

describe('skillSettings', () => {
  it('lists hooks first with their commands, then allowed-tools, then the rest', () => {
    const s = skillSettings(carefulFrontmatter)
    // Risky settings first (in frontmatter order), then the rest.
    expect(s.map(x => x.key)).toEqual(['allowed-tools', 'hooks', 'version'])
    const hooks = s.find(x => x.key === 'hooks')!
    expect(hooks.risky).toBe(true)
    expect(hooks.commands).toEqual(['bash ${CLAUDE_SKILL_DIR}/bin/check-careful.sh'])
    expect(s.find(x => x.key === 'allowed-tools')!.value).toBe('Bash, Read')
    expect(s.find(x => x.key === 'version')!.risky).toBe(false)
    expect(s.some(x => x.key === 'name' || x.key === 'description')).toBe(false)
  })

  it('never hides unreadable frontmatter', () => {
    const s = skillSettings('{not json')
    expect(s).toHaveLength(1)
    expect(s[0].risky).toBe(true)
  })

  it('is empty without frontmatter', () => {
    expect(skillSettings(undefined)).toEqual([])
    expect(skillSettings('{"name":"x","description":"y"}')).toEqual([])
  })
})

describe('ReviewQueue skill settings', () => {
  it('shows the hook command to the reviewer', () => {
    const pending: PendingSkillItem = {
      skill: {
        id: 's1', name: 'careful', description: 'Safety guardrails', scope: 'personal', body: '# careful',
        frontmatter_json: carefulFrontmatter, created_by: 'file:/Users/me/.claude/skills/careful/SKILL.md',
        status: 'pending', created_at: '2026-10-04T10:00:00Z'
      },
      created_by_session_id: '',
      agent_authored: false
    }
    const w = mount(ReviewQueue, { props: { kind: 'skill', items: [skillToQueueItem(pending)] } })
    expect(w.findAll('[data-test="rq-setting-risky"]').length).toBe(2)
    expect(w.find('[data-test="rq-hook-command"]').text()).toContain('check-careful.sh')
  })
})

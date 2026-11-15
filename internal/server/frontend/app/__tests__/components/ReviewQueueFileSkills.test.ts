import { mount } from '@vue/test-utils'
import { setHomeDir } from '../../utils/homeDir'
import ReviewQueue from '../../components/review/ReviewQueue.vue'
import {
  describeCreator, groupFileItemsByFolder, queueOrigins, shortenHome, skillSourceFolder, skillToQueueItem
} from '../../types/review'
import type { ReviewQueueItem } from '../../types/review'

function fileSkill(id: string, path: string, body = 'Step 1'): ReviewQueueItem {
  return skillToQueueItem({
    skill: {
      id, name: id, description: `${id} skill`, scope: 'personal', body,
      created_by: `file:${path}`, status: 'pending', created_at: '2026-10-01T10:00:00Z'
    },
    created_by_session_id: '',
    agent_authored: false
  })
}

const agentSkill: ReviewQueueItem = skillToQueueItem({
  skill: {
    id: 'a1', name: 'from-session', description: 'd', scope: 'personal', body: 'b',
    created_by: 'agent:abc', status: 'pending', created_at: '2026-10-01T10:00:00Z'
  },
  created_by_session_id: 'abc',
  agent_authored: true
})

const careful = fileSkill('careful', '/Users/me/.claude/skills/careful/SKILL.md')
const ship = fileSkill('ship', '/Users/me/.claude/skills/ship/SKILL.md')
const morning = fileSkill('morning', '/Users/me/.claude/skills/synced/uuid-1/morning/SKILL.md')
const project = fileSkill('helper', '/home/dev/repo/.claude/skills/helper/SKILL.md')

beforeEach(() => setHomeDir('/Users/me'))

describe('file skill labels', () => {
  it('shortens the home folder', () => {
    expect(shortenHome('/Users/me/.claude/skills/x/SKILL.md')).toBe('~/.claude/skills/x/SKILL.md')
    expect(shortenHome('/home/dev/repo')).toBe('/home/dev/repo')
    expect(shortenHome('/Users/me')).toBe('~')
    expect(shortenHome('/opt/skills/x')).toBe('/opt/skills/x')
    expect(shortenHome('/srv/me/x', '/srv/me')).toBe('~/x')
    expect(shortenHome('/srv/meow/x', '/srv/me')).toBe('/srv/meow/x')
  })

  it('labels discovered skills by their file', () => {
    expect(describeCreator('file:/Users/me/.claude/skills/careful/SKILL.md')).toBe('From ~/.claude/skills/careful/SKILL.md')
    expect(describeCreator('agent:abc')).toBe('Agent session')
  })

  it('groups file skills by the folder they were found in', () => {
    expect(skillSourceFolder(careful.createdBy)).toBe('/Users/me/.claude/skills')
    expect(skillSourceFolder(agentSkill.createdBy)).toBeNull()
    const items = [careful, agentSkill, morning, ship, project]
    expect(groupFileItemsByFolder(items)).toEqual([{ folder: '/Users/me/.claude/skills', ids: ['careful', 'ship'] }])
    expect(groupFileItemsByFolder(items, 1).map(g => g.folder)).toEqual([
      '/Users/me/.claude/skills', '/Users/me/.claude/skills/synced/uuid-1', '/home/dev/repo/.claude/skills'
    ])
  })

  it('tells where a queue came from', () => {
    expect(queueOrigins([agentSkill])).toBe('agent')
    expect(queueOrigins([careful, ship])).toBe('file')
    expect(queueOrigins([careful, agentSkill])).toBe('mixed')
  })
})

describe('ReviewQueue with discovered skills', () => {
  it('shows "From <path>" and a header that matches the origins', () => {
    const files = mount(ReviewQueue, { props: { kind: 'skill', items: [careful, ship] } })
    expect(files.text()).toContain('From ~/.claude/skills/careful/SKILL.md')
    expect(files.find('[data-test="rq-hint"]').text()).toContain('found in SKILL.md files')
    expect(files.find('[data-test="rq-hint"]').text()).not.toContain('An agent wrote these')

    const mixed = mount(ReviewQueue, { props: { kind: 'skill', items: [careful, agentSkill] } })
    expect(mixed.find('[data-test="rq-hint"]').text()).toContain('Some were written by an agent')

    const agents = mount(ReviewQueue, { props: { kind: 'skill', items: [agentSkill] } })
    expect(agents.find('[data-test="rq-hint"]').text()).toContain('An agent wrote these')
  })

  it('approves all skills from one folder after a confirmation', async () => {
    const wrapper = mount(ReviewQueue, { props: { kind: 'skill', items: [careful, agentSkill, ship, morning] } })
    const groups = wrapper.findAll('[data-test="rq-folder"]')
    expect(groups).toHaveLength(1)
    expect(groups[0]!.text()).toContain('~/.claude/skills')
    expect(groups[0]!.text()).toContain('2 skills')

    await wrapper.find('[data-test="rq-approve-folder"]').trigger('click')
    expect(wrapper.emitted('approve-many')).toBeUndefined()
    await wrapper.find('[data-test="rq-approve-folder-cancel"]').trigger('click')
    expect(wrapper.find('[data-test="rq-approve-folder-confirm"]').exists()).toBe(false)

    await wrapper.find('[data-test="rq-approve-folder"]').trigger('click')
    await wrapper.find('[data-test="rq-approve-folder-confirm"]').trigger('click')
    expect(wrapper.emitted('approve-many')).toEqual([[['careful', 'ship'], '/Users/me/.claude/skills']])
    // Reject stays per item.
    expect(wrapper.findAll('[data-test="rq-reject"]')).toHaveLength(4)
  })

  it('has no folder actions for memories or single files', () => {
    const one = mount(ReviewQueue, { props: { kind: 'skill', items: [careful, agentSkill] } })
    expect(one.find('[data-test="rq-folders"]').exists()).toBe(false)
  })

  it('collapses long bodies to their first 12 lines', async () => {
    const body = Array.from({ length: 30 }, (_, i) => `line ${i + 1}`).join('\n')
    const wrapper = mount(ReviewQueue, { props: { kind: 'skill', items: [fileSkill('long', '/Users/me/.claude/skills/long/SKILL.md', body)] } })
    const content = () => wrapper.find('[data-test="rq-content"]').text()
    expect(content()).toContain('line 12')
    expect(content()).not.toContain('line 13')
    const toggle = wrapper.find('[data-test="rq-toggle-content"]')
    expect(toggle.text()).toBe('Show all (30 lines)')
    await toggle.trigger('click')
    expect(content()).toContain('line 30')
    expect(wrapper.find('[data-test="rq-toggle-content"]').text()).toBe('Show less')

    const short = mount(ReviewQueue, { props: { kind: 'skill', items: [careful] } })
    expect(short.find('[data-test="rq-toggle-content"]').exists()).toBe(false)
  })
})

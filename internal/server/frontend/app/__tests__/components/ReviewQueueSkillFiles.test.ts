import { mount } from '@vue/test-utils'
import ReviewQueue from '../../components/review/ReviewQueue.vue'
import { formatBytes, skillToQueueItem } from '../../types/review'
import type { PendingSkillItem } from '../../types/review'

function pending(folder?: PendingSkillItem['folder']): PendingSkillItem {
  return {
    skill: {
      id: 'release', name: 'release', description: 'Cut a release', scope: 'personal', body: 'Run scripts/release.sh',
      created_by: 'file:/Users/me/.claude/skills/release/SKILL.md', status: 'pending', created_at: '2026-10-01T10:00:00Z'
    },
    created_by_session_id: '',
    agent_authored: false,
    folder
  }
}

describe('review queue: files of a skill folder', () => {
  it('maps the folder onto the queue item', () => {
    const item = skillToQueueItem(pending({
      files: [{ path: 'scripts/release.sh', size: 2048, executable: true }],
      total_bytes: 2048
    }))
    expect(item.files).toHaveLength(1)
    expect(item.filesTotalBytes).toBe(2048)
    expect(skillToQueueItem(pending()).files).toBeUndefined()
  })

  it('formats sizes', () => {
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(3 * 1024 * 1024)).toBe('3.0 MB')
  })

  it('lists every file with its size, marks scripts and links', () => {
    const item = skillToQueueItem(pending({
      files: [
        { path: 'scripts/release.sh', size: 2048, executable: true },
        { path: 'templates/notes.md', size: 100 },
        { path: 'hosts', size: 0, symlink: '/etc/hosts' }
      ],
      total_bytes: 2148
    }))
    const w = mount(ReviewQueue, { props: { kind: 'skill', items: [item] } })
    const rows = w.findAll('[data-test="rq-file"]')
    expect(rows).toHaveLength(3)
    expect(rows[0]!.text()).toContain('scripts/release.sh')
    expect(rows[0]!.text()).toContain('2.0 KB')
    expect(rows[0]!.find('[data-test="rq-file-exec"]').exists()).toBe(true)
    expect(rows[1]!.find('[data-test="rq-file-exec"]').exists()).toBe(false)
    expect(rows[2]!.text()).toContain('link to /etc/hosts (not copied)')
    expect(w.find('[data-test="rq-files"]').text()).toContain('Files in this skill (3, 2.1 KB)')
  })

  it('says when there is nothing besides SKILL.md, and shows problems', () => {
    const empty = mount(ReviewQueue, { props: { kind: 'skill', items: [skillToQueueItem(pending({ files: [], total_bytes: 0 }))] } })
    expect(empty.find('[data-test="rq-files"]').text()).toContain('No other files besides SKILL.md')

    const big = mount(ReviewQueue, {
      props: { kind: 'skill', items: [skillToQueueItem(pending({ files: [], total_bytes: 0, over_limit: true, problem: 'the skill folder has more than 256 files' }))] }
    })
    expect(big.find('[data-test="rq-files-problem"]').text()).toContain('more than 256 files')
  })

  it('shows no file list for skills without a folder', () => {
    const w = mount(ReviewQueue, { props: { kind: 'skill', items: [skillToQueueItem(pending())] } })
    expect(w.find('[data-test="rq-files"]').exists()).toBe(false)
  })
})

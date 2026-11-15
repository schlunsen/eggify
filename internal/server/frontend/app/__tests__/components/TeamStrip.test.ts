import { mount } from '@vue/test-utils'
import TeamStrip from '../../components/agents/TeamStrip.vue'
import WorkerNote from '../../components/agents/WorkerNote.vue'
import { teamProgress, workerNote, type TeamSession } from '../../utils/agents/team'

const at = (minute: number) => new Date(Date.UTC(2026, 10, 14, 10, minute)).toISOString()

const parent: TeamSession = {
  id: 'parent', status: 'idle', created_at: at(0),
  options: { team: { builder: { provider: 'claude', model: 'claude-sonnet-5-5' }, reviewer: { provider: 'codex', model: 'gpt-6-astra' }, max_review_rounds: 3 } },
}
const builder = (status: string, worker: Record<string, unknown> = {}): TeamSession => ({
  id: 'b1', status, created_at: at(1), model_name: 'claude-sonnet-5-5',
  options: { spawned_by_session_id: 'parent', worker: { mode: 'build', branch: 'worker/export', ...worker } },
})
const reviewer = (status: string): TeamSession => ({
  id: 'r1', status, created_at: at(5), model_name: 'gpt-6-astra',
  options: { spawned_by_session_id: 'parent', read_only: true, worker: { mode: 'review', reviews_session_id: 'b1' } },
})

const strip = (workers: TeamSession[]) => mount(TeamStrip, { props: { progress: teamProgress(parent, workers) } })
const currentStep = (wrapper: ReturnType<typeof strip>) => wrapper.get('[aria-current="step"]')

describe('TeamStrip', () => {
  it('marks Plan while there are no workers', () => {
    const wrapper = strip([])
    expect(wrapper.findAll('.team-step').map(s => s.text().replace('→', '').trim())).toEqual(['Plan', 'Build', 'Review', 'Merge'])
    expect(currentStep(wrapper).attributes('data-step')).toBe('plan')
    expect(wrapper.get('[data-testid="team-stage"]').text()).toBe('Planning')
    expect(wrapper.findAll('[data-testid="team-worker-chip"]')).toHaveLength(0)
  })

  it('marks Build with the builder chip while it works', () => {
    const wrapper = strip([builder('processing')])
    expect(currentStep(wrapper).attributes('data-step')).toBe('build')
    expect(wrapper.get('[data-step="plan"]').classes()).toContain('done')
    const chip = wrapper.get('[data-testid="team-worker-chip"]')
    expect(chip.element.tagName).toBe('BUTTON')
    expect(chip.text()).toContain('Build')
    expect(chip.text()).toContain('Sonnet 5.5')
    expect(chip.classes()).toContain('status-working')
    expect(chip.attributes('aria-label')).toBe('Open build worker · Sonnet 5.5 · Working · branch worker/export')
  })

  it('shows the review round and the verdict on the reviewer', () => {
    const reviewing = strip([builder('idle'), reviewer('processing')])
    expect(currentStep(reviewing).text()).toContain('Review (round 1 of 3)')
    expect(reviewing.get('[data-testid="team-stage"]').text()).toBe('Reviewing')
    expect(reviewing.find('.team-verdict').exists()).toBe(false)

    const fixing = strip([builder('processing', { review_rounds: 1, last_verdict: 'changes_requested' }), reviewer('idle')])
    expect(currentStep(fixing).attributes('data-step')).toBe('build')
    expect(fixing.get('[data-testid="team-stage"]').text()).toBe('Fixing review findings')
    const chips = fixing.findAll('[data-testid="team-worker-chip"]')
    // The verdict sits on the reviewer's chip only.
    expect(chips[0]!.find('.team-verdict').exists()).toBe(false)
    expect(chips[1]!.get('.team-verdict').text()).toBe('Changes requested')
    expect(chips[1]!.attributes('aria-label')).toBe('Open review worker · gpt-6-astra · Idle · Changes requested')
  })

  it('marks Merge once approved and says when it stopped', () => {
    const approved = strip([builder('idle', { review_rounds: 2, last_verdict: 'approve' }), reviewer('idle')])
    expect(currentStep(approved).attributes('data-step')).toBe('merge')
    expect(approved.get('.team-verdict').text()).toBe('Approved')

    const stopped = strip([builder('idle', { review_rounds: 3, last_verdict: 'changes_requested', max_review_rounds: 3 }), reviewer('idle')])
    expect(stopped.get('[data-testid="team-strip"]').attributes('data-stage')).toBe('stopped')
    expect(currentStep(stopped).classes()).toContain('problem')
    expect(currentStep(stopped).text()).toContain('Review (round 3 of 3)')
  })

  it('opens a worker from its chip', async () => {
    const wrapper = strip([builder('idle'), reviewer('processing')])
    const chips = wrapper.findAll('[data-testid="team-worker-chip"]')
    await chips[1]!.trigger('click')
    await chips[0]!.trigger('click')
    expect(wrapper.emitted('open')).toEqual([['r1'], ['b1']])
  })
})

describe('WorkerNote', () => {
  it('names the parent as a button that opens it', async () => {
    const wrapper = mount(WorkerNote, { props: { note: workerNote(builder('idle'), 'Julian Purresso')!, parentId: 'parent' } })
    expect(wrapper.text().replace(/\s+/g, ' ')).toBe('Build worker of Julian Purresso · branch worker/export')
    const link = wrapper.get('[data-testid="worker-note-parent"]')
    expect(link.element.tagName).toBe('BUTTON')
    await link.trigger('click')
    expect(wrapper.emitted('open')).toEqual([['parent']])
  })

  it('says a review worker is read-only', () => {
    const wrapper = mount(WorkerNote, { props: { note: workerNote(reviewer('idle'), 'Julian Purresso')!, parentId: 'parent' } })
    expect(wrapper.text().replace(/\s+/g, ' ')).toBe('Review worker of Julian Purresso · read-only')
  })

  it('shows plain text when the parent is no longer in the list', () => {
    const wrapper = mount(WorkerNote, { props: { note: workerNote(builder('idle'), '')!, parentId: '' } })
    expect(wrapper.find('[data-testid="worker-note-parent"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Session parent')
  })
})

import { mount } from '@vue/test-utils'
import UserQuestionModal from '../../components/agents/UserQuestionModal.vue'
import { workerProposalBadges, workerProposalOf, workerProposalTitle } from '../../utils/agents/workerProposal'

const build = {
  task: 'Add the export button to the reports page',
  mode: 'build',
  working_directory: '/Users/alex/projects/app',
  model: 'deepseek-flash',
  provider: 'deepseek',
  runtime: 'claude-sdk',
  context: 'The plan is in docs/export.md.',
  sandboxed: true,
  read_only: false,
  parent_session_id: 'parent-1',
  worktree_path: '/Users/alex/projects/app/.worktrees/worker-export',
  branch: 'worker/export-button',
  base_branch: 'main',
}

const review = {
  task: 'Review the export button for correctness',
  mode: 'review',
  working_directory: '/Users/alex/projects/app/.worktrees/worker-export',
  model: 'gpt-6-astra',
  provider: 'codex',
  runtime: 'codex',
  sandboxed: true,
  read_only: true,
  parent_session_id: 'parent-1',
  reviews_session_id: 'builder-1234-5678',
  review_round: 2,
  max_review_rounds: 3,
}

const cardFor = (proposal: Record<string, unknown>) => ({
  id: 'q1', sessionId: 'parent-1', question: 'plain text version', header: 'Worker session',
  options: [{ label: 'Start', description: '' }, { label: 'Deny', description: '' }],
  multiSelect: false, timestamp: new Date(), status: 'pending',
  requires_user: true, kind: 'worker_session', proposal,
}) as any

it('shows a build worker proposal as a dedicated card with its settings', () => {
  const wrapper = mount(UserQuestionModal, { props: { show: true, question: cardFor(build) } })
  const el = wrapper.find('[data-testid="worker-session-card"]')
  expect(el.exists()).toBe(true)
  expect(el.text()).toContain('Start a build worker?')
  expect(el.text()).toContain('Add the export button to the reports page')
  expect(el.text()).toContain('The plan is in docs/export.md.')
  expect(el.text()).toContain('~/projects/app')
  expect(el.text()).toContain('deepseek-flash')
  expect(el.text()).toContain('· deepseek')
  const badges = wrapper.findAll('[data-testid="worker-badges"] .loop-badge').map(b => b.text())
  expect(badges).toEqual(['Sandboxed', 'Own worktree · worker/export-button from main'])
  expect(wrapper.find('.options-grid').exists()).toBe(false)
  expect(wrapper.find('[data-testid="loop-session-card"]').exists()).toBe(false)
  wrapper.unmount()
})

it('shows a review worker as read-only with its review round', () => {
  const wrapper = mount(UserQuestionModal, { props: { show: true, question: cardFor(review) } })
  const el = wrapper.find('[data-testid="worker-session-card"]')
  expect(el.text()).toContain('Start a review worker?')
  const badges = wrapper.findAll('[data-testid="worker-badges"] .loop-badge').map(b => b.text())
  expect(badges).toEqual(['Sandboxed', 'Read-only', 'Review round 2 of 3'])
  wrapper.unmount()
})

it('answers Start and Deny with the server labels', async () => {
  const wrapper = mount(UserQuestionModal, { props: { show: true, question: cardFor(build) } })
  await wrapper.find('[data-testid="worker-start"]').trigger('click')
  await wrapper.find('[data-testid="worker-deny"]').trigger('click')
  expect(wrapper.emitted('submit')).toEqual([['q1', ['Start']], ['q1', ['Deny']]])
  wrapper.unmount()
})

it('does not answer while disconnected', async () => {
  const wrapper = mount(UserQuestionModal, { props: { show: true, question: cardFor(build), connected: false } })
  expect(wrapper.find('[data-testid="worker-start"]').attributes('disabled')).toBeDefined()
  await wrapper.find('[data-testid="worker-start"]').trigger('click')
  expect(wrapper.emitted('submit')).toBeUndefined()
  wrapper.unmount()
})

it('never starts a worker on Enter', () => {
  const wrapper = mount(UserQuestionModal, { props: { show: true, question: cardFor(build) }, attachTo: document.body })
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }))
  expect(wrapper.emitted('submit')).toBeUndefined()
  wrapper.unmount()
})

it('only treats server confirmations as worker cards', () => {
  const card = cardFor(build)
  expect(workerProposalOf(card)).toEqual(build)
  // An agent's own AskUserQuestion never carries requires_user.
  expect(workerProposalOf({ ...card, requires_user: false })).toBeNull()
  expect(workerProposalOf({ ...card, kind: 'loop_session' })).toBeNull()
  expect(workerProposalOf({ ...card, proposal: { mode: 'build' } })).toBeNull()
  expect(workerProposalOf(null)).toBeNull()
  const wrapper = mount(UserQuestionModal, { props: { show: true, question: { ...card, requires_user: false } } })
  expect(wrapper.find('[data-testid="worker-session-card"]').exists()).toBe(false)
  wrapper.unmount()
})

it('builds the title and badges from the proposal', () => {
  expect(workerProposalTitle({ mode: 'build' })).toBe('Start a build worker?')
  expect(workerProposalTitle({ mode: 'review' })).toBe('Start a review worker?')
  // A build without a git repository has no worktree badge.
  expect(workerProposalBadges({ ...build, branch: undefined, base_branch: undefined } as any).map(b => b.text)).toEqual(['Sandboxed'])
  expect(workerProposalBadges({ ...build, base_branch: undefined } as any).map(b => b.text)).toEqual(['Sandboxed', 'Own worktree · worker/export-button'])
  // A review that is not linked to a builder has no round.
  expect(workerProposalBadges({ ...review, reviews_session_id: undefined } as any).map(b => b.text)).toEqual(['Sandboxed', 'Read-only'])
  // The server always sandboxes; if it ever did not, the card says so.
  const unsandboxed = workerProposalBadges({ ...build, sandboxed: false } as any)[0]!
  expect(unsandboxed).toMatchObject({ text: 'Not sandboxed', danger: true })
})

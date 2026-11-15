import { mount } from '@vue/test-utils'
import { setHomeDir } from '../../utils/homeDir'
import UserQuestionModal from '../../components/agents/UserQuestionModal.vue'
import { applyLoopProposal, loopProposalOf, snapshotLoopFields } from '../../utils/agents/loopProposal'

const proposal = {
  goal: 'Build the Android app until parity with iOS',
  verify_command: 'cd android && ./gradlew assembleDebug',
  working_directory: '/Users/alex/projects/app',
  max_iterations: 10,
  timeout_minutes: 30,
  model: 'claude-opus-5-5',
  provider: 'claude',
  context: 'The iOS app is the spec.',
  yolo: true,
  sandboxed: false,
  parent_session_id: 'parent-1',
}

const card = {
  id: 'q1', sessionId: 'parent-1', question: 'plain text version', header: 'Loop session',
  options: [{ label: 'Start', description: '' }, { label: 'Edit first', description: '' }, { label: 'Deny', description: '' }],
  multiSelect: false, timestamp: new Date(), status: 'pending',
  requires_user: true, kind: 'loop_session', proposal,
} as any

beforeEach(() => setHomeDir('/Users/alex'))

it('shows a loop-session proposal as a dedicated card with its settings', () => {
  const wrapper = mount(UserQuestionModal, { props: { show: true, question: card } })
  const el = wrapper.find('[data-testid="loop-session-card"]')
  expect(el.exists()).toBe(true)
  expect(el.text()).toContain('Build the Android app until parity with iOS')
  expect(el.text()).toContain('./gradlew assembleDebug')
  expect(el.text()).toContain('YOLO')
  expect(el.text()).toContain('Not sandboxed')
  expect(el.text()).toContain('~/projects/app')
  expect(el.text()).toContain('10 iterations · 30 min')
  expect(wrapper.find('.options-grid').exists()).toBe(false)
  wrapper.unmount()
})

it('answers Start, Edit first and Deny with the server labels', async () => {
  const wrapper = mount(UserQuestionModal, { props: { show: true, question: card } })
  await wrapper.find('[data-testid="loop-start"]').trigger('click')
  await wrapper.find('[data-testid="loop-edit"]').trigger('click')
  await wrapper.find('[data-testid="loop-deny"]').trigger('click')
  expect(wrapper.emitted('submit')).toEqual([['q1', ['Start']], ['q1', ['Edit first']], ['q1', ['Deny']]])
  wrapper.unmount()
})

it('never starts a loop on Enter', async () => {
  const wrapper = mount(UserQuestionModal, { props: { show: true, question: card }, attachTo: document.body })
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }))
  expect(wrapper.emitted('submit')).toBeUndefined()
  wrapper.unmount()
})

it('only treats server confirmations as loop cards', () => {
  expect(loopProposalOf(card)).toEqual(proposal)
  // An agent's own AskUserQuestion never carries requires_user.
  expect(loopProposalOf({ ...card, requires_user: false })).toBeNull()
  expect(loopProposalOf({ ...card, kind: undefined })).toBeNull()
  const wrapper = mount(UserQuestionModal, { props: { show: true, question: { ...card, requires_user: false } } })
  expect(wrapper.find('[data-testid="loop-session-card"]').exists()).toBe(false)
  wrapper.unmount()
})

it('Edit first fills Create Session in loop mode, linked to the proposing session', () => {
  const form: Record<string, any> = { modelProvider: 'deepseek', model: 'deepseek-chat', permissionMode: 'default', sessionMode: 'interactive' }
  applyLoopProposal(form, proposal, ['claude', 'deepseek'])
  expect(form).toMatchObject({
    workingDirectory: '/Users/alex/projects/app',
    sessionMode: 'loop',
    loopGoal: proposal.goal,
    loopVerifyCommand: proposal.verify_command,
    loopMaxIterations: 10,
    loopTimeoutMinutes: 30,
    permissionMode: 'yolo',
    sandboxed: false,
    parentSessionId: 'parent-1',
    spawnedBySessionId: 'parent-1',
    contextSummary: 'The iOS app is the spec.',
    modelProvider: 'claude',
    model: 'claude-opus-5-5',
  })

  const other: Record<string, any> = { modelProvider: 'deepseek', model: 'deepseek-chat' }
  applyLoopProposal(other, proposal, ['deepseek'])
  expect(other.modelProvider).toBe('deepseek')
})

it('Edit first leaves the shared form as it was once Create Session closes', () => {
  const form: Record<string, any> = { workingDirectory: '/mine', sessionMode: 'normal', permissionMode: 'default', sandboxed: true, modelProvider: 'claude', model: 'sonnet', title: 'kept' }
  const before = snapshotLoopFields(form)
  applyLoopProposal(form, proposal, ['claude', 'deepseek'])
  expect(form.sessionMode).toBe('loop')
  Object.assign(form, before)
  expect(form).toMatchObject({ workingDirectory: '/mine', sessionMode: 'normal', permissionMode: 'default', sandboxed: true, model: 'sonnet', title: 'kept' })
  expect(form.spawnedBySessionId).toBeUndefined()
  expect(form.loopGoal).toBeUndefined()
})

import { mount } from '@vue/test-utils'
import UserQuestionModal from '../../components/agents/UserQuestionModal.vue'

const question = {
  id: 'q1', sessionId: 's1', question: 'How close should the character be?', header: 'Character',
  options: [{ label: 'Original hero', description: 'safe' }, { label: 'Batman, named', description: 'risky' }],
  multiSelect: false, timestamp: new Date(), status: 'pending'
} as any

function buttons(wrapper: ReturnType<typeof mount>) {
  return Object.fromEntries(wrapper.findAll('.modal-actions button').map(b => [b.text().trim(), b]))
}

it('dismisses a question that is no longer relevant, separately from hiding it for later', async () => {
  const wrapper = mount(UserQuestionModal, { props: { show: true, question } })
  const { Dismiss, Later } = buttons(wrapper)
  await Dismiss!.trigger('click')
  expect(wrapper.emitted('dismiss')).toEqual([['q1']])
  expect(wrapper.emitted('cancel')).toBeUndefined()
  await Later!.trigger('click')
  expect(wrapper.emitted('cancel')).toHaveLength(1)
  wrapper.unmount()
})

it('cannot dismiss while disconnected, since the agent would never hear about it', () => {
  const wrapper = mount(UserQuestionModal, { props: { show: true, question, connected: false } })
  expect(buttons(wrapper).Dismiss!.attributes('disabled')).toBeDefined()
  wrapper.unmount()
})

describe('auto-pick', () => {
  const agentQuestion = (over: Record<string, unknown> = {}) => ({
    ...question,
    options: [{ label: 'Original hero', description: 'safe' }, { label: 'Batman, named (Recommended)', description: 'risky' }],
    recommended: [1],
    ...over,
  }) as any

  afterEach(() => { vi.useRealTimers() })

  it('marks the recommended option with a badge', () => {
    const wrapper = mount(UserQuestionModal, { props: { show: true, question: agentQuestion() } })
    const cards = wrapper.findAll('.option-card')
    expect(cards[0]!.find('[data-testid="recommended-badge"]').exists()).toBe(false)
    expect(cards[1]!.find('[data-testid="recommended-badge"]').text()).toBe('Recommended')
    wrapper.unmount()
  })

  it('counts down to the auto-pick and keeps waiting on request', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-07T12:00:00Z'))
    const wrapper = mount(UserQuestionModal, {
      props: { show: true, question: agentQuestion({ auto_answer_at: '2026-10-07T12:01:42Z' }) },
    })
    const bar = wrapper.get('[data-testid="auto-pick-countdown"]')
    expect(bar.text()).toContain('Auto-picks “Batman, named” in 1:42')
    await vi.advanceTimersByTimeAsync(2000)
    expect(wrapper.get('[data-testid="auto-pick-countdown"]').text()).toContain('1:40')

    await wrapper.get('[data-testid="auto-pick-hold"]').trigger('click')
    expect(wrapper.emitted('hold')).toEqual([['q1']])
    // The answer buttons are unchanged
    expect(wrapper.emitted('submit')).toBeUndefined()

    await wrapper.setProps({ question: agentQuestion({ auto_answer_at: null, held: true }) })
    expect(wrapper.find('[data-testid="auto-pick-countdown"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="auto-pick-held"]').text()).toBe('Waiting for you')
    wrapper.unmount()
  })

  it('never counts down on a confirmation card', () => {
    const wrapper = mount(UserQuestionModal, {
      props: { show: true, question: agentQuestion({ requires_user: true, recommended: [], auto_answer_at: new Date(Date.now() + 60_000).toISOString() }) },
    })
    expect(wrapper.find('[data-testid="auto-pick-countdown"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="recommended-badge"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows nothing extra when the question waits without limit', () => {
    const wrapper = mount(UserQuestionModal, { props: { show: true, question: agentQuestion({ auto_answer_at: null }) } })
    expect(wrapper.find('.auto-pick-bar').exists()).toBe(false)
    wrapper.unmount()
  })
})

import { mount, flushPromises } from '@vue/test-utils'
import NotchTheatre from '../../components/notch/NotchTheatre.vue'
import { buildScene, type NotchSession } from '../../utils/notch/notchScene'

const NOW = Date.now()
const recent = () => ({ doneAt: {}, failedAt: {} })

function mountTheatre(scene = buildScene([], [], [], recent(), NOW), extra: Record<string, unknown> = {}) {
  const resize = vi.fn(async () => true)
  const answer = vi.fn(async () => ({ ok: true }))
  const wrapper = mount(NotchTheatre, {
    props: { scene, now: NOW, notchWidth: 185, menuHeight: 32, ear: 84, answer, resize, ...extra },
  })
  return { wrapper, resize, answer }
}

afterEach(() => { vi.useRealTimers() })

describe('NotchTheatre', () => {
  it('opens on hover after a short dwell, growing the window first, and shrinks it after closing', async () => {
    vi.useFakeTimers()
    const { wrapper, resize } = mountTheatre()
    await wrapper.trigger('mouseenter')
    expect(resize).not.toHaveBeenCalled()
    vi.advanceTimersByTime(200)
    await flushPromises()
    expect(resize).toHaveBeenCalledWith(true)
    expect(wrapper.find('.nt-drop').exists()).toBe(true)

    await wrapper.trigger('mouseleave')
    vi.advanceTimersByTime(500)
    await flushPromises()
    expect(wrapper.find('.nt-drop').exists()).toBe(false)
    expect(resize).toHaveBeenLastCalledWith(true)
    vi.advanceTimersByTime(300)
    expect(resize).toHaveBeenLastCalledWith(false)
  })

  it('passing over the menu bar does not open it', async () => {
    vi.useFakeTimers()
    const { wrapper, resize } = mountTheatre()
    await wrapper.trigger('mouseenter')
    vi.advanceTimersByTime(80)
    await wrapper.trigger('mouseleave')
    vi.advanceTimersByTime(500)
    expect(resize).not.toHaveBeenCalled()
  })

  it('says how many work, with the spotlight on', () => {
    const scene = buildScene([{ id: 'a', status: 'processing' }, { id: 'b', status: 'processing' }], [], [], recent(), NOW)
    const { wrapper } = mountTheatre(scene)
    expect(wrapper.find('.badge').text()).toBe('2 working')
    expect(wrapper.findAll('.troupe .mini')).toHaveLength(2)
    expect(wrapper.find('.rig').classes()).toContain('lit')
  })

  it('rests with closed curtains when nothing runs', async () => {
    const { wrapper } = mountTheatre()
    expect(wrapper.find('.rig').classes()).not.toContain('lit')
    await wrapper.find('.nt-ear-left').trigger('click')
    await flushPromises()
    expect(wrapper.find('.proscenium').classes()).toContain('closed')
    expect(wrapper.text()).toContain('Resting, not dead')
    expect(wrapper.find('.badge').text()).toBe('Resting')
  })

  it('answers an approval card from the playbill', async () => {
    const sessions: NotchSession[] = [{ id: 's1', status: 'processing', pending_permissions: 1, options: { agent_name: 'Builder' } }]
    const scene = buildScene(sessions, [{ session_id: 's1', session_name: 'Builder', permission_id: 'p1', tool_name: 'Bash', details: { command: 'git push' } }], [], recent(), NOW)
    const { wrapper, answer } = mountTheatre(scene)
    expect(wrapper.find('.badge').text()).toBe('Needs you')
    expect(wrapper.find('.rig').classes()).toContain('lit')
    await wrapper.find('.nt-ear-right').trigger('click')
    await flushPromises()
    expect(wrapper.find('.placard').text()).toBe('May I?')
    const allow = wrapper.findAll('.cue-btn').find(b => b.text() === 'Allow')!
    await allow.trigger('click')
    await flushPromises()
    expect(answer).toHaveBeenCalledWith({ type: 'permission', sessionId: 's1', permissionId: 'p1', approved: true })
    expect(wrapper.emitted('answered')).toEqual([['p:p1']])
  })

  it('shows a refusal on the card and keeps it', async () => {
    const scene = buildScene([{ id: 's1', status: 'processing' }], [], [{ session_id: 's1', question_id: 'q1', question: 'Pick', options: [{ label: 'A (Recommended)' }, { label: 'B' }], recommended: [0] }], recent(), NOW)
    const { wrapper, answer } = mountTheatre(scene)
    answer.mockResolvedValueOnce({ ok: false, error: 'Sign in to Eggify to answer from the notch.' } as any)
    await wrapper.find('.nt-ear-left').trigger('click')
    await flushPromises()
    const buttons = wrapper.findAll('.cue-btn')
    expect(buttons.map(b => b.text())).toEqual(['A', 'B'])
    expect(buttons[0]!.classes()).toContain('primary')
    await buttons[0]!.trigger('click')
    await flushPromises()
    expect(answer).toHaveBeenCalledWith({ type: 'question', sessionId: 's1', questionId: 'q1', answers: ['A (Recommended)'] })
    expect(wrapper.find('.cue-error').text()).toMatch(/sign in/i)
    expect(wrapper.emitted('answered')).toBeUndefined()
  })

  it('sends bigger cards to the main window', async () => {
    const scene = buildScene([{ id: 's1', status: 'processing' }], [], [{ session_id: 's1', question_id: 'q1', question: 'Many', multi_select: true, options: [{ label: 'A' }] }], recent(), NOW)
    const { wrapper } = mountTheatre(scene)
    await wrapper.find('.nt-ear-left').trigger('click')
    await flushPromises()
    await wrapper.findAll('.cue-btn').find(b => b.text() === 'Answer in Eggify')!.trigger('click')
    expect(wrapper.emitted('open')).toEqual([['s1']])
  })

  it('asks to sign in instead of showing the troupe when signed out', async () => {
    const { wrapper } = mountTheatre(undefined, { signedOut: true })
    expect(wrapper.find('.badge').text()).toBe('Sign in')
    expect(wrapper.find('.rig').classes()).not.toContain('lit')
    await wrapper.find('.nt-ear-left').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Sign in to Eggify')
  })
})

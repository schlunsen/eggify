import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import SessionItem from '../../components/agents/SessionItem.vue'
import { useBackgroundAgents } from '../../composables/agents/useBackgroundAgents'

vi.mock('../../composables/useAvatarThemes', () => ({ fetchAvatarById: vi.fn(), getAvatarImageUrl: vi.fn() }))
vi.mock('../../composables/useCharacterAvatar', () => ({ useCharacterAvatar: () => ({}) }))

const mountItem = (session: Record<string, unknown>, props: Record<string, unknown> = {}) =>
  mount(SessionItem, { props: { session: { message_count: 4, options: {}, ...session }, isActive: false, ...props }, global: { stubs: { Teleport: true } } })

beforeEach(() => { localStorage.clear(); setActivePinia(createPinia()) })

it('says an idle session\'s subagents are still working', () => {
  const wrapper = mountItem({ id: 'sub-1', status: 'idle', subagents_running: 2 })
  expect(wrapper.get('.session-subagents').text()).toBe('Idle · 2 subagents working')
  expect(wrapper.get('.session-status-dot').classes()).toContain('subagents')
})

it('shows plain idle once its subagents are done', () => {
  const wrapper = mountItem({ id: 'sub-2', status: 'idle', subagents_running: 0 })
  expect(wrapper.find('.session-subagents').exists()).toBe(false)
  expect(wrapper.find('.session-status-dot').exists()).toBe(false)
})

it('has one working signal: the dot on the avatar, no "working" pill', () => {
  const wrapper = mountItem({ id: 'sub-3', status: 'processing', subagents_running: 2 })
  expect(wrapper.find('.session-working').exists()).toBe(false)
  expect(wrapper.text()).not.toMatch(/\bworking\b/)
  expect(wrapper.find('.session-subagents').exists()).toBe(false)
  expect(wrapper.find('.session-avatar .session-status-dot.working').exists()).toBe(true)
  expect(wrapper.find('.context-usage-bar').exists()).toBe(false)
  expect(wrapper.classes()).not.toContain('has-activity')
})

it('pulses its dot while it works, as it does for idle sessions with subagents', () => {
  expect(mountItem({ id: 'w-1', status: 'processing' }).find('.session-status-dot.working').exists()).toBe(true)
  expect(mountItem({ id: 'w-2', status: 'idle', subagents_running: 1 }).find('.session-status-dot.working').exists()).toBe(true)
  expect(mountItem({ id: 'w-3', status: 'idle', subagents_running: 0 }).find('.session-status-dot').exists()).toBe(false)
  expect(mountItem({ id: 'w-4', status: 'ended' }).find('.session-status-dot').exists()).toBe(false)
})

it('follows live subagent events once the whole list is loaded', async () => {
  const { processWebSocketMessage } = useBackgroundAgents()
  const agent = (id: string, status: string) => ({
    agent_id: id, parent_session_id: 'sub-4', subagent_type: 'x', description: id, status,
    progress: 0, last_output: '', output_lines: 0, created_at: '', updated_at: '',
  })
  processWebSocketMessage({ type: 'background_agents_list', session_id: 'sub-4', agents: [agent('a', 'running'), agent('b', 'completed')] } as any)
  // The listed count is stale (3); the live list says 1.
  const wrapper = mountItem({ id: 'sub-4', status: 'idle', subagents_running: 3 })
  expect(wrapper.get('.session-subagents').text()).toBe('Idle · 1 subagent working')

  processWebSocketMessage({ type: 'background_agent_completed', session_id: 'sub-4', agent_id: 'a' } as any)
  await wrapper.vm.$nextTick()
  expect(wrapper.find('.session-subagents').exists()).toBe(false)
})

it('shows a Claude Code loop and when it runs next', () => {
  const next = new Date(Date.now() + 25 * 60000 + 10000).toISOString()
  const wrapper = mountItem({ id: 'loop-1', status: 'idle', scheduled_loop: { next_at: next, delay_seconds: 1800, reason: 'waiting for CI' } })
  const label = wrapper.get('.session-loop')
  expect(label.text()).toBe('↻ loop 25m')
  expect(label.attributes('title')).toBe('Loop · next in 25 min: waiting for CI')
})

it('shows the loop and its working subagents side by side', () => {
  const next = new Date(Date.now() + 10 * 60000).toISOString()
  const wrapper = mountItem({ id: 'loop-2', status: 'idle', subagents_running: 2, scheduled_loop: { next_at: next, delay_seconds: 900 } })
  expect(wrapper.find('.session-loop').exists()).toBe(true)
  expect(wrapper.get('.session-subagents').text()).toBe('Idle · 2 subagents working')
})

it('shows what a session waits for, with a Review button that opens it', async () => {
  const wrapper = mountItem({ id: 'needs-1', status: 'processing' }, { waitingFor: 'Allow gh pr merge 1124?' })
  expect(wrapper.classes()).toContain('needs-you')
  expect(wrapper.get('[data-testid="session-waiting"]').text()).toContain('Allow gh pr merge 1124?')
  await wrapper.get('.btn-review').trigger('click')
  expect(wrapper.emitted('select')).toEqual([['needs-1']])
})

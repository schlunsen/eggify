import { mount } from '@vue/test-utils'
import SubagentTrackerSection from '../../components/agents/SubagentTrackerSection.vue'
import { useBackgroundAgents } from '../../composables/agents/useBackgroundAgents'

const session = 'tracker-many'
const iso = (msAgo: number) => new Date(Date.now() - msAgo).toISOString()
const agent = (id: string, status: string, msAgo: number) => ({
  agent_id: id, parent_session_id: session, subagent_type: 'general-purpose', description: `Task ${id}`, status, progress: 0,
  last_output: '', output_lines: 0, created_at: iso(msAgo + 1000), updated_at: iso(msAgo),
  ...(status === 'completed' || status === 'failed' ? { completed_at: iso(msAgo) } : {})
})

beforeEach(() => {
  const agents = useBackgroundAgents()
  agents.clearSessionAgents(session)
  agents.resetListTracking()
  for (let i = 1; i <= 8; i++) agents.handleAgentStarted({ agent: agent(`done${i}`, 'completed', i * 60000) } as any)
  agents.handleAgentStarted({ agent: agent('run1', 'running', 1000) } as any)
  agents.handleAgentStarted({ agent: agent('bad1', 'failed', 500000) } as any)
})

async function mountOpen() {
  const wrapper = mount(SubagentTrackerSection, { props: { sessionId: session } })
  await wrapper.get('.tracker-toggle').trigger('click')
  return wrapper
}

it('lists running first, then failed, then only the newest finished ones as compact rows', async () => {
  const wrapper = await mountOpen()
  const cards = wrapper.findAll('.agent-card')
  expect(cards.map(c => c.get('.agent-desc').text())).toEqual([
    'Task run1', 'Task bad1', 'Task done1', 'Task done2', 'Task done3', 'Task done4', 'Task done5'
  ])
  expect(cards.filter(c => c.classes().includes('compact'))).toHaveLength(5)
  // A finished row is one line: no type badge, progress bar or output preview.
  expect(cards[2]!.find('.agent-meta-row').exists()).toBe(false)
  expect(cards[2]!.find('.mini-progress').exists()).toBe(false)
  expect(cards[0]!.find('.agent-meta-row').exists()).toBe(true)
})

it('shows the rest of the finished agents on request and folds them again', async () => {
  const wrapper = await mountOpen()
  const more = wrapper.get('.show-more-btn')
  expect(more.text()).toBe('Show 3 more finished')
  await more.trigger('click')
  expect(wrapper.findAll('.agent-card')).toHaveLength(10)
  expect(wrapper.get('.show-more-btn').text()).toBe('Show fewer finished')
  await wrapper.get('.show-more-btn').trigger('click')
  expect(wrapper.findAll('.agent-card')).toHaveLength(7)
})

it('has no fold button when there are few finished agents', async () => {
  const agents = useBackgroundAgents()
  agents.clearSessionAgents(session)
  agents.handleAgentStarted({ agent: agent('only', 'completed', 1000) } as any)
  const wrapper = await mountOpen()
  expect(wrapper.find('.show-more-btn').exists()).toBe(false)
})

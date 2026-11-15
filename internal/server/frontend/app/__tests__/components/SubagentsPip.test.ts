import { mount, flushPromises } from '@vue/test-utils'
import SubagentsPip from '../../components/agents/SubagentsPip.vue'
import { useBackgroundAgents } from '../../composables/agents/useBackgroundAgents'

const auth = vi.hoisted(() => ({ fetchWithAuth: vi.fn() }))
vi.mock('../../composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => auth }))

const session = 'pip-session'
const iso = (msAgo: number) => new Date(Date.now() - msAgo).toISOString()
const subagents = [
  { agent_id: 'run', parent_session_id: session, subagent_type: 'general-purpose', description: 'Decode the music', status: 'running', progress: 0,
    last_output: '', output_lines: 1, created_at: iso(60000), updated_at: iso(1000), recent_output: ['TOOL:Bash: ffmpeg -i song.xmi out.mid'] },
  { agent_id: 'done', parent_session_id: session, subagent_type: 'Explore', description: 'Map the engine', status: 'completed', progress: 1,
    last_output: 'Found the engine loop.', output_lines: 1, created_at: iso(90000), updated_at: iso(5000), completed_at: iso(5000), recent_output: ['Found the engine loop.'] },
]

beforeEach(() => {
  try { localStorage.clear() } catch { /* ignore */ }
  useBackgroundAgents().clearSessionAgents(session)
  useBackgroundAgents().resetListTracking()
  auth.fetchWithAuth.mockImplementation(async () => ({ ok: true, json: async () => ({ subagents: structuredClone(subagents) }) }) as Response)
})

const nextFrame = () => new Promise(resolve => requestAnimationFrame(() => resolve(null)))
async function mountPip(show = true) {
  const wrapper = mount(SubagentsPip, { props: { sessionId: session, show } })
  await flushPromises()
  await nextFrame()
  return wrapper
}

it('shows what each subagent is doing, and opens the one clicked in its own modal', async () => {
  const wrapper = await mountPip()
  expect(wrapper.get('.pip-summary').text()).toBe('1 running · 1 done')
  const rows = wrapper.findAll('.pip-row')
  expect(rows.map(r => r.get('.pip-name').text())).toEqual(['Decode the music', 'Map the engine'])
  expect(rows[0]!.get('.pip-now').text()).toContain('ffmpeg')
  await rows[1]!.trigger('click')
  // Not the full view: a modal for just that agent (see SubagentZoomModal.test.ts).
  expect(wrapper.emitted('open-agent')).toBeUndefined()
  expect(document.body.querySelector('.szm-title')?.textContent).toBe('Map the engine')
  wrapper.unmount()
})

it('closes into a pill that brings it back, and remembers that', async () => {
  const wrapper = await mountPip()
  await wrapper.get('[aria-label="Close the subagents card"]').trigger('click')
  expect(wrapper.find('.pip-list').exists()).toBe(false)
  expect(wrapper.get('.pip-pill').text()).toContain('1 subagent working')
  expect(localStorage.getItem('eggify-subagents-pip-collapsed')).toBe('1')

  const again = await mountPip()
  expect(again.find('.pip-pill').exists()).toBe(true)
  await again.get('.pip-pill').trigger('click')
  expect(again.find('.pip-list').exists()).toBe(true)
  expect(localStorage.getItem('eggify-subagents-pip-collapsed')).toBeNull()
})

it('stays out of the way when hidden', async () => {
  const wrapper = await mountPip(false)
  expect(wrapper.find('.subagents-pip').exists()).toBe(false)
})

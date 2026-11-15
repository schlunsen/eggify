import { ref } from 'vue'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import MetricsSidebar from '../../components/agents/MetricsSidebar.vue'
import CustomDropdown from '../../components/ui/CustomDropdown.vue'
import { USER_REQUIRED_EVENT } from '../../utils/userRequired'

vi.mock('../../composables/useN0Connections', () => ({
  useN0Connections: () => ({ connections: ref([]), loaded: ref(true) }),
  n0ConnectionLabel: () => ''
}))
vi.mock('../../composables/useN0Bindings', () => ({
  useN0Bindings: () => ({ getBinding: () => null, refresh: vi.fn() })
}))

const { fetchWithAuth } = vi.hoisted(() => ({ fetchWithAuth: vi.fn() }))
vi.mock('~/composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => ({ fetchWithAuth }) }))

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

let putResponse: () => Response
beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
  putResponse = () => json({ session_id: 's1', policy: { mode: 'wait' }, effective: { mode: 'wait' } })
  fetchWithAuth.mockReset()
  fetchWithAuth.mockImplementation(async (url: string, init?: RequestInit) => {
    if (url === '/api/agent/question-policy') return json({ policy: { mode: 'auto_after', timeout_seconds: 120 }, is_default: true })
    if (init?.method === 'PUT') return putResponse()
    return json({})
  })
})

const mountSidebar = (session: Record<string, unknown>) => shallowMount(MetricsSidebar, {
  props: { show: true, session, connected: true, messageCount: 1, toolExecutions: {}, permissionStats: {} },
  global: { provide: { agentWs: { send: vi.fn() } }, stubs: { Teleport: true, NuxtLink: true, Icon: true } }
})

const claude = { id: 's1', status: 'idle', options: { provider: 'claude', model: 'claude-opus-5-5' }, question_policy: null, question_policy_effective: { mode: 'auto_after', timeout_seconds: 120 } }

const dropdown = (wrapper: ReturnType<typeof mountSidebar>) =>
  wrapper.get('[data-testid="question-policy-row"]').findComponent(CustomDropdown)

it('offers the default (with the global policy) and the explicit choices', async () => {
  const wrapper = mountSidebar(claude)
  await flushPromises()
  const dd = dropdown(wrapper)
  expect(dd.props('modelValue')).toBe('default')
  const labels = (dd.props('options') as any[]).map(o => o.label)
  expect(labels[0]).toBe('Default (auto after 2 min)')
  expect(labels).toEqual(expect.arrayContaining(['Wait for me', 'Auto-pick after 30 s', 'Auto-pick after 30 min', 'Auto-pick now']))
})

it('saves a new choice with PUT and keeps it', async () => {
  const wrapper = mountSidebar(claude)
  await flushPromises()
  dropdown(wrapper).vm.$emit('update:modelValue', 'wait')
  await flushPromises()
  const put = fetchWithAuth.mock.calls.find(([, init]) => init?.method === 'PUT')!
  expect(put[0]).toBe('/api/agent/sessions/s1/question-policy')
  expect(JSON.parse(put[1].body)).toEqual({ policy: { mode: 'wait' } })
  expect(dropdown(wrapper).props('modelValue')).toBe('wait')

  dropdown(wrapper).vm.$emit('update:modelValue', 'default')
  await flushPromises()
  const last = fetchWithAuth.mock.calls.filter(([, init]) => init?.method === 'PUT').pop()!
  expect(JSON.parse(last[1].body)).toEqual({ policy: null })
})

it('sends a timed auto-pick and reverts when the server refuses', async () => {
  putResponse = () => json({ error: 'Changing the question policy requires a signed-in user', code: 'user_required' }, 403)
  const announced = vi.fn()
  window.addEventListener(USER_REQUIRED_EVENT, announced)
  const wrapper = mountSidebar(claude)
  await flushPromises()
  dropdown(wrapper).vm.$emit('update:modelValue', 'auto_after:300')
  await flushPromises()
  const put = fetchWithAuth.mock.calls.find(([, init]) => init?.method === 'PUT')!
  expect(JSON.parse(put[1].body)).toEqual({ policy: { mode: 'auto_after', timeout_seconds: 300 } })
  expect(dropdown(wrapper).props('modelValue')).toBe('default')
  expect(announced).toHaveBeenCalled()
  expect(wrapper.get('[data-testid="question-policy-row"]').text()).toContain('signed-in user')
  window.removeEventListener(USER_REQUIRED_EVENT, announced)
})

it('is not shown for Codex or OpenAI-compatible sessions', () => {
  expect(mountSidebar({ ...claude, options: { provider: 'codex', model: 'gpt-6-astra' } }).find('[data-testid="question-policy-row"]').exists()).toBe(false)
  expect(mountSidebar({ ...claude, options: { provider: 'ollama', model: 'llama3' } }).find('[data-testid="question-policy-row"]').exists()).toBe(false)
})

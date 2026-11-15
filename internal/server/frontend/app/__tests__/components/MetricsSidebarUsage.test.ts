import { ref } from 'vue'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import MetricsSidebar from '../../components/agents/MetricsSidebar.vue'

vi.mock('../../composables/useN0Connections', () => ({
  useN0Connections: () => ({ connections: ref([]), loaded: ref(true) }),
  n0ConnectionLabel: () => ''
}))
vi.mock('../../composables/useN0Bindings', () => ({
  useN0Bindings: () => ({ getBinding: () => null, refresh: vi.fn() })
}))
const calls: string[] = []
const resetsSoon = new Date(Date.now() + 3 * 3600_000).toISOString()
vi.mock('~/composables/useAuthenticatedFetch', () => ({
  useAuthenticatedFetch: () => ({
    fetchWithAuth: vi.fn(async (url: string) => {
      calls.push(url)
      if (!url.startsWith('/api/agent/usage-limit')) return new Response('{}', { status: 404 })
      return new Response(JSON.stringify({
        limit: { status: 'allowed_warning', limit_type: 'seven_day', utilization: 0.82, resets_at: resetsSoon, observed_at: new Date().toISOString() },
        ...(url.includes('codex=1') ? { codex: { plan_type: 'prolite', limit_reached: false, observed_at: new Date().toISOString(),
          windows: [{ used_percent: 50, window_minutes: 10080, resets_at: resetsSoon }, { used_percent: 10, window_minutes: 300 }] } } : {})
      }), { status: 200 })
    })
  })
}))

beforeEach(() => { calls.length = 0; localStorage.clear(); setActivePinia(createPinia()) })

const mountSidebar = (session: Record<string, unknown>) => shallowMount(MetricsSidebar, {
  props: { show: true, session, connected: true, messageCount: 12, toolExecutions: {}, permissionStats: {} },
  global: { provide: { agentWs: { send: vi.fn() } }, stubs: { Teleport: true, NuxtLink: true, Icon: true } }
})

it("shows Codex's usage for a Codex session, on its own line", async () => {
  const wrapper = mountSidebar({ id: 'c1', status: 'idle', options: { provider: 'codex', model: 'gpt-6-sol' } })
  await flushPromises()
  const pill = wrapper.get('[data-testid="usage-limit-pill"]')
  expect(pill.text()).toMatch(/^Weekly limit: 50% used · resets /)
  expect(pill.classes()).toContain('calm')
  expect(pill.element.closest('.identity-meta')).toBeNull()
  expect(calls.some(u => u.includes('codex=1'))).toBe(true)
  wrapper.unmount()
})

it("shows Claude's usage for a Claude session without asking for Codex's", async () => {
  const wrapper = mountSidebar({ id: 'a1', status: 'idle', options: { provider: 'claude', model: 'claude-opus-5-5' } })
  await flushPromises()
  const pill = wrapper.get('[data-testid="usage-limit-pill"]')
  expect(pill.text()).toMatch(/: 82% used/)
  expect(pill.classes()).toContain('warn')
  expect(calls.every(u => !u.includes('codex=1'))).toBe(true)
  wrapper.unmount()
})

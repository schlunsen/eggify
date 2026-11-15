import { ref } from 'vue'
import { shallowMount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import MetricsSidebar from '../../components/agents/MetricsSidebar.vue'

vi.mock('../../composables/useN0Connections', () => ({
  useN0Connections: () => ({ connections: ref([]), loaded: ref(true) }),
  n0ConnectionLabel: () => ''
}))
vi.mock('../../composables/useN0Bindings', () => ({
  useN0Bindings: () => ({ getBinding: () => null, refresh: vi.fn() })
}))
vi.mock('~/composables/useAuthenticatedFetch', () => ({
  useAuthenticatedFetch: () => ({ fetchWithAuth: vi.fn(async () => new Response('{}', { status: 404 })) })
}))

beforeEach(() => { localStorage.clear(); setActivePinia(createPinia()) })

const mountSidebar = (session: Record<string, unknown>, props: Record<string, unknown> = {}) => shallowMount(MetricsSidebar, {
  props: { show: true, session, connected: true, messageCount: 12, toolExecutions: {}, permissionStats: {}, ...props },
  global: { provide: { agentWs: { send: vi.fn() } }, stubs: { Teleport: true, NuxtLink: true, Icon: true } }
})

it('shows context use as a tile that opens the breakdown', async () => {
  const wrapper = mountSidebar(
    { id: 's1', status: 'idle', options: { provider: 'claude', model: 'claude-opus-5-5' } },
    { contextUsage: { total_tokens: 144000, context_window: 200000, percentage: 72 } }
  )
  const tile = wrapper.get('.stat-tile-button')
  expect(tile.text()).toContain('72%')
  expect(tile.classes()).toContain('context-mid')
  expect(wrapper.find('#sidebar-context-detail').exists()).toBe(false)
  await tile.trigger('click')
  expect(tile.attributes('aria-expanded')).toBe('true')
  expect(wrapper.find('#sidebar-context-detail').exists()).toBe(true)
  expect(wrapper.get('.model-meta').text()).toBe('Claude · 200K context')
})

it('shows a dash before any context is known', () => {
  const wrapper = mountSidebar({ id: 's1', status: 'idle', options: {} })
  expect(wrapper.get('.stat-tile-button').text()).toContain('—')
})

it('makes the whole model row the switch button, and says why it is locked', () => {
  const idle = mountSidebar({ id: 's1', status: 'idle', options: { provider: 'claude', model: 'claude-opus-5-5' } })
  const row = idle.get('button.model-row')
  expect(row.attributes('disabled')).toBeUndefined()
  expect(row.get('.model-name').text()).toBe('claude-opus-5-5')
  expect(row.get('.model-action').text()).toBe('Switch')

  const busy = mountSidebar({ id: 's2', status: 'processing', options: { provider: 'claude', model: 'claude-opus-5-5' } })
  expect(busy.get('button.model-row').attributes('disabled')).toBeDefined()
  expect(busy.get('.model-action').text()).toBe('Busy')
})

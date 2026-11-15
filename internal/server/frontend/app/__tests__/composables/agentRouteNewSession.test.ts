import { defineComponent } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory, useRoute } from 'vue-router'
import { useSessionStore } from '../../stores/session/sessionStore'
import { useAgentRouteNavigation } from '../../composables/agents/useAgentRouteNavigation'

beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
})

it('opens Create Session for ?project=<id>&new_session=1 once the project is selected', async () => {
  const project = { id: 'p1', name: 'P', path: '/home/me/p1' }
  const fetchWithAuth = vi.fn(async () => ({ ok: true, json: async () => ({ project }) }) as Response)
  const store = useSessionStore()
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/agents', component: { template: '<div />' } }] })
  await router.push('/agents?project=p1&new_session=1')
  await router.isReady()
  const startNewSession = vi.fn()
  const Host = defineComponent({
    setup() {
      useAgentRouteNavigation({ route: useRoute(), router, sessionStore: store, connected: () => true,
        selectSession: vi.fn(), fetchWithAuth, startNewSession })
      return {}
    },
    template: '<div />',
  })
  const w = mount(Host, { global: { plugins: [router] } })
  await flushPromises()
  expect(store.selectedProject?.id).toBe('p1')
  expect(startNewSession).toHaveBeenCalledTimes(1)
  expect(startNewSession.mock.calls[0]![0]).toMatchObject({ id: 'p1', path: '/home/me/p1' })
  expect(router.currentRoute.value.query).toEqual({ project: 'p1' })
  w.unmount()
})

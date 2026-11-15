import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, h } from 'vue'
import { useAgentOverview } from '../../composables/agents/useAgentOverview'

const auth = vi.hoisted(() => ({ fetchWithAuth: vi.fn() }))
vi.mock('../../composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => auth }))

beforeEach(() => { localStorage.clear(); setActivePinia(createPinia()) })
afterEach(() => vi.unstubAllGlobals())

const sessions = [
  { id: 'running', status: 'processing', project_id: 'p', updated_at: new Date().toISOString(), message_count: 4 },
  { id: 'finished', status: 'idle', project_id: 'p', updated_at: new Date().toISOString(), message_count: 6, num_turns: 2 },
  { id: 'blocked', status: 'idle', project_id: 'p', updated_at: new Date().toISOString(), message_count: 2, pending_permissions: 1 }
]

function mountOverview() {
  auth.fetchWithAuth.mockImplementation(async (url: string) => ({ ok: true, json: async () =>
    url.startsWith('/api/agent/sessions') ? { sessions } :
    url === '/api/projects' ? { projects: [{ id: 'p', name: 'Project', path: '/p' }] } : {}
  }) as Response)
  const api: any = {}
  const wrapper = mount(defineComponent({
    setup() { Object.assign(api, useAgentOverview()); return () => h('div') }
  }))
  return { wrapper, api }
}

it('opens on the running agents instead of every recent session', async () => {
  const { wrapper, api } = mountOverview()
  try {
    await flushPromises()
    expect(api.filter.value).toBe('working')
    // 'finished' ended its turn just now, so it stays in Working after 'running'.
    expect(api.agents.value.map((a: any) => a.id)).toEqual(['running', 'finished'])
    expect(api.counts.value).toMatchObject({ all: 3, working: 1, attention: 1, recent: 1 })
  } finally { wrapper.unmount() }
})

it('still reaches the other agents when the filter changes', async () => {
  const { wrapper, api } = mountOverview()
  try {
    await flushPromises()
    api.filter.value = 'attention'
    await flushPromises()
    expect(api.agents.value.map((a: any) => a.id)).toEqual(['blocked'])
    api.filter.value = 'all'
    await flushPromises()
    expect(api.agents.value.map((a: any) => a.id).sort()).toEqual(['blocked', 'finished', 'running'])
  } finally { wrapper.unmount() }
})

it('keeps agents that went idle moments ago in Working, after the ones still working', async () => {
  const minutesAgo = (m: number) => new Date(Date.now() - m * 60000).toISOString()
  const extra = [
    { id: 'paused', status: 'idle', project_id: 'p', updated_at: minutesAgo(3), message_count: 8, num_turns: 3 },
    { id: 'long-done', status: 'idle', project_id: 'p', updated_at: minutesAgo(90), message_count: 8, num_turns: 3 }
  ]
  sessions.push(...(extra as any))
  const { wrapper, api } = mountOverview()
  try {
    await flushPromises()
    expect(api.agents.value.map((a: any) => a.id)).toEqual(['running', 'finished', 'paused'])
    expect(api.counts.value).toMatchObject({ working: 1, justIdle: 2 })
    expect(api.isJustIdle(extra[1])).toBe(false)
  } finally { wrapper.unmount(); sessions.splice(sessions.length - extra.length) }
})

it('asks for AI digests of the agents in view and filters them by tag', async () => {
  const posts: any[] = []
  auth.fetchWithAuth.mockImplementation(async (url: string, init?: RequestInit) => {
    if (url === '/api/agent/overview/digests') {
      const body = JSON.parse(String(init?.body))
      posts.push(body)
      return { ok: true, json: async () => ({ digests: {
        running: { summary: 'Shipping the login page', tags: ['auth', 'frontend'] },
        finished: { summary: 'Tidied the deploy script', tags: ['deploy'] }
      } }) } as Response
    }
    return { ok: true, json: async () => url.startsWith('/api/agent/sessions') ? { sessions } : { projects: [] } } as Response
  })
  const api: any = {}
  const wrapper = mount(defineComponent({ setup() { Object.assign(api, useAgentOverview()); return () => h('div') } }))
  try {
    await flushPromises(); await flushPromises()
    expect(posts[0]).toEqual({ generate: true, sessions: [{ id: 'running', version: '4:processing' }, { id: 'finished', version: '6:idle' }] })
    expect(api.digests.value.running.summary).toBe('Shipping the login page')
    expect(api.topTags.value.map((t: any) => t.tag)).toEqual(['auth', 'deploy', 'frontend'])
    api.tagFilter.value = 'deploy'
    expect(api.agents.value.map((a: any) => a.id)).toEqual(['finished'])
    api.summariesOn.value = false
    await flushPromises()
    expect(api.tagFilter.value).toBe('')
    expect(localStorage.getItem('eggify:overview-summaries')).toBe('off')
  } finally { wrapper.unmount() }
})

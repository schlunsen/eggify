import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { RouterLink } from 'vue-router'
import HomePage from '../../pages/index.vue'

function json(status: number, body: any): Response {
  return { ok: status >= 200 && status < 300, status, json: async () => body } as unknown as Response
}

const now = Date.now()
const iso = (minAgo: number) => new Date(now - minAgo * 60000).toISOString()

const projects = [
  { id: 'p1', name: 'zeta-api', path: '/x/zeta', color: '#ef4444', running_count: 1, last_activity: iso(2), git: null },
  { id: 'p2', name: 'old thing', path: '/x/old', missing: true, created_at: iso(9999) },
]
const sessions = [
  { id: 's1', status: 'processing', updated_at: iso(1), message_count: 4, project_id: 'p1', model_name: 'sonnet' },
  { id: 's2', status: 'idle', updated_at: iso(300), message_count: 6, num_turns: 2, project_id: 'p1', context_summary: 'Fix login bug' },
  { id: 's3', status: 'idle', updated_at: iso(10), message_count: 3, pending_permissions: 1, project_id: 'p1' },
]

const openWizard = vi.fn()
const runtimeState = { ready: true }
vi.mock('~/composables/useAgentRuntimes', () => ({
  useAgentRuntimes: () => ({ anyReady: { value: runtimeState.ready }, openWizard, load: async () => [] }),
}))
function stub(opts: { sessions?: any[], projects?: any[], ready?: boolean, providers?: any[] } = {}) {
  const fetchWithAuth = vi.fn(async (url: string) => {
    if (url.startsWith('/api/agent/sessions')) return json(200, { sessions: opts.sessions ?? sessions })
    if (url.startsWith('/api/projects/overview')) return json(200, { projects: opts.projects ?? projects })
    if (url.startsWith('/api/providers')) return json(200, { providers: opts.providers ?? [] })
    if (url.startsWith('/api/stats')) return json(200, { totalConversations: 3, totalTokens: 12500 })
    if (url.startsWith('/api/health')) return json(200, { status: 'ok', uptime_seconds: 3700 })
    if (url.startsWith('/api/db/stats')) return json(200, { stats: { db_size_human: '2 MB' } })
    if (url.startsWith('/api/agent/overview/digests')) return json(200, { digests: { s2: { summary: 'Fix login bug', tags: ['auth'] } } })
    return json(404, {})
  })
  vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth }))
  runtimeState.ready = opts.ready ?? true
  return fetchWithAuth
}

let wrapper: VueWrapper | null = null
async function mountHome() {
  const Stub = { template: '<div />' }
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/', component: HomePage }, { path: '/agents', component: Stub }, { path: '/projects', component: Stub }, { path: '/projects/:id', component: Stub },
  ] })
  await router.push('/')
  await router.isReady()
  wrapper = mount(HomePage, { global: { plugins: [router], stubs: { EggifyLogo: true, NuxtLink: RouterLink } } })
  await flushPromises()
  return wrapper
}

afterEach(() => { wrapper?.unmount(); wrapper = null; vi.unstubAllGlobals(); openWizard.mockClear() })

describe('Home page', () => {
  it('puts sessions that need you before working ones, and links to them', async () => {
    stub()
    const w = await mountHome()
    const rows = w.findAll('[data-test="active-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('Approval needed')
    expect(rows[1]!.text()).toContain('Working')
    expect(rows[0]!.attributes('href')).toContain('session=s3')
    expect(w.text()).toContain('1 agent needs you')
  })

  it('lists recent finished sessions with their summary and tags', async () => {
    stub()
    const w = await mountHome()
    const rows = w.findAll('[data-test="recent-row"]')
    expect(rows).toHaveLength(1)
    expect(rows[0]!.text()).toContain('Fix login bug')
    expect(rows[0]!.text()).toContain('auth')
  })

  it('shows projects with a start-session link and flags missing folders', async () => {
    stub()
    const w = await mountHome()
    const cards = w.findAll('[data-test="project-card"]')
    expect(cards.map(c => c.find('.project-name').text())).toEqual(['zeta-api', 'old thing'])
    expect(cards[0]!.find('a.btn').attributes('href')).toContain('project=p1')
    expect(cards[1]!.text()).toContain('Folder missing')
    expect(cards[1]!.find('a.btn').exists()).toBe(false)
  })

  it('has the primary actions and one quiet stats line', async () => {
    stub()
    const w = await mountHome()
    expect(w.find('[data-test="new-session"]').attributes('href')).toContain('new_session=1')
    expect(w.find('[data-test="new-project"]').attributes('href')).toContain('new=1')
    expect(w.find('[data-test="statline"]').text()).toBe('3 sessions · 12.5K tokens · up 1h 1m · database 2 MB')
    expect(w.find('.stat-card').exists()).toBe(false)
  })

  it('shows the checklist for a fresh install and hides it once done', async () => {
    stub({ sessions: [], projects: [], ready: false })
    let w = await mountHome()
    expect(w.findAll('.checklist-items li')).toHaveLength(3)
    expect(w.findAll('.checklist-items li.done')).toHaveLength(0)
    await w.find('[data-step="agent"] button').trigger('click')
    expect(openWizard).toHaveBeenCalled()
    expect(w.find('[data-test="active-row"]').exists()).toBe(false)
    w.unmount()

    stub()
    w = await mountHome()
    expect(w.find('.checklist').exists()).toBe(false)
  })
})

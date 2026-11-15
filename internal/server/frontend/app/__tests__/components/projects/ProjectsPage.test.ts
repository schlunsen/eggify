import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory, type Router } from 'vue-router'
import ProjectsPage from '../../../pages/projects/index.vue'

function json(status: number, body: any): Response {
  return { ok: status >= 200 && status < 300, status, json: async () => body, clone() { return this } } as unknown as Response
}

const sample = [
  { id: 'a', name: 'zeta-api', path: '/home/me/code/zeta-api', display_path: '~/code/zeta-api', color: '#ef4444', session_count: 4, running_count: 0, last_activity: '2026-09-01 10:00:00', created_at: '2026-01-01T00:00:00Z', missing: false, git: { is_repo: true, branch: 'main', dirty: true } },
  { id: 'b', name: 'Alpha site', path: '/home/me/code/alpha', display_path: '~/code/alpha', color: '#22c55e', session_count: 1, running_count: 2, last_activity: '2026-08-01 10:00:00', created_at: '2026-01-02T00:00:00Z', missing: false, git: null },
  { id: 'c', name: 'Mid tool', path: '/home/me/old/mid', display_path: '~/old/mid', session_count: 0, running_count: 0, created_at: '2026-02-01T00:00:00Z', missing: true, git: null },
]

function stubApi(projects: any[]) {
  const fetchWithAuth = vi.fn(async (url: string, init?: RequestInit) => {
    if (url.startsWith('/api/projects/overview')) return json(200, { projects, home: '/home/me' })
    if (url.startsWith('/api/projects/setup/defaults')) return json(200, { home: '/home/me', projects_dir: '/home/me/projects', projects_dir_display: '~/projects', git_available: true })
    if (url.startsWith('/api/projects/setup/inspect')) return json(200, { exists: false, is_dir: false, can_create: true, display_path: 'x' })
    if (init?.method === 'DELETE') return json(200, { status: 'success' })
    return json(404, {})
  })
  vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth }))
  return fetchWithAuth
}

function makeRouter(): Router {
  const Stub = { template: '<div />' }
  return createRouter({ history: createMemoryHistory(), routes: [
    { path: '/projects', component: ProjectsPage },
    { path: '/projects/:id', component: Stub },
    { path: '/agents', component: Stub },
  ] })
}

let wrapper: VueWrapper | null = null
async function mountAt(url: string, projects: any[]) {
  const api = stubApi(projects)
  const router = makeRouter()
  await router.push(url)
  await router.isReady()
  wrapper = mount(ProjectsPage, { attachTo: document.body, global: { plugins: [router] } })
  await flushPromises()
  return { w: wrapper, router, api }
}

beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  vi.unstubAllGlobals()
})

const names = (w: VueWrapper) => w.findAll('.pp-row__name').map(n => n.text())

describe('Projects page', () => {
  it('shows the empty state with three starting points', async () => {
    const { w } = await mountAt('/projects', [])
    expect(w.text()).toContain('No projects yet')
    const starts = w.findAll('.pp-start')
    expect(starts.map(s => s.attributes('data-mode'))).toEqual(['existing', 'clone', 'empty'])
    await starts[1]!.trigger('click')
    await flushPromises()
    expect(w.find('.np-dialog').exists()).toBe(true)
    expect(w.find('.np-title').text()).toBe('Clone a repository')
  })

  it('lists projects by recent activity, running first, and sorts by name', async () => {
    const { w } = await mountAt('/projects', sample)
    expect(names(w)).toEqual(['Alpha site', 'zeta-api', 'Mid tool'])
    expect(w.text()).toContain('2 running')
    expect(w.text()).toContain('~/code/zeta-api')
    expect(w.find('[data-project-id="a"] .pp-meta--branch').text()).toContain('main')
    expect(w.find('[data-project-id="a"] .pp-dirty').exists()).toBe(true)
    expect(w.find('[data-project-id="c"]').text()).toContain('Folder not found')
    await w.get('[data-sort="name"]').trigger('click')
    expect(names(w)).toEqual(['Alpha site', 'Mid tool', 'zeta-api'])
    expect(localStorage.getItem('eggify-projects-sort')).toBe('name')
  })

  it('filters with search and shows a no-results state', async () => {
    const { w } = await mountAt('/projects', sample)
    await w.get('input[type="search"]').setValue('zeta')
    expect(names(w)).toEqual(['zeta-api'])
    expect(w.text()).toContain('1 of 3 projects')
    await w.get('input[type="search"]').setValue('main')
    expect(names(w)).toEqual(['zeta-api']) // branch matches too
    await w.get('input[type="search"]').setValue('nothing-like-this')
    expect(w.text()).toContain('No projects match')
  })

  it('opens the new-project dialog from /projects?new=1 and clears the param on close', async () => {
    const { w, router } = await mountAt('/projects?new=1', sample)
    expect(w.find('.np-dialog').exists()).toBe(true)
    expect(w.find('.np-title').text()).toBe('New project')
    await w.get('.np-dialog').trigger('keydown', { key: 'Escape' })
    await flushPromises()
    expect(w.find('.np-dialog').exists()).toBe(false)
    expect(router.currentRoute.value.query.new).toBeUndefined()

    // Client-side navigation to ?new=1 opens it again (palette / ⇧⌘N / menu).
    await router.push('/projects?new=1')
    await flushPromises()
    expect(w.find('.np-dialog').exists()).toBe(true)
  })

  it('starts a session in a project and removes projects without touching files', async () => {
    const { w, router, api } = await mountAt('/projects', sample)
    await w.get('[data-project-id="a"] [data-action="start"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/agents')
    expect(router.currentRoute.value.query).toEqual({ project: 'a', new_session: '1' })
    expect(localStorage.getItem('selectedProjectId')).toBe('a')

    await router.push('/projects')
    await flushPromises()
    await w.get('[data-project-id="b"] [data-action="menu"]').trigger('click')
    await w.get('[data-project-id="b"] [data-action="remove"]').trigger('click')
    expect(w.text()).toContain('stay on disk')
    await w.get('.pp-btn--danger').trigger('click')
    await flushPromises()
    expect(api).toHaveBeenCalledWith('/api/projects/b', { method: 'DELETE' })
    expect(names(w)).not.toContain('Alpha site')
  })
})

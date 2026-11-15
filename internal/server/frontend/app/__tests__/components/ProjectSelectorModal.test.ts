import { defineComponent, ref } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory, useRoute } from 'vue-router'
import ProjectSelectorModal from '../../components/ProjectSelectorModal.vue'
import { useSessionStore } from '../../stores/session/sessionStore'
import { useAgentRouteNavigation } from '../../composables/agents/useAgentRouteNavigation'

beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
})
afterEach(() => vi.unstubAllGlobals())

it.each(['click', 'keyboard'])('keeps the selected project and its session when switching via %s from a project URL', async (method) => {
  const projects = [
    { id: 'old', name: 'Old project', path: '/old' },
    { id: 'target', name: 'Target project', path: '/target' },
    { id: 'other', name: 'Other project', path: '/other' },
  ]
  const fetchWithAuth = vi.fn(async () => ({ ok: true, json: async () => ({ projects }) }) as Response)
  vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth }))
  const store = useSessionStore()
  store.setSelectedProject(projects[0] as any)
  store.sessions = [{ id: 'target-session', project_id: 'target', status: 'idle' }] as any
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/agents', component: { template: '<div />' } },
  ] })
  await router.push('/agents?project=old')
  await router.isReady()
  const Host = defineComponent({
    components: { ProjectSelectorModal },
    setup() {
      const open = ref(false)
      useAgentRouteNavigation({ route: useRoute(), router, sessionStore: store,
        connected: () => false, selectSession: vi.fn(), fetchWithAuth })
      return { open }
    },
    template: '<button @click="open = true">Open</button><ProjectSelectorModal v-model="open" />',
  })
  const wrapper = mount(Host, { attachTo: document.body, global: { plugins: [router] } })
  try {
    await wrapper.get('button').trigger('click')
    await flushPromises()
    if (method === 'keyboard') {
      const input = wrapper.get('input')
      // The cursor opens on the project after the current one.
      expect(wrapper.get('.project-item-focused').text()).toContain('Target project')
      await input.trigger('keydown', { key: 'ArrowDown' })
      expect(wrapper.get('.project-item-focused').text()).toContain('Other project')
      await input.trigger('keydown', { key: 'ArrowUp' })
      expect(wrapper.get('.project-item-focused').text()).toContain('Target project')
      await input.trigger('keydown', { key: 'Enter' })
    } else {
      await wrapper.findAll('.project-item')[1].trigger('click')
    }
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ project: 'target' })
    expect(store.selectedProject).toEqual(projects[1])
    expect(store.activeSessionId).toBe('target-session')
    expect(localStorage.getItem('selectedProjectId')).toBe('target')
    expect(wrapper.find('.modal-overlay').exists()).toBe(false)
  } finally { wrapper.unmount() }
})

function mountPicker(projects: any[], current?: any) {
  const fetchWithAuth = vi.fn(async () => ({ ok: true, json: async () => ({ projects }) }) as Response)
  vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth }))
  const store = useSessionStore()
  if (current) store.setSelectedProject(current)
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/:p(.*)*', component: { template: '<div />' } },
  ] })
  const Host = defineComponent({
    components: { ProjectSelectorModal },
    setup: () => {
      const open = ref(false)
      setTimeout(() => { open.value = true })
      return { open }
    },
    template: '<ProjectSelectorModal v-model="open" />',
  })
  const wrapper = mount(Host, { attachTo: document.body, global: { plugins: [router] } })
  return { wrapper, router, fetchWithAuth, store }
}

async function opened() {
  await new Promise(r => setTimeout(r))
  await flushPromises()
}

it('shows logos, descriptions and git state from the overview', async () => {
  const { wrapper, fetchWithAuth } = mountPicker([
    { id: 'a', name: 'Alpha', path: '/a', display_path: '~/a', icon_url: '/api/projects/a/icon?v=1',
      summary: 'A music app.', git: { is_repo: true, branch: 'main', dirty: true }, running_count: 2 },
    { id: 'b', name: 'Beta', path: '/b', description: 'Own words.', summary: 'From README.' },
  ])
  try {
    await opened()
    expect(fetchWithAuth.mock.calls[0]![0]).toBe('/api/projects/overview?git=0')
    const [alpha, beta] = wrapper.findAll('.project-item')
    expect(alpha!.get('img').attributes('src')).toBe('/api/projects/a/icon?v=1')
    expect(alpha!.text()).toContain('A music app.')
    expect(alpha!.text()).toContain('main')
    expect(alpha!.find('.ps-dirty').exists()).toBe(true)
    expect(alpha!.text()).toContain('2 running')
    expect(beta!.find('img').exists()).toBe(false)
    expect(beta!.get('.ps-initials').text()).toBe('BE')
    // The user's own description wins over the README's.
    expect(beta!.text()).toContain('Own words.')
    // A logo that fails to load falls back to initials.
    await alpha!.get('img').trigger('error')
    expect(wrapper.findAll('.project-item')[0]!.find('img').exists()).toBe(false)
  } finally { wrapper.unmount() }
})

it('ranks name matches first, highlights them and searches descriptions', async () => {
  const { wrapper } = mountPicker([
    { id: 'x', name: 'Website', path: '/w', summary: 'Music player site' },
    { id: 'y', name: 'mymusic', path: '/m' },
    { id: 'z', name: 'Other', path: '/o' },
  ])
  try {
    await opened()
    await wrapper.get('input').setValue('music')
    const names = wrapper.findAll('.project-name').map(n => n.text())
    expect(names).toEqual(['mymusic', 'Website'])
    expect(wrapper.get('.project-name mark').text()).toBe('music')
    expect(wrapper.get('.project-item-focused').text()).toContain('mymusic')
  } finally { wrapper.unmount() }
})

it('cmd+Enter opens a new session in the project; New project opens the dialog', async () => {
  const { wrapper, router } = mountPicker([
    { id: 'a', name: 'Alpha', path: '/a' },
    { id: 'b', name: 'Beta', path: '/b' },
  ])
  try {
    await opened()
    await wrapper.get('input').trigger('keydown', { key: 'Enter', metaKey: true })
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ project: 'a', new_session: '1' })
  } finally { wrapper.unmount() }

  const second = mountPicker([{ id: 'a', name: 'Alpha', path: '/a' }])
  try {
    await opened()
    await second.wrapper.get('.ps-new').trigger('click')
    await flushPromises()
    expect(second.router.currentRoute.value.fullPath).toBe('/projects?new=1')
  } finally { second.wrapper.unmount() }
})

it('Clear selection drops the current project', async () => {
  const projects = [{ id: 'a', name: 'Alpha', path: '/a' }]
  const { wrapper, store } = mountPicker(projects, projects[0])
  try {
    await opened()
    await wrapper.findAll('.ps-new').find(b => b.text() === 'Clear selection')!.trigger('click')
    expect(store.selectedProject).toBeNull()
    expect(wrapper.find('.ps').exists()).toBe(false)
  } finally { wrapper.unmount() }
})

it('the header project button opens the shared picker', async () => {
  const { default: ProjectSelector } = await import('../../components/ProjectSelector.vue')
  vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth: vi.fn(async () => ({ json: async () => ({ projects: [] }) })) }))
  const open = vi.fn()
  const wrapper = mount(ProjectSelector, { global: { provide: { openProjectSelector: open } } })
  await wrapper.get('button').trigger('click')
  expect(open).toHaveBeenCalledOnce()
  wrapper.unmount()
})

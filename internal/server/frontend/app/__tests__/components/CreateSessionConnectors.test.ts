import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import CreateSessionModal from '../../components/agents/CreateSessionModal.vue'
import { useSessionActions } from '../../composables/agents/useSessionActions'
import {
  LAST_CONNECTORS_KEY,
  connectorsPayload,
  defaultConnectorSelection,
  loadLastConnectorSelection,
  saveLastConnectorSelection,
} from '../../utils/agents/connectorSelection'

const n0Connections = ref<any[]>([])
const n0Loaded = ref(false)
vi.mock('../../composables/useN0Connections', () => ({
  useN0Connections: () => ({
    connections: n0Connections,
    loaded: n0Loaded,
    fetchConnections: async () => { n0Loaded.value = true },
  }),
  n0ConnectionLabel: (c: any) => c?.app_name || '',
  n0HostLabel: (h: string) => h,
  pickDefaultN0Connection: (cs: any[]) => cs.find(c => c.status === 'active') || null,
}))
vi.mock('../../composables/useAgentRuntimes', () => ({
  useAgentRuntimes: () => ({ runtimes: ref([]), loaded: ref(true), anyReady: ref(true), load: vi.fn(), refresh: vi.fn(), fetchRuntimes: vi.fn() }),
}))
vi.mock('../../composables/useAuthenticatedFetch', () => ({
  useAuthenticatedFetch: () => ({ fetchWithAuth: (url: string, init?: any) => (globalThis.fetch as any)(url, init) }),
}))
vi.mock('../../composables/useStores', () => ({
  useSettings: () => ({ saveSessionDefaults: vi.fn(), getSessionDefaults: () => null }),
}))

function stubFetch() {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.startsWith('/api/connectors')) {
      return { ok: true, json: async () => ({ connectors: [
        { slug: 'github', name: 'GitHub', is_connected: true },
        { slug: 'runpod', name: 'RunPod', is_connected: true },
        { slug: 'n0', name: 'n0', is_connected: true },
        { slug: 'slack', name: 'Slack', is_connected: false },
      ] }) }
    }
    if (url.startsWith('/api/agent/validate-directory')) return { ok: true, json: async () => ({ valid: true }) }
    if (url.startsWith('/api/skills')) return { ok: true, json: async () => ({ skills: [] }) }
    return { ok: true, json: async () => ({}) }
  }))
}

function mountModal() {
  const formData: any = {
    workingDirectory: '/tmp/x', permissionMode: 'default', modelProvider: 'claude', model: 'sonnet',
    systemPrompt: '', promptMode: 'custom', selectedAgent: '', tools: [],
  }
  const wrapper = shallowMount(CreateSessionModal, {
    props: {
      show: true, formData, providers: [{ id: 'claude', name: 'Claude', icon: '', models: ['sonnet'] }],
      currentProvider: null, agents: [], selectedAgentPreview: null,
      loadingProviders: false, loadingAgents: false, creating: false,
    },
    global: { stubs: { Teleport: true, NuxtLink: true } },
  })
  return { wrapper, formData }
}

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  n0Connections.value = [{ id: 7, status: 'active', app_name: 'demo', host: 'https://nzero.pro' }]
  n0Loaded.value = false
  stubFetch()
})
afterEach(() => vi.unstubAllGlobals())

describe('connector selection helpers', () => {
  it('preselects everything but n0 without a saved choice', () => {
    expect(defaultConnectorSelection(['github', 'n0', 'runpod'], null)).toEqual(['github', 'runpod'])
  })
  it('restores a saved choice and gives newly connected ones the default', () => {
    const saved = { selected: ['n0'], offered: ['github', 'n0'] }
    expect(defaultConnectorSelection(['github', 'n0', 'runpod'], saved)).toEqual(['n0', 'runpod'])
    expect(defaultConnectorSelection(['github', 'n0'], { selected: [], offered: ['github', 'n0'] })).toEqual([])
  })
  it('round-trips through localStorage and survives bad data', () => {
    saveLastConnectorSelection(['github'], ['github', 'n0'])
    expect(loadLastConnectorSelection()).toEqual({ selected: ['github'], offered: ['github', 'n0'] })
    saveLastConnectorSelection([], [])
    expect(loadLastConnectorSelection()).toEqual({ selected: ['github'], offered: ['github', 'n0'] })
    localStorage.setItem(LAST_CONNECTORS_KEY, '{nope')
    expect(loadLastConnectorSelection()).toBeNull()
  })
  it('always produces an array payload', () => {
    expect(connectorsPayload(undefined)).toEqual([])
    expect(connectorsPayload([])).toEqual([])
    expect(connectorsPayload(['a', 3])).toEqual(['a'])
  })
})

describe('CreateSessionModal connectors', () => {
  it('does not preselect n0 without a saved choice, and hints at its tools', async () => {
    const { wrapper, formData } = mountModal()
    await flushPromises()
    expect(formData.connectors).toEqual(['github', 'runpod'])
    expect(formData.connectorsOffered).toEqual(['github', 'runpod', 'n0'])
    expect(wrapper.find('[data-testid="n0-tools-hint"]').exists()).toBe(true)
  })

  it('restores the saved choice, including n0 and an empty one', async () => {
    saveLastConnectorSelection(['n0'], ['github', 'runpod', 'n0'])
    const first = mountModal()
    await flushPromises()
    expect(first.formData.connectors).toEqual(['n0'])
    expect(first.formData.n0ConnectionId).toBe(7)

    saveLastConnectorSelection([], ['github', 'runpod', 'n0'])
    const second = mountModal()
    await flushPromises()
    expect(second.formData.connectors).toEqual([])
  })
})

describe('create_session payload', () => {
  function actions(send: ReturnType<typeof vi.fn>) {
    const r = <T>(v: T) => ref(v) as any
    return useSessionActions({
      agentWs: { connected: true, send },
      sessions: r([]), activeSessionId: r(null), messages: r({}), messagesLoaded: r(new Set()),
      showCreateSessionModal: r(true), showResumeModal: r(false), selectedResumeSession: r(null),
      availableSessions: r([]), loadingSessions: r(false), creatingSession: r(false), resumingSession: r(false),
      sessionPermissions: r(new Map()), awaitingToolResults: r(new Set()), todoHideTimers: r(new Map()),
      sessionToolStats: r(new Map()), sessionPermissionStats: r(new Map()), isUserNearBottom: r(true),
      sessionForm: r({}), resumeForm: r({}), availableAgents: r([]), selectedAgentPreview: r(null),
      loadingAgents: r(false), availableProviders: r([]), currentProvider: r(null), loadingProviders: r(false),
      projectAreas: r([]), currentProjectId: r(null), fetchProjectAreas: async () => {},
      scrollToBottom: () => {}, focusMessageInput: () => {}, cleanupSessionData: () => {},
    } as any)
  }
  const form = (extra: any) => ({
    workingDirectory: '/tmp/x', permissionMode: 'default', modelProvider: 'claude', model: 'sonnet',
    promptMode: 'custom', systemPrompt: '', tools: [], selectedAvatarId: 1, ...extra,
  })

  it('sends an empty connectors array when none are selected', async () => {
    const send = vi.fn()
    await actions(send).createSessionWithOptions(form({ connectors: [], connectorsOffered: ['github', 'n0'] }))
    const msg = send.mock.calls.find(c => c[0].type === 'create_session')![0]
    expect(msg.options.connectors).toEqual([])
    expect(msg.options.n0_connection_id).toBeUndefined()
    expect(loadLastConnectorSelection()).toEqual({ selected: [], offered: ['github', 'n0'] })
  })

  it('sends connectors even when the form never set them', async () => {
    const send = vi.fn()
    await actions(send).createSessionWithOptions(form({}))
    const msg = send.mock.calls.find(c => c[0].type === 'create_session')![0]
    expect(msg.options).toHaveProperty('connectors')
    expect(msg.options.connectors).toEqual([])
  })

  it('sends the selection and the n0 connection', async () => {
    const send = vi.fn()
    await actions(send).createSessionWithOptions(form({ connectors: ['github', 'n0'], n0ConnectionId: 7, connectorsOffered: ['github', 'n0'] }))
    const msg = send.mock.calls.find(c => c[0].type === 'create_session')![0]
    expect(msg.options.connectors).toEqual(['github', 'n0'])
    expect(msg.options.n0_connection_id).toBe(7)
  })
})

describe('opening with loop settings', () => {
  it('shows a loop proposal\'s settings instead of overwriting them', async () => {
    const formData: any = {
      workingDirectory: '/tmp/x', permissionMode: 'yolo', modelProvider: 'claude', model: 'sonnet',
      systemPrompt: '', promptMode: 'custom', selectedAgent: '', tools: [],
    }
    const props = {
      show: false, formData, providers: [{ id: 'claude', name: 'Claude', icon: '', models: ['sonnet'] }],
      currentProvider: null, agents: [], selectedAgentPreview: null,
      loadingProviders: false, loadingAgents: false, creating: false,
    }
    const wrapper = shallowMount(CreateSessionModal, { props, global: { stubs: { Teleport: true, NuxtLink: true } } })
    await flushPromises()
    // "Edit first" fills the shared form, then opens the dialog.
    Object.assign(formData, { sessionMode: 'loop', loopGoal: 'make tests pass', loopVerifyCommand: 'go test ./...', loopMaxIterations: 20, loopTimeoutMinutes: 90 })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(formData).toMatchObject({ sessionMode: 'loop', loopGoal: 'make tests pass', loopVerifyCommand: 'go test ./...', loopMaxIterations: 20, loopTimeoutMinutes: 90 })
    const vm = wrapper.vm as any
    expect(vm.sessionMode).toBe('loop')
    expect(vm.loopGoal).toBe('make tests pass')

    // Closed, then a New Session with a reset form (no loop fields): defaults, not the proposal.
    await wrapper.setProps({ show: false })
    for (const key of ['sessionMode', 'loopGoal', 'loopVerifyCommand', 'loopMaxIterations', 'loopTimeoutMinutes']) delete formData[key]
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(vm.sessionMode).toBe('interactive')
    expect(vm.loopGoal).toBe('')
    expect(formData).toMatchObject({ sessionMode: 'interactive', loopGoal: '', loopVerifyCommand: '', loopMaxIterations: 10, loopTimeoutMinutes: 30 })
  })
})

describe('compact session launcher', () => {
  it('starts compact, expands settings without creating, and submits the selected provider/model', async () => {
    const { wrapper, formData } = mountModal()
    await flushPromises()
    expect(wrapper.get('#session-settings').attributes('style')).toContain('display: none')
    await wrapper.get('.settings-toggle').trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('create')).toBeUndefined()
    await wrapper.get('.settings-toggle').trigger('click')
    expect(wrapper.get('#session-settings').attributes('style') || '').not.toContain('display: none')
    await wrapper.setProps({ providers: [{ id: 'codex', name: 'Codex', icon: '', models: ['astra'] }] })
    const picker = wrapper.findComponent({ name: 'SessionModelPicker' })
    picker.vm.$emit('select', { provider: 'codex', model: 'astra' })
    await flushPromises()
    picker.vm.$emit('launch')
    expect(formData).toMatchObject({ modelProvider: 'codex', model: 'astra' })
    expect(wrapper.emitted('create')).toHaveLength(1)
    wrapper.unmount()
  })

  it('blocks keyboard creation during loading, validation failure, and creation', async () => {
    const { wrapper } = mountModal()
    const picker = wrapper.findComponent({ name: 'SessionModelPicker' })
    picker.vm.$emit('launch')
    expect(wrapper.emitted('create')).toBeUndefined()
    await flushPromises()
    await wrapper.setProps({ creating: true })
    picker.vm.$emit('launch')
    expect(wrapper.emitted('create')).toBeUndefined()
    await wrapper.setProps({ creating: false, loadingProviders: true })
    picker.vm.$emit('launch')
    expect(wrapper.emitted('create')).toBeUndefined()
    await wrapper.setProps({ loadingProviders: false })
    await wrapper.get('#working-directory').setValue('')
    picker.vm.$emit('launch')
    expect(wrapper.emitted('create')).toBeUndefined()
    wrapper.unmount()
  })
})

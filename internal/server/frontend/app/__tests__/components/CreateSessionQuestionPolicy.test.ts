import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import CreateSessionModal from '../../components/agents/CreateSessionModal.vue'
import { useSessionActions } from '../../composables/agents/useSessionActions'
import CustomDropdown from '../../components/ui/CustomDropdown.vue'

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
  useAgentRuntimes: () => ({ runtimes: ref([]), loaded: ref(true), anyReady: ref(true), refresh: vi.fn(), fetchRuntimes: vi.fn(), load: vi.fn() }),
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
    if (url.startsWith('/api/skills')) return { ok: true, json: async () => ({ skills: [] }) }
    if (url === '/api/agent/question-policy') return { ok: true, json: async () => ({ policy: { mode: 'wait' }, is_default: false }) }
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
const createMessage = async (extra: any) => {
  const send = vi.fn()
  await actions(send).createSessionWithOptions(form(extra))
  return send.mock.calls.find(c => c[0].type === 'create_session')![0]
}

describe('Codex network consent in session creation', () => {
  it('does not send a network grant by default', async () => {
    const msg = await createMessage({ modelProvider: 'codex', sandboxed: true })
    expect(msg.options.codex_network_access).toBeUndefined()
  })

  it('sends explicit consent with the filesystem sandbox enabled', async () => {
    const msg = await createMessage({ modelProvider: 'codex', codexNetworkAccess: true })
    expect(msg.options.codex_network_access).toBe(true)
    expect(msg.options.sandboxed).toBe(true)
  })

  it('never sends a stale grant to a different provider', async () => {
    const msg = await createMessage({ modelProvider: 'claude', codexNetworkAccess: true })
    expect(msg.options.codex_network_access).toBeUndefined()
  })

  it('never re-enables a sandbox the user explicitly turned off', async () => {
    const msg = await createMessage({ modelProvider: 'codex', sandboxed: false, codexNetworkAccess: true })
    expect(msg.options.codex_network_access).toBeUndefined()
    expect(msg.options.sandboxed).toBe(false)
  })

  it('resets consent when opening the next new session', async () => {
    const { wrapper, formData } = mountModal()
    await flushPromises()
    formData.codexNetworkAccess = true
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    expect(formData.codexNetworkAccess).toBe(false)
    wrapper.unmount()
  })
})

describe('question policy in Create Session', () => {
  it('defaults to the global policy and names it', async () => {
    const { wrapper, formData } = mountModal()
    await flushPromises()
    const dd = wrapper.get('[data-testid="question-policy-select"]')
    const dropdown = wrapper.findAllComponents(CustomDropdown).find(c => c.attributes('data-testid') === 'question-policy-select')!
    expect(dd.exists()).toBe(true)
    expect(dropdown.props('modelValue')).toBe('default')
    expect((dropdown.props('options') as any[])[0].label).toBe('Default (wait for me)')
    dropdown.vm.$emit('update:modelValue', 'auto_after:300')
    expect(formData.questionPolicy).toBe('auto_after:300')
  })

  it('is hidden for Codex', async () => {
    const { wrapper, formData } = mountModal()
    formData.modelProvider = 'codex'
    await wrapper.setProps({ formData: { ...formData } })
    expect(wrapper.find('[data-testid="question-policy-select"]').exists()).toBe(false)
  })

  it('sends nothing for the default and the chosen policy otherwise', async () => {
    expect((await createMessage({})).options).not.toHaveProperty('question_policy')
    expect((await createMessage({ questionPolicy: 'default' })).options).not.toHaveProperty('question_policy')
    expect((await createMessage({ questionPolicy: 'wait' })).options.question_policy).toEqual({ mode: 'wait' })
    expect((await createMessage({ questionPolicy: 'auto_after:300' })).options.question_policy).toEqual({ mode: 'auto_after', timeout_seconds: 300 })
    expect((await createMessage({ questionPolicy: 'auto', modelProvider: 'codex', model: 'gpt-6-astra' })).options).not.toHaveProperty('question_policy')
  })
})

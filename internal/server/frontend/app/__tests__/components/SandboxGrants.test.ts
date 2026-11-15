import { ref, defineComponent, h } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import SandboxGrants from '../../components/agents/SandboxGrants.vue'
import SessionMetrics from '../../components/SessionMetrics.vue'
import { useSessionActions } from '../../composables/agents/useSessionActions'
import { GRANTS_STORAGE_KEY, grantsPayload, grantsSummary, readSavedGrants } from '../../utils/agents/sandboxGrants'

const { load } = vi.hoisted(() => ({ load: vi.fn() }))
vi.mock('../../composables/useSandboxSettings', () => ({ useSandboxSettings: () => ({ load }) }))
vi.mock('../../composables/useStores', () => ({
  useSettings: () => ({ saveSessionDefaults: vi.fn(), getSessionDefaults: () => null }),
}))
vi.mock('../../stores/projects/projectSubscriptionsStore', () => ({ useProjectSubscriptionsStore: () => ({ getProjectGitStatus: () => null, subscribeToProject: vi.fn(), unsubscribeFromProject: vi.fn() }) }))

const grants = [
  { id: 'ssh', title: 'SSH keys', description: 'Use the keys loaded in your ssh-agent.', risk: 'The agent can use those keys, but cannot read a key file.', available: false, reason: 'No ssh-agent is running' },
  { id: 'github', title: 'GitHub', description: 'Sign in to GitHub with your gh login.', risk: 'The agent can act as you on GitHub.', available: true },
  { id: 'cloud', title: 'Cloud credentials', description: 'Make ~/.aws readable.', risk: 'The agent can read and use those credentials.', available: true },
  { id: 'npm', title: 'npm login', description: 'Make ~/.npmrc readable.', risk: 'The agent can publish packages as you.', available: true },
  { id: 'localhost', title: 'Local servers', description: 'Start dev servers.', risk: 'It also reaches every other service on this Mac\'s localhost, Eggify included.', available: true },
]

beforeEach(() => {
  localStorage.clear()
  load.mockReset()
  load.mockResolvedValue({ default_enabled: true, allowed_domains: [], default_allowed_domains: [], grants })
})

// A parent that holds the choice the way the Create Session form does.
function mountHost(props: Record<string, unknown> = {}) {
  const state = { grantIds: ref<string[]>([]), paths: ref<{ path: string; write: boolean }[]>([]) }
  const Host = defineComponent({
    setup: () => () => h(SandboxGrants, {
      sandboxed: true, provider: 'claude', projectKey: 'proj-1', ...props,
      grantIds: state.grantIds.value, paths: state.paths.value,
      'onUpdate:grantIds': (v: string[]) => { state.grantIds.value = v },
      'onUpdate:paths': (v: { path: string; write: boolean }[]) => { state.paths.value = v },
    }),
  })
  return { wrapper: mount(Host), state }
}

describe('SandboxGrants list', () => {
  it('renders every grant from the API with its description and risk', async () => {
    const { wrapper } = mountHost()
    await flushPromises()
    const text = wrapper.get('[data-testid="sandbox-grants"]').text()
    expect(text).toContain('Give this session access to')
    for (const g of grants) expect(text).toContain(g.title)
    expect(text).toContain('Sign in to GitHub with your gh login.')
    expect(text).toContain('The agent can act as you on GitHub.')
    expect(text).toContain('Eggify included')
    expect(wrapper.findAll('input[type="checkbox"]')).toHaveLength(5)
    wrapper.unmount()
  })

  it('disables an unavailable grant and says why', async () => {
    const { wrapper } = mountHost()
    await flushPromises()
    const ssh = wrapper.get<HTMLInputElement>('[data-testid="grant-ssh"]')
    expect(ssh.element.disabled).toBe(true)
    expect(wrapper.get('[data-testid="grant-reason-ssh"]').text()).toContain('No ssh-agent is running')
    expect(wrapper.text()).not.toContain('cannot read a key file')
    expect(wrapper.get<HTMLInputElement>('[data-testid="grant-github"]').element.disabled).toBe(false)
    wrapper.unmount()
  })

  it('is never pre-checked for a project without a saved choice', async () => {
    const { wrapper, state } = mountHost()
    await flushPromises()
    expect(state.grantIds.value).toEqual([])
    for (const g of grants) expect(wrapper.get<HTMLInputElement>(`[data-testid="grant-${g.id}"]`).element.checked).toBe(false)
    wrapper.unmount()
  })

  it('emits the choice and remembers it per project', async () => {
    const { wrapper, state } = mountHost()
    await flushPromises()
    await wrapper.get('[data-testid="grant-github"]').setValue(true)
    await wrapper.get('[data-testid="grant-localhost"]').setValue(true)
    expect(state.grantIds.value).toEqual(['github', 'localhost'])
    await wrapper.get('[data-testid="grant-path-add"]').trigger('click')
    await wrapper.get('[data-testid="grant-path-input"]').setValue('~/data')
    await wrapper.get('[data-testid="grant-path-mode"]').setValue('write')
    expect(state.paths.value).toEqual([{ path: '~/data', write: true }])
    expect(readSavedGrants('proj-1')).toEqual({ grants: ['github', 'localhost'], paths: [{ path: '~/data', write: true }] })
    expect(readSavedGrants('other-project')).toBeNull()
    wrapper.unmount()

    // The next dialog for the same project starts from the remembered choice;
    // another project starts empty.
    const again = mountHost()
    await flushPromises()
    expect(again.state.grantIds.value).toEqual(['github', 'localhost'])
    expect(again.state.paths.value).toEqual([{ path: '~/data', write: true }])
    again.wrapper.unmount()
    const other = mountHost({ projectKey: 'proj-2' })
    await flushPromises()
    expect(other.state.grantIds.value).toEqual([])
    other.wrapper.unmount()
  })

  it('drops a remembered grant that is unavailable now', async () => {
    localStorage.setItem(GRANTS_STORAGE_KEY, JSON.stringify({ 'proj-1': { grants: ['ssh', 'npm'], paths: [] } }))
    const { wrapper, state } = mountHost()
    await flushPromises()
    expect(state.grantIds.value).toEqual(['npm'])
    wrapper.unmount()
  })

  it('adds and removes folder rows, at most ten', async () => {
    const { wrapper, state } = mountHost()
    await flushPromises()
    for (let i = 0; i < 12; i++) await wrapper.get('[data-testid="grant-path-add"]').trigger('click')
    expect(state.paths.value).toHaveLength(10)
    expect(wrapper.get<HTMLButtonElement>('[data-testid="grant-path-add"]').element.disabled).toBe(true)
    await wrapper.findAll('[data-testid="grant-path-remove"]')[0]!.trigger('click')
    expect(state.paths.value).toHaveLength(9)
    expect(wrapper.get<HTMLButtonElement>('[data-testid="grant-path-add"]').element.disabled).toBe(false)
    wrapper.unmount()
  })

  it('is hidden, and holds nothing, while the sandbox is off', async () => {
    localStorage.setItem(GRANTS_STORAGE_KEY, JSON.stringify({ 'proj-1': { grants: ['npm'], paths: [{ path: '~/x', write: false }] } }))
    const { wrapper, state } = mountHost({ sandboxed: false })
    await flushPromises()
    expect(wrapper.find('[data-testid="sandbox-grants"]').exists()).toBe(false)
    expect(state.grantIds.value).toEqual([])
    expect(state.paths.value).toEqual([])
    wrapper.unmount()
  })

  it('follows the global default when the toggle is unset', async () => {
    load.mockResolvedValue({ default_enabled: false, grants })
    const { wrapper } = mountHost({ sandboxed: null })
    await flushPromises()
    expect(wrapper.find('[data-testid="sandbox-grants"]').exists()).toBe(false)
    wrapper.unmount()
    load.mockResolvedValue({ default_enabled: true, grants })
    const on = mountHost({ sandboxed: null })
    await flushPromises()
    expect(on.wrapper.find('[data-testid="sandbox-grants"]').exists()).toBe(true)
    on.wrapper.unmount()
  })

  it('is hidden for Codex and OpenAI-compatible sessions', async () => {
    for (const provider of ['codex', 'ollama']) {
      const { wrapper } = mountHost({ provider })
      await flushPromises()
      expect(wrapper.find('[data-testid="sandbox-grants"]').exists(), provider).toBe(false)
      wrapper.unmount()
    }
  })

  it('clears the choice when the sandbox is switched off or the runtime changes', async () => {
    const wrapper = mount(SandboxGrants, { props: { sandboxed: true, provider: 'claude', projectKey: '', grantIds: ['npm'], paths: [{ path: '~/x', write: false }] } })
    await flushPromises()
    await wrapper.setProps({ grantIds: ['npm'], paths: [{ path: '~/x', write: false }] })
    await wrapper.setProps({ sandboxed: false })
    expect((wrapper.emitted('update:grantIds') as any[]).at(-1)).toEqual([[]])
    expect((wrapper.emitted('update:paths') as any[]).at(-1)).toEqual([[]])
    await wrapper.setProps({ sandboxed: true, grantIds: ['npm'], paths: [] })
    await wrapper.setProps({ provider: 'codex' })
    expect((wrapper.emitted('update:grantIds') as any[]).at(-1)).toEqual([[]])
    wrapper.unmount()
  })

  it('survives unavailable localStorage', async () => {
    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })
    const getSpy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    const { wrapper, state } = mountHost()
    await flushPromises()
    await wrapper.get('[data-testid="grant-npm"]').setValue(true)
    expect(state.grantIds.value).toEqual(['npm'])
    spy.mockRestore()
    getSpy.mockRestore()
    wrapper.unmount()
  })
})

describe('grants payload', () => {
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
  const createMessage = async (extra: any) => {
    setActivePinia(createPinia())
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => ({ valid: true }) })))
    const send = vi.fn()
    await actions(send).createSessionWithOptions({
      workingDirectory: '/tmp/x', permissionMode: 'default', modelProvider: 'claude', model: 'claude-sonnet-5-5',
      promptMode: 'custom', systemPrompt: '', tools: [], selectedAvatarId: 1, ...extra,
    })
    vi.unstubAllGlobals()
    return send.mock.calls.find(c => c[0].type === 'create_session')![0]
  }

  it('goes out as sandbox_grants and sandbox_grant_paths on an explicitly sandboxed session', async () => {
    const msg = await createMessage({ sandboxGrants: ['github', 'localhost'], sandboxGrantPaths: [{ path: ' ~/data ', write: true }, { path: '  ', write: false }] })
    expect(msg.options.sandbox_grants).toEqual(['github', 'localhost'])
    expect(msg.options.sandbox_grant_paths).toEqual([{ path: '~/data', write: true }])
    expect(msg.options.sandboxed).toBe(true)
  })

  it('sends nothing without a choice', async () => {
    const msg = await createMessage({ sandboxGrants: [], sandboxGrantPaths: [] })
    expect(msg.options).not.toHaveProperty('sandbox_grants')
    expect(msg.options).not.toHaveProperty('sandbox_grant_paths')
    expect(grantsPayload({ grants: [], paths: [] })).toEqual({})
  })
})

describe('Permissions panel', () => {
  const mountMetrics = (options: Record<string, unknown>) => mount(SessionMetrics, {
    props: { session: { id: 'test', status: 'idle', message_count: 0, options } },
    global: { stubs: { Icon: true, ProjectPermissions: true, GitStatus: true } },
  })

  beforeEach(() => setActivePinia(createPinia()))

  it('lists the grants next to "Sandboxed", with the folders below', () => {
    const wrapper = mountMetrics({ sandboxed: true, sandbox_grants: ['ssh', 'github'], sandbox_grant_paths: [{ path: '/Users/x/data', write: true }] })
    expect(wrapper.get('[data-testid="sandbox-grants-summary"]').text()).toContain('SSH keys, GitHub, 1 folder or file')
    expect(wrapper.get('[data-testid="sandbox-grant-paths"]').text()).toContain('read and write')
    expect(wrapper.text()).toContain('Sandboxed · SSH keys, GitHub, 1 folder or file')
    wrapper.unmount()
  })

  it('shows plain "Sandboxed" without grants and nothing when not sandboxed', () => {
    const plain = mountMetrics({ sandboxed: true })
    expect(plain.find('[data-testid="sandbox-grants-summary"]').exists()).toBe(false)
    expect(plain.text()).not.toContain('Sandboxed ·')
    plain.unmount()
    const off = mountMetrics({ sandboxed: false, sandbox_grants: ['ssh'] })
    expect(off.find('[data-testid="sandbox-grants-summary"]').exists()).toBe(false)
    off.unmount()
    expect(grantsSummary(null)).toBe('')
  })
})

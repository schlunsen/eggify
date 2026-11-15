import { ref } from 'vue'
import { mount, shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import CreateSessionModal from '../../components/agents/CreateSessionModal.vue'
import TeamSetupFields from '../../components/agents/TeamSetupFields.vue'
import { useSessionActions } from '../../composables/agents/useSessionActions'
import { TEAM_SETUP_KEY, defaultTeamSetup, offeredTeamSetup, type TeamSetup } from '../../utils/agents/team'
import { modelKey } from '../../utils/agents/modelShortcuts'

vi.mock('../../composables/useN0Connections', () => ({
  useN0Connections: () => ({ connections: ref([]), loaded: ref(true), fetchConnections: async () => {} }),
  n0ConnectionLabel: (c: any) => c?.app_name || '',
  n0HostLabel: (h: string) => h,
  pickDefaultN0Connection: () => null,
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

const providers = [
  { id: 'claude', name: 'Claude', icon: '', models: ['claude-fable-5-2', 'claude-sonnet-5-5'] },
  { id: 'deepseek', name: 'DeepSeek', icon: '', models: ['deepseek-flash'] },
  { id: 'codex', name: 'OpenAI Codex', icon: '', models: ['gpt-6-astra'] },
]

const complete: TeamSetup = {
  enabled: true,
  builder: { provider: 'deepseek', model: 'deepseek-flash' },
  reviewer: { provider: 'codex', model: 'gpt-6-astra' },
  maxReviewRounds: 2,
}

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  vi.stubGlobal('fetch', vi.fn(async (url: string) => ({ ok: true, json: async () => (String(url).startsWith('/api/agent/validate-directory') ? { valid: true } : {}) })))
})
afterEach(() => vi.unstubAllGlobals())

describe('TeamSetupFields', () => {
  const mountFields = (modelValue: TeamSetup) => mount(TeamSetupFields, { props: { modelValue, providers } })
  const lastUpdate = (wrapper: ReturnType<typeof mountFields>) => (wrapper.emitted('update:modelValue') as any[]).at(-1)[0] as TeamSetup

  it('shows only the switch while off', () => {
    const wrapper = mountFields(defaultTeamSetup())
    const toggle = wrapper.get('[data-testid="team-toggle"]')
    expect(toggle.attributes('role')).toBe('switch')
    expect(toggle.attributes('aria-checked')).toBe('false')
    expect(wrapper.find('[data-testid="team-builder"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="team-note"]').exists()).toBe(false)
  })

  it('turns on from the switch', async () => {
    const wrapper = mountFields(defaultTeamSetup())
    const toggle = wrapper.get('[data-testid="team-toggle"]')
    ;(toggle.element as HTMLInputElement).checked = true
    await toggle.trigger('change')
    expect(lastUpdate(wrapper).enabled).toBe(true)
  })

  it('offers every model by provider for the builder and the reviewer', () => {
    const wrapper = mountFields({ ...defaultTeamSetup(), enabled: true })
    for (const role of ['builder', 'reviewer']) {
      const select = wrapper.get(`[data-testid="team-${role}"]`)
      expect(select.findAll('optgroup').map(g => g.attributes('label'))).toEqual(['Claude', 'DeepSeek', 'OpenAI Codex'])
      expect(select.findAll('optgroup option').map(o => o.text())).toEqual(['claude-fable-5-2', 'claude-sonnet-5-5', 'deepseek-flash · V4.1 Flash', 'gpt-6-astra'])
      // Nothing picked yet: the placeholder.
      expect((select.element as HTMLSelectElement).value).toBe('')
    }
    expect(wrapper.get('[data-testid="team-note"]').text()).toContain('start without asking, sandboxed, on these models')
    expect(wrapper.get('[data-testid="team-problem"]').text()).toBe('Pick a model for the builder.')
  })

  it('picks a provider and model per role', async () => {
    const wrapper = mountFields({ ...defaultTeamSetup(), enabled: true })
    await wrapper.get('[data-testid="team-builder"]').setValue(modelKey({ provider: 'deepseek', model: 'deepseek-flash' }))
    expect(lastUpdate(wrapper).builder).toEqual({ provider: 'deepseek', model: 'deepseek-flash' })
    await wrapper.get('[data-testid="team-reviewer"]').setValue(modelKey({ provider: 'codex', model: 'gpt-6-astra' }))
    expect(lastUpdate(wrapper).reviewer).toEqual({ provider: 'codex', model: 'gpt-6-astra' })
  })

  it('shows a complete setup without a problem', () => {
    const wrapper = mountFields(complete)
    expect((wrapper.get('[data-testid="team-builder"]').element as HTMLSelectElement).value).toBe(modelKey(complete.builder))
    expect((wrapper.get('[data-testid="team-rounds"]').element as HTMLInputElement).value).toBe('2')
    expect(wrapper.find('[data-testid="team-problem"]').exists()).toBe(false)
  })

  it('keeps review rounds between 1 and 5', async () => {
    const wrapper = mountFields(complete)
    const rounds = wrapper.get('[data-testid="team-rounds"]')
    await rounds.setValue('9')
    expect(lastUpdate(wrapper).maxReviewRounds).toBe(5)
    await rounds.setValue('0')
    expect(lastUpdate(wrapper).maxReviewRounds).toBe(3)
    await rounds.setValue('4')
    expect(lastUpdate(wrapper).maxReviewRounds).toBe(4)
  })

  it('Enter in a picker never bubbles up to create the session', async () => {
    const onEnter = vi.fn()
    const wrapper = mount({ components: { TeamSetupFields }, template: '<div @keydown.enter="onEnter"><TeamSetupFields :model-value="value" :providers="providers" /></div>', setup: () => ({ onEnter, value: complete, providers }) })
    await wrapper.get('[data-testid="team-builder"]').trigger('keydown', { key: 'Enter' })
    await wrapper.get('[data-testid="team-toggle"]').trigger('keydown', { key: 'Enter' })
    expect(onEnter).not.toHaveBeenCalled()
  })
})

describe('offeredTeamSetup', () => {
  it('clears a member whose model is no longer offered', () => {
    const stale = { ...complete, builder: { provider: 'deepseek', model: 'deepseek-gone' } }
    expect(offeredTeamSetup(stale, providers)).toEqual({ ...complete, builder: { provider: '', model: '' } })
    expect(offeredTeamSetup(complete, providers)).toEqual(complete)
  })
})

describe('Team in Create Session', () => {
  function mountModal(extra: Record<string, unknown> = {}) {
    const formData: any = {
      workingDirectory: '/tmp/x', permissionMode: 'default', modelProvider: 'claude', model: 'claude-fable-5-2',
      systemPrompt: '', promptMode: 'custom', selectedAgent: '', tools: [], ...extra,
    }
    const wrapper = shallowMount(CreateSessionModal, {
      props: {
        show: true, formData, providers,
        currentProvider: null, agents: [], selectedAgentPreview: null,
        loadingProviders: false, loadingAgents: false, creating: false,
      },
      global: { stubs: { Teleport: true, NuxtLink: true } },
    })
    return { wrapper, formData }
  }

  it('starts off, with the last models and rounds remembered', async () => {
    localStorage.setItem(TEAM_SETUP_KEY, JSON.stringify(complete))
    const { wrapper, formData } = mountModal()
    await flushPromises()
    const fields = wrapper.getComponent(TeamSetupFields)
    expect(fields.props('modelValue')).toEqual({ ...complete, enabled: false })
    expect(formData.team).toEqual({ ...complete, enabled: false })
    wrapper.unmount()
  })

  it('drops a remembered model no provider offers any more', async () => {
    localStorage.setItem(TEAM_SETUP_KEY, JSON.stringify({ ...complete, reviewer: { provider: 'codex', model: 'gpt-gone' } }))
    const { wrapper, formData } = mountModal()
    await flushPromises()
    expect(formData.team.reviewer).toEqual({ provider: '', model: '' })
    expect(formData.team.builder).toEqual(complete.builder)
    wrapper.unmount()
  })

  it('cannot be created until both models are picked', async () => {
    const { wrapper, formData } = mountModal()
    await flushPromises()
    const create = () => wrapper.get('.btn-create')
    expect(create().attributes('disabled')).toBeUndefined()
    wrapper.getComponent(TeamSetupFields).vm.$emit('update:modelValue', { ...defaultTeamSetup(), enabled: true })
    await flushPromises()
    expect(formData.team.enabled).toBe(true)
    expect(create().attributes('disabled')).toBeDefined()
    wrapper.getComponent(TeamSetupFields).vm.$emit('update:modelValue', complete)
    await flushPromises()
    expect(create().attributes('disabled')).toBeUndefined()
    await create().trigger('click')
    expect((wrapper.emitted('create') as any[])[0][0].team).toEqual(complete)
    wrapper.unmount()
  })

  it('is switched off again when the next dialog opens', async () => {
    const { wrapper, formData } = mountModal()
    await flushPromises()
    wrapper.getComponent(TeamSetupFields).vm.$emit('update:modelValue', complete)
    await flushPromises()
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(formData.team.enabled).toBe(false)
    wrapper.unmount()
  })

  it('locks the worktree switch on while the team is on', async () => {
    const { wrapper } = mountModal()
    await flushPromises()
    expect(wrapper.find('[data-testid="team-worktree-locked"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="team-worktree-note"]').exists()).toBe(false)
    wrapper.getComponent(TeamSetupFields).vm.$emit('update:modelValue', complete)
    await flushPromises()
    const locked = wrapper.get('[data-testid="team-worktree-locked"]')
    expect((locked.element as HTMLInputElement).checked).toBe(true)
    expect(locked.attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="team-worktree-note"]').text()).toContain('its own git worktree')
    expect(wrapper.find('.worktree-branch-input input').attributes('placeholder')).toContain('team/')
    wrapper.unmount()
  })

  it('is not offered for a session an agent proposed', async () => {
    const { wrapper } = mountModal({ spawnedBySessionId: 'parent-1' })
    await flushPromises()
    expect(wrapper.findComponent(TeamSetupFields).exists()).toBe(false)
    wrapper.unmount()
  })
})

describe('options.team on create_session', () => {
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
    const send = vi.fn()
    await actions(send).createSessionWithOptions({
      workingDirectory: '/tmp/x', permissionMode: 'default', modelProvider: 'claude', model: 'claude-fable-5-2',
      promptMode: 'custom', systemPrompt: '', tools: [], selectedAvatarId: 1, ...extra,
    })
    return send.mock.calls.find(c => c[0].type === 'create_session')![0]
  }

  it('sends the team and remembers it', async () => {
    const msg = await createMessage({ team: complete })
    expect(msg.options.team).toEqual({ builder: complete.builder, reviewer: complete.reviewer, max_review_rounds: 2 })
    // The orchestrator keeps its own model.
    expect(msg.options.model).toBe('claude-fable-5-2')
    expect(JSON.parse(localStorage.getItem(TEAM_SETUP_KEY)!)).toEqual(complete)
  })

  it("sends a typed branch name as the team's branch", async () => {
    expect((await createMessage({ team: complete, worktreeBranch: 'team/changelog' })).options.worktree_branch).toBe('team/changelog')
    expect((await createMessage({ team: complete })).options).not.toHaveProperty('worktree_branch')
    // Without a team a branch name only counts with the worktree switch on.
    expect((await createMessage({ worktreeBranch: 'x' })).options).not.toHaveProperty('worktree_branch')
  })

  it('sends nothing while the team is off, incomplete, or for a proposed session', async () => {
    expect((await createMessage({})).options).not.toHaveProperty('team')
    expect((await createMessage({ team: { ...complete, enabled: false } })).options).not.toHaveProperty('team')
    expect((await createMessage({ team: { ...complete, reviewer: { provider: '', model: '' } } })).options).not.toHaveProperty('team')
    expect((await createMessage({ team: complete, spawnedBySessionId: 'parent-1' })).options).not.toHaveProperty('team')
    expect(localStorage.getItem(TEAM_SETUP_KEY)).toBeNull()
  })
})

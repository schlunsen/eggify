import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import MetricsSidebar from '../../components/agents/MetricsSidebar.vue'
import { getEventBus } from '../../stores/events/eventBus'

vi.mock('../../composables/useN0Connections', () => ({
  useN0Connections: () => ({ connections: ref([]), loaded: ref(true) }),
  n0ConnectionLabel: () => ''
}))
vi.mock('../../composables/useN0Bindings', () => ({
  useN0Bindings: () => ({ getBinding: () => null, refresh: vi.fn() })
}))
// The sidebar's Questions row loads the global question policy.
vi.mock('../../composables/agents/useQuestionPolicy', () => ({
  useQuestionPolicy: () => ({ globalPolicy: ref(null), loadGlobal: vi.fn(async () => null), saveSession: vi.fn() })
}))
// …and its Skills row the session's skills (this test counts fetch calls).
vi.mock('../../composables/agents/useUsageLimit', async () => {
  const { ref } = await import('vue')
  return { useUsageLimit: () => ref(null), useCodexUsage: () => ref(null) }
})
vi.mock('../../composables/agents/useSessionSkills', () => ({
  useSessionSkills: () => ({ stateFor: () => undefined, load: vi.fn(async () => {}) })
}))

beforeEach(() => { setActivePinia(createPinia()) })

it('refreshes models when opened, sends the selection, and closes on acknowledgement', async () => {
  const fetchModels = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({ providers: [
      { id: 'claude', name: 'Claude', models: ['old', 'shared'] },
      { id: 'other', name: 'Other Provider', models: ['shared', 'second'], base_url: 'https://provider.example' }
    ] })
  })
  vi.stubGlobal('fetch', fetchModels)
  const showModal = vi.spyOn(HTMLDialogElement.prototype, 'showModal').mockImplementation(function () { this.open = true })
  const close = vi.spyOn(HTMLDialogElement.prototype, 'close').mockImplementation(function () {
    this.open = false
    this.dispatchEvent(new Event('close'))
  })
  const send = vi.fn().mockReturnValue(true)
  const session = { id: 'test-session', status: 'idle', options: { provider: 'claude', model: 'old' } }
  const wrapper = shallowMount(MetricsSidebar, {
    props: { show: true, session, connected: true, messageCount: 0, toolExecutions: [], permissionStats: {} },
    global: { provide: { agentWs: { send } }, stubs: { Teleport: true, NuxtLink: true } }
  })
  try {
    wrapper.vm.openModelSwitcher()
    await flushPromises()
    expect(showModal).toHaveBeenCalledOnce()
    const search = () => wrapper.get('input[role="combobox"]')
    expect(wrapper.findAll('.model-result')).toHaveLength(4)
    await search().trigger('keydown', { key: 'Enter' })
    expect(send).not.toHaveBeenCalled()
    await search().trigger('keydown', { key: 'ArrowDown' })
    await flushPromises()
    expect(search().attributes('aria-activedescendant')).toBe('model-result-1')
    await search().trigger('keydown', { key: 'ArrowUp' })
    expect(search().attributes('aria-activedescendant')).toBe('model-result-0')
    // A mousemove without movement (WebKit, after a scroll) leaves the keyboard highlight alone.
    await wrapper.findAll('.model-result')[2]!.trigger('mousemove', { movementX: 0, movementY: 0 })
    expect(search().attributes('aria-activedescendant')).toBe('model-result-0')
    await wrapper.findAll('.model-result')[2]!.trigger('mousemove', { movementX: 3, movementY: 0 })
    expect(search().attributes('aria-activedescendant')).toBe('model-result-2')
    await search().trigger('keydown', { key: 'ArrowUp' })
    await search().trigger('keydown', { key: 'ArrowUp' })
    await search().setValue('missing-model')
    expect(wrapper.text()).toContain('No models found')
    await search().trigger('keydown', { key: 'Enter' })
    expect(send).not.toHaveBeenCalled()
    await search().setValue('shared')
    expect(wrapper.findAll('.model-result')).toHaveLength(2)
    await search().setValue('other shared')
    expect(wrapper.findAll('.model-result')).toHaveLength(1)
    await search().trigger('keydown', { key: 'Enter', isComposing: true })
    expect(send).not.toHaveBeenCalled()
    await search().trigger('keydown', { key: 'Enter' })
    expect(send).toHaveBeenCalledWith({ type: 'change_session_model', session_id: 'test-session', provider: 'other', model: 'shared', base_url: 'https://provider.example' })
    expect(wrapper.emitted('model-switch-completed')).toBeUndefined()
    getEventBus().emit('session:model-changed', { sessionId: 'different-session' })
    await flushPromises()
    expect(wrapper.emitted('model-switch-completed')).toBeUndefined()
    getEventBus().emit('session:model-changed', { sessionId: 'test-session' })
    await flushPromises()
    expect(close).toHaveBeenCalled()
    expect((wrapper.get('dialog').element as HTMLDialogElement).open).toBe(false)
    expect(wrapper.emitted('model-switch-completed')).toEqual([['test-session']])
    wrapper.vm.openModelSwitcher()
    await flushPromises()
    expect(fetchModels).toHaveBeenCalledTimes(2)
    expect((search().element as HTMLInputElement).value).toBe('')
    await wrapper.get('dialog').trigger('cancel')
    await flushPromises()
    expect(wrapper.emitted('model-switch-completed')).toHaveLength(1)
    await wrapper.setProps({ session: { ...session, id: 'another', status: 'processing' } })
    await flushPromises()
    wrapper.vm.openModelSwitcher()
    await flushPromises()
    expect(fetchModels).toHaveBeenCalledTimes(2)
  } finally {
    wrapper.unmount()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  }
})

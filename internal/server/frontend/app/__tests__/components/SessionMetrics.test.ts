import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import SessionMetrics from '../../components/SessionMetrics.vue'
import { useUIStore } from '../../stores/ui/uiStore'

vi.mock('../../stores/projects/projectSubscriptionsStore', () => ({ useProjectSubscriptionsStore: () => ({ getProjectGitStatus: () => null, subscribeToProject: vi.fn(), unsubscribeFromProject: vi.fn() }) }))

beforeEach(() => { localStorage.clear(); setActivePinia(createPinia()) })

it.each([undefined, false, true])('shows the persisted Codex network permission (%s)', (network) => {
  const wrapper = mount(SessionMetrics, {
    props: { session: { id: 'test', status: 'idle', message_count: 0, options: { provider: 'codex', sandboxed: true, codex_network_access: network } } },
    global: { stubs: { Icon: true, ProjectPermissions: true, GitStatus: true } }
  })
  const text = wrapper.get('[data-testid="sandbox-metric"]').text()
  expect(text).toContain(network ? 'Network: unrestricted' : 'Network: disabled')
  expect(text).toContain('Start a new session to change network access')
  expect(text).not.toContain('limited to the allowlist')
  wrapper.unmount()
})

it('defaults to compact sections and counts executions rather than tool names', async () => {
  const wrapper = mount(SessionMetrics, { props: { session: { id: 'test', status: 'idle', message_count: 51, options: {} }, toolExecutions: { Bash: 4, Read: 2 } }, global: { stubs: { Icon: true, ProjectPermissions: true, GitStatus: true } } })
  const store = useUIStore()
  expect(store.expandedSections).toMatchObject({ toolsPermissions: false, activity: false })
  const activity = wrapper.findAll('button').find(button => button.text().includes('Activity'))!
  expect(activity.attributes('aria-expanded')).toBe('false')
  await activity.trigger('click')
  expect(activity.attributes('aria-expanded')).toBe('true')
  expect(wrapper.get('.tools-metric .metric-value').text()).toBe('6')
  wrapper.unmount()
})

it('sums up each collapsed row in one line', () => {
  const session = { id: 'test', status: 'idle', message_count: 3, options: { sandboxed: true, auto_handoff_after_messages: 80 } }
  const wrapper = mount(SessionMetrics, {
    props: { session, toolExecutions: { Bash: 1 }, permissionStats: { approved: 2, denied: 1, total: 3 } },
    global: { stubs: { Icon: true, ProjectPermissions: true, GitStatus: true } }
  })
  const summary = (title: string) => wrapper.findAll('.row-header').find(row => row.text().includes(title))!.get('.row-summary').text()
  expect(summary('Permissions')).toBe('Ask first · Sandboxed · Handoff at 80')
  expect(summary('Activity')).toBe('1 tool call · 1 denied')
  wrapper.unmount()
})

it('says when nothing has run yet', () => {
  const wrapper = mount(SessionMetrics, { props: { session: { id: 'test', status: 'idle', message_count: 0, options: { dangerously_skip_permissions: true } } }, global: { stubs: { Icon: true, ProjectPermissions: true, GitStatus: true } } })
  const summaries = wrapper.findAll('.row-header .row-summary').map(el => el.text())
  expect(summaries).toEqual(['Full access · Not sandboxed', 'No tool calls yet'])
  wrapper.unmount()
})

it('preserves saved sections and supplies defaults for the new activity section', () => {
  localStorage.setItem('cct:ui', JSON.stringify({ version: 1, data: { sectionOrder: ['gitStatus', 'sessionInfo', 'toolsPermissions'], expandedSections: { sessionInfo: false, gitStatus: true } } }))
  const store = useUIStore()
  expect(store.expandedSections.sessionInfo).toBe(false)
  expect(store.expandedSections.activity).toBe(false)
  expect(store.expandedSections.toolsPermissions).toBe(false)
})

it('keeps approval skipping off when confirmation is cancelled', async () => {
  const send = vi.fn()
  const originalConfirm = window.confirm
  const confirm = vi.fn(() => false)
  window.confirm = confirm
  const wrapper = mount(SessionMetrics, { props: { session: { id: 'test', status: 'idle', message_count: 0, options: {} } }, global: { provide: { agentWs: { send, off: vi.fn() } }, stubs: { Icon: true, ProjectPermissions: true, GitStatus: true } } })
  const checkbox = wrapper.get<HTMLInputElement>('input[aria-label="YOLO Mode"]')
  await checkbox.setValue(true)
  expect(confirm).toHaveBeenCalledOnce()
  expect(send).not.toHaveBeenCalled()
  expect(checkbox.element.checked).toBe(false)
  wrapper.unmount()
  window.confirm = originalConfirm
})

it('turns YOLO on once the server confirms it, also in a sandboxed Codex session with network', async () => {
  const { useSessionStore } = await import('../../stores/session/sessionStore')
  const send = vi.fn()
  const originalConfirm = window.confirm
  window.confirm = vi.fn(() => true)
  const store = useSessionStore()
  store.sessions = [{ id: 'yolo', status: 'idle', message_count: 0, options: { provider: 'codex', sandboxed: true, codex_network_access: true } }] as any
  const wrapper = mount(SessionMetrics, { props: { session: store.sessions[0] as any }, global: { provide: { agentWs: { send, off: vi.fn() } }, stubs: { Icon: true, ProjectPermissions: true, GitStatus: true } } })
  const checkbox = wrapper.get<HTMLInputElement>('input[aria-label="YOLO Mode"]')
  expect(checkbox.attributes('disabled')).toBeUndefined()
  await checkbox.setValue(true)
  expect(send).toHaveBeenCalledWith({ type: 'toggle_yolo_mode', session_id: 'yolo', enabled: true })
  // The server answers with yolo_mode_toggled; the page puts it in the store.
  store.setSessionYoloMode('yolo', true)
  await wrapper.vm.$nextTick()
  expect(checkbox.element.checked).toBe(true)
  expect(wrapper.text()).toContain('Enabled')
  // The network grant is untouched by the YOLO switch.
  expect((store.sessions[0] as any).options.codex_network_access).toBe(true)
  await checkbox.setValue(false)
  expect(send).toHaveBeenLastCalledWith({ type: 'toggle_yolo_mode', session_id: 'yolo', enabled: false })
  store.setSessionYoloMode('yolo', false)
  await wrapper.vm.$nextTick()
  expect(checkbox.element.checked).toBe(false)
  wrapper.unmount()
  window.confirm = originalConfirm
})

it('loads the session worktree after it changes instead of using project status', async () => {
  const { flushPromises } = await import('@vue/test-utils')
  let statusCalls = 0
  const fetchMock = vi.fn(async (url: string) => {
    const status = url.endsWith('git-status')
    if (status) statusCalls++
    return { ok: true, json: async () => status ? {
      branch: statusCalls === 1 ? 'main' : 'feature/live',
      worktree_path: statusCalls === 1 ? '' : '/repo/.worktrees/live',
      clean: false, staged: [], modified: ['changed.go'], untracked: [], deleted: []
    } : {} }
  })
  vi.stubGlobal('fetch', fetchMock)
  const session = { id: 'session', project_id: 'project', status: 'processing', message_count: 1, git_branch: 'main', options: { working_directory: '/repo' } }
  const wrapper = mount(SessionMetrics, { props: { session }, global: { stubs: { Icon: true, ProjectPermissions: true, GitStatus: true } } })
  await flushPromises()
  expect(wrapper.findComponent({ name: 'GitStatus' }).props('status').branch).toBe('main')
  await wrapper.setProps({ session: { ...session, options: { ...session.options, workspace: { working_directory: '/repo/.worktrees/live', worktree_path: '/repo/.worktrees/live', branch: 'feature/live' } } } })
  await flushPromises()
  const git = wrapper.findComponent({ name: 'GitStatus' })
  expect(git.props('worktreePath')).toBe('/repo/.worktrees/live')
  expect(git.props('status')).toMatchObject({ branch: 'feature/live', modified: ['changed.go'], clean: false })
  wrapper.unmount()
  vi.unstubAllGlobals()
})

it('ignores late Git responses from the previous workspace and clears stale status on errors', async () => {
  const { flushPromises } = await import('@vue/test-utils')
  let finishOld: (value: unknown) => void = () => {}
  const oldResponse = new Promise(resolve => { finishOld = resolve })
  let statusCalls = 0
  vi.stubGlobal('fetch', vi.fn((url: string) => {
    if (!url.endsWith('git-status')) return Promise.resolve({ ok: true, json: async () => ({}) })
    if (++statusCalls === 1) return oldResponse
    if (statusCalls === 2) return Promise.resolve({ ok: true, json: async () => ({ branch: 'feature/new', worktree_path: '/new', clean: true }) })
    return Promise.resolve({ ok: false, json: async () => ({ error: 'Worktree no longer exists' }) })
  }))
  const session = { id: 'session', status: 'processing', message_count: 1, git_branch: 'main', options: { working_directory: '/repo' } }
  const wrapper = mount(SessionMetrics, { props: { session }, global: { stubs: { Icon: true, ProjectPermissions: true, GitStatus: true } } })
  await wrapper.setProps({ session: { ...session, options: { ...session.options, workspace: { working_directory: '/new', worktree_path: '/new', branch: 'feature/new' } } } })
  await flushPromises()
  finishOld({ ok: true, json: async () => ({ branch: 'main', worktree_path: '', clean: true }) })
  await flushPromises()
  expect(wrapper.findComponent({ name: 'GitStatus' }).props('status').branch).toBe('feature/new')
  await wrapper.get('button[aria-label="Refresh Git status"]').trigger('click')
  await flushPromises()
  expect(wrapper.findComponent({ name: 'GitStatus' }).exists()).toBe(false)
  expect(wrapper.get('.git-error').text()).toContain('Worktree no longer exists')
  wrapper.unmount()
  vi.unstubAllGlobals()
})

it('stops replaying the update glow and the remote lookup while git status is unchanged', async () => {
  const { flushPromises } = await import('@vue/test-utils')
  vi.useFakeTimers()
  let branch = 'main'
  let remoteCalls = 0
  const fetchMock = vi.fn(async (url: string) => {
    if (url.endsWith('git-remote')) {
      remoteCalls++
      return { ok: true, json: async () => ({ html_url: 'https://github.com/o/r' }) }
    }
    return { ok: true, json: async () => ({
      branch, clean: true, staged: [], modified: [], untracked: [], deleted: []
    }) }
  })
  vi.stubGlobal('fetch', fetchMock)
  const session = { id: 'session', status: 'processing', message_count: 1, git_branch: 'main', options: { working_directory: '/repo' } }
  const wrapper = mount(SessionMetrics, { props: { session }, global: { stubs: { Icon: true, ProjectPermissions: true, GitStatus: true } } })
  try {
    await flushPromises()
    const glowing = () => wrapper.find('.git-status-updated').exists()
    expect(glowing()).toBe(false)
    expect(remoteCalls).toBe(1)

    // Three polls with an identical working tree must not touch the panel.
    for (let i = 0; i < 3; i++) {
      await vi.advanceTimersByTimeAsync(5000)
      await flushPromises()
    }
    expect(glowing()).toBe(false)
    expect(remoteCalls).toBe(1)

    // A real change still announces itself.
    branch = 'feature/live'
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(glowing()).toBe(true)
    expect(remoteCalls).toBe(2)
  } finally {
    wrapper.unmount()
    vi.useRealTimers()
    vi.unstubAllGlobals()
  }
})

describe('names the combination of prompts and sandbox', () => {
  const nameOf = (options: Record<string, unknown>) => {
    const wrapper = mount(SessionMetrics, { props: { session: { id: 'x', status: 'idle', message_count: 0, options } as any }, global: { stubs: { Icon: true, ProjectPermissions: true, GitStatus: true } } })
    const out = { name: wrapper.get('[data-testid="work-mode-name"]').text(), mode: wrapper.get('.yolo-mode-metric').attributes('data-work-mode'), full: wrapper.get('.yolo-mode-metric').classes().includes('full-access') }
    wrapper.unmount()
    return out
  }
  it('Autopilot: prompts off inside the sandbox', () => {
    expect(nameOf({ dangerously_skip_permissions: true, sandboxed: true })).toEqual({ name: 'Autopilot · sandboxed', mode: 'autopilot', full: false })
  })
  it('Full access: prompts off, no sandbox, in the warning colour', () => {
    expect(nameOf({ dangerously_skip_permissions: true, sandboxed: false })).toEqual({ name: 'Full access · not sandboxed', mode: 'full', full: true })
  })
  it('Ask first, sandboxed or not', () => {
    expect(nameOf({ sandboxed: true }).name).toBe('Ask first')
    expect(nameOf({}).name).toBe('Ask first')
  })
  it('Codex keeps the plain YOLO wording', () => {
    expect(nameOf({ provider: 'codex', dangerously_skip_permissions: true, sandboxed: true })).toMatchObject({ name: 'YOLO Mode', full: false })
  })
})

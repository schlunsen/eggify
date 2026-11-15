import { mount, flushPromises } from '@vue/test-utils'
import SubagentsPip from '../../components/agents/SubagentsPip.vue'
import TeamStrip from '../../components/agents/TeamStrip.vue'
import { useBackgroundAgents } from '../../composables/agents/useBackgroundAgents'
import { useWorkerActivity } from '../../composables/agents/useWorkerActivity'
import { normalizeSession } from '../../utils/agents/normalizeSession'
import { teamProgress, type TeamSession } from '../../utils/agents/team'
import { createWorkerAgentCache, workerAgent } from '../../utils/agents/workerAgent'

const auth = vi.hoisted(() => ({ fetchWithAuth: vi.fn() }))
vi.mock('../../composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => auth }))

const parentId = 'parent-session'
const iso = (msAgo: number) => new Date(Date.now() - msAgo).toISOString()
const builder = (status = 'processing'): TeamSession => ({
  id: 'worker-1', status, created_at: iso(60000), updated_at: iso(1000), model_name: 'claude-sonnet-5-5',
  options: { spawned_by_session_id: parentId, worker: { mode: 'build', task: 'Add the changelog page\nwith a modal', branch: 'worker/changelog' } },
})

beforeEach(() => {
  try { localStorage.clear() } catch { /* ignore */ }
  useBackgroundAgents().clearSessionAgents(parentId)
  useBackgroundAgents().resetListTracking()
  useWorkerActivity().resetWorkerActivity()
  auth.fetchWithAuth.mockReset()
  auth.fetchWithAuth.mockImplementation(async (url: string) => ({
    ok: true,
    json: async () => url.endsWith('/workers') ? { workers: [] } : { subagents: [] },
  }) as Response)
})

const nextFrame = () => new Promise(resolve => requestAnimationFrame(() => resolve(null)))

describe('normalizeSession', () => {
  it('keeps the worker and team fields the UI reads', () => {
    const team = { builder: { provider: 'claude', model: 'sonnet' }, reviewer: { provider: 'codex', model: 'gpt-6-astra' } }
    const worker = normalizeSession(builder())!
    expect(worker.options.spawned_by_session_id).toBe(parentId)
    expect(worker.options.worker.branch).toBe('worker/changelog')
    expect(normalizeSession({ id: 'p', options: { team, read_only: true } }).options).toMatchObject({ team, read_only: true })
    expect(normalizeSession({ id: 'p' }).options).toMatchObject({ worker: null, team: null, spawned_by_session_id: null, read_only: false })
  })
})

describe('workerAgent', () => {
  it('shows a working worker as a running agent and an idle one as done', () => {
    const running = workerAgent(builder(), ['TOOL:Bash: go test ./...'])!
    expect(running).toMatchObject({
      agent_id: 'worker-1', parent_session_id: parentId, status: 'running', subagent_type: 'build worker',
      description: 'Add the changelog page', model: 'claude-sonnet-5-5', last_output: 'TOOL:Bash: go test ./...',
    })
    expect(running.prompt).toContain('with a modal')
    const idle = workerAgent(builder('idle'), [])!
    expect(idle.status).toBe('completed')
    expect(idle.completed_at).toBe(idle.updated_at)
    expect(workerAgent({ id: 'plain', status: 'idle' }, [])).toBeNull()
  })

  it('keeps the same agent object while nothing changed', () => {
    const cache = createWorkerAgentCache()
    const session = builder()
    const lines = ['TOOL:Read: a.go']
    const first = cache.get(session, lines)
    expect(cache.get(session, lines)).toBe(first)
    expect(cache.get(session, [...lines, 'TOOL:Edit: a.go'])).not.toBe(first)
  })
})

describe('useWorkerActivity', () => {
  it('appends live lines and keeps live ones over kept lines from the server', () => {
    const activity = useWorkerActivity()
    activity.processWebSocketMessage({ type: 'worker_activity', session_id: parentId, worker_session_id: 'w', output: 'TOOL:Bash: ls', at: 5 })
    activity.processWebSocketMessage({ type: 'worker_activity', session_id: parentId, worker_session_id: 'w', lines: ['RESULT_START', 'a.go', 'RESULT_END'], lines_at: [6, 6, 6] })
    expect(activity.getWorkerLines('w')).toEqual(['TOOL:Bash: ls', 'RESULT_START', 'a.go', 'RESULT_END'])
    expect(activity.getWorkerTimes('w')).toEqual([5, 6, 6, 6])
    activity.applyKeptLines([{ session_id: 'w', lines: ['old'], lines_at: [1] }, { session_id: 'x', lines: ['kept'], lines_at: [2] }])
    expect(activity.getWorkerLines('w')[0]).toBe('TOOL:Bash: ls')
    expect(activity.getWorkerLines('x')).toEqual(['kept'])
  })

  it('loads a parent once', async () => {
    const activity = useWorkerActivity()
    await activity.loadWorkerActivity(parentId, auth.fetchWithAuth)
    await activity.loadWorkerActivity(parentId, auth.fetchWithAuth)
    expect(auth.fetchWithAuth).toHaveBeenCalledTimes(1)
    expect(auth.fetchWithAuth).toHaveBeenCalledWith(`/api/agent/sessions/${parentId}/workers`)
  })
})

describe('SubagentsPip with workers', () => {
  it('shows a working worker with its live action and opens its session from the modal', async () => {
    useWorkerActivity().processWebSocketMessage({ type: 'worker_activity', session_id: parentId, worker_session_id: 'worker-1', output: 'TOOL:Bash: go test ./internal/changelog' })
    const wrapper = mount(SubagentsPip, { props: { sessionId: parentId, show: true, workers: [builder()] } })
    await flushPromises()
    await nextFrame()

    expect(wrapper.get('.pip-title').text()).toBe('Agents')
    const row = wrapper.get('.pip-row')
    expect(row.get('.pip-name').text()).toBe('Add the changelog page')
    expect(row.get('.pip-now').text()).toContain('go test')
    expect(auth.fetchWithAuth).toHaveBeenCalledWith(`/api/agent/sessions/${parentId}/workers`)

    await row.trigger('click')
    const open = document.body.querySelector<HTMLButtonElement>('[data-testid="szm-open-session"]')
    expect(open).not.toBeNull()
    expect(document.body.querySelector('[data-testid="szm-open-view"]')).toBeNull()
    open!.click()
    await flushPromises()
    expect(wrapper.emitted('open-session')).toEqual([['worker-1']])
    wrapper.unmount()
  })

  it('follows new lines of the worker', async () => {
    const wrapper = mount(SubagentsPip, { props: { sessionId: parentId, show: true, workers: [builder()] } })
    await flushPromises()
    await nextFrame()
    useWorkerActivity().processWebSocketMessage({ type: 'worker_activity', session_id: parentId, worker_session_id: 'worker-1', output: 'TOOL:Edit: internal/changelog/changelog.go' })
    await flushPromises()
    await nextFrame()
    expect(wrapper.get('.pip-now').text()).toContain('changelog.go')
    wrapper.unmount()
  })

  it('stays Subagents without workers', async () => {
    useBackgroundAgents().processWebSocketMessage({ type: 'background_agents_list', session_id: parentId, agents: [
      { agent_id: 'a', parent_session_id: parentId, subagent_type: 'Explore', description: 'Look around', status: 'running', progress: 0, last_output: '', output_lines: 0, created_at: iso(1000), updated_at: iso(500) },
    ] } as any)
    const wrapper = mount(SubagentsPip, { props: { sessionId: parentId, show: true } })
    await flushPromises()
    await nextFrame()
    expect(wrapper.get('.pip-title').text()).toBe('Subagents')
    expect(auth.fetchWithAuth).not.toHaveBeenCalledWith(`/api/agent/sessions/${parentId}/workers`)
    wrapper.unmount()
  })
})

describe('TeamStrip Now line', () => {
  const parent: TeamSession = { id: parentId, status: 'idle', options: { team: { builder: { provider: 'claude', model: 'sonnet' }, reviewer: { provider: 'codex', model: 'gpt-6-astra' } } } }

  it("shows the working worker's latest action and opens it", async () => {
    const wrapper = mount(TeamStrip, { props: { progress: teamProgress(parent, [builder()]) } })
    expect(wrapper.find('[data-testid="team-now"]').exists()).toBe(false)
    useWorkerActivity().processWebSocketMessage({ type: 'worker_activity', session_id: parentId, worker_session_id: 'worker-1', output: 'TOOL:Bash: npm run build' })
    await flushPromises()
    const now = wrapper.get('[data-testid="team-now"]')
    expect(now.text()).toContain('npm run build')
    await now.trigger('click')
    expect(wrapper.emitted('open')).toEqual([['worker-1']])
  })

  it('says nothing when no worker works', () => {
    useWorkerActivity().processWebSocketMessage({ type: 'worker_activity', session_id: parentId, worker_session_id: 'worker-1', output: 'TOOL:Bash: ls' })
    const wrapper = mount(TeamStrip, { props: { progress: teamProgress(parent, [builder('idle')]) } })
    expect(wrapper.find('[data-testid="team-now"]').exists()).toBe(false)
  })
})

import { mount, flushPromises } from '@vue/test-utils'
import AllAgentsOverlay from '../../components/agents/AllAgentsOverlay.vue'
import { SESSION_TAGS_STORAGE_KEY } from '../../utils/sessionTags'

const auth = vi.hoisted(() => ({ fetchWithAuth: vi.fn() }))
vi.mock('../../composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => auth }))

const minutesAgo = (m: number) => new Date(Date.now() - m * 60000).toISOString()
const sessions = [
  { id: 'aaaaaaaa-1', status: 'processing', created_at: minutesAgo(120), updated_at: minutesAgo(1), selected_avatar: { id: 7, name: 'Julian', color: '#123456', image_path: '/avatars/julian.png' }, model_name: 'opus', project_id: 'p1', git_branch: 'main' },
  { id: 'bbbbbbbb-2', status: 'idle', created_at: minutesAgo(60), updated_at: minutesAgo(2), pending_permissions: 1, selected_avatar: { id: 8, name: 'Ricky', color: '#654321', image_path: '/avatars/ricky.png' } },
  { id: 'cccccccc-3', status: 'idle', created_at: minutesAgo(90), updated_at: minutesAgo(30), selected_avatar: { id: 9, name: 'Bubbles', color: '#abcdef', image_path: '/avatars/bubbles.png' } }
]
const subagents = [
  { agent_id: 'run-1', parent_session_id: 'aaaaaaaa-1', subagent_type: 'general-purpose', description: 'Fix the tests', status: 'running', progress: 0, last_output: '', output_lines: 1,
    created_at: minutesAgo(5), updated_at: minutesAgo(0), recent_output: ['TOOL:Bash: go test ./...'] },
  { agent_id: 'kid-1', parent_session_id: 'aaaaaaaa-1', subagent_type: 'code-reviewer', description: 'Review the fix', status: 'running', progress: 0, last_output: '', output_lines: 0,
    created_at: minutesAgo(1), updated_at: minutesAgo(0), parent_agent_id: 'run-1', recent_output: [] },
  { agent_id: 'done-3', parent_session_id: 'cccccccc-3', subagent_type: 'Explore', description: 'Map the API', status: 'completed', progress: 1, last_output: 'Found it', output_lines: 1,
    created_at: minutesAgo(40), updated_at: minutesAgo(35), completed_at: minutesAgo(35), recent_output: ['Found it'] },
  { agent_id: 'fail-3', parent_session_id: 'cccccccc-3', subagent_type: 'general-purpose', description: 'Broken work', status: 'failed', progress: 0, last_output: '', output_lines: 0,
    created_at: minutesAgo(40), updated_at: minutesAgo(32), completed_at: minutesAgo(32), error_message: 'Agent failed: exit status 1', recent_output: [] }
]

function respond(body: unknown) {
  return { ok: true, json: async () => structuredClone(body) } as Response
}

beforeEach(() => {
  try { localStorage.clear() } catch { /* ignore */ }
  localStorage.setItem(SESSION_TAGS_STORAGE_KEY, JSON.stringify({ 'aaaaaaaa-1': 'auth, flaky tests' }))
  auth.fetchWithAuth.mockReset()
  auth.fetchWithAuth.mockImplementation(async (url: string) => {
    if (url.startsWith('/api/agent/sessions?')) return respond({ sessions })
    if (url.startsWith('/api/agent/subagents?')) return respond({ subagents })
    if (url === '/api/projects') return respond({ projects: [{ id: 'p1', name: 'Eggify' }] })
    if (url.endsWith('/subagents')) return respond({ subagents: subagents.filter(a => url.includes(a.parent_session_id)) })
    if (url === '/api/agent/overview/digests') return respond({ digests: { 'aaaaaaaa-1': { summary: 'Fixing the flaky Go tests', tags: ['tests'] } } })
    return { ok: false, json: async () => ({}) } as Response
  })
})

function mountOverlay() {
  return mount(AllAgentsOverlay, { attachTo: document.body, props: { show: true }, global: { stubs: { Teleport: true } } })
}
type Wrapper = ReturnType<typeof mountOverlay>
const treeText = (w: Wrapper) => w.findAll('.aa-group, .aa-row').map(el =>
  el.classes('aa-group') ? `# ${el.text()}` : el.classes('aa-session') ? el.get('.aa-name').text() : el.classes('aa-more') ? `+ ${el.text()}` : `  ${el.get('.aa-name').text()}`)
const press = (w: Wrapper, key: string) => w.get('.all-agents').trigger('keydown', { key })
const selectedName = (w: Wrapper) => w.get('.aa-selected').text()

it('loads every session and subagent, and shows working ones in a tree by default', async () => {
  const w = mountOverlay()
  await flushPromises()
  expect(auth.fetchWithAuth).toHaveBeenCalledWith('/api/agent/sessions?status=all')
  expect(auth.fetchWithAuth.mock.calls.some(([url]) => String(url).startsWith('/api/agent/subagents?status=all&since='))).toBe(true)

  const stats = w.get('.aa-stats').text()
  expect(stats).toContain('1 session working')
  expect(stats).toContain('2 subagents running')
  expect(stats).toContain('1 finished in the last hour')
  expect(stats).toContain('1 failed in the last hour')

  // Running filter: Needs you, then Working with its subagents nested.
  expect(treeText(w)).toEqual(['# Needs you 1', 'Ricky', '# Working 1', 'Julian', '  Fix the tests', '  Review the fix'])
  const julian = w.findAll('.aa-session')[1]!
  expect(julian.findAll('.aa-tag').map(t => t.text())).toEqual(['auth', 'flaky tests'])
  expect(julian.get('.aa-avatar img').attributes('src')).toBe('/avatars/julian.png')
  expect(w.findAll('.aa-agent')[1]!.attributes('aria-level')).toBe('3')
  expect(w.findAll('.aa-agent')[0]!.get('.aa-now').text()).toBe('$go test ./...')
  w.unmount()
})

it('filters: All adds recent sessions with finished subagents folded, Failed shows failures, text matches', async () => {
  const w = mountOverlay()
  await flushPromises()
  await w.get('.aa-filter-all').trigger('click')
  expect(treeText(w)).toEqual(['# Needs you 1', 'Ricky', '# Working 1', 'Julian', '  Fix the tests', '  Review the fix', '# Recent 1', 'Bubbles', '+ 2 finished'])
  await w.get('.aa-more').trigger('click')
  expect(treeText(w).slice(-3)).toEqual(['+ 2 finished', '  Broken work', '  Map the API'])

  await w.get('.aa-filter-failed').trigger('click')
  expect(treeText(w)).toEqual(['# Recent 1', 'Bubbles', '  Broken work'])

  await w.get('.aa-filter-all').trigger('click')
  await w.get('.aa-search').setValue('review')
  expect(treeText(w)).toEqual(['# Working 1', 'Julian', '  Fix the tests', '  Review the fix'])
  await w.get('.aa-search').setValue('flaky')
  expect(treeText(w)).toEqual(['# Working 1', 'Julian', '  Fix the tests', '  Review the fix'])
  await w.get('.aa-search').setValue('zzz')
  expect(w.get('.aa-empty h2').text()).toBe('Nothing matches this filter')
  w.unmount()
})

it('moves with the arrow keys, folds with ←/→ and opens with Enter', async () => {
  const w = mountOverlay()
  await flushPromises()
  expect(selectedName(w)).toContain('Ricky')
  // A session shows its summary, model, project and an Open button.
  await press(w, 'ArrowDown')
  await flushPromises()
  expect(selectedName(w)).toContain('Julian')
  const detail = w.get('.aa-session-detail')
  expect(detail.get('.aa-digest').text()).toBe('Fixing the flaky Go tests')
  expect(detail.text()).toContain('opus')
  expect(detail.text()).toContain('Eggify')
  expect(detail.text()).toContain('2 running')

  await press(w, 'ArrowLeft')
  expect(treeText(w)).toEqual(['# Needs you 1', 'Ricky', '# Working 1', 'Julian'])
  await press(w, 'ArrowRight')
  expect(treeText(w)).toHaveLength(6)
  await press(w, 'ArrowRight') // into the first child
  await press(w, 'ArrowDown')
  await flushPromises()
  expect(selectedName(w)).toContain('Review the fix')
  // A subagent is shown with the session's own detail view.
  expect(w.get('.sd-title').text()).toBe('Review the fix')
  expect(w.get('.sd').text()).toContain('Fix the tests') // launched by

  await press(w, 'Enter')
  expect(w.emitted('open')![0]![0]).toMatchObject({ session: { id: 'aaaaaaaa-1' }, agentId: 'kid-1' })
  await press(w, 'ArrowLeft') // back to the parent subagent
  expect(selectedName(w)).toContain('Fix the tests')
  await press(w, 'Home')
  await press(w, 'Enter')
  expect(w.emitted('open')![1]![0]).toEqual({ session: expect.objectContaining({ id: 'bbbbbbbb-2' }) })
  w.unmount()
})

it('shows a compact egg loader until the first list arrives', async () => {
  let release: () => void = () => {}
  auth.fetchWithAuth.mockImplementation(() => new Promise(resolve => { release = () => resolve(respond({ sessions: [], subagents: [] })) }))
  const w = mountOverlay()
  await flushPromises()
  expect(w.find('.aa-empty .egg-loader.is-compact').exists()).toBe(true)
  release()
  w.unmount()
})

it('shows a session kept going by a cron job in Working, with the job above its subagents and in the detail pane', async () => {
  const cronSession = {
    id: 'dddddddd-4', status: 'idle', created_at: minutesAgo(300), updated_at: minutesAgo(200),
    selected_avatar: { id: 10, name: 'J-Roc', color: '#ff8800', image_path: '/avatars/jroc.png' },
    scheduled_jobs: [{
      id: '8841f567', cron: '*/30 * * * *', schedule: 'Every 30 minutes', recurring: true,
      prompt: 'check TestFlight review status for build 2.20\nreport to the user',
      created_at: minutesAgo(100), expires_at: new Date(Date.now() + 6 * 86400e3).toISOString(),
      next_at: new Date(Date.now() + 14 * 60000).toISOString()
    }]
  }
  auth.fetchWithAuth.mockImplementation(async (url: string) => {
    if (url.startsWith('/api/agent/sessions?')) return respond({ sessions: [cronSession] })
    if (url.startsWith('/api/agent/subagents?')) return respond({ subagents: [] })
    if (url === '/api/projects') return respond({ projects: [] })
    return { ok: false, json: async () => ({}) } as Response
  })
  const w = mountOverlay()
  await flushPromises()
  expect(w.get('.aa-stats').text()).toContain('1 loop scheduled')
  expect(w.get('.aa-stats').text()).toContain('1 session working')
  expect(treeText(w)).toEqual(['# Working 1', 'J-Roc', '  check TestFlight review status for build 2.20'])
  expect(w.get('.aa-session .aa-status').text()).toBe('Idle · loop next in 14 min')
  const loopRow = w.get('.aa-loop')
  expect(loopRow.text()).toContain('Every 30 minutes')
  expect(loopRow.text()).toContain('Scheduled job · next in 14 min')
  expect(loopRow.get('.aa-time').text()).toBe('14m')

  press(w, 'ArrowDown')
  await flushPromises()
  const detail = w.get('.aa-loop-detail')
  expect(detail.text()).toContain('*/30 * * * *')
  expect(detail.text()).toContain('report to the user')
  await detail.get('.aa-open').trigger('click')
  expect(w.emitted('open')?.[0]?.[0]).toMatchObject({ session: { id: 'dddddddd-4' } })
  w.unmount()
})

it('hangs worker sessions under the session that started them; Enter opens the worker', async () => {
  const lead = { id: 'eeeeeeee-5', status: 'idle', created_at: minutesAgo(200), updated_at: minutesAgo(20), selected_avatar: { id: 11, name: 'Lahey', color: '#333333', image_path: '/avatars/lahey.png' } }
  const builder = {
    id: 'ffffffff-6', status: 'processing', created_at: minutesAgo(40), updated_at: minutesAgo(1), model_name: 'claude-sonnet-5-5',
    options: { spawned_by_session_id: lead.id, worker: { mode: 'build', task: 'Add the export button', branch: 'worker/export', base_branch: 'main', review_rounds: 1, max_review_rounds: 3, last_verdict: 'changes_requested' } },
  }
  const reviewer = {
    id: 'gggggggg-7', status: 'idle', created_at: minutesAgo(20), updated_at: minutesAgo(10), model_name: 'gpt-6-astra',
    options: { spawned_by_session_id: lead.id, read_only: true, worker: { mode: 'review', task: 'Review the export button', reviews_session_id: builder.id } },
  }
  const helper = { agent_id: 'help-1', parent_session_id: builder.id, subagent_type: 'Explore', description: 'Find the reports page', status: 'running', progress: 0, last_output: '', output_lines: 0,
    created_at: minutesAgo(2), updated_at: minutesAgo(0), recent_output: [] }
  auth.fetchWithAuth.mockImplementation(async (url: string) => {
    if (url.startsWith('/api/agent/sessions?')) return respond({ sessions: [lead, builder, reviewer] })
    if (url.startsWith('/api/agent/subagents?')) return respond({ subagents: [helper] })
    if (url === '/api/projects') return respond({ projects: [] })
    if (url.endsWith('/subagents')) return respond({ subagents: url.includes(builder.id) ? [helper] : [] })
    return { ok: false, json: async () => ({}) } as Response
  })
  localStorage.setItem('eggify-all-agents-filter', 'all')
  const w = mountOverlay()
  await flushPromises()

  // The workers are not session rows of their own.
  expect(treeText(w)).toEqual(['# Working 1', 'Lahey', '  Build worker', '  Find the reports page', '  Review worker'])
  expect(w.get('.aa-session .aa-status').text()).toBe('Idle · 1 worker working')
  const [build, review] = w.findAll('.aa-worker')
  expect(build!.classes()).toContain('aa-worker-working')
  expect(build!.get('[data-testid="subagent-model"]').text()).toBe('Sonnet 5.5')
  expect(build!.get('.aa-now').text()).toBe('Working · branch worker/export · 1 subagent running')
  expect(build!.attributes('aria-level')).toBe('2')
  expect(review!.get('.aa-worker-verdict').text()).toBe('Changes requested')
  expect(review!.get('.aa-now').text()).toBe('Idle · read-only')
  // Its subagent sits one level below the worker.
  expect(w.get('.aa-agent').attributes('aria-level')).toBe('3')

  await press(w, 'ArrowDown')
  await flushPromises()
  expect(selectedName(w)).toContain('Build worker')
  const detail = w.get('.aa-worker-detail')
  expect(detail.text()).toContain('Add the export button')
  expect(detail.text()).toContain('Lahey')
  expect(detail.text()).toContain('worker/export')
  expect(detail.text()).toContain('round 1 of 3')
  await press(w, 'Enter')
  expect(w.emitted('open')?.[0]?.[0]).toEqual({ session: expect.objectContaining({ id: builder.id }) })

  // The worker's subagent opens in the worker's session.
  await press(w, 'ArrowDown')
  await flushPromises()
  expect(w.get('.aa-detail-session').text()).toBe('Build worker')
  await press(w, 'Enter')
  expect(w.emitted('open')?.[1]?.[0]).toEqual({ session: expect.objectContaining({ id: builder.id }), agentId: 'help-1' })

  // ← goes from a worker back to its parent.
  await press(w, 'ArrowDown')
  expect(selectedName(w)).toContain('Review worker')
  await press(w, 'ArrowLeft')
  await flushPromises()
  expect(selectedName(w)).toContain('Lahey')
  w.unmount()
})

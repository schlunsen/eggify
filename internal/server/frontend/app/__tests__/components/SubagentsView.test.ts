import { mount, flushPromises } from '@vue/test-utils'
import { setHomeDir } from '../../utils/homeDir'
import SubagentsView from '../../components/agents/SubagentsView.vue'
import { useBackgroundAgents } from '../../composables/agents/useBackgroundAgents'

const auth = vi.hoisted(() => ({ fetchWithAuth: vi.fn() }))
vi.mock('../../composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => auth }))

beforeEach(() => setHomeDir('/Users/me'))

const session = 'session-1'
const earlier = new Date(Date.now() - 60000).toISOString()
const muchEarlier = new Date(Date.now() - 120000).toISOString()
const subagents = [
  { agent_id: 'done', parent_session_id: session, subagent_type: 'Explore', description: 'Map the API', status: 'completed', progress: 1,
    last_output: '## Summary\n\nThe handler lives in **routes/agent.go**. It validates input.\n\n- detail one\n- detail two', output_lines: 3,
    created_at: earlier, updated_at: earlier, completed_at: earlier, recent_output: ['TOOL:Grep: HandleGet', 'RESULT_START', 'routes/agent.go:29', 'RESULT_END', 'Found the handler'] },
  { agent_id: 'run', parent_session_id: session, subagent_type: 'general-purpose', description: 'Fix the tests', status: 'running', progress: 0, last_output: '', output_lines: 1,
    created_at: muchEarlier, updated_at: new Date().toISOString(), background: true, recent_output: ['TOOL:Read: /app/internal/server/ZenMode.vue', 'TOOL:Bash: go test ./...'] },
  { agent_id: 'child', parent_session_id: session, subagent_type: 'reviewer', description: 'Review the fix', status: 'running', progress: 0, last_output: '', output_lines: 0,
    created_at: new Date().toISOString(), updated_at: new Date().toISOString(), parent_agent_id: 'run', recent_output: [] },
  { agent_id: 'restart', parent_session_id: session, subagent_type: 'general-purpose', description: 'Old work', status: 'failed', progress: 0, last_output: '', output_lines: 0,
    created_at: muchEarlier, updated_at: muchEarlier, error_message: 'App restarted while agent was running', recent_output: ['TOOL:Edit: /a/b.ts'] },
  { agent_id: 'broken', parent_session_id: session, subagent_type: 'general-purpose', description: 'Broken work', status: 'failed', progress: 0, last_output: '', output_lines: 0,
    created_at: muchEarlier, updated_at: muchEarlier, error_message: 'Agent failed: exit status 1', recent_output: [] }
]

beforeEach(() => {
  try { localStorage.clear() } catch { /* ignore */ }
  useBackgroundAgents().clearSessionAgents(session)
  auth.fetchWithAuth.mockImplementation(async () => ({ ok: true, json: async () => ({ subagents: structuredClone(subagents) }) }) as Response)
})

function mountView(props: Record<string, unknown> = {}) {
  return mount(SubagentsView, { attachTo: document.body, props: { show: true, sessionId: session, sessionName: 'Julian', ...props }, global: { stubs: { Teleport: true } } })
}
const nextFrame = () => new Promise(resolve => requestAnimationFrame(() => resolve(null)))
const rows = (wrapper: ReturnType<typeof mountView>) => wrapper.findAll('.sv-row')
const rowTitles = (wrapper: ReturnType<typeof mountView>) => rows(wrapper).map(r => r.get('.sv-row-title').text())
const row = (wrapper: ReturnType<typeof mountView>, title: string) => rows(wrapper).find(r => r.get('.sv-row-title').text() === title)!

it('lists running then finished subagents: live commands, and only the selected one’s result', async () => {
  const wrapper = mountView()
  await flushPromises()
  expect(auth.fetchWithAuth).toHaveBeenCalledWith(`/api/agent/sessions/${session}/subagents`)
  expect(wrapper.get('h1').text()).toBe('Subagents 5')
  expect(wrapper.get('.sv-sum-running').text()).toBe('2 running')
  // Running first, oldest first; then finished, newest first.
  expect(wrapper.findAll('.sv-group-title').map(h => h.text())).toEqual(['Running 2', 'Finished 3'])
  expect(rowTitles(wrapper)).toEqual(['Fix the tests', 'Review the fix', 'Map the API', 'Old work', 'Broken work'])

  const fix = row(wrapper, 'Fix the tests')
  expect(fix.get('.sv-row-line').text()).toBe('$go test ./...')
  expect(fix.get('.sv-row-time').attributes('title')).toBe('2 steps')
  expect(row(wrapper, 'Review the fix').get('.sv-row-line').text()).toBe('Starting…')
  // Finished rows are one line until selected.
  for (const title of ['Map the API', 'Old work', 'Broken work']) expect(row(wrapper, title).find('.sv-row-line').exists()).toBe(false)
  await row(wrapper, 'Map the API').trigger('click')
  expect(row(wrapper, 'Map the API').get('.sv-row-line').text()).toBe('The handler lives in routes/agent.go. It validates input.')
  await row(wrapper, 'Old work').trigger('click')
  expect(row(wrapper, 'Old work').get('.sv-row-line').text()).toContain('Eggify restarted')
  expect(row(wrapper, 'Map the API').find('.sv-row-line').exists()).toBe(false)
  await row(wrapper, 'Broken work').trigger('click')
  expect(row(wrapper, 'Broken work').get('.sv-row-line').text()).toBe('exit status 1')
  wrapper.unmount()
})

it('shows the first running subagent with its command now, key steps, and every step on request', async () => {
  const wrapper = mountView()
  await flushPromises()
  expect(rows(wrapper)[0]!.attributes('aria-selected')).toBe('true')
  const detail = wrapper.get('.sd')
  expect(detail.get('.sd-title').text()).toBe('Fix the tests')
  expect(detail.get('.sd-pill').text()).toBe('Running')
  expect(detail.get('.sd-now .sd-cmd-big').text()).toBe('$go test ./...')
  expect(detail.get('.sd-ago').text()).toBe('just now')
  // A Read and a test run change nothing: no key steps yet.
  expect(detail.get('#sd-activity-title').text()).toBe('Key steps 0 of 2')
  expect(detail.findAll('.sd-step')).toHaveLength(0)
  expect(detail.get('.sd-empty').text()).toContain('Nothing changed yet')
  await detail.findAll('.sd-chip').find(c => c.text() === 'All steps')!.trigger('click')
  expect(detail.get('#sd-activity-title').text()).toBe('All steps 2')
  const steps = detail.findAll('.sd-step')
  expect(steps.map(s => s.get('.sd-step-tool').text())).toEqual(['Shell', 'Read'])
  expect(steps[0]!.classes()).toContain('sd-step-current')
  expect(steps[1]!.get('.sd-path').text()).toBe('/app/internal/server/ZenMode.vue')
  // Shell only hides the Read.
  await detail.findAll('.sd-chip').find(c => c.text() === 'Shell only')!.trigger('click')
  expect(detail.findAll('.sd-step')).toHaveLength(1)
  wrapper.unmount()
})

it('shows a finished subagent’s report, full report and step output on request', async () => {
  const wrapper = mountView()
  await flushPromises()
  await row(wrapper, 'Map the API').trigger('click')
  const detail = wrapper.get('.sd')
  expect(detail.get('.sd-pill').text()).toBe('Done')
  expect(detail.get('.sd-md').html()).toContain('<strong>routes/agent.go</strong>')
  await detail.get('.sd-report .sd-link').trigger('click')
  expect(detail.get('.sd-md').text()).toContain('detail two')
  await detail.findAll('.sd-chip').find(c => c.text() === 'All steps')!.trigger('click')
  // The closing text is the report, so it is not repeated as a step.
  expect(detail.findAll('.sd-note')).toHaveLength(0)
  const grep = detail.get('.sd-step')
  expect(grep.find('.sd-result').exists()).toBe(false)
  await grep.get('.sd-step-row').trigger('click')
  expect(grep.get('.sd-result').text()).toBe('routes/agent.go:29')
  wrapper.unmount()
})

it('follows live output once per frame, completion, and filters by status', async () => {
  const wrapper = mountView()
  await flushPromises()
  const { processWebSocketMessage } = useBackgroundAgents()
  processWebSocketMessage({ type: 'background_agent_output', session_id: session, agent_id: 'child', output: 'TOOL:Read: /app/a.ts' })
  processWebSocketMessage({ type: 'background_agent_output', session_id: session, agent_id: 'child', output: 'TOOL:Bash: cd /Users/me/projects/app && npm test' })
  processWebSocketMessage({ type: 'background_agent_completed', session_id: session, agent_id: 'run', final_output: 'Tests pass now.' })
  await nextFrame()
  await flushPromises()
  expect(row(wrapper, 'Review the fix').get('.sv-row-line').text()).toBe('$npm test')
  // Finished and not selected: one line.
  expect(row(wrapper, 'Fix the tests').find('.sv-row-line').exists()).toBe(false)
  // The selection follows the first running agent while nothing was picked.
  expect(wrapper.get('.sd-title').text()).toBe('Review the fix')
  expect(wrapper.get('.sd-now .sd-cwd').text()).toBe('in ~/projects/app')

  const chips = wrapper.findAll('.sv-filters button')
  expect(chips.map(c => c.text())).toEqual(['All 5', 'Running 1', 'Done 2', 'Failed 1', 'Interrupted 1'])
  await chips[4]!.trigger('click')
  expect(rowTitles(wrapper)).toEqual(['Old work'])
  expect(wrapper.get('.sd-problem p').text()).toContain('Eggify restarted')
  await chips[1]!.trigger('click')
  expect(rowTitles(wrapper)).toEqual(['Review the fix'])
  wrapper.unmount()
})

it('marks an agent failed with an interruption message as Interrupted live', async () => {
  const wrapper = mountView()
  await flushPromises()
  useBackgroundAgents().processWebSocketMessage({ type: 'background_agent_failed', session_id: session, agent_id: 'child', error_message: 'Agent stopped', interrupted: true })
  await nextFrame()
  await flushPromises()
  await row(wrapper, 'Review the fix').trigger('click')
  expect(wrapper.get('.sd-pill').text()).toBe('Interrupted')
  expect(wrapper.get('.sd-problem p').text()).toBe('It was stopped before it finished.')
  wrapper.unmount()
})

it('moves the selection with the arrow keys', async () => {
  // Real Teleport: the stub re-creates its content on every render, which would drop focus.
  const wrapper = mount(SubagentsView, { attachTo: document.body, props: { show: true, sessionId: session, sessionName: 'Julian' } })
  await flushPromises()
  const domRows = () => [...document.querySelectorAll<HTMLElement>('.sv-row')]
  const title = () => document.querySelector('.sd-title')?.textContent
  domRows()[0]!.focus()
  domRows()[0]!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
  await flushPromises()
  expect(title()).toBe('Review the fix')
  expect(document.activeElement).toBe(domRows()[1])
  domRows()[1]!.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true }))
  await flushPromises()
  expect(title()).toBe('Broken work')
  expect(document.activeElement).toBe(domRows()[4])
  wrapper.unmount()
})

it('closes on Escape (through the shortcut registry)', async () => {
  const { installShortcutDispatcher } = await import('../../composables/useKeyboardShortcuts')
  const uninstall = installShortcutDispatcher(window)
  const wrapper = mountView()
  await flushPromises()
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', cancelable: true }))
  expect(wrapper.emitted('close')).toHaveLength(1)
  wrapper.unmount()
  uninstall()
})

it('opens in zen as characters when the session is in zen, and a click opens that subagent', async () => {
  const wrapper = mountView({ initialMode: 'zen' })
  await flushPromises()
  expect(wrapper.find('.sv-body').exists()).toBe(false)
  const critters = wrapper.findAll('.critter')
  expect(critters.map(c => c.get('.name').text())).toEqual(['Fix the tests', 'Review the fix', 'Map the API', 'Old work', 'Broken work'])
  await critters[2]!.trigger('click')
  await flushPromises()
  expect(wrapper.find('.sv-body').exists()).toBe(true)
  expect(wrapper.get('.sv-row-highlight .sv-row-title').text()).toBe('Map the API')
  expect(wrapper.get('.sd-title').text()).toBe('Map the API')
  expect(wrapper.get('.sd-result').text()).toBe('routes/agent.go:29')
  wrapper.unmount()
})

it('briefs a finished subagent: its task, what it produced, and the key steps', async () => {
  const brief = {
    agent_id: 'pr', parent_session_id: session, subagent_type: 'general-purpose', description: 'Fix the SSRF', status: 'completed', progress: 1,
    last_output: 'Opened the PR.', output_lines: 20, created_at: earlier, updated_at: earlier, completed_at: earlier,
    prompt: 'Fix the link preview SSRF.\nPin the vetted IP.\nRe-check redirects.\nOpen a PR.',
    recent_output: [
      'TOOL:Read: /repo/apps/messages/link_previews.py',
      'TOOL:Edit: /repo/apps/messages/link_previews.py',
      'TOOL:Bash: npm test', 'RESULT_START', 'Exit code 1', 'RESULT_END',
      'TOOL:Bash: git commit -am "Pin link preview fetches"', 'RESULT_START', '[fix/ssrf 1a2b3c4] Pin link preview fetches', 'RESULT_END',
      'TOOL:Bash: git push -u origin fix/ssrf', 'RESULT_START', ' * [new branch]      fix/ssrf -> fix/ssrf', 'RESULT_END',
      'TOOL:Bash: gh pr create --fill', 'RESULT_START', 'https://github.com/acme/app/pull/1037', 'RESULT_END',
      'TOOL:Grep: pinned_send',
      'Opened the PR.'
    ]
  }
  auth.fetchWithAuth.mockImplementation(async () => ({ ok: true, json: async () => ({ subagents: [structuredClone(brief)] }) }) as Response)
  const wrapper = mountView()
  await flushPromises()
  const detail = wrapper.get('.sd')
  expect(detail.get('.sd-task-text').text()).toContain('Fix the link preview SSRF.')
  expect(detail.get('.sd-task .sd-link').text()).toBe('Show the whole task')

  const products = detail.get('.sd-products')
  expect(products.findAll('dt').map(d => d.text())).toEqual(['Opened PRs', 'Commits', 'Pushed', 'Files changed'])
  expect(products.get('a.sd-tag-link').text()).toBe('#1037')
  expect(products.get('a.sd-tag-link').attributes('href')).toBe('https://github.com/acme/app/pull/1037')
  expect(products.get('.sd-commit').text()).toBe('1a2b3c4Pin link preview fetches')
  expect(products.findAll('code.sd-tag').map(c => c.text())).toEqual(['fix/ssrf'])
  await products.get('.sd-files .sd-link').trigger('click')
  expect(products.get('.sd-file-list').text()).toBe('/repo/apps/messages/link_previews.py')

  // Edit, failed test run, commit, push, PR; not the Read or the search.
  expect(detail.get('#sd-activity-title').text()).toBe('Key steps 5 of 7')
  expect(detail.findAll('.sd-step').map(s => s.get('.sd-step-tool').text())).toEqual(['Shell', 'Shell', 'Shell', 'Shell', 'Edit'])
  expect(detail.findAll('.sd-step-failed')).toHaveLength(1)
  wrapper.unmount()
})

it('opens on the subagent the All agents overlay asked for', async () => {
  const wrapper = mountView({ focusAgentId: 'broken', initialMode: 'zen' })
  await flushPromises()
  expect(row(wrapper, 'Broken work').attributes('aria-selected')).toBe('true')
  expect(wrapper.get('.sd-title').text()).toBe('Broken work')
  wrapper.unmount()
})

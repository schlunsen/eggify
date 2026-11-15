import { mount, flushPromises } from '@vue/test-utils'
import { ref } from 'vue'
import AgentOverview from '../../components/agents/AgentOverview.vue'

vi.mock('../../composables/agents/useAgentOverview', async () => {
  const { overviewState } = await vi.importActual<typeof import('../../utils/agents/overviewState')>('../../utils/agents/overviewState')
  return {
  useAgentOverview: () => ({
    stateOf: overviewState, logsVersion: ref(0),
    agents: ref([
      { id: 'agent-one', status: 'processing', project_id: 'one', selected_avatar: { name: 'First' }, view_mode: 'zen' },
      { id: 'agent-two', status: 'processing', project_id: 'two', selected_avatar: { name: 'Second' }, subagents_running: 1, subagents_total: 2 }
    ]),
    projects: ref({ one: { name: 'Project one' }, two: { name: 'Project two' } }),
    details: ref({ 'agent-one': { logs: [
      { id: 'm1', time: '', kind: 'Agent', text: 'Planning the fix' },
      { id: 'm2', time: '', kind: 'Bash', text: 'go test ./internal/...\n  -count=1' }
    ] }, 'agent-two': { logs: [], subagents: [
      { agent_id: 'toolu_run', status: 'running', description: 'Review chat UI', subagent_type: 'general-purpose', background: true, created_at: new Date().toISOString(), recent_output: ['TOOL:Grep: markdown', 'RESULT_START', 'found markdown', 'RESULT_END'] },
      { agent_id: 'toolu_done', status: 'completed', description: 'Review backend', created_at: '2026-09-25T10:00:00Z', completed_at: '2026-09-25T10:02:00Z', recent_output: ['DONE'] }
    ] } }), loading: ref(false), error: ref(''), projectCount: ref(2), contextFor: () => undefined,
    filter: ref('all'), recentHours: ref(24), recentLimit: ref(12),
    counts: ref({ all: 2, working: 2, attention: 0, recent: 0, unreviewed: 0, justIdle: 0 }),
    needsReview: () => false, markReviewed: vi.fn(), hiddenRecentCount: ref(0),
    isJustIdle: (agent: any) => agent.id === 'agent-idle',
    layout: ref('columns'), summariesOn: ref(true), tagFilter: ref(''),
    digests: ref({
      'agent-one': { summary: 'Fixing flaky Codex tests; race found, patch in progress', tags: ['testing', 'bug-fix'] },
      'agent-two': { summary: '', tags: [], pending: true }
    }),
    topTags: ref([{ tag: 'testing', count: 1 }, { tag: 'bug-fix', count: 1 }])
  })
  }
})

beforeEach(() => localStorage.setItem('eggify:overview-detail', 'advanced'))
afterEach(() => localStorage.removeItem('eggify:overview-detail'))

it('shows both projects and opens the chosen agent without treating log clicks as navigation', async () => {
  const wrapper = mount(AgentOverview)
  expect(wrapper.findAll('.agent-column')).toHaveLength(2)
  expect(wrapper.text()).toContain('Project two')
  await wrapper.findAll('.activity-log')[1].trigger('click')
  expect(wrapper.emitted('select')).toBeUndefined()
  await wrapper.get('button[aria-label="Open agent Second"]').trigger('click')
  expect(wrapper.emitted('select')?.[0]?.[0]).toMatchObject({ id: 'agent-two', status: 'processing', project_id: 'two' })
  wrapper.unmount()
})

it('shows how many subagents ran and what each one is doing', () => {
  const wrapper = mount(AgentOverview)
  const columns = wrapper.findAll('.agent-column')
  expect(columns[0].find('.column-subagents').exists()).toBe(false)
  const strip = columns[1].get('.column-subagents')
  expect(strip.get('.subagents-heading').text()).toContain('1 running · 2 total')
  const rows = strip.findAll('.subagent-list li')
  expect(rows.map(r => r.get('.sub-desc').text())).toEqual(['Review chat UI', 'Review backend'])
  expect(rows[0].classes()).toContain('sub-running')
  expect(rows[0].get('pre').text()).toBe('→ Grep markdown\nfound markdown')
  expect(rows[0].get('.sub-now').text()).toBe('Grep · markdown')
  expect(rows[1].find('.sub-now').exists()).toBe(false)
  expect(rows[1].get('.sub-meta').text()).toContain('2m')
  wrapper.unmount()
})

it('shows what each working agent is doing now in Advanced view', () => {
  const wrapper = mount(AgentOverview)
  expect(wrapper.text()).not.toContain('Zen view')
  const [first, second] = wrapper.findAll('.agent-column')
  expect(first.get('.column-now').text()).toBe('▸ Bash · go test ./internal/... -count=1')
  expect(first.get('.view-mode').text()).toBe('ZEN')
  expect(second.find('.view-mode').exists()).toBe(false)
  wrapper.unmount()
})

const key = (k: string, target: EventTarget = window) =>
  target.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }))
const selected = (wrapper: ReturnType<typeof mount>) => wrapper.findAll('.agent-column.selected').map(c => c.get('h2').text())

it('moves a keyboard cursor over the agents and opens the selected one with Enter', async () => {
  const wrapper = mount(AgentOverview, { attachTo: document.body })
  // Starts on the first agent when the current session isn't shown.
  expect(selected(wrapper)).toEqual(['First'])
  key('ArrowRight'); await flushPromises()
  expect(selected(wrapper)).toEqual(['Second'])
  expect(document.activeElement?.getAttribute('aria-label')).toBe('Open agent Second')
  key('ArrowRight'); await flushPromises()
  expect(selected(wrapper)).toEqual(['Second']) // stops at the end
  key('Home'); await flushPromises()
  expect(selected(wrapper)).toEqual(['First'])
  key('End'); await flushPromises()
  // Enter from anywhere that isn't a control opens the selected agent.
  ;(document.activeElement as HTMLElement).blur()
  key('Enter')
  expect(wrapper.emitted('select')?.[0]?.[0]).toMatchObject({ id: 'agent-two' })
  wrapper.unmount()
})

it('starts on the agent you came from and leaves the time-window select its own arrow keys', async () => {
  const wrapper = mount(AgentOverview, { attachTo: document.body, props: { activeSessionId: 'agent-two' } })
  expect(selected(wrapper)).toEqual(['Second'])
  key('ArrowLeft', wrapper.get('select').element); await flushPromises()
  expect(selected(wrapper)).toEqual(['Second'])
  wrapper.unmount()
})

it('shows each agent’s AI summary and tags, and filters by a tag when one is clicked', async () => {
  const wrapper = mount(AgentOverview)
  const [first, second] = wrapper.findAll('.agent-column')
  expect(first.get('.digest-summary').text()).toBe('Fixing flaky Codex tests; race found, patch in progress')
  expect(first.findAll('.digest-tags .tag-chip').map(t => t.text())).toEqual(['#testing', '#bug-fix'])
  expect(second.get('.digest-summary').text()).toBe('Summarizing…')
  expect(wrapper.findAll('.tag-bar .tag-chip').map(t => t.text())).toEqual(['#testing 1', '#bug-fix 1'])
  await first.get('.digest-tags .tag-chip').trigger('click')
  expect((wrapper.vm as any).tagFilter).toBe('testing')
  expect(wrapper.get('.tag-clear').text()).toBe('Clear #testing ×')
  expect(wrapper.emitted('select')).toBeUndefined() // a tag click is not a navigation
  await wrapper.get('.tag-clear').trigger('click')
  expect((wrapper.vm as any).tagFilter).toBe('')
  wrapper.unmount()
})

it('lists agents one per row with their summary, and the cursor opens them too', async () => {
  const wrapper = mount(AgentOverview, { attachTo: document.body })
  await wrapper.findAll('[aria-label="Layout"] button')[1].trigger('click')
  expect((wrapper.vm as any).layout).toBe('list')
  const rows = wrapper.findAll('.agent-row')
  expect(rows).toHaveLength(2)
  expect(rows[0].get('.row-summary').text()).toBe('Fixing flaky Codex tests; race found, patch in progress')
  expect(rows[0].get('.row-who small').text()).toBe('Project one')
  expect(rows[1].get('.row-summary').classes()).toContain('pending')
  key('ArrowDown'); await flushPromises()
  expect(wrapper.findAll('.agent-row.selected').map(r => r.get('strong').text())).toEqual(['Second'])
  ;(document.activeElement as HTMLElement).blur()
  key('Enter')
  expect(wrapper.emitted('select')?.[0]?.[0]).toMatchObject({ id: 'agent-two' })
  wrapper.unmount()
})

it('hides summaries and tags when they are switched off', async () => {
  const wrapper = mount(AgentOverview)
  await wrapper.get('.summaries-toggle').trigger('click')
  expect(wrapper.find('.column-digest').exists()).toBe(false)
  expect(wrapper.find('.tag-bar').exists()).toBe(false)
  wrapper.unmount()
})


it('defaults to Simple and preserves Advanced layout when switching back', async () => {
  localStorage.removeItem('eggify:overview-detail')
  const wrapper = mount(AgentOverview)
  expect(wrapper.findAll('.simple-card')).toHaveLength(2)
  expect(wrapper.find('.activity-log').exists()).toBe(false)
  expect(wrapper.find('.column-context').exists()).toBe(false)
  expect(wrapper.get('.simple-summary').text()).toContain('Fixing flaky Codex tests')
  const [firstCard, secondCard] = wrapper.findAll('.simple-card')
  expect(firstCard.findAll('.simple-tags .tag-chip').map(t => t.text())).toEqual(['#testing', '#bug-fix'])
  expect(secondCard.find('.simple-tags').exists()).toBe(false)
  await firstCard.get('.simple-tags .tag-chip').trigger('click')
  expect((wrapper.vm as any).tagFilter).toBe('testing')
  expect(wrapper.emitted('select')).toBeUndefined() // a tag click is not a navigation
  await wrapper.get('.tag-clear').trigger('click')
  await wrapper.get('button[aria-label="Open agent Second"]').trigger('click')
  expect(wrapper.emitted('select')?.[0]?.[0]).toMatchObject({ id: 'agent-two' })
  await wrapper.findAll('.detail-toggle button')[1].trigger('click')
  expect(wrapper.findAll('.agent-column')).toHaveLength(2)
  await wrapper.get('.tag-bar .tag-chip').trigger('click')
  await wrapper.findAll('[aria-label="Layout"] button')[1].trigger('click')
  await wrapper.findAll('.detail-toggle button')[0].trigger('click')
  expect((wrapper.vm as any).tagFilter).toBe('testing') // the tag bar is shown in Simple too
  await wrapper.get('.tag-clear').trigger('click')
  expect(wrapper.findAll('.simple-card')).toHaveLength(2)
  await wrapper.findAll('.detail-toggle button')[1].trigger('click')
  expect(wrapper.findAll('.agent-row')).toHaveLength(2)
  wrapper.unmount()
  const reopened = mount(AgentOverview)
  expect(reopened.find('.simple-grid').exists()).toBe(false)
  reopened.unmount()
})

it('stacks Simple cards in one column per project and moves the cursor across and along them', async () => {
  localStorage.removeItem('eggify:overview-detail')
  const wrapper = mount(AgentOverview, { attachTo: document.body, props: { activeSessionId: 'agent-one' } })
  ;(wrapper.vm as any).agents.push({ id: 'agent-three', status: 'processing', project_id: 'one', selected_avatar: { name: 'Third' } })
  await flushPromises()
  const columns = wrapper.findAll('.simple-column')
  expect(columns.map(c => c.get('.simple-column-title').text())).toEqual(['Project one2', 'Project two1'])
  expect(columns[0].findAll('.simple-name').map(h => h.text())).toEqual(['First', 'Third'])
  const current = () => wrapper.findAll('.simple-card.selected').map(c => c.get('.simple-name').text())
  expect(current()).toEqual(['First'])
  key('ArrowDown'); await flushPromises()
  expect(current()).toEqual(['Third'])
  key('ArrowDown'); await flushPromises()
  expect(current()).toEqual(['Third'])
  key('ArrowRight'); await flushPromises()
  expect(current()).toEqual(['Second'])
  key('ArrowRight'); await flushPromises()
  expect(current()).toEqual(['Second'])
  key('ArrowLeft'); await flushPromises()
  expect(current()).toEqual(['First'])
  expect(wrapper.get('.simple-card.selected .simple-current').text()).toBe('Current')
  // A poll that re-sorts the agents doesn't move the cards while the overview is open.
  ;(wrapper.vm as any).agents.reverse()
  await flushPromises()
  expect(wrapper.findAll('.simple-column').map(c => c.findAll('.simple-name').map(h => h.text()))).toEqual([['First', 'Third'], ['Second']])
  wrapper.unmount()
})

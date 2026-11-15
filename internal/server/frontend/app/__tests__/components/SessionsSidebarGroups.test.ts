import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import SessionsSidebar from '../../components/agents/SessionsSidebar.vue'
import { useSessionStore } from '../../stores/session/sessionStore'
import { reloadCollapsedGroups, SIDEBAR_COLLAPSED_GROUPS_KEY, useSessionGroups } from '../../composables/agents/useSessionGroups'
import { defineComponent, h, ref } from 'vue'

vi.mock('../../composables/useAvatarThemes', () => ({ fetchAvatarById: vi.fn(), getAvatarImageUrl: vi.fn() }))
vi.mock('../../composables/useCharacterAvatar', () => ({ useCharacterAvatar: () => ({}) }))
vi.mock('../../composables/useSidebar', async () => {
  const { ref } = await import('vue')
  return { useSidebar: () => ({ isCollapsed: ref(false) }) }
})
vi.mock('../../composables/useN0Bindings', () => ({ useN0Bindings: () => ({ scheduleRefresh: vi.fn(), getBinding: () => null }) }))
vi.mock('../../composables/agents/useSessionDigests', () => ({ useSessionDigests: () => ({ scheduleLoad: vi.fn(), getDigest: () => undefined }) }))

const now = Date.now()
const ago = (minutes: number) => new Date(now - minutes * 60000).toISOString()
const yesterday = (() => { const d = new Date(now); d.setDate(d.getDate() - 1); d.setHours(12, 0, 0, 0); return d.toISOString() })()
// Today's sessions must stay after local midnight even when the test runs just after it.
const todayAt = (minutes: number) => new Date(Math.max(now - minutes * 60000, new Date(now).setHours(0, 0, 1, 0))).toISOString()

const sessions = [
  { id: 'old', status: 'ended', message_count: 3, updated_at: yesterday },
  { id: 'today-a', status: 'idle', message_count: 3, updated_at: todayAt(1) },
  { id: 'busy', status: 'processing', message_count: 3, updated_at: ago(2) },
  { id: 'asks', status: 'idle', message_count: 3, updated_at: ago(30) },
  { id: 'today-b', status: 'idle', message_count: 3, updated_at: todayAt(5) },
]

function mountSidebar(activeSessionId: string | null = null) {
  return mount(SessionsSidebar, {
    props: { sessions, activeSessionId, connected: true, creating: false },
    global: { stubs: { Teleport: true } },
  })
}

beforeEach(() => {
  localStorage.clear()
  reloadCollapsedGroups()
  setActivePinia(createPinia())
  useSessionStore().addQuestion('asks', { id: 'q1', sessionId: 'asks', question: 'Which?', header: '', options: [], multiSelect: false, status: 'pending', timestamp: new Date() })
})

const rowIds = (w: ReturnType<typeof mountSidebar>) => w.findAll('.session-item').map(r => r.attributes('data-session-id'))

it('groups sessions under Needs you, Working, Today and Earlier headers with counts', () => {
  const w = mountSidebar()
  expect(w.findAll('.group-header').map(h => `${h.get('.group-label').text()} ${h.get('.group-count').text()}`)).toEqual([
    'Needs you 1', 'Working 1', 'Today 2', 'Earlier 1',
  ])
  // Earlier starts collapsed
  expect(rowIds(w)).toEqual(['asks', 'busy', 'today-a', 'today-b'])
  expect(w.get('[data-group="needs"] .session-item').classes()).toContain('needs-you')
  expect(w.get('[data-group="needs"] [data-testid="session-waiting"]').text()).toContain('Question waiting')
  expect(w.get('[data-group="working"] .session-item').classes()).toContain('variant-full')
  // Idle for a minute: still full-size (12 h), not one line yet
  expect(w.get('[data-group="today"] .session-item').classes()).toContain('variant-full')
  expect(w.find('.session-filters').exists()).toBe(false)
})

it('collapses Today and Earlier from their headers and remembers it', async () => {
  const w = mountSidebar()
  await w.get('[data-group="earlier"] .group-header').trigger('click')
  expect(rowIds(w)).toContain('old')
  expect(w.get('[data-group="earlier"] .session-item').classes()).toContain('ended')
  await w.get('[data-group="today"] .group-header').trigger('click')
  expect(rowIds(w)).toEqual(['asks', 'busy', 'old'])
  expect(JSON.parse(localStorage.getItem(SIDEBAR_COLLAPSED_GROUPS_KEY)!)).toMatchObject({ today: true, earlier: false })
  w.unmount()

  reloadCollapsedGroups()
  expect(rowIds(mountSidebar())).toEqual(['asks', 'busy', 'old'])
})

it('survives storage that throws', async () => {
  const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })
  const w = mountSidebar()
  await w.get('[data-group="today"] .group-header').trigger('click')
  expect(rowIds(w)).toEqual(['asks', 'busy'])
  spy.mockRestore()
})

it('moves through the rows on screen with the arrow keys, skipping collapsed groups', async () => {
  const w = mountSidebar('busy')
  const list = w.get('.sessions-list')
  await list.trigger('focus')
  expect(w.get('.session-item.focused').attributes('data-session-id')).toBe('busy')
  await list.trigger('keydown', { key: 'ArrowDown' })
  await list.trigger('keydown', { key: 'ArrowDown' })
  expect(w.get('.session-item.focused').attributes('data-session-id')).toBe('today-b')
  await list.trigger('keydown', { key: 'ArrowDown' }) // Earlier is collapsed: stays on the last row
  expect(w.get('.session-item.focused').attributes('data-session-id')).toBe('today-b')
  await list.trigger('keydown', { key: 'Home' })
  expect(w.get('.session-item.focused').attributes('data-session-id')).toBe('asks')
  await list.trigger('keydown', { key: 'Enter' })
  expect(w.emitted('select')).toEqual([['asks']])
})

it('gives the session shortcuts (⌘1…9, ⌥⌘↑/↓) the same order as the rows on screen', async () => {
  let order: string[] = []
  const Probe = defineComponent({
    setup() {
      const { order: visible } = useSessionGroups(ref(sessions))
      return () => { order = visible.value.map(s => s.id); return h('div') }
    },
  })
  const w = mountSidebar()
  const probe = mount(Probe)
  expect(order).toEqual(rowIds(w))
  await w.get('[data-group="earlier"] .group-header').trigger('click')
  await probe.vm.$nextTick()
  expect(order).toEqual(rowIds(w))
  expect(order).toContain('old')
})

it('keeps showing the first prompt after the store drops the session messages', async () => {
  const store = useSessionStore()
  store.addMessages('today-a', [{ id: 'm1', sessionId: 'today-a', role: 'user', content: 'Fix the login page', timestamp: new Date() } as any])
  const w = mountSidebar()
  const row = () => w.get('[data-session-id="today-a"]')
  expect(row().text()).toContain('Fix the login page')

  // Opening other sessions forgets the messages of this one (KEEP_MESSAGES_FOR_SESSIONS)
  for (const id of ['s1', 's2', 's3', 's4', 's5']) store.setActiveSession(id)
  await w.vm.$nextTick()
  expect(store.messages['today-a']).toBeUndefined()
  expect(row().text()).toContain('Fix the login page')
})

describe('worker sessions', () => {
  const worker = (id: string, mode: 'build' | 'review', status: string, minutesAgo: number, extra: Record<string, unknown> = {}) => ({
    id, status, message_count: 2, updated_at: ago(minutesAgo), created_at: ago(minutesAgo + 10),
    model_name: mode === 'build' ? 'claude-sonnet-5-5' : 'gpt-6-astra',
    options: { spawned_by_session_id: 'today-a', worker: { mode, ...extra } },
  })
  const withWorkers = [
    ...sessions,
    worker('rev', 'review', 'idle', 3, { reviews_session_id: 'bld' }),
    worker('bld', 'build', 'processing', 4, { branch: 'worker/export', review_rounds: 1, last_verdict: 'changes_requested' }),
  ]
  const mountWith = (list: any[] = withWorkers, activeSessionId: string | null = null) => mount(SessionsSidebar, {
    props: { sessions: list, activeSessionId, connected: true, creating: false },
    global: { stubs: { Teleport: true } },
  })

  it('shows workers as one-line rows under their parent, which counts as Working', () => {
    const w = mountWith()
    // The parent moved from Today to Working because its builder works.
    expect(w.findAll('.group-header').map(h => `${h.get('.group-label').text()} ${h.get('.group-count').text()}`)).toEqual([
      'Needs you 1', 'Working 2', 'Today 1', 'Earlier 1',
    ])
    expect(rowIds(w)).toEqual(['asks', 'today-a', 'bld', 'rev', 'busy', 'today-b'])
    const rows = w.findAll('[data-group="working"] [data-testid="worker-session-row"]')
    expect(rows.map(r => ['.worker-role', '.worker-model', '.worker-status', '.worker-verdict'].map(part => r.get(part).text()))).toEqual([
      ['Build', 'Sonnet 5.5', 'working', 'Changes requested'],
      ['Review', 'gpt-6-astra', 'idle', 'Changes requested'],
    ])
    expect(w.get('.session-item[data-session-id="today-a"] [data-testid="session-workers-working"]').text()).toBe('Idle · 1 worker working')
    expect(w.get('.session-item[data-session-id="today-a"]').classes()).toContain('working')
    w.unmount()
  })

  it('opens a worker on click and marks the open one', async () => {
    const w = mountWith(withWorkers, 'rev')
    expect(w.get('.session-item.active').attributes('data-session-id')).toBe('rev')
    await w.get('.session-item[data-session-id="bld"]').trigger('click')
    expect(w.emitted('select')).toEqual([['bld']])
    w.unmount()
  })

  it('shows a worker as a normal row when its parent is not in the list', () => {
    const w = mountWith(withWorkers.filter(s => s.id !== 'today-a'))
    expect(w.find('[data-testid="worker-session-row"]').exists()).toBe(false)
    expect(rowIds(w)).toEqual(['asks', 'busy', 'bld', 'rev', 'today-b'])
    w.unmount()
  })

  it('steps through worker rows with the arrow keys, in the order on screen', async () => {
    const w = mountWith(withWorkers, 'today-a')
    const list = w.get('.sessions-list')
    await list.trigger('focus')
    await list.trigger('keydown', { key: 'ArrowDown' })
    expect(w.get('.session-item.focused').attributes('data-session-id')).toBe('bld')
    await list.trigger('keydown', { key: 'ArrowDown' })
    expect(w.get('.session-item.focused').attributes('data-session-id')).toBe('rev')
    await list.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('select')).toEqual([['rev']])
    await list.trigger('keydown', { key: 'ArrowDown' })
    expect(w.get('.session-item.focused').attributes('data-session-id')).toBe('busy')

    // The page's session shortcuts use the same order.
    let order: string[] = []
    const Probe = defineComponent({
      setup() {
        const { order: visible } = useSessionGroups(ref(withWorkers))
        return () => { order = visible.value.map(s => s.id); return h('div') }
      },
    })
    mount(Probe)
    expect(order).toEqual(rowIds(w))
    w.unmount()
  })

  it('selects worker rows like any session in select mode', async () => {
    const w = mountWith()
    await w.get('[data-testid="select-sessions"]').trigger('click')
    const selected = () => w.findAll('.session-item.selected').map(r => r.attributes('data-session-id'))
    await w.get('.session-item[data-session-id="bld"]').trigger('click')
    expect(w.emitted('select')).toBeUndefined()
    expect(selected()).toEqual(['bld'])
    expect(w.get('.session-item[data-session-id="bld"] [data-testid="session-select-box"]').attributes('aria-checked')).toBe('true')
    // Shift-click: the range runs through the rows on screen, workers included.
    await w.get('.session-item[data-session-id="busy"]').trigger('click', { shiftKey: true })
    expect(selected()).toEqual(['bld', 'rev', 'busy'])
    await w.get('[data-testid="bulk-delete"]').trigger('click')
    expect((w.emitted('bulk-delete') as any[])[0][0].sort()).toEqual(['bld', 'busy', 'rev'])

    // A group's checkbox takes its workers too.
    await w.get('[data-group="working"] [data-testid="group-select-box"]').trigger('click')
    expect(selected()).toEqual(['today-a', 'bld', 'rev', 'busy'])
    expect(w.get('[data-group="working"] [data-testid="group-select-box"]').attributes('aria-checked')).toBe('true')
    w.unmount()
  })
})

import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import SessionsSidebar from '../../components/agents/SessionsSidebar.vue'
import { reloadCollapsedGroups } from '../../composables/agents/useSessionGroups'

vi.mock('../../composables/useAvatarThemes', () => ({ fetchAvatarById: vi.fn(), getAvatarImageUrl: vi.fn() }))
vi.mock('../../composables/useCharacterAvatar', () => ({ useCharacterAvatar: () => ({}) }))
vi.mock('../../composables/useSidebar', async () => {
  const { ref } = await import('vue')
  return { useSidebar: () => ({ isCollapsed: ref(false) }) }
})
vi.mock('../../composables/useN0Bindings', () => ({ useN0Bindings: () => ({ scheduleRefresh: vi.fn(), getBinding: () => null }) }))
vi.mock('../../composables/agents/useSessionDigests', () => ({ useSessionDigests: () => ({ scheduleLoad: vi.fn(), getDigest: () => undefined }) }))

const now = Date.now()
const daysAgo = (n: number) => { const d = new Date(now); d.setDate(d.getDate() - n); d.setHours(12, 0, 0, 0); return d.toISOString() }
const todayAt = (minutes: number) => new Date(Math.max(now - minutes * 60000, new Date(now).setHours(0, 0, 1, 0))).toISOString()

const sessions = [
  { id: 'busy', status: 'processing', message_count: 3, updated_at: todayAt(1) },
  { id: 'today', status: 'idle', message_count: 3, updated_at: todayAt(5) },
  { id: 'old-1', status: 'ended', message_count: 3, updated_at: daysAgo(2) },
  { id: 'old-2', status: 'idle', message_count: 3, updated_at: daysAgo(3) },
  { id: 'old-3', status: 'ended', message_count: 3, updated_at: daysAgo(4) },
]

function mountSidebar(list = sessions) {
  return mount(SessionsSidebar, {
    props: { sessions: list, activeSessionId: 'busy', connected: true, creating: false },
    global: { stubs: { Teleport: true } },
  })
}

beforeEach(() => {
  localStorage.clear()
  reloadCollapsedGroups()
  setActivePinia(createPinia())
})

const checked = (w: ReturnType<typeof mountSidebar>) =>
  w.findAll('.session-item.selected').map(r => r.attributes('data-session-id'))

it('"Clean up Earlier" opens the group with all of it selected, and Delete emits those ids', async () => {
  const w = mountSidebar()
  expect(w.find('[data-testid="bulk-bar"]').exists()).toBe(false)
  await w.get('[data-testid="select-earlier"]').trigger('click')

  expect(w.get('[data-testid="select-toolbar"]').text()).toContain('3 selected')
  expect(checked(w)).toEqual(['old-1', 'old-2', 'old-3'])
  // Only old-2 hasn't ended yet
  expect(w.get('[data-testid="bulk-end"]').text()).toBe('End 1')
  expect(w.get('[data-testid="bulk-delete"]').text()).toBe('Delete 3')

  await w.get('[data-testid="bulk-delete"]').trigger('click')
  expect(w.emitted('bulk-delete')).toEqual([[['old-1', 'old-2', 'old-3']]])
  await w.get('[data-testid="bulk-end"]').trigger('click')
  expect(w.emitted('bulk-end')).toEqual([[['old-2']]])
})

it('in select mode a click toggles the row instead of opening it; shift-click selects a range', async () => {
  const w = mountSidebar()
  await w.get('[data-group="earlier"] .group-header').trigger('click')
  await w.get('[data-testid="select-sessions"]').trigger('click')
  expect(w.findAll('[data-testid="session-select-box"]').length).toBe(5)
  expect(w.find('.session-row-actions').exists()).toBe(false)

  const row = (id: string) => w.get(`.session-item[data-session-id="${id}"]`)
  await row('today').trigger('click')
  expect(w.emitted('select')).toBeUndefined()
  expect(checked(w)).toEqual(['today'])

  await row('old-2').trigger('click', { shiftKey: true })
  expect(checked(w)).toEqual(['today', 'old-1', 'old-2'])

  await row('today').trigger('click')
  expect(checked(w)).toEqual(['old-1', 'old-2'])
})

it('a group checkbox selects the whole group, even while it is collapsed', async () => {
  const w = mountSidebar()
  await w.get('[data-testid="select-sessions"]').trigger('click')
  const earlierBox = w.get('[data-group="earlier"] [data-testid="group-select-box"]')
  await earlierBox.trigger('click')
  // Clicking the box doesn't fold the group open/closed
  expect(w.findAll('[data-group="earlier"] .session-item').length).toBe(0)
  expect(earlierBox.attributes('aria-checked')).toBe('true')
  expect(w.get('[data-testid="bulk-delete"]').text()).toBe('Delete 3')
  await earlierBox.trigger('click')
  expect(w.get('[data-testid="bulk-delete"]').attributes('disabled')).toBeDefined()
})

it('All / None, Done, and leaving select mode once the selected sessions are gone', async () => {
  const w = mountSidebar()
  await w.get('[data-testid="select-earlier"]').trigger('click')
  const allBtn = w.findAll('.select-toolbar .btn-link')[0]!
  await allBtn.trigger('click')
  expect(w.get('[data-testid="bulk-delete"]').text()).toBe('Delete 5')
  await allBtn.trigger('click')
  expect(w.get('[data-testid="bulk-delete"]').text()).toBe('Delete')

  await w.get('[data-testid="select-cancel"]').trigger('click')
  expect(w.find('[data-testid="bulk-bar"]').exists()).toBe(false)
  expect(w.find('.btn-new-session').exists()).toBe(true)

  // Deleted elsewhere (or by the bulk action): select mode ends by itself
  await w.get('[data-testid="select-earlier"]').trigger('click')
  await w.setProps({ sessions: sessions.slice(0, 2) })
  expect(w.find('[data-testid="bulk-bar"]').exists()).toBe(false)
})

it('Space toggles the focused row from the keyboard in select mode', async () => {
  const w = mountSidebar()
  await w.get('[data-testid="select-sessions"]').trigger('click')
  const list = w.get('.sessions-list')
  await list.trigger('focus')
  await list.trigger('keydown', { key: ' ' })
  expect(checked(w)).toEqual(['busy'])
  expect(w.emitted('select')).toBeUndefined()
})

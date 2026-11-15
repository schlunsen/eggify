import { mount, flushPromises } from '@vue/test-utils'
import ChangelogModal from '../../components/ChangelogModal.vue'
import { useChangelog } from '../../composables/useChangelog'

const payload = {
  current: '2.21.0',
  total: 5,
  releases: [
    { version: '2.21.0', date: '2026-11-14', sections: [
      { title: 'Added', items: ['**Teams**: build and review', 'Link [x](javascript:alert(1)) <img src=x onerror=alert(1)>'] },
      { title: 'Fixed', items: ['A crash'] },
      { title: 'Fixed', items: ['Repeated title'] },
    ], note: '(Never published.)' },
    { version: 'Unreleased', date: '', unreleased: true, sections: [{ title: 'Added', items: ['Upcoming thing'] }] },
    { version: '2.20.0', date: '2026-11-01', sections: [{ title: 'Security', items: ['Locked it down'] }] },
  ],
}

const fetchMock = vi.fn()
vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth: fetchMock }))
vi.mock('../../composables/useKeyboardShortcuts', () => ({ useClosable: vi.fn() }))

// Every mount shares the open state, so unmount them all between tests.
const mounted: Array<{ unmount: () => void }> = []
function mountModal(attach = false) {
  const w = mount(ChangelogModal, { attachTo: attach ? document.body : undefined, global: { stubs: { Teleport: true } } })
  mounted.push(w)
  return w
}
afterEach(() => { while (mounted.length) mounted.pop()!.unmount() })

beforeEach(() => {
  localStorage.clear()
  fetchMock.mockReset()
  fetchMock.mockResolvedValue({ ok: true, json: async () => payload })
  useChangelog().closeChangelog()
})

it('renders nothing until opened, then lists releases with section tags', async () => {
  const w = mountModal()
  expect(w.find('[data-testid="changelog-modal"]').exists()).toBe(false)
  useChangelog().checkForUpdate('2.21.0')
  useChangelog().openChangelog()
  await flushPromises()
  expect(w.findAll('[data-testid="changelog-release"]')).toHaveLength(3)
  expect(w.text()).toContain('v2.21.0')
  expect(w.get('[data-testid="changelog-note"]').text()).toBe('(Never published.)')
  expect(w.text()).toContain('2026-11-14')
  expect(w.find('.cl-tag--security').text()).toBe('Security')
  expect(w.find('strong').text()).toBe('Teams')
})

it('shows Unreleased without a v, date or new chip, and focuses the dialog', async () => {
  localStorage.setItem('eggify-changelog-seen', '2.0.0')
  const w = mountModal(true)
  useChangelog().checkForUpdate('2.21.0')
  useChangelog().openChangelog()
  await flushPromises()
  const un = w.findAll('[data-testid="changelog-release"]').find(a => a.text().includes('Upcoming thing'))!
  expect(un.get('.cl-version').text()).toContain('Unreleased')
  expect(un.text()).not.toContain('vUnreleased')
  expect(un.text()).toContain('upcoming')
  expect(un.find('.cl-new').exists()).toBe(false)
  expect(w.findAll('.cl-new').length).toBeGreaterThan(0)
  expect(document.activeElement).toBe(w.get('.cl-close').element)
})

it('says "No changelog entries" with no filter and nothing loaded', async () => {
  fetchMock.mockResolvedValue({ ok: true, json: async () => ({ current: '', total: 0, releases: [] }) })
  const w = mountModal()
  useChangelog().openChangelog()
  await flushPromises()
  expect(w.get('[data-testid="changelog-empty"]').text()).toBe('No changelog entries.')
})

it('sanitises markdown: no handlers, no javascript: links', async () => {
  const w = mountModal()
  useChangelog().openChangelog()
  await flushPromises()
  expect(w.html()).not.toContain('onerror')
  expect(w.html()).not.toContain('javascript:')
})

it('filters by text and offers Load more while releases are missing', async () => {
  const w = mountModal()
  useChangelog().openChangelog()
  await flushPromises()
  await w.get('[data-testid="changelog-search"]').setValue('locked')
  expect(w.findAll('[data-testid="changelog-release"]')).toHaveLength(1)
  await w.get('[data-testid="changelog-search"]').setValue('nope')
  expect(w.find('[data-testid="changelog-empty"]').exists()).toBe(true)
  await w.get('[data-testid="changelog-search"]').setValue('')
  await w.get('[data-testid="changelog-more"]').trigger('click')
  expect(fetchMock).toHaveBeenLastCalledWith('/api/changelog?limit=40')
})

it('opening marks the running version as seen', async () => {
  localStorage.setItem('eggify-changelog-seen', '2.20.0')
  const cl = useChangelog()
  expect(cl.checkForUpdate('2.21.0')).toBe(true)
  cl.openChangelog()
  expect(localStorage.getItem('eggify-changelog-seen')).toBe('2.21.0')
  expect(cl.unseen.value).toBe(false)
})

it('traps Tab and returns focus to the opener on close', async () => {
  const opener = document.createElement('button')
  document.body.appendChild(opener)
  opener.focus()
  const w = mountModal(true)
  useChangelog().openChangelog()
  await flushPromises()
  const last = w.findAll('button').at(-1)!.element as HTMLElement
  last.focus()
  await w.get('.cl-dialog').trigger('keydown', { key: 'Tab' })
  expect(document.activeElement).toBe(w.get('.cl-close').element)
  useChangelog().closeChangelog()
  await flushPromises()
  expect(document.activeElement).toBe(opener)
  opener.remove()
})

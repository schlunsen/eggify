import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import SessionItem from '../../components/agents/SessionItem.vue'
import { configureSessionTagSync, readSessionTags, writeSessionTagList } from '../../utils/sessionTags'
import { tagHue } from '../../utils/tagColor'
import { useSessionDigests } from '../../composables/agents/useSessionDigests'

vi.mock('../../composables/useAvatarThemes', () => ({ fetchAvatarById: vi.fn(), getAvatarImageUrl: vi.fn() }))
vi.mock('../../composables/useCharacterAvatar', () => ({ useCharacterAvatar: () => ({ name: 'Mittens' }) }))

const NOW = Date.now()
const mountItem = (props: Record<string, unknown> = {}, session: Record<string, unknown> = {}) =>
  mount(SessionItem, {
    props: {
      session: { id: 't1', status: 'idle', message_count: 4, options: {}, updated_at: new Date(NOW - 4 * 60000).toISOString(), ...session },
      isActive: false,
      now: NOW,
      ...props,
    },
    global: { stubs: { Teleport: true } },
  })

beforeEach(() => {
  localStorage.clear()
  configureSessionTagSync(null)
  setActivePinia(createPinia())
  useSessionDigests().digests.value = {}
})

it('shows each tag as its own chip, coloured by the tag', () => {
  writeSessionTagList('t1', ['bug', 'ios'])
  const chips = mountItem().findAll('.tag-chip')
  expect(chips.map(c => c.text())).toEqual(['bug', 'ios'])
  expect(chips[0]!.attributes('style')).toContain(`--tag-hue: ${tagHue('bug')}`)
  expect(chips[1]!.attributes('style')).toContain(`--tag-hue: ${tagHue('ios')}`)
})

it('folds tags that do not fit into a +N chip', () => {
  writeSessionTagList('t1', ['bug', 'ios', 'sidebar-redesign', 'release'])
  const wrapper = mountItem({ variant: 'compact' })
  const chips = wrapper.findAll('.tag-chip')
  expect(chips.map(c => c.text())).toEqual(['bug', 'ios', '+2'])
  expect(wrapper.get('.tag-more').attributes('title')).toBe('sidebar-redesign, release')
})

it('a compact row is one line: name, tags and time, the summary only in the tooltip', async () => {
  writeSessionTagList('t1', ['bug'])
  useSessionDigests().digests.value = { t1: { summary: 'Fixing the sidebar', tags: [] } }
  const wrapper = mountItem({ variant: 'compact' })
  const row = wrapper.get('.session-name-row')
  expect(row.get('.session-name').text()).toBe('Mittens')
  expect(row.find('.tag-chip').exists()).toBe(true)
  expect(row.get('.session-time').text()).toBe('4m')
  expect(wrapper.find('[data-testid="session-description"]').exists()).toBe(false)
  await wrapper.trigger('mouseenter')
  expect(wrapper.get('[data-testid="session-tooltip"]').text()).toContain('Fixing the sidebar')
})

it('a full row has the summary, the tags and a labelled context meter', () => {
  writeSessionTagList('t1', ['bug'])
  useSessionDigests().digests.value = { t1: { summary: 'Fixing the sidebar', tags: [] } }
  const wrapper = mountItem({ contextUsage: { percentage: 64.2 } })
  expect(wrapper.get('[data-testid="session-description"]').text()).toBe('Fixing the sidebar')
  expect(wrapper.get('.session-meta .tag-chip').text()).toBe('bug')
  expect(wrapper.get('[data-testid="context-meter"]').text()).toBe('64%')
})

it('clicking a chip edits the tags', async () => {
  writeSessionTagList('t1', ['bug', 'ios'])
  const wrapper = mountItem()
  await wrapper.get('.tag-chip').trigger('click')
  const input = wrapper.get('.tag-input')
  expect((input.element as HTMLInputElement).value).toBe('bug, ios')
  expect(wrapper.emitted('select')).toBeUndefined()
  await input.setValue('bug, ui, perf')
  await input.trigger('keydown', { key: 'Enter' })
  expect(readSessionTags().t1).toBe('bug, ui, perf')
  expect(wrapper.findAll('.tag-chip').map(c => c.text())).toEqual(['bug', 'ui', 'perf'])
})

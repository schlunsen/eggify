import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import SessionItem from '../../components/agents/SessionItem.vue'
import { useSessionDigests } from '../../composables/agents/useSessionDigests'

vi.mock('../../composables/useAvatarThemes', () => ({ fetchAvatarById: vi.fn(), getAvatarImageUrl: vi.fn() }))
vi.mock('../../composables/useCharacterAvatar', () => ({ useCharacterAvatar: () => ({}) }))

const session = { id: 's1', status: 'idle', message_count: 4, options: {} }
const mountItem = (props: Record<string, unknown> = {}) =>
  mount(SessionItem, { props: { session, isActive: false, ...props }, global: { stubs: { Teleport: true } } })

beforeEach(() => { localStorage.clear(); setActivePinia(createPinia()); useSessionDigests().digests.value = {} })

it('shows the AI summary as the description and keeps the clutter out of the row', async () => {
  useSessionDigests().digests.value = { s1: { summary: 'Reworking the sidebar so each session says what it is about', tags: ['sidebar', 'ui'] } }
  const wrapper = mountItem({ initialPrompt: 'first prompt' })
  const description = wrapper.get('[data-testid="session-description"]')
  expect(description.text()).toBe('Reworking the sidebar so each session says what it is about')
  expect(description.classes()).not.toContain('is-prompt')
  // Model, branch, cost, id and topics are not in the row...
  expect(wrapper.find('.session-model').exists()).toBe(false)
  expect(wrapper.find('.session-branch').exists()).toBe(false)
  expect(wrapper.find('.session-cost').exists()).toBe(false)
  expect(wrapper.find('[data-testid="session-topics"]').exists()).toBe(false)
  // ...but show up when hovering
  await wrapper.trigger('mouseenter')
  const tip = wrapper.get('[data-testid="session-tooltip"]')
  expect(tip.text()).toContain('sidebar')
  expect(tip.text()).toContain('Messages:')
})

it('falls back to the first prompt until a summary exists', () => {
  const wrapper = mountItem({ initialPrompt: 'Lets look into a git worktree' })
  const description = wrapper.get('[data-testid="session-description"]')
  expect(description.text()).toBe('Lets look into a git worktree')
  expect(description.classes()).toContain('is-prompt')
})

it('shows no description when there is neither', () => {
  expect(mountItem().find('[data-testid="session-description"]').exists()).toBe(false)
})

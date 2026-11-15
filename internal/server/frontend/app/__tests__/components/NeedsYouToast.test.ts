import { reactive } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { vi } from 'vitest'

const route = vi.hoisted(() => ({ value: null as any }))
vi.mock('vue-router', async (orig) => ({ ...(await orig() as any), useRoute: () => route.value }))

const notify = vi.hoisted(() => vi.fn(async () => true))
vi.mock('~/utils/desktop', async (orig) => ({ ...(await orig() as any), desktopNotify: notify }))

import NeedsYouToast from '~/components/NeedsYouToast.vue'
import { useSessionStore } from '~/stores/session/sessionStore'
import { announceAgentAttention } from '~/utils/agents/needsYou'

const question = (over: Record<string, unknown> = {}) => ({
  type: 'user_question', session_id: 's2', question_id: 'q1', session_name: 'Billing fixes',
  header: 'Approach', question: 'Which approach?', options: [{ label: 'A (Recommended)' }, { label: 'B' }],
  recommended: [0], auto_answer_at: new Date(Date.now() + 102_000).toISOString(), held: false, ...over,
})

let hidden = false
let focused = true

async function render() {
  const wrapper = mount(NeedsYouToast, {
    global: { stubs: { NuxtLink: { props: ['to'], template: '<a class="nuxt-link" :data-to="JSON.stringify(to)"><slot /></a>' } } },
  })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  setActivePinia(createPinia())
  route.value = reactive({ path: '/agents', fullPath: '/agents', query: {} })
  notify.mockClear()
  hidden = false
  focused = true
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => (hidden ? 'hidden' : 'visible') })
  document.hasFocus = () => focused
  const store = useSessionStore()
  store.activeSessionId = 's1'
})

it('shows a question from another session with its countdown and an Open link', async () => {
  const wrapper = await render()
  announceAgentAttention(question())
  await flushPromises()
  const toast = wrapper.get('[data-testid="needs-you-toast"]')
  expect(toast.text()).toContain('Billing fixes asks: Approach')
  expect(toast.text()).toMatch(/auto-picks in 1:4\d/)
  expect(JSON.parse(toast.get('.nuxt-link').attributes('data-to')!)).toEqual({ path: '/agents', query: { session: 's2' } })
  expect(toast.get('.nuxt-link').text()).toBe('Open')
  expect(notify).not.toHaveBeenCalled()
  wrapper.unmount()
})

it('stays quiet for the session on screen', async () => {
  const wrapper = await render()
  announceAgentAttention(question({ session_id: 's1' }))
  await flushPromises()
  expect(wrapper.find('[data-testid="needs-you-toast"]').exists()).toBe(false)
  wrapper.unmount()
})

it('shows the open session too when on another page', async () => {
  route.value.path = '/projects'
  route.value.fullPath = '/projects'
  const wrapper = await render()
  announceAgentAttention(question({ session_id: 's1' }))
  await flushPromises()
  expect(wrapper.findAll('[data-testid="needs-you-toast"]')).toHaveLength(1)
  wrapper.unmount()
})

it('removes the toast when the question is resolved, and dedupes repeats', async () => {
  const wrapper = await render()
  announceAgentAttention(question())
  announceAgentAttention(question())
  await flushPromises()
  expect(wrapper.findAll('[data-testid="needs-you-toast"]')).toHaveLength(1)
  announceAgentAttention({ type: 'user_question_resolved', session_id: 's2', question_id: 'q1', reason: 'auto_answered', answer: ['A (Recommended)'] })
  await flushPromises()
  expect(wrapper.find('[data-testid="needs-you-toast"]').exists()).toBe(false)
  wrapper.unmount()
})

it('drops the countdown when the question is held, and never shows one on a confirmation', async () => {
  const wrapper = await render()
  announceAgentAttention(question())
  announceAgentAttention(question({ question_id: 'q2', requires_user: true, recommended: [] }))
  await flushPromises()
  announceAgentAttention({ type: 'user_question_timer', session_id: 's2', question_id: 'q1', auto_answer_at: null, held: true })
  await flushPromises()
  for (const t of wrapper.findAll('[data-testid="needs-you-toast"]')) expect(t.text()).not.toContain('auto-picks')
  wrapper.unmount()
})

it('shows a permission request with Review and keeps at most three', async () => {
  const wrapper = await render()
  announceAgentAttention({ type: 'permission_request', session_id: 's3', permission_id: 'p1', session_name: 'Deploy', tool: 'Bash', details: { command: 'gh pr merge 1124' } })
  for (const id of ['qa', 'qb', 'qc']) announceAgentAttention(question({ question_id: id }))
  await flushPromises()
  const toasts = wrapper.findAll('[data-testid="needs-you-toast"]')
  expect(toasts).toHaveLength(3)
  expect(wrapper.text()).not.toContain('Deploy wants to run')

  wrapper.unmount()
  const again = await render()
  announceAgentAttention({ type: 'permission_request', session_id: 's3', permission_id: 'p1', session_name: 'Deploy', tool: 'Bash', details: { command: 'gh pr merge 1124' } })
  await flushPromises()
  const t = again.get('[data-testid="needs-you-toast"]')
  expect(t.text()).toContain('Deploy wants to run Bash: gh pr merge 1124')
  expect(t.get('.nuxt-link').text()).toBe('Review')
  again.unmount()
})

it('also notifies the desktop once when the window is in the background', async () => {
  hidden = true
  focused = false
  const wrapper = await render()
  announceAgentAttention(question({ session_id: 's1' }))
  announceAgentAttention(question({ session_id: 's1' }))
  await flushPromises()
  expect(notify).toHaveBeenCalledTimes(1)
  expect(notify.mock.calls[0]![0]).toBe('Billing fixes')
  expect(notify.mock.calls[0]![1]).toContain('Asks: Approach')
  // Hidden tab: even the open session gets a toast
  expect(wrapper.findAll('[data-testid="needs-you-toast"]')).toHaveLength(1)
  wrapper.unmount()
})

it('stays quiet for a question answered at once ("Auto-pick now")', async () => {
  hidden = true
  focused = false
  const wrapper = await render()
  announceAgentAttention(question({ question_id: 'q-now', auto_answer_at: new Date().toISOString() }))
  await flushPromises()
  expect(notify).not.toHaveBeenCalled()
  expect(wrapper.find('[data-testid="needs-you-toast"]').exists()).toBe(false)
  // A held question waits for the user even though its old deadline passed.
  announceAgentAttention(question({ question_id: 'q-held', auto_answer_at: new Date().toISOString(), held: true }))
  await flushPromises()
  expect(notify).toHaveBeenCalledTimes(1)
  wrapper.unmount()
})

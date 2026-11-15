import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'

const { fetchWithAuth, auth } = vi.hoisted(() => ({
  fetchWithAuth: vi.fn(),
  auth: { enabled: true, signedIn: true },
}))
vi.mock('~/composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => ({ fetchWithAuth }) }))
vi.mock('~/composables/useAuth', () => ({
  useAuth: () => ({ authEnabled: ref(auth.enabled), isAuthenticated: ref(auth.signedIn) }),
}))

import ResumeAfterRestartBanner from '~/components/agents/ResumeAfterRestartBanner.vue'

const json = (body: unknown, status = 200) => Promise.resolve(new Response(JSON.stringify(body), { status }))
const pending = (n: number) => ({ sessions: Array.from({ length: n }, (_, i) => ({ id: `s${i}`, why: 'turn' })), reason: 'quit', working_now: 0 })

beforeEach(() => {
  fetchWithAuth.mockReset()
  auth.enabled = true
  auth.signedIn = true
})

it('offers to continue the interrupted sessions and continues them in one click', async () => {
  fetchWithAuth.mockImplementation((_url: string, init?: RequestInit) => init?.method === 'POST' ? json({ resumed: 2 }) : json(pending(2)))
  const w = mount(ResumeAfterRestartBanner, { props: { connected: true } })
  await flushPromises()
  expect(w.text()).toContain('2 sessions were interrupted by the restart')
  await w.findAll('button').find(b => b.text() === 'Continue')!.trigger('click')
  await flushPromises()
  expect(fetchWithAuth).toHaveBeenLastCalledWith('/api/agent/resume-after-restart', { method: 'POST' })
  expect(w.find('.resume-banner').exists()).toBe(false)
})

it('dismiss drops the offer', async () => {
  fetchWithAuth.mockImplementation((_url: string, init?: RequestInit) => init?.method === 'DELETE' ? json({ dismissed: 1 }) : json(pending(1)))
  const w = mount(ResumeAfterRestartBanner, { props: { connected: true } })
  await flushPromises()
  expect(w.text()).toContain('1 session was interrupted')
  await w.findAll('button').find(b => b.text() === 'Dismiss')!.trigger('click')
  await flushPromises()
  expect(fetchWithAuth).toHaveBeenLastCalledWith('/api/agent/resume-after-restart', { method: 'DELETE' })
  expect(w.find('.resume-banner').exists()).toBe(false)
})

it('shows nothing when no session was interrupted, and checks again after a reconnect', async () => {
  fetchWithAuth.mockImplementationOnce(() => json(pending(0))).mockImplementation(() => json(pending(3)))
  const w = mount(ResumeAfterRestartBanner, { props: { connected: true } })
  await flushPromises()
  expect(w.find('.resume-banner').exists()).toBe(false)
  await w.setProps({ connected: false })
  await w.setProps({ connected: true })
  await flushPromises()
  expect(w.text()).toContain('3 sessions were interrupted')
})

it('is hidden without a signed-in user when user auth is on', async () => {
  auth.signedIn = false
  fetchWithAuth.mockImplementation(() => json(pending(2)))
  const w = mount(ResumeAfterRestartBanner, { props: { connected: true } })
  await flushPromises()
  expect(fetchWithAuth).not.toHaveBeenCalled()
  expect(w.find('.resume-banner').exists()).toBe(false)
})

it('explains a 403 instead of hiding the offer', async () => {
  fetchWithAuth.mockImplementation((_url: string, init?: RequestInit) => init?.method === 'POST' ? json({ code: 'user_required' }, 403) : json(pending(1)))
  const w = mount(ResumeAfterRestartBanner, { props: { connected: true } })
  await flushPromises()
  await w.findAll('button').find(b => b.text() === 'Continue')!.trigger('click')
  await flushPromises()
  expect(w.text()).toContain('Sign in to continue')
})

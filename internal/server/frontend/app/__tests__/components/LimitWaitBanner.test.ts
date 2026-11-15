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

import LimitWaitBanner from '~/components/agents/LimitWaitBanner.vue'

const json = (body: unknown, status = 200) => Promise.resolve(new Response(JSON.stringify(body), { status }))
const inAnHour = () => new Date(Date.now() + 3600_000).toISOString()
const wait = () => ({ why: 'turn', since: new Date().toISOString(), resume_at: inAnHour(), resets_at: inAnHour(), limit_type: 'five_hour' })

beforeEach(() => {
  fetchWithAuth.mockReset()
  auth.enabled = true
  auth.signedIn = true
})

it('says when the session continues', () => {
  const w = mount(LimitWaitBanner, { props: { sessionId: 's1', limitWait: wait() } })
  expect(w.text()).toMatch(/Continues .* \(in (1 h|60 min)\)/)
  expect(w.text()).toContain('5-hour window')
})

it('resume now continues the session', async () => {
  fetchWithAuth.mockImplementation(() => json({ resumed: true }))
  const w = mount(LimitWaitBanner, { props: { sessionId: 's1', limitWait: wait() } })
  await w.findAll('button').find(b => b.text() === 'Resume now')!.trigger('click')
  await flushPromises()
  expect(fetchWithAuth).toHaveBeenCalledWith('/api/agent/sessions/s1/limit-wait/resume', { method: 'POST' })
  expect(w.find('[data-testid="limit-wait-banner"]').exists()).toBe(false)
})

it("don't continue drops the wait", async () => {
  fetchWithAuth.mockImplementation(() => json({ cancelled: true }))
  const w = mount(LimitWaitBanner, { props: { sessionId: 's1', limitWait: wait() } })
  await w.findAll('button').find(b => b.text() === "Don't continue")!.trigger('click')
  await flushPromises()
  expect(fetchWithAuth).toHaveBeenCalledWith('/api/agent/sessions/s1/limit-wait', { method: 'DELETE' })
})

it('shows no buttons without a signed-in user', () => {
  auth.signedIn = false
  const w = mount(LimitWaitBanner, { props: { sessionId: 's1', limitWait: wait() } })
  expect(w.text()).toContain('Continues')
  expect(w.findAll('button')).toHaveLength(0)
})

it('asks to sign in when the server needs a user', async () => {
  fetchWithAuth.mockImplementation(() => json({ code: 'user_required' }, 403))
  const w = mount(LimitWaitBanner, { props: { sessionId: 's1', limitWait: wait() } })
  await w.findAll('button').find(b => b.text() === 'Resume now')!.trigger('click')
  await flushPromises()
  expect(w.text()).toContain('Sign in to change this.')
})

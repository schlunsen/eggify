import { mount, flushPromises } from '@vue/test-utils'
import PairDevice from '../../components/settings/PairDevice.vue'
import { formatCountdown, secondsLeft } from '../../composables/usePairing'

const PAIR = {
  id: 'p1',
  code: 'c0de',
  base_url: 'https://egg.ngrok.app',
  pair_url: 'eggify://pair?u=https%3A%2F%2Fegg.ngrok.app&c=c0de',
  ttl_seconds: 300,
}

let statusQueue: string[] = []
const calls: { method: string, url: string, body?: string }[] = []

function json(body: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
}

beforeEach(() => {
  vi.useFakeTimers()
  statusQueue = []
  calls.length = 0
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    calls.push({ method: init?.method || 'GET', url, body: init?.body as string | undefined })
    if (url === '/api/auth/pairing' && init?.method === 'POST') {
      return json({ ...PAIR, expires_at: new Date(Date.now() + 300_000).toISOString() })
    }
    if (url.endsWith('/status')) return json({ id: 'p1', status: statusQueue.shift() || 'pending', device_name: 'Dev’s iPhone' })
    if (init?.method === 'DELETE') return json({ cancelled: true })
    return json({ error: 'nope' }, 404)
  }))
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

it('formats the countdown', () => {
  expect(formatCountdown(299)).toBe('4:59')
  expect(formatCountdown(7)).toBe('0:07')
  expect(secondsLeft(new Date(Date.now() - 1000).toISOString())).toBe(0)
})

it('shows a pairing QR with a countdown and closes when the phone redeems it', async () => {
  const w = mount(PairDevice, { props: { baseUrl: 'https://egg.ngrok.app' } })
  await w.get('[data-testid="pair-create"]').trigger('click')
  await flushPromises()

  expect(JSON.parse(calls[0]!.body!)).toEqual({ base_url: 'https://egg.ngrok.app' })
  expect(w.find('[data-testid="pair-ticket"]').exists()).toBe(true)
  expect(w.text()).toContain('Point your iPhone camera at this code')
  expect(w.text()).toContain('as you')
  expect(w.get('[data-testid="pair-countdown"]').text()).toMatch(/^[45]:\d\d$/)
  // The code itself is only in the QR / link, not printed.
  expect(w.text()).not.toContain('c0de')

  await w.get('[data-testid="pair-show-link"]').trigger('click')
  expect((w.get('#pair-link-input').element as HTMLInputElement).value).toBe(PAIR.pair_url)

  statusQueue = ['redeemed']
  await vi.advanceTimersByTimeAsync(2100)
  await flushPromises()
  expect(w.find('[data-testid="pair-ticket"]').exists()).toBe(false)
  expect(w.get('[data-testid="pair-done"]').text()).toContain('Dev’s iPhone')
})

it('hides the code when it expires', async () => {
  const w = mount(PairDevice)
  await w.get('[data-testid="pair-create"]').trigger('click')
  await flushPromises()
  await vi.advanceTimersByTimeAsync(301_000)
  await flushPromises()
  expect(w.find('[data-testid="pair-ticket"]').exists()).toBe(false)
  expect(w.find('[data-testid="pair-expired"]').exists()).toBe(true)
})

it('cancels an unused code on Cancel', async () => {
  const w = mount(PairDevice)
  await w.get('[data-testid="pair-create"]').trigger('click')
  await flushPromises()
  await w.get('[data-testid="pair-cancel"]').trigger('click')
  await flushPromises()
  expect(calls.some(c => c.method === 'DELETE' && c.url === '/api/auth/pairing/p1')).toBe(true)
  expect(w.find('[data-testid="pair-ticket"]').exists()).toBe(false)
})

it('explains a user_required refusal', async () => {
  vi.stubGlobal('fetch', vi.fn(() => json({ error: 'Pairing a device requires a signed-in user', code: 'user_required' }, 403)))
  const w = mount(PairDevice)
  await w.get('[data-testid="pair-create"]').trigger('click')
  await flushPromises()
  expect(w.get('[data-testid="pair-error"]').text()).toContain('Sign in')
})

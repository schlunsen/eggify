import { mount, flushPromises } from '@vue/test-utils'
import AgentSetupWizard from '../../components/onboarding/AgentSetupWizard.vue'
import { useAgentRuntimes, __resetAgentRuntimesState, ONBOARDING_SEEN_KEY } from '../../composables/useAgentRuntimes'

const { fetchWithAuth } = vi.hoisted(() => ({ fetchWithAuth: vi.fn() }))
vi.mock('../../composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => ({ fetchWithAuth }) }))
const { shell } = vi.hoisted(() => ({ shell: { desktop: false } }))
vi.mock('../../utils/desktop', () => ({
  isDesktop: () => shell.desktop,
  desktopOpenExternal: vi.fn().mockResolvedValue(true),
}))

const NuxtLink = { props: ['to'], template: '<a :href="to"><slot /></a>' }

const json = (body: unknown, status = 200) => Promise.resolve(new Response(JSON.stringify(body), { status }))

const runtime = (id: 'claude' | 'codex', over: Record<string, unknown> = {}) => ({
  id, name: id === 'claude' ? 'Claude Code' : 'OpenAI Codex', installed: true, on_path: true, version: '1.0.0',
  version_ok: true, meets_recommended: true, auth: { state: 'not_logged_in' }, ready: false,
  login_command: `${id} login`, install: { commands: ['npm i'], docs_url: 'https://x' }, next_steps: [], checked_at: '',
  ...over,
})

beforeEach(() => {
  __resetAgentRuntimesState()
  fetchWithAuth.mockReset()
  localStorage.clear()
  shell.desktop = false
  document.body.innerHTML = ''
})

const mountWizard = () => mount(AgentSetupWizard, { attachTo: document.body, global: { stubs: { NuxtLink } } })

it('opens, lists both runtimes and marks itself seen on close', async () => {
  fetchWithAuth.mockImplementation(() => json({ runtimes: [runtime('claude', { ready: true, auth: { state: 'logged_in', method: 'subscription' } }), runtime('codex')], any_ready: true, logins: {} }))
  const w = mountWizard()
  const rt = useAgentRuntimes()
  rt.openWizard('first-run')
  await flushPromises()
  const dialog = document.querySelector('[role="dialog"]')!
  expect(dialog.getAttribute('aria-modal')).toBe('true')
  expect(dialog.textContent).toContain('Set up your agents')
  expect(dialog.textContent).toContain('Welcome to Eggify')
  expect(dialog.textContent).toContain('Claude Code is ready')
  expect(fetchWithAuth).toHaveBeenCalledWith('/api/agent/runtimes?refresh=1')

  // Escape closes and remembers it.
  dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
  await flushPromises()
  expect(rt.wizardOpen.value).toBe(false)
  expect(localStorage.getItem(ONBOARDING_SEEN_KEY)).toBe('1')
  w.unmount()
})

it('first run is desktop-only and only once', () => {
  const rt = useAgentRuntimes()
  expect(rt.shouldShowFirstRun()).toBe(false)
  shell.desktop = true
  expect(rt.shouldShowFirstRun()).toBe(true)
  localStorage.setItem(ONBOARDING_SEEN_KEY, '1')
  expect(rt.shouldShowFirstRun()).toBe(false)
})

it('starts a login, polls until it succeeds and refreshes the status', async () => {
  vi.useFakeTimers()
  let loggedIn = false
  fetchWithAuth.mockImplementation((url: string, opts?: RequestInit) => {
    if (url.startsWith('/api/agent/runtimes?') || url === '/api/agent/runtimes') {
      return json({ runtimes: [runtime('claude', loggedIn ? { ready: true, auth: { state: 'logged_in', method: 'subscription' } } : {}), runtime('codex')], logins: {} })
    }
    if (url === '/api/agent/runtimes/claude/login' && opts?.method === 'POST') {
      return json({ flow: { id: 'f', runtime: 'claude', mode: 'browser', state: 'waiting', browser_opened: true, accepts_code: false } })
    }
    if (url === '/api/agent/runtimes/claude/login') {
      loggedIn = true
      return json({ flow: { id: 'f', runtime: 'claude', mode: 'browser', state: 'succeeded', browser_opened: true, accepts_code: false } })
    }
    return json({}, 404)
  })
  const w = mountWizard()
  const rt = useAgentRuntimes()
  rt.openWizard('nudge')
  await flushPromises()
  expect(document.body.textContent).toContain('No agent is ready to run yet')

  const loginBtn = Array.from(document.querySelectorAll('button')).find(b => b.textContent?.includes('Log in with Claude'))!
  loginBtn.click()
  await flushPromises()
  expect(document.body.textContent).toContain('Waiting for you to finish in the browser')

  await vi.advanceTimersByTimeAsync(1600)
  await flushPromises()
  expect(rt.byId('claude')?.ready).toBe(true)
  expect(document.body.textContent).toContain('Signed in')
  vi.useRealTimers()
  w.unmount()
})

it('shows the sign-in-required refusal for an API-key-only caller', async () => {
  fetchWithAuth.mockImplementation((url: string, opts?: RequestInit) => {
    if (opts?.method === 'POST') return json({ error: 'x', code: 'user_required' }, 403)
    return json({ runtimes: [runtime('claude'), runtime('codex')], logins: {} })
  })
  const w = mountWizard()
  const rt = useAgentRuntimes()
  rt.openWizard()
  await flushPromises()
  await rt.startLogin('claude')
  await flushPromises()
  expect(document.body.textContent).toContain('requires you to be signed in')
  w.unmount()
})

it('codex in the desktop app opens the sign-in URL in the system browser', async () => {
  shell.desktop = true
  const { desktopOpenExternal } = await import('../../utils/desktop')
  fetchWithAuth.mockImplementation((url: string, opts?: RequestInit) => {
    if (opts?.method === 'POST') return json({ flow: { id: 'f', runtime: 'codex', mode: 'browser', state: 'waiting', browser_opened: false, accepts_code: false, auth_url: 'https://auth.openai.com/oauth/authorize?x=1' } })
    return json({ runtimes: [runtime('claude'), runtime('codex')], logins: {} })
  })
  const w = mountWizard()
  useAgentRuntimes().openWizard()
  await flushPromises()
  const btn = Array.from(document.querySelectorAll('button')).find(b => b.textContent?.includes('Log in with ChatGPT'))!
  btn.click()
  await flushPromises()
  expect(desktopOpenExternal).toHaveBeenCalledWith('https://auth.openai.com/oauth/authorize?x=1')
  useAgentRuntimes().stopPolling()
  w.unmount()
})

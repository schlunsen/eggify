import { mount, flushPromises } from '@vue/test-utils'
import AgentRuntimeCard from '../../components/onboarding/AgentRuntimeCard.vue'
import type { LoginFlow, RuntimeStatus } from '../../composables/useAgentRuntimes'

const NuxtLink = { props: ['to'], template: '<a :href="to"><slot /></a>' }

const base = (over: Partial<RuntimeStatus> = {}): RuntimeStatus => ({
  id: 'claude',
  name: 'Claude Code',
  installed: true,
  path: '/opt/homebrew/bin/claude',
  on_path: true,
  version: '2.1.280',
  min_version: '2.0.0',
  recommended_version: '2.1.280',
  version_ok: true,
  meets_recommended: true,
  auth: { state: 'not_logged_in' },
  ready: false,
  login_command: 'claude auth login',
  install: { commands: ['npm install -g @anthropic-ai/claude-code', 'brew install --cask claude-code'], docs_url: 'https://docs.example/claude' },
  next_steps: [],
  checked_at: '2026-10-03T00:00:00Z',
  ...over,
})

const flow = (over: Partial<LoginFlow> = {}): LoginFlow => ({
  id: 'f1', runtime: 'claude', mode: 'browser', state: 'waiting', browser_opened: true, accepts_code: false,
  started_at: '', expires_at: '', ...over,
})

const mountCard = (props: Record<string, unknown>) =>
  mount(AgentRuntimeCard, { props: { runtimeId: 'claude', ...props }, global: { stubs: { NuxtLink } } })

it('shows a ready runtime with plan and masked account', () => {
  const w = mountCard({ runtime: base({ ready: true, auth: { state: 'logged_in', method: 'subscription', plan: 'max', account: 'da•••@example.com' } }) })
  expect(w.attributes('data-state')).toBe('ready')
  expect(w.text()).toContain('Ready')
  expect(w.text()).toContain('Claude Max · da•••@example.com')
  expect(w.find('button.rt-btn--primary').exists()).toBe(false)
})

it('counts a provider key as ready', () => {
  const w = mountCard({ runtime: base({ ready: true, provider_keys: ['claude'], auth: { state: 'not_logged_in', method: 'provider_key' } }) })
  expect(w.text()).toContain('API key from Settings → Providers (claude)')
})

it('not installed: shows install commands with copy buttons and a re-check', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
  const w = mountCard({ runtime: base({ installed: false, version: undefined, version_ok: false, path: undefined }) })
  expect(w.attributes('data-state')).toBe('missing')
  expect(w.text()).toContain('Not installed')
  expect(w.text()).toContain('npm install -g @anthropic-ai/claude-code')
  await w.get('button[aria-label="Copy npm install -g @anthropic-ai/claude-code"]').trigger('click')
  await flushPromises()
  expect(writeText).toHaveBeenCalledWith('npm install -g @anthropic-ai/claude-code')
  expect(w.text()).toContain('Copied')
  await w.get('button.rt-btn--primary').trigger('click')
  expect(w.emitted('recheck')).toHaveLength(1)
})

it('not logged in: log in, console login and the API key path', async () => {
  const w = mountCard({ runtime: base() })
  expect(w.attributes('data-state')).toBe('logged-out')
  expect(w.text()).toContain('Not logged in')
  await w.get('button.rt-btn--primary').trigger('click')
  expect(w.emitted('login')?.[0]).toEqual([undefined])
  const consoleBtn = w.findAll('button.rt-link').find(b => b.text().includes('Console'))!
  await consoleBtn.trigger('click')
  expect(w.emitted('login')?.[1]).toEqual(['console'])
  expect(w.get('a[href="/providers"]').text()).toContain('Use an API key instead')
  expect(w.text()).toContain('claude auth login')
})

it('waiting for the browser: sign-in link, code field and cancel', async () => {
  const w = mountCard({
    runtime: base(),
    flow: flow({ auth_url: 'https://claude.com/cai/oauth/authorize?code=true', accepts_code: true }),
  })
  expect(w.attributes('data-state')).toBe('login')
  expect(w.text()).toContain('Waiting for you to finish in the browser')
  expect(w.get('a[target="_blank"]').attributes('href')).toBe('https://claude.com/cai/oauth/authorize?code=true')
  await w.get('input').setValue('  abc#def ')
  await w.get('form').trigger('submit')
  expect(w.emitted('code')?.[0]).toEqual(['abc#def'])
  const cancel = w.findAll('button').find(b => b.text() === 'Cancel')!
  await cancel.trigger('click')
  expect(w.emitted('cancel')).toHaveLength(1)
})

it('codex device code flow shows the code', () => {
  const w = mount(AgentRuntimeCard, {
    props: {
      runtimeId: 'codex',
      runtime: base({ id: 'codex', name: 'OpenAI Codex', login_command: 'codex login' }),
      flow: flow({ runtime: 'codex', mode: 'device', browser_opened: false, user_code: 'ABCD-1234', verification_url: 'https://auth.openai.com/codex/device' }),
    },
    global: { stubs: { NuxtLink } },
  })
  expect(w.text()).toContain('ABCD-1234')
  expect(w.get('a[target="_blank"]').text()).toContain('Open verification page')
})

it('a failed login shows its message and the actions again', () => {
  const w = mountCard({ runtime: base(), flow: flow({ state: 'failed', message: 'Login failed: Request failed with status code 400' }) })
  expect(w.get('[role="alert"]').text()).toContain('status code 400')
  expect(w.get('button.rt-btn--primary').text()).toContain('Log in with Claude')
})

it('warns when the CLI is off PATH or below the recommended version', () => {
  const w = mountCard({
    runtime: base({
      ready: true,
      auth: { state: 'logged_in', method: 'subscription' },
      meets_recommended: false,
      next_steps: [
        { kind: 'add_to_path', text: 'Found Claude Code at /opt/homebrew/bin/claude, but it is not on the PATH' },
        { kind: 'update', text: 'Update Claude Code to 2.1.280 or newer', command: 'npm install -g @anthropic-ai/claude-code@latest' },
      ],
    }),
  })
  expect(w.text()).toContain('not on the PATH')
  expect(w.text()).toContain('@latest')
})

it('checking state is busy', () => {
  const w = mountCard({ checking: true })
  expect(w.attributes('aria-busy')).toBe('true')
  expect(w.text()).toContain('Checking')
})

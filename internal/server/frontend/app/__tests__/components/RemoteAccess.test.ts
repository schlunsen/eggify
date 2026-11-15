import { mount, flushPromises } from '@vue/test-utils'
import { ref } from 'vue'
import RemoteAccess from '../../components/settings/RemoteAccess.vue'
import SettingsGlyph from '../../components/settings/Glyph.vue'
import type { TunnelStatus } from '../../composables/useTunnel'

const mocks = vi.hoisted(() => ({
  status: null as any,
  busy: null as any,
  urlCopied: null as any,
  start: vi.fn(),
  stop: vi.fn(),
  saveSettings: vi.fn(),
  deleteToken: vi.fn(),
  copyUrl: vi.fn(),
  openExternal: vi.fn(),
  fetchStatus: vi.fn(),
  setApiKey: vi.fn(),
  deleteApiKey: vi.fn(),
  listAgentSessions: vi.fn(),
  stopAgentSession: vi.fn(),
}))

vi.mock('../../composables/useTunnel', () => ({
  useTunnel: () => ({
    status: mocks.status,
    busy: mocks.busy,
    urlCopied: mocks.urlCopied,
    copyUrl: mocks.copyUrl,
    startPolling: vi.fn(),
    stopPolling: vi.fn(),
    saveSettings: mocks.saveSettings,
    start: mocks.start,
    stop: mocks.stop,
    deleteToken: mocks.deleteToken,
    fetchStatus: mocks.fetchStatus,
    setApiKey: mocks.setApiKey,
    deleteApiKey: mocks.deleteApiKey,
    listAgentSessions: mocks.listAgentSessions,
    stopAgentSession: mocks.stopAgentSession,
  }),
  NGROK_AGENT_LIMIT: 'ERR_NGROK_108',
  isAgentLimitError: (s: any) => !!s && (s.error_code === 'ERR_NGROK_108' || !!s.error?.includes('ERR_NGROK_108')),
}))
vi.mock('../../utils/desktop', () => ({ desktopOpenExternal: mocks.openExternal }))

const baseSettings = { token_set: false, autostart: false, protection: { mode: 'none' } }

function mountWith(status: Partial<TunnelStatus>) {
  mocks.status.value = { provider: 'ngrok', status: 'disconnected', can_start: true, settings: baseSettings, ...status }
  // Note.vue uses the auto-imported SettingsGlyph.
  return mount(RemoteAccess, { global: { components: { SettingsGlyph } } })
}

beforeEach(() => {
  mocks.status = ref<TunnelStatus | null>(null)
  mocks.busy = ref(false)
  mocks.urlCopied = ref(false)
  for (const fn of [mocks.start, mocks.stop, mocks.saveSettings, mocks.deleteToken, mocks.copyUrl, mocks.openExternal,
    mocks.fetchStatus, mocks.setApiKey, mocks.deleteApiKey, mocks.listAgentSessions, mocks.stopAgentSession]) fn.mockReset()
  mocks.setApiKey.mockResolvedValue({ ok: true })
  mocks.deleteApiKey.mockResolvedValue({ ok: true })
  mocks.listAgentSessions.mockResolvedValue({ ok: true, sessions: [] })
  mocks.stopAgentSession.mockResolvedValue({ ok: true })
  mocks.start.mockResolvedValue({ ok: true })
  mocks.stop.mockResolvedValue({ ok: true })
  mocks.saveSettings.mockResolvedValue({ ok: true })
  mocks.deleteToken.mockResolvedValue({ ok: true })
  mocks.openExternal.mockResolvedValue(true)
})

const toggle = (w: ReturnType<typeof mount>) => w.get('[data-testid="ra-toggle"]').element as HTMLInputElement

it('starts at step 1 without a token: switch disabled, token field shown', () => {
  const w = mountWith({ can_start: false, blocked: { code: 'no_token', message: 'add your ngrok authtoken first' } })
  expect(w.get('[data-testid="ra-status"]').text()).toContain('Off')
  expect(toggle(w).disabled).toBe(true)
  expect(w.find('[data-testid="ra-token-input"]').exists()).toBe(true)
  expect(w.find('[data-testid="ra-blocked"]').exists()).toBe(false) // no_token is shown as step 1, not an error
  expect(w.text()).toContain('Start with step 1')
})

it('opens the ngrok authtoken page through the desktop adapter', async () => {
  const w = mountWith({})
  await w.get('[data-testid="ra-open-ngrok"]').trigger('click')
  expect(mocks.openExternal).toHaveBeenCalledWith('https://dashboard.ngrok.com/get-started/your-authtoken')
})

it('saves a pasted token and never displays it', async () => {
  const w = mountWith({})
  await w.get('[data-testid="ra-token-input"]').setValue('  2abcdefSECRETtoken  ')
  await w.get('[data-testid="ra-token-save"]').trigger('submit')
  await flushPromises()
  expect(mocks.saveSettings).toHaveBeenCalledWith({ authtoken: '2abcdefSECRETtoken' })
  expect((w.get('[data-testid="ra-token-input"]').element as HTMLInputElement).value).toBe('')
})

it('shows only the masked token once saved', () => {
  const w = mountWith({ settings: { ...baseSettings, token_set: true, token_hint: '…wxyz', token_source: 'settings' } as any })
  expect(w.get('[data-testid="ra-token-set"]').text()).toContain('…wxyz')
  expect(w.find('[data-testid="ra-token-input"]').exists()).toBe(false)
  expect(toggle(w).disabled).toBe(false)
  expect(toggle(w).checked).toBe(false)
})

it('toggling calls start, then stop', async () => {
  const w = mountWith({ settings: { ...baseSettings, token_set: true, token_hint: '…wxyz' } as any })
  toggle(w).checked = true
  await w.get('[data-testid="ra-toggle"]').trigger('change')
  await flushPromises()
  expect(mocks.start).toHaveBeenCalledTimes(1)

  mocks.status.value = { ...mocks.status.value, status: 'connecting', running: true, message: 'Connecting to ngrok…' }
  await flushPromises()
  expect(w.get('[data-testid="ra-status"]').text()).toContain('Connecting')
  expect(w.get('[data-testid="ra-status-line"]').text()).toContain('Connecting to ngrok')
  expect(toggle(w).checked).toBe(true)

  toggle(w).checked = false
  await w.get('[data-testid="ra-toggle"]').trigger('change')
  await flushPromises()
  expect(mocks.stop).toHaveBeenCalledTimes(1)
})

it('shows the public URL, copy button and QR code when connected', async () => {
  const w = mountWith({
    status: 'connected', running: true, public_url: 'https://my-egg.ngrok-free.app',
    connected_since: new Date().toISOString(), region: 'eu',
    settings: { ...baseSettings, token_set: true, token_hint: '…wxyz' } as any,
  })
  expect(w.get('[data-testid="ra-status"]').text()).toContain('Online')
  expect(w.get('[data-testid="ra-url"]').text()).toContain('https://my-egg.ngrok-free.app')
  expect(w.find('[data-testid="qr-code"]').exists()).toBe(true)
  expect(w.find('[data-testid="qr-code"] path').attributes('d')).toMatch(/^M\d/)
  await w.get('[data-testid="ra-copy"]').trigger('click')
  expect(mocks.copyUrl).toHaveBeenCalled()
  await w.get('[data-testid="ra-url"]').trigger('click')
  expect(mocks.openExternal).toHaveBeenCalledWith('https://my-egg.ngrok-free.app')
})

it('surfaces an ngrok error', () => {
  const w = mountWith({
    status: 'error', running: false,
    error: 'ngrok rejected the authtoken (ERR_NGROK_105): The authtoken you specified is invalid.',
    settings: { ...baseSettings, token_set: true, token_hint: '…wxyz' } as any,
  })
  expect(w.get('[data-testid="ra-status"]').text()).toContain('Error')
  expect(w.get('[data-testid="ra-error"]').text()).toContain('ERR_NGROK_105')
  expect(toggle(w).checked).toBe(false)
  expect(toggle(w).disabled).toBe(false) // can retry
})

it('explains when user authentication is off', () => {
  const w = mountWith({
    can_start: false,
    blocked: { code: 'user_auth_disabled', message: 'Remote access needs user authentication.' },
    settings: { ...baseSettings, token_set: true, token_hint: '…wxyz' } as any,
  })
  expect(w.get('[data-testid="ra-blocked"]').text()).toContain('needs user authentication')
  expect(toggle(w).disabled).toBe(true)
})

it('shows a refused action (e.g. user_required) as an error', async () => {
  mocks.start.mockResolvedValue({ ok: false, error: 'Changing remote access requires a signed-in user', code: 'user_required' })
  const w = mountWith({ settings: { ...baseSettings, token_set: true, token_hint: '…wxyz' } as any })
  toggle(w).checked = true
  await w.get('[data-testid="ra-toggle"]').trigger('change')
  await flushPromises()
  expect(w.get('[data-testid="ra-action-error"]').text()).toContain('requires a signed-in user')
  expect(toggle(w).checked).toBe(false)
})

it('saves the autostart choice and the domain', async () => {
  const w = mountWith({ settings: { ...baseSettings, token_set: true } as any })
  const box = w.get('[data-testid="ra-autostart"]')
  ;(box.element as HTMLInputElement).checked = true
  await box.trigger('change')
  expect(mocks.saveSettings).toHaveBeenCalledWith({ autostart: true })

  await w.get('[data-testid="ra-domain-input"]').setValue('my-egg.ngrok-free.app')
  await w.get('[data-testid="ra-domain-save"]').trigger('submit')
  expect(mocks.saveSettings).toHaveBeenCalledWith({ domain: 'my-egg.ngrok-free.app' })
})

describe('ngrok agent session limit (ERR_NGROK_108)', () => {
  const limitError = 'ngrok: Your account is limited to 3 simultaneous ngrok agent sessions (ERR_NGROK_108). Other ngrok agents are using all of your account\'s sessions'
  const limitStatus = (settings: any = {}) => ({
    status: 'error' as const, running: false, error: limitError, error_code: 'ERR_NGROK_108',
    settings: { ...baseSettings, token_set: true, token_hint: '…wxyz', ...settings } as any,
  })
  const agents = [
    { id: 'ts_live', os: 'darwin', agent_version: '1.13.0', region: 'eu', ip: '203.0.113.5', started_at: new Date().toISOString(),
      eggify: true, eggify_host: 'my-mac', this_install: true, current: true },
    { id: 'ts_server', os: 'linux', agent_version: '3.20.0', region: 'us', ip: '198.51.100.7', started_at: '2026-10-01T10:00:00Z',
      metadata: 'build-box', eggify: false, this_install: false, current: false },
    { id: 'ts_old', os: 'darwin', eggify: true, eggify_host: 'my-mac', this_install: true, current: false },
  ]

  it('explains the limit without a key and offers the API key field', async () => {
    const w = mountWith(limitStatus())
    await flushPromises()
    expect(w.find('[data-testid="ra-limit"]').exists()).toBe(true)
    expect(w.find('[data-testid="ra-error"]').exists()).toBe(false)
    expect(w.get('[data-testid="ra-limit"]').text()).toContain('agent sessions are in use')
    expect(w.find('[data-testid="na-key-input"]').exists()).toBe(true)
    expect(w.text()).toContain('Add an ngrok API key to see and stop these sessions from Eggify')
    expect(mocks.listAgentSessions).not.toHaveBeenCalled()

    await w.get('[data-testid="ra-open-agents"]').trigger('click')
    expect(mocks.openExternal).toHaveBeenCalledWith('https://dashboard.ngrok.com/agents')
    await w.get('[data-testid="na-open-api-keys"]').trigger('click')
    expect(mocks.openExternal).toHaveBeenCalledWith('https://dashboard.ngrok.com/api-keys')

    await w.get('[data-testid="na-key-input"]').setValue('  2abcAPIkey_secret ')
    await w.get('[data-testid="na-key-save"]').trigger('submit')
    await flushPromises()
    expect(mocks.setApiKey).toHaveBeenCalledWith('2abcAPIkey_secret')
    expect(mocks.fetchStatus).toHaveBeenCalled()
  })

  it('also recognises the code in the error text', () => {
    const w = mountWith({ ...limitStatus(), error_code: undefined })
    expect(w.find('[data-testid="ra-limit"]').exists()).toBe(true)
  })

  it('lists the agents with a saved key and marks this Eggify', async () => {
    mocks.listAgentSessions.mockResolvedValue({ ok: true, sessions: agents })
    const w = mountWith(limitStatus({ api_key_set: true, api_key_hint: '…k3y9' }))
    await flushPromises()
    expect(w.get('[data-testid="na-key-set"]').text()).toContain('••••k3y9')
    expect(w.find('[data-testid="na-key-input"]').exists()).toBe(false)
    const items = w.findAll('[data-testid="na-item"]')
    expect(items).toHaveLength(3)
    expect(items[0]!.text()).toContain('my-mac')
    expect(items[0]!.find('[data-testid="na-current"]').exists()).toBe(true)
    expect(items[0]!.find('[data-testid="na-stop"]').exists()).toBe(false) // its own live session
    expect(items[0]!.text()).toContain('In use')
    expect(items[1]!.text()).toContain('Linux')
    expect(items[1]!.text()).toContain('198.51.100.7')
    expect(items[1]!.text()).toContain('build-box')
    expect(items[2]!.text()).toContain('old session')
  })

  it('stops an agent, then retries once by itself; Try again stays available', async () => {
    vi.useFakeTimers()
    try {
      mocks.listAgentSessions.mockResolvedValue({ ok: true, sessions: agents })
      const w = mountWith(limitStatus({ api_key_set: true, api_key_hint: '…k3y9' }))
      await flushPromises()
      await w.findAll('[data-testid="na-stop"]')[0]!.trigger('click')
      await flushPromises()
      expect(mocks.stopAgentSession).toHaveBeenCalledWith('ts_server')
      expect(w.find('[data-testid="na-stopped"]').exists()).toBe(true)
      expect(w.get('[data-testid="ra-retry-note"]').text()).toContain('Trying again')
      expect(mocks.start).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(1500)
      expect(mocks.start).toHaveBeenCalledTimes(1)

      // Still limited (e.g. the old session hasn't expired yet): a second
      // stop doesn't auto-retry again, the button does.
      await w.findAll('[data-testid="na-stop"]')[0]!.trigger('click')
      await flushPromises()
      await vi.advanceTimersByTimeAsync(1500)
      expect(mocks.start).toHaveBeenCalledTimes(1)
      expect(w.get('[data-testid="ra-retry-note"]').text()).toContain('Try again')
      await w.get('[data-testid="ra-retry"]').trigger('click')
      await flushPromises()
      expect(mocks.start).toHaveBeenCalledTimes(2)
    } finally {
      vi.useRealTimers()
    }
  })

  it('shows a clear error when ngrok refuses the API key', async () => {
    mocks.listAgentSessions.mockResolvedValue({ ok: false, code: 'invalid_key', error: 'ngrok rejected the API key. Create a new one at dashboard.ngrok.com/api-keys' })
    const w = mountWith(limitStatus({ api_key_set: true, api_key_hint: '…k3y9' }))
    await flushPromises()
    expect(w.get('[data-testid="na-list-error"]').text()).toContain('rejected the API key')
  })

  it('offers a Connected ngrok agents section outside the error', async () => {
    mocks.listAgentSessions.mockResolvedValue({ ok: true, sessions: agents.slice(0, 1) })
    const w = mountWith({ status: 'connected', running: true, public_url: 'https://x.ngrok-free.app',
      settings: { ...baseSettings, token_set: true, api_key_set: true, api_key_hint: '…k3y9' } as any })
    const section = w.get('[data-testid="ra-agents-section"]')
    expect(section.text()).toContain('Connected ngrok agents')
    expect(mocks.listAgentSessions).not.toHaveBeenCalled() // loads when opened
    ;(section.element as HTMLDetailsElement).open = true
    await section.trigger('toggle')
    await flushPromises()
    expect(mocks.listAgentSessions).toHaveBeenCalledTimes(1)
    expect(w.findAll('[data-testid="na-item"]')).toHaveLength(1)
  })
})

describe('Eggify relay mode', () => {
  const relaySettings = {
    ...baseSettings,
    relay_available: true,
    mode: 'relay',
    default_relay_addr: 'relay.example.com:8443',
    relay_pin: 'abcDEF123_-pin',
  }

  it('hides the mode choice when the build has no relay', () => {
    const w = mountWith({})
    expect(w.find('[data-testid="ra-mode"]').exists()).toBe(false)
    expect(w.find('[data-testid="ra-token-input"]').exists()).toBe(true)
  })

  it('switches to the relay from the mode choice', async () => {
    const w = mountWith({ settings: { ...baseSettings, relay_available: true, mode: 'ngrok' } as any })
    await w.get('[data-testid="ra-mode-relay"]').trigger('change')
    expect(mocks.saveSettings).toHaveBeenCalledWith({ mode: 'relay' })
  })

  it('needs no ngrok setup and shows the relay and the pin', () => {
    const w = mountWith({ settings: relaySettings as any })
    expect(w.find('[data-testid="ra-token-input"]').exists()).toBe(false)
    expect(w.find('[data-testid="ra-domain-input"]').exists()).toBe(false)
    expect(w.find('[data-testid="ra-agents-section"]').exists()).toBe(false)
    expect(w.text()).not.toContain('Extra protection at ngrok')
    expect(w.get('[data-testid="ra-relay-step"]').text()).toContain('relay.example.com:8443')
    expect(w.get('[data-testid="ra-relay-pin"]').text()).toBe('abcDEF123_-pin')
    expect(toggle(w).disabled).toBe(false)
    expect(w.text()).not.toContain('Start with step 1')
  })

  it('says a relay address is needed when the build has none', () => {
    const w = mountWith({ settings: { ...relaySettings, default_relay_addr: '', relay_addr: '' } as any })
    expect(w.get('[data-testid="ra-relay-needed"]').text()).toContain('relay address is needed')
    expect(w.get('[data-testid="ra-relay-input"]').exists()).toBe(true)
  })

  it('saves another relay address', async () => {
    const w = mountWith({ settings: relaySettings as any })
    await w.get('[data-testid="ra-relay-input"]').setValue(' relay.example.com:8443 ')
    await w.get('[data-testid="ra-relay-input"]').element.form!.dispatchEvent(new Event('submit'))
    await flushPromises()
    expect(mocks.saveSettings).toHaveBeenCalledWith({ relay_addr: 'relay.example.com:8443' })
  })

  it('explains the certificate when connected', () => {
    const w = mountWith({
      status: 'connected', running: true, provider: 'relay',
      public_url: 'https://abcdefghijklmnopqrstuvwxyz234567.r.example.com',
      settings: relaySettings as any,
    })
    expect(w.get('[data-testid="ra-relay-hint"]').text()).toContain('certificate')
  })
})

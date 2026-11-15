// Episode 8: remote access (relay and ngrok), pairing a device, signed-in devices.
// Nothing connects for real: the tunnel status, the pairing code and the device list are
// stubbed API answers with fictional addresses and codes, shown by the real settings page.
import { libShot } from '../lib.mjs'
import { openWorld, go, sleep } from '../world.mjs'

const RELAY_ID = 'norwegianbluedemoinstall2345abcd'          // 32 base32 characters, like a real install id; not one
const RELAY_URL = `https://${RELAY_ID}.r.example.com`
const PIN = 'DemoPinNotARealKeyDemoPinNotARealKeyDemoPin'     // 43 characters, the length of a real pin
const NGROK_DOMAIN = 'norwegian-blue-demo.ngrok.app'
const ago = m => new Date(Date.now() - m * 60000).toISOString()

const settings = (mode, extra = {}) => ({
  token_set: false, token_source: '', autostart: false, protection: { mode: 'none' }, api_key_set: false,
  mode, relay_addr: '', default_relay_addr: 'relay.example.com:8443', relay_available: true, ...extra,
})
const STATES = {
  relayOff: { status: 'disconnected', provider: 'relay', running: false, can_start: true, settings: settings('relay') },
  relayOn: { status: 'connected', provider: 'relay', public_url: RELAY_URL, region: 'relay.example.com:8443', connected_since: ago(42), running: true, can_start: true, settings: settings('relay', { relay_pin: PIN, autostart: true }) },
  ngrokNoToken: { status: 'disconnected', provider: 'ngrok', running: false, can_start: false, blocked: { code: 'no_token', message: 'Add your ngrok authtoken to connect.' }, settings: settings('ngrok') },
  ngrokOff: { status: 'disconnected', provider: 'ngrok', running: false, can_start: true, settings: settings('ngrok', { token_set: true, token_hint: '…d3m0', token_source: 'settings', domain: NGROK_DOMAIN }) },
  ngrokOn: { status: 'connected', provider: 'ngrok', public_url: `https://${NGROK_DOMAIN}`, domain: NGROK_DOMAIN, region: 'eu', connected_since: ago(17), running: true, can_start: true, settings: settings('ngrok', { token_set: true, token_hint: '…d3m0', token_source: 'settings', domain: NGROK_DOMAIN, autostart: true }) },
}
const DEVICES = () => ({
  count: 4,
  sessions: [
    { id: 'dev-demo-1', username: 'praline', device_name: 'Chrome on Mac', device_model: 'Macintosh', created_via: 'password', created_at: ago(3 * 1440), last_used_at: ago(0), expires_at: ago(-4 * 1440), current: true },
    { id: 'dev-demo-2', username: 'praline', device_name: 'Mr. Praline\'s iPhone', device_model: 'iPhone 17 Pro', created_via: 'pairing', created_at: ago(2 * 1440 + 30), last_used_at: ago(11), expires_at: ago(-5 * 1440), current: false },
    { id: 'dev-demo-3', username: 'praline', device_name: 'Back-room Mac mini', device_model: 'Mac mini', created_via: 'pairing', created_at: ago(6 * 1440), last_used_at: ago(190), expires_at: ago(-1 * 1440), current: false },
    { id: 'dev-demo-4', username: 'praline', device_name: 'Eggify for Mac', device_model: 'MacBook Pro', created_via: 'setup', created_at: ago(9 * 1440), last_used_at: ago(26 * 60), expires_at: ago(-2 * 1440), current: false },
  ],
})

export default async function (server) {
  const app = await openWorld(server)
  const { browser, context, page } = app
  let state = 'relayOff'
  try {
    await context.route('**/api/tunnel/status*', route => route.fulfill({ json: STATES[state] }))
    await context.route('**/api/auth/sessions*', route => (route.request().method() === 'GET' ? route.fulfill({ json: DEVICES() }) : route.continue()))
    await context.route('**/api/auth/pairing', route => {
      if (route.request().method() !== 'POST') return route.continue()
      const base = STATES[state].public_url || RELAY_URL
      const relay = base === RELAY_URL
      route.fulfill({ json: {
        id: 'pair-demo-1', code: 'DEMO-CODE-NOT-REAL', base_url: base, expires_at: new Date(Date.now() + 298000).toISOString(), ttl_seconds: 300,
        pair_url: `eggify://pair?u=${encodeURIComponent(base)}&c=DEMO-CODE-NOT-REAL${relay ? `&p=${PIN}` : ''}`, ...(relay ? { cert_pin: PIN } : {}),
      } })
    })
    await context.route('**/api/auth/pairing/*/status', route => route.fulfill({ json: { id: 'pair-demo-1', status: 'pending', expires_at: new Date(Date.now() + 298000).toISOString() } }))

    const open = async (s, section = 'access') => {
      state = s
      await go(page, server, `/settings#${section}`)
      await page.waitForSelector('[data-testid=remote-access]')
      await page.locator(`#${section}`).evaluate(el => el.scrollIntoView({ block: 'start' }))
      await sleep(900)
    }
    const HOW = 'stubbed API (/api/tunnel/status with a fictional address); nothing is connected'

    await open('relayOff')
    await libShot(page, 'remote', 'relay-off', {
      shows: 'Settings → Remote access, Eggify relay mode, switched off: the mode choice, the big on/off switch, status "Not connected"',
      how: HOW, pad: 4,
      boxes: { section: '#access', card: '[data-testid=remote-access]', mode: '[data-testid=ra-mode]', relayMode: '[data-testid=ra-mode-relay]', ngrokMode: '[data-testid=ra-mode-ngrok]', toggle: '[data-testid=ra-toggle]', status: '[data-testid=ra-status]', 'pair?': '[data-testid=pair-device]' },
    })
    await open('relayOn')
    await page.waitForSelector('[data-testid=ra-live]')
    await libShot(page, 'remote', 'relay-on', {
      shows: 'Eggify relay mode connected: the install\'s own address with Copy and a QR code, the key pin, connected since',
      how: HOW + '; the address and pin are made up', pad: 4,
      boxes: { card: '[data-testid=remote-access]', toggle: '[data-testid=ra-toggle]', status: '[data-testid=ra-status]', live: '[data-testid=ra-live]', url: '[data-testid=ra-url]', 'copy?': '[data-testid=ra-copy]', 'qr?': '.ra-qr', 'pin?': '[data-testid=ra-relay-pin]' },
    })
    // pairing code (asks the stubbed endpoint)
    const pair = page.locator('#access [data-testid=pair-device]')
    await pair.evaluate(el => el.scrollIntoView({ block: 'center' }))
    await pair.locator('[data-testid=pair-create]').click()
    await page.waitForSelector('[data-testid=pair-ticket]')
    await pair.evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(700)
    await libShot(page, 'remote', 'pair-device', {
      shows: 'Pair a device: the one-time pairing QR code for the iPhone app, its countdown (5 minutes), Show link',
      how: 'stubbed API (POST /api/auth/pairing answers a made-up code; no real code exists)', pad: 4,
      boxes: { pair: pair, ticket: '[data-testid=pair-ticket]', 'qr?': pair.locator('[data-testid=qr-code]'), 'countdown?': '[data-testid=pair-countdown]', 'showLink?': '[data-testid=pair-show-link]' },
    })

    await open('ngrokNoToken')
    await libShot(page, 'remote', 'ngrok-no-token', {
      shows: 'ngrok mode before setup: paste your authtoken (with the button to ngrok\'s dashboard), optional reserved domain',
      how: HOW, pad: 4,
      boxes: { card: '[data-testid=remote-access]', mode: '[data-testid=ra-mode]', 'tokenInput?': '[data-testid=ra-token-input]', 'openNgrok?': '[data-testid=ra-open-ngrok]', 'blocked?': '[data-testid=ra-blocked]', toggle: '[data-testid=ra-toggle]' },
    })
    await open('ngrokOff')
    await libShot(page, 'remote', 'ngrok-off', {
      shows: 'ngrok mode with a token saved (only its last characters shown), reserved domain, switched off',
      how: HOW, pad: 4,
      boxes: { card: '[data-testid=remote-access]', toggle: '[data-testid=ra-toggle]', status: '[data-testid=ra-status]', 'tokenSet?': '[data-testid=ra-token-set]', 'domain?': '[data-testid=ra-domain-input]', 'autostart?': '[data-testid=ra-autostart]' },
    })
    await open('ngrokOn')
    await page.waitForSelector('[data-testid=ra-live]')
    await libShot(page, 'remote', 'ngrok-on', {
      shows: 'ngrok mode connected: the public URL with Copy and an address QR code, region, connected since',
      how: HOW + '; the domain is made up', pad: 4,
      boxes: { card: '[data-testid=remote-access]', toggle: '[data-testid=ra-toggle]', status: '[data-testid=ra-status]', live: '[data-testid=ra-live]', url: '[data-testid=ra-url]', 'qr?': '.ra-qr' },
    })

    // Account & security: signed-in devices
    state = 'relayOn'
    await go(page, server, '/settings#account')
    await page.waitForSelector('[data-testid=signed-in-devices] [data-testid=device-row]')
    await page.locator('[data-testid=signed-in-devices]').evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(700)
    await libShot(page, 'remote', 'signed-in-devices', {
      shows: 'Settings → Account & security → Signed-in devices: this browser, a paired iPhone, another Mac; how each signed in, last used, Sign out',
      how: 'stubbed API (GET /api/auth/sessions with fictional devices)', pad: 4,
      boxes: { devices: '[data-testid=signed-in-devices]', current: page.locator('[data-testid=device-row]').first(), phone: page.locator('[data-testid=device-row]').nth(1), 'signOut?': '[data-testid=device-signout-dev-demo-2]' },
    })
  } finally { await browser.close() }
}

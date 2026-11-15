#!/usr/bin/env node
// Episode 8 "From the sofa": takes the shots named in episode.json.
//
//   learn/capture/library/server.sh --port 3408     # another terminal: the library's demo world
//   node learn/episodes/08-from-the-sofa/capture.mjs        # [--port 3408]
//
// Same states as the library's `remote` area (capture/library/areas/remote.mjs), re-shot into this
// episode's folder. Nothing connects for real. Stubbed API answers (fictional data through the
// real settings page): /api/tunnel/status (relay off/on, ngrok no token/on, made-up address, pin and
// domain), POST /api/auth/pairing (a made-up code, so no real code or URL exists) and its status,
// GET /api/auth/sessions (fictional devices), /api/system/usage (calm meter).
// phone-home: NOT shot here. It is the promo film's app-made iPhone screenshot
// (promo/public/shots/11-phone-home.png, fictional sessions) centred on a paper-coloured
// 3840x2160 canvas with sharp; boxes are placed by hand. No iOS simulator was run.
import { drawAllBoxes, parseArgs, readServer, shot as episodeShot, shotsDir, ROOT } from '../../capture/lib.mjs'
import { openWorld, go, sleep } from '../../capture/library/world.mjs'
import { createRequire } from 'node:module'
const sharp = createRequire(import.meta.url)('../../capture/node_modules/sharp')
import { writeFileSync, mkdirSync } from 'node:fs'
import { join } from 'node:path'

const SLUG = 'from-the-sofa'
const args = parseArgs()
const server = readServer(Number(args.port || 3408))
const RELAY_ID = 'norwegianbluedemoinstall2345abcd'
const RELAY_URL = `https://${RELAY_ID}.r.example.com`
const PIN = 'DemoPinNotARealKeyDemoPinNotARealKeyDemoPin'
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
  ngrokOn: { status: 'connected', provider: 'ngrok', public_url: `https://${NGROK_DOMAIN}`, domain: NGROK_DOMAIN, region: 'eu', connected_since: ago(17), running: true, can_start: true, settings: settings('ngrok', { token_set: true, token_hint: '…d3m0', token_source: 'settings', domain: NGROK_DOMAIN, autostart: true }) },
}
const DEVICES = () => ({
  count: 4,
  sessions: [
    { id: 'dev-demo-1', username: 'praline', device_name: 'Chrome on Mac', device_model: 'Macintosh', created_via: 'password', created_at: ago(3 * 1440), last_used_at: ago(0), expires_at: ago(-4 * 1440), current: true },
    { id: 'dev-demo-2', username: 'praline', device_name: "Mr. Praline's iPhone", device_model: 'iPhone 17 Pro', created_via: 'pairing', created_at: ago(2 * 1440 + 30), last_used_at: ago(11), expires_at: ago(-5 * 1440), current: false },
    { id: 'dev-demo-3', username: 'praline', device_name: 'Back-room Mac mini', device_model: 'Mac mini', created_via: 'pairing', created_at: ago(6 * 1440), last_used_at: ago(190), expires_at: ago(-1 * 1440), current: false },
    { id: 'dev-demo-4', username: 'praline', device_name: 'Eggify for Mac', device_model: 'MacBook Pro', created_via: 'setup', created_at: ago(9 * 1440), last_used_at: ago(26 * 60), expires_at: ago(-2 * 1440), current: false },
  ],
})
// optional boxes (name ending in ?) are dropped when absent
async function libShot(page, name, boxes) {
  const keep = {}
  for (const [k, v] of Object.entries(boxes)) {
    const opt = k.endsWith('?'); const id = opt ? k.slice(0, -1) : k
    if (opt) { const loc = typeof v === 'string' ? page.locator(v) : v; if (typeof v === 'object' && 'x' in v && !('count' in v)) { keep[id] = v; continue } if (!(await loc.count())) continue }
    keep[id] = v
  }
  await episodeShot(page, SLUG, name, { boxes: keep })
}
{
  const app = await openWorld(server, { preload: false, subagents: false })
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
    await open('relayOff')
    await libShot(page, 'relay-off', { card: '[data-testid=remote-access]', mode: '[data-testid=ra-mode]', relayMode: '[data-testid=ra-mode-relay]', ngrokMode: '[data-testid=ra-mode-ngrok]', toggle: '[data-testid=ra-toggle]', status: '[data-testid=ra-status]' })
    await open('relayOn')
    await page.waitForSelector('[data-testid=ra-live]')
    await libShot(page, 'relay-on', { card: '[data-testid=remote-access]', toggle: '[data-testid=ra-toggle]', status: '[data-testid=ra-status]', live: '[data-testid=ra-live]', url: '[data-testid=ra-url]', 'copy?': '[data-testid=ra-copy]', 'qr?': '.ra-qr', 'pin?': '[data-testid=ra-relay-pin]', pairButton: '#access [data-testid=pair-create]', pair: '#access [data-testid=pair-device]' })
    const pair = page.locator('#access [data-testid=pair-device]')
    await pair.evaluate(el => el.scrollIntoView({ block: 'center' }))
    await pair.locator('[data-testid=pair-create]').click()
    await page.waitForSelector('[data-testid=pair-ticket]')
    await pair.evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(700)
    await libShot(page, 'pair-device', { pair, ticket: '[data-testid=pair-ticket]', 'qr?': pair.locator('[data-testid=qr-code]'), 'countdown?': '[data-testid=pair-countdown]', 'showLink?': '[data-testid=pair-show-link]' })
    await open('ngrokNoToken')
    await libShot(page, 'ngrok-no-token', { mode: '[data-testid=ra-mode]', ngrokMode: '[data-testid=ra-mode-ngrok]', 'tokenInput?': '[data-testid=ra-token-input]', 'openNgrok?': '[data-testid=ra-open-ngrok]', toggle: '[data-testid=ra-toggle]' })
    await open('ngrokOn')
    await page.waitForSelector('[data-testid=ra-live]')
    await libShot(page, 'ngrok-on', { toggle: '[data-testid=ra-toggle]', status: '[data-testid=ra-status]', live: '[data-testid=ra-live]', url: '[data-testid=ra-url]', 'qr?': '.ra-qr' })
    state = 'relayOn'
    await go(page, server, '/settings#account')
    await page.waitForSelector('[data-testid=signed-in-devices] [data-testid=device-row]')
    await page.locator('[data-testid=signed-in-devices]').evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(700)
    await libShot(page, 'signed-in-devices', { devices: '[data-testid=signed-in-devices]', current: page.locator('[data-testid=device-row]').first(), phone: page.locator('[data-testid=device-row]').nth(1), 'signOut?': '[data-testid=device-signout-dev-demo-2]' })
  } finally { await browser.close() }
}
// phone-home: the promo film's iPhone screenshot on a paper canvas
{
  const dir = shotsDir(SLUG); mkdirSync(dir, { recursive: true })
  const bg = await sharp(join(dir, 'relay-off.png')).extract({ left: 3700, top: 1500, width: 1, height: 1 }).raw().toBuffer()
  const src = join(ROOT, 'promo', 'public', 'shots', '11-phone-home.png')
  const phone = await sharp(src).resize({ height: 2000 }).png().toBuffer()
  const { width: pw } = await sharp(phone).metadata()
  const left = Math.round((3840 - pw) / 2), top = 80
  await sharp({ create: { width: 3840, height: 2160, channels: 3, background: { r: bg[0], g: bg[1], b: bg[2] } } })
    .composite([{ input: phone, left, top }]).png().toFile(join(dir, 'phone-home.png'))
  const cx = left / 2, cy = top / 2, k = pw / 2 / 331   // css px per preview px (331 px wide preview)
  const box = (px, py, w, h) => ({ x: Math.round(cx + px * k), y: Math.round(cy + py * k), w: Math.round(w * k), h: Math.round(h * k) })
  writeFileSync(join(dir, 'phone-home.json'), JSON.stringify({ boxes: { phone: { x: Math.round(cx), y: Math.round(cy), w: Math.round(pw / 2), h: 1000 }, working: box(14, 278, 304, 132), recent: box(14, 462, 304, 200) } }, null, 2))
}
await drawAllBoxes(SLUG)

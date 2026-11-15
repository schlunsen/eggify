// Helpers for the screen library (learn/public/library/shots/<area>/<name>.png|json).
// Thin layer over ../lib.mjs: same viewport, scale and box conventions, another output folder,
// plus a WebSocket bridge and list patchers for states that need a live agent on a real install.
import sharp from 'sharp'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { LEARN_DIR, SCALE, VIEWPORT, openApp, settle, sleep } from '../lib.mjs'

export * from '../lib.mjs'

export const LIB_DIR = dirname(fileURLToPath(import.meta.url))
export const LIBRARY_SHOTS = join(LEARN_DIR, 'public', 'library', 'shots')
export const PROOFS = join(LIB_DIR, '.proofs')
export const PORT = 3420

/** A record of every shot of this run, for INDEX.md (written by run.mjs). */
export const taken = []

async function boxOf(page, target) {
  if (target && typeof target === 'object' && 'x' in target && 'w' in target) return target
  const loc = typeof target === 'string' ? page.locator(target).first() : target
  const b = await loc.boundingBox({ timeout: 1500 }).catch(() => null)
  if (!b) return null
  return { x: Math.round(b.x), y: Math.round(b.y), w: Math.round(b.width), h: Math.round(b.height) }
}

/**
 * Like ../lib.mjs shot(), but writes learn/public/library/shots/<area>/<name>.png + .json and a
 * proof (1600 px wide JPEG with the boxes drawn) to learn/capture/library/.proofs/<area>/<name>.jpg.
 * A box partly outside the viewport is clipped to it; a missing box is an error unless its id ends in "?".
 * opts.how: 'seeded' | 'stubbed API' | 'stubbed event' … (free text for the index); opts.shows: one line.
 */
export async function libShot(page, area, name, opts = {}) {
  if (opts.settle !== false) await settle(page, { timeout: 5000, ...(opts.settleOpts || {}) })
  const dir = join(LIBRARY_SHOTS, area)
  mkdirSync(dir, { recursive: true })
  const boxes = {}
  for (const [rawId, target] of Object.entries(opts.boxes || {})) {
    const optional = rawId.endsWith('?')
    const id = optional ? rawId.slice(0, -1) : rawId
    let b = await boxOf(page, target)
    if (!b) {
      if (optional) continue
      throw new Error(`shot ${area}/${name}: box "${id}" not found`)
    }
    const pad = opts.pad ?? 0
    let x = Math.max(0, b.x - pad), y = Math.max(0, b.y - pad)
    let x2 = Math.min(VIEWPORT.width, b.x + b.w + pad), y2 = Math.min(VIEWPORT.height, b.y + b.h + pad)
    b = { x, y, w: x2 - x, h: y2 - y }
    if (b.w < 4 || b.h < 4) {
      if (optional) continue
      throw new Error(`shot ${area}/${name}: box "${id}" is outside the viewport or empty: ${JSON.stringify(b)}`)
    }
    boxes[id] = b
  }
  const png = join(dir, `${name}.png`)
  for (let attempt = 1; ; attempt++) {
    try {
      await page.screenshot({ path: png, type: 'png', animations: 'disabled', caret: 'hide', scale: 'device', timeout: 12000 })
      break
    } catch (e) {     // Chromium sometimes waits for a web font that never reports "loaded"
      if (attempt >= 3) throw e
      console.warn(`  [shot] ${name}: screenshot timed out, trying again`)
      await sleep(800)
    }
  }
  const meta = await sharp(png).metadata()
  if (meta.width !== VIEWPORT.width * SCALE || meta.height !== VIEWPORT.height * SCALE) {
    throw new Error(`shot ${name}: ${meta.width}×${meta.height}`)
  }
  writeFileSync(join(dir, `${name}.json`), JSON.stringify({ boxes }, null, 2) + '\n')
  await proof(area, name)
  taken.push({ area, name, boxes: Object.keys(boxes), how: opts.how || 'seeded', shows: opts.shows || '' })
  console.log(`  shot ${area}/${name}  [${Object.keys(boxes).join(', ')}]`)
  return { png, boxes }
}

/** Proof sheet: the shot at 1600 px wide with its boxes drawn and named. */
export async function proof(area, name) {
  const dir = join(LIBRARY_SHOTS, area)
  const { boxes } = JSON.parse(readFileSync(join(dir, `${name}.json`), 'utf8'))
  const colors = ['#e6194b', '#3cb44b', '#4363d8', '#f58231', '#911eb4', '#008080', '#9a6324', '#800000']
  const rects = Object.entries(boxes).map(([id, b], i) => {
    const c = colors[i % colors.length]
    const ty = b.y > 22 ? b.y - 6 : b.y + b.h + 16
    return `<rect x="${b.x}" y="${b.y}" width="${b.w}" height="${b.h}" fill="none" stroke="${c}" stroke-width="3"/>
      <text x="${b.x}" y="${ty}" font-family="Helvetica" font-size="16" font-weight="bold" fill="${c}" stroke="#fff" stroke-width="3" paint-order="stroke">${id}</text>`
  }).join('\n')
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${VIEWPORT.width}" height="${VIEWPORT.height}">${rects}</svg>`
  const out = join(PROOFS, area)
  mkdirSync(out, { recursive: true })
  const file = join(out, `${name}.jpg`)
  const big = await sharp(join(dir, `${name}.png`)).resize(VIEWPORT.width, VIEWPORT.height)
    .composite([{ input: Buffer.from(svg), left: 0, top: 0 }]).png().toBuffer()
  await sharp(big).resize(1600).jpeg({ quality: 72 }).toFile(file)
  return file
}

/**
 * Bridge for the page's agent WebSocket (/agent/ws): everything passes through to the real
 * server, and the capture script can
 *   inject(msg)            send the page a message the server would send during a live turn
 *   dropOut(pred)          keep a page → server message from being sent (e.g. an answer to a demo card)
 *   mapIn(fn)              change a server → page message (fn returns the message, or null to drop it)
 *   waitFor(type, ms)      wait until the server sent a message of that type
 * Install before the first navigation.
 */
export async function wsBridge(context) {
  const st = { pages: new Set(), servers: [], outDrops: [], inMaps: [], seen: [], sent: [] }
  await context.routeWebSocket(/\/agent\/ws/, ws => {
    const server = ws.connectToServer()
    st.pages.add(ws)
    st.servers.push(server)
    ws.onClose(() => { st.pages.delete(ws); server.close() })
    ws.onMessage(raw => {
      let d = null
      try { d = JSON.parse(raw) } catch { /* not JSON */ }
      if (d && st.outDrops.some(f => f(d))) return
      if (d) st.sent.push(d)
      server.send(raw)
    })
    server.onMessage(raw => {
      let d = null
      try { d = JSON.parse(raw) } catch { /* not JSON */ }
      if (!d) return ws.send(raw)
      for (const f of st.inMaps) { d = f(d); if (!d) return }
      st.seen.push({ type: d.type, session_id: d.session_id, at: Date.now() })
      ws.send(JSON.stringify(d))
    })
  })
  return {
    inject: msg => { if (!st.pages.size) throw new Error('no agent socket yet'); for (const p of st.pages) { try { p.send(JSON.stringify(msg)) } catch { st.pages.delete(p) } } },
    /** Send the server a message as the page would (through the newest socket). */
    toServer: msg => { const sv = st.servers[st.servers.length - 1]; if (!sv) throw new Error('no agent socket yet'); sv.send(JSON.stringify(msg)) },
    dropOut: pred => st.outDrops.push(pred),
    mapIn: fn => st.inMaps.push(fn),
    seen: st.seen,
    sent: st.sent,
    async waitFor(type, { timeout = 10000, sessionId, since = 0 } = {}) {
      const t0 = Date.now()
      while (Date.now() - t0 < timeout) {
        if (st.seen.some(m => m.type === type && m.at >= since && (!sessionId || m.session_id === sessionId))) return true
        await sleep(100)
      }
      return false
    },
  }
}

/**
 * Patch sessions in both listings the UI reads: REST GET /api/agent/sessions and the WebSocket
 * `sessions_list` (and full sessions in session_updated / session_created).
 * patches: {<sessionId>: {field: value, …} | (session) => fields}. In-memory-only fields of a
 * live turn go here (subagents_running, scheduled_loop, loop_state, pending_*).
 */
export async function patchSessions(context, bridge, patches) {
  const apply = s => {
    if (!s || !s.id) return s
    const p = patches[s.id]
    if (!p) return s
    return { ...s, ...(typeof p === 'function' ? p(s) : p) }
  }
  await context.route(/\/api\/agent\/sessions(\?[^/]*)?$/, async route => {
    if (route.request().method() !== 'GET') return route.continue()
    const res = await route.fetch()
    let body
    try { body = await res.json() } catch { return route.fulfill({ response: res }) }
    if (Array.isArray(body.sessions)) body.sessions = body.sessions.map(apply)
    await route.fulfill({ response: res, json: body })
  })
  bridge?.mapIn(d => {
    if (d.type === 'sessions_list' && Array.isArray(d.sessions)) d.sessions = d.sessions.map(apply)
    if ((d.type === 'session_updated' || d.type === 'session_created') && d.session?.id) d.session = apply(d.session)
    return d
  })
}

/**
 * One signed-in session for the whole library, kept next to the server's data: the server allows
 * only a few logins per quarter of an hour, and every browser launch would otherwise be one.
 * Returns the cookie header value ("session_token=…").
 */
export async function sessionCookie(server) {
  const file = `/tmp/eggify-learn-${server.port}/library-session`
  if (existsSync(file)) {
    const cookie = readFileSync(file, 'utf8').trim()
    const res = await fetch(`${server.url}/api/auth/sessions`, { headers: { Cookie: cookie } }).catch(() => null)
    if (res?.ok) return cookie
  }
  const res = await fetch(`${server.url}/api/auth/login`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: server.user, password: server.password }),
  })
  if (!res.ok) throw new Error(`Sign-in failed: ${res.status} ${await res.text()}`)
  const cookie = (res.headers.getSetCookie?.() || []).map(c => c.split(';')[0]).find(c => c.startsWith('session_token='))
  if (!cookie) throw new Error('login returned no session cookie')
  writeFileSync(file, cookie + '\n', { mode: 0o600 })
  return cookie
}

/** openApp + bridge, ready for the agents page. Returns {browser, context, page, bridge}. */
export async function openLibraryApp(server, opts = {}) {
  const cookie = await sessionCookie(server)
  const app = await openApp(server, {
    ...opts, signIn: false,
    storage: {
      zenAudioEnabled: 'false', zenMusicEnabled: 'false', zenAudioDroneOn: 'false',
      'eggify-zen-circus-backdrop': 'meadow',
      ...(opts.storage || {}),
    },
  })
  await app.context.addCookies([{ name: 'session_token', value: cookie.slice('session_token='.length), url: server.url, httpOnly: true }])
  // openApp already made a page; the bridge must exist before its first navigation, which is the case.
  const bridge = await wsBridge(app.context)
  return { ...app, bridge }
}

export const iso = d => new Date(d).toISOString()
export const minsAgo = m => new Date(Date.now() - m * 60000)

/** Press a shortcut and give the UI a moment. */
export async function press(page, chord, wait = 500) {
  await page.keyboard.press(chord)
  await sleep(wait)
}

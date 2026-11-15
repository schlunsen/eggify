// Playwright helpers for the learning videos' screen captures.
//
// Every shot is a 1920×1080 CSS px viewport at device scale 2 (a 3840×2160 PNG) plus
// <name>.json with {"boxes": {"<id>": {x, y, w, h}}} in CSS px (see ../README.md).
// Captures only ever run against a demo server started by demo-server.sh.
import { chromium } from 'playwright'
import sharp from 'sharp'
import { spawnSync } from 'node:child_process'
import { createServer } from 'node:http'
import { copyFileSync, createReadStream, existsSync, mkdirSync, statSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

export const CAPTURE_DIR = dirname(fileURLToPath(import.meta.url))
export const LEARN_DIR = resolve(CAPTURE_DIR, '..')
export const ROOT = resolve(LEARN_DIR, '..')
export const VIEWPORT = { width: 1920, height: 1080 }
export const SCALE = 2

export const sleep = ms => new Promise(r => setTimeout(r, ms))

/** `--port 3402 --flag` → {port: '3402', flag: true} */
export function parseArgs(argv = process.argv.slice(2)) {
  const out = {}
  for (let i = 0; i < argv.length; i++) {
    if (!argv[i].startsWith('--')) continue
    const key = argv[i].slice(2)
    const next = argv[i + 1]
    if (next === undefined || next.startsWith('--')) out[key] = true
    else { out[key] = next; i++ }
  }
  return out
}

/** What demo-server.sh wrote: {url, port, user, password, home, db, fresh, pid}. */
export function readServer(port = 3401) {
  const file = `/tmp/eggify-learn-${port}/server.json`
  if (!existsSync(file)) {
    throw new Error(`No demo server on port ${port} (${file} is missing). Start one: learn/capture/demo-server.sh --port ${port}`)
  }
  return JSON.parse(readFileSync(file, 'utf8'))
}

export function shotsDir(slug) { return join(LEARN_DIR, 'public', 'episodes', slug, 'shots') }
export function clipsDir(slug) { return join(LEARN_DIR, 'public', 'episodes', slug, 'clips') }

// Things that would differ between two takes of the same screen.
const STEADY_CSS = `
  *, *::before, *::after { caret-color: transparent !important; }
  ::-webkit-scrollbar { display: none !important; }
  nuxt-devtools-frame, #nuxt-devtools-container, vite-error-overlay { display: none !important; }
`

/**
 * Launch Chromium and open a context sized for shots.
 * opts.reducedMotion (default true): the app then draws its loaders and working
 *   states still, so a shot never catches an animation half-way.
 * opts.storage: extra localStorage values, set before any page script runs.
 * opts.recordVideo: passed to Playwright (used by recordClip).
 */
export async function launch(opts = {}) {
  const browser = await chromium.launch({ headless: opts.headless !== false })
  const context = await browser.newContext({
    viewport: VIEWPORT,
    deviceScaleFactor: opts.scale ?? SCALE,
    colorScheme: 'light',
    locale: 'en-GB',
    timezoneId: 'Europe/London',
    reducedMotion: opts.reducedMotion === false ? 'no-preference' : 'reduce',
    ...(opts.recordVideo ? { recordVideo: opts.recordVideo } : {}),
  })
  const storage = {
    'eggify-theme': 'theatre-light',                 // the app default (Puppet Theatre Light), pinned
    'eggify-agent-onboarding-seen-v1': '1',
    'eggify-right-sidebar-collapsed': 'false',
    ...(opts.storage || {}),
  }
  await context.addInitScript(({ storage, css }) => {
    try {
      for (const [k, v] of Object.entries(storage)) if (localStorage.getItem(k) === null) localStorage.setItem(k, v)
    } catch { /* about:blank */ }
    const add = () => {
      if (document.getElementById('learn-capture-css')) return
      const s = document.createElement('style')
      s.id = 'learn-capture-css'
      s.textContent = css
      ;(document.head || document.documentElement).appendChild(s)
    }
    if (document.documentElement) add()
    document.addEventListener('DOMContentLoaded', add)
  }, { storage, css: STEADY_CSS })
  return { browser, context }
}

const cookieFile = server => `/tmp/eggify-learn-${server.port}/session-cookie`

/**
 * The demo user's session token. Logins are rate limited per address, so one login is
 * kept in /tmp/eggify-learn-<port>/session-cookie and reused until the server refuses it.
 */
export async function sessionToken(server) {
  const file = cookieFile(server)
  if (existsSync(file)) {
    const token = readFileSync(file, 'utf8').trim()
    const res = await fetch(`${server.url}/api/auth/status`, { headers: { Cookie: `session_token=${token}` } })
    if (res.ok && (await res.json()).authenticated) return token
  }
  const res = await fetch(`${server.url}/api/auth/login`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: server.user, password: server.password }),
  })
  if (!res.ok) throw new Error(`Sign-in failed: ${res.status} ${await res.text()}`)
  const token = (res.headers.getSetCookie?.() || []).map(c => c.split(';')[0]).find(c => c.startsWith('session_token='))?.slice('session_token='.length)
  if (!token) throw new Error('Sign-in returned no session cookie')
  rememberToken(server, token)
  return token
}
export function rememberToken(server, token) {
  writeFileSync(cookieFile(server), token + '\n', { mode: 0o600 })
}

/** Sign the context in as the demo user (sets the session cookie). */
export async function signIn(context, server) {
  const token = await sessionToken(server)
  await context.addCookies([{ name: 'session_token', value: token, url: server.url, httpOnly: true, sameSite: 'Lax' }])
}

/** After signing in through the UI: keep that session for later runs. */
export async function rememberSignIn(context, server) {
  const c = (await context.cookies(server.url)).find(c => c.name === 'session_token')
  if (c) rememberToken(server, c.value)
}

/**
 * Answer an API route with fixed JSON instead of asking the server. For fictional but
 * realistic demo data that the real UI then shows (e.g. /api/agent/runtimes, whose real
 * answer depends on the CLIs and logins of the machine that takes the shots).
 * `body` may be a function (request) => object. Say in the episode's notes what was stubbed.
 */
export async function stubJson(target, pattern, body, { status = 200 } = {}) {
  await target.route(pattern, route => route.fulfill({
    status, contentType: 'application/json',
    body: JSON.stringify(typeof body === 'function' ? body(route.request()) : body),
  }))
}

/**
 * The header's CPU/memory meter shows the capturing machine's real load and changes
 * colour with it; give it calm, fixed numbers (a 16 GB Mac at 12 % CPU).
 */
export async function steadyUsage(context) {
  await stubJson(context, '**/api/system/usage', () => ({
    time: new Date().toISOString(), cpu_percent: 12.4, mem_used_bytes: 9.6 * 2 ** 30, mem_total_bytes: 16 * 2 ** 30,
    cores: 8, eggify_cpu_percent: 0.4, eggify_rss_bytes: 61 * 2 ** 20, eggify_processes: 2, available: true,
  }))
}

/** launch + signIn + a page, in one call. opts.realUsage keeps the real CPU/memory meter. */
export async function openApp(server, opts = {}) {
  const { browser, context } = await launch(opts)
  if (!opts.realUsage) await steadyUsage(context)
  if (opts.signIn !== false) await signIn(context, server)
  const page = await context.newPage()
  page.on('pageerror', e => console.warn('  [page error]', e.message))
  return { browser, context, page }
}

/**
 * Wait until the page has stopped changing: network quiet (the app polls, so this
 * is best effort), web fonts loaded, images decoded, finite animations finished,
 * no loaders on screen, and two frames painted.
 */
export async function settle(page, { timeout = 15000, extra = 250 } = {}) {
  await page.waitForLoadState('networkidle', { timeout: 6000 }).catch(() => {})
  await page.evaluate(async () => {
    const cap = (p, ms) => Promise.race([p, new Promise(r => setTimeout(r, ms))])
    await cap(document.fonts.ready, 8000)
    // images on screen only: lazy ones further down never load
    const onScreen = i => { const r = i.getBoundingClientRect(); return r.bottom > 0 && r.top < innerHeight && r.width > 0 }
    await cap(Promise.all([...document.images].filter(i => !i.complete && onScreen(i)).map(i => new Promise(r => { i.onload = i.onerror = r }))), 8000)
  })
  await page.waitForFunction(() => {
    const visible = el => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0 }
    const loaders = [...document.querySelectorAll('.egg-loader, .rt-skeleton, .loading-spinner, .rt-spinner, [aria-busy="true"]')].filter(visible)
    if (loaders.length) return false
    return document.getAnimations().every(a => {
      const t = a.effect?.getComputedTiming?.()
      return a.playState !== 'running' || t?.iterations === Infinity
    })
  }, null, { timeout }).catch(() => console.warn('  [settle] still animating or loading after', timeout, 'ms'))
  await page.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))))
  if (extra) await sleep(extra)
}

/** goto + settle. `path` is relative to the server ("/agents"). */
export async function go(page, server, path, opts) {
  const bare = u => u.split('#')[0]
  const same = bare(page.url()) === bare(server.url + path)
  await page.goto(server.url + path, { waitUntil: 'domcontentloaded' })
  if (same) await page.reload({ waitUntil: 'domcontentloaded' })   // a hash-only change does not reload
  await settle(page, opts)
}

async function boxOf(page, target) {
  if (target && typeof target === 'object' && 'x' in target && 'w' in target) return target
  const loc = typeof target === 'string' ? page.locator(target).first() : target
  const b = await loc.boundingBox()
  if (!b) return null
  return { x: Math.round(b.x), y: Math.round(b.y), w: Math.round(b.width), h: Math.round(b.height) }
}

/**
 * Take a shot: <shots>/<name>.png (3840×2160) and <name>.json with the boxes.
 * boxes: {id: selector | Locator | {x, y, w, h}}. A box that is missing or outside
 * the viewport is an error, since the video would zoom to nothing.
 * opts.settle false skips the settle wait; opts.pad grows every box by n px.
 */
export async function shot(page, slug, name, opts = {}) {
  if (opts.settle !== false) await settle(page)
  const dir = shotsDir(slug)
  mkdirSync(dir, { recursive: true })
  const boxes = {}
  for (const [id, target] of Object.entries(opts.boxes || {})) {
    let b = await boxOf(page, target)
    if (!b) throw new Error(`shot ${name}: box "${id}" not found`)
    const pad = opts.pad || 0
    b = { x: b.x - pad, y: b.y - pad, w: b.w + 2 * pad, h: b.h + 2 * pad }
    if (b.x < 0 || b.y < 0 || b.x + b.w > VIEWPORT.width || b.y + b.h > VIEWPORT.height || b.w < 4 || b.h < 4) {
      throw new Error(`shot ${name}: box "${id}" is outside the viewport or empty: ${JSON.stringify(b)}`)
    }
    boxes[id] = b
  }
  const png = join(dir, `${name}.png`)
  await page.screenshot({ path: png, type: 'png', animations: 'disabled', caret: 'hide', scale: 'device' })
  const meta = await sharp(png).metadata()
  if (meta.width !== VIEWPORT.width * SCALE || meta.height !== VIEWPORT.height * SCALE) {
    throw new Error(`shot ${name}: ${meta.width}×${meta.height}, expected ${VIEWPORT.width * SCALE}×${VIEWPORT.height * SCALE}`)
  }
  writeFileSync(join(dir, `${name}.json`), JSON.stringify({ boxes }, null, 2) + '\n')
  console.log(`  shot ${slug}/${name}  [${Object.keys(boxes).join(', ')}]`)
  return { png, boxes }
}

/**
 * Proof sheet for one shot: the PNG at 1920×1080 with its boxes drawn and named,
 * written to learn/capture/.cache/boxes/<slug>/<name>.jpg (not committed). Look at it.
 */
export async function drawBoxes(slug, name) {
  const dir = shotsDir(slug)
  const { boxes } = JSON.parse(readFileSync(join(dir, `${name}.json`), 'utf8'))
  const colors = ['#e6194b', '#3cb44b', '#4363d8', '#f58231', '#911eb4', '#008080', '#9a6324', '#800000']
  const rects = Object.entries(boxes).map(([id, b], i) => {
    const c = colors[i % colors.length]
    const ty = b.y > 22 ? b.y - 6 : b.y + b.h + 16
    return `<rect x="${b.x}" y="${b.y}" width="${b.w}" height="${b.h}" fill="none" stroke="${c}" stroke-width="3"/>
      <text x="${b.x}" y="${ty}" font-family="Helvetica" font-size="16" font-weight="bold" fill="${c}" stroke="#fff" stroke-width="3" paint-order="stroke">${id}</text>`
  }).join('\n')
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${VIEWPORT.width}" height="${VIEWPORT.height}">${rects}</svg>`
  const out = join(CAPTURE_DIR, '.cache', 'boxes', slug)
  mkdirSync(out, { recursive: true })
  const file = join(out, `${name}.jpg`)
  await sharp(join(dir, `${name}.png`)).resize(VIEWPORT.width, VIEWPORT.height)
    .composite([{ input: Buffer.from(svg), left: 0, top: 0 }]).jpeg({ quality: 82 }).toFile(file)
  return file
}

/** drawBoxes for every shot of an episode. */
export async function drawAllBoxes(slug) {
  const names = readdirSync(shotsDir(slug)).filter(f => f.endsWith('.json')).map(f => f.slice(0, -5))
  const files = []
  for (const n of names) files.push(await drawBoxes(slug, n))
  return files
}

/**
 * Record a screen clip to <clips>/<name>.mp4 (1920×1080, 30 fps, H.264).
 * `run(page, {mark})` drives the page; call `mark()` when the part worth keeping
 * starts (everything before it, e.g. loading, is cut). Motion is left on by default.
 */
export async function recordClip(server, slug, name, run, opts = {}) {
  const tmp = join(CAPTURE_DIR, '.cache', 'video', `${slug}-${name}`)
  rmSync(tmp, { recursive: true, force: true })
  mkdirSync(tmp, { recursive: true })
  const { browser, context } = await launch({
    reducedMotion: false, ...opts, scale: 1,
    recordVideo: { dir: tmp, size: VIEWPORT },
  })
  if (!opts.realUsage) await steadyUsage(context)
  if (opts.signIn !== false) await signIn(context, server)
  const page = await context.newPage()
  const began = Date.now()
  let from = 0
  await run(page, { mark: () => { from = (Date.now() - began) / 1000 } })
  const video = page.video()
  await context.close()
  const webm = await video.path()
  await browser.close()
  mkdirSync(clipsDir(slug), { recursive: true })
  const out = join(clipsDir(slug), `${name}.mp4`)
  const r = spawnSync('ffmpeg', ['-y', '-loglevel', 'error', '-ss', from.toFixed(2), '-i', webm,
    '-vf', `scale=${VIEWPORT.width}:${VIEWPORT.height}:flags=lanczos,fps=30`, '-an',
    '-c:v', 'libx264', '-preset', 'slow', '-crf', '16', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', out], { stdio: 'inherit' })
  if (r.status !== 0) throw new Error(`ffmpeg failed for clip ${name}`)
  rmSync(tmp, { recursive: true, force: true })
  console.log(`  clip ${slug}/${name}.mp4 (cut the first ${from.toFixed(1)} s)`)
  return out
}

/**
 * Serve the landing page (../../landing) locally for the install shot. The site is
 * copied to learn/capture/.cache/landing and built there, so the landing folder
 * itself is never touched. Returns {url, stop}.
 */
export async function serveLanding(port = 4390) {
  const src = join(ROOT, 'landing')
  const work = join(CAPTURE_DIR, '.cache', 'landing')
  mkdirSync(work, { recursive: true })
  const sync = spawnSync('rsync', ['-a', '--delete', '--exclude', 'node_modules', '--exclude', 'dist', '--exclude', '.astro', `${src}/`, `${work}/`], { stdio: 'inherit' })
  if (sync.status !== 0) throw new Error('could not copy landing/')
  // src/data/release.ts reads ../../../internal/version/version.go; keep that path working in the copy
  mkdirSync(join(CAPTURE_DIR, '.cache', 'internal', 'version'), { recursive: true })
  copyFileSync(join(ROOT, 'internal', 'version', 'version.go'), join(CAPTURE_DIR, '.cache', 'internal', 'version', 'version.go'))
  const run = (cmd, args) => {
    const r = spawnSync(cmd, args, { cwd: work, stdio: ['ignore', 'ignore', 'inherit'], env: { ...process.env, ASTRO_TELEMETRY_DISABLED: '1' } })
    if (r.status !== 0) throw new Error(`${cmd} ${args.join(' ')} failed in ${work}`)
  }
  if (!existsSync(join(work, 'node_modules'))) run('npm', ['ci', '--silent'])
  run('npx', ['astro', 'build'])
  // Serve dist/ from this process (astro preview leaves a detached server behind)
  const dist = join(work, 'dist')
  const types = { html: 'text/html; charset=utf-8', css: 'text/css', js: 'text/javascript', mjs: 'text/javascript', json: 'application/json', svg: 'image/svg+xml', png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', webp: 'image/webp', avif: 'image/avif', ico: 'image/x-icon', woff2: 'font/woff2', woff: 'font/woff', mp4: 'video/mp4', webm: 'video/webm', mp3: 'audio/mpeg', txt: 'text/plain', xml: 'application/xml' }
  const httpServer = createServer((req, res) => {
    let file = resolve(dist, '.' + decodeURIComponent(new URL(req.url, 'http://x').pathname))
    if (!file.startsWith(dist)) { res.writeHead(403).end(); return }
    if (existsSync(file) && statSync(file).isDirectory()) file = join(file, 'index.html')
    if (!existsSync(file) && existsSync(file + '.html')) file += '.html'
    if (!existsSync(file)) { res.writeHead(404).end('not found'); return }
    res.writeHead(200, { 'Content-Type': types[file.split('.').pop().toLowerCase()] || 'application/octet-stream' })
    createReadStream(file).pipe(res)
  })
  await new Promise((ok, fail) => { httpServer.once('error', fail); httpServer.listen(port, '127.0.0.1', ok) })
  return { url: `http://127.0.0.1:${port}`, stop: () => { httpServer.closeAllConnections(); httpServer.close() } }
}

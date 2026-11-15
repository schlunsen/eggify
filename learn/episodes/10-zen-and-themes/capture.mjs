#!/usr/bin/env node
// Episode 9 "Zen and themes": takes the shots and clips named in episode.json.
//
//   learn/capture/library/server.sh --port 3409        # the library's demo world, in another terminal
//   node learn/episodes/10-zen-and-themes/capture.mjs  # [--port 3409] [--only chat,stage,picker,backdrops,menu,themes] [--clip stage|themes]
//
// Every PNG is taken through the real UI on the library's seeded world (selectors as in
// capture/library/areas/zen.mjs). Nothing is copied from the library. Stubbed (fictional data through
// the real UI; library/world.mjs openWorld): /api/agent/runtimes, usage-limit, context-usage,
// /api/system/usage (calm readings), subagents of the Postgres session. Backdrops are pinned through the
// picker's own localStorage setting. Sound is off; clips are silent (the video has its own music).
import { mkdirSync, rmSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { drawAllBoxes, parseArgs, readServer, shot, sleep, recordClip } from '../../capture/lib.mjs'
import { press } from '../../capture/library/lib.mjs'
import { openWorld, openSession, S, go, subagents } from '../../capture/library/world.mjs'

const LEARN = join(dirname(fileURLToPath(import.meta.url)), '..', '..')
const CAPTURE_DIR = join(LEARN, 'capture')
const SLUG = 'zen-and-themes'
const args = parseArgs()
const server = readServer(Number(args.port || 3409))
const only = typeof args.only === 'string' ? new Set(args.only.split(',')) : null
const want = name => !only || only.has(name)
const setStorage = (page, key, value) => page.evaluate(([k, v]) => localStorage.setItem(k, v), [key, value])
const setZen = (page, id, m) => page.evaluate(async ([sid, mode]) => {
  await fetch(`/api/agent/sessions/${sid}/view-mode`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ view_mode: mode }) })
}, [id, m])
const zenOpts = { settleOpts: { timeout: 2500 } }

if (args.clip) {
  const name = args.clip
  if (name === 'stage') {
    // The working Postgres session needs the library world (preload + stubbed subagents), so this
    // clip records the openWorld page itself (same recipe as recordClip: 1920x1080, 30 fps, silent).
    const tmp = join(CAPTURE_DIR, '.cache', 'video', `${SLUG}-stage`)
    rmSync(tmp, { recursive: true, force: true }); mkdirSync(tmp, { recursive: true })
    const w = await openWorld(server, { reducedMotion: false, scale: 1, recordVideo: { dir: tmp, size: { width: 1920, height: 1080 } } })
    const began = Date.now()
    const { page } = w
    await setZen(page, S.postgres, 'zen')
    await openSession(w, S.postgres)
    await page.waitForSelector('.zen-mode.zen-viz-circus', { timeout: 20000 })
    await sleep(1500)
    await page.locator('.zen-fullscreen-btn:not(.zen-fly-btn)').click()
    await sleep(5000)
    await page.mouse.move(1900, 1070)
    const from = (Date.now() - began) / 1000
    await sleep(8000)
    await setZen(page, S.postgres, 'live')
    const video = page.video()
    await w.context.close()
    const webm = await video.path()
    await w.browser.close()
    mkdirSync(join(LEARN, 'public', 'episodes', SLUG, 'clips'), { recursive: true })
    const out = join(LEARN, 'public', 'episodes', SLUG, 'clips', 'zen-motion.mp4')
    spawnSync('ffmpeg', ['-y', '-loglevel', 'error', '-ss', from.toFixed(2), '-i', webm, '-vf', 'scale=1920:1080:flags=lanczos,fps=30', '-an', '-c:v', 'libx264', '-preset', 'slow', '-crf', '16', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', out], { stdio: 'inherit' })
    rmSync(tmp, { recursive: true, force: true })
  }
  if (name === 'themes') {
    await recordClip(server, SLUG, 'theme-cycle', async (page, { mark }) => {
      await page.goto(`${server.url}/agents?session=${S.steer}`, { waitUntil: 'domcontentloaded' })
      await setZen(page, S.steer, 'live')
      await page.reload({ waitUntil: 'domcontentloaded' })
      await page.waitForSelector('.messages-container .message, .messages-container', { timeout: 20000 })
      await sleep(2500)
      await page.mouse.move(1000, 1070)
      await sleep(1500)
      mark()
      await sleep(600)
      for (let i = 0; i < 4; i++) { await press(page, 'Meta+Alt+BracketRight', 1500) }
      await sleep(500)
    })
  }
  process.exit(0)
}

const app = await openWorld(server)
const { browser, page } = app
const gone = () => page.mouse.move(5, 5)
const toZen = async () => {
  await page.locator('.view-mode-toggle button', { hasText: 'Zen' }).click()
  await page.waitForSelector('.zen-mode.zen-viz-circus', { timeout: 15000 })
  await page.waitForSelector('[data-testid=zen-latest-response]', { timeout: 15000 })
  await sleep(3500)
}
try {
  await setZen(page, S.steer, 'live')
  if (want('chat')) {
    await openSession(app, S.steer)
    await gone()
    await shot(page, SLUG, 'chat', { boxes: { toggle: '.view-mode-toggle', zen: page.locator('.view-mode-toggle button', { hasText: 'Zen' }) } })
  }
  if (want('stage') || want('picker')) {
    await openSession(app, S.steer)
    await toZen()
    await gone()
    if (want('stage')) await shot(page, SLUG, 'stage', {
      ...zenOpts,
      boxes: { stage: '.zen-mode', plate: '[data-testid=zen-plate]', reply: '[data-testid=zen-latest-response]', 'composer?': '.chat-input-wrapper', 'sound?': '[data-testid=zen-sound-toggle]', 'liveSwitch?': '.view-mode-toggle' },
    })
    if (want('picker')) {
      await page.locator('.zen-viz-active-btn').click()
      await page.waitForSelector('.zen-viz-option')
      await sleep(500)
      await shot(page, SLUG, 'picker', {
        ...zenOpts,
        boxes: { picker: '.zen-viz-picker', options: page.locator('.zen-viz-option').first().locator('xpath=..'), 'backdrop?': '[data-testid=zen-backdrop-cycle]' },
      })
      await page.keyboard.press('Escape')
    }
    await setZen(page, S.steer, 'live')
  }
  if (want('backdrops')) {
    await press(page, 'Meta+KeyB', 600)
    await press(page, 'Meta+Alt+KeyB', 900)
    for (const b of ['seaside', 'gothic', 'snow']) {
      await setStorage(page, 'eggify-zen-circus-backdrop', b)
      await setZen(page, S.steer, 'zen')
      await openSession(app, S.steer)
      await page.waitForSelector('.zen-mode.zen-viz-circus', { timeout: 15000 })
      if (!(await page.locator('.zen-mode.zen-fullscreen').count())) { await page.locator('.zen-fullscreen-btn:not(.zen-fly-btn)').click(); await sleep(1500) }
      await page.locator('[data-testid=zen-latest-response] .zen-card-close').click().catch(() => {})
      await sleep(3000)
      await gone()
      await shot(page, SLUG, `backdrop-${b}`, { ...zenOpts, boxes: { stage: '.zen-mode', plate: '[data-testid=zen-plate]' } })
    }
    await setStorage(page, 'eggify-zen-circus-backdrop', 'meadow')
    await setZen(page, S.steer, 'live')
    await page.locator('.zen-fullscreen-btn:not(.zen-fly-btn)').click().catch(() => {})
    await press(page, 'Meta+KeyB', 600)
    await press(page, 'Meta+Alt+KeyB', 900)
  }
  if (want('menu')) {
    await setStorage(page, 'eggify-theme', 'theatre-light')
    await openSession(app, S.steer)
    await page.locator('.edit-diff-message').first().evaluate(el => el.scrollIntoView({ block: 'center' }))
    await page.locator('.settings-button').click()
    await page.waitForSelector('.settings-theme-row')
    await sleep(400)
    await shot(page, SLUG, 'menu', { boxes: { themeRow: '.settings-theme-row', button: '.settings-button', 'shortcuts?': page.getByText('Keyboard Shortcuts').first() } })
    await page.locator('.settings-theme-row .theme-selector-button').click()
    await page.waitForSelector('.theme-option')
    await sleep(500)
    await shot(page, SLUG, 'picker-themes', { boxes: { picker: '.theme-dropdown', current: '.theme-option.theme-option-active', manage: page.getByText('Manage Themes').first() } })
    await page.keyboard.press('Escape')
  }
  if (want('themes')) {
    for (const t of ['theatre-dark', 'nord-dark', 'dracula-dark']) {
      await setStorage(page, 'eggify-theme', t)
      await openSession(app, S.steer)
      await page.waitForSelector('.edit-diff-message')
      await page.locator('.edit-diff-message').first().evaluate(el => el.scrollIntoView({ block: 'center' }))
      await sleep(800)
      await gone()
      await shot(page, SLUG, `theme-${t}`, { boxes: { sidebar: '.sessions-sidebar', chat: '.messages-container', rightSidebar: '.metrics-sidebar' } })
    }
    await setStorage(page, 'eggify-theme', 'theatre-light')
  }
} finally {
  await setZen(page, S.steer, 'live').catch(() => {})
  await browser.close()
}
await drawAllBoxes(SLUG)

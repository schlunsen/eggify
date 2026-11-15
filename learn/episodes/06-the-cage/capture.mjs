#!/usr/bin/env node
// Episode 6 "Keeping agents in the cage": takes the shots named in episode.json.
//
//   learn/capture/library/server.sh --port 3406        # the library's demo world, in another terminal
//   node learn/episodes/06-the-cage/capture.mjs        # [--port 3406] [--only create,settings,sandboxed,trust,memories,skills,yolo]
//
// Every PNG is taken by this script through the real UI on the library's seeded world
// (same selectors as capture/library/areas/cage.mjs). Nothing is copied from the library.
// Stubbed (fictional demo data through the real UI; library/world.mjs openWorld):
//   /api/agent/runtimes   Claude Code and Codex installed and signed in (Create Session needs a runtime)
//   /api/agent/usage-limit, context-usage, /api/system/usage   calm readings for the sidebar and header
//   WS session_updated    the team session's full options re-sent (the list leaves "sandboxed" out)
// The workspace-trust notice, its inventory, the pending skills and memories are the server's own
// answers about real files and rows in the demo home. Nothing is submitted or approved.
import { drawAllBoxes, parseArgs, readServer, shot, sleep } from '../../capture/lib.mjs'
import { openWorld, openSession, refreshSession, S, go } from '../../capture/library/world.mjs'
import { PROJECTS } from '../../capture/library/seed-extra.mjs'

const SLUG = 'the-cage'
const args = parseArgs()
const server = readServer(Number(args.port || 3406))
const only = typeof args.only === 'string' ? new Set(args.only.split(',')) : null
const want = name => !only || only.has(name)

const app = await openWorld(server)
const { browser, page } = app

async function openPermissions() {
  if (!(await page.locator('.yolo-mode-metric').isVisible().catch(() => false))) {
    await page.locator('button.row-header[aria-controls="permissions-row"]').click()
  }
  await page.waitForSelector('.yolo-mode-metric')
}
const gone = () => page.mouse.move(5, 5)

try {
  if (want('create')) {
    await go(page, server, `/agents?project=${PROJECTS.parrot.id}&new_session=1`)
    await page.waitForSelector('.settings-toggle', { timeout: 15000 })
    await page.locator('.settings-toggle').click()
    await sleep(500)
    const group = page.locator('.form-group', { has: page.locator('[data-testid=sandbox-toggle]') }).last()
    await group.evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(400)
    await gone()
    await shot(page, SLUG, 'create', {
      boxes: { dialog: '.modal-content', sandbox: group, checkbox: '[data-testid=sandbox-toggle]', 'permissionMode': page.locator('.form-group', { has: page.locator('#permission-mode') }).last() },
    })
    await page.keyboard.press('Escape')
  }

  if (want('settings')) {
    await go(page, server, '/settings#sandbox')
    await page.waitForSelector('#sandbox')
    await page.locator('#sandbox').evaluate(el => el.scrollIntoView({ block: 'start' }))
    await sleep(600)
    await gone()
    await shot(page, SLUG, 'settings', {
      boxes: { section: '#sandbox', defaultSwitch: 'input[aria-label="Sandbox new sessions"]', domains: 'textarea[aria-label="Allowed domains"]', save: page.locator('#sandbox button', { hasText: 'Save allowlist' }) },
    })
  }

  if (want('sandboxed')) {
    await openSession(app, S.team)
    await refreshSession(app, S.team)
    await openPermissions()
    await page.locator('[data-testid=sandbox-metric]').evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(500)
    await gone()
    await shot(page, SLUG, 'sandboxed', {
      boxes: { permissions: '#permissions-row', yolo: '.yolo-mode-metric', sandbox: '[data-testid=sandbox-metric]' },
    })
    await openSession(app, S.steer)
    await openPermissions()
    await page.locator('[data-testid=sandbox-metric]').evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(500)
    await gone()
    await shot(page, SLUG, 'not-sandboxed', {
      boxes: { permissions: '#permissions-row', yolo: '.yolo-mode-metric', sandbox: '[data-testid=sandbox-metric]' },
    })
  }

  if (want('trust')) {
    await openSession(app, S.trust)
    await page.waitForSelector('.workspace-trust-banner')
    await sleep(500)
    await gone()
    await shot(page, SLUG, 'trust', {
      boxes: { banner: '.workspace-trust-banner', review: '.workspace-trust-banner .trust-btn.secondary', trust: '.workspace-trust-banner .trust-btn.primary' },
    })
    await page.locator('.workspace-trust-banner .trust-btn.secondary').click()
    await page.waitForSelector('.trust-details')
    await sleep(500)
    await gone()
    await shot(page, SLUG, 'trust-review', {
      boxes: { banner: '.workspace-trust-banner', details: '.trust-details', trust: '.workspace-trust-banner .trust-btn.primary', hook: page.locator('.trust-details code, .trust-details pre').first() },
    })
  }

  if (want('memories')) {
    await go(page, server, `/projects/${PROJECTS.teashop.id}/memories`)
    await page.waitForSelector('.review-queue [data-test=rq-item]', { timeout: 15000 })
    await sleep(800)
    await gone()
    const item = page.locator('[data-test=rq-item]').first()
    await shot(page, SLUG, 'memories', {
      boxes: { queue: '.review-queue', item, session: page.locator('[data-test=rq-session]').first(), approve: page.locator('[data-test=rq-approve]').first(), reject: page.locator('[data-test=rq-reject]').first() },
    })
  }

  if (want('skills')) {
    await go(page, server, '/skills')
    await page.waitForSelector('.review-queue [data-test=rq-item]', { timeout: 15000 })
    await sleep(800)
    const risky = page.locator('[data-test=rq-item]', { hasText: 'deploy-to-counter' }).first()
    await risky.evaluate(el => el.scrollIntoView({ block: 'start' }))
    await sleep(500)
    await gone()
    await shot(page, SLUG, 'skill', {
      boxes: { item: risky, settings: risky.locator('[data-test=rq-settings]'), hook: risky.locator('[data-test=rq-hook-command]').first(), files: risky.locator('[data-test=rq-files]'), exec: risky.locator('[data-test=rq-file-exec]').first(), actions: risky.locator('.rq-actions'), approve: risky.locator('[data-test=rq-approve]'), reject: risky.locator('[data-test=rq-reject]') },
    })
  }

  if (want('yolo')) {
    await openSession(app, S.steer)
    await openPermissions()
    await page.locator('.yolo-mode-metric').evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(500)
    await gone()
    await shot(page, SLUG, 'yolo', {
      boxes: { permissions: '#permissions-row', yolo: '.yolo-mode-metric', header: 'button.row-header[aria-controls="permissions-row"]' },
    })
  }
} finally {
  await browser.close()
}
await drawAllBoxes(SLUG)

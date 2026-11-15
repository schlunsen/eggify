#!/usr/bin/env node
// Episode 4 "Subagents and All agents": takes the shots named in episode.json.
//
//   learn/capture/library/server.sh --port 3404          # in another terminal: the library's demo world
//   node learn/episodes/04-subagents/capture.mjs         # [--port 3404] [--only chat,zoom,view,idle,all,zen]
//
// The server is the screen library's (base seed + library/seed-extra.mjs); the session is "Move order
// storage to Postgres" (avatar name "Brew Benedict"), whose agent hands the job to subagents.
// Everything is shot through the real UI with real key presses and clicks.
//
// Stubbed / injected (fictional demo data shown through the real UI; library/world.mjs openWorld):
//   /api/agent/sessions/<id>/subagents, /api/agent/subagents, WS background_agents_list   the six
//                                  subagents of that session (three running, two done, one failed);
//                                  no agent runs on the demo server
//   /api/agent/sessions (+ WS sessions_list)   live-only fields patched in: which sessions are working,
//                                  subagent counts, loops and cron chips, "needs you" rows
//   WS session_updated (idle)      the "idle" shot: the session's turn ended, its subagents carry on
//   /api/agent/runtimes, /api/agent/usage-limit, .../context-usage, /api/system/usage   calm readings
// Keys: ⇧⌘A (Subagents view) is pressed as on the desktop. ⇧⌘S (All agents) is pressed as its
// browser-tab variant (⌥⇧⌘S); no shot is zoomed on a key label.
import { drawAllBoxes, parseArgs, readServer, shot, sleep } from '../../capture/lib.mjs'
import { press } from '../../capture/library/lib.mjs'
import { openWorld, openSession, S, settle } from '../../capture/library/world.mjs'

const SLUG = 'subagents'
const args = parseArgs()
const server = readServer(Number(args.port || 3404))
const only = typeof args.only === 'string' ? new Set(args.only.split(',')) : null
const want = name => !only || only.has(name)

const app = await openWorld(server, { storage: { 'eggify-zen-stage': 'circus' } })
const { browser, page, bridge } = app
const modes = page.locator('.sv-modes[aria-label="View"]')
const stages = page.locator('.sv-modes[aria-label="Zen stage"]')

try {
  await openSession(app, S.postgres)
  await page.waitForSelector('.subagents-pip .pip-row')
  await page.mouse.move(1000, 640)
  await sleep(600)

  // ---- 1. The card in the chat's corner
  if (want('chat')) {
    await shot(page, SLUG, 'chat', {
      boxes: {
        pip: '.subagents-pip', runningRow: page.locator('.pip-row').first(), secondRow: page.locator('.pip-row').nth(1),
        finishedRow: page.locator('.pip-row').nth(3), openView: 'button[aria-label="Open the subagents view"]',
      },
    })
  }

  // ---- 2. Zoom into one
  if (want('zoom')) {
    await page.locator('.pip-row').first().click()
    await page.waitForSelector('.szm')
    await sleep(1800)
    await page.mouse.move(1000, 900)
    await shot(page, SLUG, 'zoom', {
      boxes: {
        modal: '.szm', title: '#szm-title', next: '[data-testid=szm-next]', openView: '[data-testid=szm-open-view]',
        now: '.szm .sd-now', steps: '.szm .sd-activity',
      },
    })
    await page.locator('[data-testid=szm-close]').click()
    await sleep(500)
  }

  // ---- 3. The Subagents view (⇧⌘A)
  if (want('view')) {
    await press(page, 'Meta+Shift+KeyA', 900)
    await page.waitForSelector('.subagents-view')
    await modes.locator('button', { hasText: 'Activity' }).click().catch(() => {})
    await page.waitForSelector('.sv-row')
    await sleep(1000)
    await page.mouse.move(1000, 900)
    await shot(page, SLUG, 'view', {
      boxes: {
        view: '.subagents-view', filters: '.sv-filters', list: page.locator('.sv-row').first().locator('xpath=..'),
        runningRow: page.locator('.sv-row').first(), detail: '.subagents-view .sd', now: '.subagents-view .sd-now',
        task: { x: 424, y: 288, w: 1100, h: 50 }, steps: '.subagents-view .sd-activity', modes: modes,
        filterRunning: page.locator('.sv-filters button.sv-filter-running'), filterFailed: page.locator('.sv-filters button.sv-filter-failed'),
      },
    })
    await page.locator('.sv-row', { hasText: 'schema' }).click()
    await sleep(900)
    await page.mouse.move(1000, 900)
    await shot(page, SLUG, 'view-done', {
      boxes: {
        view: '.subagents-view', doneRow: page.locator('.sv-row', { hasText: 'schema' }), detail: '.subagents-view .sd',
        report: '.subagents-view .sd-report', produced: '.subagents-view .sd-products', steps: '.subagents-view .sd-activity',
      },
    })
    await page.locator('.sv-filters button.sv-filter-failed').click()
    await sleep(900)
    await page.mouse.move(1000, 900)
    await shot(page, SLUG, 'view-failed', {
      boxes: { view: '.subagents-view', filters: '.sv-filters', failedRow: page.locator('.sv-row').first(), detail: '.subagents-view .sd', 'error?': '.subagents-view .sd-problem' },
    })
    await page.locator('.sv-filters button.sv-filter-all').click()
    await sleep(400)
  }

  // ---- 4. Zen stage of the view
  if (want('zen')) {
    if (!(await page.locator('.subagents-view').count())) {
      await press(page, 'Meta+Shift+KeyA', 900)
      await page.waitForSelector('.subagents-view')
    }
    await modes.locator('button', { hasText: 'Zen' }).click()
    await sleep(4000)
    await page.mouse.move(1000, 1000)
    await shot(page, SLUG, 'zen-circus', {
      boxes: { view: '.subagents-view', modes, stageSwitch: stages, mainAgent: '.crate-label', bench: '.circus-bench span' },
    })
    await stages.locator('button', { hasText: 'Synthwave' }).click()
    await sleep(3500)
    await page.mouse.move(1000, 1000)
    await shot(page, SLUG, 'zen-synthwave', { boxes: { view: '.subagents-view', stageSwitch: stages } })
    await stages.locator('button', { hasText: 'Circus' }).click()
    await page.locator('button[aria-label="Close subagents view"]').click()
    await sleep(500)
  }

  // ---- 5. Idle, with subagents still working
  if (want('idle')) {
    if (await page.locator('.subagents-view').count()) { await page.locator('button[aria-label="Close subagents view"]').click(); await sleep(500) }
    bridge.inject({ type: 'session_updated', session_id: S.postgres, session: { id: S.postgres, status: 'idle', subagents_running: 3, subagents_total: 6 } })
    await sleep(1500)
    await page.mouse.move(1000, 640)
    await shot(page, SLUG, 'idle', {
      boxes: {
        sidebar: '.sessions-sidebar', sessionRow: '.session-item.active', pip: '.subagents-pip',
        'composerChip': '.subagents-chip',
      },
    })
  }

  // ---- 6. All agents (⇧⌘S; ⌥⇧⌘S in a browser tab)
  if (want('all')) {
    await openSession(app, S.postgres)
    await press(page, 'Meta+Alt+Shift+KeyS', 1500)
    await page.waitForSelector('.all-agents')
    await page.waitForSelector('.aa-row.aa-agent')
    await sleep(1000)
    await page.mouse.move(1000, 1000)
    const row = page.locator('.aa-row.aa-agent', { hasText: 'Postgres order store' }).first()
    await shot(page, SLUG, 'all-agents', {
      boxes: {
        overlay: '.all-agents', counts: '.aa-stats', filters: '.aa-filters', search: '.aa-search', tree: '.aa-tree',
        needsYou: page.locator('.aa-row.aa-session').first(), loop: page.locator('.aa-row.aa-loop', { hasText: 'Plank prices' }),
        postgresSession: page.locator('.aa-row.aa-session', { hasText: 'Brew Benedict' }), agent: row,
        filterAll: page.locator('.aa-filters button.aa-filter-all'), filterFailed: page.locator('.aa-filters button.aa-filter-failed'),
        'detail?': '.aa-detail',
      },
    })
    await row.click()
    await sleep(1000)
    await page.mouse.move(1000, 1000)
    await shot(page, SLUG, 'all-agents-detail', {
      boxes: { overlay: '.all-agents', tree: '.aa-tree', selected: row, detail: '.aa-detail', 'now?': '.aa-detail .sd-now', 'steps?': '.aa-detail .sd-activity' },
    })
    const loop = page.locator('.aa-row.aa-loop', { hasText: 'Plank prices' })
    await loop.click()
    await sleep(800)
    await page.mouse.move(1000, 1000)
    await shot(page, SLUG, 'all-agents-loop', { boxes: { overlay: '.all-agents', tree: '.aa-tree', selected: loop, detail: '.aa-detail' } })
    await page.locator('.aa-filters button.aa-filter-failed').click()
    await sleep(1200)
    await page.mouse.move(1000, 1000)
    await shot(page, SLUG, 'all-agents-failed', {
      boxes: { overlay: '.all-agents', filters: '.aa-filters', tree: '.aa-tree', firstRow: page.locator('.aa-tree .aa-row').first() },
    })
  }
} finally {
  await browser.close()
}
const proofs = await drawAllBoxes(SLUG)
console.log(`\nProof sheets with the boxes drawn (look at them):\n  ${proofs.join('\n  ')}`)

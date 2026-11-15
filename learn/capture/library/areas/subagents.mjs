// Episode 4: subagents (PiP card, zoom modal, Subagents view and its Zen stage, All agents).
import { libShot, press } from '../lib.mjs'
import { openWorld, openSession, S, sleep } from '../world.mjs'

const HOW = 'seeded session; its subagents are a stubbed API answer (/subagents) with times near now'

export default async function (server) {
  const app = await openWorld(server, { storage: { 'eggify-zen-stage': 'circus' } })
  const { browser, page } = app
  try {
    await openSession(app, S.postgres)
    await page.waitForSelector('.subagents-pip .pip-row')
    await sleep(500)
    await libShot(page, 'subagents', 'pip-card', {
      shows: 'A working session\'s chat with the Subagents card (PiP) in the top-right corner: three running subagents with their Now line and model chip, finished ones below; Agent tool cards in the chat; "subagents" counts in the right sidebar',
      how: HOW,
      boxes: {
        pip: '.subagents-pip', runningRow: page.locator('.pip-row').first(), finishedRow: page.locator('.pip-row').nth(3), openView: 'button[aria-label="Open the subagents view"]',
        'agentCards?': page.locator('.tool-use', { hasText: 'Agent' }).first(), 'sidebarSubagents?': page.locator('.metrics-sidebar button', { hasText: 'Subagents' }).first(),
        'sessionRow?': page.locator('.session-item.active, .session-item').first(),
      },
    })

    // zoom modal on a running subagent
    await page.locator('.pip-row').first().click()
    await page.waitForSelector('.szm')
    await sleep(900)
    await libShot(page, 'subagents', 'zoom-modal-running', {
      shows: 'Subagent zoom modal on a running subagent: Now box with the running command and folder, Task, key steps; ‹ 1 of 6 › to step through, Open in Subagents view',
      how: HOW, settleOpts: { timeout: 2500 },
      boxes: { modal: '.szm', title: '#szm-title', next: '[data-testid=szm-next]', openView: '[data-testid=szm-open-view]', 'now?': '.szm .sd-now', 'steps?': '.szm .sd-activity' },
    })
    await page.locator('[data-testid=szm-close]').click()
    await sleep(500)
    await page.locator('.pip-row').nth(3).click()
    await page.waitForSelector('.szm')
    await sleep(900)
    await libShot(page, 'subagents', 'zoom-modal-finished', {
      shows: 'Subagent zoom modal on a finished subagent: its report, what it produced (pull request, commit, pushed branch, changed files) and key steps',
      how: HOW, settleOpts: { timeout: 2500 },
      boxes: { modal: '.szm', 'report?': '.szm .sd-report', 'produced?': '.szm .sd-products', 'steps?': '.szm .sd-activity' },
    })
    await page.locator('[data-testid=szm-close]').click()
    await sleep(400)

    // Subagents view: Activity
    await press(page, 'Meta+Shift+KeyA', 900)
    await page.waitForSelector('.subagents-view')
    await page.locator('.sv-modes[aria-label="View"] button', { hasText: 'Activity' }).click().catch(() => {})
    await page.waitForSelector('.sv-row')
    await sleep(700)
    await libShot(page, 'subagents', 'view-activity-running', {
      shows: 'Subagents view, Activity: list of every subagent (running first, filter chips with counts) and the selected running one briefed on the right: Now, Task, Key steps',
      how: HOW, settleOpts: { timeout: 2500 },
      boxes: {
        view: '.subagents-view', filters: '.sv-filters', list: page.locator('.sv-row').first().locator('xpath=..'), selectedRow: page.locator('.sv-row').first(),
        detail: '.subagents-view .sd', 'now?': '.subagents-view .sd-now', 'steps?': '.subagents-view .sd-activity', modes: '.sv-modes[aria-label="View"]',
      },
    })
    await page.locator('.sv-row', { hasText: 'schema' }).click()
    await sleep(700)
    await libShot(page, 'subagents', 'view-activity-finished', {
      shows: 'Subagents view with a finished subagent selected: report gist, Task, Produced (PR #41, commit, pushed branch, changed files), Key steps newest first',
      how: HOW, settleOpts: { timeout: 2500 },
      boxes: { view: '.subagents-view', selectedRow: page.locator('.sv-row', { hasText: 'schema' }), detail: '.subagents-view .sd', 'report?': '.subagents-view .sd-report', 'produced?': '.subagents-view .sd-products', 'steps?': '.subagents-view .sd-activity' },
    })
    await page.locator('.subagents-view .sd-chip', { hasText: 'All steps' }).click().catch(() => {})
    await sleep(500)
    await page.locator('.subagents-view .sd-activity').evaluate(el => el.scrollIntoView({ block: 'start' })).catch(() => {})
    await sleep(300)
    await libShot(page, 'subagents', 'view-all-steps', {
      shows: 'The same subagent with All steps: every tool call with its time and duration, the agent\'s notes in between',
      how: HOW, settleOpts: { timeout: 2500 },
      boxes: { view: '.subagents-view', 'steps?': '.subagents-view .sd-activity', 'chips?': page.locator('.subagents-view .sd-chip').first().locator('xpath=..') },
    })
    await page.locator('.sv-filters button.sv-filter-failed').click().catch(() => {})
    await sleep(600)
    await libShot(page, 'subagents', 'view-failed', {
      shows: 'Subagents view filtered to Failed: the failed subagent with its error in plain words', how: HOW, settleOpts: { timeout: 2500 },
      boxes: { view: '.subagents-view', filters: '.sv-filters', 'detail?': '.subagents-view .sd' },
    })
    await page.locator('.sv-filters button.sv-filter-all').click().catch(() => {})

    // Subagents view: Zen stage (Circus)
    await page.locator('.sv-modes[aria-label="View"] button', { hasText: 'Zen' }).click()
    await sleep(3500)
    await libShot(page, 'subagents', 'view-zen-circus', {
      shows: 'Subagents view, Zen (Circus stage): the main agent as the egg gentleman on a soapbox, each subagent an engraved costume egg with a speech bubble of its latest action; finished ones on the bench',
      how: HOW + '; motion is reduced for the still (a clip would show them wandering)', settleOpts: { timeout: 2500 },
      boxes: { view: '.subagents-view', modes: '.sv-modes[aria-label="View"]', 'stageSwitch?': '.sv-modes[aria-label="Zen stage"]' },
    })
    await page.locator('.sv-modes[aria-label="Zen stage"] button', { hasText: 'Synthwave' }).click().catch(() => {})
    await sleep(3000)
    await libShot(page, 'subagents', 'view-zen-synthwave', {
      shows: 'The same Zen view on the Synthwave stage: neon skyline, the main agent as the DJ, subagents as characters', how: HOW, settleOpts: { timeout: 2500 },
      boxes: { view: '.subagents-view', 'stageSwitch?': '.sv-modes[aria-label="Zen stage"]' },
    })
    await page.locator('.sv-modes[aria-label="Zen stage"] button', { hasText: 'Circus' }).click().catch(() => {})
    await page.locator('button[aria-label="Close subagents view"]').click()
    await sleep(500)

    // All agents overlay
    await press(page, 'Meta+Alt+Shift+KeyS', 1500)
    await page.waitForSelector('.all-agents')
    await page.waitForSelector('.aa-row.aa-agent')
    await sleep(800)
    await libShot(page, 'subagents', 'all-agents-running', {
      shows: 'All agents overlay (⇧⌘S), Running filter: every working session with its running subagents and loops in a tree (Needs you / Working / Recent), header counts',
      how: 'seeded sessions; subagents stubbed API; loops and waiting states patched into the session list', settleOpts: { timeout: 2500 },
      boxes: { overlay: '.all-agents', filters: '.aa-filters', search: '.aa-search', tree: '.aa-tree', 'detail?': '.aa-detail', firstSession: page.locator('.aa-row.aa-session').first(), firstAgent: page.locator('.aa-row.aa-agent').first(), 'loop?': page.locator('.aa-row.aa-loop').first() },
    })
    await page.locator('.aa-row.aa-agent', { hasText: 'Postgres order store' }).first().click()
    await sleep(900)
    await libShot(page, 'subagents', 'all-agents-subagent', {
      shows: 'All agents with a running subagent selected: its live detail (Now, Task, key steps) on the right, nested subagent below it in the tree',
      how: 'seeded sessions; subagents stubbed API', settleOpts: { timeout: 2500 },
      boxes: { overlay: '.all-agents', tree: '.aa-tree', selected: page.locator('.aa-row.aa-agent', { hasText: 'Postgres order store' }).first(), detail: '.aa-detail' },
    })
    await page.locator('.aa-filters button.aa-filter-all').click()
    await sleep(1200)
    await libShot(page, 'subagents', 'all-agents-all', {
      shows: 'All agents, All filter: finished and failed subagents too, recent idle sessions', how: 'seeded sessions; subagents stubbed API', settleOpts: { timeout: 2500 },
      boxes: { overlay: '.all-agents', filters: '.aa-filters', tree: '.aa-tree' },
    })
    const loop = page.locator('.aa-row.aa-loop').first()
    if (await loop.count()) {
      await loop.click()
      await sleep(700)
      await libShot(page, 'subagents', 'all-agents-loop', {
        shows: 'All agents with a loop selected: its schedule or goal, check command, iteration and last output, Open session', how: 'loop state patched into the session list (in-memory on a real install)', settleOpts: { timeout: 2500 },
        boxes: { overlay: '.all-agents', selected: loop, detail: '.aa-detail' },
      })
    }
  } finally { await browser.close() }
}

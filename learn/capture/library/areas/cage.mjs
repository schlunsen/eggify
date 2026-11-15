// Episode 6: the cage (sandbox, workspace trust, review queue, YOLO).
import { libShot } from '../lib.mjs'
import { openWorld, openSession, refreshSession, S, go, sleep } from '../world.mjs'
import { PROJECTS } from '../seed-extra.mjs'

async function openPermissions(page) {
  // the row remembers whether it was open
  if (!(await page.locator('.yolo-mode-metric').isVisible().catch(() => false))) {
    await page.locator('button.row-header[aria-controls="permissions-row"]').click()
  }
  await page.waitForSelector('.yolo-mode-metric')
}

const settingsSection = async (page, server, id) => {
  await go(page, server, `/settings#${id}`)
  await page.waitForSelector(`#${id}`)
  await page.locator(`#${id}`).evaluate(el => el.scrollIntoView({ block: 'start' }))
  await sleep(600)
}

export default async function (server) {
  const app = await openWorld(server)
  const { browser, page, bridge } = app
  try {
    // --- Create Session: the sandbox switch and the permission mode
    await go(page, server, `/agents?project=${PROJECTS.parrot.id}&new_session=1`)
    await page.waitForSelector('.settings-toggle', { timeout: 15000 })
    await page.locator('.settings-toggle').click()
    await sleep(500)
    const sandboxGroup = page.locator('.form-group', { has: page.locator('[data-testid=sandbox-toggle]') }).last()
    await sandboxGroup.evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(400)
    await libShot(page, 'cage', 'create-session-sandbox', {
      shows: 'Create Session → Session settings: the Filesystem sandbox switch (on by default) with what it does, and the permission mode above it',
      how: 'seeded; runtimes stubbed as ready (not submitted)',
      boxes: { dialog: '.modal-content', sandbox: sandboxGroup, 'permissionMode?': page.locator('.form-group', { has: page.locator('#permission-mode') }).last() },
    })
    await page.keyboard.press('Escape')

    // --- Settings → Sandbox
    await settingsSection(page, server, 'sandbox')
    await libShot(page, 'cage', 'settings-sandbox', {
      shows: 'Settings → Sandbox: "Sandbox new sessions" default and the list of domains sandboxed commands may reach',
      how: 'seeded (the server\'s own defaults)',
      boxes: { section: '#sandbox', defaultSwitch: 'input[aria-label="Sandbox new sessions"]', domains: 'textarea[aria-label="Allowed domains"]', 'save?': page.locator('#sandbox button', { hasText: 'Save allowlist' }) },
      pad: 6,
    })

    // --- Workspace trust: a folder with its own Claude config
    await openSession(app, S.trust)
    await page.waitForSelector('.workspace-trust-banner')
    await sleep(500)
    await libShot(page, 'cage', 'workspace-trust-banner', {
      shows: 'The workspace-trust notice in a chat: the folder has its own Claude config (hook, allowed tools, MCP server, env) which is not loaded until you trust it',
      how: 'seeded (real .claude/settings.json, hook script and .mcp.json in the demo repository; the server\'s own answer)',
      boxes: { banner: '.workspace-trust-banner', review: '.workspace-trust-banner .trust-btn.secondary', trust: '.workspace-trust-banner .trust-btn.primary' },
    })
    await page.locator('.workspace-trust-banner .trust-btn.secondary').click()
    await page.waitForSelector('.trust-details')
    await sleep(500)
    await libShot(page, 'cage', 'workspace-trust-review', {
      shows: 'The notice expanded with Review: every hook command, allowed tool, MCP server, env value and script the folder would load, and Trust folder',
      how: 'seeded (the server\'s own inventory of the demo repository)',
      boxes: { banner: '.workspace-trust-banner', details: '.trust-details', trust: '.workspace-trust-banner .trust-btn.primary' },
    })

    // --- Review queue: skills found on disk
    await go(page, server, '/skills')
    await page.waitForSelector('.review-queue [data-test=rq-item]', { timeout: 15000 })
    await sleep(800)
    await page.locator('.review-queue').evaluate(el => el.scrollIntoView({ block: 'start' }))
    await sleep(400)
    const risky = page.locator('[data-test=rq-item]', { hasText: 'deploy-to-counter' }).first()
    await libShot(page, 'cage', 'review-queue-skills', {
      shows: 'Skills page, review queue: skills found on disk wait for approval; each shows where it came from, its files and its settings',
      how: 'seeded (real SKILL.md folders in the demo home, found by the server\'s discovery)',
      boxes: { queue: '.review-queue', 'count?': '[data-test=rq-count]', item: risky, 'approve?': risky.locator('[data-test=rq-approve]'), 'reject?': risky.locator('[data-test=rq-reject]') },
    })
    await risky.evaluate(el => el.scrollIntoView({ block: 'start' }))
    await sleep(400)
    await libShot(page, 'cage', 'review-queue-skill-detail', {
      shows: 'A pending skill that asks for a lot: its hook command and allowed tools flagged as risky settings, the folder\'s files with the executable script marked, Approve / Edit + Approve / Reject',
      how: 'seeded (real SKILL.md folder with a hook, allowed-tools and an executable script)',
      boxes: { item: risky, 'settings?': risky.locator('[data-test=rq-settings]'), 'hook?': risky.locator('[data-test=rq-hook-command]').first(), 'files?': risky.locator('[data-test=rq-files]'), 'exec?': risky.locator('[data-test=rq-file-exec]').first(), 'actions?': risky.locator('.rq-actions') },
    })

    // --- Review queue: memories an agent stored
    await go(page, server, `/projects/${PROJECTS.teashop.id}/memories`)
    await page.waitForSelector('.review-queue [data-test=rq-item]', { timeout: 15000 })
    await sleep(800)
    await libShot(page, 'cage', 'review-queue-memories', {
      shows: 'A project\'s memories page: two memories an agent stored wait for review (with the session that wrote them), above the approved ones',
      how: 'seeded (memory rows written with status pending / approved)',
      boxes: { queue: '.review-queue', item: page.locator('[data-test=rq-item]').first(), 'session?': page.locator('[data-test=rq-session]').first(), 'approve?': page.locator('[data-test=rq-approve]').first() },
    })

    // --- YOLO and the sandbox state in the right sidebar
    await openSession(app, S.steer)
    await openPermissions(page)
    await page.locator('.yolo-mode-metric').evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(500)
    await libShot(page, 'cage', 'yolo-toggle', {
      shows: 'The right sidebar\'s Permissions row opened: the YOLO Mode switch (off), always-allow rules, the session\'s sandbox state',
      how: 'seeded',
      boxes: { permissions: '#permissions-row', yolo: '.yolo-mode-metric', 'sandbox?': '[data-testid=sandbox-metric]', header: 'button.row-header[aria-controls="permissions-row"]' },
    })
    await openSession(app, S.team)
    await refreshSession(app, S.team)
    await openPermissions(page)
    await page.locator('[data-testid=sandbox-metric]').evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(500)
    await libShot(page, 'cage', 'sidebar-sandboxed', {
      shows: 'The Permissions row of a sandboxed session (the team orchestrator): YOLO off, "Sandboxed" state',
      how: 'seeded; the session\'s full options re-sent as a session_updated event (the list leaves "sandboxed" out)',
      boxes: { permissions: '#permissions-row', yolo: '.yolo-mode-metric', sandbox: '[data-testid=sandbox-metric]' },
    })
  } finally { await browser.close() }
}

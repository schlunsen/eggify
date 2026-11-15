#!/usr/bin/env node
// Episode 2 "Projects, sessions and tags": takes the shots named in episode.json.
//
//   learn/capture/library/server.sh --port 3402          # in another terminal: the library's demo world
//   node learn/episodes/02-projects-sessions-tags/capture.mjs    # [--port 3402] [--only projects,create,sidebar,tag,jump,picker,overview]
//
// The server is the screen library's (six fictional projects, 21 sessions, tags, digests: the base
// seed plus library/seed-extra.mjs), so the screens match learn/public/library/shots/projects.
// Everything is shot through the real UI with real key presses and clicks; the tag added in the
// "tag" step is really saved on the demo server (PUT /api/agent/sessions/:id/tags).
//
// Stubbed / injected (fictional demo data shown through the real UI; library/world.mjs openWorld):
//   /api/agent/runtimes            Claude Code and Codex installed and signed in (Create Session's model list)
//   /api/agent/usage-limit         a calm usage reading (the right sidebar's "Session limit" pill)
//   /api/agent/sessions/*/context-usage   context percentages
//   /api/agent/sessions (+ WS sessions_list)   live-only fields patched in: which sessions are working,
//                                  subagent counts, the loop and cron chips (no agent runs on the demo server)
//   /api/agent/sessions/*/subagents, /api/agent/subagents   the subagents of "Move orders to Postgres"
//   /api/system/usage              the header's CPU/memory meter (lib.mjs steadyUsage)
//   WS permission_request          one injected event (below): the "Needs you" row of the sidebar
// Browser tab keys: ⌘S, ⌘1 and ⇧⌘P are pressed here as their browser variants (with ⌥); no shot
// shows a key label for them except the picker's footer (New project ⌥⇧⌘N), which no beat zooms on.
import { drawAllBoxes, parseArgs, readServer, shot, sleep } from '../../capture/lib.mjs'
import { press } from '../../capture/library/lib.mjs'
import { openWorld, openSession, S, go, settle } from '../../capture/library/world.mjs'
import { PROJECTS } from '../../capture/library/seed-extra.mjs'

const SLUG = 'projects-sessions-tags'
const args = parseArgs()
const server = readServer(Number(args.port || 3402))
const only = typeof args.only === 'string' ? new Set(args.only.split(',')) : null
const want = name => !only || only.has(name)
const TAG = 'loyalty'

// A session of teashop-api that waits for an approval: what the server sends when an agent asks.
const permissionRequest = () => ({
  type: 'permission_request', session_id: S.loginBug, permission_id: 'perm-demo-login-1', tool: 'Bash', action: 'execute',
  description: 'Run: git push -u origin fix/empty-password', details: { command: 'git push -u origin fix/empty-password', description: 'Push the branch' },
})

async function closeToasts(page) {
  for (let i = 0; i < 4; i++) {
    const x = page.locator('.needs-you-toast button').last()
    if (!(await x.count())) break
    await x.click().catch(() => {})
    await sleep(200)
  }
}

const app = await openWorld(server)
const { browser, page, bridge } = app
const tea = page.locator('.pp-row', { hasText: 'teashop-api' })
/** A locator's box cut to the viewport (a board column runs below the fold). */
async function clipped(loc) {
  const b = await loc.boundingBox()
  const x = Math.max(0, Math.round(b.x)), y = Math.max(0, Math.round(b.y))
  return { x, y, w: Math.min(1920, Math.round(b.x + b.width)) - x, h: Math.min(1080, Math.round(b.y + b.height)) - y }
}
const row = name => page.locator('.session-item', { hasText: name })

/** The agents page on the "Loyalty stamps" session of teashop-api, Earlier open, one row waiting for you. */
async function openTeashop() {
  await page.evaluate(() => {
    localStorage.setItem('eggify-sidebar-collapsed-groups', JSON.stringify({ today: false, earlier: false }))
    localStorage.removeItem('eggify-right-sidebar-collapsed')
  })
  await openSession(app, S.steer)
  bridge.inject(permissionRequest())
  await sleep(1200)
  await closeToasts(page)
  await page.mouse.move(1200, 600)
  await sleep(300)
}

try {
  // The session that gets tagged starts with one tag ("orders"; the seed gives it "feature" too, and
  // three chips are cut short in the sidebar row). Set through the app's own API; restored at the end.
  await page.goto(`${server.url}/settings`, { waitUntil: 'domcontentloaded' })
  await page.evaluate(async ({ id }) => {
    await fetch(`/api/agent/sessions/${id}/tags`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ tags: ['orders'] }) })
    localStorage.removeItem('cct-session-tags-pending')
  }, { id: S.steer })

  // ---- 1. Projects page
  if (want('projects') || want('create')) {
    await go(page, server, '/projects')
    await page.waitForSelector('.pp-row:not(.pp-row--skeleton) .pp-meta--branch')
    await page.mouse.move(1700, 900)
    if (want('projects')) await shot(page, SLUG, 'projects', {
      boxes: {
        heading: '.pp-header', newProject: '.pp-new', list: '.pp-list',
        teashop: tea, name: tea.locator('.pp-row__name'), path: tea.locator('.pp-row__path'),
        branch: tea.locator('.pp-meta--branch'), dirty: tea.locator('.pp-dirty'), running: tea.locator('.pp-meta--running'),
        meta: tea.locator('.pp-row__meta'), startSession: tea.locator('[data-action=start]'),
      },
    })
  }

  // ---- 2. Start session on the project's row → Create Session, folder filled in
  if (want('create')) {
    await tea.locator('[data-action=start]').click()
    const modal = page.locator('.modal-content').filter({ has: page.locator('#new-session-title') })
    await modal.waitFor({ timeout: 15000 })
    await sleep(900)
    await page.mouse.move(1750, 950)
    await shot(page, SLUG, 'create-session', {
      boxes: {
        dialog: modal, folder: modal.locator('.form-group').first(), createButton: '.btn-create',
      },
    })
    await page.keyboard.press('Escape')
  }

  // ---- 3. The sidebar of one project: Needs you / Working / Today / Earlier
  if (want('sidebar') || want('tag') || want('jump') || want('picker')) {
    await openTeashop()
    if (want('sidebar')) await shot(page, SLUG, 'sidebar', {
      boxes: {
        sidebar: '.sessions-sidebar', newSession: '.btn-new-session',
        needsYou: '.session-group.group-needs', working: '.session-group.group-working',
        today: '.session-group.group-today', earlier: '.session-group.group-earlier',
        waitingRow: page.locator('.group-needs .session-item').first(), openRow: '.session-item.active',
        tags: page.locator('.session-item.active [data-testid=session-tags]'),
      },
    })
  }

  // ---- 4. Tag the open session (⌘S; ⌥⌘S in a browser tab)
  if (want('tag')) {
    await press(page, 'Meta+Alt+KeyS', 600)
    await page.waitForSelector('.tag-pop')
    await page.locator('.tag-pop__input').pressSequentially(TAG, { delay: 40 })
    await sleep(400)
    await shot(page, SLUG, 'tag-typing', {
      boxes: { popover: '.tag-pop', field: '.tag-pop__field', input: '.tag-pop__input', hint: '.tag-pop__hint' },
    })
    await page.keyboard.press('Enter')
    await sleep(700)
    await shot(page, SLUG, 'tag-added', {
      boxes: { popover: '.tag-pop', field: '.tag-pop__field', newChip: page.locator('.tag-pop__chip', { hasText: TAG }) },
    })
    await page.keyboard.press('Escape')
    await sleep(600)
    await page.mouse.move(1200, 600)
    await shot(page, SLUG, 'tagged', {
      boxes: {
        sidebar: '.sessions-sidebar', openRow: '.session-item.active',
        tags: page.locator('.session-item.active [data-testid=session-tags]'),
        newChip: page.locator('.session-item.active [data-testid=session-tags] > *', { hasText: TAG }).first(),
        workingTags: page.locator('.group-working [data-testid=session-tags]').first(),
      },
    })
  }

  // ---- 5. Jump to the first session in the list (⌘1; ⌥⌘1 in a browser tab): the one that waits
  if (want('jump')) {
    const before = await page.locator('.session-item.active').innerText()
    await press(page, 'Meta+Alt+Digit1', 1500)
    let after = await page.locator('.session-item.active').innerText()
    if (after === before) throw new Error('⌘1 did not move the selection')
    await settle(page, { timeout: 4000 })
    await sleep(800)
    await closeToasts(page)
    await page.mouse.move(1200, 640)
    await shot(page, SLUG, 'jump', {
      boxes: { sidebar: '.sessions-sidebar', openRow: '.session-item.active', needsYou: '.session-group.group-needs' },
    })
  }

  // ---- 6. Switch project (⇧⌘P; ⌥⇧⌘P in a browser tab), then pick parrot-web
  if (want('picker')) {
    await press(page, 'Meta+Alt+Shift+KeyP', 900)
    await page.waitForSelector('.ps')
    await page.waitForSelector('.ps .ps-branch, .ps .ps-chip-text').catch(() => {})
    await sleep(700)
    const parrot = page.locator('.ps .project-item', { hasText: 'parrot-web' })
    await shot(page, SLUG, 'project-picker', {
      boxes: { picker: '.ps', search: '.ps-search', list: '.ps-list', current: page.locator('.ps .project-item', { hasText: 'teashop-api' }), parrot },
    })
    await parrot.click()
    await sleep(1500)
    // the page opens the project's first session (one seeded as working, with nothing to show); open a finished one
    const since = Date.now()
    await row('Ovo the Calm').click()
    await bridge.waitFor('messages_loaded', { since, timeout: 12000 })
    await settle(page, { timeout: 4000 })
    await sleep(600)
    await closeToasts(page)
    await page.mouse.move(1200, 640)
    await shot(page, SLUG, 'project-switched', {
      boxes: { sidebar: '.sessions-sidebar', openRow: '.session-item.active', projectButton: page.locator('header button, .app-header button, nav button', { hasText: 'parrot-web' }).first() },
    })
  }

  // ---- 7. Agent overview (⌘O): every project, then one tag
  if (want('overview')) {
    await page.evaluate(() => { for (const k of Object.keys(localStorage)) if (/project/i.test(k) && !/collapsed/.test(k)) console.log('LS', k, localStorage.getItem(k)?.slice(0, 80)) })
    await openTeashop()
    await press(page, 'Meta+KeyO', 1500)
    await page.waitForSelector('.agent-overview')
    await sleep(2500)
    await page.mouse.move(1500, 1000)
    await shot(page, SLUG, 'overview', {
      boxes: {
        header: '.agent-overview h1, .agent-overview .overview-title', filters: '.overview-filters', tagBar: '.tag-bar',
        ordersTag: page.locator('.tag-bar .tag-chip', { hasText: '#orders' }),
        firstColumn: await clipped(page.locator('.simple-column').first()), firstCard: page.locator('.simple-card').first(),
        secondColumn: await clipped(page.locator('.simple-column').nth(1)), board: await clipped(page.locator('.simple-board')),
      },
    })
    await page.locator('.tag-bar .tag-chip', { hasText: '#orders' }).click()
    await sleep(1200)
    await page.mouse.move(1500, 1000)
    await shot(page, SLUG, 'overview-tag', {
      boxes: {
        tagBar: '.tag-bar', ordersTag: page.locator('.tag-bar .tag-chip', { hasText: '#orders' }), clear: '.tag-clear',
        firstCard: page.locator('.simple-card').first(),
      },
    })
    await page.keyboard.press('Escape')
  }
} finally {
  // leave the demo data as it was seeded
  await page.evaluate(async ({ id }) => {
    await fetch(`/api/agent/sessions/${id}/tags`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ tags: ['feature', 'orders'] }) })
  }, { id: S.steer }).catch(() => {})
  await browser.close()
}
await drawAllBoxes(SLUG)

// Episode 2: projects, sessions and tags.
import { libShot, press } from '../lib.mjs'
import { openWorld, openSession, S, go, settle, sleep, permissionRequest, question } from '../world.mjs'
import { PROJECTS } from '../seed-extra.mjs'

export default async function (server) {
  const app = await openWorld(server)
  const { browser, page, bridge } = app
  try {
    // --- Projects page
    await go(page, server, '/projects')
    await page.waitForSelector('.pp-row:not(.pp-row--skeleton) .pp-meta--branch')
    await libShot(page, 'projects', 'projects-page', {
      shows: 'Projects page: six projects with branch, uncommitted dot, running sessions, session count and last activity',
      how: 'seeded (real repositories and sessions; "running" = sessions seeded as working)',
      boxes: {
        newProject: '.pp-new', search: '.pp-search', sort: '.pp-sort', list: '.pp-list',
        firstProject: page.locator('.pp-row').nth(0), branch: page.locator('.pp-row').nth(0).locator('.pp-meta--branch'),
        running: page.locator('.pp-row').nth(0).locator('.pp-meta--running'), startSession: page.locator('.pp-row').nth(0).locator('.pp-row__actions .pp-btn').first(),
        cheeseShop: page.locator('.pp-row', { hasText: 'cheese-shop' }),
      },
    })
    await page.locator('.pp-row', { hasText: 'parrot-web' }).locator('.pp-icon-btn').click()
    await sleep(300)
    await libShot(page, 'projects', 'project-menu', {
      shows: 'A project row\'s ⋯ menu (open, Git, edit, Remove from Eggify)', how: 'seeded',
      boxes: { menu: '.pp-menu__list', row: page.locator('.pp-row', { hasText: 'parrot-web' }) },
    })
    await page.keyboard.press('Escape')

    // --- New project dialog
    await go(page, server, '/projects?new=1')
    await page.waitForSelector('.np-dialog')
    await libShot(page, 'projects', 'new-project', {
      shows: 'New project dialog: the three starting points', how: 'seeded',
      boxes: { dialog: '.np-dialog', existing: '.np-option[data-mode=existing]', clone: '.np-option[data-mode=clone]', empty: '.np-option[data-mode=empty]' },
    })

    await go(page, server, '/projects?new=existing')
    await page.waitForSelector('.np-dialog .np-combo input')
    const folder = page.locator('.np-dialog .np-combo input')
    await folder.click()
    await folder.pressSequentially('~/projects/a', { delay: 30 })
    await page.waitForSelector('.np-suggest__item').catch(() => {})
    await sleep(500)
    await libShot(page, 'projects', 'new-project-existing-typing', {
      shows: 'New project → Open an existing folder: a path being typed, with folder suggestions', how: 'seeded',
      boxes: { dialog: '.np-dialog', folder: '.np-combo', 'suggestions?': '.np-suggest' },
    })
    await folder.fill('~/projects/argument-clinic')
    await page.keyboard.press('Escape').catch(() => {})
    await page.locator('.np-title').click()
    await sleep(1200)
    await libShot(page, 'projects', 'new-project-existing', {
      shows: 'New project → Open an existing folder: argument-clinic chosen, name and details filled in from the folder', how: 'seeded',
      boxes: { dialog: '.np-dialog', folder: '.np-combo', 'status?': '.np-status', submit: '.np-dialog .np-btn--primary' },
    })

    await go(page, server, '/projects?new=clone')
    await page.waitForSelector('.np-dialog input')
    const repo = page.locator('.np-dialog input').first()
    await repo.click()
    await repo.pressSequentially('https://github.com/teashop-example/spanish-inquisition', { delay: 10 })
    await page.locator('.np-title').click()
    await sleep(1000)
    await libShot(page, 'projects', 'new-project-clone', {
      shows: 'New project → Clone a repository: an HTTPS URL typed, destination folder proposed under ~/projects', how: 'seeded (nothing is cloned; the URL is fictional)',
      boxes: { dialog: '.np-dialog', repository: page.locator('.np-dialog .np-field').first(), 'destination?': '.np-dest', submit: '.np-dialog .np-btn--primary' },
    })

    await go(page, server, '/projects?new=empty')
    await page.waitForSelector('.np-dialog input')
    const name = page.locator('.np-dialog input').first()
    await name.click()
    await name.pressSequentially('ministry-forms', { delay: 10 })
    await page.locator('.np-title').click()
    await sleep(1000)
    await libShot(page, 'projects', 'new-project-empty', {
      shows: 'New project → Create an empty folder: name typed, parent folder ~/projects, git init option', how: 'seeded (not submitted)',
      boxes: { dialog: '.np-dialog', name: page.locator('.np-dialog .np-field').first(), 'destination?': '.np-dest', 'gitInit?': '.np-check', submit: '.np-dialog .np-btn--primary' },
    })

    // --- Agents page: the sidebar's groups (no project selected, so every project's sessions show)
    await page.evaluate(() => localStorage.setItem('eggify-sidebar-collapsed-groups', JSON.stringify({ today: false, earlier: false })))
    const since = Date.now()
    await go(page, server, '/agents')
    await bridge.waitFor('messages_loaded', { since })
    bridge.inject(permissionRequest()); bridge.inject(question())
    await sleep(1200)
    await page.locator('.needs-you-toast button[aria-label*="ismiss"], .needs-you-toast .toast-close').first().click().catch(() => {})
    await hideToasts(page)
    await libShot(page, 'projects', 'sidebar-groups', {
      shows: 'Agents page with every project\'s sessions: sidebar groups Needs you / Working / Today / Earlier, tag chips, worker rows under the team session',
      how: 'seeded sessions and tags; "Needs you" rows from an injected permission_request and user_question event; subagent count and loop chip patched into the session list',
      boxes: {
        sidebar: '.sessions-sidebar', newSession: '.btn-new-session',
        needsYou: '.session-group.group-needs', working: '.session-group.group-working', 'today?': '.session-group.group-today', 'earlier?': '.session-group.group-earlier',
        waitingRow: page.locator('.group-needs .session-item').first(), tags: page.locator('.group-working [data-testid=session-tags]').first(),
        'workerRows?': page.locator('[data-testid=worker-session-row]').first(),
      },
    })
    // the list scrolled to Today / Earlier
    await page.locator('.sessions-list').evaluate(el => { el.scrollTop = el.scrollHeight })
    await sleep(400)
    await libShot(page, 'projects', 'sidebar-earlier', {
      shows: 'The sidebar scrolled down: Today and Earlier as one-line rows with tag chips, ended sessions dimmed',
      how: 'seeded',
      boxes: { sidebar: '.sessions-sidebar', 'today?': '.session-group.group-today', 'earlier?': '.session-group.group-earlier' },
    })

    // --- Command palette
    await press(page, 'Meta+KeyK')
    await page.waitForSelector('.palette')
    await libShot(page, 'projects', 'command-palette', {
      shows: 'Command palette (⌘K): sessions, projects and actions', how: 'seeded',
      boxes: { palette: '.palette', input: '.palette-search', results: '.palette-results', footer: '.palette-footer' },
    })
    await page.locator('.palette-input').pressSequentially('parrot', { delay: 20 })
    await sleep(500)
    await libShot(page, 'projects', 'command-palette-search', {
      shows: 'Command palette with "parrot" typed: matching sessions and the parrot-web project', how: 'seeded',
      boxes: { palette: '.palette', input: '.palette-search', firstResult: page.locator('.palette-item').first() },
    })
    await page.keyboard.press('Escape')

    // --- Project picker
    await press(page, 'Meta+Alt+Shift+KeyP', 900)
    await page.waitForSelector('.ps')
    await page.waitForSelector('.ps .ps-branch').catch(() => {})
    await sleep(600)
    await libShot(page, 'projects', 'project-picker', {
      shows: 'Switch project picker (⇧⌘P): projects with description, branch, running sessions and last activity', how: 'seeded',
      boxes: { picker: '.ps', search: '.ps-search', list: '.ps-list', firstProject: page.locator('.ps .project-item').first(), footer: '.ps-footer' },
    })
    await page.keyboard.press('Escape')

    // --- Agent overview
    await press(page, 'Meta+KeyO', 1500)
    await page.waitForSelector('.agent-overview')
    await sleep(2500)
    await libShot(page, 'projects', 'agent-overview', {
      shows: 'Agent overview (⌘O), Working tab: one column per project, cards with summaries and tags', how: 'seeded sessions and digests; live states patched into the list',
      boxes: { overview: '.agent-overview', filters: '.overview-filters', 'board?': '.simple-board', 'firstColumn?': page.locator('.simple-column').first(), 'firstCard?': page.locator('.simple-card').first() },
    })
    await page.locator('.overview-filters button', { hasText: 'All' }).click()
    await sleep(2000)
    await libShot(page, 'projects', 'agent-overview-all', {
      shows: 'Agent overview, All tab: every session of the last day by project', how: 'seeded sessions and digests; live states patched into the list',
      boxes: { overview: '.agent-overview', filters: '.overview-filters', 'board?': '.simple-board', 'firstCard?': page.locator('.simple-card').first() },
    })
    await page.keyboard.press('Escape')

    // --- Tag popover on an open session
    await openSession(app, S.steer)
    await press(page, 'Meta+Alt+KeyS', 600)
    await page.waitForSelector('.tag-pop')
    await page.locator('.tag-pop__input').pressSequentially('loy', { delay: 40 })
    await sleep(400)
    await libShot(page, 'projects', 'tag-popover', {
      shows: 'Add tag popover (⌘S) on the open session: its tags as chips, "loy" being typed', how: 'seeded',
      boxes: { popover: '.tag-pop', input: '.tag-pop__field', 'chips?': page.locator('.tag-pop__chip').first(), 'suggestions?': '.tag-pop__suggestions' },
    })
    await page.locator('.tag-pop__input').fill('d')
    await sleep(400)
    await libShot(page, 'projects', 'tag-popover-suggestions', {
      shows: 'Add tag popover with "d" typed: tags used on other sessions are suggested', how: 'seeded',
      boxes: { popover: '.tag-pop', input: '.tag-pop__field', 'suggestions?': '.tag-pop__suggestions' },
    })
    await page.keyboard.press('Escape')

    // --- Create Session prefilled from a project
    await go(page, server, `/agents?project=${PROJECTS.parrot.id}&new_session=1`)
    await page.waitForSelector('[aria-labelledby=new-session-title]', { timeout: 15000 })
    await sleep(800)
    await libShot(page, 'projects', 'create-session-from-project', {
      shows: 'Create Session opened from a project ("Start a session here"): working directory prefilled with the project folder, model list, Team switch', how: 'seeded; agent runtimes stubbed as installed and signed in',
      boxes: { dialog: '.modal-content', workingDirectory: page.locator('.modal-content .form-group').first(), models: page.locator('.modal-content .launch-basics, .modal-content .modal-body').first(), 'team?': '[data-testid=team-setup]', 'settingsToggle?': '.settings-toggle', create: '.btn-create' },
    })
  } finally { await browser.close() }
}

export async function hideToasts(page) {
  // close the "needs you" toasts the way a user would (their × button)
  for (let i = 0; i < 4; i++) {
    const x = page.locator('.needs-you-toast button').last()
    if (!(await x.count())) break
    await x.click().catch(() => {})
    await sleep(200)
  }
}

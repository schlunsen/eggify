// Episode 7: git (the project Git page, a session's Git card and Git Changes page).
import { join } from 'node:path'
import { libShot } from '../lib.mjs'
import { openWorld, openSession, S, go, sleep } from '../world.mjs'
import { PROJECTS, TEAM, repoPaths } from '../seed-extra.mjs'

export default async function (server) {
  const app = await openWorld(server)
  const { browser, page } = app
  const cheese = repoPaths(server).cheese
  const teamWt = join(cheese, TEAM.worktreeDir), workerWt = join(cheese, TEAM.workerDir)
  const gitUrl = q => `/projects/${PROJECTS.cheese.id}/git${q ? '?' + new URLSearchParams(q) : ''}`
  const ready = async (selected = false) => { await page.waitForSelector('.gp-worktrees article.wt', { timeout: 15000 }); await page.waitForSelector('section.gcp', { timeout: 15000 }); if (selected) await page.waitForSelector('.gp-worktrees article.wt.selected', { timeout: 8000 }).catch(() => {}); await sleep(900) }
  try {
    await go(page, server, gitUrl())
    await ready()
    await libShot(page, 'git', 'git-page', {
      shows: 'Project Git page of cheese-shop: three worktrees (main checkout, the team\'s, the build worker\'s) with branch, change counts, ahead/behind and open sessions; branches; latest commits; the selection\'s changes on the right',
      how: 'seeded (a real repository with two extra worktrees and six branches)',
      boxes: {
        worktrees: '.gp-worktrees', mainWorktree: page.locator('.gp-worktrees article.wt').first(), teamWorktree: page.locator('.gp-worktrees article.wt').nth(1), workerWorktree: page.locator('.gp-worktrees article.wt').nth(2),
        'branches?': '.br-list', 'branchFilters?': '.br-filters', changes: 'section.gcp', tabs: '.gcp-tabs', 'files?': 'nav.gcp-files',
      },
    })

    // the build worker's worktree: staged + modified work, diff open
    await go(page, server, gitUrl({ wt: workerWt, scope: 'uncommitted' }))
    await ready(true)
    await page.locator('section.gcp button', { hasText: 'Expand all' }).click().catch(() => {})
    await sleep(600)
    await libShot(page, 'git', 'worktree-uncommitted', {
      shows: 'The build worker\'s worktree selected, Uncommitted tab: a staged new file and a modified file, diffs expanded with numbered lines and the changed part of a line highlighted',
      how: 'seeded (real uncommitted work in the worker\'s worktree)',
      boxes: {
        'selectedWorktree?': '.gp-worktrees article.wt.selected', tabs: '.gcp-tabs', uncommittedTab: page.locator('.gcp-tabs button[role=tab]').first(), 'files?': 'nav.gcp-files',
        firstDiff: page.locator('section.gdf').first(), 'toolbar?': page.locator('section.gcp button', { hasText: 'Collapse all' }).locator('xpath=..'),
      },
    })

    // the same checkout against main
    await go(page, server, gitUrl({ wt: workerWt, scope: 'branch' }))
    await ready()
    await page.locator('section.gcp button', { hasText: 'Expand all' }).click().catch(() => {})
    await sleep(600)
    await libShot(page, 'git', 'worktree-vs-main', {
      shows: 'The same worktree, "vs main" tab: everything the branch adds to main (its commits and files), diffs expanded',
      how: 'seeded (real commits on worker/… and team/… branches)',
      boxes: { tabs: '.gcp-tabs', vsMainTab: page.locator('.gcp-tabs button[role=tab]').nth(1), 'files?': 'nav.gcp-files', firstDiff: page.locator('section.gdf').first(), 'commits?': '.gcp-commits' },
    })
    await page.locator('section.gdf').nth(1).evaluate(el => el.scrollIntoView({ block: 'start' })).catch(() => {})
    await sleep(500)
    await libShot(page, 'git', 'diff-file', {
      shows: 'One file of the branch diff close up: file header with +/− counts and the Viewed checkbox, numbered old and new lines',
      how: 'seeded',
      boxes: { diff: page.locator('section.gdf').nth(1), 'head?': page.locator('section.gdf').nth(1).locator('.gdf-head'), 'viewed?': page.locator('section.gdf').nth(1).locator('label.gdf-viewed') },
    })

    // a branch without a checkout, picked from Branches (All)
    await go(page, server, gitUrl({ branch: 'fix/stilton-rounding', scope: 'branch' }))
    await ready()
    await page.locator('.br-filters button', { hasText: 'All' }).click().catch(() => {})
    await sleep(500)
    await page.locator('.br-list').evaluate(el => el.scrollIntoView({ block: 'center' })).catch(() => {})
    await sleep(400)
    await libShot(page, 'git', 'branches-all', {
      shows: 'Branches with the All filter: current, default, worktree and merged tags; fix/stilton-rounding selected with its change against main on the right',
      how: 'seeded',
      boxes: { branches: '.br-list', filters: '.br-filters', selectedBranch: page.locator('.br-list button.br', { hasText: 'fix/stilton-rounding' }), 'search?': 'input[aria-label="Find a branch"]', changes: 'section.gcp' },
    })

    // --- a session's Git card and its Git Changes page
    await openSession(app, S.builder)
    await page.waitForSelector('section.git-card .git-status', { timeout: 15000 })
    await sleep(800)
    await libShot(page, 'git', 'session-git-card', {
      shows: 'A session\'s Git card in the right sidebar: it works in a worktree (branch worker/…), staged and modified files, branch changes vs its base, View Changes',
      how: 'seeded (the build worker\'s session; the server reads the real worktree)',
      boxes: { card: 'section.git-card', 'worktree?': '[data-testid=worktree-indicator]', 'branch?': 'section.git-card .branch-name', 'viewChanges?': page.locator('section.git-card').getByText(/View (Changes|Branch Diff)/) },
    })
    await page.locator('section.git-card').getByText(/View (Changes|Branch Diff)/).first().click()
    await page.waitForURL(/git-diff/, { timeout: 10000 }).catch(() => {})
    await page.waitForSelector('.file-card, .file-diff', { timeout: 15000 }).catch(() => {})
    await sleep(1200)
    await libShot(page, 'git', 'session-git-changes', {
      shows: 'The session\'s Git Changes page: totals, the list of changed files and each file\'s diff',
      how: 'seeded (real changes in the worker\'s worktree)',
      boxes: { 'stats?': page.locator('.stat-card').first().locator('xpath=..'), 'firstFile?': page.locator('.file-card').first() },
    })
  } finally { await browser.close() }
}

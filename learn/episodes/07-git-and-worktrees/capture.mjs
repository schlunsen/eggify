#!/usr/bin/env node
// Episode 7 "Git page and worktrees": takes the shots named in episode.json.
//
//   learn/capture/library/server.sh --port 3407      # in another terminal: the library's demo world
//   node learn/episodes/07-git-and-worktrees/capture.mjs   # [--port 3407] [--only project,page,...]
//
// The server is the screen library's; project "cheese-shop" is a real git repository with two extra
// worktrees (the team's and a build worker's), six branches and real uncommitted work (seed-extra.mjs).
// Everything is shot through the real UI with real clicks; the server reads the real repository.
// Stubbed (fictional live state, library/world.mjs openWorld): the working/idle status of sessions,
// runtimes, usage, context numbers. Nothing on the Git pages is stubbed.
import { join } from 'node:path'
import { drawAllBoxes, parseArgs, readServer, shot, sleep } from '../../capture/lib.mjs'
import { openWorld, openSession, S, go } from '../../capture/library/world.mjs'
import { PROJECTS, TEAM, repoPaths } from '../../capture/library/seed-extra.mjs'

const SLUG = 'git-and-worktrees'
const args = parseArgs()
const server = readServer(Number(args.port || 3407))
const only = typeof args.only === 'string' ? new Set(args.only.split(',')) : null
const want = n => !only || only.has(n)
const app = await openWorld(server)
const { browser, page } = app
const cheese = repoPaths(server).cheese
const workerWt = join(cheese, TEAM.workerDir)
const gitUrl = q => `/projects/${PROJECTS.cheese.id}/git${q ? '?' + new URLSearchParams(q) : ''}`
const ready = async (selected = false) => {
  await page.waitForSelector('.gp-worktrees article.wt', { timeout: 15000 })
  await page.waitForSelector('section.gcp', { timeout: 15000 })
  if (selected) await page.waitForSelector('.gp-worktrees article.wt.selected', { timeout: 8000 }).catch(() => {})
  await sleep(1000)
}
const wt = n => page.locator('.gp-worktrees article.wt').nth(n)
const expandAll = async () => { await page.locator('section.gcp button', { hasText: 'Expand all' }).click().catch(() => {}); await sleep(600) }
const tab = n => page.locator('.gcp-tabs button[role=tab]').nth(n)

try {
  if (want('project')) {
    await go(page, server, `/projects/${PROJECTS.cheese.id}`)
    await page.waitForSelector('.header-actions a[href$="/git"]')
    await sleep(1200)
    await page.mouse.move(900, 700)
    await shot(page, SLUG, 'project', { boxes: { header: '.header-actions', gitButton: '.header-actions a[href$="/git"]' } })
  }
  if (want('page')) {
    await go(page, server, gitUrl())
    await ready()
    await page.mouse.move(1100, 900)
    await shot(page, SLUG, 'page', {
      boxes: {
        head: '.gp-head', facts: '.gp-facts', worktrees: '.gp-worktrees', mainWt: wt(0), teamWt: wt(1), workerWt: wt(2),
        mainState: wt(0).locator('.wt-state'), teamState: wt(1).locator('.wt-state'), workerState: wt(2).locator('.wt-state'),
        workerCounts: wt(2).locator('.wt-dirty'), workerAhead: wt(2).locator('.ab').first(),
        workerSessions: wt(2).locator('.wt-sessions'), mainSessions: wt(0).locator('.wt-sessions'),
        changes: 'section.gcp',
      },
    })
  }
  if (want('uncommitted')) {
    await go(page, server, gitUrl({ wt: workerWt, scope: 'uncommitted' }))
    await ready(true)
    await expandAll()
    await page.mouse.move(1100, 1000)
    await shot(page, SLUG, 'uncommitted', {
      boxes: {
        selected: '.gp-worktrees article.wt.selected', tabs: '.gcp-tabs', uncommittedTab: tab(0), vsMainTab: tab(1),
        files: 'nav.gcp-files', firstDiff: page.locator('section.gdf').first(), toolbar: '.gcp-toolbar',
      },
    })
  }
  if (want('vsmain')) {
    await go(page, server, gitUrl({ wt: workerWt, scope: 'branch' }))
    await ready()
    await expandAll()
    await page.mouse.move(1100, 1000)
    await shot(page, SLUG, 'vsmain', {
      boxes: {
        tabs: '.gcp-tabs', vsMainTab: tab(1), commits: '.gcp-commits', files: 'nav.gcp-files',
        firstDiff: page.locator('section.gdf').first(), toolbar: '.gcp-toolbar',
        viewed: page.locator('section.gdf').first().locator('label.gdf-viewed'),
      },
    })
    await page.locator('section.gdf').first().locator('label.gdf-viewed input').check()
    await page.locator('section.gdf').nth(1).locator('label.gdf-viewed input').check()
    await sleep(700)
    await page.mouse.move(1100, 1000)
    await shot(page, SLUG, 'viewed', {
      boxes: {
        stats: '.gcp-stats', viewedCount: '.gcp-viewed-count', files: 'nav.gcp-files',
        firstViewed: page.locator('section.gdf').first().locator('label.gdf-viewed'), firstDiff: page.locator('section.gdf').first(),
      },
    })
  }
  if (want('branches')) {
    await go(page, server, gitUrl({ branch: 'fix/stilton-rounding', scope: 'branch' }))
    await ready()
    await page.locator('.br-list').evaluate(el => el.scrollIntoView({ block: 'center' })).catch(() => {})
    await sleep(500)
    await shot(page, SLUG, 'branches-active', {
      boxes: { branches: '.br-list', filters: '.br-filters', filterActive: page.locator('.br-filters button', { hasText: 'Active' }), search: 'input[aria-label="Find a branch"]' },
    })
    await page.locator('.br-filters button', { hasText: 'All' }).click()
    await sleep(600)
    await page.locator('.br-list').evaluate(el => el.scrollIntoView({ block: 'center' })).catch(() => {})
    await sleep(400)
    await page.mouse.move(1100, 900)
    await shot(page, SLUG, 'branches-all', {
      boxes: {
        branches: '.br-list', filters: '.br-filters', filterAll: page.locator('.br-filters button', { hasText: 'All' }), filterMerged: page.locator('.br-filters button', { hasText: 'Merged' }),
        selectedBranch: page.locator('.br-list button.br', { hasText: 'fix/stilton-rounding' }),
        mergedTag: page.locator('.br-list .tag.ok').first(), currentTag: page.locator('.br-list .tag.accent').first(),
        changes: 'section.gcp',
      },
    })
  }
  if (want('newsession')) {
    await go(page, server, gitUrl({ wt: workerWt, scope: 'uncommitted' }))
    await ready(true)
    await page.mouse.move(1100, 1000)
    await shot(page, SLUG, 'new-here', {
      boxes: { selected: '.gp-worktrees article.wt.selected', newHere: wt(2).locator('a.wt-act', { hasText: 'New session here' }), workerSessions: wt(2).locator('.wt-sessions') },
    })
    await wt(2).locator('a.wt-act', { hasText: 'New session here' }).click()
    await page.waitForURL(/new_session=1/, { timeout: 10000 })
    await sleep(2500)
    await page.mouse.move(1100, 1000)
    await shot(page, SLUG, 'create', { boxes: { dialog: '[role=dialog], .modal, .create-session', cwd: { x: 700, y: 194, w: 521, h: 42 } } }).catch(async e => { console.log('create boxes: ' + e.message) })
  }
  if (want('session')) {
    await openSession(app, S.builder)
    await page.waitForSelector('section.git-card .git-status', { timeout: 15000 })
    await sleep(1000)
    await page.mouse.move(1000, 800)
    await shot(page, SLUG, 'session-card', {
      boxes: {
        card: 'section.git-card', worktree: '[data-testid=worktree-indicator]', branch: 'section.git-card .branch-name',
        branchChanges: page.locator('section.git-card').getByText('Branch changes').locator('xpath=../..'),
        viewChanges: page.locator('section.git-card').getByText(/View (Changes|Branch Diff)/).first(),
      },
    })
    await page.locator('section.git-card').getByText(/View (Changes|Branch Diff)/).first().click()
    await page.waitForURL(/git-diff/, { timeout: 10000 })
    await page.waitForSelector('.file-card', { timeout: 15000 })
    await sleep(1200)
    await page.mouse.move(1000, 1000)
    await shot(page, SLUG, 'session-changes', {
      boxes: { stats: page.locator('.stat-card').first().locator('xpath=..'), firstFile: page.locator('.file-card').first() },
    })
  }
} finally {
  await browser.close()
}
const proofs = await drawAllBoxes(SLUG)
console.log(`\nProof sheets:\n  ${proofs.join('\n  ')}`)

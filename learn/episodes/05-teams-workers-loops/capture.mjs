#!/usr/bin/env node
// Episode 5 "Teams, workers and loops": takes the shots named in episode.json.
//
//   learn/capture/library/server.sh --port 3405     # another terminal: the library's demo world
//   node learn/episodes/05-teams-workers-loops/capture.mjs        # [--port 3405]
//
// Same states as the library's `teams` area (capture/library/areas/teams.mjs), re-shot into this
// episode's folder on a server whose home is /Users/Shared/<name> (so paths read like a Mac's).
// Seeded: the team session, its two workers, the loop session, the proposing session.
// Stubbed / injected (fictional data through the real UI; library/world.mjs openWorld):
//   runtimes (Create Session's model list), usage limit, context usage, system usage meter;
//   team-chat: the reviewer's "Now" line (/workers answer + worker_activity event);
//   worker-proposal-card, loop-proposal-card: injected user_question events, never answered;
//   loop-session: injected loop_started / loop_iteration events for the banner.
//   the proposal cards' folder is rewritten to /Users/Shared/projects/cheese-shop so the app shows ~/projects/cheese-shop.
// Nothing is submitted; no agent runs.
import { drawAllBoxes, parseArgs, readServer, shot as episodeShot } from '../../capture/lib.mjs'
import { openWorld, openSession, S, go, sleep, workerProposal, loopProposal, loopEvents, workerLines, setWorking } from '../../capture/library/world.mjs'
import { PROJECTS } from '../../capture/library/seed-extra.mjs'

const SLUG = 'teams-workers-loops'
const args = parseArgs()
const server = readServer(Number(args.port || 3405))
async function libShot(page, area, name, opts = {}) {
  const boxes = {}
  for (const [k, v] of Object.entries(opts.boxes || {})) boxes[k.replace(/\?$/, '')] = v
  // optional boxes (name ending in ?) are dropped when absent
  const keep = {}
  for (const [k, v] of Object.entries(opts.boxes || {})) {
    const opt = k.endsWith('?'); const id = opt ? k.slice(0, -1) : k
    if (opt) { const loc = typeof v === 'string' ? page.locator(v) : v; if (typeof v === 'object' && 'x' in v) { keep[id] = v; continue } if (!(await loc.count())) continue }
    keep[id] = v
  }
  await episodeShot(page, SLUG, name, { boxes: keep })
}
// The app shortens /Users/<anything>/ to "~", so under /Users/Shared/<name> a folder would read
// "~/<name>/projects/…". The proposal cards' folder is stubbed payload: give it the form a real
// Mac shows, "~/projects/cheese-shop" (/Users/Shared/projects/… shortened by the app).
const readable = ev => JSON.parse(JSON.stringify(ev).split(server.home + '/projects').join('/Users/Shared/projects'))
async function pick(select, text) {
  const value = await select.evaluate((el, t) => [...el.options].find(o => o.textContent.includes(t))?.value, text)
  if (value) await select.selectOption(value)
  return !!value
}
{
  const app = await openWorld(server)
  const { browser, page, bridge } = app
  try {
    // --- Create Session with Team on
    await go(page, server, `/agents?project=${PROJECTS.cheese.id}&new_session=1`)
    await page.waitForSelector('[data-testid=team-toggle]', { timeout: 15000 })
    await sleep(600)
    await page.locator('[data-testid=team-toggle]').check({ force: true }).catch(async () => { await page.locator('.team-toggle').click() })
    await sleep(500)
    await pick(page.locator('[data-testid=team-builder]'), 'deepseek-chat')
    await pick(page.locator('[data-testid=team-reviewer]'), 'gpt-6-astra')
    await sleep(400)
    await page.locator('[data-testid=team-setup]').evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(400)
    await libShot(page, 'teams', 'create-session-team', {
      shows: 'Create Session with Team switched on: this session plans, a builder model and a reviewer model do the work, review rounds',
      how: 'seeded; runtimes stubbed as ready (not submitted)',
      boxes: { dialog: '.modal-content', team: '[data-testid=team-setup]', toggle: '.team-toggle', builder: '[data-testid=team-builder]', reviewer: '[data-testid=team-reviewer]', rounds: '[data-testid=team-rounds]', 'note?': '[data-testid=team-note]', create: '.btn-create' },
    })
    await page.locator('.settings-toggle').click()
    await sleep(500)
    await page.locator('[data-testid=team-worktree-note]').evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(400)
    await libShot(page, 'teams', 'create-session-team-worktree', {
      shows: 'Session settings while Team is on: the Isolated Worktree switch is locked on (a team never works in the checkout it was started in), sandbox, permissions',
      how: 'seeded; runtimes stubbed as ready (not submitted)',
      boxes: { dialog: '.modal-content', worktreeLocked: page.locator('.worktree-group', { has: page.locator('[data-testid=team-worktree-note]') }), worktreeNote: '[data-testid=team-worktree-note]' },
    })
    // loop mode in the same dialog (Team off again)
    await page.locator('.modal-content').evaluate(el => { const b = el.querySelector('.modal-body') || el; b.scrollTop = 0 })
    await page.locator('[data-testid=team-toggle]').uncheck({ force: true }).catch(async () => { await page.locator('.team-toggle').click() })
    await page.locator('.mode-btn', { hasText: 'Loop' }).click()
    await sleep(400)
    const loopInputs = page.locator('.loop-config input, .loop-config textarea')
    const n = await loopInputs.count()
    if (n > 0) await loopInputs.nth(0).fill('Plank prices round to the nearest penny and the tests pass')
    if (n > 1) await loopInputs.nth(1).fill('cargo test --quiet')
    await page.locator('.loop-config').evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(400)
    await libShot(page, 'teams', 'create-session-loop', {
      shows: 'Create Session in Loop mode: a goal, the check command that decides when it is done, and the iteration limit',
      how: 'seeded; runtimes stubbed as ready (not submitted)',
      boxes: { dialog: '.modal-content', modes: '.mode-selector', loopConfig: '.loop-config' },
    })

    // --- the team session: strip, worker chips, worker rows in the sidebar
    await openSession(app, S.team)
    bridge.inject({ type: 'worker_activity', session_id: S.team, worker_session_id: S.reviewer, output: 'TOOL:Bash: uv run pytest -q tests/test_alerts.py', is_error: false, ...workerLines(server), session_id: S.team })
    await page.waitForSelector('[data-testid=team-strip]')
    await sleep(900)
    await libShot(page, 'teams', 'team-chat', {
      shows: 'A team session\'s chat: the TeamStrip (Plan → Build → Review (round 2 of 3) → Merge) with a chip per worker and the working worker\'s latest action; the orchestrator\'s messages with worker reports; worker rows under the session in the sidebar',
      how: 'seeded sessions (team + two workers); the reviewer\'s live action line is a stubbed /workers answer and injected worker_activity event',
      settleOpts: { timeout: 2500 },
      boxes: {
        strip: '[data-testid=team-strip]', stage: '[data-testid=team-stage]', steps: '.team-steps', reviewStep: '.team-step[data-step=review]', workers: '.team-workers',
        builderChip: page.locator('[data-testid=team-worker-chip]').first(), reviewerChip: page.locator('[data-testid=team-worker-chip]').nth(1), 'now?': '[data-testid=team-now]',
        builderRow: page.locator('[data-testid=worker-session-row]').first(), reviewerRow: page.locator('[data-testid=worker-session-row]').nth(1), 'sessionRow?': page.locator('.session-item').first(),
      },
    })
    await page.locator('.messages-container').evaluate(el => { el.scrollTop = 0 })
    await sleep(500)
    await libShot(page, 'teams', 'team-chat-plan', {
      shows: 'The start of the team session: the user\'s request and the orchestrator\'s plan (what it does itself, what goes to the builder, what the reviewer checks)',
      how: 'seeded', settleOpts: { timeout: 2500 },
      boxes: { strip: '[data-testid=team-strip]', chat: '.messages-container' },
    })

    // --- a worker's own chat
    await openSession(app, S.builder)
    await page.waitForSelector('[data-testid=worker-note]')
    await page.locator('.messages-container').evaluate(el => { el.scrollTop = 0 })
    await sleep(500)
    await libShot(page, 'teams', 'worker-chat-builder', {
      shows: 'The build worker\'s chat: the WorkerNote at the top ("Build worker of … · branch worker/…"), its task as the first prompt, its work in its own worktree',
      how: 'seeded', settleOpts: { timeout: 2500 },
      boxes: { note: '[data-testid=worker-note]', parentLink: '[data-testid=worker-note-parent]', chat: '.messages-container', 'git?': 'section.git-card' },
    })
    await openSession(app, S.reviewer)
    setWorking(app, S.reviewer)
    await page.waitForSelector('[data-testid=worker-note]')
    await sleep(700)
    await libShot(page, 'teams', 'worker-chat-reviewer', {
      shows: 'The review worker\'s chat (read-only, working): WorkerNote, the review prompt asking for a verdict, git diff and test commands',
      how: 'seeded as working', settleOpts: { timeout: 2500 },
      boxes: { note: '[data-testid=worker-note]', chat: '.messages-container', status: '.status-pill' },
    })

    // --- proposal cards
    await openSession(app, S.proposer)
    bridge.inject(readable(workerProposal(server)))
    await page.waitForSelector('[data-testid=worker-session-card]')
    await sleep(700)
    await libShot(page, 'teams', 'worker-proposal-card', {
      shows: 'An agent proposes a worker session: task, mode (build), model, sandbox and worktree badges, Start / Deny / Later. Nothing exists before Start',
      how: 'seeded session; the card is an injected user_question event (kind worker_session), never answered', settleOpts: { timeout: 2500 },
      boxes: { card: '[data-testid=worker-session-card]', 'badges?': '[data-testid=worker-badges]', start: '[data-testid=worker-start]', deny: '[data-testid=worker-deny]' },
    })
    await openSession(app, S.proposer)
    bridge.inject(readable(loopProposal(server)))
    await page.waitForSelector('[data-testid=loop-session-card]')
    await sleep(700)
    await libShot(page, 'teams', 'loop-proposal-card', {
      shows: 'An agent proposes a loop session: goal, check command, limits, the warning that it runs with permission prompts off; Start loop / Edit first / Deny',
      how: 'seeded session; the card is an injected user_question event (kind loop_session), never answered', settleOpts: { timeout: 2500 },
      boxes: { card: '[data-testid=loop-session-card]', start: '[data-testid=loop-start]', edit: '[data-testid=loop-edit]', deny: '[data-testid=loop-deny]' },
    })

    // --- a running loop session
    await openSession(app, S.loop)
    for (const e of loopEvents()) { bridge.inject(e); await sleep(120) }
    setWorking(app, S.loop)
    await page.waitForSelector('.loop-banner')
    await sleep(700)
    await libShot(page, 'teams', 'loop-session', {
      shows: 'A loop session mid-run: the loop banner (iteration 3 / 20, a dot per check: two failed), Stop loop, loop notes in the chat with the check\'s output',
      how: 'seeded session and messages; the banner state comes from injected loop_started / loop_iteration events', settleOpts: { timeout: 2500 },
      boxes: { banner: '.loop-banner', 'title?': '.loop-banner-title', 'timeline?': '.loop-timeline', 'stop?': '.loop-stop-btn', chat: '.messages-container' },
    })
  } finally { await browser.close() }
}
await drawAllBoxes(SLUG)

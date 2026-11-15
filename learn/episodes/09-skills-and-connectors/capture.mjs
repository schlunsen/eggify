#!/usr/bin/env node
// Episode 10 "Skills and connectors": takes the shots named in episode.json.
//
//   learn/capture/library/server.sh --port 3410      # in another terminal: the library's demo world
//   node learn/episodes/09-skills-and-connectors/capture.mjs   # [--port 3410] [--only skills,queue,...]
//
// Seeded on the demo server (seed-extra.mjs): six real SKILL.md folders in the demo home, four approved
// through the review API (the Skills cards), two waiting in the review queue; GitHub, Linear and Sentry
// connected with made-up keys. Everything is shot through the real UI.
// Stubbed (fictional state shown through the real UI), nothing is contacted:
//   - GET /api/connectors/n0: one connected n0 app ("tea-counter" in the workspace "Teashop"), for the
//     connected n0 card and for n0 in Create Session. No real n0 login happens; the "Connect n0" click is
//     only a cursor in the video.
//   - GET /api/agent/sessions/:id/skills: the server's own list plus one skill of another project
//     ("cheese-label-print"), so the panel shows "Add to this session".
//   - an approval card (a user_question with the shape of the server's tool confirmation) for a
//     Slack "post message" call, injected as a WebSocket event and never answered.
//   - the demo world's usual live state (library/world.mjs): runtimes, usage, context numbers.
// The "Connect Slack" dialog is filled with a made-up key and closed again, never submitted.
import { drawAllBoxes, parseArgs, readServer, shot, sleep } from '../../capture/lib.mjs'
import { openWorld, openSession, S, go } from '../../capture/library/world.mjs'
import { PROJECTS } from '../../capture/library/seed-extra.mjs'

const SLUG = 'skills-and-connectors'
const args = parseArgs()
const server = readServer(Number(args.port || 3410))
const only = typeof args.only === 'string' ? new Set(args.only.split(',')) : null
const want = n => !only || only.has(n)
const app = await openWorld(server)
const { browser, page, context, bridge } = app
const isoAgo = d => new Date(Date.now() - d * 86400000).toISOString()

// ---- stubs
let n0On = false          // the stub below answers only once this is switched on
await context.route(/\/api\/connectors\/n0(\?.*)?$/, async route => {
  if (!n0On) return route.continue()
  const real = await route.fetch()
  const body = await real.json()
  body.connections = [{
    id: 1, host: 'https://moon.nzero.pro', workspace_name: 'Teashop', app_subdomain: 'tea-counter', app_name: 'Tea counter',
    status: 'active', status_message: '', scopes: ['mcp'], last_used_at: isoAgo(0.1), created_at: isoAgo(3),
    connected_tools_url: 'https://moon.nzero.pro/settings/security',
  }]
  body.browser_is_local = true
  route.fulfill({ response: real, json: body })
})
await context.route(/\/api\/agent\/sessions\/[0-9a-f-]{36}\/skills(\?.*)?$/, async route => {
  if (route.request().method() !== 'GET') return route.continue()
  const real = await route.fetch()
  const body = await real.json()
  body.skills = [...(body.skills || []), {
    id: 'demo-other-skill', name: 'cheese-label-print', description: 'Print a price label for a cheese, with its weight and best-before date.',
    scope: 'project', user_invocable: true, reach: '', added: false,
  }]
  route.fulfill({ response: real, json: body })
})
const approval = () => ({
  type: 'user_question', session_id: S.refund, question_id: 'q-demo-confirm-1', header: 'Approval',
  question: 'The agent wants to run slack__post_message, which sends a message to people. Allow it?\n\n• channel: #counter\n• text: The spring menu is live.',
  options: [
    { label: 'Approve', description: 'Run slack__post_message now' },
    { label: 'Deny', description: 'Block this call; the agent is told you declined' },
  ],
  multi_select: false, question_index: 1, question_total: 1, requires_user: true, recommended: [], auto_answer_at: null, held: false,
  session_name: 'Review the returns page copy',
})

try {
  if (want('skills')) {
    await go(page, server, '/skills')
    await page.waitForSelector('.skill-card', { timeout: 15000 })
    await page.locator('.skill-card').first().evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(700)
    await page.mouse.move(900, 600)
    await shot(page, SLUG, 'skills', {
      boxes: { skillsNav: page.locator('nav a[href="/skills"], a[href="/skills"]').first(), cards: page.locator('.skill-card').first().locator('xpath=..'), first: page.locator('.skill-card').first(), release: page.locator('.skill-card', { hasText: 'release-checklist' }).first() },
    })
  }
  if (want('queue')) {
    await go(page, server, '/skills')
    await page.waitForSelector('[data-test=rq-item]', { timeout: 15000 })
    await sleep(800)
    await page.mouse.move(1100, 900)
    const first = page.locator('[data-test=rq-item]').first()
    await shot(page, SLUG, 'queue', {
      boxes: {
        title: page.locator('.review-queue h2, .review-queue h3').first(), name: first.locator('h3, h4, .rq-name, strong').first(),
        approve: first.getByRole('button', { name: 'Approve', exact: true }), reject: first.getByRole('button', { name: 'Reject', exact: true }),
        body: first.locator('pre').first(),
      },
    })
  }
  if (want('slash') || want('session') || want('panel')) {
    await openSession(app, S.steer)
    if (want('slash')) {
      await page.locator('textarea.message-input').click()
      await page.locator('textarea.message-input').pressSequentially('/rel', { delay: 60 })
      await page.waitForSelector('.autocomplete-overlay')
      await sleep(700)
      await shot(page, SLUG, 'slash', {
        boxes: { composer: '.input-area', list: '.autocomplete-overlay', item: page.locator('.autocomplete-overlay .command-item').first(), input: 'textarea.message-input' },
      })
      await page.locator('textarea.message-input').fill('')
      await page.keyboard.press('Escape')
      await sleep(400)
    }
    if (want('session')) {
      await page.mouse.move(900, 600)
      await shot(page, SLUG, 'session', {
        boxes: { skillsRow: '[data-testid=skills-row]', sidebar: page.locator('[data-testid=skills-row]').locator('xpath=ancestor::*[contains(@class,"sidebar") or contains(@class,"metrics")][1]') },
      })
    }
    if (want('panel')) {
      await page.locator('[data-testid=skills-row] button.skills-open').click()
      await page.waitForSelector('.skills-pop')
      await sleep(900)
      await shot(page, SLUG, 'panel', {
        boxes: {
          panel: '.skills-pop', active: '[data-testid=skills-active]', other: '[data-testid=skills-other]',
          use: page.locator('.skills-pop [data-action=use]').first(), add: page.locator('.skills-pop [data-action=add]').first(),
          firstName: page.locator('.skills-pop__name').first(),
        },
      })
      await page.keyboard.press('Escape')
    }
  }
  if (want('connectors') || want('key')) {
    await go(page, server, '/connectors')
    await page.waitForSelector('text=GitHub', { timeout: 15000 })
    await sleep(1200)
    await page.mouse.move(1100, 1000)
    const n0card = page.locator('.n0-card')
    if (want('connectors')) {
      await shot(page, SLUG, 'connectors', {
        boxes: {
          n0: n0card, connectN0: page.getByRole('button', { name: 'Connect n0', exact: true }), host: '#n0-host',
          connected: page.locator('.section', { hasText: /^\s*Connected \(/ }).first(),
          github: page.locator('.connector-card', { hasText: 'GitHub' }).first(),
        },
      })
    }
    if (want('key')) {
      const slack = page.locator('.connector-card', { hasText: 'Slack' }).first()
      await slack.evaluate(el => el.scrollIntoView({ block: 'center' }))
      await sleep(400)
      await shot(page, SLUG, 'cards', { boxes: { slack, slackConnect: slack.getByRole('button', { name: 'Connect', exact: true }) } })
      await slack.getByRole('button', { name: 'Connect', exact: true }).click()
      await page.waitForSelector('.modal-content, [role=dialog]')
      const key = page.getByPlaceholder('Enter your Slack API key')
      await key.fill('xoxb-demo-0000-not-a-real-key')
      await sleep(500)
      await shot(page, SLUG, 'key', {
        boxes: { dialog: page.locator('.modal-content, [role=dialog]').last(), keyField: key, connect: page.locator('.modal-content, [role=dialog]').last().getByRole('button', { name: 'Connect', exact: true }) },
      })
      await page.keyboard.press('Escape')
      await sleep(400)
    }
  }
  if (want('n0')) {
    n0On = true
    await go(page, server, '/connectors')
    await page.waitForSelector('.n0-conn-row', { timeout: 15000 })
    await sleep(1000)
    await page.mouse.move(1100, 1000)
    await shot(page, SLUG, 'n0', {
      boxes: { n0: '.n0-card', row: '.n0-conn-row', app: '.n0-conn-app', status: '.n0-status-badge', connectN0: page.getByRole('button', { name: 'Connect n0', exact: true }) },
    })
  }
  if (want('create')) {
    n0On = true
    await go(page, server, `/agents?project=${PROJECTS.parrot.id}&new_session=1`)
    await page.waitForSelector('.settings-toggle', { timeout: 15000 })
    await page.locator('.settings-toggle').click()
    await sleep(700)
    const group = page.locator('.form-group', { has: page.locator('.label-count', { hasText: /\d+\/\d+/ }) }).first()
    await group.evaluate(el => el.scrollIntoView({ block: 'start' }))
    await sleep(700)
    await page.mouse.move(1500, 1000)
    await shot(page, SLUG, 'create', {
      boxes: {
        dialog: '.modal-content', connectors: group, selectAll: group.locator('.toggle-all-btn'),
        firstTick: group.locator('.skill-checkbox').first(), n0Tick: group.locator('.skill-checkbox', { hasText: 'n0' }).first(),
      },
    })
    await page.keyboard.press('Escape')
  }
  if (want('approval')) {
    await openSession(app, S.refund)
    bridge.inject(approval())
    await page.waitForSelector('.question-modal', { timeout: 10000 })
    await sleep(900)
    await shot(page, SLUG, 'approval', {
      boxes: { card: '.question-modal', question: page.locator('.question-modal .modal-subtitle').first(), approveOpt: page.locator('.question-modal .option-card').nth(0), denyOpt: page.locator('.question-modal .option-card').nth(1), submit: page.locator('.question-modal').getByRole('button', { name: /Submit/ }) },
    })
  }
} finally {
  await browser.close()
}
await drawAllBoxes(SLUG)

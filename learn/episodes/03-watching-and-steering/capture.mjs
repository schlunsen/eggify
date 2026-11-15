#!/usr/bin/env node
// Episode 3 "Watching and steering an agent": takes the shots named in episode.json.
//
//   learn/capture/library/server.sh --port 3403          # in another terminal: the library's demo world
//   node learn/episodes/03-watching-and-steering/capture.mjs    # [--port 3403] [--only tools,permission,question,working,model,sidebar]
//
// The server is the screen library's (base seed + library/seed-extra.mjs), so the screens match
// learn/public/library/shots/steering. Everything is shot through the real UI. Nothing is copied
// from the library: every PNG here is taken by this script.
//
// Stubbed / injected (fictional demo data shown through the real UI; library/world.mjs openWorld):
//   /api/agent/runtimes            Claude Code and Codex installed and signed in (the model list)
//   /api/agent/usage-limit         a calm usage reading (the sidebar's "Session limit" pill)
//   /api/agent/sessions/*/context-usage   context percentages (needs a running CLI otherwise)
//   /api/agent/sessions (+ WS sessions_list)   live-only fields patched in (which sessions are working,
//                                  how many approvals/questions wait: no agent runs on the demo server)
//   WS permission_request          one injected event: the approval card (Bash, git push)
//   WS user_question               one injected event: the question card (AskUserQuestion)
//   WS session_updated             "processing", for the composer while the agent works
//   WS messages_loaded             the inline Edit diff is rebuilt from the stored tool call
//   /api/system/usage              the header's CPU/memory meter (lib.mjs steadyUsage)
// The demo cards are never answered (the bridge drops answers): nothing is sent to the server.
// Browser tab keys: ⇧⌘M is pressed here as its browser variant (with ⌥); no beat zooms on its label.
import { drawAllBoxes, parseArgs, readServer, shot, sleep } from '../../capture/lib.mjs'
import { press } from '../../capture/library/lib.mjs'
import { openWorld, openSession, S, settle, permissionRequest, question, setWorking } from '../../capture/library/world.mjs'

const SLUG = 'watching-and-steering'
const args = parseArgs()
const server = readServer(Number(args.port || 3403))
const only = typeof args.only === 'string' ? new Set(args.only.split(',')) : null
const want = name => !only || only.has(name)

const app = await openWorld(server)
const { browser, page } = app
const chat = () => page.locator('.messages-container').first()
const scrollChat = top => chat().evaluate((el, top) => { el.scrollTop = top }, top)
const centre = sel => page.locator(sel).first().evaluate(el => el.scrollIntoView({ block: 'center' }))
const gone = () => page.mouse.move(5, 5)

try {
  // ---- the chat while an agent works: what it reads, edits and runs --------------------------
  if (want('tools')) {
    await openSession(app, S.steer)
    await scrollChat(0)
    await sleep(500)
    await gone()
    const card = (name) => page.locator('.tool-use-wrapper, .tool-use').filter({ hasText: new RegExp(`^\\s*${name}\\b`) }).first()
    await shot(page, SLUG, 'tools', {
      boxes: {
        chat: '.messages-container', prompt: page.locator('[class*="prompt"]').filter({ hasText: 'Regulars should get a stamp' }).first(),
        readCard: page.locator('.tool-use.file-tool').first(), grepCard: page.locator('.tool-use').filter({ hasText: 'Grep' }).first(),
      },
    })
    await centre('.edit-diff-message')
    await sleep(500)
    await shot(page, SLUG, 'edit', { boxes: { chat: '.messages-container', editDiff: '.edit-diff-message' } })
    await centre('.tool-card.command-card')
    await sleep(500)
    await shot(page, SLUG, 'commands', {
      boxes: { chat: '.messages-container', bashCard: '.tool-card.command-card' },
    })
    await chat().evaluate(el => { el.scrollTop = el.scrollHeight })
    await centre('.messages-container pre')
    await sleep(400)
    await shot(page, SLUG, 'reply', {
      boxes: { chat: '.messages-container', code: page.locator('pre').first(), composer: '.input-area' },
    })
  }

  // ---- an approval card ------------------------------------------------------------------------
  if (want('permission')) {
    await openSession(app, S.refund)
    app.bridge.inject(permissionRequest())
    await page.waitForSelector('.permission-request')
    await sleep(700)
    await gone()
    await shot(page, SLUG, 'permission', {
      boxes: {
        card: '.permission-request', deny: '.permission-request .btn-deny', approveOnce: '.permission-request .btn-approve',
        allowSimilar: '.permission-request .btn-approve-similar', alwaysExact: '.permission-request .btn-approve-exact',
        pattern: '.permission-request .similar-preview', buttons: await (async () => { const a = await page.locator('.permission-request .btn-deny').boundingBox(), b = await page.locator('.permission-request .btn-approve').boundingBox(); return { x: Math.round(a.x), y: Math.round(a.y), w: Math.round(b.x + b.width - a.x), h: Math.round(a.height) } })(),
        needsRow: page.locator('.group-needs .session-item').first(), sidebar: '.sessions-sidebar',
      },
    })
  }

  // ---- a question ------------------------------------------------------------------------------
  if (want('question')) {
    await openSession(app, S.hours)
    app.bridge.inject(question())
    await page.waitForSelector('.question-modal')
    await sleep(800)
    await gone()
    const qboxes = {
      card: '.question-modal', options: '.question-modal .option-card >> nth=0', recommended: '[data-testid=recommended-badge]',
      countdown: '[data-testid=auto-pick-countdown]', keepWaiting: '[data-testid=auto-pick-hold]', actions: '.question-modal .modal-actions',
    }
    await shot(page, SLUG, 'question', { boxes: qboxes })
    await page.locator('.question-modal .option-card').nth(1).click()
    await sleep(300)
    await gone()
    await shot(page, SLUG, 'question-picked', {
      boxes: { card: '.question-modal', selected: '.question-modal .option-card.selected', submit: '.question-modal .modal-actions .btn-primary' },
    })
  }

  // ---- stopping a turn and steering --------------------------------------------------------------
  if (want('working')) {
    await openSession(app, S.teaOfMonth)
    setWorking(app, S.teaOfMonth)
    await page.waitForSelector('.processing-indicator')
    await sleep(900)
    await gone()
    const wboxes = {
      composer: '.input-area', stop: '.interrupt-btn', stopHint: page.locator('.input-area', { hasText: 'to stop' }).locator(':text("to stop")').first(),
      processing: '.processing-indicator', textarea: '.input-area textarea', status: '.status-pill',
    }
    await shot(page, SLUG, 'working', { boxes: wboxes })
    await page.locator('.input-area textarea').fill('Wait: pick the tea by ISO week, not by month.')
    await gone()
    await shot(page, SLUG, 'working-typing', { boxes: { ...wboxes, send: '.input-area .btn-send' } })
    await page.locator('.input-area textarea').fill('')
  }

  // ---- the right sidebar and the model switcher --------------------------------------------------
  if (want('model') || want('sidebar')) {
    await openSession(app, S.steer)
    await page.locator('.metrics-sidebar').waitFor()
    await sleep(600)
    await gone()
    await shot(page, SLUG, 'sidebar', {
      boxes: {
        sidebar: '.metrics-sidebar', duration: '.stat-tile >> nth=0', messages: '.stat-tile >> nth=1', context: '.stat-tile-button',
        modelRow: 'button.model-row', usage: '[data-testid=usage-limit-pill]', questions: '.question-policy-row', permissions: 'button.row-header[aria-controls="permissions-row"]',
      },
    })
    await page.locator('.stat-tile-button').first().click()
    await sleep(700)
    await page.locator('.context-usage-bar details.category-details summary').click().catch(() => {})
    await sleep(300)
    await gone()
    await shot(page, SLUG, 'context', {
      boxes: { sidebar: '.metrics-sidebar', ring: '.stat-tile-button', usageBar: '.context-usage-bar' },
    })
    await page.locator('.stat-tile-button').first().click().catch(() => {})
    await sleep(300)

    await press(page, 'Meta+Alt+Shift+KeyM', 900)               // ⇧⌘M (⌥⇧⌘M in a browser tab)
    await page.waitForSelector('dialog.model-dialog[open], dialog.model-dialog')
    await sleep(600)
    await gone()
    await shot(page, SLUG, 'model', {
      boxes: { note: page.locator('dialog.model-dialog :text("Applies to your next reply")').first(), dialog: 'dialog.model-dialog', search: '.model-search', firstResult: page.locator('.model-result').first() },
    })
    await page.locator('.model-search').pressSequentially('deep', { delay: 40 })
    await sleep(500)
    await gone()
    await shot(page, SLUG, 'model-search', {
      boxes: { dialog: 'dialog.model-dialog', search: '.model-search', firstResult: page.locator('.model-result').first() },
    })
    await page.locator('button[aria-label="Close model switcher"]').click()
  }
} finally {
  await browser.close()
}
const proofs = await drawAllBoxes(SLUG)
console.log(`\nProof sheets with the boxes drawn (look at them):\n  ${proofs.join('\n  ')}`)

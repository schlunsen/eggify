// Episode 3: steering a session (tool cards, approvals, questions, model switch, stop).
import { libShot } from '../lib.mjs'
import { openWorld, openSession, S, sleep, permissionRequest, question, setWorking } from '../world.mjs'

export default async function (server) {
  const app = await openWorld(server)
  const { browser, page, bridge } = app
  try {
    // --- a finished turn: tool cards, the Edit diff, a markdown reply
    await openSession(app, S.steer)
    const center = async sel => page.locator(sel).first().evaluate(el => el.scrollIntoView({ block: 'center' }))
    await center('.edit-diff-message')
    await sleep(500)
    await libShot(page, 'steering', 'chat-tool-cards', {
      shows: 'A session chat mid-conversation: Read and Grep cards, the agent\'s note, an Edit with its inline diff, Write cards and a Bash command',
      how: 'seeded messages; the inline Edit diff is rebuilt from the stored tool call (live-only data)',
      boxes: {
        chat: '.messages-container', editDiff: '.edit-diff-message', 'readCard?': page.locator('.tool-use.file-tool').first(),
        'bashCard?': page.locator('.tool-card.command-card').first(),
      },
    })
    await page.locator('.messages-container').first().evaluate(el => { el.scrollTop = el.scrollHeight })
    await center('.messages-container pre')
    await sleep(400)
    await libShot(page, 'steering', 'chat-reply', {
      shows: 'The agent\'s final reply in markdown: summary line, a table of changed files, a code block, a numbered list of open decisions',
      how: 'seeded',
      boxes: { 'table?': page.locator('table').first(), 'code?': page.locator('pre').first(), composer: '.input-area', rightSidebar: '.metrics-sidebar' },
    })

    // --- right sidebar and the model switcher
    await libShot(page, 'steering', 'right-sidebar', {
      shows: 'The session\'s right sidebar: status and tags, usage pill, duration / messages / context, model row with Switch, Questions policy, Skills, Git card, Permissions, Activity, Subagents',
      how: 'seeded; usage limit and context numbers are stubbed API answers',
      boxes: {
        sidebar: '.metrics-sidebar', modelRow: 'button.model-row', 'usage?': '[data-testid=usage-limit-pill]', 'context?': '.stat-tile-button',
        'questions?': '.question-policy-row', 'skills?': '[data-testid=skills-row]', git: 'section.git-card',
        permissions: 'button.row-header[aria-controls="permissions-row"]', headerModel: 'button.model-button', viewMode: '.view-mode-toggle',
      },
    })
    await page.locator('button.model-row').click()
    await page.waitForSelector('dialog.model-dialog[open], dialog.model-dialog')
    await sleep(600)
    await libShot(page, 'steering', 'model-switcher', {
      shows: 'Switch model dialog: every provider\'s models, the current one marked, search field', how: 'seeded (the providers list is the server\'s own)',
      boxes: { dialog: 'dialog.model-dialog', search: '.model-search', firstResult: page.locator('.model-result').first() },
    })
    await page.locator('.model-search').pressSequentially('deep', { delay: 30 })
    await sleep(400)
    await libShot(page, 'steering', 'model-switcher-search', {
      shows: 'Switch model dialog with "deep" typed: DeepSeek models of another provider', how: 'seeded',
      boxes: { dialog: 'dialog.model-dialog', search: '.model-search', firstResult: page.locator('.model-result').first() },
    })
    await page.locator('button[aria-label="Close model switcher"]').click()
    await page.waitForSelector('dialog.model-dialog', { state: 'hidden' }).catch(() => {})
    await page.locator('.stat-tile-button').first().click().catch(() => {})
    await sleep(600)
    await page.locator('.context-usage-bar details.category-details summary').click().catch(() => {})
    await sleep(300)
    await libShot(page, 'steering', 'context-usage', {
      shows: 'Context usage opened from the context ring: how full the model\'s context window is, by category', how: 'stubbed API (context-usage needs a running CLI)',
      boxes: { 'usageBar?': '.context-usage-bar', 'ring?': '.stat-tile-button' },
    })
    await page.locator('.stat-tile-button').first().click().catch(() => {})   // closed again (the app remembers it)

    // --- a permission request
    await openSession(app, S.refund)
    bridge.inject(permissionRequest())
    await page.waitForSelector('.permission-request')
    await sleep(600)
    await libShot(page, 'steering', 'permission-card', {
      shows: 'A permission request above the composer: the agent wants to run git push; Deny, Approve Once, Allow Similar (Bash(git push:*)) and Always Exact',
      how: 'seeded session; the card is an injected permission_request event (never answered)',
      boxes: {
        card: '.permission-request', deny: '.permission-request .btn-deny', approveOnce: '.permission-request .btn-approve',
        allowSimilar: '.permission-request .btn-approve-similar', alwaysExact: '.permission-request .btn-approve-exact', 'pattern?': '.permission-request .similar-preview',
        'sidebarRow?': page.locator('.group-needs .session-item').first(),
      },
    })

    // --- a question
    await openSession(app, S.hours)
    bridge.inject(question())
    await page.waitForSelector('.question-modal')
    await sleep(700)
    await libShot(page, 'steering', 'question-card', {
      shows: 'An agent\'s question (AskUserQuestion): three options, the recommended one marked, countdown to the auto-pick with Keep waiting, Later / Dismiss / Submit',
      how: 'seeded session; the card is an injected user_question event (never answered)',
      boxes: {
        card: '.question-modal', options: page.locator('.question-modal .option-card').first(), 'recommended?': '[data-testid=recommended-badge]',
        'countdown?': '[data-testid=auto-pick-countdown]', 'keepWaiting?': '[data-testid=auto-pick-hold]', actions: '.question-modal .modal-actions',
      },
    })
    await page.locator('.question-modal .option-card').nth(1).click()
    await sleep(300)
    await libShot(page, 'steering', 'question-card-selected', {
      shows: 'The same question with the second option chosen, ready to submit', how: 'seeded session; injected user_question event',
      boxes: { card: '.question-modal', selected: '.question-modal .option-card.selected', submit: '.question-modal .modal-actions .btn-primary' },
    })

    // --- working: Stop, the processing row, the working edge
    await openSession(app, S.teaOfMonth)
    setWorking(app, S.teaOfMonth)
    await sleep(900)
    await libShot(page, 'steering', 'working-composer', {
      shows: 'A session mid-turn: status Processing, the "Processing your message…" row, the composer\'s working edge and ⌘. to stop',
      how: 'seeded as working; session_updated (processing) event injected for the open chat',
      boxes: {
        composer: '.input-area', 'stopHint?': page.locator('.input-area', { hasText: 'to stop' }).locator(':text("to stop")').first(), 'stop?': '.interrupt-btn',
        'processing?': '.processing-indicator', 'workingEdge?': '[data-testid=working-edge]', status: '.status-pill',
      },
    })
  } finally { await browser.close() }
}

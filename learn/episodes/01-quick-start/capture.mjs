#!/usr/bin/env node
// Episode 1 "Quick start": takes the shots named in episode.json.
//
//   learn/capture/demo-server.sh --port 3401 --fresh      # in another terminal: no user yet
//   node learn/episodes/01-quick-start/capture.mjs        # [--port 3401] [--only install,setup,…] [--no-landing] [--clip]
//
// On a --fresh server it goes through the first run like a new user: the /setup page
// (shot, then the form is really submitted), Agents & logins and Providers on the still
// empty install, then the demo workspace is seeded and the session shots are taken.
// On a server that already has its user, `setup` is skipped (it cannot be shown any more).
//
// Stubbed answers (fictional demo data shown through the real UI, see README):
//   /api/agent/runtimes        which agent CLIs are installed and signed in
//   /api/agent/runtimes/codex/login   the "login is running" state of runtimes-login
//   /api/system/usage          the header's CPU/memory meter (lib.mjs steadyUsage)
import { drawAllBoxes, go, recordClip, openApp, parseArgs, readServer, rememberSignIn, serveLanding, settle, shot, signIn, sleep, stubJson } from '../../capture/lib.mjs'
import { PROJECTS, SESSIONS, makeRepos, seed } from '../../capture/seed.mjs'

const SLUG = 'quick-start'
const args = parseArgs()
const server = readServer(Number(args.port || 3401))
const only = typeof args.only === 'string' ? new Set(args.only.split(',')) : null
const want = name => !only || only.has(name)
const DEMO_KEY = 'sk-demo-0000-not-a-real-key-0000'   // typed into a password field; never a real key

// ---- fictional runtime states ------------------------------------------------
const now = () => new Date().toISOString()
const claudeRuntime = (ready) => ({
  id: 'claude', name: 'Claude Code', installed: true, path: '/opt/homebrew/bin/claude', on_path: true,
  version: '2.1.321', min_version: '2.0.0', recommended_version: '2.1.321', version_ok: true, meets_recommended: true,
  auth: ready ? { state: 'logged_in', method: 'subscription', detail: 'claude.ai', plan: 'max', account: 'pr•••@example.com' } : { state: 'not_logged_in' },
  ready, login_command: 'claude auth login',
  install: { commands: ['npm install -g @anthropic-ai/claude-code', 'brew install --cask claude-code'], docs_url: 'https://docs.claude.com/en/docs/claude-code/setup' },
  next_steps: ready ? [] : [{ kind: 'login', text: 'Log in with your Claude account', command: 'claude auth login' }, { kind: 'provider_key', text: 'Or add an API key in Settings → Providers' }],
  checked_at: now(),
})
const codexRuntime = (ready) => ({
  id: 'codex', name: 'OpenAI Codex', installed: true, path: '/opt/homebrew/bin/codex', on_path: true,
  version: '0.162.0', version_ok: true, meets_recommended: true,
  auth: ready ? { state: 'logged_in', method: 'subscription', detail: 'ChatGPT', plan: 'plus', account: 'pr•••@example.com' } : { state: 'not_logged_in' },
  ready, login_command: 'codex login',
  install: { commands: ['npm install -g @openai/codex', 'brew install codex'], docs_url: 'https://developers.openai.com/codex/cli' },
  next_steps: ready ? [] : [{ kind: 'login', text: 'Log in with your ChatGPT account', command: 'codex login' }, { kind: 'provider_key', text: 'Or add an API key in Settings → Providers' }],
  checked_at: now(),
})
const runtimesBody = (claudeReady, codexReady, logins = {}) => ({
  any_ready: claudeReady || codexReady, logins, runtimes: [claudeRuntime(claudeReady), codexRuntime(codexReady)],
})
const codexFlow = () => ({
  id: 'demo-login-1', runtime: 'codex', mode: 'chatgpt', state: 'waiting', browser_opened: true, accepts_code: false,
  auth_url: 'https://auth.openai.com/oauth/authorize', started_at: now(), expires_at: new Date(Date.now() + 600000).toISOString(),
})
async function stubRuntimes(page, claudeReady, codexReady) {
  await page.unroute('**/api/agent/runtimes*')
  await stubJson(page, '**/api/agent/runtimes*', runtimesBody(claudeReady, codexReady))
}

// ---- shots ---------------------------------------------------------------------

async function install(page) {
  if (args['no-landing']) return console.log('  (skipping install: --no-landing)')
  const landing = await serveLanding()
  try {
    await page.goto(landing.url, { waitUntil: 'networkidle' })
    await settle(page)
    await shot(page, SLUG, 'install-hero', { boxes: { headline: '.hero h1', licenseButton: '.hero a.btn.primary' } })
    await page.locator('#download').scrollIntoViewIfNeeded()
    await page.evaluate(() => {
      const r = document.querySelector('#download').getBoundingClientRect()
      window.scrollBy(0, r.top - 215)
    })
    await settle(page)
    await shot(page, SLUG, 'install', {
      boxes: { downloadCard: '#download', downloadButton: '#download .download-cta a.btn.primary', steps: '#install .steps ol', stepOpen: '#install .steps li >> nth=0' },
    })
  } finally { landing.stop() }
}

async function setup(page) {
  await go(page, server, '/setup')
  await page.locator('#username').waitFor({ timeout: 15000 })
  const boxes = { username: '#username', password: '#password', confirmPassword: '#confirmPassword', createButton: '.btn-submit', card: '.setup-card' }
  await shot(page, SLUG, 'setup-empty', { boxes })
  await page.fill('#username', server.user)
  await page.fill('#password', server.password)
  await page.fill('#confirmPassword', server.password)
  await page.evaluate(() => document.activeElement?.blur())
  await shot(page, SLUG, 'setup', { boxes })
  await page.click('.btn-submit')                       // the account is really created here
  await page.waitForURL(u => !u.pathname.startsWith('/setup'), { timeout: 15000 })
  await rememberSignIn(page.context(), server)
  await settle(page)
}

async function runtimes(page) {
  const card = id => `.rt-card:has(#rt-${id}-name)`
  const open = async () => {
    await go(page, server, '/settings#agent-logins')
    await page.locator('#agent-logins').scrollIntoViewIfNeeded().catch(() => {})
    await page.evaluate(() => {
      const el = document.querySelector('.rt-card')?.closest('.settings-section') || document.querySelector('#agent-logins')
      if (el) { const r = el.getBoundingClientRect(); window.scrollBy(0, r.top - 110) }
    })
    await settle(page)
  }
  const section = page.locator('.settings-section:has(.rt-card)')

  // Claude Code signed in, Codex installed but not signed in: "press Log in"
  await stubRuntimes(page, true, false)
  await open()
  await shot(page, SLUG, 'runtimes', {
    boxes: {
      section, claudeCard: card('claude'), codexCard: card('codex'),
      claudeBadge: `${card('claude')} .rt-badge`, codexBadge: `${card('codex')} .rt-badge`,
      loginButton: `${card('codex')} .rt-btn--primary`, recheckButton: page.getByRole('button', { name: /Re-check/ }),
    },
  })

  // After pressing "Log in with ChatGPT": the tool's own login runs in the browser
  await stubJson(page, '**/api/agent/runtimes/codex/login', { flow: codexFlow() })
  await page.locator(`${card('codex')} .rt-btn--primary`).click()
  await page.locator(`${card('codex')}[data-state="login"]`).waitFor()
  await sleep(300)
  await shot(page, SLUG, 'runtimes-login', {
    settle: false,
    boxes: { section, claudeCard: card('claude'), codexCard: card('codex'), codexBadge: `${card('codex')} .rt-badge`, waiting: `${card('codex')} .rt-wait` },
  })
  await page.unroute('**/api/agent/runtimes/codex/login')

  // Both signed in
  await stubRuntimes(page, true, true)
  await open()
  await shot(page, SLUG, 'runtimes-ready', { boxes: { section, claudeCard: card('claude'), codexCard: card('codex') } })

  // Nothing signed in yet (what a brand-new Mac with both tools installed shows)
  await stubRuntimes(page, false, false)
  await open()
  await shot(page, SLUG, 'runtimes-logged-out', {
    boxes: { section, claudeCard: card('claude'), codexCard: card('codex'), claudeLogin: `${card('claude')} .rt-btn--primary`, codexLogin: `${card('codex')} .rt-btn--primary` },
  })
  await stubRuntimes(page, true, false)
}

async function providers(page) {
  await go(page, server, '/providers')
  const row = page.locator('.provider-row').filter({ has: page.locator('.provider-name', { hasText: /^DeepSeek$/ }) })
  if (!(await row.getByRole('button', { name: /Set up/ }).count())) return console.log('  (skipping providers: DeepSeek already has a key on this server; use a --fresh one)')
  await shot(page, SLUG, 'providers-list', {
    boxes: { list: '.provider-list >> nth=0', deepseekRow: row, setupButton: row.locator('.action-btn') },
  })
  await row.locator('.action-btn').click()
  const modal = page.locator('.modal-content')
  await modal.waitFor()
  const key = modal.locator('input[type="password"]')
  await key.fill(DEMO_KEY)
  await page.evaluate(() => document.activeElement?.blur())
  await shot(page, SLUG, 'providers', {
    boxes: { dialog: modal, apiKey: key, baseUrl: modal.locator('.readonly-value'), model: modal.locator('select.form-select'), saveButton: modal.locator('.btn-primary') },
  })
  await modal.locator('.btn-primary').click()            // stores the dummy key in the demo database
  await modal.waitFor({ state: 'hidden', timeout: 10000 })
  await page.locator('.toast-success').waitFor({ state: 'hidden', timeout: 10000 }).catch(() => {})
  await shot(page, SLUG, 'providers-saved', { boxes: { deepseekRow: row } })
}

async function createSession(page) {
  await stubRuntimes(page, true, false)
  await go(page, server, `/agents?session=${SESSIONS.loginBug}`)   // the chat behind the dialog
  await page.locator('.message').first().waitFor()
  await go(page, server, `/agents?project=${PROJECTS.teashop.id}&new_session=1`)
  const modal = page.locator('.modal-content[role="dialog"]')
  await modal.waitFor()
  await page.locator('.quick-model').first().waitFor()
  // Pick the first Claude model on offer (the row shows model and provider)
  const choice = page.locator('.quick-model').filter({ hasText: 'claude-sonnet-5-5' }).first()
  await choice.click()
  await page.evaluate(() => document.activeElement?.blur())
  await page.mouse.move(5, 5)
  await shot(page, SLUG, 'create-session', {
    boxes: {
      dialog: modal, folder: '#working-directory', models: '.quick-models', model: choice.locator('.model-label strong'),
      provider: choice.locator('.model-label small'), modelRow: choice, allModels: '.browse-button',
      settingsToggle: '.settings-toggle', createButton: '.btn-create',
    },
  })
  await page.locator('.settings-toggle').click()
  await settle(page)
  await shot(page, SLUG, 'create-session-settings', { boxes: { dialog: modal, createButton: '.btn-create' } })
  await page.keyboard.press('Escape')
}

async function session(page) {
  await stubRuntimes(page, true, false)
  await go(page, server, `/agents?session=${SESSIONS.loginBug}`)
  await page.locator('.message').first().waitFor()
  await settle(page)
  // The chat opens at its end: the prompt stays in the bar on top, the tool cards and the reply are below it
  const scrollChat = top => page.evaluate((top) => {
    let el = document.querySelector('.message')?.parentElement
    while (el && !(el.scrollHeight > el.clientHeight + 4 && /auto|scroll/.test(getComputedStyle(el).overflowY))) el = el.parentElement
    if (el) el.scrollTop = top ? 0 : el.scrollHeight
  }, top)
  await scrollChat(false)
  await settle(page)
  const msg = text => page.locator('.message').filter({ hasText: text })
  const toolCard = name => page.locator('.tool-use-wrapper').filter({ has: page.locator('.tool-name', { hasText: new RegExp(`^${name}$`) }) }).first()
  const reply = msg('Empty and whitespace-only passwords').last()
  const boxes = {
    sidebar: '.sessions-sidebar', newSessionButton: page.getByRole('button', { name: /New Session/ }).first(),
    prompt: page.locator('[class*="prompt"]').filter({ hasText: 'The login endpoint accepts empty passwords' }).first(),
    editing: page.locator('.message').filter({ has: page.locator('.tool-card.command-card') }).first(),
    toolRead: toolCard('Read'), toolGrep: toolCard('Grep'), toolEdit: toolCard('Edit'), toolWrite: toolCard('Write'), toolBash: toolCard('Bash'),
    reply, composer: '.input-area textarea', gitPanel: '.git-status',
  }
  await shot(page, SLUG, 'session', { boxes })
  await page.locator('.input-area textarea').fill('Looks good. Commit it and open a pull request.')
  await shot(page, SLUG, 'session-typing', { boxes: { composer: '.input-area textarea', reply } })
  await page.locator('.input-area textarea').fill('')
  await scrollChat(true)
  await settle(page)
  await shot(page, SLUG, 'session-start', {
    boxes: { promptBubble: msg('The login endpoint accepts empty passwords').first(), toolRead: toolCard('Read'), toolGrep: toolCard('Grep'), composer: '.input-area textarea' },
  })
}

// The first session starts from the first-run card on the agents page ("Start with a project"),
// shown while the install has no projects and no sessions: Open an existing folder → Add project →
// "Start a session here". Runs before the demo workspace is seeded; the folders exist already
// (as they would on a new user's computer), and the project made here is removed again so the
// seed can add it under its fixed id.
async function firstProject(page) {
  await stubRuntimes(page, true, false)
  makeRepos(server.home)
  await go(page, server, '/agents')
  await page.waitForSelector('.fpn')
  await page.mouse.move(5, 5)
  const opt = n => page.locator('.fpn__option').nth(n)
  await shot(page, SLUG, 'first-project', {
    boxes: { card: '.fpn', existing: opt(0), clone: opt(1), empty: opt(2), options: '.fpn__options', later: '.fpn__later', newSessionButton: page.getByRole('button', { name: /New Session/ }).first() },
  })
  await opt(0).click()
  const folder = page.locator('.np-dialog .np-combo input')
  await folder.waitFor()
  await folder.fill('~/projects/teashop-api')
  await page.locator('.np-title').click()               // closes the suggestions
  await sleep(1200)                                     // the folder is inspected, the name filled in
  await shot(page, SLUG, 'new-project-folder', {
    boxes: { dialog: '.np-dialog', folder: '.np-combo', submit: '.np-dialog .np-btn--primary' },
  })
  await page.locator('.np-dialog .np-btn--primary').click()
  await page.waitForSelector('.np-done')
  await page.mouse.move(5, 5)
  await shot(page, SLUG, 'new-project-done', {
    boxes: { dialog: '.np-dialog', startSession: '.np-done__actions .np-btn--primary' },
  })
  // The Create Session dialog that follows is shot by createSession() on the seeded workspace:
  // a brand-new install has no most-used models to list yet.
  const list = await (await page.request.get(`${server.url}/api/projects`)).json()
  for (const p of list.projects || []) await page.request.delete(`${server.url}/api/projects/${p.id}`)
}

// ---- run -------------------------------------------------------------------------

const { browser, page } = await openApp(server, { signIn: false })
try {
  if (want('install')) await install(page)
  const status = await (await fetch(`${server.url}/api/auth/status`)).json()
  if (status.needs_setup) {
    if (want('setup')) await setup(page)
    else { await seed(server); await signIn(page.context(), server) }   // creates the user without the form
  } else {
    if (only?.has('setup')) console.log('  (setup cannot be shot: the user already exists)')
    await signIn(page.context(), server)
  }
  // The empty install first, as a new user sees it…
  if (want('runtimes')) await runtimes(page)
  if (want('providers')) await providers(page)
  if (want('first-project')) await firstProject(page)
  // …then the demo workspace
  await seed(server)
  if (want('create-session')) await createSession(page)
  if (want('session')) await session(page)
} finally {
  await browser.close()
}
if (args.clip) {
  // A short test clip (not used by episode 1): scrolling through the finished session
  await recordClip(server, SLUG, 'session-scroll', async (page, { mark }) => {
    await page.goto(`${server.url}/agents?session=${SESSIONS.loginBug}`)
    await page.locator('.message').first().waitFor()
    await settle(page)
    mark()
    await sleep(600)
    await page.locator('.message').last().hover()
    for (let i = 0; i < 12; i++) { await page.mouse.wheel(0, -60); await sleep(90) }
    await sleep(500)
    for (let i = 0; i < 12; i++) { await page.mouse.wheel(0, 60); await sleep(90) }
    await sleep(700)
  })
}
const proofs = await drawAllBoxes(SLUG)
console.log(`\nProof sheets with the boxes drawn (look at them):\n  ${proofs.join('\n  ')}`)

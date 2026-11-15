// The library's fictional "live" state: what a real install only has while agents run
// (subagents, waiting cards, loops, usage), given to the real UI as stubbed API answers and
// injected WebSocket events. Sessions, messages, projects, repositories, skills and memories are
// real rows and files on the demo server (../seed.mjs + seed-extra.mjs).
import { join } from 'node:path'
import { minsAgo, openLibraryApp, patchSessions, sleep, stubJson, go, settle } from './lib.mjs'
import { SESSIONS as S, TEAM, repoPaths } from './seed-extra.mjs'

export { S }
const isoAgo = m => minsAgo(m).toISOString()
const isoIn = m => new Date(Date.now() + m * 60000).toISOString()

// ---------------------------------------------------------------- runtimes, usage, context

const runtime = (id, name, version, plan, login) => ({
  id, name, installed: true, path: `/opt/homebrew/bin/${id}`, on_path: true, version, version_ok: true, meets_recommended: true,
  auth: { state: 'logged_in', method: 'subscription', detail: id === 'claude' ? 'claude.ai' : 'ChatGPT', plan, account: 'pr•••@example.com' },
  ready: true, login_command: login, install: { commands: [], docs_url: '' }, next_steps: [], checked_at: new Date().toISOString(),
})
export const runtimesBody = () => ({
  any_ready: true, logins: {},
  runtimes: [runtime('claude', 'Claude Code', '2.1.321', 'max', 'claude auth login'), runtime('codex', 'OpenAI Codex', '0.162.0', 'plus', 'codex login')],
})
export const usageBody = () => ({
  limit: { status: 'allowed', limit_type: 'five_hour', utilization: 0.38, resets_at: isoIn(142), observed_at: isoAgo(1) },
})
const CONTEXT = { [S.steer]: 31, [S.postgres]: 54, [S.team]: 47, [S.refund]: 22, [S.hours]: 9, [S.loop]: 38, [S.builder]: 28, [S.reviewer]: 12, [S.trust]: 14, [S.proposer]: 18 }
function contextBody(url) {
  const id = url.match(/sessions\/([0-9a-f-]{36})\/context-usage/)?.[1]
  const pct = CONTEXT[id] ?? 17
  const window = 200000, total = Math.round(window * pct / 100)
  const part = (name, share) => ({ name, tokens: Math.round(total * share), percentage: +(pct * share).toFixed(1) })
  return {
    model: 'claude-sonnet-5-5', total_tokens: total, context_window: window, percentage: pct,
    categories: [part('System prompt', 0.08), part('Tools', 0.17), part('Memory files', 0.05), part('Messages', 0.70),
      { name: 'Free space', tokens: window - total, percentage: 100 - pct }],
  }
}

// ---------------------------------------------------------------- subagents (of the "Move orders to Postgres" session)

function lines(repo) {
  const cd = `cd ${repo} && `
  const schema = [
    'I\'ll write the schema for orders and loyalty cards, add the migration, and open a pull request.',
    `TOOL:Read: ${repo}/internal/orders/orders.go`, 'RESULT_START', 'package orders', 'RESULT_END',
    `TOOL:Write: ${repo}/migrations/0003_orders.sql`, 'RESULT_START', `File created successfully at: ${repo}/migrations/0003_orders.sql`, 'RESULT_END',
    `TOOL:Write: ${repo}/migrations/0003_orders.down.sql`, 'RESULT_START', `File created successfully at: ${repo}/migrations/0003_orders.down.sql`, 'RESULT_END',
    `TOOL:Edit: ${repo}/README.md`, 'RESULT_START', `The file ${repo}/README.md has been updated successfully.`, 'RESULT_END',
    `TOOL:Bash: ${cd}git checkout -b feature/orders-schema && git add -A && git commit -m "Orders and loyalty cards: Postgres schema"`, 'RESULT_START', '[feature/orders-schema 4c81e2f] Orders and loyalty cards: Postgres schema', ' 3 files changed, 58 insertions(+)', 'RESULT_END',
    `TOOL:Bash: ${cd}git push -u origin feature/orders-schema`, 'RESULT_START', ' * [new branch]      feature/orders-schema -> feature/orders-schema', 'RESULT_END',
    `TOOL:Bash: ${cd}gh pr create --title "Orders and loyalty cards: Postgres schema" --body "Tables orders and cards, with a down migration."`, 'RESULT_START', 'https://github.com/teashop-example/teashop-api/pull/41', 'RESULT_END',
  ]
  const schemaReport = 'Opened **PR #41** with the Postgres schema: tables `orders` and `cards`, prices in pence, a foreign key from orders to cards, and a down migration. `migrations/0003_orders.sql` applies cleanly to an empty database.'
  const store = [
    'The store goes behind the existing `orders.Store` interface, so the handlers do not change.',
    `TOOL:Read: ${repo}/internal/orders/orders.go`, 'RESULT_START', 'package orders', 'RESULT_END',
    `TOOL:Grep: Store`, 'RESULT_START', 'internal/orders/orders.go:14:type Store interface {', 'RESULT_END',
    `TOOL:Write: ${repo}/internal/orders/pgstore.go`, 'RESULT_START', `File created successfully at: ${repo}/internal/orders/pgstore.go`, 'RESULT_END',
    `TOOL:Write: ${repo}/internal/orders/pgstore_test.go`, 'RESULT_START', `File created successfully at: ${repo}/internal/orders/pgstore_test.go`, 'RESULT_END',
    `TOOL:Bash: ${cd}go test ./internal/orders/...`, 'RESULT_START', '--- FAIL: TestPgStoreListByCustomer (0.02s)', '    pgstore_test.go:48: got 3 orders, want 2', 'FAIL', 'RESULT_END',
    'The list query has no `WHERE customer = $1`. Fixing the query, then running the tests again.',
    `TOOL:Edit: ${repo}/internal/orders/pgstore.go`, 'RESULT_START', `The file ${repo}/internal/orders/pgstore.go has been updated successfully.`, 'RESULT_END',
    `TOOL:Bash: ${cd}go test -race ./internal/orders/...`,
  ]
  const audit = [
    'Reading every handler that touches orders.',
    `TOOL:Grep: orders\\.`, 'RESULT_START', 'cmd/teashop/main.go:18', 'cmd/teashop/orders.go:9', 'cmd/teashop/orders.go:31', 'RESULT_END',
    `TOOL:Read: ${repo}/cmd/teashop/orders.go`, 'RESULT_START', 'package main', 'RESULT_END',
    `TOOL:Read: ${repo}/cmd/teashop/main.go`,
  ]
  const explore = [
    `TOOL:Grep: orders\\.`, 'RESULT_START', 'cmd/teashop/main.go:18', 'cmd/teashop/orders.go:9', 'RESULT_END',
    `TOOL:Read: ${repo}/cmd/teashop/orders.go`, 'RESULT_START', 'package main', 'RESULT_END',
    `TOOL:Glob: internal/orders/*.go`, 'RESULT_START', 'internal/orders/orders.go', 'RESULT_END',
  ]
  const exploreReport = 'Orders are created in `cmd/teashop/orders.go` (`POST /orders`) and listed in the same file (`GET /orders`); both go through `orders.Store`. Nothing else reads the map directly.'
  const fixtures = [
    `TOOL:Write: ${repo}/internal/orders/testdata/orders.sql`, 'RESULT_START', `File created successfully at: ${repo}/internal/orders/testdata/orders.sql`, 'RESULT_END',
    `TOOL:Bash: ${cd}psql -f internal/orders/testdata/orders.sql teashop_test`, 'RESULT_START', 'psql: error: connection to server on socket "/tmp/.s.PGSQL.5432" failed: No such file or directory', 'RESULT_END',
  ]
  return { schema, schemaReport, store, audit, explore, exploreReport, fixtures }
}

/** The subagents of the Postgres session: three working, two finished, one failed, with times near now. */
export function subagents(server) {
  // A Mac user's home in the agents' own words, so the UI shortens it to ~/projects/teashop-api
  const repo = '/Users/praline/projects/teashop-api'
  const L = lines(repo)
  const now = Date.now()
  const times = (n, startMin, endMin) => Array.from({ length: n }, (_, i) => Math.round(now - startMin * 60000 + (startMin - endMin) * 60000 * (i / Math.max(1, n - 1))))
  const agent = (id, type, description, status, startMin, endMin, out, extra = {}) => ({
    agent_id: id, parent_session_id: S.postgres, subagent_type: type, description, status,
    progress: status === 'running' ? 0.5 : 1, last_output: extra.report || out[out.length - 1], output_lines: out.length,
    created_at: isoAgo(startMin), updated_at: isoAgo(endMin), ...(status === 'running' ? {} : { completed_at: isoAgo(endMin) }),
    parent_agent_id: extra.parent || '', background: true, recent_output: out, recent_output_at: times(out.length, startMin, endMin),
    task_id: '', prompt: extra.prompt || '', model: extra.model || 'claude-sonnet-5-5', model_source: extra.source || 'actual',
    interrupted: false, limit_paused: false, ...(extra.error ? { error_message: extra.error } : {}),
  })
  return [
    agent('toolu_demo_sub_store', 'general-purpose', 'Implement the Postgres order store', 'running', 9, 0.2, L.store,
      { prompt: 'Implement orders.Store on pgx behind the existing interface, with tests against a temporary database.' }),
    agent('toolu_demo_sub_audit', 'code-reviewer', 'Audit handlers that touch orders', 'running', 8.5, 0.4, L.audit,
      { prompt: 'Read every handler that touches orders and list what breaks once storage is asynchronous and can fail.' }),
    agent('toolu_demo_sub_fixtures', 'general-purpose', 'Load test fixtures into Postgres', 'running', 3, 0.6, [`TOOL:Read: ${repo}/internal/orders/pgstore_test.go`, 'RESULT_START', 'package orders', 'RESULT_END', ...L.fixtures.slice(0, 1)],
      { parent: 'toolu_demo_sub_store', model: 'claude-haiku-5-5', prompt: 'Write SQL fixtures for the order store tests: two customers, five orders.' }),
    agent('toolu_demo_sub_schema', 'general-purpose', 'Write the orders schema and migration', 'completed', 9.5, 1.2, [...L.schema, L.schemaReport],
      { report: L.schemaReport, prompt: 'Write the Postgres schema for orders and loyalty cards and a migration under migrations/. Open a pull request.' }),
    agent('toolu_demo_sub_explore', 'Explore', 'Explore how orders are read and written', 'completed', 14, 11.5, [...L.explore, L.exploreReport],
      { report: L.exploreReport, model: 'claude-haiku-5-5', prompt: 'Find every place that creates, reads or lists orders in this repository and report the call sites.' }),
    agent('toolu_demo_sub_seed', 'general-purpose', 'Seed a local Postgres for the tests', 'failed', 7, 2.5, L.fixtures,
      { error: 'psql: connection to server on socket "/tmp/.s.PGSQL.5432" failed: No such file or directory', model: 'claude-haiku-5-5', prompt: 'Start a local Postgres and load the test fixtures.' }),
  ]
}

// ---------------------------------------------------------------- waiting cards

export const permissionRequest = () => ({
  type: 'permission_request', session_id: S.refund, permission_id: 'perm-demo-refund-1', tool: 'Bash', action: 'execute',
  description: 'Run: git push -u origin feature/refund-form', details: { command: 'git push -u origin feature/refund-form', description: 'Push the branch' },
})
export const question = () => ({
  type: 'user_question', session_id: S.hours, question_id: 'q-demo-hours-1', header: 'Lunch closure',
  question: 'How should the table show the lunch closure?',
  options: [
    { label: 'Two rows per day (Recommended)', description: 'Morning 09:00–12:30 and afternoon 13:30–17:30, so the gap is plain to see' },
    { label: 'One row with a note', description: '09:00–17:30 with "closed 12:30–13:30" in a third column' },
    { label: 'Leave lunch out', description: 'Only opening and closing time; lunch stays in the text above the table' },
  ],
  multi_select: false, question_index: 1, question_total: 1, recommended: [0],
  auto_answer_at: isoIn(1.6), held: false,
})
export const workerProposal = server => {
  const repo = join(server.home, 'projects', 'cheese-shop')
  return {
    type: 'user_question', session_id: S.proposer, question_id: 'q-demo-worker-1', header: 'Worker session',
    question: 'Start a build worker for the rounding fix?',
    options: [{ label: 'Start', description: 'Create the session and send it the task' }, { label: 'Deny', description: 'Do not start it' }],
    multi_select: false, question_index: 1, question_total: 1, requires_user: true, kind: 'worker_session', recommended: [], auto_answer_at: null, held: false,
    proposal: {
      task: 'Finish the tests for cheese/prices.py on fix/stilton-rounding: prices round half up to the penny, like the till. Commit on your branch.',
      mode: 'build', working_directory: repo, model: 'deepseek-chat', provider: 'deepseek', runtime: 'claude-sdk',
      context: 'The till rounds half up; Python\'s round() rounds half to even, which is where the penny goes missing.',
      sandboxed: true, read_only: false, parent_session_id: S.proposer,
      worktree_path: join(repo, '.worktrees/9c0e5b77-worker-stilton-rounding-tests'), branch: 'worker/9c0e5b77-stilton-rounding-tests', base_branch: 'fix/stilton-rounding',
    },
  }
}
export const loopProposal = server => ({
  type: 'user_question', session_id: S.proposer, question_id: 'q-demo-loop-1', header: 'Loop session',
  question: 'Start a loop session?',
  options: [{ label: 'Start', description: 'Create the session and start the loop' }, { label: 'Edit first', description: 'Open Create Session with these values' }, { label: 'Deny', description: 'Do not start it' }],
  multi_select: false, question_index: 1, question_total: 1, requires_user: true, kind: 'loop_session', recommended: [], auto_answer_at: null, held: false,
  proposal: {
    goal: 'Every price in cheese/ is computed in whole pence and `uv run pytest` passes',
    verify_command: 'uv run pytest -q', working_directory: join(server.home, 'projects', 'cheese-shop'),
    max_iterations: 15, timeout_minutes: 45, model: 'claude-sonnet-5-5', provider: 'claude',
    context: 'Start with cheese/prices.py; the importer still multiplies floats.',
    yolo: true, sandboxed: false, parent_session_id: S.proposer,
  },
})

// ---------------------------------------------------------------- loops

const loopEvent = (type, iteration, extra = {}) => ({
  type, session_id: S.loop, iteration, max_iterations: 20,
  goal: 'Plank prices round to the nearest penny and `cargo test` passes', verify_command: 'cargo test --quiet', ...extra,
})
export const loopEvents = () => [
  loopEvent('loop_started', 1),
  loopEvent('loop_iteration', 1, { verify_passed: false, verify_output: 'error[E0583]: file not found for module `prices`' }),
  loopEvent('loop_iteration', 2, { verify_passed: false, verify_output: 'test prices::tests::rounds_to_the_nearest_penny ... FAILED\n  left: 299\n right: 300' }),
  loopEvent('loop_iteration', 3),
]
export const loopState = () => loopEvent('loop_iteration', 3, { verify_passed: false, verify_output: 'test prices::tests::rounds_to_the_nearest_penny ... FAILED\n  left: 299\n right: 300' })
const cronJob = () => ({
  id: 'c7d2a1', cron: '0 2 * * *', schedule: 'Every day at 02:00', prompt: 'Check every link in handbook/ and api/ and list the dead ones.',
  recurring: true, created_at: isoAgo(3 * 1440), expires_at: isoIn(4 * 1440), next_at: nextTwoAM(), last_run_at: lastTwoAM(),
})
function nextTwoAM() { const d = new Date(); d.setHours(2, 0, 0, 0); if (d <= new Date()) d.setDate(d.getDate() + 1); return d.toISOString() }
function lastTwoAM() { const d = new Date(nextTwoAM()); d.setDate(d.getDate() - 1); return d.toISOString() }

/** In-memory-only fields of sessions that would be live on a real install. */
export function sessionPatches() {
  const fresh = (m = 0) => ({ updated_at: isoAgo(m) })
  return {
    [S.postgres]: { status: 'processing', subagents_running: 3, subagents_total: 6, ...fresh(0.1) },
    [S.refund]: { status: 'processing', pending_permissions: 1, ...fresh(0.5) },
    [S.hours]: { status: 'processing', pending_questions: 1, ...fresh(1) },
    [S.reviewer]: { status: 'processing', ...fresh(0.2) },
    [S.proposer]: { status: 'processing', ...fresh(0.7) },
    [S.loop]: { status: 'processing', loop_state: loopState(), ...fresh(0.3) },
    [S.teaOfMonth]: { status: 'processing', ...fresh(0.4) },
    [S.cron]: s => ({ scheduled_jobs: [cronJob()], scheduled_loop: { next_at: nextTwoAM(), delay_seconds: 0, reason: 'Every day at 02:00', scheduled_at: isoAgo(34), kind: 'cron' } }),
  }
}

// ---------------------------------------------------------------- the standard context

/**
 * Open the app the way every library shot starts: signed in, runtimes "ready", a calm usage
 * reading, context numbers, live-looking sessions and (by default) the Postgres session's subagents.
 */
export async function openWorld(server, opts = {}) {
  const app = await openLibraryApp(server, opts)
  const { context, bridge } = app
  await stubJson(context, '**/api/agent/runtimes*', runtimesBody)
  await stubJson(context, '**/api/agent/usage-limit*', usageBody)
  await stubJson(context, '**/api/agent/sessions/*/context-usage', req => contextBody(req.url()))
  await patchSessions(context, bridge, sessionPatches())
  if (opts.subagents !== false) await stubSubagents(context, bridge, server)
  await context.route(/\/api\/agent\/sessions\/[0-9a-f-]{36}\/workers/, route => {
    const id = route.request().url().match(/sessions\/([0-9a-f-]{36})/)[1]
    route.fulfill({ json: { workers: id === S.team ? [workerLines(server)] : [] } })
  })
  // The demo cards are never answered for real: an answer would only make the server say "not waiting".
  bridge.dropOut(d => ['permission_response', 'user_question_response', 'user_question_dismiss', 'user_question_hold', 'add_always_allow_rule', 'interrupt_session', 'toggle_yolo_mode'].includes(d.type))
  // On subscribe the server lists the questions it knows are waiting (none): keep the demo ones.
  bridge.mapIn(d => (d.type === 'user_questions_sync' ? null : d))
  // The inline diff of an Edit is live data (edit_tool_data); rebuild it from the stored tool call.
  bridge.mapIn(d => {
    if (d.type === 'messages_loaded' && Array.isArray(d.messages)) {
      for (const m of d.messages) {
        const edit = (m.tool_uses || []).find(t => t.name === 'Edit' && t.input?.old_string !== undefined)
        if (edit && !m.edit_tool_data) {
          m.edit_tool_data = { filePath: edit.input.file_path, oldString: edit.input.old_string, newString: edit.input.new_string, replaceAll: false, status: 'completed' }
        }
      }
    }
    return d
  })
  const world = { ...app, server }
  if (opts.preload !== false) await preload(world)
  return world
}

/**
 * Sessions seeded as "processing" are not loaded by the server until someone opens them (the page
 * only re-opens idle ones), and its per-session endpoints (git status, workspace trust) answer 404
 * meanwhile. Open them once the way the page opens a stored session: create_session with their id.
 */
const PRELOAD = [S.postgres, S.refund, S.hours, S.reviewer, S.proposer, S.loop, S.teaOfMonth]
export async function preload(app) {
  const { page, server, bridge } = app
  let quiet = true
  bridge.mapIn(d => (quiet && (d.type === 'session_created' || d.type === 'session_updated') && PRELOAD.includes(d.session_id || d.session?.id) ? null : d))
  await page.goto(`${server.url}/settings`, { waitUntil: 'domcontentloaded' })
  await bridge.waitFor('auth_success', { timeout: 15000 })
  for (const id of PRELOAD) bridge.toServer({ type: 'create_session', session_id: id, options: {} })
  await sleep(1500)
  quiet = false
}

/** What the team's working worker is doing (the parent's page gets this as worker_activity events). */
export function workerLines(server) {
  const ww = join(server.home, 'projects', 'cheese-shop', TEAM.workerDir)
  const now = Date.now()
  const out = [
    'TOOL:Bash: git diff team/low-stock-emails...HEAD --stat', 'RESULT_START', ' cheese/alerts.py      | 24 ++++++++++++', ' tests/test_alerts.py  | 19 +++++++++', 'RESULT_END',
    'TOOL:Bash: git status --short && git diff', 'RESULT_START', ' M cheese/alerts.py', 'A  cheese/sent_log.py', 'RESULT_END',
    `TOOL:Read: ${ww}/cheese/alerts.py`, 'RESULT_START', 'from email.message import EmailMessage', 'RESULT_END',
    `TOOL:Read: ${ww}/cheese/sent_log.py`, 'RESULT_START', 'from datetime import date', 'RESULT_END',
    'The filter is in place, but _already_sent_today() returns an empty set, so nothing is skipped yet.',
    'TOOL:Bash: uv run pytest -q tests/test_alerts.py',
  ]
  return { session_id: S.reviewer, lines: out, lines_at: out.map((_, i) => now - (out.length - i) * 9000) }
}

export async function stubSubagents(context, bridge, server) {
  const all = () => subagents(server)
  await context.route(/\/api\/agent\/sessions\/[0-9a-f-]{36}\/subagents/, route => {
    const id = route.request().url().match(/sessions\/([0-9a-f-]{36})/)[1]
    const list = id === S.postgres ? all() : []
    route.fulfill({ json: { subagents: list, total: list.length, running: list.filter(a => a.status === 'running').length } })
  })
  await context.route(/\/api\/agent\/subagents(\?.*)?$/, route => {
    const list = all().map(a => ({ ...a, recent_output: a.recent_output.slice(-20), recent_output_at: a.recent_output_at.slice(-20) }))
    route.fulfill({ json: { subagents: list, total: list.length, running: 3, truncated: false, since: isoAgo(1440) } })
  })
  bridge.mapIn(d => {
    if (d.type === 'background_agents_list' && d.session_id === S.postgres) d.agents = all()
    return d
  })
}

/** Open /agents on one session and wait until its messages are on screen. */
export async function openSession(app, sessionId, { wait = 900 } = {}) {
  const { page, server, bridge } = app
  const since = Date.now()
  await page.goto(`${server.url}/agents?session=${sessionId}`, { waitUntil: 'domcontentloaded' })
  await bridge.waitFor('messages_loaded', { sessionId, since, timeout: 12000 })
  await settle(page, { timeout: 3000 })
  await sleep(wait)
}

/**
 * The session list drops some options the right sidebar reads (sandboxed, mode, loop); the server
 * sends them in session_updated during a session's life. Send the server's own session that way.
 */
export async function refreshSession(app, sessionId, extra = {}) {
  const session = await app.page.evaluate(async id => {
    const r = await fetch('/api/agent/sessions?status=all')
    return (await r.json()).sessions.find(s => s.id === id)
  }, sessionId)
  if (session) app.bridge.inject({ type: 'session_updated', session_id: sessionId, session: { ...session, ...extra } })
  await sleep(400)
}

/** Make the open session look mid-turn (Stop, working edge): what session_updated does during a live turn. */
export function setWorking(app, sessionId, working = true) {
  app.bridge.inject({ type: 'session_updated', session_id: sessionId, session: { id: sessionId, status: working ? 'processing' : 'idle' } })
}

export { go, settle, sleep }

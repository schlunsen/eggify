#!/usr/bin/env node
// Seeds a demo server (demo-server.sh) with a fictional workspace: the user Mr. Praline,
// three small git repositories in his home folder, the projects that point at them and a
// handful of agent sessions with conversations, tool calls, tags and summaries.
//
//   node learn/capture/seed.mjs --port 3401
//
// Everything here is invented (docs/MEDIA_PROVENANCE.md): no real names, paths, e-mails or keys.
// Projects and tags go through the REST API; sessions and messages are written to the
// server's SQLite file, as scripts/ios-e2e-server.sh does. Running it twice is harmless.
import { DatabaseSync } from 'node:sqlite'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, realpathSync, writeFileSync, appendFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { randomUUID } from 'node:crypto'
import { pathToFileURL } from 'node:url'
import { parseArgs, readServer, sessionToken } from './lib.mjs'

/** Fixed ids, so capture scripts can open a session by URL. */
export const SESSIONS = {
  loginBug: '11111111-2222-4333-8444-555555555501',     // "Fix the empty-password login" (teashop-api) — the one to open
  priceCards: '11111111-2222-4333-8444-555555555502',   // "Price cards for the shop page" (parrot-web)
  teaOfMonth: '11111111-2222-4333-8444-555555555503',   // "Tea of the month endpoint" (teashop-api), shown as working
  apiReference: '11111111-2222-4333-8444-555555555504', // "Write the API reference" (circus-docs), DeepSeek
  copyReview: '11111111-2222-4333-8444-555555555505',   // "Review the returns page copy" (parrot-web), Codex
  goUpgrade: '11111111-2222-4333-8444-555555555506',    // "Upgrade to Go 1.26" (teashop-api), ended
}
export const PROJECTS = {
  teashop: { id: 'aaaaaaaa-0000-4000-8000-000000000001', name: 'teashop-api', color: '#b4532a', description: 'The tea shop\'s ordering API (Go)' },
  parrot: { id: 'aaaaaaaa-0000-4000-8000-000000000002', name: 'parrot-web', color: '#2f7d6b', description: 'The Norwegian Blue pet shop website' },
  docs: { id: 'aaaaaaaa-0000-4000-8000-000000000003', name: 'circus-docs', color: '#6a4c93', description: 'Handbook and API reference for the shop' },
}

// ---------------------------------------------------------------- git repos

function git(cwd, home, args, date) {
  const env = {
    PATH: '/usr/bin:/bin:/opt/homebrew/bin', HOME: home, LANG: 'en_US.UTF-8',
    GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null',       // never the real user's git identity
    GIT_AUTHOR_NAME: 'Mr. Praline', GIT_AUTHOR_EMAIL: 'praline@example.com',
    GIT_COMMITTER_NAME: 'Mr. Praline', GIT_COMMITTER_EMAIL: 'praline@example.com',
  }
  if (date) { env.GIT_AUTHOR_DATE = date; env.GIT_COMMITTER_DATE = date }
  return execFileSync('git', args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] }).toString()
}
function put(root, rel, text) {
  const file = join(root, rel)
  mkdirSync(dirname(file), { recursive: true })
  writeFileSync(file, text)
}
const ago = (min) => new Date(Date.now() - min * 60000)
const iso = (d) => d.toISOString()
const DAY = 1440

const LOGIN_GO = `package auth

import "errors"

var ErrBadCredentials = errors.New("wrong username or password")

// Login checks a customer's password and returns a session token.
func (s *Service) Login(username string, pw *string) (string, error) {
	if pw == nil {
		return "", ErrBadCredentials
	}
	user, err := s.users.Find(username)
	if err != nil {
		return "", ErrBadCredentials
	}
	if !s.hasher.Matches(user.PasswordHash, *pw) {
		return "", ErrBadCredentials
	}
	return s.tokens.Issue(user.ID)
}
`
const LOGIN_GO_FIXED = LOGIN_GO
  .replace('var ErrBadCredentials = errors.New("wrong username or password")',
    'var (\n\tErrBadCredentials = errors.New("wrong username or password")\n\tErrEmptyPassword   = errors.New("password must not be empty")\n)')
  .replace('\tif pw == nil {\n\t\treturn "", ErrBadCredentials\n\t}',
    '\tif pw == nil || strings.TrimSpace(*pw) == "" {\n\t\treturn "", ErrEmptyPassword\n\t}')
  .replace('import "errors"', 'import (\n\t"errors"\n\t"strings"\n)')
const LOGIN_TEST_GO = `package auth

import "testing"

func TestLoginEmptyPassword(t *testing.T) {
	svc := newTestService(t)
	for _, pw := range []string{"", "   "} {
		if _, err := svc.Login("praline", &pw); err != ErrEmptyPassword {
			t.Fatalf("Login(%q) = %v, want ErrEmptyPassword", pw, err)
		}
	}
}
`

export function makeRepos(home) {
  const base = join(home, 'projects')
  mkdirSync(base, { recursive: true })
  const repos = {}

  // teashop-api: on a fix branch with uncommitted work (the open session's edit)
  let r = repos.teashop = join(base, 'teashop-api')
  if (!existsSync(join(r, '.git'))) {
    mkdirSync(r, { recursive: true })
    git(r, home, ['init', '-q', '-b', 'main'])
    put(r, 'go.mod', 'module example.com/teashop\n\ngo 1.26\n')
    put(r, 'README.md', '# teashop-api\n\nThe ordering API of a small tea shop: menu, orders and customer logins.\n\n```sh\ngo run ./cmd/teashop\n```\n')
    put(r, 'cmd/teashop/main.go', 'package main\n\nimport (\n\t"log"\n\t"net/http"\n\n\t"example.com/teashop/internal/menu"\n)\n\nfunc main() {\n\thttp.HandleFunc("/menu", menu.Handler)\n\tlog.Fatal(http.ListenAndServe(":8080", nil))\n}\n')
    put(r, 'internal/menu/menu.go', 'package menu\n\nimport (\n\t"encoding/json"\n\t"net/http"\n)\n\ntype Tea struct {\n\tName  string `json:"name"`\n\tPence int    `json:"pence"`\n}\n\nvar teas = []Tea{{"Earl Grey", 320}, {"Lapsang Souchong", 380}, {"Darjeeling", 350}}\n\nfunc Handler(w http.ResponseWriter, _ *http.Request) {\n\t_ = json.NewEncoder(w).Encode(teas)\n}\n')
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'Menu endpoint and project skeleton'], iso(ago(9 * DAY)))
    put(r, 'internal/auth/login.go', LOGIN_GO)
    put(r, 'internal/auth/service.go', 'package auth\n\n// Service signs customers in.\ntype Service struct {\n\tusers  UserStore\n\thasher Hasher\n\ttokens TokenIssuer\n}\n')
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'Customer login'], iso(ago(6 * DAY)))
    put(r, 'internal/orders/orders.go', 'package orders\n\n// Order is one pot of tea, paid for.\ntype Order struct {\n\tID    string\n\tTea   string\n\tPence int\n}\n')
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'Orders: model'], iso(ago(3 * DAY)))
    git(r, home, ['checkout', '-q', '-b', 'fix/empty-password'])
    put(r, 'internal/auth/login.go', LOGIN_GO_FIXED)          // modified, not committed
    put(r, 'internal/auth/login_test.go', LOGIN_TEST_GO)      // new, not committed
  }

  // parrot-web: clean, on a feature branch one commit ahead of main
  r = repos.parrot = join(base, 'parrot-web')
  if (!existsSync(join(r, '.git'))) {
    mkdirSync(r, { recursive: true })
    git(r, home, ['init', '-q', '-b', 'main'])
    put(r, 'package.json', JSON.stringify({ name: 'parrot-web', private: true, description: 'The Norwegian Blue pet shop website', scripts: { dev: 'vite', build: 'vite build', test: 'vitest run' } }, null, 2) + '\n')
    put(r, 'README.md', '# parrot-web\n\nThe website of a pet shop that sells remarkable birds. Beautiful plumage.\n')
    put(r, 'index.html', '<!doctype html>\n<html lang="en">\n  <head><meta charset="utf-8"><title>Norwegian Blue Pet Shop</title></head>\n  <body><div id="app"></div><script type="module" src="/src/main.ts"></script></body>\n</html>\n')
    put(r, 'src/main.ts', "import { renderShop } from './shop'\n\nrenderShop(document.querySelector('#app')!)\n")
    put(r, 'src/shop.ts', "import { birds } from './birds'\n\nexport function renderShop(el: Element) {\n  el.innerHTML = birds.map(b => `<li>${b.name}: £${b.price}</li>`).join('')\n}\n")
    put(r, 'src/birds.ts', "export const birds = [\n  { name: 'Norwegian Blue', price: 120, resting: true },\n  { name: 'African Grey', price: 95, resting: false },\n  { name: 'Budgerigar', price: 18, resting: false },\n]\n")
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'Shop page with a list of birds'], iso(ago(12 * DAY)))
    git(r, home, ['checkout', '-q', '-b', 'feature/price-cards'])
    put(r, 'src/PriceCard.ts', "export function priceCard(b: { name: string, price: number, resting: boolean }) {\n  return `<article class=\"card\"><h3>${b.name}</h3><p>£${b.price}</p>${b.resting ? '<small>Resting</small>' : ''}</article>`\n}\n")
    put(r, 'src/shop.ts', "import { birds } from './birds'\nimport { priceCard } from './PriceCard'\n\nexport function renderShop(el: Element) {\n  el.innerHTML = birds.map(priceCard).join('')\n}\n")
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'Price cards on the shop page'], iso(ago(200)))
  }

  // circus-docs: main only, one untracked draft
  r = repos.docs = join(base, 'circus-docs')
  if (!existsSync(join(r, '.git'))) {
    mkdirSync(r, { recursive: true })
    git(r, home, ['init', '-q', '-b', 'main'])
    put(r, 'README.md', '# circus-docs\n\nThe shop handbook: how orders, refunds and complaints work.\n')
    put(r, 'handbook/complaints.md', '# Complaints\n\nEvery complaint is registered at the counter. Parrots are not exchanged for slugs.\n')
    put(r, 'handbook/opening-hours.md', '# Opening hours\n\nMonday to Saturday, nine to half past five. Closed for lunch.\n')
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'Handbook: complaints and opening hours'], iso(ago(20 * DAY)))
    put(r, 'api/reference.md', '# API reference\n\n## GET /menu\n\nReturns the teas on sale, with prices in pence.\n\n## POST /orders\n\nPlaces an order for one pot of tea.\n')
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'API reference: menu and orders'], iso(ago(DAY + 60)))
    put(r, 'api/login.md', '# POST /login\n\nDraft.\n')
  }
  for (const k of Object.keys(repos)) repos[k] = realpathSync(repos[k])
  return repos
}

// ---------------------------------------------------------------- conversations

let toolN = 0
const tool = (name, input) => ({ id: `toolu_demo_${String(++toolN).padStart(3, '0')}`, name, input })
const user = (content) => ({ role: 'user', content })
const say = (content) => ({ role: 'assistant', content })
const use = (...tools) => ({ role: 'assistant', content: '', tools })

function conversations(repos) {
  const t = repos.teashop, p = repos.parrot, d = repos.docs
  return [
    {
      id: SESSIONS.loginBug, name: 'Fix the empty-password login', project: 'teashop', dir: t, branch: 'fix/empty-password',
      provider: 'claude', model: 'claude-sonnet-5-5', status: 'idle', endedMinAgo: 4, lengthMin: 6, cost: 0.38, turns: 1,
      tags: ['bug', 'auth'], summary: 'Login accepted blank passwords. Now rejected with ErrEmptyPassword, with a test.',
      messages: [
        user('The login endpoint accepts empty passwords. Fix it and add a test.'),
        { role: 'assistant', content: 'I\'ll read the login code first, then fix it and add the test.', tools: [
          tool('Read', { file_path: `${t}/internal/auth/login.go` }),
          tool('Grep', { pattern: 'ErrBadCredentials', path: `${t}/internal`, output_mode: 'content' }),
        ] },
        { role: 'assistant', content: '', tools: [
          tool('Edit', {
            file_path: `${t}/internal/auth/login.go`,
            old_string: '\tif pw == nil {\n\t\treturn "", ErrBadCredentials\n\t}',
            new_string: '\tif pw == nil || strings.TrimSpace(*pw) == "" {\n\t\treturn "", ErrEmptyPassword\n\t}',
          }),
          tool('Write', { file_path: `${t}/internal/auth/login_test.go`, content: LOGIN_TEST_GO }),
          tool('Bash', { command: 'go test ./internal/auth/...', description: 'Run the auth tests' }),
        ] },
        say('**Fixed.** Empty and whitespace-only passwords are now refused before the hash check with a new `ErrEmptyPassword`, and a test covers both. The auth tests pass. Nothing is committed yet: the changes are on `fix/empty-password`, ready for you to review.'),
      ],
    },
    {
      id: SESSIONS.teaOfMonth, name: 'Tea of the month endpoint', project: 'teashop', dir: t, branch: 'fix/empty-password',
      provider: 'claude', model: 'claude-opus-5-5', status: 'processing', endedMinAgo: 0, lengthMin: 3, cost: 0.21, turns: 1,
      tags: ['feature'], summary: 'Adding GET /menu/tea-of-the-month, picked by the calendar month.',
      messages: [
        user('Add a GET /menu/tea-of-the-month endpoint. Pick the tea by calendar month so it changes by itself.'),
        say('I\'ll read the menu package, then add the handler next to the existing one.'),
        use(tool('Read', { file_path: `${t}/internal/menu/menu.go` })),
        use(tool('Read', { file_path: `${t}/cmd/teashop/main.go` })),
        use(tool('Glob', { pattern: 'internal/menu/*_test.go', path: t })),
        say('There are no menu tests yet, so I\'ll add one with the handler.'),
        use(tool('Write', { file_path: `${t}/internal/menu/month.go`, content: 'package menu\n' })),
      ],
    },
    {
      id: SESSIONS.priceCards, name: 'Price cards for the shop page', project: 'parrot', dir: p, branch: 'feature/price-cards',
      provider: 'claude', model: 'claude-sonnet-5-5', status: 'idle', endedMinAgo: 195, lengthMin: 14, cost: 0.74, turns: 3,
      tags: ['frontend', 'design'], summary: 'Birds are shown as price cards; resting birds get a small label. Committed.',
      messages: [
        user('Replace the plain list on the shop page with price cards. Mark birds that are resting.'),
        use(tool('Read', { file_path: `${p}/src/shop.ts` })),
        use(tool('Read', { file_path: `${p}/src/birds.ts` })),
        use(tool('Write', { file_path: `${p}/src/PriceCard.ts`, content: 'export function priceCard() {}\n' })),
        use(tool('Edit', { file_path: `${p}/src/shop.ts`, old_string: "birds.map(b => `<li>${b.name}: £${b.price}</li>`)", new_string: 'birds.map(priceCard)' })),
        use(tool('Bash', { command: 'npm run build', description: 'Build the site' })),
        say('The shop page now renders one **price card** per bird, and resting birds carry a small "Resting" label. The build passes.\n\n```ts\nexport function renderShop(el: Element) {\n  el.innerHTML = birds.map(priceCard).join(\'\')\n}\n```'),
        user('Lovely. Commit it.'),
        use(tool('Bash', { command: 'git add -A && git commit -m "Price cards on the shop page"', description: 'Commit the change' })),
        say('Committed on `feature/price-cards` as "Price cards on the shop page".'),
      ],
    },
    {
      id: SESSIONS.apiReference, name: 'Write the API reference', project: 'docs', dir: d, branch: 'main',
      provider: 'deepseek', model: 'deepseek-chat', status: 'idle', endedMinAgo: DAY + 55, lengthMin: 9, cost: 0.03, turns: 2,
      tags: ['docs'], summary: 'API reference for /menu and /orders written from the Go handlers.',
      messages: [
        user('Write an API reference for the menu and orders endpoints in api/reference.md.'),
        use(tool('Glob', { pattern: 'handbook/*.md', path: d })),
        use(tool('Read', { file_path: `${d}/README.md` })),
        use(tool('Write', { file_path: `${d}/api/reference.md`, content: '# API reference\n' })),
        say('`api/reference.md` now documents **GET /menu** and **POST /orders**, with an example request and response for each.'),
      ],
    },
    {
      id: SESSIONS.copyReview, name: 'Review the returns page copy', project: 'parrot', dir: p, branch: 'feature/price-cards',
      provider: 'codex', model: 'gpt-6-astra', status: 'idle', endedMinAgo: DAY + 300, lengthMin: 5, cost: 0, turns: 1,
      tags: ['review'], summary: 'Read-through of the returns policy text; three wording fixes suggested.',
      messages: [
        user('Read the returns policy on the site and tell me what a customer could misunderstand.'),
        use(tool('Grep', { pattern: 'return', path: `${p}/src`, output_mode: 'files_with_matches' })),
        use(tool('Read', { file_path: `${p}/README.md` })),
        say('Three things a customer could trip over:\n\n1. "Resting" is never explained next to the price.\n2. The refund period is given in two places with different numbers.\n3. "Remarkable bird" reads as a guarantee.\n\nI have not changed any files.'),
      ],
    },
    {
      id: SESSIONS.goUpgrade, name: 'Upgrade to Go 1.26', project: 'teashop', dir: t, branch: 'main',
      provider: 'claude', model: 'claude-haiku-5-5', status: 'ended', endedMinAgo: 3 * DAY + 20, lengthMin: 4, cost: 0.05, turns: 1,
      tags: ['chore'], summary: 'go.mod moved to Go 1.26; tests pass.',
      messages: [
        user('Bump the module to Go 1.26 and make sure everything still builds.'),
        use(tool('Read', { file_path: `${t}/go.mod` })),
        use(tool('Edit', { file_path: `${t}/go.mod`, old_string: 'go 1.25', new_string: 'go 1.26' })),
        use(tool('Bash', { command: 'go build ./... && go test ./...', description: 'Build and test' })),
        say('`go.mod` now says `go 1.26`. Everything builds and the tests pass.'),
      ],
    },
  ]
}

// ---------------------------------------------------------------- seeding

const sqlTime = (d) => d.toISOString().replace('T', ' ').slice(0, 19)

async function api(server, cookie, method, path, body) {
  const res = await fetch(server.url + path, {
    method, headers: { 'Content-Type': 'application/json', Origin: server.url, ...(cookie ? { Cookie: cookie } : {}) },   // cookie requests must come from the app's own origin
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  return res
}

async function ensureUser(server) {
  const status = await (await fetch(`${server.url}/api/auth/status`)).json()
  if (status.needs_setup) {
    const res = await api(server, null, 'POST', '/api/auth/setup', { username: server.user, password: server.password })
    if (!res.ok) throw new Error(`setup failed: ${res.status} ${await res.text()}`)
    console.log(`created user ${server.user}`)
  }
  return `session_token=${await sessionToken(server)}`
}

export async function seed(server) {
  const cookie = await ensureUser(server)
  const repos = makeRepos(server.home)

  // Projects, through the API (it checks the folders like the app does)
  const existing = await (await api(server, cookie, 'GET', '/api/projects')).json()
  const have = new Set((existing.projects || []).map(p => p.id))
  for (const [key, p] of Object.entries(PROJECTS)) {
    if (have.has(p.id)) continue
    const res = await api(server, cookie, 'POST', '/api/projects', {
      id: p.id, name: p.name, path: repos[key], description: p.description, color: p.color,
      default_provider: 'claude', default_model: 'claude-sonnet-5-5',
    })
    if (!res.ok) throw new Error(`project ${p.name}: ${res.status} ${await res.text()}`)
  }

  // Sessions and messages, straight into SQLite
  const db = new DatabaseSync(server.db)
  db.exec('PRAGMA busy_timeout = 5000')
  const owner = db.prepare('SELECT id FROM users WHERE username = ?').get(server.user)
  const insSession = db.prepare(`INSERT OR REPLACE INTO agent_sessions
    (id, status, created_at, updated_at, ended_at, message_count, cost_usd, num_turns, duration_ms, model_name, git_branch,
     options, provider, project_id, owner_user_id, owner_uuid, view_mode)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'live')`)
  const delMessages = db.prepare('DELETE FROM agent_messages WHERE session_id = ?')
  const insMessage = db.prepare(`INSERT INTO agent_messages
    (id, session_id, sequence, role, content, tool_uses, timestamp, tokens_used, user_id, provider, model_name)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
  const insDigest = db.prepare(`INSERT OR REPLACE INTO session_digests (session_id, summary, tags, source_version, updated_at)
    VALUES (?, ?, ?, ?, ?)`)

  const convs = conversations(repos)
  for (const c of convs) {
    const end = ago(c.endedMinAgo), start = ago(c.endedMinAgo + c.lengthMin)
    const options = {
      agent_name: c.name, working_directory: c.dir, model: c.model, provider: c.provider,
      project_id: PROJECTS[c.project].id, permission_mode: 'default',
    }
    insSession.run(c.id, c.status, sqlTime(start), sqlTime(end), c.status === 'ended' ? sqlTime(end) : null,
      c.messages.length, c.cost, c.turns, c.lengthMin * 60000, c.model, c.branch,
      JSON.stringify(options), c.provider, PROJECTS[c.project].id, server.user, owner?.id ?? null)
    delMessages.run(c.id)
    c.messages.forEach((m, i) => {
      const at = new Date(start.getTime() + (end.getTime() - start.getTime()) * (i / Math.max(1, c.messages.length - 1)))
      insMessage.run(randomUUID(), c.id, i + 1, m.role, m.content, m.tools ? JSON.stringify(m.tools) : null, sqlTime(at),
        m.role === 'assistant' ? 180 + 37 * i : 0, m.role === 'user' ? server.user : null,
        m.role === 'assistant' ? c.provider : null, m.role === 'assistant' ? c.model : null)
    })
    insDigest.run(c.id, c.summary, JSON.stringify(c.tags), `${c.messages.length}:${c.status}`, sqlTime(end))
  }
  db.close()

  // The user's own tags, through the API
  for (const c of convs) {
    const res = await api(server, cookie, 'PUT', `/api/agent/sessions/${c.id}/tags`, { tags: c.tags })
    if (!res.ok) console.warn(`tags for "${c.name}": ${res.status} ${await res.text()}`)
  }

  if (server.fresh) {   // a --fresh server counts as seeded from now on
    try { appendFileSync(join(dirname(dirname(dirname(server.home))), 'seeded'), new Date().toISOString() + '\n') } catch { /* fine */ }
  }
  console.log(`seeded ${convs.length} sessions in ${Object.keys(PROJECTS).length} projects under ${dirname(repos.teashop)}`)
  return { repos, sessions: convs.map(c => ({ id: c.id, name: c.name, project: PROJECTS[c.project].name, status: c.status })) }
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const args = parseArgs()
  const server = readServer(Number(args.port || 3401))
  const out = await seed(server)
  if (args.json) console.log(JSON.stringify(out, null, 2))
}

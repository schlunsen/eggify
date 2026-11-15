#!/usr/bin/env node
// Extra demo data for the screen library, layered on ../seed.mjs (run that first):
// three more projects (one with worktrees, one with its own Claude config), a dozen more
// sessions (a team with its workers, sessions that wait for the user, a loop, a session with
// subagents), skills on disk (approved and waiting for review), memories, connector keys.
//
//   node learn/capture/library/seed-extra.mjs --port 3420
//
// Everything is invented (docs/MEDIA_PROVENANCE.md). Running it again refreshes the timestamps,
// so "2 min ago" stays true: run it right before a capture.
import { DatabaseSync } from 'node:sqlite'
import { execFileSync } from 'node:child_process'
import { chmodSync, existsSync, mkdirSync, realpathSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { randomUUID } from 'node:crypto'
import { pathToFileURL } from 'node:url'
import { parseArgs, readServer } from '../lib.mjs'
import { sessionCookie } from './lib.mjs'
import { PROJECTS as BASE_PROJECTS, SESSIONS as BASE_SESSIONS } from '../seed.mjs'

export const X_PROJECTS = {
  cheese: { id: 'aaaaaaaa-0000-4000-8000-000000000004', name: 'cheese-shop', color: '#c98a1b', description: 'Stock and counter service for the cheese shop (Python)' },
  walks: { id: 'aaaaaaaa-0000-4000-8000-000000000005', name: 'silly-walks', color: '#3b6ea5', description: 'Grant applications for the Ministry of Silly Walks' },
  lumber: { id: 'aaaaaaaa-0000-4000-8000-000000000006', name: 'lumberjack-cli', color: '#4f7a3a', description: 'Command-line tool for the lumber yard (Rust)' },
}
export const PROJECTS = { ...BASE_PROJECTS, ...X_PROJECTS }

const sid = n => `22222222-3333-4444-8555-6666666666${String(n).padStart(2, '0')}`
export const X_SESSIONS = {
  steer: sid(1),      // "Loyalty stamps for regulars" (teashop-api): the chat to steer; idle, rich conversation
  refund: sid(2),     // "Refund form for returned parrots" (parrot-web): waits for a permission
  hours: sid(3),      // "Opening hours page" (circus-docs): waits for an answer
  postgres: sid(4),   // "Move orders to Postgres" (teashop-api): working, with subagents
  team: sid(5),       // "Low-stock e-mails" (cheese-shop): team orchestrator
  builder: sid(6),    //   its build worker
  reviewer: sid(7),   //   its review worker (working, round 2)
  loop: sid(8),       // "Make the lumberjack tests pass" (lumberjack-cli): loop session
  trust: sid(9),      // "Validate the grant form" (silly-walks): folder with its own Claude config
  cron: sid(10),      // "Nightly link check" (circus-docs): idle with a scheduled job
  wensley: sid(11),   // earlier
  notFound: sid(12),  // earlier, ended
  spam: sid(13),      // earlier
  manPage: sid(14),   // earlier, ended
  proposer: sid(15),  // "Plan the Stilton price update" (cheese-shop): proposes a worker / a loop
}
export const SESSIONS = { ...BASE_SESSIONS, ...X_SESSIONS }

export const TEAM = {
  worktreeDir: '.worktrees/3f2a9c1e-team-low-stock-emails', branch: 'team/low-stock-emails',
  workerDir: '.worktrees/7b1d4e2a-worker-low-stock-email', workerBranch: 'worker/7b1d4e2a-low-stock-email',
}

// ---------------------------------------------------------------- helpers

function git(cwd, home, args, date) {
  const env = {
    PATH: '/usr/bin:/bin:/opt/homebrew/bin', HOME: home, LANG: 'en_US.UTF-8',
    GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null',
    GIT_AUTHOR_NAME: 'Mr. Praline', GIT_AUTHOR_EMAIL: 'praline@example.com',
    GIT_COMMITTER_NAME: 'Mr. Praline', GIT_COMMITTER_EMAIL: 'praline@example.com',
  }
  if (date) { env.GIT_AUTHOR_DATE = date; env.GIT_COMMITTER_DATE = date }
  return execFileSync('git', args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] }).toString()
}
function put(root, rel, text, mode) {
  const file = join(root, rel)
  mkdirSync(dirname(file), { recursive: true })
  writeFileSync(file, text)
  if (mode) chmodSync(file, mode)
}
const ago = min => new Date(Date.now() - min * 60000)
const iso = d => d.toISOString()
const sqlTime = d => d.toISOString().replace('T', ' ').slice(0, 19)
const DAY = 1440

// ---------------------------------------------------------------- repositories

const STOCK_PY = `from dataclasses import dataclass


@dataclass
class Cheese:
    name: str
    kilos: float
    reorder_at: float


SHELF = [
    Cheese("Red Leicester", 4.0, 2.0),
    Cheese("Tilsit", 1.5, 2.0),
    Cheese("Caerphilly", 6.2, 3.0),
    Cheese("Wensleydale", 0.0, 2.5),
]


def in_stock(name: str) -> bool:
    return any(c.name == name and c.kilos > 0 for c in SHELF)
`
const STOCK_PY_LOW = STOCK_PY + `

def low_stock() -> list[Cheese]:
    """Cheeses at or below their reorder level, emptiest first."""
    low = [c for c in SHELF if c.kilos <= c.reorder_at]
    return sorted(low, key=lambda c: c.kilos / c.reorder_at if c.reorder_at else 0)
`
const ALERTS_PY = `from email.message import EmailMessage

from .stock import Cheese, low_stock

COUNTER = "counter@cheeseshop.example"


def low_stock_email(cheeses: list[Cheese]) -> EmailMessage:
    msg = EmailMessage()
    msg["To"] = COUNTER
    msg["Subject"] = f"{len(cheeses)} cheeses need reordering"
    lines = [f"- {c.name}: {c.kilos:g} kg left (reorder at {c.reorder_at:g} kg)" for c in cheeses]
    msg.set_content("Running low:\\n\\n" + "\\n".join(lines) + "\\n")
    return msg


def send_low_stock_alert(send) -> bool:
    """Sends one e-mail listing every low cheese. Returns False when the shelf is fine."""
    cheeses = low_stock()
    if not cheeses:
        return False
    send(low_stock_email(cheeses))
    return True
`
const ALERTS_PY_WIP = ALERTS_PY.replace('    cheeses = low_stock()\n', '    cheeses = [c for c in low_stock() if c.name not in _already_sent_today()]\n')
  + `

def _already_sent_today() -> set[str]:
    # TODO(review round 1): remember what was sent, so the counter gets one e-mail a day
    return set()
`
const TEST_ALERTS_PY = `from cheese.alerts import low_stock_email, send_low_stock_alert
from cheese.stock import low_stock


def test_low_stock_lists_emptiest_first():
    assert [c.name for c in low_stock()] == ["Wensleydale", "Tilsit"]


def test_email_names_every_low_cheese():
    body = low_stock_email(low_stock()).get_content()
    assert "Wensleydale: 0 kg left" in body
    assert "Tilsit: 1.5 kg left" in body


def test_alert_is_sent_once():
    sent = []
    assert send_low_stock_alert(sent.append) is True
    assert len(sent) == 1
`

function makeRepos(home) {
  const base = join(home, 'projects')
  mkdirSync(base, { recursive: true })
  const repos = {}

  // cheese-shop: main checkout + a team worktree + a worker worktree, several branches
  let r = repos.cheese = join(base, 'cheese-shop')
  if (!existsSync(join(r, '.git'))) {
    mkdirSync(r, { recursive: true })
    git(r, home, ['init', '-q', '-b', 'main'])
    put(r, '.gitignore', '__pycache__/\n.venv/\n.worktrees/\n')
    put(r, 'README.md', '# cheese-shop\n\nStock and counter service for a cheese shop. It knows what is on the shelf, what has run out, and what to reorder.\n\n```sh\nuv run pytest\n```\n')
    put(r, 'pyproject.toml', '[project]\nname = "cheese-shop"\nversion = "0.3.0"\ndescription = "Stock and counter service for the cheese shop"\nrequires-python = ">=3.12"\n\n[tool.pytest.ini_options]\ntestpaths = ["tests"]\n')
    put(r, 'cheese/__init__.py', '')
    put(r, 'cheese/stock.py', STOCK_PY)
    put(r, 'cheese/counter.py', 'from .stock import in_stock\n\n\ndef ask(name: str) -> str:\n    if in_stock(name):\n        return f"Certainly, sir. {name} it is."\n    return f"I\'m afraid we\'re fresh out of {name}, sir."\n')
    put(r, 'tests/test_counter.py', 'from cheese.counter import ask\n\n\ndef test_fresh_out():\n    assert "fresh out" in ask("Wensleydale")\n\n\ndef test_in_stock():\n    assert ask("Red Leicester").startswith("Certainly")\n')
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'Shelf, counter and tests'], iso(ago(30 * DAY)))

    git(r, home, ['checkout', '-q', '-b', 'feature/wensleydale-import'])
    put(r, 'cheese/importer.py', 'import csv\n\nfrom .stock import Cheese\n\n\ndef read_delivery(path: str) -> list[Cheese]:\n    with open(path, newline="") as f:\n        return [Cheese(row["name"], float(row["kilos"]), float(row["reorder_at"])) for row in csv.DictReader(f)]\n')
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'Import a delivery from CSV'], iso(ago(12 * DAY)))
    git(r, home, ['checkout', '-q', 'main'])
    git(r, home, ['merge', '-q', '--no-ff', 'feature/wensleydale-import', '-m', 'Merge feature/wensleydale-import'], iso(ago(11 * DAY)))

    git(r, home, ['checkout', '-q', '-b', 'chore/ruff'])
    put(r, 'pyproject.toml', '[project]\nname = "cheese-shop"\nversion = "0.3.0"\ndescription = "Stock and counter service for the cheese shop"\nrequires-python = ">=3.12"\n\n[tool.pytest.ini_options]\ntestpaths = ["tests"]\n\n[tool.ruff]\nline-length = 110\n')
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'Lint with ruff'], iso(ago(8 * DAY)))
    git(r, home, ['checkout', '-q', 'main'])
    git(r, home, ['merge', '-q', '--ff-only', 'chore/ruff'])

    git(r, home, ['checkout', '-q', '-b', 'fix/stilton-rounding'])
    put(r, 'cheese/prices.py', 'from decimal import ROUND_HALF_UP, Decimal\n\n\ndef price(pence_per_kilo: int, kilos: float) -> int:\n    """Price in pence, rounded half up like the till does."""\n    exact = Decimal(pence_per_kilo) * Decimal(str(kilos))\n    return int(exact.quantize(Decimal("1"), rounding=ROUND_HALF_UP))\n')
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'Round prices half up, like the till'], iso(ago(2 * DAY)))
    git(r, home, ['checkout', '-q', 'main'])

    // the team's worktree: the plan and the low_stock() helper are committed, a note is not
    const tw = join(r, TEAM.worktreeDir)
    git(r, home, ['worktree', 'add', '-q', '-b', TEAM.branch, tw, 'main'])
    put(tw, 'docs/low-stock-emails.md', '# Low-stock e-mails\n\nWhen a cheese drops to its reorder level, the counter gets one e-mail a day listing every low cheese, emptiest first.\n\n1. `stock.low_stock()` returns the cheeses to reorder\n2. `alerts.send_low_stock_alert()` builds and sends the e-mail\n3. the nightly job calls it\n')
    git(tw, home, ['add', '-A']); git(tw, home, ['commit', '-q', '-m', 'Plan: low-stock e-mails'], iso(ago(95)))
    put(tw, 'cheese/stock.py', STOCK_PY_LOW)
    git(tw, home, ['add', '-A']); git(tw, home, ['commit', '-q', '-m', 'stock.low_stock(): what to reorder'], iso(ago(80)))
    put(tw, 'docs/review-notes.md', '# Review notes\n\nRound 1: changes requested. The alert would go out on every run; it must be once a day.\n')

    // the build worker's worktree: branched from the team branch, one commit and work in progress
    const ww = join(r, TEAM.workerDir)
    git(r, home, ['worktree', 'add', '-q', '-b', TEAM.workerBranch, ww, TEAM.branch])
    put(ww, 'cheese/alerts.py', ALERTS_PY)
    put(ww, 'tests/test_alerts.py', TEST_ALERTS_PY)
    git(ww, home, ['add', '-A']); git(ww, home, ['commit', '-q', '-m', 'E-mail the counter when cheese runs low'], iso(ago(42)))
    put(ww, 'cheese/alerts.py', ALERTS_PY_WIP)                    // modified, not committed
    put(ww, 'cheese/sent_log.py', 'from datetime import date\n\n_sent: dict[date, set[str]] = {}\n\n\ndef remember(names: list[str]) -> None:\n    _sent.setdefault(date.today(), set()).update(names)\n')   // new
    git(ww, home, ['add', 'cheese/sent_log.py'])                  // staged

    // main checkout: a little uncommitted work of its own
    put(r, 'README.md', '# cheese-shop\n\nStock and counter service for a cheese shop. It knows what is on the shelf, what has run out, and what to reorder.\n\n```sh\nuv run pytest\n```\n\n## Deliveries\n\nDrop the supplier\'s CSV into `deliveries/` and run `uv run python -m cheese.importer`.\n')
    put(r, 'deliveries/2026-11-14.csv', 'name,kilos,reorder_at\nWensleydale,5,2.5\nTilsit,3,2\n')
  }

  // silly-walks: a folder with its own Claude config (hook, allowed tools, MCP server) → workspace trust
  r = repos.walks = join(base, 'silly-walks')
  if (!existsSync(join(r, '.git'))) {
    mkdirSync(r, { recursive: true })
    git(r, home, ['init', '-q', '-b', 'main'])
    put(r, 'README.md', '# silly-walks\n\nGrant applications for the Ministry of Silly Walks: the form, its validation and the review queue.\n')
    put(r, 'package.json', JSON.stringify({ name: 'silly-walks', private: true, description: 'Grant applications for the Ministry of Silly Walks', scripts: { test: 'vitest run', lint: 'eslint .' } }, null, 2) + '\n')
    put(r, 'src/form.ts', "export interface Application {\n  applicant: string\n  walk: string\n  sillinessOutOfTen: number\n  amountPounds: number\n}\n\nexport function validate(a: Application): string[] {\n  const problems: string[] = []\n  if (!a.applicant.trim()) problems.push('Who is applying?')\n  if (a.sillinessOutOfTen < 1 || a.sillinessOutOfTen > 10) problems.push('Silliness is marked out of ten.')\n  return problems\n}\n")
    put(r, '.claude/settings.json', JSON.stringify({
      permissions: { allow: ['Bash(npm test:*)', 'Bash(npm run lint:*)'] },
      hooks: { PostToolUse: [{ matcher: 'Edit|Write', hooks: [{ type: 'command', command: '$CLAUDE_PROJECT_DIR/.claude/hooks/format.sh' }] }] },
      env: { MINISTRY_ENV: 'staging' },
    }, null, 2) + '\n')
    put(r, '.claude/hooks/format.sh', '#!/bin/sh\n# Formats the file an agent just changed.\nnpx prettier --write "$CLAUDE_FILE_PATHS" >/dev/null 2>&1\n', 0o755)
    put(r, '.mcp.json', JSON.stringify({ mcpServers: { 'grant-register': { command: 'node', args: ['./tools/grant-register.mjs'] } } }, null, 2) + '\n')
    put(r, 'tools/grant-register.mjs', "// Looks a grant number up in the Ministry's register (demo).\nconsole.log('grant-register ready')\n")
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'Application form and its validation'], iso(ago(15 * DAY)))
  }

  // lumberjack-cli
  r = repos.lumber = join(base, 'lumberjack-cli')
  if (!existsSync(join(r, '.git'))) {
    mkdirSync(r, { recursive: true })
    git(r, home, ['init', '-q', '-b', 'main'])
    put(r, 'Cargo.toml', '[package]\nname = "lumberjack"\nversion = "0.4.1"\nedition = "2024"\ndescription = "Command-line tool for the lumber yard"\n')
    put(r, 'README.md', '# lumberjack-cli\n\nCounts logs, prices planks and prints the day\'s tally. It sleeps all night and it works all day.\n')
    put(r, 'src/main.rs', 'mod tally;\n\nfn main() {\n    let logs = tally::count(&["larch", "fir", "scots pine"]);\n    println!("{logs} logs felled today");\n}\n')
    put(r, 'src/tally.rs', 'pub fn count(trees: &[&str]) -> usize {\n    trees.len()\n}\n\n#[cfg(test)]\nmod tests {\n    use super::*;\n\n    #[test]\n    fn counts_every_tree() {\n        assert_eq!(count(&["larch", "fir"]), 2);\n    }\n}\n')
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'Tally of the day'], iso(ago(18 * DAY)))
    git(r, home, ['checkout', '-q', '-b', 'feature/plank-prices'])
    put(r, 'src/prices.rs', 'pub fn plank_pence(metres: f64, pence_per_metre: u32) -> u32 {\n    (metres * pence_per_metre as f64).round() as u32\n}\n')
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'Price a plank by the metre'], iso(ago(70)))
  }
  // a folder that is not a project yet (New project → Open an existing folder)
  r = join(base, 'argument-clinic')
  if (!existsSync(join(r, '.git'))) {
    mkdirSync(r, { recursive: true })
    git(r, home, ['init', '-q', '-b', 'main'])
    put(r, 'README.md', '# argument-clinic\n\nBooking system for five-minute arguments and the full half hour.\n')
    put(r, 'package.json', JSON.stringify({ name: 'argument-clinic', private: true, description: 'Booking system for five-minute arguments and the full half hour' }, null, 2) + '\n')
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'Bookings: five minutes or the full half hour'], iso(ago(40 * DAY)))
  }

  // a logo for the pet shop's site (the project picker shows a project's own icon)
  r = join(base, 'parrot-web')
  if (existsSync(join(r, '.git')) && !existsSync(join(r, 'public/favicon.svg'))) {
    put(r, 'public/favicon.svg', '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="14" fill="#2f7d6b"/><path d="M20 44c0-14 8-26 20-26 6 0 10 4 10 9l6 3-7 3c-2 9-9 15-18 15-4 0-8-1-11-4z" fill="#8fd3f4"/><circle cx="41" cy="26" r="2.5" fill="#12333b"/><path d="M50 27l7 2-7 3z" fill="#f2b33d"/></svg>\n')
    git(r, home, ['add', '-A']); git(r, home, ['commit', '-q', '-m', 'Shop logo'], iso(ago(190)))
  }

  for (const k of Object.keys(repos)) repos[k] = realpathSync(repos[k])
  return repos
}

// ---------------------------------------------------------------- skills on disk

function makeSkills(home) {
  const root = join(home, '.claude', 'skills')
  const skill = (name, front, body, files = {}) => {
    const dir = join(root, name)
    put(dir, 'SKILL.md', `---\n${front.trim()}\n---\n\n${body.trim()}\n`)
    for (const [rel, f] of Object.entries(files)) put(dir, rel, f.text, f.mode)
  }
  skill('tea-tasting-notes', 'name: tea-tasting-notes\ndescription: Write tasting notes for a new tea in the house style (leaf, liquor, nose, cup) for the menu.',
    '# Tea tasting notes\n\nWrite four short lines for the menu card:\n\n1. **Leaf**: what the dry leaf looks like\n2. **Liquor**: colour in the cup\n3. **Nose**: two or three aromas, no more\n4. **Cup**: how it drinks, and what to have with it\n\nKeep it under 60 words. No superlatives.\n',
    { 'examples/lapsang.md': { text: '**Leaf** long, dark, twisted. **Liquor** deep amber. **Nose** pine smoke, dried plum. **Cup** brisk, dry finish; good with shortbread.\n' } })
  skill('release-checklist', 'name: release-checklist\ndescription: Walk through the release checklist before tagging a version (tests, changelog, version bump, tag).\nargument-hint: "[version]"',
    '# Release checklist\n\nBefore tagging `$ARGUMENTS`:\n\n- [ ] the test suite passes on a clean checkout\n- [ ] `CHANGELOG.md` has an entry for the version\n- [ ] the version is bumped in every manifest\n- [ ] nothing is uncommitted\n\nReport each item as done or blocked. Do not tag: say when it is ready.\n')
  skill('complaint-reply', 'name: complaint-reply\ndescription: Draft a polite, factual reply to a customer complaint from the shop handbook\'s rules.',
    '# Complaint reply\n\nRead the complaint, find the rule that applies in `handbook/complaints.md`, and draft a reply that:\n\n- thanks the customer and restates the problem in one sentence\n- says what the shop will do, and by when\n- never argues about whether the parrot is resting\n')
  skill('parrot-care-sheet', 'name: parrot-care-sheet\ndescription: Produce a one-page care sheet for a bird sold by the shop (diet, cage, signs it is unwell).\nallowed-tools: Read Grep',
    '# Parrot care sheet\n\nOne page, three headings: **Diet**, **Cage**, **When to call us**. Use the species entry in `src/birds.ts` for the facts.\n')
  // Waits for review: it wants to run commands (hooks, allowed tools) and ships an executable script.
  skill('deploy-to-counter',
    'name: deploy-to-counter\ndescription: Build the shop site and copy it to the counter\'s display, after the checks pass.\nallowed-tools: Bash(npm run build:*) Bash(rsync:*) Read\nhooks:\n  PreToolUse:\n    - matcher: Bash\n      hooks:\n        - type: command\n          command: ./check.sh\nmodel: sonnet',
    '# Deploy to the counter\n\n1. Run `./check.sh` (tests, clean checkout).\n2. `npm run build`\n3. `rsync -a dist/ counter.local:/srv/display/`\n4. Say what changed since the last deploy, from `git log`.\n\nStop at the first failure and report it.\n',
    {
      'check.sh': { text: '#!/bin/sh\n# Refuses to deploy from a dirty checkout or with failing tests.\nset -e\ngit diff --quiet || { echo "uncommitted changes" >&2; exit 2; }\nnpm test --silent\n', mode: 0o755 },
      'templates/deploy-note.md': { text: '# Deployed {{date}}\n\n{{changes}}\n' },
      'hosts.txt': { text: 'counter.local\nback-room.local\n' },
    })
  skill('silly-walk-grading', 'name: silly-walk-grading\ndescription: Grade a grant application\'s walk for silliness against the Ministry\'s published scale.',
    '# Silly walk grading\n\nScore the walk described in the application from 1 to 10 using `docs/scale.md`, and give one sentence of reasoning per point deducted.\n')
}

// ---------------------------------------------------------------- conversations

let toolN = 100
const tool = (name, input) => ({ id: `toolu_demo_${String(++toolN).padStart(3, '0')}`, name, input })
const user = content => ({ role: 'user', content })
const say = content => ({ role: 'assistant', content })
const use = (...tools) => ({ role: 'assistant', content: '', tools })
const note = content => ({ role: 'system', content })
const md = (...lines) => lines.join('\n')

function conversations(repos) {
  const t = repos.teashop, p = repos.parrot, d = repos.docs, c = repos.cheese, w = repos.walks, l = repos.lumber
  const tw = join(c, TEAM.worktreeDir), ww = join(c, TEAM.workerDir)
  const team = {
    builder: { provider: 'deepseek', model: 'deepseek-chat' }, reviewer: { provider: 'codex', model: 'gpt-6-astra' },
    max_review_rounds: 3, brief_sent: true, worktree: { path: tw, branch: TEAM.branch, base: 'main' },
  }
  return [
    {
      id: X_SESSIONS.steer, name: 'Loyalty stamps for regulars', project: 'teashop', dir: t, branch: 'fix/empty-password',
      provider: 'claude', model: 'claude-sonnet-5-5', status: 'idle', endedMinAgo: 2, lengthMin: 11, cost: 0.61, turns: 2,
      tags: ['feature', 'orders'], summary: 'Regulars earn a stamp per pot; the tenth pot is free. Stamps live on the order model, with tests.',
      messages: [
        user('Regulars should get a stamp for every pot of tea, and the tenth pot free. Add it to orders.'),
        { role: 'assistant', content: 'I\'ll read the order model first, then see where orders are created.', tools: [
          tool('Read', { file_path: `${t}/internal/orders/orders.go` }),
          tool('Grep', { pattern: 'Order\\{', path: `${t}/internal`, output_mode: 'content' }),
        ] },
        { role: 'assistant', content: '`Order` has no customer yet, so stamps need somewhere to live. I\'ll add `Customer` and `Free` to the order, and a small `Card` type that counts stamps.', tools: [
          tool('Edit', {
            file_path: `${t}/internal/orders/orders.go`,
            old_string: 'type Order struct {\n\tID    string\n\tTea   string\n\tPence int\n}',
            new_string: 'type Order struct {\n\tID       string\n\tCustomer string\n\tTea      string\n\tPence    int\n\tFree     bool // the tenth pot is on the house\n}',
          }),
        ] },
        use(
          tool('Write', { file_path: `${t}/internal/orders/card.go`, content: 'package orders\n\n// Card is a regular\'s loyalty card.\ntype Card struct{ Stamps int }\n' }),
          tool('Write', { file_path: `${t}/internal/orders/card_test.go`, content: 'package orders\n' }),
          tool('Bash', { command: 'go test ./internal/orders/...', description: 'Run the order tests' }),
          tool('TodoWrite', { todos: [
            { content: 'Add Customer and Free to Order', status: 'completed', activeForm: 'Adding Customer and Free to Order' },
            { content: 'Card type with Stamp()', status: 'completed', activeForm: 'Writing the Card type' },
            { content: 'Tests for the tenth pot', status: 'completed', activeForm: 'Testing the tenth pot' },
          ] }),
        ),
        say(md(
          '**Done.** Every paid pot stamps the customer\'s card, and the tenth pot is free.',
          '',
          '- `internal/orders/orders.go`: `Order` gains `Customer` and `Free`',
          '- `internal/orders/card.go`: new `Card` type with `Stamp()`',
          '- `internal/orders/card_test.go`: nine stamps, then a free pot, then back to zero',
          '',
          '```go',
          'func (c *Card) Stamp() (free bool) {',
          '\tc.Stamps++',
          '\tif c.Stamps == 10 {',
          '\t\tc.Stamps = 0',
          '\t\treturn true',
          '\t}',
          '\treturn false',
          '}',
          '```',
          '',
          'Two things to decide before this ships:',
          '',
          '1. Cards are kept in memory, so stamps are lost on a restart. They belong in the database with the orders.',
          '2. A free pot still appears on the receipt at full price with a discount line. Say if you would rather show `£0.00`.',
          '',
          'The order tests pass (`ok  example.com/teashop/internal/orders  0.24s`). Nothing is committed.',
        )),
      ],
    },
    {
      id: X_SESSIONS.refund, name: 'Refund form for returned parrots', project: 'parrot', dir: p, branch: 'feature/price-cards',
      provider: 'claude', model: 'claude-sonnet-5-5', status: 'processing', endedMinAgo: 1, lengthMin: 7, cost: 0.33, turns: 1,
      tags: ['frontend', 'payments'], summary: 'Refund form for birds returned within 14 days; waiting to push the branch.',
      messages: [
        user('Add a refund form for birds returned within 14 days. Push the branch when the build passes.'),
        use(tool('Read', { file_path: `${p}/src/shop.ts` })),
        use(tool('Read', { file_path: `${p}/src/birds.ts` })),
        say('I\'ll add the form as its own module and link it from each price card.'),
        use(tool('Write', { file_path: `${p}/src/RefundForm.ts`, content: 'export function refundForm() {}\n' })),
        use(tool('Edit', { file_path: `${p}/src/PriceCard.ts`, old_string: "${b.resting ? '<small>Resting</small>' : ''}</article>`", new_string: "${b.resting ? '<small>Resting</small>' : ''}<a href=\"#refund\">Return this bird</a></article>`" })),
        use(tool('Bash', { command: 'npm run build', description: 'Build the site' })),
        use(tool('Bash', { command: 'git checkout -b feature/refund-form && git add -A && git commit -m "Refund form for returned birds"', description: 'Commit on a new branch' })),
        say('The form is in `src/RefundForm.ts` and every price card links to it. The build passes and the work is committed on `feature/refund-form`. Pushing the branch now.'),
      ],
    },
    {
      id: X_SESSIONS.hours, name: 'Opening hours page', project: 'docs', dir: d, branch: 'main',
      provider: 'claude', model: 'claude-opus-5-5', status: 'processing', endedMinAgo: 3, lengthMin: 4, cost: 0.19, turns: 1,
      tags: ['docs'], summary: 'Rewriting the opening hours page as a table; asked how to show the lunch closure.',
      messages: [
        user('Turn the opening hours page into a proper table, one row per day.'),
        use(tool('Read', { file_path: `${d}/handbook/opening-hours.md` })),
        say('The page says "nine to half past five, closed for lunch" but not when lunch is. Before I write the table I need to know how to show it.'),
      ],
    },
    {
      id: X_SESSIONS.postgres, name: 'Move orders to Postgres', project: 'teashop', dir: t, branch: 'fix/empty-password',
      provider: 'claude', model: 'claude-opus-5-5', status: 'processing', endedMinAgo: 0, lengthMin: 16, cost: 1.42, turns: 1,
      tags: ['database', 'orders'], summary: 'Order storage is moving from memory to Postgres; subagents write the schema, the store and audit the handlers.',
      messages: [
        user('Move order storage from the in-memory map to Postgres. Split it up: the schema and migration, the store, and an audit of every handler that touches orders.'),
        say('I\'ll look at how orders are stored today, then hand the three parts to subagents so they run side by side.'),
        use(tool('Read', { file_path: `${t}/internal/orders/orders.go` })),
        use(tool('Grep', { pattern: 'orders\\.', path: `${t}/cmd`, output_mode: 'files_with_matches' })),
        use(tool('Agent', { description: 'Explore how orders are read and written', subagent_type: 'Explore', prompt: 'Find every place that creates, reads or lists orders in this repository and report the call sites.', model: 'haiku' })),
        say('Orders are only touched in `internal/orders` and the two handlers in `cmd/teashop`. Starting the three parts now.'),
        use(
          tool('Agent', { description: 'Write the orders schema and migration', subagent_type: 'general-purpose', run_in_background: true, prompt: 'Write the Postgres schema for orders and loyalty cards and a migration under migrations/. Open a pull request.' }),
          tool('Agent', { description: 'Implement the Postgres order store', subagent_type: 'general-purpose', run_in_background: true, prompt: 'Implement orders.Store on pgx behind the existing interface, with tests against a temporary database.' }),
          tool('Agent', { description: 'Audit handlers that touch orders', subagent_type: 'code-reviewer', run_in_background: true, prompt: 'Read every handler that touches orders and list what breaks once storage is asynchronous and can fail.', model: 'sonnet' }),
        ),
        say('Three subagents are working: the **schema**, the **store** and the **handler audit**. I\'ll wire the store into `main.go` as soon as the schema is merged.'),
      ],
    },
    {
      id: X_SESSIONS.team, name: 'Low-stock e-mails', project: 'cheese', dir: tw, branch: TEAM.branch,
      provider: 'claude', model: 'claude-fable-5-2', status: 'idle', endedMinAgo: 6, lengthMin: 92, cost: 2.18, turns: 4,
      tags: ['team', 'stock'], summary: 'Team run: e-mail the counter when a cheese drops to its reorder level. Review round 2 is under way.',
      options: { sandboxed: true, team },
      messages: [
        user('E-mail the counter when a cheese drops to its reorder level. One e-mail a day with every low cheese, emptiest first.'),
        say(md(
          'Here is the plan. I\'ll keep the shelf logic and the e-mail apart, so each can be tested alone.',
          '',
          '1. **`stock.low_stock()`**: the cheeses at or below their reorder level, emptiest first. Small enough that I\'ll write it here.',
          '2. **`alerts.send_low_stock_alert()`**: builds one e-mail and sends it. This goes to the **builder**.',
          '3. **Review** of the builder\'s branch against this plan, by the **reviewer**.',
          '',
          'The plan is in `docs/low-stock-emails.md`.',
        )),
        use(tool('Write', { file_path: `${tw}/docs/low-stock-emails.md`, content: '# Low-stock e-mails\n' })),
        use(tool('Edit', { file_path: `${tw}/cheese/stock.py`, old_string: '    return any(c.name == name and c.kilos > 0 for c in SHELF)\n', new_string: '    return any(c.name == name and c.kilos > 0 for c in SHELF)\n\n\ndef low_stock() -> list[Cheese]:\n    """Cheeses at or below their reorder level, emptiest first."""\n    low = [c for c in SHELF if c.kilos <= c.reorder_at]\n    return sorted(low, key=lambda c: c.kilos / c.reorder_at if c.reorder_at else 0)\n' })),
        use(tool('Bash', { command: 'git add -A && git commit -m "stock.low_stock(): what to reorder"', description: 'Commit the helper' })),
        use(tool('mcp__eggify-tools__start_worker_session', { mode: 'build', task: 'Add cheese/alerts.py: send_low_stock_alert() e-mails the counter every cheese from stock.low_stock(), once a day. Tests in tests/test_alerts.py.' })),
        note('📬 Report from the build worker: committed "E-mail the counter when cheese runs low" on worker/7b1d4e2a-low-stock-email. 3 tests pass.'),
        use(tool('mcp__eggify-tools__start_worker_session', { mode: 'review', reviews_session_id: X_SESSIONS.builder, task: 'Review the build worker\'s branch against docs/low-stock-emails.md.' })),
        note('📬 Review round 1 of 3: CHANGES_REQUESTED. The alert is sent on every run; the plan says once a day.'),
        say('The reviewer is right: nothing remembers that an e-mail went out, so the nightly job would send one every time it runs. I\'ve sent the finding back to the builder.'),
        use(tool('mcp__eggify-tools__message_loop_session', { session_id: X_SESSIONS.builder, message: 'Review round 1: the alert must go out once a day. Remember which cheeses were reported today and skip them.' })),
        note('📬 Report from the build worker: sent_log.py remembers what was reported today; alerts skip those cheeses. Not committed yet.'),
        use(tool('mcp__eggify-tools__start_worker_session', { mode: 'review', reviews_session_id: X_SESSIONS.builder, task: 'Second review: is the alert now sent once a day?' })),
        say('The builder has added a sent log, and the reviewer is reading the branch a second time. When it approves I\'ll merge `worker/7b1d4e2a-low-stock-email` into `team/low-stock-emails` and tell you the branch is ready.'),
      ],
    },
    {
      id: X_SESSIONS.builder, name: 'Build: low-stock e-mail', project: 'cheese', dir: ww, branch: TEAM.workerBranch,
      provider: 'deepseek', model: 'deepseek-chat', status: 'idle', endedMinAgo: 9, lengthMin: 70, cost: 0.09, turns: 2, createdBefore: 2,
      tags: [], summary: 'Build worker: alerts.py e-mails the counter; adding a sent log after review round 1.',
      options: {
        sandboxed: true, spawned_by_session_id: X_SESSIONS.team,
        worker: { task: 'Add cheese/alerts.py: send_low_stock_alert() e-mails the counter every cheese from stock.low_stock(), once a day. Tests in tests/test_alerts.py.', mode: 'build', worktree_path: ww, branch: TEAM.workerBranch, base_branch: TEAM.branch, review_rounds: 1, max_review_rounds: 3, last_verdict: 'changes_requested' },
      },
      parent: X_SESSIONS.team,
      messages: [
        user('Add cheese/alerts.py: send_low_stock_alert() e-mails the counter every cheese from stock.low_stock(), once a day. Tests in tests/test_alerts.py.\n\nYou work in your own worktree on branch worker/7b1d4e2a-low-stock-email. Commit there; the session that started you merges.'),
        use(tool('Read', { file_path: `${ww}/cheese/stock.py` })),
        use(tool('Read', { file_path: `${ww}/docs/low-stock-emails.md` })),
        use(tool('Write', { file_path: `${ww}/cheese/alerts.py`, content: ALERTS_PY })),
        use(tool('Write', { file_path: `${ww}/tests/test_alerts.py`, content: TEST_ALERTS_PY })),
        use(tool('Bash', { command: 'uv run pytest -q', description: 'Run the tests' })),
        use(tool('Bash', { command: 'git add -A && git commit -m "E-mail the counter when cheese runs low"', description: 'Commit on the worker branch' })),
        say('`cheese/alerts.py` builds one e-mail listing every low cheese, emptiest first, and `send_low_stock_alert()` sends it through the `send` function it is given. Three tests pass. Committed on `worker/7b1d4e2a-low-stock-email`.'),
        user('Review round 1: the alert must go out once a day. Remember which cheeses were reported today and skip them.'),
        use(tool('Write', { file_path: `${ww}/cheese/sent_log.py`, content: 'from datetime import date\n' })),
        use(tool('Edit', { file_path: `${ww}/cheese/alerts.py`, old_string: '    cheeses = low_stock()\n', new_string: '    cheeses = [c for c in low_stock() if c.name not in _already_sent_today()]\n' })),
        say('Added `cheese/sent_log.py`, which remembers the cheeses reported today, and `send_low_stock_alert()` now skips them. `_already_sent_today()` still returns an empty set: I\'m wiring it to the log next, then I\'ll add the test and commit.'),
      ],
    },
    {
      id: X_SESSIONS.reviewer, name: 'Review: low-stock e-mail', project: 'cheese', dir: ww, branch: TEAM.workerBranch,
      provider: 'codex', model: 'gpt-6-astra', status: 'processing', endedMinAgo: 0, lengthMin: 3, cost: 0, turns: 1,
      tags: [], summary: 'Review worker, round 2: is the alert sent once a day?',
      options: {
        sandboxed: true, read_only: true, spawned_by_session_id: X_SESSIONS.team,
        worker: { task: 'Second review: is the alert now sent once a day?', mode: 'review', worktree_path: ww, branch: TEAM.workerBranch, base_branch: TEAM.branch, reviews_session_id: X_SESSIONS.builder },
      },
      parent: X_SESSIONS.team,
      messages: [
        user('Second review: is the alert now sent once a day?\n\nYou are reviewing the build worker\'s branch worker/7b1d4e2a-low-stock-email, read-only. Read the committed changes (git diff team/low-stock-emails...HEAD) and the uncommitted ones (git status, git diff). End with <verdict>APPROVE</verdict> or <verdict>CHANGES_REQUESTED</verdict>.'),
        use(tool('Bash', { command: 'git diff team/low-stock-emails...HEAD --stat', description: 'What the branch changes' })),
        use(tool('Bash', { command: 'git status --short && git diff', description: 'Uncommitted work' })),
        use(tool('Read', { file_path: `${ww}/cheese/alerts.py` })),
        use(tool('Read', { file_path: `${ww}/cheese/sent_log.py` })),
        say('The filter is in place, but `_already_sent_today()` returns an empty set, so nothing is skipped yet. Checking whether the tests would notice.'),
        use(tool('Bash', { command: 'uv run pytest -q tests/test_alerts.py', description: 'Run the alert tests' })),
      ],
    },
    {
      id: X_SESSIONS.loop, name: 'Make the lumberjack tests pass', project: 'lumber', dir: l, branch: 'feature/plank-prices',
      provider: 'claude', model: 'claude-sonnet-5-5', status: 'processing', endedMinAgo: 1, lengthMin: 24, cost: 0.57, turns: 3,
      tags: ['loop', 'tests'], summary: 'Loop: plank prices must round to the nearest penny; iterating until cargo test passes.',
      options: { mode: 'loop', dangerously_skip_permissions: true, loop: { goal: 'Plank prices round to the nearest penny and `cargo test` passes', verify_command: 'cargo test --quiet', max_iterations: 20 } },
      messages: [
        user('Goal: plank prices round to the nearest penny and `cargo test` passes.\n\nThis session runs in loop mode: after each of your turns `cargo test --quiet` is run. If it fails you get its output and try again, up to 20 times.'),
        use(tool('Read', { file_path: `${l}/src/prices.rs` })),
        use(tool('Edit', { file_path: `${l}/src/prices.rs`, old_string: '    (metres * pence_per_metre as f64).round() as u32\n}', new_string: '    (metres * pence_per_metre as f64).round() as u32\n}\n\n#[cfg(test)]\nmod tests {\n    use super::*;\n\n    #[test]\n    fn rounds_to_the_nearest_penny() {\n        assert_eq!(plank_pence(2.4, 125), 300);\n        assert_eq!(plank_pence(0.333, 100), 33);\n    }\n}' })),
        say('Added a rounding test for `plank_pence`. The module is not declared in `main.rs` yet, so the test will not run until it is.'),
        note('🔁 Loop iteration 1 of 20: `cargo test --quiet` failed.\n\nerror[E0583]: file not found for module `prices`'),
        use(tool('Edit', { file_path: `${l}/src/main.rs`, old_string: 'mod tally;\n', new_string: 'mod prices;\nmod tally;\n' })),
        say('Declared `mod prices;` in `main.rs`.'),
        note('🔁 Loop iteration 2 of 20: `cargo test --quiet` failed.\n\ntest prices::tests::rounds_to_the_nearest_penny ... FAILED\n  left: 299\n right: 300'),
        use(tool('Read', { file_path: `${l}/src/prices.rs` })),
        say('`2.4 * 125` is `299.99999999999994` in floating point and the test expects 300, so `round()` is doing its job; the trouble is an earlier truncation in `plank_pence`. Switching the sum to integer tenths of a penny.'),
        use(tool('Edit', { file_path: `${l}/src/prices.rs`, old_string: '    (metres * pence_per_metre as f64).round() as u32\n', new_string: '    let millimetres = (metres * 1000.0).round() as u64;\n    ((millimetres * pence_per_metre as u64 + 500) / 1000) as u32\n' })),
      ],
    },
    {
      id: X_SESSIONS.trust, name: 'Validate the grant form', project: 'walks', dir: w, branch: 'main',
      provider: 'claude', model: 'claude-sonnet-5-5', status: 'idle', endedMinAgo: 48, lengthMin: 5, cost: 0.12, turns: 1,
      tags: ['forms'], summary: 'Grant form validation: the amount must be positive and at most £10,000.',
      messages: [
        user('The grant form accepts negative amounts. Validate the amount: positive, and no more than £10,000.'),
        use(tool('Read', { file_path: `${w}/src/form.ts` })),
        use(tool('Edit', { file_path: `${w}/src/form.ts`, old_string: "  return problems\n", new_string: "  if (a.amountPounds <= 0) problems.push('Ask for more than nothing.')\n  if (a.amountPounds > 10000) problems.push('The Ministry grants at most £10,000.')\n  return problems\n" })),
        say('`validate()` now refuses amounts of zero or less and anything above **£10,000**, each with its own message. I have not run the tests: this folder\'s own Claude config (a format hook and allowed commands) is not loaded until you trust it.'),
      ],
    },
    {
      id: X_SESSIONS.cron, name: 'Nightly link check', project: 'docs', dir: d, branch: 'main',
      provider: 'claude', model: 'claude-haiku-5-5', status: 'idle', endedMinAgo: 34, lengthMin: 3, cost: 0.02, turns: 2,
      tags: ['docs', 'chore'], summary: 'Checks every link in the handbook each night and reports the dead ones.',
      messages: [
        user('Every night at two, check all links in the handbook and tell me which are dead.'),
        use(tool('CronCreate', { cron: '0 2 * * *', prompt: 'Check every link in handbook/ and api/ and list the dead ones.', recurring: true })),
        say('Scheduled: **every day at 02:00** I\'ll check the links in `handbook/` and `api/` and list any that are dead. Last night\'s run found none.'),
      ],
    },
    {
      id: X_SESSIONS.proposer, name: 'Plan the Stilton price update', project: 'cheese', dir: c, branch: 'main',
      provider: 'claude', model: 'claude-fable-5-2', status: 'processing', endedMinAgo: 2, lengthMin: 6, cost: 0.44, turns: 1,
      tags: ['pricing'], summary: 'Planning the Stilton price change; proposing to hand the till rounding fix to another model.',
      messages: [
        user('Stilton goes up to £24 a kilo on Monday. Work out what has to change, and get the rounding bug on the price branch fixed while you are at it.'),
        use(tool('Read', { file_path: `${c}/cheese/stock.py` })),
        use(tool('Bash', { command: 'git log --oneline main..fix/stilton-rounding', description: 'What is on the rounding branch' })),
        say(md(
          'Two separate jobs:',
          '',
          '- **The price change** is one line in the price list plus the shelf labels. I\'ll do that here.',
          '- **The rounding fix** on `fix/stilton-rounding` needs its tests finished. It is self-contained, so I\'d hand it to a build worker on a cheaper model and keep going with the prices.',
        )),
      ],
    },
    // --- older sessions, for Today / Earlier
    {
      id: X_SESSIONS.wensley, name: 'Wensleydale import script', project: 'cheese', dir: c, branch: 'main',
      provider: 'claude', model: 'claude-sonnet-5-5', status: 'idle', endedMinAgo: 2 * DAY + 130, lengthMin: 12, cost: 0.29, turns: 2,
      tags: ['import'], summary: 'CSV importer for supplier deliveries, merged to main.',
      messages: [user('Write an importer for the supplier\'s delivery CSV.'), use(tool('Write', { file_path: `${c}/cheese/importer.py`, content: 'import csv\n' })), say('`cheese/importer.py` reads a delivery CSV into `Cheese` rows. Merged to `main`.')],
    },
    {
      id: X_SESSIONS.notFound, name: 'Dead parrot 404 page', project: 'parrot', dir: p, branch: 'main',
      provider: 'claude', model: 'claude-haiku-5-5', status: 'ended', endedMinAgo: 4 * DAY + 40, lengthMin: 6, cost: 0.04, turns: 1,
      tags: ['frontend'], summary: 'A 404 page: "This page is no more. It has ceased to be."',
      messages: [user('We need a 404 page.'), use(tool('Write', { file_path: `${p}/404.html`, content: '<h1>This page is no more</h1>\n' })), say('Added `404.html`: "This page is no more. It has ceased to be."')],
    },
    {
      id: X_SESSIONS.spam, name: 'Spam filter for the contact form', project: 'walks', dir: w, branch: 'main',
      provider: 'deepseek', model: 'deepseek-chat', status: 'idle', endedMinAgo: 5 * DAY + 200, lengthMin: 8, cost: 0.02, turns: 1,
      tags: ['forms', 'spam'], summary: 'Honeypot field and a rate limit on the contact form.',
      messages: [user('The contact form gets nothing but spam, spam, spam. Stop it.'), use(tool('Read', { file_path: `${w}/src/form.ts` })), say('Added a hidden honeypot field and a limit of three messages per hour per address.')],
    },
    {
      id: X_SESSIONS.manPage, name: 'Lumberjack man page', project: 'lumber', dir: l, branch: 'main',
      provider: 'codex', model: 'gpt-6-astra', status: 'ended', endedMinAgo: 6 * DAY + 15, lengthMin: 5, cost: 0, turns: 1,
      tags: ['docs'], summary: 'man page for the lumberjack command.',
      messages: [user('Write a man page for the CLI.'), use(tool('Write', { file_path: `${l}/man/lumberjack.1`, content: '.TH LUMBERJACK 1\n' })), say('`man/lumberjack.1` documents every subcommand and flag.')],
    },
  ]
}

// ---------------------------------------------------------------- seeding

async function api(server, cookie, method, path, body) {
  return fetch(server.url + path, {
    method, headers: { 'Content-Type': 'application/json', Origin: server.url, ...(cookie ? { Cookie: cookie } : {}) },   // cookie requests must come from the app's own origin
    body: body === undefined ? undefined : JSON.stringify(body),
  })
}
const login = server => sessionCookie(server)

export async function seedExtra(server) {
  const cookie = await login(server)
  const repos = makeRepos(server.home)
  // the base seed's repositories, for sessions that work in them
  for (const [k, n] of [['teashop', 'teashop-api'], ['parrot', 'parrot-web'], ['docs', 'circus-docs']]) repos[k] = realpathSync(join(server.home, 'projects', n))
  makeSkills(server.home)

  const existing = await (await api(server, cookie, 'GET', '/api/projects')).json()
  const have = new Set((existing.projects || []).map(p => p.id))
  for (const [key, p] of Object.entries(X_PROJECTS)) {
    if (have.has(p.id)) continue
    const res = await api(server, cookie, 'POST', '/api/projects', {
      id: p.id, name: p.name, path: repos[key], description: p.description, color: p.color,
      default_provider: 'claude', default_model: 'claude-sonnet-5-5',
    })
    if (!res.ok) throw new Error(`project ${p.name}: ${res.status} ${await res.text()}`)
  }

  const db = new DatabaseSync(server.db)
  db.exec('PRAGMA busy_timeout = 5000')
  const owner = db.prepare('SELECT id FROM users WHERE username = ?').get(server.user)
  const insSession = db.prepare(`INSERT OR REPLACE INTO agent_sessions
    (id, status, created_at, updated_at, ended_at, message_count, cost_usd, num_turns, duration_ms, model_name, git_branch,
     options, provider, project_id, owner_user_id, owner_uuid, view_mode, parent_session_id)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'live', ?)`)
  const delMessages = db.prepare('DELETE FROM agent_messages WHERE session_id = ?')
  const insMessage = db.prepare(`INSERT INTO agent_messages
    (id, session_id, sequence, role, content, tool_uses, timestamp, tokens_used, user_id, provider, model_name)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
  const insDigest = db.prepare(`INSERT OR REPLACE INTO session_digests (session_id, summary, tags, source_version, updated_at)
    VALUES (?, ?, ?, ?, ?)`)

  const convs = conversations(repos)
  // parents before children (FK on parent_session_id)
  convs.sort((a, b) => (a.parent ? 1 : 0) - (b.parent ? 1 : 0))
  for (const c of convs) {
    const end = ago(c.endedMinAgo), start = ago(c.endedMinAgo + c.lengthMin)
    const options = {
      agent_name: c.name, working_directory: c.dir, model: c.model, provider: c.provider,
      project_id: PROJECTS[c.project].id, permission_mode: 'default', ...(c.options || {}),
    }
    insSession.run(c.id, c.status, sqlTime(start), sqlTime(end), c.status === 'ended' ? sqlTime(end) : null,
      c.messages.length, c.cost, c.turns, c.lengthMin * 60000, c.model, c.branch,
      JSON.stringify(options), c.provider, PROJECTS[c.project].id, server.user, owner?.id ?? null, c.parent || null)
    delMessages.run(c.id)
    c.messages.forEach((m, i) => {
      const at = new Date(start.getTime() + (end.getTime() - start.getTime()) * (i / Math.max(1, c.messages.length - 1)))
      insMessage.run(randomUUID(), c.id, i + 1, m.role, m.content, m.tools ? JSON.stringify(m.tools) : null, sqlTime(at),
        m.role === 'assistant' ? 210 + 41 * i : 0, m.role === 'user' ? server.user : null,
        m.role === 'assistant' ? c.provider : null, m.role === 'assistant' ? c.model : null)
    })
    insDigest.run(c.id, c.summary, JSON.stringify(c.tags), `${c.messages.length}:${c.status}`, sqlTime(end))
  }

  // Memories of teashop-api: two approved by the user, two an agent stored that wait for review
  const insMemory = db.prepare(`INSERT OR REPLACE INTO memories
    (id, project_id, memory_type, title, content, tags, importance, source, created_by, status, is_archived, created_at, updated_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`)
  const tea = PROJECTS.teashop.id
  const mem = (id, type, title, content, tags, importance, source, by, status, minAgo) => {
    try { insMemory.run(id, tea, type, title, content, JSON.stringify(tags), importance, source, by, status, sqlTime(ago(minAgo)), sqlTime(ago(minAgo))) }
    catch (e) { console.warn(`memory "${title}": ${e.message}`) }
  }
  mem('mem-demo-0001', 'convention', 'Prices are pence, never floats', 'Every price in this codebase is an int number of pence. Format to pounds only at the edge (templates, receipts).', ['money'], 8, 'user', 'user', 'approved', 9 * DAY)
  mem('mem-demo-0002', 'decision', 'One error per login failure', 'Login returns ErrBadCredentials for unknown user and wrong password alike, so the API never reveals which usernames exist.', ['auth', 'security'], 7, 'user', 'user', 'approved', 6 * DAY)
  mem('mem-demo-0003', 'gotcha', 'go test needs TEASHOP_DB unset', 'The order tests start their own temporary database. With TEASHOP_DB set in the shell they connect to that one instead and fail on a dirty schema.', ['tests', 'database'], 5, 'agent', `agent:${X_SESSIONS.postgres}`, 'pending', 12)
  mem('mem-demo-0004', 'preference', 'Mr. Praline wants receipts to show free pots as £0.00', 'When a loyalty card earns a free pot, print the line at £0.00 rather than full price with a discount line.', ['orders', 'receipts'], 5, 'agent', `agent:${X_SESSIONS.steer}`, 'pending', 2)
  // Each session its own avatar from the "Eggify Eggs" set (the sidebar names sessions after it)
  const eggs = db.prepare("SELECT a.id FROM avatars a JOIN avatar_themes t ON t.id = a.theme_id WHERE t.name IN ('Eggify Eggs', 'Line Cats') ORDER BY t.name = 'Eggify Eggs' DESC, a.id").all().map(r => r.id)
  if (eggs.length) {
    const order = [X_SESSIONS.steer, X_SESSIONS.postgres, X_SESSIONS.refund, X_SESSIONS.hours, X_SESSIONS.team, X_SESSIONS.loop, BASE_SESSIONS.loginBug,
      BASE_SESSIONS.teaOfMonth, X_SESSIONS.proposer, X_SESSIONS.trust, X_SESSIONS.cron, BASE_SESSIONS.priceCards, X_SESSIONS.builder, X_SESSIONS.reviewer,
      BASE_SESSIONS.apiReference, BASE_SESSIONS.copyReview, X_SESSIONS.wensley, X_SESSIONS.spam, BASE_SESSIONS.goUpgrade, X_SESSIONS.notFound, X_SESSIONS.manPage]
    const setAvatar = db.prepare('UPDATE agent_sessions SET selected_avatar_id = ? WHERE id = ?')
    order.forEach((id, i) => setAvatar.run(eggs[i % eggs.length], id))
  }
  db.close()

  for (const c of convs) {
    if (!c.tags.length) continue
    const res = await api(server, cookie, 'PUT', `/api/agent/sessions/${c.id}/tags`, { tags: c.tags })
    if (!res.ok) console.warn(`tags for "${c.name}": ${res.status} ${await res.text()}`)
  }

  // Skills: found on disk (always queued as pending), then four of them approved as the user would
  let res = await api(server, cookie, 'POST', '/api/skills/discover', {})
  if (!res.ok) console.warn(`discover: ${res.status} ${await res.text()}`)
  const pending = await (await api(server, cookie, 'GET', '/api/review/pending?kind=skill')).json()
  const approve = new Set(['tea-tasting-notes', 'release-checklist', 'complaint-reply', 'parrot-care-sheet'])
  for (const item of pending.skills || []) {
    if (!approve.has(item.skill?.name)) continue
    res = await api(server, cookie, 'POST', `/api/review/skills/${item.skill.id}/approve`, {})
    if (!res.ok) console.warn(`approve ${item.skill.name}: ${res.status} ${await res.text()}`)
  }

  // Connector keys: made-up strings, stored encrypted and never checked against the service
  for (const [slug, key] of [['github', 'ghp_demo0000notarealtoken0000demo'], ['linear', 'lin_api_demo0000notarealkey0000'], ['sentry', 'sntrys_demo0000notarealkey0000']]) {
    res = await api(server, cookie, 'POST', `/api/connectors/${slug}/connect`, { api_key: key, extra_config: '' })
    if (!res.ok) console.warn(`connector ${slug}: ${res.status} ${await res.text()}`)
  }

  // New sessions are sandboxed unless switched off (Settings → Sandbox)
  const sandbox = await (await api(server, cookie, 'GET', '/api/agent/sandbox')).json().catch(() => null)
  if (sandbox && !sandbox.default_enabled) {
    res = await api(server, cookie, 'PUT', '/api/agent/sandbox', { default_enabled: true, allowed_domains: sandbox.allowed_domains })
    if (!res.ok) console.warn(`sandbox default: ${res.status} ${await res.text()}`)
  }

  console.log(`library seed: ${convs.length} more sessions, ${Object.keys(X_PROJECTS).length} more projects`)
  return { repos }
}

/** The repositories' real paths on a seeded server (for capture scripts). */
export function repoPaths(server) {
  const out = {}
  for (const [k, n] of [['teashop', 'teashop-api'], ['parrot', 'parrot-web'], ['docs', 'circus-docs'], ['cheese', 'cheese-shop'], ['walks', 'silly-walks'], ['lumber', 'lumberjack-cli']]) {
    out[k] = realpathSync(join(server.home, 'projects', n))
  }
  return out
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const args = parseArgs()
  await seedExtra(readServer(Number(args.port || 3420)))
}

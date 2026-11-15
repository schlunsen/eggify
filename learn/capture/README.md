# Screen captures for the learning videos

Shots and clips are taken with Playwright from a **throwaway Eggify server** that runs with a
temporary home folder, an empty environment and invented demo data. Nothing from a real install
may appear in a shot (`../../docs/MEDIA_PROVENANCE.md`).

```
capture/
  demo-server.sh   builds and starts the server, creates the user, seeds, cleans up on exit
  seed.mjs         the fictional workspace (user, git repositories, projects, sessions, tags)
  lib.mjs          Playwright helpers: openApp, go, settle, shot, drawBoxes, recordClip, stubJson, serveLanding
  package.json     playwright + sharp (own node_modules; `npm install && npx playwright install chromium`)
  bin/ logs/ .cache/   build output, logs, proof sheets, the landing page copy (not committed)
```

## Taking an episode's shots

```bash
cd learn/capture && npm install && npx playwright install chromium     # once

learn/capture/demo-server.sh --port 3401 --fresh       # terminal 1 (Ctrl-C stops it and deletes its data)
node learn/episodes/01-quick-start/capture.mjs         # terminal 2; [--only install,setup,…] [--no-landing] [--clip]
```

Output: `learn/public/episodes/<slug>/shots/<name>.png` (3840×2160) and `<name>.json`
(`{"boxes": {id: {x, y, w, h}}}` in 1920×1080 CSS px). After every run **look at the proof
sheets** in `learn/capture/.cache/boxes/<slug>/<name>.jpg`: the shot at 1920×1080 with its
boxes drawn and named.

`demo-server.sh` options: `--port N` (default 3401), `--fresh` (no user yet, so `/setup` is
shown; seed it later with `node learn/capture/seed.mjs --port N`), `--keep` (leave
`/tmp/eggify-learn-N`), `--rebuild` (frontend + binary), `--stop --port N` (stop one started in
the background). The first run builds the frontend (`npm ci && npm run generate`, a few
minutes) and `learn/capture/bin/eggify`; later runs reuse both, so pass `--rebuild` after the
app changed. When it is up it prints `DEMO_SERVER {json}` and writes
`/tmp/eggify-learn-<port>/server.json` (`url, port, user, password, home, db, fresh, pid`),
which `readServer(port)` reads.

**Ports: 3400 + episode number** (episode 1 → 3401, episode 2 → 3402), so several episodes can
be captured at once. The landing page copy is served on 4390 while `install` is shot.

How the server is kept clean:
- `env -i`: no provider keys, tokens or names from the real machine. `PATH` is
  `/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin` (git, sqlite3; a Homebrew `claude` is found
  there too, but it is not logged in under the demo home).
- `HOME=/Users/Shared/praline` (a sibling name such as `/Users/Shared/mrpraline` on other ports, so
  several servers can run at once; removed again on exit), and the server is started **from that folder**:
  Create Session offers the server's working directory as the default folder, which would
  otherwise be the real checkout path.
- Plain HTTP on loopback, `EGGIFY_SECRET_STORE=file`, no browser, random password per start.
- Logins are rate limited per address (a 429 after a handful), so `lib.mjs` keeps one session
  token in `/tmp/eggify-learn-<port>/session-cookie` and reuses it.

## What the seed contains

User **praline** ("Mr. Praline", git author `Mr. Praline <praline@example.com>`), admin, password
in `server.json`.

Projects (real git repositories under `~/projects`, i.e. `/Users/Shared/praline/projects/…`
on screen, since macOS resolves `/tmp`):

| Project | Id | State |
| --- | --- | --- |
| `teashop-api` (Go) | `aaaaaaaa-0000-4000-8000-000000000001` | on `fix/empty-password`, 1 modified + 1 untracked file, 3 commits on `main` |
| `parrot-web` (TypeScript) | `…000000000002` | on `feature/price-cards`, clean, 1 commit ahead of `main` |
| `circus-docs` (Markdown) | `…000000000003` | on `main`, 1 untracked draft |

Sessions (`SESSIONS` in `seed.mjs`; open one with `/agents?session=<id>`). The web app shows a
session under its **avatar name** (a cat name such as "Ray Pawston", picked from the id), not
under `agent_name`; the names below are the seed's `agent_name`, which iOS and fallbacks show.

| Key | Id (`11111111-2222-4333-8444-…`) | `agent_name` | Project | Model | Status | Tags |
| --- | --- | --- | --- | --- | --- | --- |
| `loginBug` | `…555555555501` | Fix the empty-password login | teashop-api | claude-sonnet-5-5 | idle, minutes ago | bug, auth |
| `priceCards` | `…02` | Price cards for the shop page | parrot-web | claude-sonnet-5-5 | idle, ~3 h ago | frontend, design |
| `teaOfMonth` | `…03` | Tea of the month endpoint | teashop-api | claude-opus-5-5 | **processing** | feature |
| `apiReference` | `…04` | Write the API reference | circus-docs | deepseek-chat | idle, yesterday | docs |
| `copyReview` | `…05` | Review the returns page copy | parrot-web | gpt-6-astra (codex) | idle, yesterday | review |
| `goUpgrade` | `…06` | Upgrade to Go 1.26 | teashop-api | claude-haiku-5-5 | ended, 3 days ago | chore |

Each has a conversation with tool calls (Read, Grep, Glob, Edit, Write, Bash), a one-line
summary and topic tags in `session_digests`, and user tags. `loginBug` is the one made to be
opened: its edit and its new test file are really in the repository, uncommitted, so the Git
panel agrees with the chat. Timestamps are relative to the moment of seeding; seeding again
refreshes them (`INSERT OR REPLACE`).

`teaOfMonth` is sidebar dressing: it is stored as `processing`, so the sidebar shows a Working
group, but no agent runs. **Do not open it** (its Git panel says "session not found" and the
model picker says Busy). A server restart turns it idle.

Projects and tags go through the REST API; sessions, messages and digests are written to the
SQLite file with `node:sqlite` while the server runs (as `scripts/ios-e2e-server.sh` does).

## Writing `capture.mjs` for a new episode

```js
import { openApp, go, shot, settle, readServer, parseArgs, drawAllBoxes, stubJson, recordClip } from '../../capture/lib.mjs'
import { SESSIONS, PROJECTS } from '../../capture/seed.mjs'

const server = readServer(Number(parseArgs().port || 3402))      // 3400 + episode number
const { browser, page } = await openApp(server)                   // 1920×1080 @2x, signed in, default theme
await go(page, server, `/agents?session=${SESSIONS.priceCards}`)  // goto + settle
await shot(page, 'projects-and-tags', 'sidebar', {
  boxes: { tags: '.session-item .tag-chip >> nth=0', newSession: page.getByRole('button', { name: /New Session/ }) },
})
await browser.close()
await drawAllBoxes('projects-and-tags')                           // then look at .cache/boxes/…
```

- `shot()` settles first (fonts, on-screen images, finite animations, no loaders), hides carets
  and scrollbars, and **fails** when a box is missing or outside the viewport. A box is a
  selector, a Locator or `{x, y, w, h}`.
- The context uses `prefers-reduced-motion: reduce` (the app then draws loaders and working
  states still), locale `en-GB`, the theme pinned to the app default (Puppet Theatre Light).
  `openApp(server, { reducedMotion: false })` keeps motion.
- `go()` reloads when only the `#hash` changed (a hash change alone would not refetch).
- `stubJson(page, '**/api/…', body)` answers a route with fixed JSON: fictional data shown
  through the real UI. Say in the script's header what is stubbed. `openApp` always stubs
  `/api/system/usage` (the header's CPU/memory meter would otherwise show the capturing
  machine's load in changing colours); `{ realUsage: true }` turns that off.
- Never inject HTML or edit the DOM to fake a state. Hiding flicker with CSS is fine.
- `recordClip(server, slug, name, async (page, { mark }) => { … })` records
  `public/episodes/<slug>/clips/<name>.mp4` (1920×1080, 30 fps, H.264, motion on; `mark()` cuts
  everything before it). Tested with `capture.mjs --clip` of episode 1 (`session-scroll.mp4`).
- `serveLanding()` copies `landing/` to `.cache/landing`, builds it there and serves `dist/`
  (the `landing/` folder is not touched). The build asks `https://eggify.xyz/download/latest.json`
  for the version number and falls back to `internal/version/version.go`.

## Episode 1 notes

Stubbed: `/api/agent/runtimes` (Claude Code 2.1.321 signed in as `pr•••@example.com` on Claude
Max, Codex 0.162.0 installed and not logged in; variants: login running, both ready, both
logged out), `/api/agent/runtimes/codex/login` for `runtimes-login`, `/api/system/usage`.
The true answer on this Mac under the demo home was "Claude Code 2.1.311: Not logged in, update
advised" and "OpenAI Codex: Not installed".

On a `--fresh` server the script goes through the first run for real: it fills and **submits**
`/setup`, shoots Agents & logins and Providers on the empty install (header says "No Project"),
saves a dummy DeepSeek key (`sk-demo-0000-not-a-real-key-0000`, masked on screen), then seeds
and shoots Create Session and the session. On a server that already has its user, `setup` is
skipped.

## Limits

- **No live agent turn.** Everything in a chat is stored history. Not shown honestly without a
  running agent: streaming text, "Processing your message…", approval cards and questions,
  subagents, loop/worker cards, the Zen stage reacting, the Activity row (it says "No tool
  calls yet" for seeded sessions), context use (0 %).
- **Is a live turn possible under the demo home?** Not with what is on this Mac without a real
  account or paid calls, so none was run. `claude` is found (Homebrew path) but is not logged
  in under the temporary home, and logging it in would put a real account into the demo;
  `codex` is not installed; provider keys are deliberately absent. An untested way that costs
  nothing: point the **Custom** provider (loopback `http://` base URLs are allowed) at a small
  fake OpenAI-compatible server that streams a scripted reply and tool calls, as
  `internal/server/agents/direct_provider_turns_test.go` does; that would give real streaming,
  tool cards and status changes on Eggify's own loop (not the Claude CLI path).
- Paths on screen start with `/Users/Shared/praline/` where the app shows them in full (Create Session's
  Working Directory). A tidier path would need a home outside `/tmp`.
- Tool cards show the call (file name, command); stored results are not shown inline.
- The chat's simple formatter renders bold, inline code (each with a Copy button) and code
  blocks; `- ` list lines stay literal, so seeded replies avoid lists.
- The Agents & logins wizard dialog (`AgentSetupWizard`) only opens by itself in the desktop
  app; in a browser the same cards live in Settings → Agents & logins, which is what is shot.

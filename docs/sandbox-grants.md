# Sandbox that builds, hides personal secrets, and takes grants

Design note for making sandboxed Claude sessions usable every day. It records what was measured
on a real Mac (Claude Code 2.1.311, 2026-11-15) and what to build from it.

## What was measured

`scripts/tests/sandbox-probe.sh` runs about 50 everyday developer commands and prints one line
each. Run inside Claude Code's sandbox with the settings `buildClaudeSessionSettings` produced
before this change:

- Blocked, as intended: Eggify's API key and database, writes outside the project, `.git/hooks`,
  `.git/config`, `.mcp.json`, `~/.zshrc`, LaunchAgents, sites off the allowlist, localhost.
- Broken, which is why nobody keeps the sandbox on: `go build` / `go test` (build cache in
  `~/Library/Caches/go-build`), `npm install` / `npx` (`~/.npm`), `uv` (`~/.cache/uv`), `gh` (no
  login, and TLS verification fails), git over SSH, a dev server on localhost.
- Not protected: `~/.ssh/id_rsa`, `~/.config/gh`, shell history and other personal credentials
  were readable, with GitHub and npm on the egress allowlist.

Then settings were changed until the probe passed. What worked (each verified with a real turn):

| Goal | Setting that works |
| --- | --- |
| Go builds, tests, new modules | `allowWrite` on a cache folder; env `GOCACHE=<c>/go-build`, `GOPATH=<c>/gopath`, `GOFLAGS=-modcacherw`, `GOPROXY=file://<real module cache>/cache/download,https://proxy.golang.org,direct`. The `file://` proxy feeds the new module cache (and the Go toolchain zip) from the user's existing one without network; without it the toolchain download is redirected to `storage.googleapis.com`, which is not allowlisted. `GOPATH` (not only `GOMODCACHE`) is needed because the checksum database cache lives in `$GOPATH/pkg/sumdb`. |
| npm, npx, uv, pip | env `npm_config_cache`, `UV_CACHE_DIR`, `PIP_CACHE_DIR` inside the cache folder |
| Hide personal secrets | `filesystem.denyRead` on `~/.ssh`, `~/.config/gh`, `~/.zsh_history`, … (blocks `ls` and `cat`) |
| Re-open one file inside a hidden folder | `filesystem.allowRead` (`~/.ssh/known_hosts`); it wins over `denyRead` |
| Use SSH keys without reading them | `network.allowUnixSockets` with the ssh-agent socket **and its directory** (the raw `SSH_AUTH_SOCK` value alone was refused; with `[sock, dirname(sock)]` `ssh-add -l` listed the key), plus an ssh `ProxyCommand` that tunnels through the sandbox's HTTP proxy with `CONNECT host:22` and the proxy's Basic credentials from `HTTPS_PROXY` (`scripts/tests/sandbox-connect-reference.pl` is the working reference). `git ls-remote git@github.com:…` then works while `cat ~/.ssh/id_rsa` fails. The proxy only tunnels to allowlisted domains: `gitlab.com:22` was refused. `nc -X 5` cannot be used: the proxy needs credentials. |
| `gh` | env `GH_TOKEN` (from `gh auth token`, taken outside the sandbox), `GH_CONFIG_DIR` pointing into the cache folder (gh refuses to start when it cannot read its config folder), and `enableWeakerNetworkIsolation: true` (lets Go programs verify TLS certificates through `trustd`) |
| A dev server on localhost | `network.allowLocalBinding: true`. **It also makes every local service reachable**: `curl http://127.0.0.1:3335/api/health` (Eggify itself) answered 200. There is no per-port setting. |

Not solved: `mktemp` without `-t` uses the per-user temp folder under `/var/folders`, which stays
read-only (`$TMPDIR`, `/tmp/claude-<uid>`, is writable). Allowing all of `/tmp` was tried and
rejected: other sessions' scratch folders live there.

## What to build

### 1. Builds work (every sandboxed Claude session)

- A cache folder per project, `~/.cache/eggify-sandbox/<key>/` (`key` from the working
  directory's project, 0700, created at session start), in `filesystem.allowWrite`.
- The cache variables above, table-driven so more tools can be added, each only when it is safe
  (a variable that moves credentials or config, such as `CARGO_HOME` or `GRADLE_USER_HOME`, is not).
- Shared caches stay read-only on purpose: a poisoned shared cache would run outside the sandbox
  later.
- Default allowlist gains `raw.githubusercontent.com`, `crates.io`, `static.crates.io`,
  `index.crates.io`.

### 2. Personal secrets are hidden (every sandboxed session)

`denyRead` for the sandbox and `Read(...)` deny rules for the file tools, for: `~/.ssh`,
`~/.gnupg`, `~/.aws`, `~/.azure`, `~/.config/gcloud`, `~/.config/gh`, `~/.kube`,
`~/.docker/config.json`, `~/.netrc`, `~/.npmrc`, `~/.pypirc`, `~/.cargo/credentials*`,
`~/.gem/credentials`, `~/.config/eggify`, shell histories, `~/Library/Keychains`,
`~/Library/Cookies`, browser profiles, Mail and Messages. A path that contains the session's
working directory is skipped, as for Eggify's own folder.

### 3. Grants (chosen by a signed-in admin when the session is created)

`SessionOptions.SandboxGrants` (ids) and `SandboxGrantPaths` (extra folders or files):

| Grant | What it opens | What the agent can then do |
| --- | --- | --- |
| `ssh` | ssh-agent socket, `known_hosts` and `config` readable, `GIT_SSH_COMMAND` through `eggify sandbox-connect` | Use the keys in the agent against allowlisted hosts. It cannot read a private key. |
| `github` | `GH_TOKEN`, `GH_CONFIG_DIR`, trust-store lookups, git's HTTPS credential helper | Act as you on GitHub. Commands can read the token. |
| `cloud` | `~/.aws`, `~/.azure`, `~/.config/gcloud`, `~/.kube` readable, trust-store lookups | Read and use those credentials. |
| `npm` | `~/.npmrc` readable | Publish as you. |
| `localhost` | `allowLocalBinding` | Start and reach local servers, **and reach every other service on this Mac's localhost, Eggify included**. |
| a path | that folder or file readable, optionally writable | Whatever is in it. Never Eggify's own protected paths. |

Rules: only a signed-in admin can set grants (like YOLO and the sandbox opt-out; the API key
never can), they are fixed at creation, shown on the session, and dropped from handovers and from
sessions started by agents (workers, loops). A secret never goes on a command line: `GH_TOKEN`
travels in the CLI's environment, not in `--settings`.

### Later

- A card when the sandbox blocks something ("allow crates.io for this session / always / no",
  "run this one command outside the sandbox").
- Grants for Codex sessions.
- Making the sandbox the default for new sessions once the probe passes on a clean install.

## As built (deviations from the plan above)

- **`GOMODCACHE` is also set** (to `<cache>/gopath/pkg/mod`): the server's environment is passed on, and an inherited
  `GOMODCACHE` would win over `GOPATH` and point at the read-only module cache.
- **pnpm is left out**: its store variable is `npm_config_store_dir`, which npm itself reads and warns about as an unknown
  config. Yarn (`YARN_CACHE_FOLDER`) and bun (`BUN_INSTALL_CACHE_DIR`) were added.
- **Variables are set in the CLI's environment** (`opts.WithEnvVar`), not in the settings `env` key.
- **Path grants never reopen a hidden credential folder** (stricter than the plan): a path may not be, contain or lie inside
  one; use the `cloud` / `npm` / `ssh` grants for those. Write grants also refuse shell start-up files and
  `~/Library/LaunchAgents`, the sandbox cache root, and paths that must stay read-only (`sandboxWriteProtected`).
- **A grant that stopped being available** (ssh-agent quit, `gh` signed out) is left out at CLI start with a log warning
  instead of failing the session; it is checked strictly at creation.
- **`GIT_SSH_COMMAND`** from the server's environment is replaced by the ssh grant's (no wrapping).
- **Auto-handoff successors lose their grants** (they go through a handover, which never carries them).
- **Not supported yet**: Codex and OpenAI-compatible sessions (refused at creation with a message; a model switch onto them is refused).

### Review round 1

- **Cache variables reach sandboxed commands only.** They are no longer in the CLI's environment (hooks and stdio MCP servers run
  outside the sandbox and would have resolved packages from a folder the agent can write). The CLI sources
  `CLAUDE_ENV_FILE` before each sandboxed Bash command (verified live on 2.1.311), so the exports live in
  `~/.claude/eggify/sandbox-env/<session id>.sh`: 0600, readable in the sandbox, write-denied there and for the Edit tool,
  every value single-quoted, removed with the session and swept at startup. A `CLAUDE_ENV_FILE` already in the server's environment is
  sourced first. Not written when the session works inside `~/.claude/eggify` itself (the sandbox does not write-protect it then): such a session has no cache variables.
  `GH_TOKEN`, `GIT_SSH_COMMAND` and the git credential config stay in the CLI environment.
- **The cache is keyed by trust class** (`sandboxCacheClass`), not just project: Go does not re-verify build-cache entries or
  extracted modules, so what a session writes to its cache runs in whoever uses it next. Separate folders for plain user sessions,
  sessions with grants, read-only sessions, and each spawning session's workers (per mode). Trade-off: the first build in each class
  is slower (the file `GOPROXY` still feeds modules from the user's own module cache without network).
- **`GH_CONFIG_DIR` is an empty folder under `~/.claude/eggify/sandbox-env/gh-<session id>`** that the agent cannot write. Checked with `gh` (this Mac):
  `gh config list` and `gh auth git-credential` work against an empty read-only config folder and write nothing there. Not checked: `gh` subcommands
  that save state (`gh auth login`, `gh config set`) will fail, which is intended.
- **ssh-agent socket**: the socket is always opened in both spellings (raw and symlink-resolved). Its folder is opened only while the constant
  `sshGrantAddsSocketFolder` is true (the first live test needed it, but only with the unresolved `/var/run/…` spelling): flip it to false to test
  whether the socket alone suffices. Whether a folder entry is recursive is **unknown**, so it is treated as recursive: with the folder opened the grant is
  unavailable when the folder is the home folder, `/tmp`, `/var/run` (and their `/private` forms), the per-user temp folder itself, a hidden credential
  folder or anything holding or inside one, or when it holds any other socket.
- **Path grants are stored resolved** and re-resolved at each start; a path whose components the session can itself write (its working directory,
  its cache, another write grant) is refused at creation, since the agent could swap it for a link.
- Availability checks are cached 30 s (`gh`) or cheap (a stat and a directory listing). `findGH` uses Go's `exec.LookPath("gh")` on the server's own PATH (Go refuses a result that resolves relative to the current directory), then `/opt/homebrew/bin`, `/usr/local/bin`, `/usr/bin` and `~/.local/bin`. It runs in the server's process, so the folder it could look at is the server's working directory, never a session's; it is not otherwise restricted to fixed folders.
- The ssh grant's "another socket in the folder" check is made when the dialog asks and at each CLI start, not continuously: a socket created in that folder later is not noticed until the next start.
- The `ssh` risk text says the session can authenticate and sign with your keys on the hosts it can reach; `github` and `cloud` say that TLS trust-store lookups are enabled (`enableWeakerNetworkIsolation`).
- With user authentication off, the API key can set grants (as for YOLO and the sandbox opt-out): nothing tells the human from an agent then.
- Further hidden credential entries: `~/.git-credentials`, `~/.pgpass`, `~/.my.cnf`, `~/.appstoreconnect`, `~/.config/op`, `~/.terraform.d/credentials.tfrc.json`, `~/.vault-token`, `~/.config/doctl`, `~/.config/hub`, and on macOS 1Password's app folders.
- `~/.claude/eggify/sandbox-env` and `launchers` are always write-protected for sandboxed sessions, and the startup sweep only removes entries older than a day, older than the process and of no live session.
- A session started by another session that also has grants gets its own granted cache, not the workers'.

## Prompts off inside the sandbox ("Autopilot"): measured gap, 2026-11-15

Most people run with permission prompts off (YOLO). The aim is that prompts off **inside the
sandbox** is the default way to run. One thing stands in the way, measured live on Claude Code
2.1.311 with the sandbox settings on and `--permission-mode bypassPermissions` (how Eggify runs
YOLO, and how team workers run today):

- The sandbox confines **Bash** only. The **Write** and **Edit** tools wrote
  `~/eggify-yolo-probe.txt` and a file in `~/Library/LaunchAgents/` without being stopped. Only
  the explicit deny rules (Eggify's own files, the hidden credentials, `.git/hooks`, …) hold.
- So a sandboxed session with prompts off, including every build worker, can still plant a
  LaunchAgent or edit `~/.zshrc` through the file tools.

### What was built

A Claude CLI session that is **sandboxed with prompts off** (`DangerouslySkipPermissions`, or
permission mode `yolo` / `bypassPermissions` / `allow-all`; every sandboxed build and review worker
is one) no longer starts in the CLI's bypass mode and gets neither skip flag
(`autopilotSession`, applied in `buildSDKOptions` and both prompt start paths). It runs in the
CLI's **`default`** mode with Eggify's permission callback attached, and the callback
(`createPermissionCallback`, first thing) answers every request at once with `autopilotDecide`
(`agents/autopilot.go`), a pure function of the tool, its input and the session's scope. No card,
no waiting, and it works with no browser connected. Why `default` and not `acceptEdits`:
`acceptEdits` would let the CLI approve edits itself, by its own idea of the working directory
(and its additional directories); in `default` mode the CLI asks about every Write/Edit, so the
decision is Eggify's. Sandboxed Bash is still auto-allowed by the CLI (`autoAllowBashIfSandboxed`),
read tools inside the working directory too; deny rules (Eggify's files, hidden credentials,
read-only sessions) are applied by the CLI before it asks.

| Tool | Decision |
| --- | --- |
| `Write`, `Edit`, `MultiEdit` (`file_path`), `NotebookEdit` (`notebook_path`), and any other built-in tool that names a file and is not a known read tool | allowed inside the **write scope**, else denied with "This session is sandboxed: it can only change files inside <working directory>. Ask the user to add this folder to the session's access." |
| `Bash` | allowed (the sandbox confines it); denied when the call asks to leave the sandbox (`dangerouslyDisableSandbox`) |
| `Read`, `Glob`, `Grep`, `LS`, … | allowed (hidden paths are denied by the deny rules) |
| `WebFetch` | allowed to allowlisted hosts (`sandboxWebToolDecision`), else denied at once, naming Settings → Sandbox |
| `WebSearch` | denied at once (it has always needed the user; it runs outside the sandbox's egress proxy) |
| `AskUserQuestion`, `ExitPlanMode` | **not decided here**: they take the normal path (question card / permission card) |
| everything else (MCP tools, `Agent`/`Task`, `TodoWrite`, …) | allowed, as YOLO did |

**Write scope** (`newAutopilotScope`): the working directory (resolved), the write path grants
(re-validated like at CLI start), and the CLI's scratch folder `/tmp/claude-<uid>/<working dir with non-alphanumerics as ->/` (and its
`/private` form; not the shared `/tmp/claude-<uid>` itself, which holds the CLI's settings and each session's `tasks/`, refused). A working directory that is `/`, the home folder or holds it gives the file
tools **no** folder (stricter than the sandbox's own `.` for commands). **Never writable**, even
inside the scope: `sandboxWriteProtected` (`.mcp.json`, `.git/hooks|config|info|commondir`, Claude
settings, …), the project's `.claude/hooks`, Eggify's protected paths, the hidden personal
credentials, `~/.claude`, `~/.config` (unless the session works inside them). Protected paths are
compared case-insensitively on macOS.

**Resolving a target**: clean the path (relative ones against the working directory, `~`
refused), resolve the deepest existing ancestor through symlinks and append the rest (which does
not exist, so holds no links; a dangling link is refused), then require the result inside a write
root and neither spelling inside a protected path. A link inside the project that leads outside the
scope is refused with its own message.

**Questions and confirmations**: tool-confirmation cards (`toolguard`) are asked by eggify-tools'
control endpoints, not by the CLI's permission request, so they are unchanged and still ask,
whatever the mode. `AskUserQuestion` reaches the callback in `default` mode like any tool and is
handed to the question machinery before anything is decided.

**Subagents**: the CLI sends every permission request, a subagent's included, through the one
control channel the SDK offers, and a subagent inherits the parent's mode, so they get the same
decisions (read from the SDK and CLI behaviour; the live test covers only the main agent).

### Remaining limits

- **Check, then write.** The decision is made when the CLI asks; the write happens a moment later.
  A sandboxed command that swaps a path component for a symlink in between (the project is
  writable to commands) can redirect the write. Reduced, not closed: the target is resolved at the
  time of the check, and a link that already leads out is refused. Hard links to files outside the
  project are not seen.
- **Project allow rules**: an Autopilot session's flag settings carry `permissions.ask` for
  `Edit`, `Write`, `MultiEdit`, `NotebookEdit`, so a trusted project's `permissions.allow` cannot
  approve a file write without Eggify (`TestLiveSandboxAutopilot` tries it). `sandbox.excludedCommands` in a
  project's settings makes a sandboxed session refuse to start.
- **YOLO switch**: turning it off also writes permission mode `default` (on writes `yolo`), so a
  stale mode cannot keep prompts off.
- A write through Bash is confined by the sandbox, as before; only the file tools are decided here.
- Not live-tested from a worker (no nested CLI): `TestLiveSandboxAutopilot`
  (`EGGIFY_LIVE_SANDBOX=1`) writes inside the project (must work), `~/eggify-autopilot-probe.txt`
  and `~/Library/LaunchAgents/…` (must not) and runs `go version`.

### Autopilot as the default way to run

Create Session has one choice for the Claude CLI, "How should it work?": **Autopilot** (YOLO +
sandbox, grants shown under it), **Ask first** (prompts on; the sandbox switch under it) and
**Full access** (YOLO, no sandbox, marked as risky). It sets the same fields as before
(`permission_mode`, YOLO flags, `sandboxed`). The default is the last choice (localStorage
`eggify-work-mode`), else Autopilot. Codex and OpenAI-compatible providers keep the permission
mode and the sandbox switch, with a line saying why. `sandbox.default_enabled` is **on until the
user saves a choice** (an explicit false is respected); until then only Claude CLI sessions follow it
(`SandboxPreferences.DefaultIsImplicit`: Codex and the OpenAI-compatible loop start as before;
`GET /api/agent/sandbox` reports `default_implicit`, saving only the allowlist keeps it implicit, and
while it is implicit an explicit `sandboxed: true` on the OpenAI-compatible path, as the released
iPhone app sends it, starts unsandboxed with a chat notice instead of failing; Codex keeps an explicit true).
Choosing Autopilot or Full access is still the YOLO privilege (signed-in admin or the only
account); an unsandboxed session while the default is on needs a user too
(`SandboxOptOutAction`). The sidebar names the combination: "Autopilot · sandboxed", "Ask first",
"Full access · not sandboxed". Toggling YOLO there moves between Autopilot and Ask first (sandboxed)
or Full access and Ask first (not); the sandbox stays fixed at creation.

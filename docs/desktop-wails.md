# Wails v3 desktop app

Status: macOS (Apple Silicon) releases are signed, notarized and self-updating, downloadable from
the release folder you publish to, e.g. https://example.com/download/Eggify.dmg (see *Releases* and
*Self-update* below). Lives next to the
Tauri app (`src-tauri/`), which is unchanged. Wails version: `v3.0.0-beta.27`.

## Build and run

```bash
# Prerequisites: Go 1.26, Xcode CLT, Node (frontend), optionally the wails3 CLI
go install github.com/wailsapp/wails/v3/cmd/wails3@latest   # only for `wails3 doctor` / signing helpers
just build-frontend                  # internal/server/frontend/.output (embedded by the server)

just desktop-wails-build             # -> dist/wails/Eggify.app (ad-hoc signed, local)
just desktop-app                     # -> same, signed with the Developer ID (see Releases)
open dist/wails/Eggify.app

just desktop-wails-dev               # go run from source (cmd/eggify-desktop)
just desktop-wails-test              # vet + tests of the desktop module
```

The build is `scripts/build-desktop-wails.sh`: `go build -tags production` of
`cmd/eggify-desktop`, then a hand-assembled `.app` (`Info.plist` from
`cmd/eggify-desktop/build/darwin`, icon from `src-tauri/icons`, sherpa-onnx dylibs copied
into `Contents/Frameworks` with the rpath rewritten, like `scripts/package-release.sh`).
We don't use the `wails3 init` Taskfile layout; the app has no Vite frontend of its own.

Environment knobs:

| Variable | Effect |
|---|---|
| `EGGIFY_DESKTOP_PORT` | Loopback port (default `3335`; CLI uses 3333, Tauri 3334). Falls back to a random port if taken. |
| `EGGIFY_SKIP_SHELL_ENV=1` | Don't import the login-shell environment. |
| `EGGIFY_VERBOSE=1` / `--verbose` | Verbose server logging. |
| `EGGIFY_DESKTOP_DIAG=1` | Logs page state, CSP violations, console errors and WebSocket open/close from the webview to stderr (test runs). |
| `EGGIFY_DESKTOP_DIAG_SCRIPT=<file>` | With `DIAG=1` only: extra JS injected on every page (used to drive login in automated checks). |

Logs: the desktop shell logs to stderr with the `[eggify-desktop]` prefix; the server logs to
`~/.claude/eggify/logs` as usual.

## Architecture

```
Eggify.app/Contents/MacOS/eggify-desktop  (one process, one Go toolchain)
├── shellenv.Import()          $SHELL -l -i -c env  → os env (before config is read)
├── internal/server (in-process, desktop mode)
│     plain HTTP on 127.0.0.1:3335; TLS off; auth unchanged
│     + optional ngrok tunnel as an extra listener (Settings / tray)
│     + desktop routes: /__desktop/bootstrap, /wails/*  (token-gated)
└── Wails app
      window → wails://localhost/ (splash) → http://127.0.0.1:3335/__desktop/bootstrap?t=… → /
      services: DesktopService (SelectFolder, OpenExternal, Notify), notifications
      tray (incl. Remote access on/off, Copy public URL), single instance, window state,
      deep links (eggify://)
```

### Decision: load the loopback URL directly (option a)

The window navigates straight to the in-process server on `http://127.0.0.1:<port>`. The page,
`/api`, `/agent/ws` and `/ws` are then **same-origin**, so nothing about the web app changes:

- **WebSockets** work as in a browser. Wails' asset server (option b) cannot proxy WebSocket
  upgrades, so serving the SPA through it would have meant cross-origin WS to a loopback port.
- **CSP** is the server's normal policy (`connect-src 'self' ws://<host>`), no desktop variant.
- **Session cookie**: with TLS off the cookie is not `Secure`, and WebKit stores it for
  `http://127.0.0.1`. Under a custom `wails://` scheme WKWebView ignores `Set-Cookie` from
  scheme-handler responses, which would have broken user auth entirely.
- It is the Tauri architecture minus the sidecar and minus the self-signed cert.

**Why plain HTTP is acceptable here, and only here** (`internal/server/desktop.go`):
`EnableDesktopMode` forces `tls.enabled=false`, `server.host=127.0.0.1` and ignores the CLI's `--tunnel`,
in memory only (config.json is not rewritten). `Server.Serve(ln)` refuses to run outside desktop
mode or on a non-loopback listener. Traffic never leaves the machine; TLS on loopback protects
against nobody who couldn't already read `~/.claude/eggify`. The CLI is untouched (tests:
`internal/server/desktop_test.go`). The auth model is unchanged: the API key is still "a local
process", privileged actions still need a signed-in user, the WebSocket is attributed to the
user via the session cookie.

### Remote access (ngrok)

The loopback listener stays the only way the webview reaches the server. Remote access adds a
second, public listener: an ngrok tunnel served by the same in-process Fiber app
(`internal/remoteaccess`, see CLAUDE.md → *Remote access (ngrok)*). It is switched on from
Settings → Remote access or the tray, and at launch when "Start remote access when Eggify
starts" is on (`Server.Serve` runs the autostart).

- **Tray** (`cmd/eggify-desktop/remote.go`): "Remote access: Off/Connecting…/On/Failed" toggle,
  "Copy public URL" (enabled while connected), "Remote access settings…". It calls the
  in-process `remoteaccess.Manager` (`srv.RemoteAccess()`), not the HTTP API with the API key:
  the tray is native UI only the person at the machine can click. If starting is refused (no
  token, user auth off, no admin yet) it notifies and opens `/settings#access`.
- **Notifications**: native notification when the tunnel comes up at a new URL or fails;
  a session that drops and returns at the same URL stays quiet.
- **Over the tunnel** the desktop bridge (`/__desktop/*`, `/wails/*`) answers 404
  (`tunnelPathGuard`), on top of its own token checks.
- "Get your authtoken" and the public URL open in the system browser through
  `DesktopService.OpenExternal`.

**Native bindings over the same origin.** Wails' JS runtime calls
`fetch(location.origin + "/wails/runtime")`. Because the page is served by Fiber, the desktop
module registers `/wails/*` on the Fiber app and forwards it to Wails' own asset handler, which
it obtains by implementing `application.AssetServerTransport` (a wrapper around the default
`HTTPTransport`, `cmd/eggify-desktop/bridge.go`). Any local process can reach the port, so
`/wails/*` requires a per-launch 256-bit token: the window is first sent to
`/__desktop/bootstrap?t=<token>`, which sets it as an `HttpOnly; SameSite=Strict; Path=/wails`
cookie and redirects into the dashboard. Requests with a foreign `Origin` are refused and the
dashboard's cookies are stripped before forwarding to Wails. `OpenExternal` only opens
`http(s)`/`mailto` URLs.

**Frontend adapter** (`app/utils/desktop.ts`): `desktopShell()` → `wails | tauri | web`
(Wails detected by `window._wails` or the `eggify_desktop=wails` marker cookie set at bootstrap),
`isDesktop()`, `desktopSelectFolder()`, `desktopOpenExternal()`, `desktopNotify()`. Wails calls go
through `/wails/runtime.js` (`Call.ByName("main.DesktopService.…")`), Tauri calls keep using
`__TAURI_INTERNALS__.invoke`, so the Tauri app keeps working. Wired into the folder picker
(projects page), external links plugin and the n0 OAuth link opener.

**First-run agent setup.** On the first startup (after the admin signs in) the dashboard opens
"Set up your agents" (see CLAUDE.md → *Agent CLI onboarding*): it detects `claude` / `codex`
with the login-shell PATH imported by `shellenv` plus common install locations (a CLI found
only there gets an "add to PATH" hint, since sessions use PATH), and runs the CLIs' own
logins. Claude Code opens the system browser itself; for Codex the dashboard opens the
sign-in URL through `desktopOpenExternal`. The "seen" flag lives in the webview's
localStorage, so the wizard stays reachable from Settings → Agents & logins.

**Window chrome (macOS).** The window uses `MacTitleBarHiddenInset`: no visible title bar,
traffic lights float over the content (Tauri: `titleBarStyle: "Overlay"`).
`plugins/desktop-shell.client.ts` marks `<html>` with `desktop-wails desktop-mac` and eagerly
loads the Wails runtime, which implements drag regions: in `assets/css/desktop-shell.css` the
navbar gets `--wails-draggable: drag` (links, buttons, inputs and the right-hand controls
`no-drag`) and 90px of left padding for the traffic lights; pages without the navbar
(setup, login) get a fixed 28px drag strip at the top. All rules are scoped to
`html.desktop-mac`, so the web layout and the Tauri app are unchanged. Windows/Linux keep the
native frame. The splash page has its own drag strip.

**Separate Go module.** `cmd/eggify-desktop` has its own `go.mod` (`replace
github.com/schlunsen/eggify => ../..`). Importing `internal/...` across the module boundary works
because Go's internal rule is path-based. Reasons: the main `go.mod`/`go.sum` stay free of a
beta dependency tree; `go build ./...`/`go test ./...` keep working on Linux machines without
GTK4/WebKitGTK 6 dev packages (Wails needs cgo + those headers there); the CLI's build matrix is
unchanged. A build tag would have kept compilation out but still put Wails in `go.mod` (tidy
considers all tags). Cost: shared deps can resolve to slightly newer versions in the desktop
build (MVS over Wails' requirements), and CI must test the module separately
(`just desktop-wails-test`).

**One binary is the server and the CLI.** The server re-executes `os.Executable()` for helpers
(`mcp-server` for eggify-tools). `main` hands any args that name a CLI subcommand to
`internal/cmd` (`cmd.HasSubcommand`), so the desktop binary behaves like `eggify` there.
`EGGIFY_PORT` is exported so eggify-tools finds the API.

## What was verified (macOS 27, arm64, unsigned .app, isolated `HOME`)

| Check | Result |
|---|---|
| `.app` builds and launches; splash → dashboard | ✅ |
| In-process server on `http://127.0.0.1:3336` (test port), TLS off, health OK | ✅ |
| Login-shell env imported before config is read | ✅ (`imported N variables`) |
| First-run setup + login from inside the WebKit view (`/api/auth/setup`, `/api/auth/login`) | ✅ 200, `authenticated: true` |
| Session cookie survives an app restart (WKWebView persistent store) | ✅ restart lands authenticated on `/` |
| Agents page renders | ✅ (screenshot) |
| `/agent/ws` connects as the signed-in user | ✅ server log: `WebSocket: User spikeadmin connected`, `pre-authenticated via session` |
| No CSP violations / console errors reported by the diag hook | ✅ none |
| Wails binding call through the bridge (`/wails/runtime.js` → `DesktopService`) | ✅ (`OpenExternal("javascript:…")` correctly refused) |
| `/wails/*` without the bridge cookie, wrong bootstrap token | ✅ 403 (also unit-tested) |
| Hidden title bar, navbar drag region, traffic-light padding | ✅ screenshot + computed styles (`--wails-draggable: drag` on navbar, `no-drag` on buttons); an actual mouse drag was not automated |
| Single instance: second launch exits | ✅ |
| Graceful shutdown on SIGTERM (server `Shutdown`) | ✅ |
| Window state saved/restored | ✅ (restores within ~2px, see gaps) |

Login was driven by `EGGIFY_DESKTOP_DIAG_SCRIPT` calling the same endpoints the setup/login
forms post to (no keystroke automation on the user's desktop); typing in the forms was not
exercised. Not verified (need a human or would touch the user's apps): folder picker dialog,
notifications permission prompt, tray menu clicks, `eggify://` deep links (LaunchServices would
pick whichever Eggify app registered the scheme), positive `OpenExternal`.

## Feature parity vs Tauri

| Feature | Tauri (`src-tauri`) | Wails spike | Notes |
|---|---|---|---|
| Server | sidecar process `eggify-server` on 3334, HTTPS self-signed | **in-process**, HTTP on 127.0.0.1:3335 | one process, no port polling of a child, no orphaned sidecar |
| Login-shell env | ✅ `$SHELL -l -i -c env` | ✅ `internal/shellenv` (fills missing vars, always PATH; 5s timeout) | |
| Splash screen | ✅ animated | ✅ simple, shows startup errors | |
| Title bar overlay | ✅ CSS injected after 1.1s `sleep` | ✅ `MacTitleBarHiddenInset` + scoped CSS from the SPA | no timing hack |
| Folder picker | ✅ | ✅ `DesktopService.SelectFolder` | not clicked in tests |
| External links | ✅ shell plugin | ✅ `DesktopService.OpenExternal` (http/https/mailto only) | |
| Notifications | ✅ plugin | ✅ Wails notifications service (needs bundle id + permission) | the dashboard doesn't call it yet (nor did Tauri's unused `useTauriHooks`) |
| Tray | ✅ Show / Hooks status / Skills / Quit | ✅ Show / Agents / Skills / Remote access / Quit | no hooks status line yet |
| Single instance | ✅ | ✅ `SingleInstanceOptions` | |
| Window state | ✅ plugin | ✅ ~60 lines, `desktop-window-state.json` | |
| Deep links `eggify://` | ✅ plugin (registered, no handler in the app) | ✅ `CFBundleURLTypes` + navigate to an allow-listed SPA path | unverified |
| Close → hide, Dock reopen | default close | ✅ hide on close, reopen from Dock/tray | |
| Hook notifications / hotkeys / invoke_skill commands | Rust commands (`hooks.rs`, `hotkeys.rs`) | ❌ not ported | the composables using them are not imported anywhere today |
| Keychain secrets (`internal/secretstore` auto mode) | ❌ sidecar binary is not the signed app | ✅ once signed: the signed binary *is* the server | needs signing to activate |
| Auto-update | off | ✅ own updater ported from n0 (signature + team verified, atomic swap) | Wails' updater off (bug #6186) |
| Windows / Linux builds | ✅ | ❌ not attempted | see below |
| Bundle size | ~sidecar 80MB + Rust shell | 108MB `.app` (one binary + sherpa dylibs) | |

## Releases: signing, notarization, deploy

Modelled on the n0 desktop release (`desktop-build-darwin`, `desktop-dmg`, `deploy-dmg` in n0's
Justfile). All steps live in `scripts/desktop-release.sh`; the justfile recipes wrap it.

| Recipe | What it does |
|---|---|
| `just desktop-app` | Builds `dist/wails/Eggify.app` (fresh frontend build, `SKIP_FRONTEND=1` to reuse) and signs the dylibs, then the app, with the **Developer ID Application** identity of `APPLE_TEAM_ID`, hardened runtime, secure timestamp and `build/darwin/entitlements.plist`. Verifies the signature, team and runtime flag. Without an identity it fails, unless `ALLOW_ADHOC=1` (loud warning; local use only). |
| `just desktop-dmg` | `desktop-app`, then a DMG with volume "Eggify" (app + `/Applications` link, Finder window styled via osascript with a 60 s timeout, `DMG_STYLE=0` skips), compressed (UDZO), signed, **notarized** (`xcrun notarytool submit --keychain-profile $NOTARY_PROFILE --wait`; a non-`Accepted` status prints the notary log and fails), stapled (5 retries). The ticket is stapled to the `.app` too, then the update ZIP is made with `ditto -c -k --keepParent` → `dist/wails/Eggify-darwin-<arch>.zip`, plus `.sha256` sidecars and `dist/wails/latest.json`. The submission id/status is kept in `dist/wails/notarization.txt`. |
| `just desktop-deploy` | The **pre-publish gate**, exactly n0's plus a bit: `spctl -a -t open --context context:primary-signature` and `stapler validate` on the DMG; unzip the update ZIP and run `codesign --verify --deep --strict`, `spctl --assess --type exec`, `stapler validate`, TeamIdentifier == `APPLE_TEAM_ID`, bundle id == `com.eggify.desktop.wails`, bundle version == `version.go`; the manifest's SHA-256s must match the files. Ad-hoc, unnotarized or foreign-team builds are refused. Then publishes and runs `desktop-verify`. |
| `just desktop-release` | `desktop-dmg` + `desktop-deploy`. |
| `just desktop-verify` | HEAD-checks every published URL (200) and that `latest.json` names this version. |
| `just desktop-rollback <version>` | Re-points `latest.json`, `Eggify.dmg` and the ZIP alias at an earlier release still on the server. |

Environment (none has a default; export them or put them in `~/.config/eggify/release.env`, see
`scripts/release-env.sh`): `APPLE_TEAM_ID` (your team, e.g. `ABCDE12345`), `SIGN_IDENTITY` (default:
first matching Developer ID identity in the keychain), `NOTARY_PROFILE` (or an App Store Connect
key; create a profile with `xcrun notarytool store-credentials <name> --apple-id <id> --team-id
<team>`), `DEPLOY_HOST` (e.g. `user@your-host`), `DEPLOY_DIR` (e.g. `/var/www/example/download`),
`PUBLIC_BASE_URL` (e.g. `https://example.com/download`), `RELEASE_PAGE_URL` (optional). Missing
ones stop the script with a message. No secrets are stored in the repo; notarytool reads the
app-specific password from the keychain profile.

**Endpoints compiled into the app.** The source holds no service addresses. `scripts/build-desktop-wails.sh`
passes them as `-ldflags -X` from `EGGIFY_BUILD_RELAY_ADDR` (`internal/remoteaccess.DefaultRelayAddr`),
`EGGIFY_BUILD_LICENSE_URL` (`internal/license.DefaultBaseURL`), `EGGIFY_BUILD_UPDATE_MANIFEST_URL`
(`main.defaultManifestURL`) and `EGGIFY_BUILD_DOWNLOAD_URL` (`main.defaultDownloadURL`), plus
`APPLE_TEAM_ID` (`main.expectedTeamID`). A build without them has no default relay (the user types one),
no license server (activation says so; `EGGIFY_LICENSE_URL` works), no updater, and an updater that
refuses every update without a team id. `just release-doctor` warns about unset ones.

**Where it is published.** `$PUBLIC_BASE_URL` (e.g. `https://example.com/download/`), i.e. `$DEPLOY_DIR`
on `$DEPLOY_HOST`, served as static files by your web server. Upload
order: versioned binaries first (`Eggify-<v>.dmg`, `Eggify-<v>-darwin-<arch>.zip`, their `.sha256`,
`latest-<v>.json`), each to a `.part` file renamed into place; then the stable aliases
(`Eggify.dmg`, `Eggify-darwin-<arch>.zip`) and **`latest.json` last**, so a client never sees a
manifest pointing at a missing file. Old versioned files stay on the server.
`landing/deploy.sh` rsyncs the landing page with `--delete --exclude '/download/'`, so landing
deploys never touch releases.

```
https://example.com/download/latest.json                    manifest (self-updater, landing page)
https://example.com/download/Eggify.dmg                     newest DMG (landing "Download for macOS")
https://example.com/download/Eggify-2.0.0.dmg(.sha256)      versioned DMG
https://example.com/download/Eggify-2.0.0-darwin-arm64.zip(.sha256)   update ZIP (what the updater installs)
```

`latest.json`:

```json
{
  "version": "2.0.0",
  "published_at": "2026-10-03T12:34:56Z",
  "release_url": "https://example.com/#download",
  "min_os": "13.4",
  "platforms": {
    "darwin-arm64": {
      "url": "https://example.com/download/Eggify-2.0.0-darwin-arm64.zip",
      "sha256": "…", "size": 12345678,
      "dmg_url": "https://example.com/download/Eggify-2.0.0.dmg", "dmg_sha256": "…", "dmg_size": 12345678
    }
  }
}
```

A release is one architecture (the build machine's). Publishing an Intel build later needs
`latest.json` to carry both `platforms` entries (merge before upload; not automated yet).

**Rolling back.** `just desktop-rollback 2.0.0` points `latest.json` and the aliases at 2.0.0 again.
Installed apps never downgrade themselves (the updater refuses a bundle that is not newer than the
running one, which also stops replay of an old signed build); rollback stops new installs and
pending updates of a bad release. To fix users already on it, ship a higher version.

With a Developer ID + hardened runtime the binary meets `internal/secretstore`'s `auto`
conditions, so the MFA/connector keys move to the login keychain.

## Self-update

`cmd/eggify-desktop/updater.go` (+ `updater_darwin.go`), a port of n0's `updater.go` /
`selfupdate.go`. Wails' own updater stays off (wailsapp/wails#6186).

1. **Check** on launch (after 20 s) and every 6 h, or from the tray's **Check for Updates…**
   (shows a dialog when up to date). Fetches `latest.json` (HTTPS, only the hosts of the compiled-in manifest and download URLs) and
   compares versions; `min_os` blocks the offer on older macOS.
2. The dashboard shows **"Update available — Restart to update"** (`DesktopUpdateToast.vue`, mounted in
   `app.vue`, bottom left). It reads `DesktopService.UpdateState` and listens to the `eggify:update`
   Wails event; outside the Wails app (`desktopShell() !== 'wails'`) it renders nothing and never
   polls, so the web dashboard and the Tauri app are unchanged. "Later" hides it for that version.
   When the app cannot replace itself (not in an `.app`, or running from the DMG/App Translocation)
   the button becomes **Download** (opens `Eggify.dmg`); an unwritable folder or any failed check
   shows the error with **Retry** / **Download**.
3. **Install** (`DesktopService.InstallUpdate`, no arguments: the URL comes from the manifest, never
   from the page): download the ZIP into a staging dir next to the running app (same volume) →
   size + **SHA-256** check against the manifest → `ditto -x -k` (Go's zip would break the seal) →
   **verify the new bundle before anything is replaced**: `codesign --verify --deep --strict`,
   TeamIdentifier == the team compiled in with `-ldflags -X main.expectedTeamID` (not settable at run
   time), Identifier == `com.eggify.desktop.wails` (our team signs other apps), `spctl --assess
   --type exec` (Gatekeeper = notarized), `CFBundleShortVersionString` == manifest version and newer
   than the running one → drop quarantine → **atomic swap** with `renamex_np(RENAME_SWAP)` (two
   renames with rollback on filesystems without it) → a detached helper waits for this process to
   exit (single-instance lock, port 3335) and `open`s the app again → the app quits gracefully.
   **App Management** (macOS 13+): only a process signed by the same team may modify a notarized
   bundle, unless the user allowed it in System Settings → Privacy & Security → App Management. So
   every bundle write happens in this process (quarantine is removed with `removexattr`, not
   `/usr/bin/xattr`). If the swap still fails with EPERM, the installed app is unchanged, the state
   carries `error_code: "app_management"`, and the dialog says "macOS blocked the update" with
   **Open App Management settings** (`DesktopService.OpenAppManagementSettings`, which opens only that
   pane), **Try again** and **Download the installer** (`download_url`, the release's DMG). Nothing
   retries by itself. Leftover `.Eggify-update-*` staging dirs are removed before a new attempt.
4. The SHA-256 is served by the same host, so it only proves integrity. Authenticity comes from the
   signature checks: a compromised web server can't get an unsigned, foreign-team, other-app or older
   bundle installed. Redirects off the trusted host are refused.

Testing knobs (never needed in production): `EGGIFY_UPDATE_MANIFEST_URL` (its host becomes trusted;
plain `http` only for loopback), `EGGIFY_UPDATE_FAKE_VERSION=1.0.0` (pretend to be older),
`EGGIFY_DISABLE_UPDATES=1`. Unit tests (`updater_test.go`): manifest parsing, version comparison,
URL policy, SHA-256, signature policy with a fake verifier (other team, n0's bundle id, unsigned,
downgrade), and the full check → download → verify → swap flow against a local server inside a temp
dir, plus the real `RENAME_SWAP`. `updater_appmgmt_test.go`: an EPERM swap gives `app_management`
and leaves the app alone, other errors don't, stale staging is swept, `download_url` follows the
manifest; `updater_quarantine_darwin_test.go` clears real quarantine xattrs.

## Linux and Windows

- **Linux**: Wails v3 targets GTK4 + WebKitGTK 6.0 (GTK3/WebKit2GTK-4.1 is a fallback build
  path). Builds need `libgtk-4-dev libwebkitgtk-6.0-dev` and cgo; runtime packages are newer than
  what Tauri's deb depends on (`libwebkit2gtk-4.1-0`), which drops older LTS distros (Ubuntu 22.04
  has no WebKitGTK 6). The separate module keeps that out of everybody's `go build ./...`.
  Tray uses the StatusNotifier D-Bus protocol (needs an AppIndicator extension on GNOME).
  Loopback HTTP and cookies behave like on macOS (WebKit). Not built in this spike.
- **Windows**: WebView2; the same loopback design should work (WebView2 treats
  `http://127.0.0.1` as a secure context). Needs an MSIX/NSIS packaging step and code signing.
  Not built in this spike.

## Gotchas found

- The server redirects the standard `log` package to its log file during `Setup`, so the desktop
  shell uses its own logger.
- The server writes `.mcp.json` into the working directory on startup; in desktop mode that is
  `/` (Finder) or wherever you launched from, so it is skipped in desktop mode.
- `EGGIFY_DISABLE_TLS=true` (CLI) disables the listener's TLS but leaves `config.TLS.Enabled`
  true, so cookies stay `Secure` over HTTP. Desktop mode sets the config value instead. Worth a
  separate fix for the CLI.
- sherpa-onnx links its dylibs through an absolute rpath into the Go module cache; the `.app`
  must bundle them.
- WebKit does not paint occluded windows, so `screencapture -l` of a background window is blank
  (testing only).
- Saved window size drifts by ~2px per launch (frame vs content rounding); harmless, fix with a
  tolerance.
- The second instance binds its port briefly before Wails' single-instance check exits it.
- Loading the Wails runtime enables its context-menu policy (`--default-contextmenu: auto`):
  the browser context menu only appears on inputs/selected text in production builds.

## Recommendation: **iterate** (then migrate)

No blocker was found. The two big risks from the prior evaluation are resolved by the design:
WebSockets and cookies never go through Wails' asset server, and the CSP needs no desktop
variant. The in-process server is a real simplification (no sidecar lifecycle, no
port-polling of a child, one Go toolchain, no Rust), and it is the only route to keychain-backed
secrets in the desktop app. Wails being beta is contained: the app uses windows, menus, tray,
dialogs, notifications, single instance and the HTTP transport, all stable-looking APIs.

Remaining effort to replace Tauri (estimate):

| Work | Effort |
|---|---|
| ~~Signing + notarization + DMG~~ done for arm64 (`just desktop-release`); x64/universal and CI still open | 0.5–1 day |
| Port hook notifications / tray hooks status / hotkeys (if still wanted) | 1 day |
| Wire notifications into the dashboard (agent done / permission request) | 0.5 day |
| Linux build (GTK4/WebKitGTK 6 CI image, deb/AppImage via `wails3 tool package`) | 2–3 days |
| Windows build (WebView2, NSIS/MSIX, signing) | 2–3 days |
| Manual QA of dialogs/tray/deep links, migration of Tauri users (port 3334 → 3335 means one re-login and fresh localStorage) | 1 day |
| CI job for `cmd/eggify-desktop` (separate module) | 0.5 day |

≈ **1.5–2 weeks** for full parity on all three platforms; **~3 days** for a signed macOS-only
replacement.

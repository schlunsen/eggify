# ADR 014: Switching between your own Eggify machines (relay, native apps)

## Status

Accepted; steps 0-2 shipped in a first form in 2.20.0 (tray menu instead of a Machines menu, no per-machine ⌃⌘1–9 yet)

## Date

2026-10-11

## Context

One person often runs Eggify on more than one Mac (e.g. a desktop and a laptop). Today:

- **iOS** stores several hosts (`HostManager`, a Keychain token per host, `SessionCache` per host)
  but shows one at a time, and switching lives in Settings → Manage Hosts, then rebuilds the UI.
- **The Mac app** only knows its own in-process server. The window loads
  `http://127.0.0.1:3335`; `/wails/*` is gated by a per-launch bootstrap cookie
  (`cmd/eggify-desktop/bridge.go`).
- The building blocks exist: the **Eggify relay** (pinned P-256 key per install, no account,
  `docs/remote-relay.md`), **QR / link pairing** that mints an ordinary user session
  (`created_via=pairing`, revocable under Signed-in devices), and Bearer session tokens accepted
  on REST and on WebSocket upgrades (`sessionTokenCandidates` in `internal/server/auth_handlers.go`).

The owner of both machines should be able to switch between them from the Mac app and the iPhone
app without signing in again.

## Decisions

1. **Switch, don't merge.** One machine is on screen at a time. A combined view (Needs you
   across machines) is a later step.
2. **Relay only.** The other machine must have remote access on in relay mode. No ngrok, no LAN /
   Bonjour, no direct IP for this feature. The relay address carries the key pin, so the
   connection is authenticated end to end without a CA.
3. **Native apps only.** The Mac desktop app and the iOS app. A plain browser tab gets nothing
   (it cannot hold another machine's token safely, and browsers warn at relay addresses).
4. **Pairing is the only way in.** A machine is added by redeeming a pair link
   (`eggify://pair?u=…&c=…&p=…`) from the other machine's Remote access settings. No passwords
   typed for another machine.
5. **On the Mac, a native proxy per machine.** The desktop app runs one loopback listener per
   paired machine on a fixed port. Switching points the window at it (`window.SetURL`):
   - `/wails/*` and `/__desktop/*` stay local (bindings, menu, updater); everything else goes to
     the other machine over the relay, with the key pin checked.
   - The proxy strips `Cookie`, `Set-Cookie` and any incoming `Authorization`, and adds
     `Authorization: Bearer <paired token>`. It rewrites `Host` and `Origin` to the other
     machine's address so its origin check (`isOriginAllowed`) passes; CSP `connect-src 'self'`
     still covers the proxy address.
   - The page is the other machine's own frontend, so UI and API versions always match.
   - localStorage is per origin, so a fixed port per machine keeps each machine's local state
     (tabs, filters, drafts) separate and stable.
6. **Agents on this Mac cannot use the proxy.** It refuses any request without a per-launch gate
   cookie (HttpOnly, SameSite=Strict) that only the app's window receives, like the bridge cookie.
   The paired token lives only in the app process and in `internal/secretstore` (keychain in the
   notarized app), in a file in the agents' Read/Edit deny list. It is never in env, logs or an API.
7. **The other machine stays in charge.** It sees an ordinary signed-in user coming through
   remote access (the API key is refused there anyway); all its own checks apply. Revoking the
   device on that machine ends the switch.

## Setting up a second machine (user steps)

On the **second Mac** ("Studio"), once:

1. Install the Eggify desktop app and activate it with your serial (each Mac uses one seat).
2. Create its account on first run (its own account; it does not need to match the first Mac's).
3. Settings → Remote access → **Eggify relay** → switch it on, and turn on **Start at login**
   so it is reachable after a reboot. It must stay awake (Energy settings) to be reachable.
4. Click **Pair a device**: a QR code and a **Copy link** button (valid 5 minutes).

On the **first Mac**:

5. Copy the link on the second Mac (with Universal Clipboard it is on this Mac's clipboard too),
   then the menu bar icon → **Machines** → **Add a Mac from Clipboard…** → **Add Mac**. Its name
   comes from the other Mac's host name.
6. Switch with ⌥⌘← / ⌥⌘→ (Go → Previous / Next Machine) or the menu bar icon → Machines. "This Mac" goes back.

On the **iPhone**: scan the second Mac's QR code with the Camera, or, once the first Mac has added
it (steps 5-6), add it from the iPhone without a QR code:

7. On the iPhone (signed in to the first Mac), tap the Mac's name at the top of the Agents list. Under
   "MacBook knows 1 more Mac: Studio" tap **Add Studio** → **Add Studio** (also in Settings → Manage Hosts).
8. The first Mac asks Studio for a pair code with its own login there, and the iPhone signs in to Studio
   directly with its own login (shown on Studio under Signed-in devices as "<iPhone> (added via MacBook)").
   **Switch to Studio** now, or **Stay here** and switch later from the same menu.

Nothing is shared: the first Mac's login on Studio never leaves the first Mac, and the iPhone's is its own.
Only a signed-in user on the first Mac can ask for a code (never the API key, so not an agent there).

To remove access: on the second Mac, Settings → Account & security → Signed-in devices → sign out
"MacBook (Eggify desktop)"; or on the first Mac, Machines → Remove → Studio (which also signs out
there).

## Plan

### 0. Spike (1-2 days): go / no-go

Hardcode one machine (relay address, pin, token from a pairing) in the desktop app and check:
REST, `/ws`, `/agent/ws`, the terminal WebSocket, Wails bindings on the proxied page, session
media streaming (Range), CSP and the origin check with rewritten `Host`/`Origin`, and whether
`requestIsHTTPS` / the tunnel path guard cause surprises.

### 1. Mac: machines and pairing

- `cmd/eggify-desktop/machines.go`: machine list (id, name, colour, relay address, pin, fixed
  port, last seen), token encrypted via `internal/secretstore`; file added to the deny list in
  `agents/sandbox.go`.
- "Add a Mac…": paste or open the pair link. `deepLinkPath` gets an `eggify://pair` case (today
  every `eggify://` link is navigation). Redeemed natively with a device name like
  "MacBook (Eggify desktop)".
- A machine that refuses the token (401, revoked) → "Studio signed this Mac out. Pair again."
  Signing out on a proxied page forgets that machine here (after a confirm) instead of leaving a
  dead token.

### 2. Mac: switching UI

- Native **Machines** menu and tray section: This Mac, each paired machine with a status dot,
  Add a Mac…. This is the main switch and works whatever version the other frontend is.
- Shortcuts in `catalog.json` (`desktopMenu: true`): ⌥⌘← / ⌥⌘→ previous / next machine.
- Machine chip (name + colour) in the navbar via a binding (`DesktopService.CurrentMachine`),
  and the window title, so you always know where you type.
- Offline machine → a native page with Retry and Back to this Mac.

### 3. iOS: fast switching

- Machine switcher in the Agents header (tap the name), not only in Settings.
- Keep the other host's cache warm so switching shows its list at once.
- Remote access settings mention that the same pair link adds a Mac to another Mac.
- Add the Macs the current Mac knows with one tap (`GET /api/machines/paired`,
  `POST /api/machines/paired/:id/pair-code`, signed-in user only): the current Mac has the other one
  mint a pair link, and the phone redeems it there itself. Shipped.

### 4. Knowing the other machine needs you

- Both apps poll the machines not on screen every 30-60 s for pending approvals / questions.
- Mac: "Studio: 1 needs you" in the menu/tray and a native notification; clicking switches and
  opens the session. iOS: a toast with **Switch**.

### Server additions

- Machine identity in `GET /api/version` (or `/api/instance`): hostname, install id, version,
  for display and to refuse pairing the same machine twice.
- Signed-in devices shows desktop pairings as a Mac, not a phone.

### Tests

- Proxy red-team: no gate cookie → refused; forwarded `Cookie` / API key stripped; wrong pin →
  refused; `/wails`, `/__desktop` never forwarded.
- Machines file encrypted and in the deny list.
- iOS switcher tests next to the `HostManager` ones.

## Consequences

- **Good:** almost no server changes; the other machine's own auth, ownership and CSP rules keep
  working; each machine's UI matches its API; tokens are revocable per device.
- **Costs:** the other machine must run remote access in relay mode (and pay the relay's latency
  even on the same LAN); a browser tab cannot switch; one more loopback listener per machine in
  the desktop app; the proxy must handle long-lived WebSockets and large media streams.
- **Later, not decided here:** LAN / Bonjour, combined views, handing a session to another
  machine, sharing the Claude usage-limit state between machines.

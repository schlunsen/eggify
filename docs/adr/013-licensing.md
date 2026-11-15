# ADR 013: Paid licenses (serial numbers) and a Rust license server

## Status

Accepted (decisions); implementation in progress

## Date

2026-10-11

## Context

Until 2.17.1 every Eggify build was free: anyone who downloaded the desktop app from
the website could use all of it, including the **Eggify relay** (`relay.example.com`, remote access
from the iPhone app; see `docs/remote-relay.md`). The relay only checks that an install owns the
key it presents (so traffic can't be hijacked); it never checks *who* may use it. Every user's
remote traffic would run through our server, at our cost, for free.

At the time the repository (`schlunsen/eggify`) was private. It is now public (fresh history), so
the source holds no service addresses: the relay address, license server, update manifest and
download URL are injected into release builds (`EGGIFY_BUILD_*`, see `docs/desktop-wails.md`), and a
plain source build has none of them. Anyone can still run their own relay (`eggify-relay`).

Eggify is to become a paid product: only paying users (and, for now, the owner) may run it.

## Decisions

1. **The app only works with a license.** Without one, the desktop app opens on an activation
   screen ("Enter your serial number") and every API refuses until it is activated. There is no
   free tier and no trial for now.
2. **Serial numbers, created by hand for now.** The license server has an admin command to create,
   list and revoke serials. A payment provider (Lemon Squeezy, Stripe or Paddle) is added later; its
   webhook will create serials the same way.
3. **3 computers per serial.** A serial can be active on up to 3 installs at once; deactivating
   one (from the app, or by the admin) frees its seat.
4. **30 days offline.** The app holds a signed license that renews in the background. It keeps
   working for 30 days without reaching the license server (travel, outages), then asks to
   reconnect. A revoked or refunded serial therefore stops working within 30 days at most.
5. **The relay requires a license.** The relay admits an install only with a valid signed license
   (feature `relay`). Until licenses exist, an allowlist of install ids on the relay keeps it to the
   owner's own installs.
6. **The iPhone app follows the Mac.** The iOS app has no license of its own: it connects to a
   licensed Mac, and the Mac (and the relay) do the checking.
7. **The license server is a separate service written in Rust**, hosted by us
   at `https://license.example.com` (wire contract: `docs/licensing-protocol.md`). It is the only thing that issues licenses; the desktop app and the relay
   only verify them, offline, with a public key compiled into them.
8. **The free builds were removed.** On 2026-10-11 every file in `example.com/download`
   (2.14.0 – 2.17.1: DMGs, update ZIPs, checksums, `latest.json`) was deleted permanently. The
   download button returns 404 until the first licensed release. No other place publishes builds
   (the old GitHub release and Homebrew repositories no longer exist).

## Design

**Serials.** `EGG-XXXXX-XXXXX-XXXXX-XXXXX`: random, Crockford base32 (no ambiguous letters), with a
check character so typos are caught in the app before any request. The server stores only a hash
of each serial, plus its plan, seat count (3), status (active / revoked) and notes.

**Activation.** The app sends the serial and its **install public key** (a key pair made per install
and kept in its encrypted settings, like the relay identity). The server checks the serial and the
free seats, records the activation, and returns a **license certificate**:

```json
{ "v": 1, "serial_id": "…", "install": "<sha256 of the install public key>",
  "plan": "standard", "features": ["app", "relay"],
  "issued_at": "…", "expires_at": "<issued + 30 days>" }
```

signed with the server's **Ed25519** key. The app verifies the signature with the public key built
into it, so checking needs no network. It renews the certificate in the background (daily while
online); each renewal re-checks that the serial is still active and the seat still held.

**Binding.** A certificate names one install's public key. Copying it to another computer doesn't
work there: that install has a different key, and the app proves possession of its own key when it
renews. The relay asks the install to sign a challenge with the same key it already proves today, so
a certificate presented to the relay must belong to the install presenting it.

**Relay.** The install sends its certificate in the relay handshake. The relay admits it when the
signature is valid, the certificate is not expired, it covers `relay`, and it belongs to the
install's key, **or** when the install id is on the relay's allowlist (owner's installs, staff). It
refuses everything else with a coded error (a "This relay is for Eggify subscribers" permanent one when
there is no usable license; an expired token or ended `relay_until` is not permanent), and ends the
admission when the token (or the allowlist entry) ends, closing the session. One license install may
hold at most three relay addresses at a time (`docs/licensing-protocol.md`, "Relay admission"). The app shows the
error under Settings → Remote access.

**Admin.** A CLI on the license server (`eggify-license serial create --seats 3 --note …`, `list`,
`revoke`, `activations`, `deactivate`) and, later, the payment provider's webhook.

**What the license can't do.** The check runs on the user's computer, so a determined person could
patch it out of a build. Signing, notarization and the hardened runtime make that real work, and
anything we serve (the relay, updates, later cloud features) checks the license on our side, where
it can't be patched away. The license is there to make paying the normal path, not to be
unbreakable.

## Consequences

### Positive
- Only paying users (and the owner) can run Eggify and use the relay.
- Licensing works offline for 30 days and needs no account, only a serial.
- One signed certificate serves the app, the relay and future paid services.
- Adding payments later only adds a webhook that creates serials.

### Negative
- Copies of the free builds (≤ 2.17.1) already installed keep working; they can't be recalled. They
  no longer get updates (`latest.json` is gone), and the relay will refuse them once it requires a
  license.
- A new service to run, back up and keep up (the license server and its database). If it is down,
  activations and renewals wait; licensed apps keep running for up to 30 days.
- No download is available until the first licensed release.

## Open questions

- Payment provider and prices (Lemon Squeezy is the likely choice: it handles VAT as merchant of
  record and can email serials).
- Whether the CLI (`eggify` from source / `go install`) is licensed the same way, or stays a
  developer build.
- Whether to offer a trial later.

# Licensing protocol

The wire contract between the license server (`services/license-server`, Rust), the desktop app
(`internal/license`, Go) and the relay (`internal/relay`). Decisions and reasons:
`docs/adr/013-licensing.md`. All three must agree on everything below; change it here first.

## Keys

- **Server signing key**: Ed25519, PKCS#8 PEM at `/etc/eggify-license/signing.pem` on the server
  (root, 0600). It never leaves the server.
- **Server public key** (built into the app and the relay), raw 32 bytes, standard base64:
  `LbjYx4fbRqbyIzHknzQ09vRBliThR1ZH03+Dp3QgJP4=`
- **Install key**: Ed25519 key pair made by each desktop install on first activation and kept by it
  (encrypted with the connector key, like the remote-access settings). `install_key` on the wire is
  the raw 32-byte public key, base64url without padding. The **install id** is
  `base64url_nopad(sha256(raw public key))`.

## Serial numbers

`EGG-XXXXX-XXXXX-XXXXX-XXXXX`: 20 characters from the Crockford base32 alphabet
`0123456789ABCDEFGHJKMNPQRSTVWXYZ` (value = index): 19 random characters, then 1 check character.

- **Check character** = `ALPHABET[ (Σ_{i=1..19} i · value(c_i)) mod 31 ]` (i is the 1-based position
  in the 19-character body). 31 is prime and every weight is non-zero mod 31, so a single wrong
  character is caught, except one swap: `0` (value 0) and `Z` (value 31) are equal mod 31, so
  typing one for the other goes unnoticed (the server then answers `unknown_serial`). Almost all
  swaps of neighbours are caught too.
- **Normalising** user input (client and server do the same): upper-case; drop spaces, `-` and `_`;
  drop a leading `EGG` unless exactly 20 characters are left (a serial may itself start with `EGG`);
  map `O`→`0`, `I`/`L`→`1`. The result must be exactly 20 alphabet characters
  with a correct check character; anything else is `invalid_serial` (the app says so before sending).
- The server stores only `sha256(normalised 20 characters)` (hex) and the last group for display
  (`serial_hint`, e.g. `EGG-…-V5N0T`).

Test vectors (body → serial):

| 19-character body | serial |
|---|---|
| `0000000000000000000` | `EGG-00000-00000-00000-00000` |
| `ABCDEFGHJKMNPQRSTVW` | `EGG-ABCDE-FGHJK-MNPQR-STVWT` |
| `ZZZZZZZZZZZZZZZZZZZ` | `EGG-ZZZZZ-ZZZZZ-ZZZZZ-ZZZZ0` |
| `7H3K9Q2M4X8R6T1V5N0` | `EGG-7H3K9-Q2M4X-8R6T1-V5N0T` |
| `EGG0000000000000000` | `EGG-EGG00-00000-00000-00001` |

Normalising `egg 7h3k9-q2m4x-8r6t1-v5n0t` gives `7H3K9Q2M4X8R6T1V5N0T` (valid).
`EGG-7H3K9-Q2M4X-8R6T1-V5N0A` is `invalid_serial` (wrong check character).

## License token

`<payload>.<signature>`: `base64url_nopad(payload JSON bytes)` + `.` + `base64url_nopad(Ed25519
signature over exactly those payload bytes)`, signed with the server key.

```json
{
  "v": 1,
  "serial_id": "8f6c…",          // the server's id for the serial (UUID string)
  "install": "<install id>",      // base64url_nopad(sha256(install public key))
  "plan": "standard",
  "features": ["app", "relay"],   // "app" always; "relay" only while relay_until > issued_at
  "relay_until": 1823536000,      // end of the relay subscription (unix seconds), or null
  "machine": "Dev's MacBook Pro",
  "issued_at": 1792000000,        // unix seconds
  "expires_at": 1794592000        // issued_at + 30 days
}
```

A token is **valid** for an install when: the signature verifies with the server public key, `v` is
1, `install` equals that install's id, and `now < expires_at`. Unknown fields are ignored. The app
needs feature `app`; the relay needs feature `relay` **and** `now < relay_until` (a token can
outlive the subscription by up to 30 days, so `relay_until` is what ends relay access on time).

**Pricing model.** The app is a one-time purchase: the license never runs out, the 30-day token
only bounds how long an install works offline (it renews in the background). The relay is a
separate yearly subscription, stored per serial as `relay_until` (null = no relay); renewals pick
up changes to it.

## HTTP API

Base URL `https://license.example.com` (its own Caddy site, `reverse_proxy 127.0.0.1:8471`; the
API is at the root, e.g. `https://license.example.com/v1/activate`). JSON in and out: every POST must send `Content-Type: application/json` (parameters such as `; charset=utf-8` are fine), else 415 `unsupported_media_type` (not an abuse failure). Errors are
`{"error": "<code>", "message": "<plain words for the user>"}` with the status below.

**Signed requests.** `activate`, `renew` and `deactivate` carry `install_key`, `timestamp` (unix
seconds) and `signature` = `base64url_nopad(Ed25519(install private key, message))` where

```
message = "eggify-license-v1\n" + action + "\n" + install_key + "\n" + timestamp + "\n" + serial
```

`action` is `activate`, `renew` or `deactivate`; `serial` is the normalised 20 characters for
`activate` and the empty string otherwise. The server refuses a timestamp more than 300 s from its
clock (`stale_timestamp`, 401) and a bad signature (`bad_signature`, 401). The signature is checked first: `stale_timestamp` is only answered to a validly signed request, and a request with a bad signature is always `bad_signature`.

| Request | Success | Errors |
|---|---|---|
| `POST /v1/activate {serial, install_key, timestamp, signature, machine?, app_version?}` | 200 `{token, expires_at, serial_hint, seats, seats_used}` | 400 `invalid_serial` / `bad_request`, 401, 404 `unknown_serial`, 403 `serial_revoked`, 409 `seats_full` (+ `activations: [{machine, activated_at, last_seen_at}]`), 429 `rate_limited` / `temporarily_blocked`, 503 `busy` |
| `POST /v1/renew {install_key, timestamp, signature, app_version?}` | 200 `{token, expires_at, serial_hint}` | 401, 404 `not_activated`, 403 `serial_revoked`, 429 `rate_limited` / `temporarily_blocked`, 503 `busy` |
| `POST /v1/deactivate {install_key, timestamp, signature}` | 200 `{}` (also when not activated) | 401, 429 `rate_limited` / `temporarily_blocked`, 503 `busy` |
| `GET /v1/health` | 200 `{ok: true, version}` | 503 `busy` (in-flight cap only) |

Any route can also answer 413 `payload_too_large` (body over 16 KB) and 503 `timeout` (request
not finished within 10 s). 429 and 503 answers carry `Retry-After` (seconds); clients wait at least
that long before retrying.

- Activating an install that is already active on the same serial returns a fresh token (no new
  seat). Activating it on another serial moves it (the old activation ends).
- `renew` updates the activation's `last_seen_at`, `app_version` and returns a new 30-day token.
- Rate limits per client IP: `activate` 10/min, `interest` 5/min, the rest 60/min.

## Abuse protection

A **client** is an IPv4 address or an IPv6 /64 (behind Caddy: the `X-Forwarded-For` address Caddy
adds; the header is trusted only from a loopback peer).

- **Failures**: `invalid_serial`, `unknown_serial`, `bad_signature`,
  `serial_revoked`, `bad_request`. `stale_timestamp` (a wrong clock) and 415 do not count. 5 failures within 15 min ban the client for 1 h; each further
  ban doubles (2 h, 4 h, … at most 7 days). The ban count is remembered for 30 days after the last
  ban. A successful `activate` or `renew` clears the 15-minute failure window (other successes, such as
  `deactivate` or `interest`, do not); nothing but time or an admin lifts a ban. When a ban
  escalates, the stored ban row is re-read, so the count is never taken from a stale cache; a ban
  that cannot be written to the database stays in effect in memory until it can.
- **While banned**, every route except `/v1/health` answers 429 `temporarily_blocked` with
  `Retry-After` = seconds until the ban ends; nothing else is looked at. Bans are stored, so a
  restart doesn't lift them. Admins can also ban a client or a CIDR block by hand.
- **Global brake**: more than 100 `unknown_serial` answers (the brute-force signal; other failures only feed the per-client ban) across all clients within the last hour →
  `activate` answers 503 `busy` ("Too many failed activations right now; try again later",
  `Retry-After` = until the hour's count is back to 100). `renew` and `deactivate` are not
  affected (they need a valid install signature).
- **Limits**: body ≤ 16 KB (413 `payload_too_large`), 10 s per request (503 `timeout`), at most
  256 requests in flight (503 `busy`, `Retry-After: 5`), at most 8 per client, and the body must
  arrive within 3 s (503 `timeout`). Banned, busy and rate-limited requests are logged once per
  ban or minute, not per request.

Apps should show `message` for `temporarily_blocked` and `busy`, and not retry automatically
before `Retry-After`.

## Interest form

The website's "I'm interested" form posts here from the browser. It is the only route with CORS.

`POST /v1/interest {email, name?, note?, website?}`

| Response | Meaning |
|---|---|
| 200 `{ok: true, message: "Thanks, we will be in touch."}` | Stored, or a filled honeypot (see below). The same answer either way. |
| 400 `invalid_email` | The address does not look like one ("That email address doesn't look right."). Not a failure for abuse purposes, so it never bans. |
| 400 `bad_request` | Body is not JSON. Counts as a failure, like the other routes. |
| 415 `unsupported_media_type` | `Content-Type` is not `application/json` (so other websites can't post a form-style request without a preflight). Not a failure. |
| 413 `payload_too_large` | Body over 16 KB. |
| 429 `rate_limited` | More than 5 submissions a minute from one client (`Retry-After: 60`). Never braked globally, since people mistype. |
| 429 `temporarily_blocked` | The client is banned (see Abuse protection). |

- **Address**: trimmed and lower-cased, then checked for shape only (one `@`, a plain local part,
  a domain of at least two labels of letters, digits and hyphens). No deliverability check.
- **One row per address.** A repeat submission updates the row: `submissions` goes up, `updated_at`
  moves on, and the name and note are kept unless the repeat gives new ones. The first submission's
  IP hash and user agent are kept.
- **Honeypot**: `website` is a field people never see. When it has any text, the server answers
  200 as above and stores nothing.
- **Stored**: email, name, note, first and last submission times, the submission count, an
  HMAC-SHA256 of the client block (IPv4 address or IPv6 /64) keyed by a secret derived from the
  server's signing key, so the address is not stored and the hash can't be reversed without the
  key, and the user agent (≤ 200 characters). The table is capped: at most 500 new addresses per
  hour across all clients and 100,000 rows; over a cap the answer is the usual 200 and nothing is
  stored (repeats of a stored address still merge). The CSV export prefixes fields that start with
  `=`, `+`, `-`, `@`, tab or CR with a single quote.
- **CORS**: only the origin in `EGGIFY_LICENSE_INTEREST_ORIGIN` gets `Access-Control-Allow-Origin`, on `/v1/interest` only.
  Preflights are answered with 204 before routing and the rate limit. Responses to that origin also
  expose `Retry-After`, so the page can show the wait.

## Admin CLI (on the server)

```
eggify-license serve      --db /var/lib/eggify-license/license.db --key /etc/eggify-license/signing.pem --listen 127.0.0.1:8471
eggify-license serial create --seats 3 --plan standard --note "who/why" [--relay-months 12]   # prints the serial once
eggify-license serial list                                       # incl. relay: "-", "until 2027-10-11", "ended …"
eggify-license serial relay <serial | id> --extend-months 12     # from the current end, or from now if ended/none
eggify-license serial relay <serial | id> --until 2027-10-11     # relay works through that date (UTC)
eggify-license serial relay <serial | id> --off
eggify-license serial revoke <serial | id>
eggify-license activations <serial | id>
eggify-license deactivate <activation id>
eggify-license ban list
eggify-license ban add <ip | cidr> --hours 24 --note "why"      # a plain IPv6 address means its /64
eggify-license ban remove <ip | cidr>
eggify-license interest list [--limit 50]                        # newest sign-ups: email, name, submissions, first/last seen, note
eggify-license interest export > signups.csv                     # every sign-up as CSV (RFC 4180), oldest first
eggify-license stats                                             # activations, renewals, failures, bans (24 h)
eggify-license keygen --out <path>                               # new signing key (PKCS#8 PEM, 0600)
eggify-license pubkey --key <path>                               # its raw public key, standard base64
```

## Relay admission

The relay hello (`internal/relay/protocol.go`) gets two optional fields, `license` (token) and
`license_key` (install_key), and the proof gets two optional signatures with the install private key
over `"eggify-relay-license-v<N>\n" + <relay control name> + "\n" + base64url_nopad(nonce)`:

- `license_sig` (v1): `N = 1`, the message above.
- `license_sig2` (v2, added later): `N = 2` and one more line, `"\n" + base64url_nopad(sha256(spki))`,
  where `spki` is the DER key of the install's relay identity from the hello. It ties the license
  proof to the relay address it is presented for. Apps send **both**; a relay that only knows v1
  ignores `license_sig2`, a current relay verifies `license_sig2` when present and otherwise accepts
  `license_sig` unless started to require the bound proof (`RequireBoundLicenseProof`, for when the
  old apps are gone). The Rust license server never sees this proof.

The relay admits an install when its id is on the allowlist (`-allow <file>`, one relay install id
per line, `#` comments, re-read by content hash on every lookup and on a timer), or when the token
passes the same verification the app uses (signature by one of the accepted server keys, 64-byte
signature, exactly one `.`, `v`, `install` = sha256 of `license_key`, not expired), covers feature
`relay` with `now < relay_until`, and the license proof verifies. Otherwise it refuses.

**Refusals** carry `error` (the text), `permanent` and a `code`, e.g. `{"error":"…","permanent":true,
"code":"subscribers_only"}` (`code` is new; older apps read only the first two):

| code | permanent | when |
|---|---|---|
| `subscribers_only` | yes | no allowlist entry and no usable license: "This relay is for Eggify subscribers. Activate your license, or use ngrok in Settings → Remote access." |
| `license_expired` | no | the token is genuine but past `expires_at` (renewing fixes it) |
| `relay_ended` | no | `relay_until` has passed (a renewed token may extend it) |
| `too_many_installs` | no | the license already holds the maximum relay addresses (3 per license install, `MaxIDsPerLicense`) |
| `bad_version`, `bad_proof` | yes | protocol version / relay identity proof |
| `relay_full` | no | the relay is at its install limit |

The app retries non-permanent refusals with backoff and reconnects at once when its license changes
(renewal, activation). A permanent refusal keeps the tunnel off until the license changes.

**Admission ends.** The relay remembers when an admission ends (`min(expires_at, relay_until)`; never
for allowlisted installs, which are re-checked against the file) and closes the install's session
then, once a minute at most late. The install reconnects with its current token and is admitted or
refused again. The app only hands the relay a token that covers `relay` now, and applies a revocation
or deactivation in memory before writing the license file.

**Clock rollback.** The app keeps a high-water mark (latest of now, token `issued_at`, last renewal
attempt) in its encrypted state and treats a clock more than 10 minutes behind it as expired.

**Key rotation.** Token verification accepts a list of server public keys (the built-in key plus any
added with `license.AddServerPublicKeys`; the relay takes a comma-separated `-license-key`).

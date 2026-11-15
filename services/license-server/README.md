# eggify-license

The Eggify license server: turns serial numbers into signed 30-day licenses for the desktop app
and the relay. One static binary, SQLite storage, an Ed25519 signing key.

The wire contract (serial format, token format, signed requests, API, errors, rate limits) is
[`docs/licensing-protocol.md`](../../docs/licensing-protocol.md); the reasons are in
[`docs/adr/013-licensing.md`](../../docs/adr/013-licensing.md). Change the protocol there first.

## Build

```bash
cd services/license-server
cargo test
# Static Linux binary (needs cargo-zigbuild + the musl target):
cargo zigbuild --release --target x86_64-unknown-linux-musl
# → target/x86_64-unknown-linux-musl/release/eggify-license
```

SQLite is compiled in (`rusqlite` with `bundled`), so the binary needs nothing on the server.

## Run

```bash
eggify-license keygen --out /etc/eggify-license/signing.pem   # once; 0600, never overwrites
eggify-license pubkey --key /etc/eggify-license/signing.pem   # raw public key, standard base64
eggify-license serve --db /var/lib/eggify-license/license.db \
                     --key /etc/eggify-license/signing.pem --listen 127.0.0.1:8471
```

`pubkey` prints the key the app and relay compile in (`LbjYx4fb…` in the spec for production).
A test/staging environment gets its own key from `keygen`; its public key must then be built
into the app/relay used against it.

- Routes: `POST /v1/activate`, `POST /v1/renew`, `POST /v1/deactivate`, `GET /v1/health`.
- The schema is created/migrated on start (`schema_version` table). WAL mode; admin commands can
  run while the server is up.
- Client IP: the TCP peer, or, only when the peer is loopback (Caddy), the last
  `X-Forwarded-For` entry. IPv6 clients count per /64. Rate limits: `activate` 10/min,
  `renew`/`deactivate` 60/min, per client; `/v1/health` is not limited.
- Every 30 s the server writes its request counters (for `stats`), drops forgotten bans and
  reloads the ban list from the database, so `ban add|remove` reach a running server within 30 s.
- Logs go to stdout, one line per request outcome (`activate seats_full ip=… install=AbCdEfGh
  serial=EGG-…-V5N0T`). Serials, tokens, signatures and keys are never logged.
- SIGTERM / Ctrl-C: stops accepting, finishes in-flight requests, writes the counters, exits.

## Abuse protection

Details in the protocol doc ("Abuse protection"). In short:

- 5 failed requests (`invalid_serial`, `unknown_serial`, `bad_signature`,
  `serial_revoked`, `bad_request`) from one client within 15 min → banned 1 h, then 2 h, 4 h, …
  up to 7 days; the count is remembered 30 days after the last ban. Banned clients get 429
  `temporarily_blocked` + `Retry-After` on every route but `/v1/health`, from memory only. Bans
  live in the `bans` table and survive restarts. Log line: `ban client=203.0.113.9/32 count=2
  duration=7200s last_failure=unknown_serial`.
- More than 100 unknown serials in an hour across all clients → `activate` answers 503 `busy`
  until the count drops (logged once as `brake on` / `brake off`); renew/deactivate unaffected.
- Body ≤ 16 KB (413), 10 s per request (503 `timeout`), ≤ 256 requests in flight (503 `busy`).

To unblock someone (e.g. a customer behind a shared NAT): `eggify-license ban list`, then
`eggify-license ban remove <client>`.

## Admin

All take `--db` (default `/var/lib/eggify-license/license.db`).

```bash
eggify-license serial create --seats 3 --plan standard --note "who/why" [--relay-months 12]   # prints the serial ONCE
eggify-license serial list
eggify-license serial relay <serial | id> --extend-months 12 | --until 2027-10-11 | --off
eggify-license serial revoke <serial | id>
eggify-license activations <serial | id>
eggify-license deactivate <activation id>
eggify-license ban list
eggify-license ban add <ip | cidr> --hours 24 --note "why"   # plain IPv6 = its /64
eggify-license ban remove <ip | cidr>
eggify-license stats                                          # last 24 h
```

**Pricing.** The app is a one-time purchase (the license never runs out; tokens are renewed every
30 days). The relay is a yearly subscription per serial (`relay_until`): tokens carry feature
`relay` and `relay_until` only while it runs. `--extend-months` adds to the current end (or starts
now if it has ended); `--until` makes the relay work through that UTC date. Installs see the change
at their next renewal (daily while online); the relay itself stops at `relay_until`.

Only `sha256(normalised serial)` and the last group (`EGG-…-V5N0T`) are stored, so a lost serial
cannot be shown again: revoke it and create a new one. Revoking or deactivating doesn't reach an
install immediately; its license runs out within 30 days (renewals are refused at once).

Back up `/var/lib/eggify-license/license.db` (e.g. `sqlite3 license.db ".backup /backup/license.db"`)
and the signing key (losing it means shipping a new public key in the app and relay).

## Deploy

systemd: [`deploy/eggify-license.service`](deploy/eggify-license.service) (dedicated
`eggify-license` user, the key stays root 0600 and reaches the service via `LoadCredential`,
`ReadWritePaths=/var/lib/eggify-license`, hardening; setup steps in its header).

Caddy: the API has its own site; routes are at the root (`https://license.example.com/v1/activate`):

```caddy
license.example.com {
	encode zstd gzip
	reverse_proxy 127.0.0.1:8471
}
```

Caddy sets `X-Forwarded-For` to the visitor's address; the server trusts it only from loopback.

Set `EGGIFY_LICENSE_INTEREST_ORIGIN` (for example `https://example.com`) to the website that
posts the "I'm interested" form from a browser; only that origin gets CORS headers on
`/v1/interest`. Unset, no origin does.

## Test vectors for the Go side

[`testdata/vectors.json`](testdata/vectors.json): a fixed test server key (seed, public key, PEM),
a fixed install key (seed, `install_key`, install id), the serial vectors, signed `activate` /
`renew` / `deactivate` requests with their exact messages, and a token with its payload, all at
fixed timestamps. `internal/license` and `internal/relay` test their verifier and signer against it.
`cargo test` checks the file still matches; regenerate after an intended change with

```bash
cargo test --test fixture -- --ignored write_fixture
```

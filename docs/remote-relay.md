# Eggify relay

Remote access without ngrok and without anyone in the middle reading the traffic.

```
iPhone ──TLS (Eggify's own key, pinned)──▶ relay ──same bytes, yamux stream──▶ Eggify on the Mac
                                           routes by SNI only        (connected out to the relay)
```

## Why

ngrok terminates HTTPS at its edge, so ngrok can read every request, including passwords and
MFA codes. A VPN (Tailscale, WireGuard) avoids that but needs an app and setup on every phone.
Because Eggify ships both ends (the Mac app and the iOS app), the pairing QR code can carry
the trust instead: the phone pins the Mac's own key, and the relay only forwards bytes.

## Pieces

| Piece | Where |
|---|---|
| Install identity (P-256 key, address, pin) | `internal/relay/identity.go`, stored in `remote_access.json` |
| Control handshake (prove the key, then yamux) | `internal/relay/protocol.go`, `client.go`, `server.go` |
| SNI routing without decrypting | `internal/relay/sni.go` |
| Relay binary | `cmd/eggify-relay` |
| Remote-access mode | `internal/remoteaccess/relay_provider.go`, manager `Mode` |
| Pin in the pairing link | `internal/server/pairing.go` (`&p=`) |
| Pin checks on the phone | `ios/.../Services/CertificatePins.swift`, `TLSDelegate`, `PinnedKeySecurity` |

## Security properties

- **The relay can't read traffic.** It sees the ClientHello's server name, the visitor's IP
  and timing, and passes the rest on. TLS ends in Eggify.
- **Names are self-certifying.** `<id>` is derived from the install's public key and the
  install proves it holds the key on every connect, so the relay keeps no registry and nobody
  can take over another install's name.
- **The phone trusts one key per address.** The pin arrives in the pairing QR code (shown only
  to a signed-in user) and is stored only for the address derived from it. A CA-issued
  certificate for the same name is refused too.
- **Everything else is unchanged.** Relayed requests are tunnel requests: local-only paths are
  404, the API key is refused, only signed-in sessions work.

What the relay operator can still do: refuse service, see who connects when, and
(theoretically) send a visitor to a different install, which the pin check stops.

## Deploying a relay

1. DNS: `relay.example.com` A record and `*.r.example.com` A record → the relay server
   (DNS only, no proxying).
2. A certificate for `relay.example.com` (Let's Encrypt). Visitor certificates are the
   installs' own; the relay needs none for `*.r.example.com`.
3. Run `eggify-relay -listen :8443 -control relay.example.com -suffix r.example.com -cert … -key …`
   (a build's default relay is injected with `-ldflags -X`, `EGGIFY_BUILD_RELAY_ADDR`; installs can also use
   `EGGIFY_RELAY_ADDR` or the relay address setting; with none, relay mode asks for one. 443 may
   belong to your web server, hence 8443). `-control` and `-suffix` have no defaults.
   Build it with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/eggify-relay`.
4. Open the port in the host firewall.

## Not yet

- Browser access at relay addresses (needs a publicly trusted certificate per install, e.g.
  Let's Encrypt via DNS-01 on an Eggify-run zone, with the key staying on the Mac).
- Per-IP limits on the relay beyond the global caps.

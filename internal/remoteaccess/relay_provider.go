package remoteaccess

import (
	"context"
	"crypto/x509"
	"errors"

	"github.com/schlunsen/eggify/internal/relay"
)

// DefaultRelayAddr is the relay a release build connects to by default. Empty
// in the source; release builds inject it with
// -ldflags "-X github.com/schlunsen/eggify/internal/remoteaccess.DefaultRelayAddr=host:port"
// (scripts/build-desktop-wails.sh, EGGIFY_BUILD_RELAY_ADDR). EGGIFY_RELAY_ADDR and
// the user's relay_addr setting override it; with none of them relay mode
// cannot start (ErrNoRelayAddr).
var DefaultRelayAddr = ""

// RelayProvider opens tunnels through an Eggify relay (internal/relay).
// Visitors' TLS ends here, with the install's own key; the relay only
// forwards bytes it can't read.
type RelayProvider struct {
	// RootCAs validates the relay (nil: system roots); ServerName overrides
	// the relay's name (tests).
	RootCAs    *x509.CertPool
	ServerName string
	// License returns this install's license token and install key, which a
	// subscribers-only relay asks for (nil: none presented).
	License func() *relay.LicenseCredentials
}

// Name implements Provider.
func (p *RelayProvider) Name() string { return ModeRelay }

// Open implements Provider.
func (p *RelayProvider) Open(ctx context.Context, cfg OpenConfig) (Listener, error) {
	if cfg.RelayIdentity == nil {
		return nil, &PermanentError{Err: errors.New("no relay identity")}
	}
	l, err := relay.Dial(ctx, relay.ClientConfig{
		Addr: cfg.RelayAddr, ServerName: p.ServerName, Identity: cfg.RelayIdentity, RootCAs: p.RootCAs,
		License: p.License,
	})
	if err != nil {
		var refused *relay.RefusedError
		if errors.As(err, &refused) && refused.Permanent {
			return nil, &PermanentError{Err: err, Code: "relay_refused"}
		}
		return nil, err
	}
	return &relayListener{l}, nil
}

type relayListener struct{ *relay.Listener }

func (l *relayListener) Region() string { return l.Relay() }

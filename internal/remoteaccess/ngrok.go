package remoteaccess

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.ngrok.com/ngrok"
	"golang.ngrok.com/ngrok/config"
)

// ngrokProvider opens tunnels with the embedded ngrok-go library (no ngrok
// binary). ngrok-go reconnects a dropped session by itself (0.5s..30s
// backoff) and re-binds the tunnel; the Manager re-opens the whole tunnel if
// it ends anyway.
type ngrokProvider struct {
	clientVersion string
}

// NewNgrokProvider returns the ngrok provider.
func NewNgrokProvider(clientVersion string) Provider {
	return &ngrokProvider{clientVersion: clientVersion}
}

func (p *ngrokProvider) Name() string { return "ngrok" }

// regioner is implemented by ngrok's session (not part of its interface).
type regioner interface{ Region() string }

func (p *ngrokProvider) Open(ctx context.Context, oc OpenConfig) (Listener, error) {
	// ngrok-go ties the session's whole lifetime (including its own
	// reconnects) to the context passed to Connect. It must outlive Open:
	// cancelling it on return killed every session right after it connected
	// ("session dropped: context canceled"), and the reconnect loop then
	// opened a new session per second until the account's agent-session limit
	// (ERR_NGROK_108) was hit. Cancel only on failure or when the listener is
	// closed.
	connCtx, cancelConn := context.WithCancel(ctx)
	ok := false
	defer func() {
		if !ok {
			cancelConn()
		}
	}()

	var (
		mu        sync.Mutex
		connected bool
		permErr   error
	)

	opts := []ngrok.ConnectOption{
		ngrok.WithAuthtoken(oc.AuthToken),
		ngrok.WithConnectHandler(func(_ context.Context, sess ngrok.Session) {
			mu.Lock()
			wasConnected := connected
			connected = true
			mu.Unlock()
			if wasConnected && oc.OnReconnect != nil {
				region := ""
				if r, ok := sess.(regioner); ok {
					region = r.Region()
				}
				oc.OnReconnect(region)
			}
		}),
		ngrok.WithDisconnectHandler(func(_ context.Context, _ ngrok.Session, err error) {
			if err == nil || errors.Is(err, context.Canceled) {
				return // session stopped for good (Close / cancel)
			}
			mu.Lock()
			isConnected := connected
			mu.Unlock()
			if isConnected {
				if oc.OnDisconnect != nil {
					oc.OnDisconnect(err)
				}
				return
			}
			// Before the first connect ngrok-go retries forever, also on a bad
			// authtoken. Stop on errors the ngrok service reported (they carry
			// an ERR_NGROK_* code or are auth failures); retry network errors.
			if isNgrokRejection(err) {
				mu.Lock()
				if permErr == nil {
					permErr = err
				}
				mu.Unlock()
				cancelConn()
				return
			}
			if oc.OnRetry != nil {
				oc.OnRetry(cleanNgrokError(err))
			}
		}),
		// "Stop agent" from the ngrok dashboard or API: tell the manager,
		// which closes this session and switches remote access off.
		ngrok.WithStopHandler(func(context.Context, ngrok.Session) error {
			if oc.OnRemoteStop != nil {
				go oc.OnRemoteStop()
			}
			return nil
		}),
	}
	if oc.Metadata != "" {
		opts = append(opts, ngrok.WithMetadata(oc.Metadata))
	}
	if p.clientVersion != "" {
		opts = append(opts, ngrok.WithClientInfo("eggify", p.clientVersion))
	}

	sess, err := ngrokConnect(connCtx, opts...)
	if err != nil {
		mu.Lock()
		pe := permErr
		mu.Unlock()
		if pe != nil {
			return nil, newNgrokPermanentError(pe)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, cleanNgrokError(err)
	}

	tun, err := sess.Listen(ctx, tunnelConfig(oc))
	if err != nil {
		_ = sess.Close()
		if isNgrokRejection(err) {
			return nil, newNgrokPermanentError(err)
		}
		return nil, cleanNgrokError(err)
	}
	region := ""
	if r, ok := sess.(regioner); ok {
		region = r.Region()
	}
	ok = true
	return &ngrokListener{Tunnel: tun, sess: sess, region: region, cancel: cancelConn}, nil
}

// newNgrokPermanentError wraps a rejection from the ngrok service, keeping
// its ERR_NGROK_* code for the UI (e.g. ERR_NGROK_108: agent session limit).
func newNgrokPermanentError(err error) *PermanentError {
	pe := &PermanentError{Err: cleanNgrokError(err)}
	var nerr ngrok.Error
	if errors.As(err, &nerr) {
		pe.Code = nerr.ErrorCode()
	}
	return pe
}

// ngrokConnect is ngrok.Connect; a variable so tests can check the session's
// context lifetime without a real ngrok account.
var ngrokConnect = ngrok.Connect

func tunnelConfig(oc OpenConfig) config.Tunnel {
	var opts []config.HTTPEndpointOption
	if oc.Domain != "" {
		opts = append(opts, config.WithDomain(oc.Domain))
	}
	switch oc.Protection.Mode {
	case ProtectionOAuth:
		var oo []config.OAuthOption
		if len(oc.Protection.AllowEmails) > 0 {
			oo = append(oo, config.WithAllowOAuthEmail(oc.Protection.AllowEmails...))
		}
		if len(oc.Protection.AllowDomains) > 0 {
			oo = append(oo, config.WithAllowOAuthDomain(oc.Protection.AllowDomains...))
		}
		opts = append(opts, config.WithOAuth(oc.Protection.OAuthProvider, oo...))
	case ProtectionBasic:
		opts = append(opts, config.WithBasicAuth(oc.Protection.BasicUser, oc.Protection.BasicPassword))
	}
	opts = append(opts, config.WithMetadata("eggify"))
	return config.HTTPEndpoint(opts...)
}

// isNgrokRejection reports errors the ngrok service returned (as opposed to
// network failures): they won't go away by retrying.
func isNgrokRejection(err error) bool {
	var nerr ngrok.Error
	if errors.As(err, &nerr) && nerr.ErrorCode() != "" {
		return true
	}
	return strings.Contains(err.Error(), "authentication failed")
}

// cleanNgrokError turns ngrok's error into a short message, with a hint for
// the common authtoken failures.
func cleanNgrokError(err error) error {
	var nerr ngrok.Error
	if errors.As(err, &nerr) && nerr.ErrorCode() != "" {
		msg := strings.TrimRight(strings.TrimSpace(firstLine(nerr.Msg())), ".")
		switch nerr.ErrorCode() {
		case "ERR_NGROK_105", "ERR_NGROK_106", "ERR_NGROK_107", "ERR_NGROK_4018":
			return fmt.Errorf("ngrok rejected the authtoken (%s): %s. Copy a fresh token from dashboard.ngrok.com → Your Authtoken", nerr.ErrorCode(), msg)
		case ErrCodeAgentLimit:
			return fmt.Errorf("ngrok: %s (%s). Other ngrok agents are using all of your account's sessions: stop one at dashboard.ngrok.com/agents (or from Eggify with an ngrok API key) and try again. Sessions that ended recently can take a minute to expire", msg, nerr.ErrorCode())
		}
		return fmt.Errorf("ngrok: %s (%s)", msg, nerr.ErrorCode())
	}
	return errors.New(firstLine(err.Error()))
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// ngrokListener closes the session along with the tunnel.
type ngrokListener struct {
	ngrok.Tunnel
	sess   ngrok.Session
	region string
	cancel context.CancelFunc // ends the session's context (see Open)
	once   sync.Once
	err    error
}

func (l *ngrokListener) URL() string    { return l.Tunnel.URL() }
func (l *ngrokListener) Region() string { return l.region }

// Close ends the tunnel and the agent session, so ngrok frees the agent
// session slot right away instead of after a heartbeat timeout. Idempotent.
func (l *ngrokListener) Close() error {
	l.once.Do(func() {
		l.err = l.Tunnel.Close()
		_ = l.sess.Close()
		if l.cancel != nil {
			l.cancel()
		}
	})
	return l.err
}

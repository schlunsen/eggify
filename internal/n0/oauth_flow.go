package n0

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/n0/oauth"
)

// CallbackPath is the OAuth redirect path, served both by the loopback
// listener and (for public https callbacks) by the main Eggify server.
const CallbackPath = "/api/connectors/n0/oauth/callback"

// RedirectKeyLoopback identifies the 127.0.0.1 registration of a host.
const RedirectKeyLoopback = "loopback"

const (
	flowTTL        = 15 * time.Minute
	discoveryTTL   = 10 * time.Minute
	stateProvider  = "n0"
	clientName     = "Eggify"
	flowStatusKeep = 30 * time.Minute
)

// StartRequest starts an authorization.
type StartRequest struct {
	Host        string
	RedirectURI string // the exact callback URL for this flow
	RedirectKey string // RedirectKeyLoopback or the public redirect URI
	ReconnectID int64  // re-authorize this connection in place
	ReturnTo    string // where the callback page sends the browser afterwards
}

// StartResult is returned to the UI.
type StartResult struct {
	AuthorizeURL string `json:"authorize_url"`
	State        string `json:"state"`
	RedirectURI  string `json:"redirect_uri"`
	ExpiresIn    int    `json:"expires_in"`
}

// StartError carries an n0 error verbatim plus a hint for the user.
type StartError struct {
	Code    string `json:"error_code"`
	Message string `json:"error"`
	Hint    string `json:"hint,omitempty"`
}

func (e *StartError) Error() string { return e.Message }

// FlowStatus is the state of one authorization, polled by the UI.
type FlowStatus struct {
	Status           string `json:"status"` // pending | connected | error | unknown
	ConnectionID     int64  `json:"connection_id,omitempty"`
	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
	Hint             string `json:"hint,omitempty"`
	ReturnTo         string `json:"-"`
}

type flow struct {
	status  FlowStatus
	created time.Time
}

// pendingAuth is persisted in oauth_state_tokens.nonce (JSON), keyed by state.
type pendingAuth struct {
	Host               string `json:"host"`
	Issuer             string `json:"issuer"`
	Resource           string `json:"resource"`
	ClientID           string `json:"client_id"`
	RedirectURI        string `json:"redirect_uri"`
	RedirectKey        string `json:"redirect_key"`
	TokenEndpoint      string `json:"token_endpoint"`
	RevocationEndpoint string `json:"revocation_endpoint"`
	ReconnectID        int64  `json:"reconnect_id,omitempty"`
	ReturnTo           string `json:"return_to,omitempty"`
}

func (m *Manager) discover(ctx context.Context, host string) (*oauth.Discovery, error) {
	m.mu.Lock()
	c, ok := m.discoveries[host]
	m.mu.Unlock()
	if ok && m.now().Sub(c.at) < discoveryTTL {
		return c.d, nil
	}
	d, err := oauth.Discover(ctx, m.HTTP, MCPURL(host))
	if err != nil {
		return nil, fmt.Errorf("n0 discovery on %s failed: %w", host, err)
	}
	m.mu.Lock()
	m.discoveries[host] = cachedDiscovery{d: d, at: m.now()}
	m.mu.Unlock()
	return d, nil
}

// ensureClient returns the registration for (host, redirect key),
// registering once. n0 rate-limits registration and deletes clients that
// stay unused for a day, so an old registration with no connections is
// replaced.
func (m *Manager) ensureClient(ctx context.Context, host string, d *oauth.Discovery, redirectKey, redirectURI string) (*database.N0OAuthClient, error) {
	existing, err := m.repo.GetN0OAuthClient(host, redirectKey)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Issuer == d.Server.Issuer && existing.TokenEndpoint == d.Server.TokenEndpoint {
		stale := false
		if m.now().Sub(existing.CreatedAt) > m.clientTTL {
			if n, err := m.repo.CountN0ConnectionsForClient(host, existing.ClientID); err == nil && n == 0 {
				stale = true
			}
		}
		if !stale {
			// Endpoints may move; keep the rest of the row current.
			existing.AuthorizationEndpoint = d.Server.AuthorizationEndpoint
			existing.RevocationEndpoint = d.Server.RevocationEndpoint
			existing.Resource = d.Resource.Resource
			return existing, nil
		}
	}
	reg, err := oauth.Register(ctx, m.HTTP, d.Server.RegistrationEndpoint,
		oauth.NewPublicClientRegistration(clientName, redirectURI, ""))
	if err != nil {
		return nil, registrationError(err, host, redirectURI)
	}
	c := &database.N0OAuthClient{
		Host:                  host,
		RedirectKey:           redirectKey,
		RedirectURI:           redirectURI,
		ClientID:              reg.ClientID,
		Issuer:                d.Server.Issuer,
		Resource:              d.Resource.Resource,
		AuthorizationEndpoint: d.Server.AuthorizationEndpoint,
		TokenEndpoint:         d.Server.TokenEndpoint,
		RevocationEndpoint:    d.Server.RevocationEndpoint,
		RegistrationEndpoint:  d.Server.RegistrationEndpoint,
		ScopesSupported:       strings.Join(d.Scopes(), " "),
		CreatedAt:             m.now(),
	}
	if err := m.repo.SaveN0OAuthClient(c); err != nil {
		return nil, err
	}
	return c, nil
}

// CallbackPrefix is the redirect prefix an n0 admin must allowlist for a
// public (https) Eggify callback.
func CallbackPrefix(redirectURI string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	return u.Scheme + "://" + u.Host + CallbackPath
}

func registrationError(err error, host, redirectURI string) error {
	var oe *oauth.Error
	if errors.As(err, &oe) {
		se := &StartError{Code: oe.Code, Message: "n0 rejected Eggify's client registration: " + oe.Error()}
		switch oe.Code {
		case "invalid_redirect_uri":
			se.Hint = fmt.Sprintf("n0 only accepts loopback callbacks and an allowlist of https callbacks. Ask the admin of %s to add %s to OAUTH_DCR_EXTRA_REDIRECT_PREFIXES, or — if your browser runs on the same machine as Eggify — connect using the loopback callback.", host, CallbackPrefix(redirectURI))
		case "rate_limited":
			se.Hint = "n0 limits client registrations per hour. Wait a bit and try again."
		}
		return se
	}
	return &StartError{Code: "registration_failed", Message: err.Error()}
}

// StartAuthorization prepares an authorization request and returns the URL
// the user's browser must open.
func (m *Manager) StartAuthorization(ctx context.Context, req StartRequest) (*StartResult, error) {
	host, err := NormalizeHost(req.Host)
	if err != nil {
		return nil, &StartError{Code: "invalid_host", Message: err.Error()}
	}
	if req.RedirectURI == "" || req.RedirectKey == "" {
		return nil, &StartError{Code: "invalid_redirect_uri", Message: "no OAuth callback available"}
	}
	if req.ReconnectID != 0 {
		if _, err := m.repo.GetN0Connection(req.ReconnectID); err != nil {
			return nil, &StartError{Code: "not_found", Message: "the n0 connection to reconnect no longer exists"}
		}
	}
	d, err := m.discover(ctx, host)
	if err != nil {
		return nil, &StartError{Code: "discovery_failed", Message: err.Error(),
			Hint: "Check the n0 URL. The n0 instance must support MCP authorization (n0 ≥ PR #825)."}
	}
	client, err := m.ensureClient(ctx, host, d, req.RedirectKey, req.RedirectURI)
	if err != nil {
		return nil, err
	}
	pk, err := oauth.NewPKCE()
	if err != nil {
		return nil, err
	}
	state, err := oauth.NewState()
	if err != nil {
		return nil, err
	}
	pending := pendingAuth{
		Host: host, Issuer: client.Issuer, Resource: d.Resource.Resource, ClientID: client.ClientID,
		RedirectURI: req.RedirectURI, RedirectKey: req.RedirectKey,
		TokenEndpoint: client.TokenEndpoint, RevocationEndpoint: d.Server.RevocationEndpoint,
		ReconnectID: req.ReconnectID, ReturnTo: req.ReturnTo,
	}
	blob, _ := json.Marshal(pending)
	// Reuse Eggify's oauth_state_tokens table (state, provider, nonce, code_verifier).
	if _, err := m.sqlDB.Exec(`INSERT INTO oauth_state_tokens (state, provider, nonce, code_verifier, expires_at) VALUES (?, ?, ?, ?, ?)`,
		state, stateProvider, string(blob), pk.Verifier, m.now().Add(flowTTL)); err != nil {
		return nil, fmt.Errorf("storing OAuth state: %w", err)
	}
	authURL, err := oauth.AuthorizeURL(client.AuthorizationEndpoint, oauth.AuthorizeParams{
		ClientID: client.ClientID, RedirectURI: req.RedirectURI, State: state,
		CodeChallenge: pk.Challenge, Resource: d.Resource.Resource, Scopes: d.Scopes(),
	})
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	for s, f := range m.flows {
		if m.now().Sub(f.created) > flowStatusKeep {
			delete(m.flows, s)
		}
	}
	m.flows[state] = &flow{status: FlowStatus{Status: "pending", ReturnTo: req.ReturnTo}, created: m.now()}
	m.mu.Unlock()
	_, _ = m.sqlDB.Exec(`DELETE FROM oauth_state_tokens WHERE provider = ? AND expires_at < ?`, stateProvider, m.now())
	return &StartResult{AuthorizeURL: authURL, State: state, RedirectURI: req.RedirectURI, ExpiresIn: int(flowTTL.Seconds())}, nil
}

// FlowStatus reports the state of an authorization started here.
func (m *Manager) FlowStatus(state string) FlowStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := m.flows[state]; ok {
		return f.status
	}
	return FlowStatus{Status: "unknown"}
}

func (m *Manager) finishFlow(state string, st FlowStatus) FlowStatus {
	m.mu.Lock()
	if f, ok := m.flows[state]; ok {
		if st.ReturnTo == "" {
			st.ReturnTo = f.status.ReturnTo
		}
		f.status = st
	} else {
		m.flows[state] = &flow{status: st, created: m.now()}
	}
	m.mu.Unlock()
	return st
}

func (m *Manager) takePending(state string) (*pendingAuth, string, error) {
	if state == "" {
		return nil, "", errors.New("missing state")
	}
	var nonce, verifier string
	var expires time.Time
	err := m.sqlDB.QueryRow(`SELECT nonce, COALESCE(code_verifier, ''), expires_at FROM oauth_state_tokens WHERE state = ? AND provider = ?`,
		state, stateProvider).Scan(&nonce, &verifier, &expires)
	if err == sql.ErrNoRows {
		return nil, "", errors.New("unknown or already used state")
	}
	if err != nil {
		return nil, "", err
	}
	// Single use.
	_, _ = m.sqlDB.Exec(`DELETE FROM oauth_state_tokens WHERE state = ?`, state)
	if m.now().After(expires) {
		return nil, "", errors.New("the authorization request expired — start again")
	}
	var p pendingAuth
	if err := json.Unmarshal([]byte(nonce), &p); err != nil {
		return nil, "", errors.New("corrupt OAuth state")
	}
	return &p, verifier, nil
}

// CompleteAuthorization handles the redirect back from n0. oauthErr and
// oauthErrDesc are the error/error_description query parameters, if any.
func (m *Manager) CompleteAuthorization(ctx context.Context, state, code, oauthErr, oauthErrDesc string) FlowStatus {
	p, verifier, err := m.takePending(state)
	if err != nil {
		return FlowStatus{Status: "error", Error: "invalid_state", ErrorDescription: err.Error()}
	}
	fail := func(code, desc, hint string) FlowStatus {
		return m.finishFlow(state, FlowStatus{Status: "error", Error: code, ErrorDescription: desc, Hint: hint, ReturnTo: p.ReturnTo})
	}
	if oauthErr != "" {
		if oauthErr == "invalid_client" || oauthErr == "unauthorized_client" {
			_ = m.repo.DeleteN0OAuthClient(p.Host, p.RedirectKey) // re-register next time
			return fail(oauthErr, oauthErrDesc, "n0 no longer knows Eggify's client registration. Click Connect again.")
		}
		if oauthErr == "access_denied" && oauthErrDesc == "" {
			oauthErrDesc = "The authorization was declined in n0."
		}
		return fail(oauthErr, oauthErrDesc, "")
	}
	if code == "" {
		return fail("invalid_request", "n0 did not return an authorization code", "")
	}
	tok, err := oauth.ExchangeCode(ctx, m.HTTP, p.TokenEndpoint, p.ClientID, code, p.RedirectURI, verifier, p.Resource)
	if err != nil {
		if oauth.ErrorCode(err) == "invalid_client" {
			_ = m.repo.DeleteN0OAuthClient(p.Host, p.RedirectKey)
		}
		code := oauth.ErrorCode(err)
		if code == "" {
			code = "token_exchange_failed"
		}
		return fail(code, err.Error(), "")
	}
	encA, err := m.encrypt(tok.AccessToken)
	if err != nil {
		return fail("server_error", "could not encrypt the n0 token", "")
	}
	encR, err := m.encrypt(tok.RefreshToken)
	if err != nil {
		return fail("server_error", "could not encrypt the n0 token", "")
	}
	conn := &database.N0Connection{
		Host: p.Host, Issuer: p.Issuer, Resource: p.Resource, ClientID: p.ClientID, RedirectURI: p.RedirectURI,
		TokenEndpoint: p.TokenEndpoint, RevocationEndpoint: p.RevocationEndpoint,
		AccessToken: encA, RefreshToken: encR, AccessExpiresAt: tok.ExpiresAt(m.now()), Scopes: tok.Scope,
		Status: database.N0StatusActive, LastUsedAt: m.now(),
	}
	created := false
	if p.ReconnectID != 0 {
		conn.ID = p.ReconnectID
		cs := m.state(conn.ID)
		cs.refreshMu.Lock()
		err = m.repo.ReplaceN0ConnectionAuth(conn)
		cs.refreshMu.Unlock()
		if errors.Is(err, database.ErrN0ConnectionNotFound) {
			err = m.repo.CreateN0Connection(conn)
			created = true
		}
	} else {
		err = m.repo.CreateN0Connection(conn)
		created = true
	}
	if err != nil {
		return fail("server_error", "could not save the n0 connection: "+err.Error(), "")
	}
	_ = m.repo.EnsureN0ConnectorMarker()
	_ = m.repo.TouchN0Connection(conn.ID)
	m.forget(conn.ID)
	m.RefreshConnectionInfo(ctx, conn.ID)
	if created {
		m.notifyChange()
	}
	return m.finishFlow(state, FlowStatus{Status: "connected", ConnectionID: conn.ID, ReturnTo: p.ReturnTo})
}

package remoteaccess

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schlunsen/eggify/internal/relay"
)

// Tunnel states.
const (
	StateDisconnected = "disconnected"
	StateConnecting   = "connecting"
	StateConnected    = "connected"
	StateError        = "error"
)

// Listener is an open tunnel: a net.Listener plus its public URL.
type Listener interface {
	net.Listener
	URL() string
	Region() string
}

// OpenConfig is what a Provider needs to open a tunnel.
type OpenConfig struct {
	AuthToken  string
	Domain     string
	Protection Protection
	// Events from the provider's session (may be called from any goroutine).
	// OnRetry: a connection attempt failed and the provider retries by itself.
	// OnDisconnect: an established session dropped; the provider reconnects.
	// OnReconnect: the session is back.
	OnRetry      func(err error)
	OnDisconnect func(err error)
	OnReconnect  func(region string)
	// OnRemoteStop: the session was stopped from outside (ngrok dashboard or
	// API "stop agent"). Called in its own goroutine.
	OnRemoteStop func()
	// Metadata identifies this Eggify's agent session in the ngrok dashboard
	// and API (see AgentMetadata).
	Metadata string
	// Relay mode: the relay's host:port and this install's identity.
	RelayAddr     string
	RelayIdentity *relay.Identity
}

// Provider opens tunnels. The real one is ngrok (NewNgrokProvider); tests use
// a fake. Open blocks until the tunnel is up, ctx is cancelled, or a
// permanent error occurs.
type Provider interface {
	Name() string
	Open(ctx context.Context, cfg OpenConfig) (Listener, error)
}

// PermanentError marks an error retrying won't fix (bad authtoken, domain
// not reserved for the account, plan limits, ...). Code is the provider's
// error code when it has one (e.g. ERR_NGROK_108).
type PermanentError struct {
	Err  error
	Code string
}

// ErrCodeAgentLimit is ngrok's "your account is limited to N simultaneous
// agent sessions" error.
const ErrCodeAgentLimit = "ERR_NGROK_108"

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// IsPermanent reports whether err is a PermanentError.
func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}

// Errors returned by Start.
var (
	ErrNoToken = errors.New("add your ngrok authtoken first")
	// ErrNoRelayAddr: relay mode has no address (none in the build, the
	// environment or the settings).
	ErrNoRelayAddr = errors.New("enter the address of an Eggify relay first (this build has no default)")
)

// GuardError is a refusal from the start guard (e.g. user auth disabled).
type GuardError struct {
	Code string // machine-readable, e.g. "user_auth_disabled"
	Msg  string
}

func (e *GuardError) Error() string { return e.Msg }

// ServeFunc serves HTTP on a tunnel listener until it is closed.
type ServeFunc func(net.Listener) error

// EnvSettings come from config.json / environment (CLI). Stored settings win.
type EnvSettings struct {
	AuthToken string
	Domain    string
}

// Status is a snapshot of the tunnel for the API, tray and logs. It never
// contains the token.
type Status struct {
	Status         string     `json:"status"` // disconnected | connecting | connected | error
	Provider       string     `json:"provider"`
	PublicURL      string     `json:"public_url,omitempty"`
	Domain         string     `json:"domain,omitempty"`
	Region         string     `json:"region,omitempty"`
	ConnectedSince *time.Time `json:"connected_since,omitempty"`
	Error          string     `json:"error,omitempty"`
	// ErrorCode is the provider's code for Error when it has one
	// (ERR_NGROK_108: the account's agent session limit is reached).
	ErrorCode   string     `json:"error_code,omitempty"`
	Message     string     `json:"message,omitempty"`
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`
	Attempt     int        `json:"attempt,omitempty"`
	// Running: remote access is switched on (connected or trying to be).
	Running bool `json:"running"`
}

// Config wires a Manager.
type Config struct {
	Provider Provider
	Serve    ServeFunc
	Store    *Store // nil: only EnvSettings
	Env      EnvSettings
	// Guard is checked before every start (e.g. user auth must be on).
	Guard func() error
	// Logf receives lifecycle messages (never the token).
	Logf func(format string, args ...any)
	// Backoff for re-opening the tunnel; defaults 1s..60s.
	MinBackoff, MaxBackoff time.Duration
	// API is the ngrok REST API client for listing/stopping agent sessions
	// (nil: api.ngrok.com).
	API *NgrokAPI
	// Hostname goes into the agent session metadata (default os.Hostname).
	Hostname string
	// RelayProvider opens relay-mode tunnels (nil: relay mode unavailable).
	RelayProvider Provider
	// DefaultRelayAddr is used when the settings name no relay
	// (default DefaultRelayAddr).
	DefaultRelayAddr string
}

// Manager owns the tunnel lifecycle: Start/Stop at any time, reconnect with
// backoff, status for the API, and change notifications for the desktop tray.
type Manager struct {
	cfg Config

	opMu sync.Mutex // serialises Start/Stop/settings changes

	mu       sync.Mutex
	status   Status
	cancel   context.CancelFunc
	done     chan struct{}
	ln       *tunnelListener
	subs     map[int]func(Status)
	nextSub  int
	settings Settings
	loadErr  error
	// nonce is the metadata nonce of the session the current run opens, so
	// the agent list can mark it as this Eggify's live session.
	nonce string
	// name is the active provider's name; read without m.mu (setStatus
	// callbacks run under it).
	name atomic.Value
	// identity caches the parsed RelayKey (identityKey is its PEM).
	identity    *relay.Identity
	identityKey string
}

// NewManager returns a stopped manager and loads the stored settings.
func NewManager(cfg Config) *Manager {
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = time.Minute
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Hostname == "" {
		cfg.Hostname, _ = os.Hostname()
	}
	if cfg.DefaultRelayAddr == "" {
		cfg.DefaultRelayAddr = DefaultRelayAddr
	}
	m := &Manager{cfg: cfg, subs: map[int]func(Status){}}
	if cfg.Store != nil {
		m.settings, m.loadErr = cfg.Store.Load()
		if m.loadErr != nil {
			cfg.Logf("remote access: %v", m.loadErr)
		}
	}
	if m.settings.Protection.Mode == "" {
		m.settings.Protection.Mode = ProtectionNone
	}
	m.settings.Mode, _ = NormalizeMode(m.settings.Mode)
	m.name.Store(m.nameFor(m.settings.Mode))
	m.status = Status{Status: StateDisconnected, Provider: m.providerName()}
	return m
}

func (m *Manager) providerFor(mode string) Provider {
	if mode == ModeRelay {
		return m.cfg.RelayProvider
	}
	return m.cfg.Provider
}

func (m *Manager) nameFor(mode string) string {
	if p := m.providerFor(mode); p != nil {
		return p.Name()
	}
	if mode == ModeRelay {
		return ModeRelay
	}
	return "ngrok"
}

func (m *Manager) providerName() string {
	if n, ok := m.name.Load().(string); ok {
		return n
	}
	return "ngrok"
}

// Mode is the selected tunnel mode (ModeNgrok or ModeRelay).
func (m *Manager) Mode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings.Mode
}

func (m *Manager) relayAddrLocked() string {
	if m.settings.RelayAddr != "" {
		return m.settings.RelayAddr
	}
	return m.cfg.DefaultRelayAddr
}

// RelayPin is the pin paired phones check in relay mode: base64url of
// SHA-256 of this install's public key ("" when there is no relay key yet).
func (m *Manager) RelayPin() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, err := m.identityLocked()
	if err != nil || id == nil {
		return ""
	}
	return id.Pin()
}

// identityLocked parses (and caches) the stored relay key; nil without one.
func (m *Manager) identityLocked() (*relay.Identity, error) {
	key := m.settings.RelayKey
	if key == "" {
		return nil, nil
	}
	if m.identity != nil && m.identityKey == key {
		return m.identity, nil
	}
	id, err := relay.ParseIdentity(key)
	if err != nil {
		return nil, err
	}
	m.identity, m.identityKey = id, key
	return id, nil
}

// ensureRelayIdentity returns the relay identity, creating and saving it on
// first use. It must be saved: the key is the install's relay address and
// the pin paired phones hold.
func (m *Manager) ensureRelayIdentity() (*relay.Identity, error) {
	m.mu.Lock()
	id, err := m.identityLocked()
	next := m.settings
	canSave := m.cfg.Store != nil && m.loadErr == nil
	m.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("relay identity: %w", err)
	}
	if id != nil {
		return id, nil
	}
	if !canSave {
		return nil, errors.New("remote access settings can't be saved, so no relay identity can be created")
	}
	key, err := relay.GenerateIdentityPEM()
	if err != nil {
		return nil, err
	}
	next.RelayKey = key
	if err := m.save(next); err != nil {
		return nil, err
	}
	m.cfg.Logf("remote access: created this install's relay identity")
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.identityLocked()
}

// Settings returns the stored settings (contains the token: never send it to
// a client; use View).
func (m *Manager) Settings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

// effective returns the token/domain to use and where the token comes from.
func (m *Manager) effective() (token, domain, source string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.effectiveLocked()
}

// SettingsView is the client-safe view of the settings.
type SettingsView struct {
	TokenSet    bool           `json:"token_set"`
	TokenHint   string         `json:"token_hint,omitempty"`
	TokenSource string         `json:"token_source,omitempty"` // settings | env
	Domain      string         `json:"domain,omitempty"`
	EnvDomain   string         `json:"env_domain,omitempty"`
	Autostart   bool           `json:"autostart"`
	Protection  ProtectionView `json:"protection"`
	LoadError   string         `json:"load_error,omitempty"`
	// APIKeySet: an ngrok API key is saved (agent sessions can be listed and
	// stopped); APIKeyHint is its last 4 characters. The key itself is never
	// sent.
	APIKeySet  bool   `json:"api_key_set"`
	APIKeyHint string `json:"api_key_hint,omitempty"`
	// Mode is ngrok or relay. RelayAddr is the stored relay ("" = default),
	// DefaultRelayAddr the build's default, RelayAvailable whether this build
	// has relay mode, and RelayPin this install's public pin (not a secret).
	Mode             string `json:"mode"`
	RelayAddr        string `json:"relay_addr,omitempty"`
	DefaultRelayAddr string `json:"default_relay_addr"`
	RelayAvailable   bool   `json:"relay_available"`
	RelayPin         string `json:"relay_pin,omitempty"`
}

// ProtectionView is Protection without the basic auth password.
type ProtectionView struct {
	Mode             string   `json:"mode"`
	OAuthProvider    string   `json:"oauth_provider,omitempty"`
	AllowEmails      []string `json:"allow_emails,omitempty"`
	AllowDomains     []string `json:"allow_domains,omitempty"`
	BasicUser        string   `json:"basic_user,omitempty"`
	BasicPasswordSet bool     `json:"basic_password_set"`
}

// View returns the settings without secrets.
func (m *Manager) View() SettingsView {
	token, _, source := m.effective()
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.settings.Protection
	v := SettingsView{
		TokenSet:    token != "",
		TokenHint:   TokenHint(token),
		TokenSource: source,
		Domain:      m.settings.Domain,
		EnvDomain:   m.cfg.Env.Domain,
		Autostart:   m.settings.Autostart,
		Protection: ProtectionView{
			Mode: p.Mode, OAuthProvider: p.OAuthProvider,
			AllowEmails: p.AllowEmails, AllowDomains: p.AllowDomains,
			BasicUser: p.BasicUser, BasicPasswordSet: p.BasicPassword != "",
		},
		APIKeySet:        m.settings.APIKey != "",
		APIKeyHint:       TokenHint(m.settings.APIKey),
		Mode:             m.settings.Mode,
		RelayAddr:        m.settings.RelayAddr,
		DefaultRelayAddr: m.cfg.DefaultRelayAddr,
		RelayAvailable:   m.cfg.RelayProvider != nil,
	}
	if id, err := m.identityLocked(); err == nil && id != nil {
		v.RelayPin = id.Pin()
	}
	if m.loadErr != nil {
		v.LoadError = m.loadErr.Error()
	}
	return v
}

// Update is a partial settings change; nil fields are left alone.
type Update struct {
	AuthToken  *string     `json:"authtoken,omitempty"`
	Domain     *string     `json:"domain,omitempty"`
	Autostart  *bool       `json:"autostart,omitempty"`
	Protection *Protection `json:"protection,omitempty"`
	Mode       *string     `json:"mode,omitempty"`
	RelayAddr  *string     `json:"relay_addr,omitempty"`
}

// UpdateSettings validates and saves u. If the tunnel is running and the
// change affects it, the tunnel is restarted. Returns whether it restarted.
func (m *Manager) UpdateSettings(u Update) (restarted bool, err error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.Lock()
	next := m.settings
	m.mu.Unlock()
	affectsTunnel := false

	if u.AuthToken != nil {
		tok, err := NormalizeToken(*u.AuthToken)
		if err != nil {
			return false, err
		}
		affectsTunnel = affectsTunnel || tok != next.AuthToken
		next.AuthToken = tok
	}
	if u.Domain != nil {
		d, err := NormalizeDomain(*u.Domain)
		if err != nil {
			return false, err
		}
		affectsTunnel = affectsTunnel || d != next.Domain
		next.Domain = d
	}
	if u.Autostart != nil {
		next.Autostart = *u.Autostart
	}
	if u.Mode != nil {
		mode, err := NormalizeMode(*u.Mode)
		if err != nil {
			return false, err
		}
		if mode == ModeRelay && m.cfg.RelayProvider == nil {
			return false, errors.New("relay mode is not available in this build")
		}
		affectsTunnel = affectsTunnel || mode != next.Mode
		next.Mode = mode
	}
	if u.RelayAddr != nil {
		addr, err := NormalizeRelayAddr(*u.RelayAddr)
		if err != nil {
			return false, err
		}
		affectsTunnel = affectsTunnel || (addr != next.RelayAddr && next.Mode == ModeRelay)
		next.RelayAddr = addr
	}
	if u.Protection != nil {
		in := *u.Protection
		// Keep the stored basic password when the client didn't resend it.
		if in.Mode == ProtectionBasic && in.BasicPassword == "" && next.Protection.Mode == ProtectionBasic {
			in.BasicPassword = next.Protection.BasicPassword
		}
		p, err := in.Normalize()
		if err != nil {
			return false, err
		}
		affectsTunnel = affectsTunnel || !protectionEqual(p, next.Protection)
		next.Protection = p
	}
	if err := m.save(next); err != nil {
		return false, err
	}
	if affectsTunnel && m.isRunning() {
		m.stopLocked()
		if err := m.startLocked(); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// DeleteToken removes the stored token (stopping a tunnel that used it).
func (m *Manager) DeleteToken() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	next := m.settings
	m.mu.Unlock()
	hadToken := next.AuthToken != ""
	next.AuthToken = ""
	if err := m.save(next); err != nil {
		return err
	}
	if hadToken && next.Mode != ModeRelay && m.isRunning() {
		m.stopLocked()
	}
	return nil
}

func (m *Manager) save(next Settings) error {
	if m.cfg.Store == nil {
		return errors.New("remote access settings can't be saved (no settings store)")
	}
	if err := m.cfg.Store.Save(next); err != nil {
		return fmt.Errorf("saving remote access settings: %w", err)
	}
	m.mu.Lock()
	m.settings = next
	m.loadErr = nil
	m.mu.Unlock()
	m.name.Store(m.nameFor(next.Mode))
	return nil
}

func protectionEqual(a, b Protection) bool {
	if a.Mode != b.Mode || a.OAuthProvider != b.OAuthProvider || a.BasicUser != b.BasicUser || a.BasicPassword != b.BasicPassword {
		return false
	}
	return fmt.Sprint(a.AllowEmails) == fmt.Sprint(b.AllowEmails) && fmt.Sprint(a.AllowDomains) == fmt.Sprint(b.AllowDomains)
}

// Status returns the current status.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.status
	if st.Domain == "" && m.settings.Mode != ModeRelay {
		_, st.Domain, _ = m.effectiveLocked()
	}
	return st
}

func (m *Manager) effectiveLocked() (token, domain, source string) {
	token, domain = m.settings.AuthToken, m.settings.Domain
	if token != "" {
		source = "settings"
	} else if m.cfg.Env.AuthToken != "" {
		token, source = m.cfg.Env.AuthToken, "env"
	}
	if domain == "" {
		domain = m.cfg.Env.Domain
	}
	return
}

// PublicURL returns the public URL while connected.
func (m *Manager) PublicURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.Status != StateConnected {
		return ""
	}
	return m.status.PublicURL
}

// CheckGuard runs the start guard (nil when starting is allowed).
func (m *Manager) CheckGuard() error {
	if m.cfg.Guard == nil {
		return nil
	}
	return m.cfg.Guard()
}

// Subscribe registers fn for status changes; the returned func unsubscribes.
// fn is called from the manager's goroutines; it must not block for long.
func (m *Manager) Subscribe(fn func(Status)) func() {
	m.mu.Lock()
	id := m.nextSub
	m.nextSub++
	m.subs[id] = fn
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.subs, id)
		m.mu.Unlock()
	}
}

// Start switches remote access on. It returns once the attempt is underway;
// the connection happens in the background (see Status). Starting a running
// tunnel is a no-op.
func (m *Manager) Start() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	return m.startLocked()
}

func (m *Manager) startLocked() error {
	if err := m.CheckGuard(); err != nil {
		return err
	}
	m.mu.Lock()
	mode := m.settings.Mode
	relayAddr := m.relayAddrLocked()
	m.mu.Unlock()
	if mode == ModeRelay && relayAddr == "" {
		return ErrNoRelayAddr
	}
	provider := m.providerFor(mode)
	if provider == nil || m.cfg.Serve == nil {
		return errors.New("remote access is not available in this build")
	}
	token, domain, _ := m.effective()
	var identity *relay.Identity
	if mode == ModeRelay {
		var err error
		if identity, err = m.ensureRelayIdentity(); err != nil {
			return err
		}
		token, domain = "", ""
	} else if token == "" {
		return ErrNoToken
	}
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.cancel, m.done = cancel, done
	prot := m.settings.Protection
	installID := m.settings.InstallID
	m.nonce = randomID(4)
	nonce := m.nonce
	m.mu.Unlock()
	if installID == "" {
		installID = m.ensureInstallID()
	}

	m.setStatus(func(s *Status) {
		*s = Status{Status: StateConnecting, Provider: m.providerName(), Domain: domain,
			Message: "Connecting to " + m.providerName() + "…", Running: true}
	})
	if mode == ModeRelay {
		m.cfg.Logf("remote access: starting (%s, relay=%s)", m.providerName(), relayAddr)
	} else {
		m.cfg.Logf("remote access: starting (%s, domain=%q)", m.providerName(), domain)
	}
	go m.run(ctx, done, provider, OpenConfig{AuthToken: token, Domain: domain, Protection: prot,
		Metadata: AgentMetadata(m.cfg.Hostname, installID, nonce), RelayAddr: relayAddr, RelayIdentity: identity})
	return nil
}

// Stop switches remote access off and closes the tunnel listener; the
// server's other listeners keep serving.
func (m *Manager) Stop() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.stopLocked()
	return nil
}

func (m *Manager) isRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancel != nil
}

func (m *Manager) stopLocked() {
	m.mu.Lock()
	cancel, done, ln := m.cancel, m.done, m.ln
	m.cancel, m.done, m.ln = nil, nil, nil
	wasRunning := cancel != nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if ln != nil {
		_ = ln.Close()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			m.cfg.Logf("remote access: tunnel goroutine did not stop within 10s")
		}
	}
	if wasRunning {
		m.cfg.Logf("remote access: stopped")
	}
	m.setStatus(func(s *Status) {
		*s = Status{Status: StateDisconnected, Provider: m.providerName()}
	})
}

// setStatus mutates the status and notifies subscribers (outside the lock).
func (m *Manager) setStatus(fn func(*Status)) {
	m.mu.Lock()
	prev := m.status
	fn(&m.status)
	st := m.status
	subs := make([]func(Status), 0, len(m.subs))
	for _, f := range m.subs {
		subs = append(subs, f)
	}
	m.mu.Unlock()
	if statusChanged(prev, st) {
		for _, f := range subs {
			f(st)
		}
	}
}

func statusChanged(a, b Status) bool {
	return a.Status != b.Status || a.PublicURL != b.PublicURL || a.Error != b.Error || a.Message != b.Message
}

// setIfCurrent updates the status only while ctx (this run) is still active,
// so late provider callbacks can't overwrite a newer state.
func (m *Manager) setIfCurrent(ctx context.Context, fn func(*Status)) {
	if ctx.Err() != nil {
		return
	}
	m.setStatus(func(s *Status) {
		if ctx.Err() == nil {
			fn(s)
		}
	})
}

func (m *Manager) backoff(attempt int) time.Duration {
	d := m.cfg.MinBackoff
	for i := 1; i < attempt && d < m.cfg.MaxBackoff; i++ {
		d *= 2
	}
	if d > m.cfg.MaxBackoff {
		d = m.cfg.MaxBackoff
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// run opens the tunnel, serves it, and re-opens it with backoff when it
// fails or drops, until ctx is cancelled or a permanent error occurs.
func (m *Manager) run(ctx context.Context, done chan struct{}, provider Provider, oc OpenConfig) {
	defer close(done)
	remoteStop := make(chan struct{})
	var remoteOnce sync.Once
	oc.OnRemoteStop = func() {
		remoteOnce.Do(func() { close(remoteStop) })
		m.stopFromRemote(done)
	}
	oc.OnRetry = func(err error) {
		m.setIfCurrent(ctx, func(s *Status) {
			s.Status = StateConnecting
			s.Message = "Still connecting: " + err.Error()
		})
	}
	oc.OnDisconnect = func(err error) {
		m.cfg.Logf("remote access: session dropped: %v", err)
		m.setIfCurrent(ctx, func(s *Status) {
			s.Status = StateConnecting
			s.Message = "Connection to " + m.providerName() + " lost, reconnecting…"
		})
	}
	oc.OnReconnect = func(region string) {
		m.setIfCurrent(ctx, func(s *Status) {
			if s.PublicURL == "" {
				return // initial connect; run() sets connected
			}
			s.Status = StateConnected
			s.Message = ""
			if region != "" {
				s.Region = region
			}
		})
	}

	attempt := 0
	for {
		ln, err := provider.Open(ctx, oc)
		if ctx.Err() != nil {
			if ln != nil {
				_ = ln.Close()
			}
			return
		}
		if err != nil {
			if IsPermanent(err) {
				m.fail(ctx, err)
				return
			}
			attempt++
			d := m.backoff(attempt)
			m.cfg.Logf("remote access: connect failed (attempt %d, retry in %s): %v", attempt, d, err)
			retryAt := time.Now().Add(d)
			m.setIfCurrent(ctx, func(s *Status) {
				s.Status = StateConnecting
				s.Error = err.Error()
				s.Message = fmt.Sprintf("Connection failed, retrying in %s", d.Round(time.Second))
				s.Attempt = attempt
				s.NextRetryAt = &retryAt
			})
			if !sleepCtx(ctx, d) {
				return
			}
			continue
		}

		tl := newTunnelListener(ln)
		m.mu.Lock()
		if ctx.Err() != nil {
			m.mu.Unlock()
			_ = tl.Close()
			return
		}
		m.ln = tl
		m.mu.Unlock()
		now := time.Now().UTC()
		m.setIfCurrent(ctx, func(s *Status) {
			s.Status = StateConnected
			s.PublicURL = ln.URL()
			s.Region = ln.Region()
			s.ConnectedSince = &now
			s.Error, s.Message, s.Attempt, s.NextRetryAt = "", "", 0, nil
		})
		m.cfg.Logf("remote access: connected at %s", ln.URL())
		attempt = 0

		serveErr := m.cfg.Serve(tl)
		_ = tl.Close()
		m.mu.Lock()
		if m.ln == tl {
			m.ln = nil
		}
		m.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		select {
		case <-remoteStop: // stopFromRemote switches remote access off
			return
		default:
		}
		attempt++
		d := m.backoff(attempt)
		msg := "tunnel closed"
		if serveErr != nil {
			msg = serveErr.Error()
		}
		m.cfg.Logf("remote access: tunnel lost (%s), reconnecting in %s", msg, d)
		retryAt := time.Now().Add(d)
		m.setIfCurrent(ctx, func(s *Status) {
			s.Status = StateConnecting
			s.PublicURL, s.ConnectedSince = "", nil
			s.Message = fmt.Sprintf("Tunnel lost, reconnecting in %s", d.Round(time.Second))
			s.Attempt = attempt
			s.NextRetryAt = &retryAt
		})
		if !sleepCtx(ctx, d) {
			return
		}
	}
}

// fail ends a run with a permanent error: the tunnel stays off and the error
// is shown until the next Start/Stop.
func (m *Manager) fail(ctx context.Context, err error) {
	m.cfg.Logf("remote access: %v", err)
	m.mu.Lock()
	if ctx.Err() != nil {
		m.mu.Unlock()
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel, m.done, m.ln = nil, nil, nil
	m.mu.Unlock()
	code := ""
	var pe *PermanentError
	if errors.As(err, &pe) {
		code = pe.Code
	}
	m.setStatus(func(s *Status) {
		*s = Status{Status: StateError, Provider: m.providerName(), Error: err.Error(), ErrorCode: code, Running: false}
	})
}

// stopFromRemote handles "stop agent" from the ngrok dashboard or API for
// the run whose done channel is done: remote access is switched off (no
// reconnect), with a message saying why.
func (m *Manager) stopFromRemote(done chan struct{}) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	current := m.done == done && m.cancel != nil
	m.mu.Unlock()
	if !current {
		return
	}
	m.cfg.Logf("remote access: stopped from the %s dashboard/API", m.providerName())
	m.stopLocked()
	m.setStatus(func(s *Status) {
		s.Message = "Stopped from the " + m.providerName() + " dashboard or API"
	})
}

// ensureInstallID creates and saves the install id used in the agent
// session metadata (kept in memory if it can't be saved).
func (m *Manager) ensureInstallID() string {
	m.mu.Lock()
	if id := m.settings.InstallID; id != "" {
		m.mu.Unlock()
		return id
	}
	next := m.settings
	m.mu.Unlock()
	next.InstallID = randomID(8)
	m.mu.Lock()
	canSave := m.cfg.Store != nil && m.loadErr == nil // never overwrite a file we couldn't read
	m.mu.Unlock()
	if !canSave || m.save(next) != nil {
		m.mu.Lock()
		m.settings.InstallID = next.InstallID
		m.mu.Unlock()
	}
	return next.InstallID
}

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

package license

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"
)

// clockSlack is how far behind its high-water mark the clock may be before
// the license counts as expired (clock rollback protection); it covers small
// corrections by time sync.
const clockSlack = 10 * time.Minute

// License states.
const (
	StateUnlicensed = "unlicensed"
	StateActive     = "active"
	StateExpired    = "expired"
	StateInvalid    = "invalid"
)

// ErrLicenseRequired is the refusal while licensing is enforced and this
// install has no valid license.
var ErrLicenseRequired = errors.New("Activate Eggify with your serial number")

// Status is what GET /api/license returns.
type Status struct {
	// Required: this build enforces licensing.
	Required    bool       `json:"required"`
	State       string     `json:"state"`
	SerialHint  string     `json:"serial_hint,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Machine     string     `json:"machine,omitempty"`
	Plan        string     `json:"plan,omitempty"`
	Features    []string   `json:"features,omitempty"`
	Seats       int        `json:"seats,omitempty"`
	SeatsUsed   int        `json:"seats_used,omitempty"`
	LastChecked *time.Time `json:"last_checked,omitempty"`
	// Error explains a state other than active, in plain words.
	Error string `json:"error,omitempty"`
	// RenewError: the license is active but the latest renewal failed
	// (offline); it keeps working until ExpiresAt.
	RenewError string `json:"renew_error,omitempty"`
}

// Config configures a Manager.
type Config struct {
	Store  *Store
	Client *Client
	// Machine names this computer when activating (default: DefaultMachineName).
	Machine func() string
	// Enforced overrides the build's switch (default: Enforced).
	Enforced func() bool
	Now      func() time.Time
	Logf     func(format string, args ...any)

	// Renewal schedule: every RenewEvery + up to RenewJitter; after a failed
	// attempt RetryMin, doubling up to RetryMax. Zero values use 24 h, 2 h,
	// 1 min and 1 h; a negative RenewJitter means none.
	RenewEvery time.Duration
	// FlushEvery is how often a failed state write is retried (default 1 min).
	FlushEvery  time.Duration
	RenewJitter time.Duration
	RetryMin    time.Duration
	RetryMax    time.Duration

	// PublicKeys are the accepted server keys (key rotation). Empty: the
	// built-in key plus AddServerPublicKeys.
	PublicKeys []ed25519.PublicKey

	// publicKey replaces the server key in this package's tests only.
	publicKey ed25519.PublicKey
}

// Manager holds the install's license, verifies it offline and renews it
// in the background. Only the token decides validity: a license works
// offline until its expires_at.
type Manager struct {
	cfg Config

	opMu    sync.Mutex // serialises changes to the state (activate / deactivate / applying a renewal)
	renewMu sync.Mutex // one renewal at a time; opMu is not held during its network call

	persistMu sync.Mutex // orders writes of the state file
	// mark is the clock high-water mark (unix seconds): the latest time this
	// install has seen (now, token issued_at, last attempt). A clock earlier
	// than mark - clockSlack is treated as tampered with.
	mark atomic.Int64
	// healMark is set by renew (holding opMu) and consumed by commit: the
	// server's time, which replaces the mark instead of only raising it.
	healMark time.Time

	mu       sync.RWMutex
	st       State
	loadErr  error
	claims   *Claims // verified claims of st.Token (also when expired)
	tokenErr error   // verification error of st.Token (nil when valid)
	dirty    bool    // in-memory state not yet saved (a save failed)
	onChange []func()

	wake chan struct{}
}

// NewManager loads the stored state. A store that can't be read leaves the
// install unlicensed with the reason in Status().Error.
func NewManager(cfg Config) *Manager {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Enforced == nil {
		cfg.Enforced = Enforced
	}
	if cfg.Machine == nil {
		cfg.Machine = DefaultMachineName
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Client == nil {
		cfg.Client = &Client{BaseURL: BaseURLFromEnv()}
	}
	if cfg.Client.Now == nil {
		cfg.Client.Now = cfg.Now
	}
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = time.Minute
	}
	if cfg.RenewEvery <= 0 {
		cfg.RenewEvery = 24 * time.Hour
	}
	if cfg.RenewJitter < 0 {
		cfg.RenewJitter = 0
	} else if cfg.RenewJitter == 0 {
		cfg.RenewJitter = 2 * time.Hour
	}
	if cfg.RetryMin <= 0 {
		cfg.RetryMin = time.Minute
	}
	if cfg.RetryMax <= 0 {
		cfg.RetryMax = time.Hour
	}
	m := &Manager{cfg: cfg, wake: make(chan struct{}, 1)}
	if cfg.Store != nil {
		st, err := cfg.Store.Load()
		if err != nil {
			cfg.Logf("License: %v", err)
			m.loadErr = err
		} else {
			m.st = st
			m.bump(st.HighWater)
		}
	}
	m.mu.Lock()
	m.refreshLocked()
	m.mu.Unlock()
	return m
}

// installID of the stored key ("" without one).
func (st State) installID() string {
	if k := st.PrivateKey(); k != nil {
		return InstallID(k.Public().(ed25519.PublicKey))
	}
	return ""
}

// refreshLocked re-verifies st.Token. Caller holds m.mu (write).
func (m *Manager) refreshLocked() {
	m.claims, m.tokenErr = nil, nil
	if m.st.Token == "" {
		return
	}
	m.claims, m.tokenErr = verifyToken(m.st.Token, m.st.installID(), m.cfg.Now(), m.keys()...)
	if m.claims != nil {
		// A token "from the future" (the server's clock ahead) must not lock
		// the app out, so only issue times the local clock could have seen.
		if t := time.Unix(m.claims.IssuedAt, 0); !t.After(m.cfg.Now().Add(clockSlack / 2)) {
			m.bump(t)
		}
	}
}

// keys are the server keys a token may be signed with.
func (m *Manager) keys() []ed25519.PublicKey {
	switch {
	case m.cfg.publicKey != nil:
		return []ed25519.PublicKey{m.cfg.publicKey}
	case len(m.cfg.PublicKeys) > 0:
		return m.cfg.PublicKeys
	}
	return ServerPublicKeys()
}

// bump raises the clock high-water mark to t.
func (m *Manager) bump(t time.Time) {
	if t.IsZero() {
		return
	}
	for {
		cur := m.mark.Load()
		if t.Unix() <= cur || m.mark.CompareAndSwap(cur, t.Unix()) {
			return
		}
	}
}

// rolledBack reports whether the clock is earlier than the latest time this
// install has seen, beyond the slack.
func (m *Manager) rolledBack(now time.Time) bool {
	mark := m.mark.Load()
	return mark > 0 && now.Unix() < mark-int64(clockSlack/time.Second)
}

// OnChange registers fn, called (in its own goroutine) whenever the stored
// token changes: activation, renewal, removal. The relay uses it to retry
// after a license change.
func (m *Manager) OnChange(fn func()) {
	m.mu.Lock()
	m.onChange = append(m.onChange, fn)
	m.mu.Unlock()
}

// Enforced reports whether this manager requires a license.
func (m *Manager) Enforced() bool { return m.cfg.Enforced() }

// valid reports whether the token is valid now and covers the app.
func (m *Manager) validLocked() bool {
	if m.claims == nil || m.tokenErr != nil {
		return false
	}
	now := m.cfg.Now()
	if m.rolledBack(now) {
		return false
	}
	m.bump(now)
	return now.Unix() < m.claims.ExpiresAt && m.claims.Has(FeatureApp)
}

// Gate returns nil when the app may be used: licensing is not enforced, or
// the license is valid now. Otherwise ErrLicenseRequired.
func (m *Manager) Gate() error {
	if m == nil || !m.cfg.Enforced() {
		return nil
	}
	m.mu.RLock()
	ok := m.validLocked()
	m.mu.RUnlock()
	if ok {
		return nil
	}
	return ErrLicenseRequired
}

// Status describes the license.
func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := Status{Required: m.cfg.Enforced(), SerialHint: m.st.SerialHint, Seats: m.st.Seats, SeatsUsed: m.st.SeatsUsed}
	if !m.st.LastChecked.IsZero() {
		t := m.st.LastChecked
		s.LastChecked = &t
	}
	if c := m.claims; c != nil {
		exp := c.Expires()
		s.ExpiresAt = &exp
		s.Machine, s.Plan, s.Features = c.Machine, c.Plan, append([]string(nil), c.Features...)
	}
	now := m.cfg.Now()
	switch {
	case m.st.Token == "":
		s.State = StateUnlicensed
		switch {
		case m.st.Notice != "":
			s.Error = m.st.Notice
		case m.loadErr != nil:
			s.Error = "The saved license couldn't be read (" + m.loadErr.Error() + "). Enter your serial number again."
		}
	case m.claims == nil || errors.Is(m.tokenErr, ErrTokenWrongInstall):
		// A token that doesn't verify, or one made for another install.
		s.State = StateInvalid
		s.Error = "The saved license isn't valid on this computer (" + errText(m.tokenErr) + "). Enter your serial number again."
	case m.rolledBack(now):
		s.State = StateExpired
		s.Error = "The computer's clock is earlier than the last time Eggify ran. Set the correct date and time, then choose Check now."
	case now.Unix() >= m.claims.ExpiresAt:
		s.State = StateExpired
		s.Error = fmt.Sprintf("The license expired on %s because Eggify couldn't reach the license server for 30 days. Connect to the internet and choose Check now.",
			m.claims.Expires().Local().Format("2 Jan 2006"))
	case !m.claims.Has(FeatureApp):
		s.State = StateInvalid
		s.Error = "This license doesn't include the Eggify app."
	default:
		s.State = StateActive
		if m.st.LastResult != "" && m.st.LastResult != "ok" {
			s.RenewError = (&APIError{Code: m.st.LastResult}).Error()
		}
	}
	return s
}

func errText(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}

// commit makes st the live state first, then writes it to disk. A failed
// write is returned but does not undo the change (a revoked license must
// stop working even when the disk is full); the write is retried by flush.
// Caller holds opMu, not mu.
func (m *Manager) commit(st State) error {
	if !m.healMark.IsZero() && !m.healMark.After(m.cfg.Now().Add(clockSlack/2)) {
		m.mark.Store(m.healMark.Unix())
	} else {
		m.bump(st.LastChecked)
		m.bump(st.LastAttempt)
		m.bump(m.cfg.Now())
	}
	m.healMark = time.Time{}
	m.mu.Lock()
	old := m.st.Token
	st.HighWater = time.Unix(m.mark.Load(), 0).UTC()
	m.st = st
	m.loadErr = nil
	m.refreshLocked()
	// refreshLocked may raise the mark (token issued_at).
	m.st.HighWater = time.Unix(m.mark.Load(), 0).UTC()
	changed := old != st.Token
	var fns []func()
	if changed {
		fns = append([]func(){}, m.onChange...)
	}
	m.mu.Unlock()
	err := m.persist()
	for _, fn := range fns {
		go fn()
	}
	return err
}

// persist writes the live state; a failure marks it dirty for flush.
func (m *Manager) persist() error {
	if m.cfg.Store == nil {
		return nil
	}
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	st := m.state()
	err := m.cfg.Store.Save(st)
	m.mu.Lock()
	m.dirty = err != nil
	m.mu.Unlock()
	if err != nil {
		m.cfg.Logf("License: couldn't save the license state: %v (will retry)", err)
		return fmt.Errorf("couldn't save the license: %w", err)
	}
	return nil
}

// flush retries a write that failed earlier.
func (m *Manager) flush() {
	m.mu.RLock()
	d := m.dirty
	m.mu.RUnlock()
	if d {
		_ = m.persist()
	}
}

func (m *Manager) state() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.st
}

// Activate activates serial (any user input; normalised here) on this
// install. machine "" uses this computer's name. Errors are *APIError with
// plain-language Error().
func (m *Manager) Activate(ctx context.Context, serialInput, machine string) (Status, error) {
	serial, err := NormalizeSerial(serialInput)
	if err != nil {
		return m.Status(), &APIError{Status: 400, Code: CodeInvalidSerial}
	}
	if machine == "" {
		machine = m.cfg.Machine()
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()

	st := m.state()
	if st.PrivateKey() == nil {
		// The install key is made once and kept, also when this attempt
		// fails, so retries and later renewals use the same identity.
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return m.Status(), err
		}
		st.InstallKey = priv
		if err := m.commit(st); err != nil {
			return m.Status(), err
		}
	}
	res, err := m.cfg.Client.Activate(ctx, st.PrivateKey(), serial, machine)
	if err != nil {
		m.cfg.Logf("License: activation refused: %v", err)
		return m.Status(), err
	}
	if _, verr := verifyToken(res.Token, st.installID(), m.cfg.Now(), m.keys()...); verr != nil {
		m.cfg.Logf("License: activation returned an unusable token: %v", verr)
		return m.Status(), &APIError{Code: CodeServerError, ServerMsg: "The license server sent a license this computer can't use (" + verr.Error() + ")."}
	}
	now := m.cfg.Now().UTC()
	st.Token = res.Token
	st.SerialHint = res.SerialHint
	if st.SerialHint == "" {
		st.SerialHint = SerialHint(serial)
	}
	st.Seats, st.SeatsUsed = res.Seats, res.SeatsUsed
	st.LastChecked, st.LastAttempt, st.LastResult, st.Notice = now, now, "ok", ""
	// The seat is taken on the server: the license is live from here, a
	// failed write is retried in the background.
	if err := m.commit(st); err != nil {
		m.cfg.Logf("License: %v", err)
	}
	m.cfg.Logf("License: activated (%s)", st.SerialHint)
	m.poke()
	return m.Status(), nil
}

// Deactivate frees this install's seat on the server, then forgets the
// license. When the server can't be reached nothing changes (the seat would
// stay taken).
func (m *Manager) Deactivate(ctx context.Context) (Status, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	st := m.state()
	if priv := st.PrivateKey(); priv != nil {
		if err := m.cfg.Client.Deactivate(ctx, priv); err != nil {
			return m.Status(), err
		}
	}
	st.Token, st.SerialHint, st.Seats, st.SeatsUsed = "", "", 0, 0
	st.LastResult, st.Notice = "", ""
	// The server freed the seat, so this install is unlicensed now whether or
	// not the file can be written (retried in the background).
	if err := m.commit(st); err != nil {
		m.cfg.Logf("License: %v", err)
	}
	m.cfg.Logf("License: deactivated")
	m.poke()
	return m.Status(), nil
}

// CheckNow renews the license right away (Settings → Check now). A failed
// check leaves the background schedule (and its backoff) alone.
func (m *Manager) CheckNow(ctx context.Context) (Status, error) {
	err := m.renew(ctx)
	if errors.Is(err, errNothingToRenew) {
		err = nil // not activated: nothing to check, the status says so
	}
	if err == nil {
		m.poke()
	}
	return m.Status(), err
}

var errNothingToRenew = errors.New("this computer isn't activated")

// renew asks the server for a fresh token. serial_revoked / not_activated
// remove the token (the install becomes unlicensed with a notice); other
// failures keep it, so the app works offline until it expires. The network
// call runs without opMu (Activate / Deactivate stay responsive); the result
// is applied only if the license is still the one that was renewed.
func (m *Manager) renew(ctx context.Context) error {
	m.renewMu.Lock()
	defer m.renewMu.Unlock()
	m.flush()
	m.opMu.Lock()
	snap := m.state()
	m.opMu.Unlock()
	priv := snap.PrivateKey()
	if priv == nil || snap.Token == "" {
		return errNothingToRenew
	}
	res, err := m.cfg.Client.Renew(ctx, priv)

	m.opMu.Lock()
	defer m.opMu.Unlock()
	st := m.state()
	if st.Token != snap.Token || !bytes.Equal(st.InstallKey, snap.InstallKey) {
		// Activated, deactivated or replaced while the request was out.
		return errNothingToRenew
	}
	now := m.cfg.Now().UTC()
	st.LastAttempt = now
	if err != nil {
		var ae *APIError
		code := CodeOffline
		if errors.As(err, &ae) {
			code = ae.Code
		}
		st.LastResult = code
		if code == CodeSerialRevoked || code == CodeNotActivated {
			st.Token, st.Seats, st.SeatsUsed = "", 0, 0
			if code == CodeSerialRevoked {
				st.Notice = "This serial number has been revoked, so Eggify was deactivated on this computer. Contact support if you think this is a mistake."
			} else {
				st.Notice = "This computer was deactivated (from another Mac or by support). Enter your serial number to activate it again."
			}
			m.cfg.Logf("License: renewal refused (%s); license removed", code)
		} else {
			m.cfg.Logf("License: renewal failed: %v", err)
		}
		// In memory first: a license the server removed stops working even
		// when the file can't be written.
		if serr := m.commit(st); serr != nil {
			m.cfg.Logf("License: %v", serr)
		}
		return err
	}
	claims, verr := verifyToken(res.Token, st.installID(), m.cfg.Now(), m.keys()...)
	if verr != nil {
		st.LastResult = CodeServerError
		_ = m.commit(st)
		m.cfg.Logf("License: renewal returned an unusable token: %v", verr)
		return &APIError{Code: CodeServerError}
	}
	st.Token = res.Token
	if res.SerialHint != "" {
		st.SerialHint = res.SerialHint
	}
	st.LastChecked, st.LastResult, st.Notice = now, "ok", ""
	// The server just told us the time: a mark that ran ahead of it (the clock
	// was briefly wrong) is set back to it, so correcting the clock and
	// checking again ends the lockout.
	m.healMark = time.Unix(claims.IssuedAt, 0)
	if err := m.commit(st); err != nil {
		m.cfg.Logf("License: %v", err)
	}
	return nil
}

func (m *Manager) poke() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Run renews the license in the background until ctx ends: at start, then
// every RenewEvery (+ jitter); after a failure it retries with backoff. Without
// a license it waits for an activation.
func (m *Manager) Run(ctx context.Context) {
	// next is when the renewal is due. The retry tick must not move it, or a
	// renewal due in more than a flush interval would never run.
	next := time.Now() // renew right away at start
	failures := 0
	flushTick := time.NewTicker(m.cfg.FlushEvery)
	defer flushTick.Stop()
	for {
		var timer <-chan time.Time
		var t *time.Timer
		if m.state().Token != "" {
			t = time.NewTimer(time.Until(next))
			timer = t.C
		}
		woken := false
		select {
		case <-ctx.Done():
		case <-m.wake:
			woken = true
		case <-flushTick.C:
			if t != nil {
				t.Stop()
			}
			m.flush()
			continue
		case <-timer:
		}
		if t != nil {
			t.Stop()
		}
		if ctx.Err() != nil {
			return
		}
		if woken {
			// Activated, deactivated or checked by hand: a fresh schedule.
			failures = 0
			next = time.Now().Add(m.nextRenewal())
			continue
		}
		err := m.renew(ctx)
		switch {
		case err == nil:
			failures = 0
			next = time.Now().Add(m.nextRenewal())
		case errors.Is(err, errNothingToRenew):
			next = time.Now().Add(m.nextRenewal())
		default:
			failures++
			next = time.Now().Add(m.backoff(failures))
		}
	}
}

func (m *Manager) nextRenewal() time.Duration {
	return m.cfg.RenewEvery + randDuration(m.cfg.RenewJitter)
}

func (m *Manager) backoff(failures int) time.Duration {
	d := m.cfg.RetryMin
	for i := 1; i < failures && d < m.cfg.RetryMax; i++ {
		d *= 2
	}
	if d > m.cfg.RetryMax {
		d = m.cfg.RetryMax
	}
	return d + randDuration(d/4)
}

func randDuration(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
}

// RelayCredentials returns the current license token and the install key it
// was issued to, for the Eggify relay's admission check ("", nil when this
// install has no valid license). The relay itself decides whether the token
// covers it.
func (m *Manager) RelayCredentials() (string, ed25519.PrivateKey) {
	if m == nil {
		return "", nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.validLocked() || !m.claims.RelayEntitled(m.cfg.Now()) {
		return "", nil
	}
	return m.st.Token, m.st.PrivateKey()
}

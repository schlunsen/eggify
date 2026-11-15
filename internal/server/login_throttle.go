package server

import (
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

// Password login throttling (in memory), on top of the per-IP limiter in
// routes/auth.go. The per-IP limiter alone let an attacker with many
// addresses guess one account's password without end; a lock keyed by the
// username alone let anyone keep a known username locked out with one wrong
// try per lock window. So:
//
//   - Per (username, client address; IPv6 by /64): every attempt counts;
//     after loginFreeAttempts that pair is locked for 1 min, doubling with
//     each further attempt up to 15 min. A successful login resets it.
//     Failures from other addresses never lock this address out. Unknown
//     usernames are tracked the same way, so a lockout does not reveal which
//     accounts exist.
//   - Per username, a higher cap that only slows: more than loginUserSlowCap
//     failures in loginUserWindow (from any address) put attempts from
//     addresses that never logged in as that user into slow mode (one per
//     loginUserSlowInterval per address and loginUserSlowPerMinute in all).
//     An address that has succeeded for that user before (loginKnownTTL) is
//     never slowed by other addresses' failures.
//   - Globally: more than loginGlobalFailCap failed logins in
//     loginGlobalWindow switches to slow mode, where at most one attempt per
//     loginGlobalSlowInterval is evaluated (others get 429). A real user is
//     slowed down, never locked out for good.
//
// Throttled attempts get the same generic 429 whether or not the user exists.

const (
	loginFreeAttempts       = 5
	loginBaseLockout        = time.Minute
	loginMaxLockout         = 15 * time.Minute
	loginEntryTTL           = time.Hour
	loginMaxTrackedNames    = 10000
	loginGlobalWindow       = 15 * time.Minute
	loginGlobalFailCap      = 100
	loginGlobalSlowInterval = 2 * time.Second

	loginUserWindow        = 15 * time.Minute
	loginUserSlowCap       = 50
	loginUserSlowInterval  = 5 * time.Second
	loginUserSlowPerMinute = 12
	loginKnownTTL          = 30 * 24 * time.Hour
	loginKnownPerUser      = 16
)

// ErrLoginThrottled is returned when a login attempt is refused before the
// password is checked.
var ErrLoginThrottled = errors.New("too many login attempts")

// loginEntry is the record of one (username, address) pair.
type loginEntry struct {
	attempts    int
	lockedUntil time.Time
	lastAttempt time.Time
	lastSlow    time.Time // last attempt let through in the username's slow mode
}

// loginUserState is what is known about one username across addresses.
type loginUserState struct {
	failTimes []time.Time          // failed logins inside loginUserWindow, oldest first
	known     map[string]time.Time // addresses that logged in successfully -> when
	slowStart time.Time            // start of the current slow-mode minute
	slowCount int
}

type loginThrottle struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]*loginEntry // username + "\x00" + address
	users   map[string]*loginUserState

	failTimes   []time.Time // failed logins inside loginGlobalWindow, oldest first
	lastGlobal  time.Time   // last attempt let through in slow mode
	slowEntered bool
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{
		now:     time.Now,
		entries: make(map[string]*loginEntry),
		users:   make(map[string]*loginUserState),
	}
}

// defaultLoginThrottle is shared by the login handlers.
var defaultLoginThrottle = newLoginThrottle()

func loginThrottleKey(username string) string {
	k := strings.ToLower(strings.TrimSpace(username))
	if len(k) > 128 {
		k = k[:128]
	}
	return k
}

// loginAddrKey is the client address a login attempt is counted under: the
// IP, IPv6 by /64 (one host usually owns the whole /64).
func loginAddrKey(addr string) string {
	ip := net.ParseIP(strings.TrimSpace(addr))
	if ip == nil {
		return strings.TrimSpace(addr)
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

func loginEntryKey(user, addr string) string { return user + "\x00" + loginAddrKey(addr) }

func (t *loginThrottle) userStateLocked(user string) *loginUserState {
	u := t.users[user]
	if u == nil {
		u = &loginUserState{known: map[string]time.Time{}}
		t.users[user] = u
	}
	return u
}

// begin reserves an attempt for username from addr, or returns
// ErrLoginThrottled. The attempt counts before the password is checked, so
// parallel guesses cannot all slip in before the first failure is recorded.
func (t *loginThrottle) begin(username, addr string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()

	// Global slow mode.
	t.pruneFailsLocked(now)
	if len(t.failTimes) >= loginGlobalFailCap {
		if now.Sub(t.lastGlobal) < loginGlobalSlowInterval {
			return ErrLoginThrottled
		}
		t.lastGlobal = now
	}

	user := loginThrottleKey(username)
	key := loginEntryKey(user, addr)
	e := t.entries[key]
	if e != nil && now.Sub(e.lastAttempt) > loginEntryTTL && !now.Before(e.lockedUntil) {
		delete(t.entries, key)
		e = nil
	}
	if e == nil {
		if len(t.entries) >= loginMaxTrackedNames {
			t.pruneLocked(now)
		}
		e = &loginEntry{}
		t.entries[key] = e
	}
	// This address's own failures lock this address, known or not.
	if now.Before(e.lockedUntil) {
		return ErrLoginThrottled
	}

	// Other addresses' failures only slow an address that never succeeded.
	u := t.userStateLocked(user)
	if seen, ok := u.known[loginAddrKey(addr)]; !ok || now.Sub(seen) > loginKnownTTL {
		u.failTimes = pruneTimes(u.failTimes, now, loginUserWindow)
		if len(u.failTimes) >= loginUserSlowCap {
			if now.Sub(e.lastSlow) < loginUserSlowInterval {
				return ErrLoginThrottled
			}
			if now.Sub(u.slowStart) >= time.Minute {
				u.slowStart, u.slowCount = now, 0
			}
			if u.slowCount >= loginUserSlowPerMinute {
				return ErrLoginThrottled
			}
			u.slowCount++
			e.lastSlow = now
		}
	}

	e.attempts++
	e.lastAttempt = now
	if e.attempts >= loginFreeAttempts {
		lock := loginBaseLockout << uint(min(e.attempts-loginFreeAttempts, 10))
		if lock > loginMaxLockout {
			lock = loginMaxLockout
		}
		e.lockedUntil = now.Add(lock)
	}
	return nil
}

// succeeded clears the record of username from addr after a correct password
// and remembers the address as one this user logs in from.
func (t *loginThrottle) succeeded(username, addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	user := loginThrottleKey(username)
	delete(t.entries, loginEntryKey(user, addr))
	u := t.userStateLocked(user)
	now := t.now()
	u.known[loginAddrKey(addr)] = now
	for len(u.known) > loginKnownPerUser {
		oldest, oldestAt := "", now.Add(time.Second)
		for a, at := range u.known {
			if at.Before(oldestAt) {
				oldest, oldestAt = a, at
			}
		}
		delete(u.known, oldest)
	}
}

// failed records a wrong password (for the global cap).
func (t *loginThrottle) failed() {
	t.mu.Lock()
	now := t.now()
	t.pruneFailsLocked(now)
	if len(t.failTimes) < loginGlobalFailCap*4 {
		t.failTimes = append(t.failTimes, now)
	}
	t.mu.Unlock()
}

// failedFor records a wrong password for username (for its slow-mode cap).
func (t *loginThrottle) failedFor(username string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	u := t.userStateLocked(loginThrottleKey(username))
	u.failTimes = pruneTimes(u.failTimes, now, loginUserWindow)
	if len(u.failTimes) < loginUserSlowCap*4 {
		u.failTimes = append(u.failTimes, now)
	}
}

func pruneTimes(ts []time.Time, now time.Time, window time.Duration) []time.Time {
	i := 0
	for i < len(ts) && now.Sub(ts[i]) > window {
		i++
	}
	if i > 0 {
		ts = append(ts[:0], ts[i:]...)
	}
	return ts
}

func (t *loginThrottle) pruneFailsLocked(now time.Time) {
	t.failTimes = pruneTimes(t.failTimes, now, loginGlobalWindow)
}

func (t *loginThrottle) pruneLocked(now time.Time) {
	for k, e := range t.entries {
		if !now.Before(e.lockedUntil) && now.Sub(e.lastAttempt) > loginBaseLockout {
			delete(t.entries, k)
		}
	}
	// Still full (a flood of locked pairs): drop unlocked ones first, then any.
	if len(t.entries) >= loginMaxTrackedNames {
		for k := range t.entries {
			delete(t.entries, k)
			if len(t.entries) < loginMaxTrackedNames/2 {
				break
			}
		}
	}
	for name, u := range t.users {
		u.failTimes = pruneTimes(u.failTimes, now, loginUserWindow)
		for a, at := range u.known {
			if now.Sub(at) > loginKnownTTL {
				delete(u.known, a)
			}
		}
		if len(u.failTimes) == 0 && len(u.known) == 0 {
			delete(t.users, name)
		}
	}
	if len(t.users) >= loginMaxTrackedNames {
		for k := range t.users {
			delete(t.users, k)
			if len(t.users) < loginMaxTrackedNames/2 {
				break
			}
		}
	}
}

// verifyLogin is VerifyPassword behind the login throttle. It returns
// ErrLoginThrottled without checking the password when the username (or the
// whole server) is throttled.
func (us *UserStore) verifyLogin(t *loginThrottle, addr, username, password string) (*User, error) {
	if t == nil {
		t = defaultLoginThrottle
	}
	if err := t.begin(username, addr); err != nil {
		return nil, err
	}
	user, err := us.VerifyPassword(username, password)
	if err != nil {
		t.failed()
		t.failedFor(username)
		return nil, err
	}
	t.succeeded(username, addr)
	return user, nil
}

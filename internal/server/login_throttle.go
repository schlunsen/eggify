package server

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// Password login throttling (in memory), on top of the per-IP limiter in
// routes/auth.go. The per-IP limiter alone let an attacker with many
// addresses guess one account's password without end.
//
//   - Per username: every attempt counts; after loginFreeAttempts the name is
//     locked for 1 min, doubling with each further attempt up to 15 min. A
//     successful login resets it. Unknown usernames are tracked the same way,
//     so a lockout does not reveal which accounts exist.
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
)

// ErrLoginThrottled is returned when a login attempt is refused before the
// password is checked.
var ErrLoginThrottled = errors.New("too many login attempts")

type loginEntry struct {
	attempts    int
	lockedUntil time.Time
	lastAttempt time.Time
}

type loginThrottle struct {
	mu    sync.Mutex
	now   func() time.Time
	users map[string]*loginEntry

	failTimes   []time.Time // failed logins inside loginGlobalWindow, oldest first
	lastGlobal  time.Time   // last attempt let through in slow mode
	slowEntered bool
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{now: time.Now, users: make(map[string]*loginEntry)}
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

// begin reserves an attempt for username, or returns ErrLoginThrottled. The
// attempt counts before the password is checked, so parallel guesses cannot
// all slip in before the first failure is recorded.
func (t *loginThrottle) begin(username string) error {
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

	key := loginThrottleKey(username)
	e := t.users[key]
	if e != nil && now.Sub(e.lastAttempt) > loginEntryTTL && !now.Before(e.lockedUntil) {
		delete(t.users, key)
		e = nil
	}
	if e == nil {
		if len(t.users) >= loginMaxTrackedNames {
			t.pruneUsersLocked(now)
		}
		e = &loginEntry{}
		t.users[key] = e
	}
	if now.Before(e.lockedUntil) {
		return ErrLoginThrottled
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

// succeeded clears username's record after a correct password.
func (t *loginThrottle) succeeded(username string) {
	t.mu.Lock()
	delete(t.users, loginThrottleKey(username))
	t.mu.Unlock()
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

func (t *loginThrottle) pruneFailsLocked(now time.Time) {
	i := 0
	for i < len(t.failTimes) && now.Sub(t.failTimes[i]) > loginGlobalWindow {
		i++
	}
	if i > 0 {
		t.failTimes = append(t.failTimes[:0], t.failTimes[i:]...)
	}
}

func (t *loginThrottle) pruneUsersLocked(now time.Time) {
	for k, e := range t.users {
		if !now.Before(e.lockedUntil) && now.Sub(e.lastAttempt) > loginBaseLockout {
			delete(t.users, k)
		}
	}
	// Still full (a flood of locked names): drop unlocked ones first, then any.
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
func (us *UserStore) verifyLogin(t *loginThrottle, username, password string) (*User, error) {
	if t == nil {
		t = defaultLoginThrottle
	}
	if err := t.begin(username); err != nil {
		return nil, err
	}
	user, err := us.VerifyPassword(username, password)
	if err != nil {
		t.failed()
		return nil, err
	}
	t.succeeded(username)
	return user, nil
}

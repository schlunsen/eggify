package server

import (
	"database/sql"
	"errors"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/logging"
)

// MFA code throttling per account (in memory, like loginThrottle).
//
// Each temporary token allows 5 guesses and each IP 15 per 15 minutes, but a
// correct password yields a fresh temporary token every time and resets the
// password lockout, so someone who knows the password could keep guessing
// codes from many addresses. This counts verification attempts per account,
// across temporary tokens and addresses:
//
//   - Every attempt counts before its code is checked (parallel guesses
//     cannot all slip in). From the mfaFreeAttempts-th attempt on, the
//     account's MFA verification is locked for 1 min, doubling with each
//     further attempt up to 15 min. While locked, a code is refused without
//     being checked, the correct one included.
//   - Only a successful verification (TOTP or backup code) clears the count.
//     A correct password does not. Without a success the count is forgotten
//     mfaEntryTTL after the last attempt.
//   - Only requests with a valid, unexpired login temporary token are counted,
//     so the lock can only be caused by someone who knows the password.
//   - The last accepted TOTP time step is kept per account, and a code for
//     that step or an earlier one is refused (no reuse inside its window).
//
// The state is kept in the mfa_throttle table (migration 66) when the
// throttle has a database (persistMFAThrottle), written before the attempt's
// code is checked, so a restart neither clears a lock nor lets a code that was
// just accepted be used again inside its 90 s window.

const (
	mfaFreeAttempts       = 5
	mfaBaseLockout        = time.Minute
	mfaMaxLockout         = 15 * time.Minute
	mfaEntryTTL           = 24 * time.Hour
	mfaMaxTrackedAccounts = 10000
	// A time step older than this many steps can no longer be accepted
	// (Skew 1), so remembering it is pointless.
	mfaStepMemory = 4
)

// ErrMFAThrottled is returned when a code is refused before it is checked.
var ErrMFAThrottled = errors.New("too many verification attempts")

type mfaEntry struct {
	attempts    int
	lockedUntil time.Time
	lastAttempt time.Time
	lastStep    int64 // last accepted TOTP time step (0 = none)
}

type mfaThrottle struct {
	mu       sync.Mutex
	now      func() time.Time
	accounts map[string]*mfaEntry
	db       *sql.DB // optional: write-through store (mfa_throttle table)
}

func newMFAThrottle() *mfaThrottle {
	return &mfaThrottle{now: time.Now, accounts: make(map[string]*mfaEntry)}
}

// persistMFAThrottle returns a throttle that loads its state from db and
// writes every change through to it.
func persistMFAThrottle(db *sql.DB) *mfaThrottle {
	t := newMFAThrottle()
	if db == nil {
		return t
	}
	t.db = db
	rows, err := db.Query(`SELECT username, attempts, locked_until, last_attempt, last_step FROM mfa_throttle`)
	if err != nil {
		logging.Warning("MFA throttle: could not load saved state: %v", err)
		return t
	}
	defer rows.Close()
	now := t.now()
	for rows.Next() {
		var name string
		var attempts int
		var locked, last, step int64
		if err := rows.Scan(&name, &attempts, &locked, &last, &step); err != nil {
			continue
		}
		e := &mfaEntry{attempts: attempts, lockedUntil: unixOrZero(locked), lastAttempt: unixOrZero(last), lastStep: step}
		if t.staleLocked(e, now) {
			_, _ = db.Exec(`DELETE FROM mfa_throttle WHERE username = ?`, name)
			continue
		}
		t.accounts[name] = e
	}
	return t
}

func unixOrZero(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

func unixOrZeroOf(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// staleLocked reports whether e holds nothing worth remembering.
func (t *mfaThrottle) staleLocked(e *mfaEntry, now time.Time) bool {
	oldestStep := now.Unix()/totpPeriod - mfaStepMemory
	idle := e.attempts == 0 || now.Sub(e.lastAttempt) > mfaEntryTTL
	return idle && !now.Before(e.lockedUntil) && e.lastStep < oldestStep
}

// saveLocked writes one account's state through to the database. A failed
// write is logged; the in-memory state still applies.
func (t *mfaThrottle) saveLocked(key string, e *mfaEntry) {
	if t.db == nil {
		return
	}
	if _, err := t.db.Exec(`INSERT INTO mfa_throttle (username, attempts, locked_until, last_attempt, last_step)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET attempts = excluded.attempts, locked_until = excluded.locked_until,
			last_attempt = excluded.last_attempt, last_step = excluded.last_step`,
		key, e.attempts, unixOrZeroOf(e.lockedUntil), unixOrZeroOf(e.lastAttempt), e.lastStep); err != nil {
		logging.Warning("MFA throttle: could not save state: %v", err)
	}
}

func (t *mfaThrottle) entryLocked(username string, now time.Time) *mfaEntry {
	key := loginThrottleKey(username)
	e := t.accounts[key]
	if e == nil {
		if len(t.accounts) >= mfaMaxTrackedAccounts {
			t.pruneLocked(now)
		}
		e = &mfaEntry{}
		t.accounts[key] = e
	}
	return e
}

// begin reserves one verification attempt for the account, or returns
// ErrMFAThrottled and how long the lock still lasts.
func (t *mfaThrottle) begin(username string) (time.Duration, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	e := t.entryLocked(username, now)
	if now.Before(e.lockedUntil) {
		return e.lockedUntil.Sub(now), ErrMFAThrottled
	}
	if e.attempts > 0 && now.Sub(e.lastAttempt) > mfaEntryTTL {
		e.attempts = 0
	}
	e.attempts++
	e.lastAttempt = now
	if e.attempts >= mfaFreeAttempts {
		lock := mfaBaseLockout << uint(min(e.attempts-mfaFreeAttempts, 10))
		if lock > mfaMaxLockout {
			lock = mfaMaxLockout
		}
		e.lockedUntil = now.Add(lock)
	}
	t.saveLocked(loginThrottleKey(username), e)
	return 0, nil
}

// succeeded clears the account's count after a correct code.
func (t *mfaThrottle) succeeded(username string) {
	t.mu.Lock()
	if e := t.accounts[loginThrottleKey(username)]; e != nil {
		e.attempts = 0
		e.lockedUntil = time.Time{}
		t.saveLocked(loginThrottleKey(username), e)
	}
	t.mu.Unlock()
}

// acceptStep records a valid TOTP code's time step. It returns false when a
// code for that step or a later one was already accepted (a replay).
func (t *mfaThrottle) acceptStep(username string, step int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entryLocked(username, t.now())
	if step <= e.lastStep {
		return false
	}
	e.lastStep = step
	t.saveLocked(loginThrottleKey(username), e)
	return true
}

func (t *mfaThrottle) pruneLocked(now time.Time) {
	for k, e := range t.accounts {
		if t.staleLocked(e, now) {
			delete(t.accounts, k)
			if t.db != nil {
				_, _ = t.db.Exec(`DELETE FROM mfa_throttle WHERE username = ?`, k)
			}
		}
	}
	// Still full: every tracked account has recent failures (each needs its
	// password). Make room rather than refuse everyone.
	if len(t.accounts) >= mfaMaxTrackedAccounts {
		for k := range t.accounts {
			delete(t.accounts, k)
			if len(t.accounts) < mfaMaxTrackedAccounts/2 {
				break
			}
		}
	}
}

// defaultMFAThrottle is used by servers built without one.
var defaultMFAThrottle = newMFAThrottle()

func (s *Server) mfaLimits() *mfaThrottle {
	if s.mfaThrottle != nil {
		return s.mfaThrottle
	}
	return defaultMFAThrottle
}

// loginLimits is the password throttle the login handler uses, so password
// checks elsewhere (MFA disable, backup codes) count and lock with it.
func (s *Server) loginLimits() *loginThrottle {
	if s.authHandler != nil && s.authHandler.loginThrottle != nil {
		return s.authHandler.loginThrottle
	}
	return defaultLoginThrottle
}

// mfaThrottled answers a code refused because the account's MFA
// verification is locked.
func mfaThrottled(c *fiber.Ctx, retryAfter time.Duration) error {
	if secs := int(math.Ceil(retryAfter.Seconds())); secs > 0 {
		c.Set(fiber.HeaderRetryAfter, strconv.Itoa(secs))
	}
	return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
		"error":   "Too many verification attempts",
		"message": "Too many verification attempts. Please wait a few minutes and try again.",
	})
}

// passwordConfirmThrottled answers a "confirm your password" check refused
// by the login throttle.
func passwordConfirmThrottled(c *fiber.Ctx) error {
	return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
		"error":   "Too many password attempts",
		"message": "Too many password attempts. Please wait a few minutes and try again.",
	})
}

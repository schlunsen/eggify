package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/pquerna/otp/totp"
)

// mfaLoginEnv is newMFATestEnv plus the real login handler and the
// password-confirming endpoints, with clocks the test moves.
type mfaLoginEnv struct {
	*mfaTestEnv
	now *time.Time
}

func newMFALoginEnv(t *testing.T, backupCodes []string) *mfaLoginEnv {
	t.Helper()
	e := newMFATestEnv(t, backupCodes)
	now := time.Now()
	clock := func() time.Time { return now }

	h := NewAuthHandler(e.s.userStore, e.s.mfaManager, nil, nil, e.s.db, e.repo, e.s.config, false)
	h.loginThrottle.now = clock
	e.s.authHandler = h
	e.s.mfaThrottle = newMFAThrottle()
	e.s.mfaThrottle.now = clock

	// The handlers under test, with alice signed in for the routes that
	// need a session (the session middleware is not what is tested here).
	app := fiber.New()
	app.Post("/api/auth/login", h.HandleLogin)
	app.Post("/api/auth/mfa/verify", e.s.handleMFAVerify)
	signedIn := func(c *fiber.Ctx) error {
		c.Locals("user", &User{Username: "alice", IsAdmin: true})
		return c.Next()
	}
	app.Post("/api/auth/mfa/disable", signedIn, e.s.handleMFADisable)
	app.Post("/api/auth/mfa/backup-codes/regenerate", signedIn, e.s.handleMFABackupCodesRegenerate)
	app.Post("/api/auth/change-password", signedIn, h.HandleChangePassword)
	e.app = app
	return &mfaLoginEnv{mfaTestEnv: e, now: &now}
}

func (e *mfaLoginEnv) post(t *testing.T, path string, body map[string]string) (int, map[string]any, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.app.Test(req, 30000)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, resp.Header.Get("Retry-After")
}

// login signs in with the correct password and returns the temporary token.
func (e *mfaLoginEnv) login(t *testing.T) string {
	t.Helper()
	st, out, _ := e.post(t, "/api/auth/login", map[string]string{"username": "alice", "password": "password123"})
	tok, _ := out["temporary_token"].(string)
	if st != fiber.StatusOK || tok == "" {
		t.Fatalf("login with the correct password = %d %v, want 200 with a temporary token", st, out)
	}
	return tok
}

func (e *mfaLoginEnv) code(t *testing.T, tok, code string) (int, string) {
	t.Helper()
	st, _, retry := e.post(t, "/api/auth/mfa/verify", map[string]string{"temporary_token": tok, "code": code})
	return st, retry
}

// codeAt is the TOTP code for the time step `steps` after the current one.
// Each accepted code must be newer than the last, so tests ask for 0, then 1.
func (e *mfaLoginEnv) codeAt(t *testing.T, steps int) string {
	t.Helper()
	c, err := totp.GenerateCode(e.secret, time.Now().Add(time.Duration(steps)*30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// awayFromStepEdge waits out the last seconds of a TOTP time step, so a code
// made for "now" (or the next step) is still inside the accepted window when
// the handler checks it a moment later.
func awayFromStepEdge() {
	if left := 30 - time.Now().Unix()%30; left <= 3 {
		time.Sleep(time.Duration(left) * time.Second)
	}
}

// The attack: the password is known, so every login yields a fresh temporary
// token (5 more guesses each) and resets the password lockout. Wrong codes
// are counted on the account, whatever token or address they come with (the
// per-IP limiter lives in routes/auth.go and is not mounted here, as if every
// guess came from its own address): after 5 the account's MFA is locked, and
// then even the correct code is refused until the lock ends.
func TestRedTeam_MFA_GuessingAcrossTemporaryTokensLocksTheAccount(t *testing.T) {
	awayFromStepEdge()
	e := newMFALoginEnv(t, nil)
	wrong := wrongTOTP(e.secret)

	for i := 0; i < mfaFreeAttempts; i++ {
		tok := e.login(t) // a new temporary token for every guess
		if st, _ := e.code(t, tok, wrong); st != fiber.StatusBadRequest {
			t.Fatalf("wrong code %d = %d, want 400", i+1, st)
		}
	}

	// Locked: a new login still works (the password is right), but the
	// correct code on its fresh token is refused, unchecked.
	tok := e.login(t)
	st, retry := e.code(t, tok, e.codeAt(t, 0))
	if st != fiber.StatusTooManyRequests {
		t.Fatalf("correct code while the account is locked = %d, want 429", st)
	}
	if secs, err := strconv.Atoi(retry); err != nil || secs < 1 || secs > 60 {
		t.Fatalf("Retry-After = %q, want 1..60 seconds", retry)
	}
	// More tokens do not help, and refused tries do not use up a token.
	for i := 0; i < 8; i++ {
		if st, _ := e.code(t, e.login(t), wrong); st != fiber.StatusTooManyRequests {
			t.Fatalf("guess %d during the lock = %d, want 429", i+1, st)
		}
	}

	// After the lock the correct code works on the token refused before,
	// and the count starts over: 4 more wrong codes are checked, not refused.
	*e.now = e.now.Add(mfaBaseLockout + time.Second)
	if st, _ := e.code(t, tok, e.codeAt(t, 0)); st != fiber.StatusOK {
		t.Fatalf("correct code after the lock ended = %d, want 200", st)
	}
	tok = e.login(t)
	for i := 0; i < mfaFreeAttempts-1; i++ {
		if st, _ := e.code(t, tok, wrong); st != fiber.StatusBadRequest {
			t.Fatalf("wrong code %d after a success = %d, want 400 (count was not reset)", i+1, st)
		}
	}
	if st, _ := e.code(t, tok, e.codeAt(t, 1)); st != fiber.StatusOK {
		t.Fatalf("correct code on the 5th attempt after a success = %d, want 200", st)
	}
}

// A correct password clears the password lockout, never the MFA count.
func TestRedTeam_MFA_CorrectPasswordDoesNotResetTheCodeCount(t *testing.T) {
	awayFromStepEdge()
	e := newMFALoginEnv(t, nil)
	wrong := wrongTOTP(e.secret)

	tok := e.login(t)
	for i := 0; i < mfaFreeAttempts-1; i++ {
		if st, _ := e.code(t, tok, wrong); st != fiber.StatusBadRequest {
			t.Fatalf("wrong code %d = %d, want 400", i+1, st)
		}
	}
	for i := 0; i < 3; i++ {
		e.login(t) // correct passwords in between
	}
	if st, _ := e.code(t, e.login(t), wrong); st != fiber.StatusBadRequest {
		t.Fatalf("5th wrong code = %d, want 400", st)
	}
	if st, _ := e.code(t, e.login(t), e.codeAt(t, 0)); st != fiber.StatusTooManyRequests {
		t.Fatalf("correct code after 5 wrong ones and several correct passwords = %d, want 429", st)
	}
}

// Without a valid temporary token (so without the password) nothing is
// counted: nobody can lock an account's MFA from outside.
func TestRedTeam_MFA_NoLockWithoutAValidTemporaryToken(t *testing.T) {
	awayFromStepEdge()
	e := newMFALoginEnv(t, nil)
	for i := 0; i < 30; i++ {
		if st, _ := e.code(t, GenerateTemporaryToken(), "000000"); st != fiber.StatusBadRequest {
			t.Fatalf("made-up temporary token = %d, want 400", st)
		}
	}
	if st, _ := e.code(t, e.login(t), e.codeAt(t, 0)); st != fiber.StatusOK {
		t.Fatalf("correct code after 30 requests without a valid token = %d, want 200", st)
	}
}

// A backup code clears the count like a TOTP code does, and wrong backup
// codes count like wrong TOTP codes.
func TestMFA_BackupCodesCountAndReset(t *testing.T) {
	e := newMFALoginEnv(t, []string{"ABCD2345", "QWER6789"})
	wrong := wrongTOTP(e.secret)
	tok := e.login(t)
	for i := 0; i < mfaFreeAttempts-1; i++ {
		e.code(t, tok, wrong)
	}
	if st, _ := e.code(t, tok, "ABCD2345"); st != fiber.StatusOK {
		t.Fatalf("backup code on the 5th attempt = %d, want 200", st)
	}
	for i := 0; i < mfaFreeAttempts; i++ {
		if st, _ := e.code(t, e.login(t), "ZZZZ9999"); st != fiber.StatusBadRequest {
			t.Fatalf("wrong backup code %d = %d, want 400", i+1, st)
		}
	}
	if st, _ := e.code(t, e.login(t), "QWER6789"); st != fiber.StatusTooManyRequests {
		t.Fatalf("valid backup code while locked = %d, want 429", st)
	}
}

// A TOTP code is accepted once: a replay inside its window (someone who saw
// the code being typed) is refused, in turn or in parallel.
func TestRedTeam_MFA_TOTPCodeCannotBeReused(t *testing.T) {
	awayFromStepEdge()
	e := newMFALoginEnv(t, nil)
	good := e.codeAt(t, 0)

	if st, _ := e.code(t, e.login(t), good); st != fiber.StatusOK {
		t.Fatalf("first use = %d, want 200", st)
	}
	if st, _ := e.code(t, e.login(t), good); st != fiber.StatusBadRequest {
		t.Fatalf("second use of the same code = %d, want 400", st)
	}
	// An older code than the last accepted one is refused too.
	older, _ := totp.GenerateCode(e.secret, time.Now().Add(-30*time.Second))
	if older != good {
		if st, _ := e.code(t, e.login(t), older); st != fiber.StatusBadRequest {
			t.Fatalf("code of an earlier time step = %d, want 400", st)
		}
	}

	// The next code, used on 8 logins at once, signs in exactly one.
	next := e.codeAt(t, 1)
	toks := make([]string, 8)
	for i := range toks {
		toks[i] = e.login(t)
	}
	var ok int32
	var wg sync.WaitGroup
	for _, tok := range toks {
		wg.Add(1)
		go func(tok string) {
			defer wg.Done()
			if st, _ := e.code(t, tok, next); st == fiber.StatusOK {
				atomic.AddInt32(&ok, 1)
			}
		}(tok)
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("one code used on 8 logins in parallel signed in %d times, want 1", ok)
	}
}

func TestMFAManager_MatchTOTPStep(t *testing.T) {
	e := newMFATestEnv(t, nil)
	at := time.Unix(1_800_000_015, 0)
	cur := at.Unix() / 30
	for _, d := range []int64{-1, 0, 1} {
		code, _ := totp.GenerateCode(e.secret, at.Add(time.Duration(d)*30*time.Second))
		step, ok, err := e.s.mfaManager.MatchTOTPStep(e.secret, code, at)
		if err != nil || !ok || step != cur+d {
			t.Fatalf("offset %d: step=%d ok=%v err=%v, want step %d", d, step, ok, err, cur+d)
		}
	}
	far, _ := totp.GenerateCode(e.secret, at.Add(5*time.Minute))
	if _, ok, _ := e.s.mfaManager.MatchTOTPStep(e.secret, far, at); ok {
		// 1 in ~333k: the far code equals one in the window.
		t.Skip("far code collides with a code in the window")
	}
	if _, ok, _ := e.s.mfaManager.MatchTOTPStep(e.secret, "12345", at); ok {
		t.Fatal("a 5-digit code matched")
	}
}

func TestMFAThrottle_BackoffDecayAndBound(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	th := newMFAThrottle()
	th.now = func() time.Time { return now }

	for i := 0; i < mfaFreeAttempts; i++ {
		if _, err := th.begin("alice"); err != nil {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	// 1, 2, 4, 8, 15, 15 minutes.
	for i, want := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, mfaMaxLockout, mfaMaxLockout} {
		left, err := th.begin("Alice ") // same account, however it is written
		if err == nil || left != want {
			t.Fatalf("lock %d: left=%v err=%v, want %v", i+1, left, err, want)
		}
		now = now.Add(want - time.Second)
		if _, err := th.begin("alice"); err == nil {
			t.Fatalf("lock %d ended early", i+1)
		}
		now = now.Add(time.Second)
		if _, err := th.begin("alice"); err != nil {
			t.Fatalf("lock %d did not end after %v", i+1, want)
		}
	}
	// Another account is unaffected.
	if _, err := th.begin("bob"); err != nil {
		t.Fatal("bob refused because of alice")
	}
	// A success clears the count; so does a day without attempts.
	now = now.Add(mfaMaxLockout)
	th.succeeded("alice")
	for i := 0; i < mfaFreeAttempts; i++ {
		if _, err := th.begin("alice"); err != nil {
			t.Fatalf("attempt %d after a success refused", i+1)
		}
	}
	now = now.Add(mfaEntryTTL + time.Second)
	for i := 0; i < mfaFreeAttempts-1; i++ {
		th.begin("alice")
	}
	if _, err := th.begin("alice"); err != nil {
		t.Fatal("the count was not forgotten after the decay window")
	}
	// A success keeps the last accepted time step.
	if !th.acceptStep("alice", 100) || th.acceptStep("alice", 100) || th.acceptStep("alice", 99) || !th.acceptStep("alice", 101) {
		t.Fatal("acceptStep must accept only newer steps")
	}
	th.succeeded("alice")
	if th.acceptStep("alice", 101) {
		t.Fatal("a success forgot the last accepted step")
	}

	// The map stays bounded.
	for i := 0; i < mfaMaxTrackedAccounts+500; i++ {
		th.begin(fmt.Sprintf("user-%d", i))
	}
	if n := len(th.accounts); n > mfaMaxTrackedAccounts {
		t.Fatalf("%d accounts tracked, cap is %d", n, mfaMaxTrackedAccounts)
	}
}

// A stolen session must not be a way to guess the password: the password
// checks on MFA disable, backup-code regeneration and password change go
// through the login throttle, and lock like wrong logins.
func TestRedTeam_PasswordConfirmationsAreThrottledLikeLogin(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		body       func(pw string) map[string]string
		wrong      int
	}{
		{"mfa disable", "/api/auth/mfa/disable", func(pw string) map[string]string { return map[string]string{"password": pw} }, fiber.StatusUnauthorized},
		{"backup codes", "/api/auth/mfa/backup-codes/regenerate", func(pw string) map[string]string { return map[string]string{"password": pw} }, fiber.StatusUnauthorized},
		{"change password", "/api/auth/change-password", func(pw string) map[string]string {
			return map[string]string{"old_password": pw, "new_password": "another-password-1"}
		}, fiber.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newMFALoginEnv(t, nil)
			for i := 0; i < loginFreeAttempts; i++ {
				if st, _, _ := e.post(t, tc.path, tc.body(fmt.Sprintf("guess-%d-xxxx", i))); st != tc.wrong {
					t.Fatalf("wrong password %d = %d, want %d", i+1, st, tc.wrong)
				}
			}
			// Locked: the right password is refused unchecked, here and at login.
			if st, _, _ := e.post(t, tc.path, tc.body("password123")); st != fiber.StatusTooManyRequests {
				t.Fatalf("correct password after %d wrong ones = %d, want 429", loginFreeAttempts, st)
			}
			if st, _, _ := e.post(t, "/api/auth/login", map[string]string{"username": "alice", "password": "password123"}); st != fiber.StatusTooManyRequests {
				t.Fatalf("login while locked = %d, want 429", st)
			}
			cfg, err := e.repo.GetMFAConfig("alice")
			if err != nil || cfg == nil || !cfg.IsEnabled {
				t.Fatalf("MFA must still be enabled (cfg=%v err=%v)", cfg, err)
			}
			if _, err := e.s.userStore.VerifyPassword("alice", "password123"); err != nil {
				t.Fatal("the password changed while the check was locked")
			}
			// After the lock, the right password works.
			*e.now = e.now.Add(loginBaseLockout + time.Second)
			if st, out, _ := e.post(t, tc.path, tc.body("password123")); st != fiber.StatusOK {
				t.Fatalf("correct password after the lock = %d %v, want 200", st, out)
			}
		})
	}
}

// The user's own successful logins never lock these checks, and a login
// lockout that was caused elsewhere covers them too.
func TestPasswordConfirmation_SharesTheLoginThrottle(t *testing.T) {
	e := newMFALoginEnv(t, nil)
	for i := 0; i < loginFreeAttempts*3; i++ {
		e.login(t)
	}
	if st, out, _ := e.post(t, "/api/auth/mfa/backup-codes/regenerate", map[string]string{"password": "password123"}); st != fiber.StatusOK {
		t.Fatalf("regenerate after many successful logins = %d %v, want 200", st, out)
	}
	for i := 0; i < loginFreeAttempts; i++ {
		e.post(t, "/api/auth/login", map[string]string{"username": "alice", "password": "wrong-password"})
	}
	if st, _, _ := e.post(t, "/api/auth/mfa/disable", map[string]string{"password": "password123"}); st != fiber.StatusTooManyRequests {
		t.Fatalf("MFA disable while the login is locked = %d, want 429", st)
	}
}

// Failed codes count towards the global failure cap of the login throttle.
func TestMFA_FailedCodesCountTowardsTheGlobalCap(t *testing.T) {
	e := newMFALoginEnv(t, nil)
	before := len(e.s.authHandler.loginThrottle.failTimes)
	e.code(t, e.login(t), wrongTOTP(e.secret))
	if got := len(e.s.authHandler.loginThrottle.failTimes); got != before+1 {
		t.Fatalf("global failures = %d, want %d", got, before+1)
	}
}

// TestRedTeam_MFAThrottle_RestartKeepsLocksAndTOTPReplayProtection: the lock
// and the last accepted time step lived in memory only, so restarting Eggify
// cleared a lock (more code guesses) and let an accepted code be used again.
func TestRedTeam_MFAThrottle_RestartKeepsLocksAndTOTPReplayProtection(t *testing.T) {
	db := NewTestDB(t)
	now := time.Now()
	first := persistMFAThrottle(db.GetDB())
	first.now = func() time.Time { return now }

	for i := 0; i < mfaFreeAttempts; i++ {
		if _, err := first.begin("Alice"); err != nil {
			t.Fatalf("attempt %d refused early", i+1)
		}
	}
	if _, err := first.begin("alice"); err == nil {
		t.Fatal("account not locked after the free attempts")
	}
	step := now.Unix() / totpPeriod
	if !first.acceptStep("bob", step) {
		t.Fatal("a fresh step was refused")
	}

	// Restart.
	second := persistMFAThrottle(db.GetDB())
	second.now = func() time.Time { return now.Add(10 * time.Second) }
	if _, err := second.begin("alice"); err == nil {
		t.Fatal("a restart cleared the MFA lock")
	}
	if second.acceptStep("bob", step) {
		t.Fatal("a restart let an already accepted TOTP code be used again")
	}
	if !second.acceptStep("bob", step+1) {
		t.Fatal("the next time step must still be accepted")
	}
	// The lock still ends on schedule.
	second.now = func() time.Time { return now.Add(mfaBaseLockout + time.Minute) }
	if _, err := second.begin("alice"); err != nil {
		t.Fatalf("lock did not expire after a restart: %v", err)
	}
}

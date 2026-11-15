package server

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/pquerna/otp/totp"

	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/remoteaccess"
)

// ---------------------------------------------------------------------------
// MFA

type mfaTestEnv struct {
	s      *Server
	app    *fiber.App
	repo   *database.Repository
	secret string
}

func newMFATestEnv(t *testing.T, backupCodes []string) *mfaTestEnv {
	t.Helper()
	db := NewTestDB(t)
	us := NewUserStore(t.TempDir())
	us.SetDatabase(db)
	if err := us.Initialize(); err != nil {
		t.Fatal(err)
	}
	if err := us.CreateUser("alice", "password123", true); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	mm, err := NewMFAManager(key)
	if err != nil {
		t.Fatal(err)
	}
	k, err := mm.GenerateTOTPSecret("alice", "Eggify")
	if err != nil {
		t.Fatal(err)
	}
	enc, iv, err := mm.EncryptSecret(k.Secret())
	if err != nil {
		t.Fatal(err)
	}
	repo := database.NewRepository(db)
	now := time.Now()
	mcfg := &database.MFAConfig{Username: "alice", IsEnabled: true, MFAEnabledAt: &now, CreatedAt: now}
	mcfg.TOTPSecret.String, mcfg.TOTPSecret.Valid = enc, true
	mcfg.TOTPSecretIV.String, mcfg.TOTPSecretIV.Valid = iv, true
	if len(backupCodes) > 0 {
		coll, err := mm.NewBackupCodeCollection(backupCodes)
		if err != nil {
			t.Fatal(err)
		}
		js, _ := mm.SerializeBackupCodes(coll)
		mcfg.BackupCodes.String, mcfg.BackupCodes.Valid = js, true
	}
	if err := repo.SaveMFAConfig(mcfg); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	cfg.Auth.UserAuthEnabled = true
	s := &Server{db: db, userStore: us, mfaManager: mm, config: cfg, repo: repo, mfaThrottle: newMFAThrottle()}
	app := fiber.New()
	app.Use(NewSessionAuthMiddleware(us, true, false).Handler())
	app.Post("/api/auth/mfa/verify", s.handleMFAVerify)
	app.Get("/api/auth/status", s.handleAuthStatus)
	app.Get("/api/data", func(c *fiber.Ctx) error { return c.SendString("secret") })
	return &mfaTestEnv{s: s, app: app, repo: repo, secret: k.Secret()}
}

func (e *mfaTestEnv) newLoginToken(t *testing.T) string {
	t.Helper()
	tok := GenerateTemporaryToken()
	if err := e.repo.SaveMFATemporaryToken(&database.MFATemporaryToken{
		Token: tok, Username: "alice", TokenType: "login", MaxAttempts: 5,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(5 * time.Minute), IPAddress: "0.0.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	return tok
}

func (e *mfaTestEnv) verify(t *testing.T, tempToken, code string, cookie string) (int, map[string]any, *http.Response) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"temporary_token": tempToken, "code": code})
	req := httptest.NewRequest("POST", "/api/auth/mfa/verify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.Header.Set("Cookie", "session_token="+cookie)
	}
	resp, err := e.app.Test(req, 30000)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out, resp
}

// wrongTOTP returns a 6-digit code that is not valid around now.
func wrongTOTP(secret string) string {
	valid := map[string]bool{}
	for _, d := range []time.Duration{-60 * time.Second, -30 * time.Second, 0, 30 * time.Second, 60 * time.Second} {
		c, _ := totp.GenerateCode(secret, time.Now().Add(d))
		valid[c] = true
	}
	for i := 0; ; i++ {
		c := fmt.Sprintf("%06d", (123456+i*7919)%1000000)
		if !valid[c] {
			return c
		}
	}
}

// 60 parallel guesses on one temporary token: at most 5 are evaluated.
func TestRedTeam_MFA_ParallelGuessesLimitedToMaxAttempts(t *testing.T) {
	e := newMFATestEnv(t, nil)
	tok := e.newLoginToken(t)
	wrong := wrongTOTP(e.secret)

	var evaluated, limited, other int32
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, _, _ := e.verify(t, tok, wrong, "")
			switch st {
			case fiber.StatusBadRequest:
				atomic.AddInt32(&evaluated, 1)
			case fiber.StatusTooManyRequests:
				atomic.AddInt32(&limited, 1)
			default:
				atomic.AddInt32(&other, 1)
			}
		}()
	}
	wg.Wait()
	if evaluated > 5 || evaluated == 0 || other != 0 {
		t.Fatalf("evaluated=%d limited=%d other=%d; want 1..5 evaluated", evaluated, limited, other)
	}
	// The right code no longer works on that token either.
	good, _ := totp.GenerateCode(e.secret, time.Now())
	if st, _, _ := e.verify(t, tok, good, ""); st != fiber.StatusTooManyRequests {
		t.Fatalf("correct code after the limit = %d, want 429", st)
	}
}

// One backup code used in parallel (on several login tokens) works once.
func TestRedTeam_MFA_BackupCodeUsedOnceInParallel(t *testing.T) {
	e := newMFATestEnv(t, []string{"ABCD2345", "QWER6789"})
	var ok int32
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		tok := e.newLoginToken(t)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if st, _, _ := e.verify(t, tok, "abcd2345", ""); st == 200 {
				atomic.AddInt32(&ok, 1)
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("backup code accepted %d times, want 1", ok)
	}
	// The other code is still usable, the used one is not.
	if st, _, _ := e.verify(t, e.newLoginToken(t), "ABCD2345", ""); st == 200 {
		t.Fatal("used backup code accepted again")
	}
	if st, out, _ := e.verify(t, e.newLoginToken(t), "QWER6789", ""); st != 200 {
		t.Fatalf("unused backup code = %d %v", st, out)
	}
}

// Two parallel correct TOTP codes on one token give one session.
func TestRedTeam_MFA_TokenConsumedOnce(t *testing.T) {
	e := newMFATestEnv(t, nil)
	tok := e.newLoginToken(t)
	good, _ := totp.GenerateCode(e.secret, time.Now())
	var ok int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if st, _, _ := e.verify(t, tok, good, ""); st == 200 {
				atomic.AddInt32(&ok, 1)
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("one temporary token produced %d sessions", ok)
	}
}

// An MFA-pending session (OAuth login of an MFA user) is refused by
// ValidateSession, reaches only the MFA step, and is revoked after it.
func TestRedTeam_MFA_PendingSessionRefusedAndRevokedAfterVerify(t *testing.T) {
	e := newMFATestEnv(t, nil)
	tok := e.newLoginToken(t)
	pending, err := e.s.userStore.createMFAPendingSession("alice", tok)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.userStore.ValidateSession(pending); err == nil {
		t.Fatal("ValidateSession accepted an MFA-pending session")
	}
	if e.s.userStore.userForSessionToken(pending) != nil {
		t.Fatal("userForSessionToken accepted an MFA-pending session")
	}

	get := func(path string) (int, map[string]any) {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Cookie", "session_token="+pending)
		resp, err := e.app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		b, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(b, &out)
		return resp.StatusCode, out
	}
	if st, out := get("/api/data"); st != fiber.StatusForbidden || out["mfa_required"] != true {
		t.Fatalf("pending session on /api/data = %d %v, want 403 mfa_required", st, out)
	}
	if st, out := get("/api/auth/status"); st != 200 || out["mfa_required"] != true || out["mfa_temp_token"] != tok {
		t.Fatalf("pending session on status = %d %v", st, out)
	}

	good, _ := totp.GenerateCode(e.secret, time.Now())
	st, out, resp := e.verify(t, tok, good, pending)
	if st != 200 {
		t.Fatalf("verify = %d %v", st, out)
	}
	if _, _, err := e.s.userStore.validateSessionAllowPending(pending); err == nil {
		t.Fatal("MFA-pending session still exists after a successful verify")
	}
	var fresh string
	for _, ck := range resp.Cookies() {
		if ck.Name == "session_token" {
			fresh = ck.Value
		}
	}
	if fresh == "" || fresh == pending {
		t.Fatal("no new session cookie after verify")
	}
	if _, err := e.s.userStore.ValidateSession(fresh); err != nil {
		t.Fatalf("new session invalid: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Login throttling

func newLoginTestApp(t *testing.T) (*fiber.App, *UserStore) {
	t.Helper()
	db := NewTestDB(t)
	us := NewUserStore(t.TempDir())
	us.SetDatabase(db)
	if err := us.Initialize(); err != nil {
		t.Fatal(err)
	}
	if err := us.CreateUser("alice", "password123", true); err != nil {
		t.Fatal(err)
	}
	h := NewAuthHandler(us, nil, nil, nil, db, database.NewRepository(db), &Config{}, false)
	app := fiber.New()
	app.Post("/api/auth/login", h.HandleLogin)
	return app, us
}

func postLogin(t *testing.T, app *fiber.App, user, pass string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 30000)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// Guesses from many IPs: the username locks after 5 attempts, even for the
// right password, and unknown usernames behave the same.
func TestRedTeam_Login_PerUsernameLockout(t *testing.T) {
	app, _ := newLoginTestApp(t)
	for i := 0; i < 5; i++ {
		if st, _ := postLogin(t, app, "alice", "wrong-password"); st != fiber.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, st)
		}
	}
	st, lockedBody := postLogin(t, app, "alice", "password123")
	if st != fiber.StatusTooManyRequests {
		t.Fatalf("correct password while locked = %d, want 429", st)
	}
	for i := 0; i < 5; i++ {
		postLogin(t, app, "nobody", "wrong-password")
	}
	st2, body2 := postLogin(t, app, "nobody", "whatever")
	if st2 != st || body2 != lockedBody {
		t.Fatalf("unknown user locked answer %d %q differs from real user's %d %q", st2, body2, st, lockedBody)
	}
}

func TestLoginThrottle_BackoffResetAndGlobalCap(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	th := newLoginThrottle()
	th.now = func() time.Time { return now }

	for i := 0; i < 5; i++ {
		if err := th.begin("Bob"); err != nil {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	if th.begin("bob") == nil {
		t.Fatal("6th attempt (any case) not locked")
	}
	now = now.Add(61 * time.Second)
	if th.begin("bob") != nil {
		t.Fatal("not unlocked after 1 min")
	}
	// Lock doubled to 2 min.
	now = now.Add(90 * time.Second)
	if th.begin("bob") == nil {
		t.Fatal("second lockout shorter than 2 min")
	}
	now = now.Add(31 * time.Second)
	if th.begin("bob") != nil {
		t.Fatal("not unlocked after 2 min")
	}
	// Capped at 15 min.
	for i := 0; i < 12; i++ {
		now = now.Add(16 * time.Minute)
		if th.begin("bob") != nil {
			t.Fatalf("still locked after 16 min (round %d)", i)
		}
	}
	th.succeeded("BOB")
	for i := 0; i < 5; i++ {
		if th.begin("bob") != nil {
			t.Fatal("success did not reset the counter")
		}
	}

	// Global cap: after 100 failures, at most one attempt per 2 s.
	g := newLoginThrottle()
	g.now = func() time.Time { return now }
	for i := 0; i < loginGlobalFailCap; i++ {
		g.failed()
	}
	if g.begin("u1") != nil {
		t.Fatal("first attempt in slow mode refused")
	}
	if g.begin("u2") == nil {
		t.Fatal("second attempt within 2 s allowed in slow mode")
	}
	now = now.Add(loginGlobalSlowInterval)
	if g.begin("u3") != nil {
		t.Fatal("attempt after the interval refused (slow mode must not lock out)")
	}
	now = now.Add(loginGlobalWindow + time.Second)
	if g.begin("u4") != nil || g.begin("u5") != nil {
		t.Fatal("slow mode did not end after the window")
	}
}

// An unknown user costs a bcrypt comparison like a wrong password does.
func TestVerifyPassword_UnknownUserComparesDummyHash(t *testing.T) {
	us := NewInMemoryUserStore()
	var calls int32
	prev := bcryptCompare
	bcryptCompare = func(h, p []byte) error {
		atomic.AddInt32(&calls, 1)
		return prev(h, p)
	}
	defer func() { bcryptCompare = prev }()
	if _, err := us.VerifyPassword("ghost", "pw"); err == nil {
		t.Fatal("unknown user verified")
	}
	if calls != 1 {
		t.Fatalf("bcrypt calls for unknown user = %d, want 1", calls)
	}
	us.users["oauthonly"] = &User{Username: "oauthonly", AuthMethod: "oauth"}
	if _, err := us.VerifyPassword("oauthonly", ""); err == nil {
		t.Fatal("password-less user verified with an empty password")
	}
	if calls != 2 {
		t.Fatalf("bcrypt calls for password-less user = %d, want 2", calls)
	}
}

// ---------------------------------------------------------------------------
// Proxy headers

// A local process sending X-Forwarded-For / -Proto / -Host changes neither
// c.IP() (rate limit key) nor the https decision.
func TestRedTeam_ProxyHeadersIgnored(t *testing.T) {
	app := fiber.New(newFiberConfig(true))
	app.Get("/probe", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"ip": c.IP(), "https": requestIsHTTPS(c), "proto": c.Protocol(), "host": c.Hostname()})
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	defer app.Shutdown()

	req, _ := http.NewRequest("GET", "http://"+ln.Addr().String()+"/probe", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out["ip"] != "127.0.0.1" {
		t.Errorf("c.IP() = %v, want the TCP peer 127.0.0.1", out["ip"])
	}
	if out["https"] != false || out["proto"] != "http" {
		t.Errorf("X-Forwarded-Proto counted: %v", out)
	}
	if h, _ := out["host"].(string); strings.Contains(h, "evil") {
		t.Errorf("X-Forwarded-Host counted: %v", out)
	}
}

// ---------------------------------------------------------------------------
// Tunnel path guard

func rawRequest(t *testing.T, addr, target string) int {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: fake.ngrok.test\r\nConnection: close\r\n\r\n", target)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestRedTeam_TunnelGuard_DotSegmentsAndEncodedPaths(t *testing.T) {
	s, prov, mainLn := newRemoteAccessTestServer(t)
	s.app.Get("/wails/*", func(c *fiber.Ctx) error { return c.SendString("desktop bridge") })
	if err := s.remoteAccess.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.remoteAccess.Stop()
	waitUntil(t, "tunnel connected", func() bool { return s.remoteAccess.Status().Status == remoteaccess.StateConnected })
	tun := prov.addr()

	for _, p := range []string{
		"/wails/../api/health", // cleans to /api/health, routed to /wails/*
		"/WAILS/../api/health",
		"/api/./health",
		"/api/health/..",
		"/api/health/.",
		"//api/health",
		"/api//health",
		"/%2e%2e/api/health",
		"/wails/%2E%2E/api/health",
		"/api%2fhealth",
		"/api%2Fhealth",
		"/api%5chealth",
		"/internal/apps",
		"/__desktop/bootstrap",
	} {
		if code := rawRequest(t, tun, p); code != fiber.StatusNotFound {
			t.Errorf("tunnel %s = %d, want 404", p, code)
		}
	}
	for _, p := range []string{"/api/health", "/api/health?next=//x/../y"} {
		if code := rawRequest(t, tun, p); code != 200 {
			t.Errorf("tunnel %s = %d, want 200", p, code)
		}
	}
	// Locally nothing changes.
	if code := rawRequest(t, mainLn.Addr().String(), "/wails/runtime.js"); code != 200 {
		t.Errorf("local /wails/runtime.js = %d", code)
	}
}

func TestSuspiciousTunnelPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/":              false,
		"/api/health":    false,
		"/a.b/c..d/e.":   false,
		"/api/../x":      true,
		"/x/..":          true,
		"/x/./y":         true,
		"//x":            true,
		"/x%2E/y":        true,
		"/x%2f":          true,
		"/x%5C":          true,
		"/_nuxt/app.js":  false,
		"/assets/a%20b":  false,
		"/x/%252e%252e/": false, // double-encoded: Fiber does not decode twice
	} {
		if got := suspiciousTunnelPath(p); got != want {
			t.Errorf("suspiciousTunnelPath(%q) = %v, want %v", p, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// OAuth

type oauthTestEnv struct {
	s        *Server
	app      *fiber.App
	userInfo map[string]any
	cfg      *Config
}

func newOAuthTestEnv(t *testing.T) *oauthTestEnv {
	t.Helper()
	e := &oauthTestEnv{userInfo: map[string]any{}}
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer"})
		case "/userinfo":
			_ = json.NewEncoder(w).Encode(e.userInfo)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(idp.Close)

	db := NewTestDB(t)
	us := NewUserStore(t.TempDir())
	us.SetDatabase(db)
	if err := us.Initialize(); err != nil {
		t.Fatal(err)
	}
	e.cfg = &Config{}
	e.cfg.Auth.OAuth = &OAuthSettings{Enabled: true, Providers: map[string]*OAuthProvider{
		"google": {
			Enabled: true, ClientID: "cid", ClientSecret: "sec", RedirectURI: "http://localhost/cb",
			AuthURL: idp.URL + "/auth", TokenURL: idp.URL + "/token", UserInfoURL: idp.URL + "/userinfo",
			AutoCreateUser: true,
		},
	}}
	e.cfg.Auth.AccessControl = &AccessControlConfig{Enabled: true, AllowedDomains: []string{"*@corp.test"}}
	e.s = &Server{db: db, userStore: us, config: e.cfg, oauthStateManager: NewOAuthStateManager(db)}
	e.app = fiber.New()
	e.app.Get("/api/auth/oauth/authorize", e.s.handleOAuthLogin)
	e.app.Get("/api/auth/oauth/callback/google", e.s.handleOAuthCallback)
	return e
}

// authorize returns the state and the binding cookie.
func (e *oauthTestEnv) authorize(t *testing.T) (string, string) {
	t.Helper()
	resp, err := e.app.Test(httptest.NewRequest("GET", "/api/auth/oauth/authorize?provider=google", nil))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	state, _ := out["state"].(string)
	binder := ""
	for _, c := range resp.Cookies() {
		if c.Name == oauthStateCookie {
			binder = c.Value
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
				t.Errorf("binding cookie flags: HttpOnly=%v SameSite=%v", c.HttpOnly, c.SameSite)
			}
		}
	}
	if state == "" || binder == "" {
		t.Fatalf("authorize: state=%q binder=%q", state, binder)
	}
	return state, binder
}

func (e *oauthTestEnv) callback(t *testing.T, state, binder string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/auth/oauth/callback/google?code=c0de&state="+url.QueryEscape(state), nil)
	if binder != "" {
		req.Header.Set("Cookie", oauthStateCookie+"="+binder)
	}
	resp, err := e.app.Test(req, 10000)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestRedTeam_OAuth_StateBoundToBrowser(t *testing.T) {
	e := newOAuthTestEnv(t)
	e.userInfo = map[string]any{"sub": "1", "email": "ann@corp.test", "email_verified": true}

	// The attacker's state, completed in a browser without the cookie.
	state, _ := e.authorize(t)
	if st, _ := e.callback(t, state, ""); st != fiber.StatusUnauthorized {
		t.Fatalf("callback without binding cookie = %d, want 401", st)
	}
	state2, _ := e.authorize(t)
	_, otherBinder := e.authorize(t)
	if st, _ := e.callback(t, state2, otherBinder); st != fiber.StatusUnauthorized {
		t.Fatalf("callback with another flow's cookie = %d, want 401", st)
	}
	// The right browser gets in.
	state3, binder3 := e.authorize(t)
	if st, body := e.callback(t, state3, binder3); st != fiber.StatusSeeOther {
		t.Fatalf("bound callback = %d %s", st, body)
	}
}

func TestRedTeam_OAuth_UnverifiedEmailRefused(t *testing.T) {
	for name, v := range map[string]any{"bool": false, "string": "false"} {
		t.Run(name, func(t *testing.T) {
			e := newOAuthTestEnv(t)
			e.userInfo = map[string]any{"sub": "2", "email": "boss@corp.test", "email_verified": v}
			state, binder := e.authorize(t)
			if st, body := e.callback(t, state, binder); st != fiber.StatusForbidden {
				t.Fatalf("unverified email = %d %s, want 403", st, body)
			}
			if e.s.userStore.FindUserByEmail("boss@corp.test") != nil {
				t.Fatal("account created for an unverified email")
			}
		})
	}
	// Google without email_verified at all is refused too.
	e := newOAuthTestEnv(t)
	e.userInfo = map[string]any{"sub": "3", "email": "boss@corp.test"}
	state, binder := e.authorize(t)
	if st, _ := e.callback(t, state, binder); st != fiber.StatusForbidden {
		t.Fatalf("google email without email_verified = %d, want 403", st)
	}
}

// admin_users ["admin"] must not make admin@<any allowed domain> an admin;
// only a full email entry does.
func TestRedTeam_OAuth_AdminByLocalPartRefused(t *testing.T) {
	e := newOAuthTestEnv(t)
	e.cfg.Auth.AdminUsers = []string{"admin", "boss@corp.test"}
	e.userInfo = map[string]any{"sub": "10", "email": "admin@corp.test", "email_verified": true}
	state, binder := e.authorize(t)
	if st, body := e.callback(t, state, binder); st != fiber.StatusSeeOther {
		t.Fatalf("login = %d %s", st, body)
	}
	if u := e.s.userStore.FindUserByEmail("admin@corp.test"); u == nil || u.IsAdmin {
		t.Fatalf("admin@corp.test: %+v (must exist, not admin)", u)
	}

	e.userInfo = map[string]any{"sub": "11", "email": "boss@corp.test", "email_verified": true}
	state, binder = e.authorize(t)
	if st, body := e.callback(t, state, binder); st != fiber.StatusSeeOther {
		t.Fatalf("login = %d %s", st, body)
	}
	if u := e.s.userStore.FindUserByEmail("boss@corp.test"); u == nil || !u.IsAdmin {
		t.Fatalf("boss@corp.test should be admin: %+v", u)
	}
}

// Without an allow list, OAuth does not create accounts (unless open
// registration is turned on explicitly).
func TestRedTeam_OAuth_NoOpenRegistrationByDefault(t *testing.T) {
	e := newOAuthTestEnv(t)
	e.cfg.Auth.AccessControl = nil
	e.userInfo = map[string]any{"sub": "20", "email": "stranger@gmail.test", "email_verified": true}
	state, binder := e.authorize(t)
	if st, _ := e.callback(t, state, binder); st != fiber.StatusForbidden {
		t.Fatalf("open sign-up = %d, want 403", st)
	}
	if e.s.userStore.FindUserByEmail("stranger@gmail.test") != nil {
		t.Fatal("account created without an allow list")
	}
	e.cfg.Auth.OAuth.Providers["google"].AllowOpenRegistration = true
	state, binder = e.authorize(t)
	if st, body := e.callback(t, state, binder); st != fiber.StatusSeeOther {
		t.Fatalf("explicit open registration = %d %s", st, body)
	}
}

var (
	testLoggerOnce sync.Once
	testLoggerDir  string
)

// sharedTestLogger initialises the process-wide logger once, in a folder
// that outlives the test (logging.Initialize is a singleton: a t.TempDir()
// would vanish under the next test that reads the log).
func sharedTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	testLoggerOnce.Do(func() { testLoggerDir, _ = os.MkdirTemp("", "eggify-test-log-") })
	logger, err := logging.Initialize(testLoggerDir, true)
	if err != nil || logger == nil {
		t.Skip("logger unavailable")
	}
	return logger
}

// The authorization code never reaches the log.
func TestRedTeam_OAuth_CodeNotLogged(t *testing.T) {
	logger := sharedTestLogger(t)
	e := newOAuthTestEnv(t)
	e.userInfo = map[string]any{"sub": "30", "email": "ann@corp.test", "email_verified": true}
	state, binder := e.authorize(t)
	req := httptest.NewRequest("GET", "/api/auth/oauth/callback/google?code=SUPERSECRETCODE123&state="+url.QueryEscape(state), nil)
	req.Header.Set("Cookie", oauthStateCookie+"="+binder)
	if _, err := e.app.Test(req, 10000); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logger.GetLogFilePath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "SUPERSECRETCODE123") {
		t.Fatal("authorization code was logged")
	}
}

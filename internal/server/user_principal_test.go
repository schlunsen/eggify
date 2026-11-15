package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// Session tokens are stored only as hashes, survive a restart, and the hash
// itself is not a usable token.
func TestSessionTokensStoredHashed(t *testing.T) {
	db := NewTestDB(t)
	us := NewUserStore(t.TempDir())
	us.SetDatabase(db)
	if err := us.Initialize(); err != nil {
		t.Fatal(err)
	}
	if err := us.CreateUser("alice", "password123", true); err != nil {
		t.Fatal(err)
	}
	token, err := us.generateSessionTokenForUser("alice")
	if err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := db.GetDB().QueryRow(`SELECT token FROM user_sessions WHERE username = 'alice'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token || stored != database.HashToken(token) {
		t.Fatalf("user_sessions.token = %q, want the hash of the session token", stored)
	}
	if _, err := us.ValidateSession(token); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if _, err := us.ValidateSession(stored); err == nil {
		t.Fatal("the stored hash must not be accepted as a session token")
	}

	// A restart loads sessions from the database: the user stays signed in.
	restarted := NewUserStore(t.TempDir())
	restarted.SetDatabase(db)
	if err := restarted.Initialize(); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ValidateSession(token); err != nil {
		t.Fatalf("session lost across restart: %v", err)
	}

	if err := restarted.RevokeSession(token); err != nil {
		t.Fatal(err)
	}
	var n int
	db.GetDB().QueryRow(`SELECT COUNT(*) FROM user_sessions`).Scan(&n)
	if n != 0 {
		t.Fatalf("revoke left %d session rows", n)
	}
	if _, err := restarted.ValidateSession(token); err == nil {
		t.Fatal("revoked session still valid")
	}
}

// newPrincipalTestApp mounts the session middleware and a route that reports
// the principal it sees, plus a privileged route.
func newPrincipalTestApp(t *testing.T, requireLogin bool) *fiber.App {
	t.Helper()
	us := NewInMemoryUserStore()
	us.apiKey = "api-key-123"
	us.users["alice"] = &User{Username: "alice", IsAdmin: true}
	PutTestSession(us, "sess-ok", &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour)})
	PutTestSession(us, "sess-mfa", &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour), MFARequired: true})

	app := fiber.New()
	app.Use(NewSessionAuthMiddleware(us, true, requireLogin).Handler())
	who := func(c *fiber.Ctx) error {
		p := principal.From(c)
		return c.JSON(fiber.Map{"kind": string(p.Kind), "username": p.Username})
	}
	app.Get("/api/whoami", who)
	app.Get("/agent/ws", who) // a WebSocket upgrade path outside /api/
	app.Post("/api/privileged", principal.RequireUser("Doing the thing"), func(c *fiber.Ctx) error { return c.SendString("done") })
	return app
}

func whoami(t *testing.T, app *fiber.App, path string, mut func(*http.Request)) (int, map[string]string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if mut != nil {
		mut(req)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	body, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &out)
	return resp.StatusCode, out
}

func withCookie(tok string) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "session_token", Value: tok}) }
}

func withBearer(tok string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
}

func TestSessionMiddlewareRecordsPrincipal(t *testing.T) {
	app := newPrincipalTestApp(t, false)

	if code, p := whoami(t, app, "/api/whoami", withCookie("sess-ok")); code != 200 || p["kind"] != "user" || p["username"] != "alice" {
		t.Errorf("session cookie: %d %v, want user alice", code, p)
	}
	if code, p := whoami(t, app, "/api/whoami", withBearer("sess-ok")); code != 200 || p["kind"] != "user" {
		t.Errorf("session bearer (iOS): %d %v, want user", code, p)
	}
	if code, p := whoami(t, app, "/api/whoami", withBearer("api-key-123")); code != 200 || p["kind"] != "api_key" {
		t.Errorf("API key: %d %v, want api_key", code, p)
	}
	// The dashboard sends the API key header AND carries the cookie: user wins.
	both := func(r *http.Request) { withCookie("sess-ok")(r); withBearer("api-key-123")(r) }
	if _, p := whoami(t, app, "/api/whoami", both); p["kind"] != "user" {
		t.Errorf("cookie + API key: %v, want user", p)
	}

	// The /agent/ws upgrade passes the middleware without auth when
	// require_login is off; the cookie must still identify the user so the
	// socket can do privileged things.
	if _, p := whoami(t, app, "/agent/ws", withCookie("sess-ok")); p["kind"] != "user" {
		t.Errorf("/agent/ws with cookie: %v, want user", p)
	}
	// MFA-pending sessions are not users yet.
	if _, p := whoami(t, app, "/agent/ws", withCookie("sess-mfa")); p["kind"] == "user" {
		t.Errorf("MFA-pending session counted as a user: %v", p)
	}
}

func TestRequireUserRoute(t *testing.T) {
	prev := principal.Enforced()
	defer principal.SetEnforced(prev)

	post := func(app *fiber.App, mut func(*http.Request)) (int, map[string]string) {
		req := httptest.NewRequest("POST", "/api/privileged", nil)
		mut(req)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		body, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(body, &out)
		return resp.StatusCode, out
	}

	principal.SetEnforced(true)
	app := newPrincipalTestApp(t, false)
	code, body := post(app, withBearer("api-key-123"))
	if code != fiber.StatusForbidden || body["code"] != principal.ErrorCode || body["error"] != "Doing the thing requires a signed-in user" {
		t.Errorf("API key on a privileged route: %d %v, want 403 user_required", code, body)
	}
	if code, _ := post(app, withCookie("sess-ok")); code != 200 {
		t.Errorf("session cookie on a privileged route: %d, want 200", code)
	}

	// User auth disabled: behave as before.
	principal.SetEnforced(false)
	if code, _ := post(app, withBearer("api-key-123")); code != 200 {
		t.Errorf("auth disabled: API key on a privileged route = %d, want 200", code)
	}
}

func TestAuthStatusReportsPrivilegedActionProtection(t *testing.T) {
	prev := principal.Enforced()
	defer principal.SetEnforced(prev)

	for _, enforced := range []bool{true, false} {
		principal.SetEnforced(enforced)
		s := &Server{config: (&ConfigManager{}).getDefaultConfig(), userStore: NewInMemoryUserStore()}
		s.config.Auth.UserAuthEnabled = enforced
		s.userStore.users["alice"] = &User{Username: "alice"}
		app := fiber.New()
		app.Get("/api/auth/status", s.handleAuthStatus)
		resp, err := app.Test(httptest.NewRequest("GET", "/api/auth/status", nil))
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		if body["privileged_actions_protected"] != enforced {
			t.Errorf("enforced=%v: privileged_actions_protected = %v", enforced, body["privileged_actions_protected"])
		}
	}
}

func TestConfigurePrivilegedActionPolicy(t *testing.T) {
	prev := principal.Enforced()
	defer principal.SetEnforced(prev)

	(&Server{quiet: true}).configurePrivilegedActionPolicy()
	if principal.Enforced() {
		t.Fatal("without user auth privileged actions must not require a user")
	}
	us := NewInMemoryUserStore()
	(&Server{quiet: true, sessionAuthMiddleware: NewSessionAuthMiddleware(us, true, false)}).configurePrivilegedActionPolicy()
	if !principal.Enforced() {
		t.Fatal("with user auth privileged actions must require a user")
	}
}

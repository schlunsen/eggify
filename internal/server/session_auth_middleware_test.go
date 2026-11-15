package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func newAuthTestApp(t *testing.T, requireLogin bool) *fiber.App {
	t.Helper()
	us := NewInMemoryUserStore()
	us.apiKey = "api-key-123"
	us.users["alice"] = &User{Username: "alice", IsAdmin: true}
	PutTestSession(us, "sess-ok", &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour)})

	app := fiber.New()
	app.Use(NewSessionAuthMiddleware(us, true, requireLogin).Handler())
	ok := func(c *fiber.Ctx) error { return c.SendString("ok") }
	for _, p := range []string{
		"/", "/agents", "/_nuxt/app.js",
		"/api/health", "/api/version", "/api/auth/status",
		"/api/providers", "/api/agent/sessions/x/messages", "/api/history/shell",
		"/api/projects/p/files/read", "/api/agent/sessions/x/terminal/ws",
	} {
		app.Get(p, ok)
	}
	return app
}

func doGet(t *testing.T, app *fiber.App, path string, mut func(*http.Request)) int {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if mut != nil {
		mut(req)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode
}

// With require_login=false (the default) API reads used to pass without any
// credentials, exposing transcripts, file contents, keys and history.
func TestSessionAuth_APIReadsRequireAuthEvenWithoutRequireLogin(t *testing.T) {
	for _, requireLogin := range []bool{false, true} {
		app := newAuthTestApp(t, requireLogin)

		for _, p := range []string{"/api/providers", "/api/agent/sessions/x/messages", "/api/history/shell", "/api/projects/p/files/read"} {
			if got := doGet(t, app, p, nil); got != fiber.StatusUnauthorized {
				t.Errorf("requireLogin=%v GET %s without auth = %d, want 401", requireLogin, p, got)
			}
			if got := doGet(t, app, p, func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "session_token", Value: "sess-ok"}) }); got != 200 {
				t.Errorf("requireLogin=%v GET %s with session cookie = %d, want 200", requireLogin, p, got)
			}
			if got := doGet(t, app, p, func(r *http.Request) { r.Header.Set("Authorization", "Bearer api-key-123") }); got != 200 {
				t.Errorf("requireLogin=%v GET %s with API key = %d, want 200", requireLogin, p, got)
			}
			// ?token= is only honoured on WebSocket upgrades
			if got := doGet(t, app, p+"?token=api-key-123", nil); got != fiber.StatusUnauthorized {
				t.Errorf("requireLogin=%v GET %s?token= (not an upgrade) = %d, want 401", requireLogin, p, got)
			}
		}

		for _, p := range []string{"/api/health", "/api/version", "/api/auth/status", "/_nuxt/app.js", "/"} {
			if got := doGet(t, app, p, nil); got != 200 {
				t.Errorf("requireLogin=%v public GET %s = %d, want 200", requireLogin, p, got)
			}
		}
	}
}

func TestSessionAuth_SPARoutesOpenOnlyWithoutRequireLogin(t *testing.T) {
	if got := doGet(t, newAuthTestApp(t, false), "/agents", nil); got != 200 {
		t.Errorf("require_login=false: SPA route = %d, want 200", got)
	}
	if got := doGet(t, newAuthTestApp(t, true), "/agents", nil); got == 200 {
		t.Errorf("require_login=true: SPA route without session should not be served")
	}
}

func TestSessionAuth_WebSocketUpgradeAcceptsQueryToken(t *testing.T) {
	app := newAuthTestApp(t, false)
	upgrade := func(r *http.Request) {
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
	}
	if got := doGet(t, app, "/api/agent/sessions/x/terminal/ws?token=api-key-123", upgrade); got != 200 {
		t.Errorf("WS upgrade with API key in ?token= = %d, want 200", got)
	}
	if got := doGet(t, app, "/api/agent/sessions/x/terminal/ws?token=nope", upgrade); got != fiber.StatusUnauthorized {
		t.Errorf("WS upgrade with bad ?token= = %d, want 401", got)
	}
}

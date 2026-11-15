package server

// Prompt-injection red-team suite, attacks 2 (terminal), 3 and 11 (CSP).
// See CLAUDE.md → "Prompt-injection protections"; the catalogue is listed in
// internal/server/agents/redteam_test.go.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/principal"
	"github.com/schlunsen/eggify/internal/server/routes"
)

// rtTerminalUpgrade is the terminal WebSocket upgrade the dashboard makes.
func rtTerminalUpgrade(token string) *http.Request {
	req := httptest.NewRequest("GET", "/api/agent/sessions/s1/terminal/ws?token="+token, nil)
	req.Host = "localhost:3333"
	req.Header.Set("Origin", "https://localhost:3333")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	return req
}

func rtTerminalApp(s *Server) *fiber.App {
	app := fiber.New()
	routes.RegisterTerminal(app.Group("/api"), app, nil, nil, nil, s.validateWebSocketToken)
	return app
}

// Attack 2: the agent opens the terminal WebSocket (an interactive shell outside
// any permission check) with the API key it read from disk.
func TestRedTeam_Terminal_APIKeyCannotOpenTheShell(t *testing.T) {
	us := NewInMemoryUserStore()
	us.apiKey = "the-api-key"
	us.users["alice"] = &User{Username: "alice"}
	PutTestSession(us, "sess-ok", &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour)})
	withUsers := &Server{userStore: us, sessionAuthMiddleware: NewSessionAuthMiddleware(us, true, false)}

	resp, err := rtTerminalApp(withUsers).Test(rtTerminalUpgrade("the-api-key"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("API key on the terminal WebSocket = %d, want 401", resp.StatusCode)
	}
	// Control: a signed-in user's session gets past the auth check.
	resp, err = rtTerminalApp(withUsers).Test(rtTerminalUpgrade("sess-ok"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode == fiber.StatusUnauthorized || resp.StatusCode == fiber.StatusForbidden {
		t.Fatalf("signed-in user refused: %d", resp.StatusCode)
	}

	// Documented degradation: with user auth disabled the API key opens it.
	apiKeyOnly := &Server{authMiddleware: NewAuthMiddleware("the-api-key", true)}
	resp, err = rtTerminalApp(apiKeyOnly).Test(rtTerminalUpgrade("the-api-key"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode == fiber.StatusUnauthorized {
		t.Fatalf("user auth disabled: API key refused (%d)", resp.StatusCode)
	}
}

// Attack 3: the agent reads user_sessions.token from eggify.db and replays it
// as the session cookie or a bearer token to become the signed-in user.
func TestRedTeam_TokenReplay_StoredSessionHashIsUseless(t *testing.T) {
	prev := principal.Enforced()
	t.Cleanup(func() { principal.SetEnforced(prev) })
	principal.SetEnforced(true)

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
	if stored == token {
		t.Fatal("the session token is stored in clear text")
	}

	app := fiber.New()
	app.Use(NewSessionAuthMiddleware(us, true, false).Handler())
	app.Post("/api/privileged", principal.RequireUser("Doing the thing"), func(c *fiber.Ctx) error { return c.SendString("done") })
	post := func(mut func(*http.Request)) int {
		req := httptest.NewRequest("POST", "/api/privileged", nil)
		mut(req)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}

	for name, mut := range map[string]func(*http.Request){
		"cookie": func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "session_token", Value: stored}) },
		"bearer": func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+stored) },
	} {
		if code := post(mut); code == 200 {
			t.Errorf("replaying the stored hash as a %s acted as the user", name)
		}
	}
	if _, err := us.ValidateSession(stored); err == nil {
		t.Error("the stored hash validates as a session")
	}
	// Control: the real token (which only the browser holds) works.
	if code := post(func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "session_token", Value: token}) }); code != 200 {
		t.Errorf("real session cookie = %d, want 200", code)
	}
}

// Attack 11 (server side): an XSS payload that slipped past the sanitizer
// runs an inline script, or the dashboard is framed for clickjacking.
func TestRedTeam_CSP_BlocksInlineScriptAndFraming(t *testing.T) {
	t.Setenv("EGGIFY_CSP", "")
	p, err := newContentSecurityPolicy(fstest.MapFS{"index.html": {Data: []byte(`<script>boot()</script>`)}}, cspModeFromEnv())
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { p.apply(c); return c.Next() })
	app.Get("/", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/html")
		return c.SendString("<html></html>")
	})
	resp, err := app.Test(httptest.NewRequest("GET", "http://localhost:3333/", nil))
	if err != nil {
		t.Fatal(err)
	}
	csp := resp.Header.Get(fiber.HeaderContentSecurityPolicy)
	if csp == "" {
		t.Fatal("no Content-Security-Policy header by default")
	}
	directives := map[string]string{}
	for _, d := range strings.Split(csp, ";") {
		d = strings.TrimSpace(d)
		if name, rest, ok := strings.Cut(d, " "); ok {
			directives[name] = rest
		}
	}
	if s := directives["script-src"]; s == "" || strings.Contains(s, "'unsafe-inline'") || strings.Contains(s, "'unsafe-eval'") || strings.Contains(s, "*") {
		t.Errorf("script-src = %q: must not allow inline scripts, eval or any host", s)
	}
	if directives["frame-ancestors"] != "'none'" {
		t.Errorf("frame-ancestors = %q, want 'none'", directives["frame-ancestors"])
	}
	if directives["object-src"] != "'none'" {
		t.Errorf("object-src = %q, want 'none'", directives["object-src"])
	}
}

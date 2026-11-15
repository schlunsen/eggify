package server

// Red-team: API paths in another case (or with dot segments) must not skip the
// sign-in check. Routing used to be case-insensitive, so GET /API/agent/sessions
// reached the /api/agent/sessions handler, while the auth middleware, which
// compared the raw path against "/api/", treated it as an SPA page and let it
// through without credentials (require_login off, the default).

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

const rtSecretBody = "TRANSCRIPT-SECRET"

func rtPathCaseUserStore() *UserStore {
	us := NewInMemoryUserStore()
	us.apiKey = "api-key-123"
	us.users["alice"] = &User{Username: "alice", IsAdmin: true}
	PutTestSession(us, "sess-ok", &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour)})
	return us
}

// rtPathCaseApps returns the session middleware (user auth on, require_login
// off) mounted on two apps: one with Fiber's default case-insensitive routing,
// so the middleware alone is tested, and one built by NewServerWithOptions,
// the app the server and the desktop app really use.
func rtPathCaseApps(t *testing.T) map[string]*fiber.App {
	t.Helper()
	apps := map[string]*fiber.App{
		"middleware only (case-insensitive routing)": fiber.New(),
		"real server config":                         NewServerWithOptions(t.TempDir(), 0, true, false).app,
	}
	for _, app := range apps {
		app.Use(NewSessionAuthMiddleware(rtPathCaseUserStore(), true, false).Handler())
		secret := func(c *fiber.Ctx) error { return c.SendString(rtSecretBody) }
		app.Get("/api/agent/sessions", secret)
		app.Get("/api/files/*", secret)
		app.Get("/api/health", func(c *fiber.Ctx) error { return c.SendString("healthy") })
		app.Post("/api/auth/setup", func(c *fiber.Ctx) error { return c.SendString("setup ran") })
	}
	return apps
}

func rtDo(t *testing.T, app *fiber.App, method, target string, mut func(*http.Request)) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if mut != nil {
		mut(req)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestRedTeam_APIPathCase_UppercaseAPIPathNeedsAuth(t *testing.T) {
	for name, app := range rtPathCaseApps(t) {
		for _, target := range []string{
			"/API/agent/sessions",
			"/Api/Agent/Sessions",
			"/aPi/agent/sessions",
			"/API/agent/sessions?x=1",
			"/API/agent/sessions/",
			"/API/files/notes.md",
		} {
			code, body := rtDo(t, app, "GET", target, nil)
			if strings.Contains(body, rtSecretBody) {
				t.Errorf("%s: GET %s without credentials leaked data (%d)", name, target, code)
			}
			if code != fiber.StatusUnauthorized && code != fiber.StatusNotFound {
				t.Errorf("%s: GET %s without credentials = %d, want 401 or 404", name, target, code)
			}
		}

		// Control: the lowercase path still works with credentials and is
		// refused without them.
		if code, _ := rtDo(t, app, "GET", "/api/agent/sessions", nil); code != fiber.StatusUnauthorized {
			t.Errorf("%s: GET /api/agent/sessions without credentials = %d, want 401", name, code)
		}
		code, body := rtDo(t, app, "GET", "/api/agent/sessions", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "session_token", Value: "sess-ok"})
		})
		if code != 200 || body != rtSecretBody {
			t.Errorf("%s: signed-in GET /api/agent/sessions = %d %q, want 200", name, code, body)
		}
	}
}

// The real server routes case-sensitively, so /API/... no longer reaches the
// /api handler even with credentials.
func TestRedTeam_APIPathCase_RealServerRoutesCaseSensitively(t *testing.T) {
	app := rtPathCaseApps(t)["real server config"]
	code, body := rtDo(t, app, "GET", "/API/agent/sessions", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "session_token", Value: "sess-ok"})
	})
	if code != fiber.StatusNotFound || strings.Contains(body, rtSecretBody) {
		t.Errorf("signed-in GET /API/agent/sessions on the real config = %d %q, want 404", code, body)
	}
}

// Exempt paths stay public in their own spelling; other spellings either reach
// the same public handler (case-insensitive routing) or nothing at all.
func TestRedTeam_APIPathCase_HealthExemption(t *testing.T) {
	for name, app := range rtPathCaseApps(t) {
		if code, body := rtDo(t, app, "GET", "/api/health", nil); code != 200 || body != "healthy" {
			t.Errorf("%s: GET /api/health = %d %q, want 200", name, code, body)
		}
		code, body := rtDo(t, app, "GET", "/API/HEALTH", nil)
		switch name {
		case "real server config":
			if code != fiber.StatusNotFound {
				t.Errorf("%s: GET /API/HEALTH = %d %q, want 404", name, code, body)
			}
		default:
			if code != 200 || body != "healthy" {
				t.Errorf("%s: GET /API/HEALTH = %d %q, want 200 (public health handler)", name, code, body)
			}
		}
	}
}

// /API/auth/setup must hit the "users already exist" check instead of
// slipping past it as a non-API path.
func TestRedTeam_APIPathCase_SetupInUpperCaseIsRefused(t *testing.T) {
	for name, app := range rtPathCaseApps(t) {
		for _, target := range []string{"/api/auth/setup", "/API/auth/setup", "/Api/Auth/Setup", "/api/auth/setup/"} {
			code, body := rtDo(t, app, "POST", target, nil)
			if code != fiber.StatusForbidden || !strings.Contains(body, "Users already exist") {
				t.Errorf("%s: POST %s with users present = %d %q, want 403 users exist", name, target, code, body)
			}
		}
	}
}

// Cleaning the path must never widen an exemption: Fiber routes on the raw
// path, so /api/files/../../sw.js reaches the /api/files/* handler even though
// it cleans to the public /sw.js.
func TestRedTeam_APIPathCase_DotSegmentsNeverExempt(t *testing.T) {
	for name, app := range rtPathCaseApps(t) {
		for _, target := range []string{
			"/api/files/../../sw.js",
			"/api/files/../../api/health",
			"/API/files/../../workbox-x.js",
			"/api/files//x",
		} {
			code, body := rtDo(t, app, "GET", target, nil)
			if strings.Contains(body, rtSecretBody) {
				t.Errorf("%s: GET %s without credentials leaked data (%d)", name, target, code)
			}
			if code == 200 {
				t.Errorf("%s: GET %s without credentials = 200", name, target)
			}
		}
	}
}

func TestAuthPath(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		p     string
		clean bool
	}{
		{"/", "/", true},
		{"/API/Agent/Sessions", "/api/agent/sessions", true},
		{"/api/", "/api", true},
		{"/api/x///", "/api/x", true},
		{"/api/files/../../sw.js", "/sw.js", false},
		{"//api/x", "/api/x", false},
		{"/a/./b", "/a/b", false},
		{"", "/", false},
	} {
		p, clean := authPath(tc.raw)
		if p != tc.p || clean != tc.clean {
			t.Errorf("authPath(%q) = %q, %v; want %q, %v", tc.raw, p, clean, tc.p, tc.clean)
		}
	}
	if !isAPIPath("/api") || !isAPIPath("/api/x") || isAPIPath("/apix") || isAPIPath("/") {
		t.Error("isAPIPath")
	}
}

package server

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

type fakeLicenseGate struct{ err error }

func (f *fakeLicenseGate) Gate() error { return f.err }

func (f *fakeLicenseGate) RelayCredentials() (string, ed25519.PrivateKey) { return "", nil }

var errTestUnlicensed = errors.New("Activate Eggify with your serial number")

// licenseGateApp mounts the gate in front of catch-all handlers.
func licenseGateApp(gate licenseService) *fiber.App {
	s := &Server{license: gate}
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(s.licenseGate)
	app.All("/*", func(c *fiber.Ctx) error { return c.SendString("ok") })
	return app
}

func licenseReq(t *testing.T, app *fiber.App, method, path string, upgrade bool) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if upgrade {
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func TestLicenseGate_UnlicensedRefusesAPIAndSockets(t *testing.T) {
	app := licenseGateApp(&fakeLicenseGate{err: errTestUnlicensed})

	for _, rc := range []struct {
		method, path string
		upgrade      bool
	}{
		{"GET", "/api/agent/sessions", false},
		{"POST", "/api/agent/sessions/x/model", false},
		{"GET", "/API/agent/sessions", false},            // case games
		{"GET", "/api/license/../agent/sessions", false}, // dot segments
		{"GET", "/api//license", false},                  // not clean: no exemption
		{"GET", "/api/providers", false},
		{"GET", "/agent/ws", true},
		{"GET", "/ws", true},
		{"GET", "/api/agent/sessions/x/terminal/ws", true},
		{"GET", "/api/license", true}, // an upgrade is never exempt
	} {
		code, body := licenseReq(t, app, rc.method, rc.path, rc.upgrade)
		if code != fiber.StatusPaymentRequired || body["code"] != "license_required" ||
			body["error"] != "Activate Eggify with your serial number" {
			t.Errorf("%s %s (upgrade=%v) = %d %v, want 402 license_required", rc.method, rc.path, rc.upgrade, code, body)
		}
	}

	// What the activation page and sign-in need keeps working.
	for _, rc := range []struct{ method, path string }{
		{"GET", "/api/license"},
		{"POST", "/api/license/activate"},
		{"POST", "/api/license/check"},
		{"GET", "/api/auth/status"},
		{"POST", "/api/auth/login"},
		{"POST", "/api/auth/setup"},
		{"GET", "/api/health"},
		{"GET", "/api/version"},
		{"GET", "/api/config/api-key"},
		{"GET", "/"},
		{"GET", "/activate"},
		{"GET", "/_nuxt/entry.js"},
		{"GET", "/favicon.svg"},
	} {
		if code, _ := licenseReq(t, app, rc.method, rc.path, false); code != 200 {
			t.Errorf("%s %s = %d, want 200 while unlicensed", rc.method, rc.path, code)
		}
	}
}

func TestLicenseGate_LicensedOrNotEnforcedPasses(t *testing.T) {
	for name, gate := range map[string]licenseService{
		"licensed / not enforced": &fakeLicenseGate{},
		"no manager":              nil,
	} {
		app := licenseGateApp(gate)
		for _, p := range []string{"/api/agent/sessions", "/api/providers"} {
			if code, _ := licenseReq(t, app, "GET", p, false); code != 200 {
				t.Errorf("%s: GET %s = %d, want 200", name, p, code)
			}
		}
		if code, _ := licenseReq(t, app, "GET", "/agent/ws", true); code == fiber.StatusPaymentRequired {
			t.Errorf("%s: /agent/ws upgrade refused", name)
		}
	}
}

// Before the first account exists the activation page (shown before /setup)
// reads the status and activates without a session; nothing else opens up.
// Once an account exists, the license routes need auth like everything else.
func TestSessionAuth_LicenseBeforeSetup(t *testing.T) {
	us := NewInMemoryUserStore()
	us.apiKey = "api-key-123"
	app := fiber.New()
	app.Use(NewSessionAuthMiddleware(us, true, false).Handler())
	app.All("/*", func(c *fiber.Ctx) error { return c.SendString("ok") })

	do := func(method, path string) int {
		resp, err := app.Test(httptest.NewRequest(method, path, strings.NewReader("{}")))
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	if got := do("GET", "/api/license"); got != 200 {
		t.Errorf("no account: GET /api/license = %d, want 200", got)
	}
	if got := do("POST", "/api/license/activate"); got != 200 {
		t.Errorf("no account: POST /api/license/activate = %d, want 200", got)
	}
	for _, rc := range [][2]string{{"POST", "/api/license/deactivate"}, {"POST", "/api/license"}, {"GET", "/api/agent/sessions"}} {
		if got := do(rc[0], rc[1]); got != fiber.StatusUnauthorized {
			t.Errorf("no account: %s %s = %d, want 401", rc[0], rc[1], got)
		}
	}

	us.users["alice"] = &User{Username: "alice", IsAdmin: true}
	PutTestSession(us, "sess-ok", &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour)})
	for _, rc := range [][2]string{{"GET", "/api/license"}, {"POST", "/api/license/activate"}} {
		if got := do(rc[0], rc[1]); got != fiber.StatusUnauthorized {
			t.Errorf("account exists: %s %s without a session = %d, want 401", rc[0], rc[1], got)
		}
	}
	req := httptest.NewRequest("GET", "/api/license", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: "sess-ok"})
	if resp, _ := app.Test(req); resp.StatusCode != 200 {
		t.Errorf("account exists: GET /api/license with a session = %d", resp.StatusCode)
	}
}

package routes

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/schlunsen/eggify/internal/capability"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// privilegedRouteApp registers the real route tables with nil handlers. A
// test middleware sets the principal from X-Test-Principal ("user" = alice,
// an admin; "nonadmin" = bob, a signed-in non-admin, once withAdmins is set;
// or "apikey"). A request that gets past the user gate reaches a nil handler and
// panics; recover turns that into a 500, so "not 403" means "allowed".
func privilegedRouteApp(t *testing.T) *fiber.App {
	t.Helper()
	t.Setenv("EGGIFY_SUBDOMAIN", "test")
	t.Setenv("EGGIFY_APPS_BASE_DOMAIN", "example.com")
	app := fiber.New()
	app.Use(recover.New())
	app.Use(func(c *fiber.Ctx) error {
		switch c.Get("X-Test-Principal") {
		case "user":
			principal.Set(c, principal.Principal{Kind: principal.User, Username: "alice"})
		case "nonadmin":
			principal.Set(c, principal.Principal{Kind: principal.User, Username: "bob"})
		case "apikey":
			principal.Set(c, principal.Principal{Kind: principal.APIKey})
		}
		return c.Next()
	})
	api := app.Group("/api")

	ok := func(c *fiber.Ctx) error { return c.SendString("ok") }
	ah := &AuthHandlers{}
	v := reflect.ValueOf(ah).Elem()
	for i := 0; i < v.NumField(); i++ {
		v.Field(i).Set(reflect.ValueOf(fiber.Handler(ok)))
	}

	RegisterConfig(api, nil)
	RegisterProvider(api, nil)
	RegisterSettings(api, nil)
	RegisterMCPConfig(api, nil)
	RegisterHooks(api, nil)
	RegisterConnector(api, nil)
	RegisterJustfile(api, nil)
	RegisterApps(api, nil)
	RegisterAgent(api, app, nil, nil, nil)
	RegisterTerminal(api, app, nil, nil, nil, func(string) bool { return false })
	RegisterAuth(api, AuthConfig{UserAuthEnabled: true}, ah, nil)
	RegisterReview(api, nil)
	RegisterAgentRuntimes(api, nil)
	RegisterTunnel(api, nil)
	RegisterProject(api, nil)
	RegisterGit(api, nil)
	RegisterLicense(api, nil)
	RegisterRefero(api, nil)
	RegisterPacks(api, nil)
	// A source that panics once reached stands in for the desktop app.
	RegisterMachines(api, handlers.NewMachinesHandler(func() handlers.PairedMachines { panic("reached the paired machines") }))
	return app
}

type routeCase struct{ method, path, body string }

// privilegedRoutes are the REST routes that need a signed-in user.
var privilegedRoutes = []routeCase{
	{"GET", "/api/config/api-key", ""},
	{"POST", "/api/config/permissions", "{}"},
	{"PUT", "/api/config/permissions", "{}"},
	{"DELETE", "/api/config/permissions", "{}"},
	{"POST", "/api/providers", "{}"},
	{"POST", "/api/providers/anthropic/set-default", ""},
	{"DELETE", "/api/providers/current", ""},
	{"DELETE", "/api/providers/anthropic", ""},
	{"PUT", "/api/settings/default_permission_mode", `{"value":"yolo"}`},
	{"PUT", "/api/agent/question-policy", `{"mode":"auto"}`},
	{"PUT", "/api/agent/sessions/00000000-0000-0000-0000-000000000001/question-policy", `{"policy":{"mode":"auto"}}`},
	{"DELETE", "/api/settings/default_permission_mode", ""},
	{"POST", "/api/projects/p/mcp-config/servers", "{}"},
	{"DELETE", "/api/projects/p/mcp-config/servers/x", ""},
	{"POST", "/api/hooks", "{}"},
	{"DELETE", "/api/hooks", "{}"},
	{"POST", "/api/connectors/github/connect", "{}"},
	{"DELETE", "/api/connectors/github/disconnect", ""},
	{"POST", "/api/projects/p/just/run", "{}"},
	{"POST", "/api/apps", "{}"},
	{"DELETE", "/api/apps/x", ""},
	{"PATCH", "/api/agent/sessions/00000000-0000-0000-0000-000000000001/options", "{}"},
	{"PUT", "/api/agent/sessions/00000000-0000-0000-0000-000000000001/skills", "{}"},
	{"POST", "/api/agent/sessions/00000000-0000-0000-0000-000000000001/connectors/github/enable", ""},
	{"POST", "/api/agent/sessions/00000000-0000-0000-0000-000000000001/connectors/github/disable", ""},
	{"POST", "/api/agent/handover/accept", "{}"},
	{"POST", "/api/agent/sessions/00000000-0000-0000-0000-000000000001/terminal/start", ""},
	{"POST", "/api/auth/users", "{}"},
	{"GET", "/api/auth/users", ""},
	{"PATCH", "/api/auth/users/bob", "{}"},
	{"DELETE", "/api/auth/users/bob", ""},
	// Workspace trust (#84) and the memory/skill review queue (#85).
	{"POST", "/api/agent/sessions/00000000-0000-0000-0000-000000000001/workspace-trust", `{"fingerprint":"x"}`},
	{"DELETE", "/api/agent/sessions/00000000-0000-0000-0000-000000000001/workspace-trust", ""},
	{"POST", "/api/review/memories/m1/approve", ""},
	{"POST", "/api/review/memories/m1/reject", ""},
	{"POST", "/api/review/skills/s1/approve", ""},
	{"POST", "/api/review/skills/s1/reject", ""},
	// Agent CLI onboarding: the CLIs' own login flows.
	{"POST", "/api/agent/runtimes/claude/login", "{}"},
	{"POST", "/api/agent/runtimes/codex/login", `{"mode":"device"}`},
	{"GET", "/api/agent/runtimes/claude/login", ""},
	{"POST", "/api/agent/runtimes/claude/login/cancel", ""},
	{"POST", "/api/agent/runtimes/claude/login/code", `{"code":"x"}`},
	// Remote access (ngrok tunnel).
	{"PUT", "/api/tunnel/settings", `{"autostart":true}`},
	{"POST", "/api/tunnel/start", ""},
	{"POST", "/api/tunnel/stop", ""},
	{"DELETE", "/api/tunnel/token", ""},
	{"PUT", "/api/tunnel/api-key", `{"api_key":"attacker_key_1234567"}`},
	{"DELETE", "/api/tunnel/api-key", ""},
	{"GET", "/api/tunnel/sessions", ""},
	{"POST", "/api/tunnel/sessions/ts_abc123/stop", ""},
	// New-project flow: folder creation, cloning and folder browsing.
	{"GET", "/api/projects/setup/defaults", ""},
	{"GET", "/api/projects/setup/inspect?path=~", ""},
	{"GET", "/api/projects/setup/complete?prefix=~/", ""},
	{"POST", "/api/projects/setup", `{"mode":"empty"}`},
	{"GET", "/api/projects/setup/jobs/j1", ""},
	{"DELETE", "/api/projects/setup/jobs/j1", ""},
	{"POST", "/api/git/clone", "{}"},
	// Creating a project picks a folder the file browser serves.
	{"POST", "/api/projects", `{"name":"x","path":"/"}`},
	// Writes a file into a folder; installs run setup commands and add
	// approved skills and hooks; default skills join every new session.
	{"POST", "/api/refero/import", `{"style_id":"s","project_dir":"/tmp"}`},
	{"POST", "/api/packs/gstack/install", ""},
	{"POST", "/api/packs/gstack/enable-project", "{}"},
	{"POST", "/api/packs/gstack/disable-project", "{}"},
	{"DELETE", "/api/packs/gstack", ""},
	{"PUT", "/api/projects/p/default-skills", "{}"},
	{"POST", "/api/projects/p/default-skills/toggle", "{}"},
	// A project area's context prompt goes into the system prompt of every
	// session started in it, and its folder is that session's working
	// directory.
	{"POST", "/api/projects/p/areas", `{"name":"x","relative_path":"../../..","context_prompt":"x"}`},
	{"PUT", "/api/projects/p/areas/a1", `{"name":"x","context_prompt":"x"}`},
	{"DELETE", "/api/projects/p/areas/a1", ""},
	// Feature files are written into (and deleted from) the user's project.
	{"PUT", "/api/projects/p/features/x", `{"content":"x"}`},
	{"DELETE", "/api/projects/p/features/x", ""},
	// Licensing: an agent must not swap the serial or give the seat away.
	{"POST", "/api/license/activate", `{"serial":"EGG-7H3K9-Q2M4X-8R6T1-V5N0T"}`},
	{"POST", "/api/license/deactivate", ""},
	// The other Macs this Mac's desktop app is paired with (ADR 014): a pair
	// code signs its holder in to the other Mac.
	{"GET", "/api/machines/paired", ""},
	{"POST", "/api/machines/paired/abc/pair-code", `{"device_name":"iPhone"}`},
}

// signedInRoutes need a signed-in user but not an admin: one's own devices
// and pairing codes, continuing one's own sessions after a restart.
var signedInRoutes = []routeCase{
	{"POST", "/api/agent/resume-after-restart", ""},
	{"DELETE", "/api/agent/resume-after-restart", ""},
	{"POST", "/api/agent/sessions/00000000-0000-0000-0000-000000000001/limit-wait/resume", ""},
	{"DELETE", "/api/agent/sessions/00000000-0000-0000-0000-000000000001/limit-wait", ""},
	{"POST", "/api/auth/pairing", "{}"},
	{"GET", "/api/auth/pairing/p1/status", ""},
	{"DELETE", "/api/auth/pairing/p1", ""},
	{"GET", "/api/auth/sessions", ""},
	{"DELETE", "/api/auth/sessions/s1", ""},
}

// withAdmins makes alice the only admin (principal.SetAdminResolver), as the
// server does with several accounts.
func withAdmins(t *testing.T) {
	t.Helper()
	principal.SetAdminResolver(func(u string) bool { return u == "alice" })
	t.Cleanup(func() { principal.SetAdminResolver(nil) })
}

// unprivilegedRoutes keep working with the API key (reads, hook/desktop-app traffic).
var unprivilegedRoutes = []routeCase{
	{"GET", "/api/config/cwd", ""},
	{"GET", "/api/providers", ""},
	{"PUT", "/api/settings/hotkeys", `{"value":"{}"}`}, // Tauri desktop app
	{"PUT", "/api/settings/diff_display_location", `{"value":"chat"}`},
	{"PUT", "/api/settings/keyboard_shortcuts", `{"value":"{}"}`},     // Settings → Keyboard shortcuts
	{"PUT", "/api/settings/resume_after_update", `{"value":"false"}`}, // a preference, not a security setting
	{"GET", "/api/hooks/executions/stats", ""},                        // Tauri desktop app
	{"POST", "/api/hooks/executions", "{}"},                           // hook scripts
	{"GET", "/api/agent/sessions", ""},
	{"GET", "/api/agent/question-policy", ""},
	{"GET", "/api/agent/subagents", ""},
	{"GET", "/api/agent/sessions/00000000-0000-0000-0000-000000000001/workers", ""}, // read-only, like /subagents
	{"GET", "/api/agent/resume-after-restart", ""},
	{"GET", "/api/agent/usage-limit", ""},
	{"PUT", "/api/settings/resume_after_usage_limit", `{"value":"false"}`}, // a preference
	{"POST", "/api/agent/sessions/00000000-0000-0000-0000-000000000001/handover", "{}"},
	{"GET", "/api/agent/runtimes", ""}, // status only; the account hint is user-only
	{"GET", "/api/tunnel/status", ""},
	{"GET", "/api/projects", ""},
	{"GET", "/api/projects/overview", ""},
	{"GET", "/api/projects/p/areas", ""},
	{"GET", "/api/projects/p/areas/a1", ""},
	{"GET", "/api/projects/p/features", ""}, // slash-command list; never leaves the project
	{"GET", "/api/projects/p/features/x", ""},
	{"GET", "/api/license", ""},
	{"POST", "/api/license/check", ""}, // a renewal: harmless
}

func doRoute(t *testing.T, app *fiber.App, rc routeCase, who string) (int, map[string]any) {
	t.Helper()
	var body io.Reader
	if rc.body != "" {
		body = strings.NewReader(rc.body)
	}
	req := httptest.NewRequest(rc.method, rc.path, body)
	req.Header.Set("Content-Type", "application/json")
	if who != "" {
		req.Header.Set("X-Test-Principal", who)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func TestPrivilegedRESTRoutesRequireSignedInUser(t *testing.T) {
	prev := principal.Enforced()
	defer principal.SetEnforced(prev)
	app := privilegedRouteApp(t)

	principal.SetEnforced(true)
	for _, rc := range privilegedRoutes {
		code, body := doRoute(t, app, rc, "apikey")
		if code != fiber.StatusForbidden || body["code"] != principal.ErrorCode {
			t.Errorf("API key: %s %s = %d %v, want 403 user_required", rc.method, rc.path, code, body)
		}
		if code, _ := doRoute(t, app, rc, "user"); code == fiber.StatusForbidden || code == fiber.StatusNotFound {
			t.Errorf("signed-in user: %s %s = %d, want it past the user gate", rc.method, rc.path, code)
		}
	}
	for _, rc := range signedInRoutes {
		code, body := doRoute(t, app, rc, "apikey")
		if code != fiber.StatusForbidden || body["code"] != principal.ErrorCode {
			t.Errorf("API key: %s %s = %d %v, want 403 user_required", rc.method, rc.path, code, body)
		}
		if code, _ := doRoute(t, app, rc, "user"); code == fiber.StatusForbidden || code == fiber.StatusNotFound {
			t.Errorf("signed-in user: %s %s = %d, want it past the user gate", rc.method, rc.path, code)
		}
	}
	for _, rc := range unprivilegedRoutes {
		if code, _ := doRoute(t, app, rc, "apikey"); code == fiber.StatusForbidden || code == fiber.StatusNotFound {
			t.Errorf("API key: %s %s = %d, should keep working", rc.method, rc.path, code)
		}
	}

	// User auth disabled: everything behaves as before.
	principal.SetEnforced(false)
	for _, rc := range append(append([]routeCase{}, privilegedRoutes...), signedInRoutes...) {
		if code, _ := doRoute(t, app, rc, "apikey"); code == fiber.StatusForbidden || code == fiber.StatusNotFound {
			t.Errorf("auth disabled: %s %s = %d, want it allowed", rc.method, rc.path, code)
		}
	}
}

// With several accounts, a signed-in non-admin gets 403 admin_required on
// every privileged route and keeps the user-level ones.
func TestPrivilegedRESTRoutesRequireAdmin(t *testing.T) {
	prev := principal.Enforced()
	defer principal.SetEnforced(prev)
	principal.SetEnforced(true)
	withAdmins(t)
	app := privilegedRouteApp(t)

	for _, rc := range privilegedRoutes {
		code, body := doRoute(t, app, rc, "nonadmin")
		if code != fiber.StatusForbidden || body["code"] != principal.AdminErrorCode {
			t.Errorf("non-admin: %s %s = %d %v, want 403 admin_required", rc.method, rc.path, code, body)
		}
		if code, _ := doRoute(t, app, rc, "user"); code == fiber.StatusForbidden || code == fiber.StatusNotFound {
			t.Errorf("admin: %s %s = %d, want it past the admin gate", rc.method, rc.path, code)
		}
	}
	for _, rc := range signedInRoutes {
		if code, _ := doRoute(t, app, rc, "nonadmin"); code == fiber.StatusForbidden || code == fiber.StatusNotFound {
			t.Errorf("non-admin: %s %s = %d, a user-level route should stay open", rc.method, rc.path, code)
		}
	}
}

func TestIsSecuritySetting(t *testing.T) {
	for key, want := range map[string]bool{
		"default_permission_mode": true,
		"yolo_default":            true,
		"mcp_servers":             true,
		"hotkeys":                 false,
		"diff_display_location":   false,
		"auto_tag_sessions":       false,
		"whisper_model":           false,
		"default_clone_path":      false,
		"tunnel_authtoken":        true,
		"ngrok_domain":            true,
		"remote_access_autostart": true,
	} {
		if got := IsSecuritySetting(key); got != want {
			t.Errorf("IsSecuritySetting(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestTerminalWebSocketPrefersSessionCookie(t *testing.T) {
	// The dashboard puts the API key in ?token= and carries the session cookie.
	// With user auth on only the session is valid; it must be found.
	validate := func(tok string) bool { return tok == "session" }
	app := fiber.New()
	registerTerminalWebSocketRoute(app, nil, nil, validate, nil)

	req := terminalUpgradeRequest("?token=api-key")
	if resp, err := app.Test(req); err != nil || resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("API key only: %v %v, want 401", resp.StatusCode, err)
	}

	req = terminalUpgradeRequest("?token=api-key")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: "session"})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode == fiber.StatusUnauthorized || resp.StatusCode == fiber.StatusForbidden {
		t.Fatalf("session cookie rejected: %d", resp.StatusCode)
	}
}

func TestInternalAppsRequireSessionCapability(t *testing.T) {
	// app.Test connections do not come from 127.0.0.1, so mount the
	// capability gate (the part after the localhost guard) directly.
	app := fiber.New()
	ok := func(c *fiber.Ctx) error { return c.SendString("ok") }
	app.Get("/internal/apps", requireAppsCapability, ok)
	app.Post("/internal/apps", requireAppsCapability, ok)
	app.Delete("/internal/apps/:name", requireAppsCapability, ok)

	sid := "00000000-0000-0000-0000-0000000000aa"
	call := func(method, path string, hdr map[string]string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(`{"name":"x","port":4000}`))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	good := map[string]string{"Authorization": "Bearer " + capability.Token(capability.Apps, sid), capability.HeaderSessionID: sid}
	otherSession := map[string]string{"Authorization": "Bearer " + capability.Token(capability.Apps, sid), capability.HeaderSessionID: "someone-else"}
	otherPurpose := map[string]string{"Authorization": "Bearer " + capability.Token("n0", sid), capability.HeaderSessionID: sid}

	for _, rc := range []struct{ method, path string }{{"GET", "/internal/apps"}, {"POST", "/internal/apps"}, {"DELETE", "/internal/apps/x"}} {
		if code := call(rc.method, rc.path, nil); code != fiber.StatusUnauthorized {
			t.Errorf("%s %s without capability = %d, want 401", rc.method, rc.path, code)
		}
		if code := call(rc.method, rc.path, otherSession); code != fiber.StatusUnauthorized {
			t.Errorf("%s %s with another session's id = %d, want 401", rc.method, rc.path, code)
		}
		if code := call(rc.method, rc.path, otherPurpose); code != fiber.StatusUnauthorized {
			t.Errorf("%s %s with another purpose's capability = %d, want 401", rc.method, rc.path, code)
		}
		if code := call(rc.method, rc.path, good); code != 200 {
			t.Errorf("%s %s with a valid capability = %d, want it past the gate", rc.method, rc.path, code)
		}
	}
}

package handlers

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/agents"
	"github.com/schlunsen/eggify/internal/server/principal"
)

func newSandboxTestApp(t *testing.T, userAuth bool) (*fiber.App, *database.Repository) {
	t.Helper()
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.ResetInstance)
	repo := database.NewRepository(db)

	prev := principal.Enforced()
	principal.SetEnforced(userAuth)
	t.Cleanup(func() { principal.SetEnforced(prev) })

	h := NewSandboxHandler(repo)
	settings := NewSettingsHandler(repo, nil)
	app := fiber.New()
	// Stand-in for the auth middleware: "X-Test-Auth: user" signs in a user,
	// anything else is an API-key request.
	app.Use(func(c *fiber.Ctx) error {
		if c.Get("X-Test-Auth") == "user" {
			principal.Set(c, principal.Principal{Kind: principal.User, Username: "alice"})
		} else {
			principal.Set(c, principal.Principal{Kind: principal.APIKey})
		}
		return c.Next()
	})
	// Same wiring as routes.RegisterSandbox.
	app.Get("/agent/sandbox", h.HandleGet)
	app.Put("/agent/sandbox", principal.RequireUser(SandboxAction), h.HandleUpdate)
	app.Put("/settings/:key", settings.HandleUpdateSetting)
	app.Delete("/settings/:key", settings.HandleDeleteSetting)
	return app, repo
}

func doSandboxJSON(t *testing.T, app *fiber.App, method, path, auth, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("X-Test-Auth", auth)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func TestSandboxSettings_GetDefaults(t *testing.T) {
	app, _ := newSandboxTestApp(t, true)
	code, body := doSandboxJSON(t, app, "GET", "/agent/sandbox", "", "")
	if code != 200 {
		t.Fatalf("GET: %d %v", code, body)
	}
	if body["default_enabled"] != true {
		t.Errorf("default_enabled = %v (on until the user saves a choice)", body["default_enabled"])
	}
	if body["can_authorize_codex_network"] != false {
		t.Error("API-key caller cannot authorize network access")
	}
	if got := body["allowed_domains"].([]any); len(got) != len(agents.DefaultSandboxAllowedDomains) {
		t.Errorf("allowed_domains = %v", got)
	}
}

func TestSandboxSettings_NetworkCapabilityRequiresAdminEvenWithoutAuth(t *testing.T) {
	app, _ := newSandboxTestApp(t, false)
	code, body := doSandboxJSON(t, app, "GET", "/agent/sandbox", "", "")
	if code != 200 || body["can_authorize_codex_network"] != false {
		t.Fatalf("auth disabled: %d %v", code, body)
	}
	code, body = doSandboxJSON(t, app, "GET", "/agent/sandbox", "user", "")
	if code != 200 || body["can_authorize_codex_network"] != true {
		t.Fatalf("signed-in owner: %d %v", code, body)
	}
	principal.SetAdminResolver(func(string) bool { return false })
	t.Cleanup(func() { principal.SetAdminResolver(nil) })
	code, body = doSandboxJSON(t, app, "GET", "/agent/sandbox", "user", "")
	if code != 200 || body["can_authorize_codex_network"] != false {
		t.Fatalf("non-admin: %d %v", code, body)
	}
}

func TestSessionOptions_NetworkRequiresNewSession(t *testing.T) {
	db := database.NewTestDB(t)
	sm, err := agents.NewSessionManager(&agents.Config{Model: "sonnet"}, db.GetDB())
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := sm.CreateSession(id, agents.SessionOptions{}); err != nil {
		t.Fatal(err)
	}
	h := &AgentSessionHandler{agentHandler: &agents.AgentHandler{SessionManager: sm}, db: db}
	app := fiber.New()
	app.Put("/sessions/:id/options", h.HandleUpdateSessionOptions)
	path := "/sessions/" + id.String() + "/options"

	code, result := doSandboxJSON(t, app, "PUT", path, "", `{"codex_network_access":true}`)
	if code != 400 || !strings.Contains(result["error"].(string), "start a new session") {
		t.Fatalf("granting network: %d %v", code, result)
	}
	// Sending the session's options back unchanged is not a change.
	code, result = doSandboxJSON(t, app, "PUT", path, "", `{"codex_network_access":false,"auto_handoff_after_messages":40}`)
	if code != 200 {
		t.Fatalf("echoing the current value: %d %v", code, result)
	}
	s, err := sm.GetSession(id)
	if err != nil || s.Options.CodexNetworkAccess {
		t.Fatalf("network grant changed: %v %v", err, s)
	}
}

func TestSandboxSettings_UpdateRequiresUser(t *testing.T) {
	app, repo := newSandboxTestApp(t, true)

	// The API key alone (what an agent could read) is not enough.
	code, refusal := doSandboxJSON(t, app, "PUT", "/agent/sandbox", "", `{"allowed_domains":["evil.test"]}`)
	if code != fiber.StatusForbidden || refusal["code"] != principal.ErrorCode {
		t.Fatalf("API-key update: got %d %v, want 403 %s", code, refusal, principal.ErrorCode)
	}
	if prefs := agents.LoadSandboxPreferences(repo); strings.Contains(strings.Join(prefs.AllowedDomains, ","), "evil.test") {
		t.Fatal("refused update must not be stored")
	}

	code, body := doSandboxJSON(t, app, "PUT", "/agent/sandbox", "user", `{"default_enabled":true,"allowed_domains":["Example.com","pypi.org"]}`)
	if code != 200 {
		t.Fatalf("user update: %d %v", code, body)
	}
	prefs := agents.LoadSandboxPreferences(repo)
	if !prefs.DefaultEnabled || strings.Join(prefs.AllowedDomains, ",") != "example.com,pypi.org" {
		t.Errorf("stored prefs = %+v", prefs)
	}

	// Partial update keeps the other field.
	code, _ = doSandboxJSON(t, app, "PUT", "/agent/sandbox", "user", `{"default_enabled":false}`)
	if code != 200 {
		t.Fatalf("partial update: %d", code)
	}
	prefs = agents.LoadSandboxPreferences(repo)
	if prefs.DefaultEnabled || len(prefs.AllowedDomains) != 2 {
		t.Errorf("partial update prefs = %+v", prefs)
	}

	code, body = doSandboxJSON(t, app, "PUT", "/agent/sandbox", "user", `{"allowed_domains":["https://x.test/path"]}`)
	if code != 400 || !strings.Contains(body["error"].(string), "invalid domain") {
		t.Errorf("invalid domain: %d %v", code, body)
	}
}

func TestSandboxSettings_UserAuthDisabledAllowsAPIKey(t *testing.T) {
	app, repo := newSandboxTestApp(t, false)
	code, body := doSandboxJSON(t, app, "PUT", "/agent/sandbox", "", `{"default_enabled":true}`)
	if code != 200 {
		t.Fatalf("with user auth disabled the API key keeps working: %d %v", code, body)
	}
	if !agents.LoadSandboxPreferences(repo).DefaultEnabled {
		t.Error("update not stored")
	}
}

func TestSettings_ReservedSandboxKeys(t *testing.T) {
	app, repo := newSandboxTestApp(t, true)
	for _, method := range []string{"PUT", "DELETE"} {
		code, _ := doSandboxJSON(t, app, method, "/settings/"+agents.SandboxSettingDomainsKey, "user", `{"value":"[\"evil.test\"]"}`)
		if code != fiber.StatusForbidden {
			t.Errorf("%s generic settings on a sandbox key: got %d, want 403", method, code)
		}
	}
	if prefs := agents.LoadSandboxPreferences(repo); strings.Contains(strings.Join(prefs.AllowedDomains, ","), "evil.test") {
		t.Fatal("generic settings endpoint must not change the allowlist")
	}
}

func TestSandboxSettings_GetListsGrantsWithoutSecrets(t *testing.T) {
	app, _ := newSandboxTestApp(t, true)
	code, body := doSandboxJSON(t, app, "GET", "/agent/sandbox", "", "")
	if code != 200 {
		t.Fatalf("GET: %d %v", code, body)
	}
	grants, ok := body["grants"].([]any)
	if !ok || len(grants) != 5 {
		t.Fatalf("grants = %v", body["grants"])
	}
	seen := map[string]bool{}
	for _, g := range grants {
		m := g.(map[string]any)
		id, _ := m["id"].(string)
		seen[id] = true
		for _, key := range []string{"title", "description", "risk"} {
			if s, _ := m[key].(string); s == "" {
				t.Errorf("grant %s has no %s", id, key)
			}
		}
		if avail, _ := m["available"].(bool); !avail {
			if reason, _ := m["reason"].(string); reason == "" {
				t.Errorf("grant %s is unavailable without a reason", id)
			}
		}
	}
	for _, id := range []string{"ssh", "github", "cloud", "npm", "localhost"} {
		if !seen[id] {
			t.Errorf("grant %s missing", id)
		}
	}
	// Read-only: the update route still only takes the settings it knew.
	code, _ = doSandboxJSON(t, app, "PUT", "/agent/sandbox", "", `{"default_enabled": true}`)
	if code != 403 {
		t.Errorf("an API key must still not change sandbox settings: %d", code)
	}
}

func TestSandboxSettings_DefaultDomainsIncludeCrates(t *testing.T) {
	app, _ := newSandboxTestApp(t, true)
	_, body := doSandboxJSON(t, app, "GET", "/agent/sandbox", "", "")
	domains, _ := body["default_allowed_domains"].([]any)
	have := map[string]bool{}
	for _, d := range domains {
		have[d.(string)] = true
	}
	for _, d := range []string{"raw.githubusercontent.com", "crates.io", "static.crates.io", "index.crates.io"} {
		if !have[d] {
			t.Errorf("default allowlist lacks %s", d)
		}
	}
}

func TestSandboxSettings_DomainsOnlyKeepsTheDefaultImplicit(t *testing.T) {
	app, repo := newSandboxTestApp(t, true)
	_, body := doSandboxJSON(t, app, "GET", "/agent/sandbox", "", "")
	if body["default_implicit"] != true {
		t.Fatalf("fresh install: default_implicit = %v", body["default_implicit"])
	}
	code, body := doSandboxJSON(t, app, "PUT", "/agent/sandbox", "user", `{"allowed_domains":["example.com"]}`)
	if code != 200 || body["default_implicit"] != true {
		t.Fatalf("domains only: %d %v", code, body)
	}
	if prefs := agents.LoadSandboxPreferences(repo); !prefs.DefaultIsImplicit || !prefs.DefaultEnabled {
		t.Errorf("domains only made the default explicit: %+v", prefs)
	}
	_, body = doSandboxJSON(t, app, "PUT", "/agent/sandbox", "user", `{"default_enabled":true}`)
	if body["default_implicit"] != false {
		t.Errorf("an explicit choice: default_implicit = %v", body["default_implicit"])
	}
}

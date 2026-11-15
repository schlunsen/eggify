package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/agentruntime"
	"github.com/schlunsen/eggify/internal/server/principal"
)

type cannedRunner map[string]string

func (r cannedRunner) Run(_ context.Context, path string, args ...string) ([]byte, int, error) {
	out, ok := r[path+" "+strings.Join(args, " ")]
	if !ok {
		return nil, 1, nil
	}
	return []byte(out), 0, nil
}

func runtimesTestApp(t *testing.T, userAuth bool, installed bool) *fiber.App {
	t.Helper()
	prev := principal.Enforced()
	principal.SetEnforced(userAuth)
	t.Cleanup(func() { principal.SetEnforced(prev) })

	d := &agentruntime.Detector{
		Runner: cannedRunner{
			"/bin/claude --version":          "2.1.280 (Claude Code)",
			"/bin/claude auth status --json": `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","email":"alice@example.com","subscriptionType":"pro"}`,
		},
		LookPath: func(name string) (string, error) {
			if installed && name == "claude" {
				return "/bin/claude", nil
			}
			return "", exec.ErrNotFound
		},
		IsFile:  func(string) bool { return false },
		Home:    func() string { return "/nowhere" },
		Getenv:  func(string) string { return "" },
		Timeout: time.Second,
		TTL:     time.Minute,
	}
	h := NewAgentRuntimesHandlerWith(d, agentruntime.NewLoginManager(d))
	t.Cleanup(h.Close)

	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		if c.Get("X-Test-Auth") == "user" {
			principal.Set(c, principal.Principal{Kind: principal.User, Username: "alice"})
		} else {
			principal.Set(c, principal.Principal{Kind: principal.APIKey})
		}
		return c.Next()
	})
	api := app.Group("/api")
	// Same wiring as routes.RegisterAgentRuntimes (routes imports handlers).
	user := principal.RequireUser(AgentLoginAction)
	api.Get("/agent/runtimes", h.HandleList)
	api.Post("/agent/runtimes/:runtime/login", user, h.HandleLoginStart)
	api.Get("/agent/runtimes/:runtime/login", user, h.HandleLoginStatus)
	api.Post("/agent/runtimes/:runtime/login/cancel", user, h.HandleLoginCancel)
	api.Post("/agent/runtimes/:runtime/login/code", user, h.HandleLoginCode)
	return app
}

func runtimesDo(t *testing.T, app *fiber.App, method, path, body, who string) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Auth", who)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func TestAgentRuntimesListRedactsAccountForAPIKey(t *testing.T) {
	app := runtimesTestApp(t, true, true)

	code, body := runtimesDo(t, app, "GET", "/api/agent/runtimes", "", "user")
	if code != 200 || body["any_ready"] != true {
		t.Fatalf("user: %d %v", code, body)
	}
	rts := body["runtimes"].([]any)
	claude := rts[0].(map[string]any)
	auth := claude["auth"].(map[string]any)
	if claude["id"] != "claude" || auth["state"] != "logged_in" || auth["account"] != "al•••@example.com" || auth["plan"] != "pro" {
		t.Fatalf("claude: %v", claude)
	}
	if rts[1].(map[string]any)["installed"] != false {
		t.Fatalf("codex should be missing: %v", rts[1])
	}

	code, body = runtimesDo(t, app, "GET", "/api/agent/runtimes", "", "apikey")
	if code != 200 {
		t.Fatalf("API key read: %d", code)
	}
	auth = body["runtimes"].([]any)[0].(map[string]any)["auth"].(map[string]any)
	if _, has := auth["account"]; has || auth["state"] != "logged_in" {
		t.Fatalf("API key must not see the account hint: %v", auth)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "alice@") {
		t.Fatal("e-mail leaked")
	}
}

func TestAgentRuntimesLoginNeedsSignedInUser(t *testing.T) {
	app := runtimesTestApp(t, true, false)
	for _, rc := range [][3]string{
		{"POST", "/api/agent/runtimes/claude/login", `{}`},
		{"GET", "/api/agent/runtimes/claude/login", ""},
		{"POST", "/api/agent/runtimes/codex/login/cancel", ""},
		{"POST", "/api/agent/runtimes/claude/login/code", `{"code":"x"}`},
	} {
		code, body := runtimesDo(t, app, rc[0], rc[1], rc[2], "apikey")
		if code != fiber.StatusForbidden || body["code"] != principal.ErrorCode {
			t.Errorf("API key %s %s = %d %v, want 403 user_required", rc[0], rc[1], code, body)
		}
	}

	// A signed-in user gets through to the handler.
	if code, body := runtimesDo(t, app, "POST", "/api/agent/runtimes/claude/login", `{}`, "user"); code != fiber.StatusConflict {
		t.Fatalf("not installed: %d %v", code, body)
	}
	if code, _ := runtimesDo(t, app, "POST", "/api/agent/runtimes/claude/login", `{"mode":"device"}`, "user"); code != fiber.StatusBadRequest {
		t.Fatalf("bad mode: %d", code)
	}
	if code, _ := runtimesDo(t, app, "POST", "/api/agent/runtimes/gemini/login", `{}`, "user"); code != fiber.StatusNotFound {
		t.Fatalf("unknown runtime: %d", code)
	}
	if code, _ := runtimesDo(t, app, "POST", "/api/agent/runtimes/claude/login/code", `{"code":"x"}`, "user"); code != fiber.StatusConflict {
		t.Fatalf("code without a flow: %d", code)
	}
	if code, _ := runtimesDo(t, app, "GET", "/api/agent/runtimes/codex/login", "", "user"); code != fiber.StatusNotFound {
		t.Fatalf("no flow: %d", code)
	}
}

func TestAgentRuntimesUserAuthDisabled(t *testing.T) {
	app := runtimesTestApp(t, false, false)
	// Documented degradation: without user auth the API key is allowed.
	if code, _ := runtimesDo(t, app, "POST", "/api/agent/runtimes/claude/login", `{}`, "apikey"); code == fiber.StatusForbidden {
		t.Fatal("auth disabled: login should not be refused with 403")
	}
}

package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/schlunsen/eggify/internal/license"
	"github.com/schlunsen/eggify/internal/server/principal"
)

type fakeLicense struct {
	state       string
	activations int
	err         error
}

func (f *fakeLicense) Status() license.Status { return license.Status{State: f.state, Required: true} }
func (f *fakeLicense) Activate(ctx context.Context, serial, machine string) (license.Status, error) {
	if f.err != nil {
		return f.Status(), f.err
	}
	f.activations++
	f.state = license.StateActive
	return f.Status(), nil
}
func (f *fakeLicense) Deactivate(context.Context) (license.Status, error) {
	f.state = license.StateUnlicensed
	return f.Status(), nil
}
func (f *fakeLicense) CheckNow(context.Context) (license.Status, error) { return f.Status(), nil }

func licenseApp(h *LicenseHandler, who string) *fiber.App {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		switch who {
		case "user":
			principal.Set(c, principal.Principal{Kind: principal.User, Username: "alice"})
		case "apikey":
			principal.Set(c, principal.Principal{Kind: principal.APIKey})
		}
		return c.Next()
	})
	app.Post("/api/license/activate", h.ActivateGate(), h.HandleActivate)
	return app
}

func postActivate(t *testing.T, app *fiber.App) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/license/activate", strings.NewReader(`{"serial":"EGG-7H3K9-Q2M4X-8R6T1-V5N0T"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

// Before the first account exists the activation page comes first: the API
// key (or nobody) may activate an install that isn't active. Once an account
// exists, only a signed-in user may.
func TestLicenseActivateGate(t *testing.T) {
	prev := principal.Enforced()
	t.Cleanup(func() { principal.SetEnforced(prev) })
	principal.SetEnforced(true)

	users := false
	f := &fakeLicense{state: license.StateUnlicensed}
	h := NewLicenseHandler(f, func() bool { return users })

	if code, _ := postActivate(t, licenseApp(h, "")); code != 200 {
		t.Fatalf("fresh install, no account: %d, want 200", code)
	}
	// Already active before setup: no re-activation without a user.
	if code, body := postActivate(t, licenseApp(h, "apikey")); code != 403 || body["code"] != principal.ErrorCode {
		t.Fatalf("active, no account, API key: %d %v, want 403", code, body)
	}

	users = true
	f.state = license.StateUnlicensed
	if code, body := postActivate(t, licenseApp(h, "apikey")); code != 403 || body["code"] != principal.ErrorCode {
		t.Fatalf("account exists, API key: %d %v, want 403 user_required", code, body)
	}
	if code, _ := postActivate(t, licenseApp(h, "user")); code != 200 {
		t.Fatalf("account exists, signed-in user: %d, want 200", code)
	}

	// User auth off: the API key works, as everywhere.
	principal.SetEnforced(false)
	f.state = license.StateUnlicensed
	if code, _ := postActivate(t, licenseApp(h, "apikey")); code != 200 {
		t.Fatalf("user auth off, API key: %d", code)
	}
}

func TestLicenseActivateErrors(t *testing.T) {
	prev := principal.Enforced()
	t.Cleanup(func() { principal.SetEnforced(prev) })
	principal.SetEnforced(true)
	f := &fakeLicense{state: license.StateUnlicensed, err: &license.APIError{Status: 409, Code: license.CodeSeatsFull,
		Activations: []license.Activation{{Machine: "Studio"}, {Machine: "Laptop"}, {Machine: "Work"}}}}
	app := licenseApp(NewLicenseHandler(f, func() bool { return true }), "user")
	code, body := postActivate(t, app)
	if code != 409 || body["code"] != license.CodeSeatsFull {
		t.Fatalf("seats_full: %d %v", code, body)
	}
	if acts, _ := body["activations"].([]any); len(acts) != 3 || !strings.Contains(body["error"].(string), "Studio") {
		t.Fatalf("seats_full body: %v", body)
	}

	f.err = &license.APIError{Code: license.CodeOffline}
	if code, body := postActivate(t, app); code != 502 || body["code"] != license.CodeOffline {
		t.Fatalf("offline: %d %v", code, body)
	}
	f.err = &license.APIError{Status: 401, Code: license.CodeStaleTimestamp}
	if code, body := postActivate(t, app); code != 400 || !strings.Contains(body["error"].(string), "clock") {
		t.Fatalf("stale timestamp: %d %v", code, body)
	}
}

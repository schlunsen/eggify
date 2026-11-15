package routes

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// fakeMachines is the desktop app's paired-Macs list.
type fakeMachines struct{ pairCodes atomic.Int32 }

func (f *fakeMachines) List() []handlers.PairedMachine {
	return []handlers.PairedMachine{{ID: "abc", Name: "Studio", BaseURL: "https://abc.r.example.com"}}
}

func (f *fakeMachines) PairCode(_ context.Context, id, _ string) (string, time.Time, error) {
	if id != "abc" {
		return "", time.Time{}, handlers.ErrPairedMachineNotFound
	}
	f.pairCodes.Add(1)
	return "eggify://pair?u=https%3A%2F%2Fabc.r.example.com&c=code&p=pin", time.Now().Add(5 * time.Minute), nil
}

func machinesApp(t *testing.T, fm *fakeMachines) *fiber.App {
	t.Helper()
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		switch c.Get("X-Test-Principal") {
		case "user":
			principal.Set(c, principal.Principal{Kind: principal.User, Username: "alice"})
		case "apikey":
			principal.Set(c, principal.Principal{Kind: principal.APIKey})
		}
		return c.Next()
	})
	RegisterMachines(app.Group("/api"), handlers.NewMachinesHandler(func() handlers.PairedMachines { return fm }))
	return app
}

// Attack: an agent on this Mac reads the API key and asks the desktop app
// for a pair code on the owner's other Mac, to sign itself in there. The
// API key gets 403 user_required and the other Mac is never asked.
func TestRedTeam_REST_AgentCannotMintPairCodeOnAnotherMac(t *testing.T) {
	prev := principal.Enforced()
	t.Cleanup(func() { principal.SetEnforced(prev) })
	principal.SetEnforced(true)
	fm := &fakeMachines{}
	app := machinesApp(t, fm)

	for _, rc := range []routeCase{
		{"GET", "/api/machines/paired", ""},
		{"POST", "/api/machines/paired/abc/pair-code", `{"device_name":"agent"}`},
	} {
		for _, who := range []string{"apikey", ""} {
			code, body := doRoute(t, app, rc, who)
			if code != fiber.StatusForbidden || body["code"] != principal.ErrorCode {
				t.Errorf("%s %s as %q = %d %v, want 403 user_required", rc.method, rc.path, who, code, body)
			}
		}
	}
	if n := fm.pairCodes.Load(); n != 0 {
		t.Fatalf("the other Mac was asked for %d pair codes", n)
	}

	// The signed-in owner (the iPhone, Bearer session) gets the list and a code.
	code, body := doRoute(t, app, routeCase{"GET", "/api/machines/paired", ""}, "user")
	if code != fiber.StatusOK {
		t.Fatalf("list as the user = %d %v", code, body)
	}
	if list, _ := body["machines"].([]any); len(list) != 1 {
		t.Fatalf("list = %v", body)
	} else if m := list[0].(map[string]any); m["token"] != nil || m["name"] != "Studio" {
		t.Fatalf("machine entry = %v", m)
	}
	code, body = doRoute(t, app, routeCase{"POST", "/api/machines/paired/abc/pair-code", `{"device_name":"iPhone"}`}, "user")
	if code != fiber.StatusOK || body["pair_url"] == "" || body["name"] != "Studio" {
		t.Fatalf("pair code as the user = %d %v", code, body)
	}
	if code, _ := doRoute(t, app, routeCase{"POST", "/api/machines/paired/nope/pair-code", "{}"}, "user"); code != fiber.StatusNotFound {
		t.Fatalf("unknown machine = %d, want 404", code)
	}
}

// Pair codes are rate limited per user (10 per 15 min).
func TestMachines_PairCodeRateLimited(t *testing.T) {
	prev := principal.Enforced()
	t.Cleanup(func() { principal.SetEnforced(prev) })
	principal.SetEnforced(true)
	fm := &fakeMachines{}
	app := machinesApp(t, fm)
	rc := routeCase{"POST", "/api/machines/paired/abc/pair-code", "{}"}
	for i := 0; i < 10; i++ {
		if code, body := doRoute(t, app, rc, "user"); code != fiber.StatusOK {
			t.Fatalf("request %d = %d %v", i+1, code, body)
		}
	}
	if code, _ := doRoute(t, app, rc, "user"); code != fiber.StatusTooManyRequests {
		t.Fatalf("11th pair code = %d, want 429", code)
	}
	if n := fm.pairCodes.Load(); n != 10 {
		t.Fatalf("pair codes = %d, want 10", n)
	}
}

// Without the desktop app (the CLI server) the list is empty and pair
// codes answer 404.
func TestMachines_WithoutDesktopApp(t *testing.T) {
	prev := principal.Enforced()
	t.Cleanup(func() { principal.SetEnforced(prev) })
	principal.SetEnforced(true)
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		principal.Set(c, principal.Principal{Kind: principal.User, Username: "alice"})
		return c.Next()
	})
	RegisterMachines(app.Group("/api"), nil)
	code, body := doRoute(t, app, routeCase{"GET", "/api/machines/paired", ""}, "")
	if list, _ := body["machines"].([]any); code != fiber.StatusOK || list == nil || len(list) != 0 || body["available"] != false {
		t.Fatalf("list = %d %v", code, body)
	}
	if code, _ := doRoute(t, app, routeCase{"POST", "/api/machines/paired/abc/pair-code", "{}"}, ""); code != fiber.StatusNotFound {
		t.Fatalf("pair code = %d, want 404", code)
	}
}

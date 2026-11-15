package principal

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestHelpers(t *testing.T) {
	prev := Enforced()
	defer SetEnforced(prev)

	alice, empty := "alice", ""
	SetEnforced(true)
	if !AllowedConnUser(&alice) || AllowedConnUser(&empty) || AllowedConnUser(nil) {
		t.Fatal("AllowedConnUser with enforcement on")
	}
	SetEnforced(false)
	if !AllowedConnUser(nil) {
		t.Fatal("with user auth disabled everything is allowed")
	}

	app := fiber.New()
	app.Get("/u", func(c *fiber.Ctx) error {
		Set(c, Principal{Kind: User, Username: "alice"})
		if name, ok := Username(c); !ok || name != "alice" {
			t.Errorf("Username = %q %v", name, ok)
		}
		return nil
	})
	app.Get("/k", func(c *fiber.Ctx) error {
		c.Locals("authenticated_via_api_key", true)
		if _, ok := Username(c); ok || From(c).Kind != APIKey {
			t.Errorf("API key counted as a user")
		}
		return nil
	})
	for _, p := range []string{"/u", "/k"} {
		if _, err := app.Test(httptest.NewRequest("GET", p, nil)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAdminRole(t *testing.T) {
	prev := Enforced()
	defer SetEnforced(prev)
	defer SetAdminResolver(nil)
	SetEnforced(true)

	alice, bob := "alice", "bob"
	// No resolver: every signed-in user counts as admin (single user).
	if !AllowedConnUser(&bob) {
		t.Fatal("without a resolver a signed-in user is admin")
	}
	SetAdminResolver(func(u string) bool { return u == "alice" })
	if !AllowedConnUser(&alice) || AllowedConnUser(&bob) || !AllowedConnSignedIn(&bob) || AllowedConnSignedIn(nil) {
		t.Fatal("admin vs signed-in")
	}
	if code, _ := Refusal(FromConnUser(&bob), "X"); code != AdminErrorCode {
		t.Fatalf("non-admin refusal code %q", code)
	}
	if code, _ := Refusal(FromConnUser(nil), "X"); code != ErrorCode {
		t.Fatalf("API key refusal code %q", code)
	}

	app := fiber.New()
	setP := func(c *fiber.Ctx) error {
		if u := c.Get("X-User"); u != "" {
			Set(c, Principal{Kind: User, Username: u})
		}
		return c.Next()
	}
	ok := func(c *fiber.Ctx) error { return c.SendString("ok") }
	app.Get("/admin", setP, RequireAdmin("A"), ok)
	app.Get("/user", setP, RequireSignedIn("U"), ok)
	for _, tc := range []struct {
		path, user string
		want       int
	}{
		{"/admin", "alice", 200}, {"/admin", "bob", 403}, {"/admin", "", 403},
		{"/user", "alice", 200}, {"/user", "bob", 200}, {"/user", "", 403},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		if tc.user != "" {
			req.Header.Set("X-User", tc.user)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != tc.want {
			t.Errorf("%s as %q = %d, want %d", tc.path, tc.user, resp.StatusCode, tc.want)
		}
	}
}

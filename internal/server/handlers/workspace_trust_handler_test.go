package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// TestWorkspaceTrustPrincipal: a signed-in user may trust a folder, a request
// that only carries the API key (which agents can read) may not.
func TestWorkspaceTrustPrincipal(t *testing.T) {
	cases := []struct {
		name     string
		enforced bool
		locals   map[string]any
		want     int
	}{
		{"user session", true, map[string]any{"username": "alice"}, 200},
		{"api key only", true, map[string]any{"authenticated_via_api_key": true}, 403},
		{"user auth disabled", false, nil, 200},
		{"user auth disabled, api key", false, map[string]any{"authenticated_via_api_key": true}, 200},
	}
	prev := principal.Enforced()
	defer principal.SetEnforced(prev)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			principal.SetEnforced(tc.enforced)
			app := fiber.New()
			app.Post("/", func(c *fiber.Ctx) error {
				for k, v := range tc.locals {
					c.Locals(k, v)
				}
				if ok, err := principal.Check(c, actionTrustWorkspace); !ok {
					return err
				}
				return c.SendString(workspaceTrustActor(c))
			})
			resp, err := app.Test(httptest.NewRequest("POST", "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

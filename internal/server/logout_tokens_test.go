package server

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// An iPhone can hold a newer Bearer token next to an older session cookie.
// The Bearer token is the one the request acts as, and signing out ends every
// session token the client presented.
func TestSessionTokenCandidates_BearerComesFirst(t *testing.T) {
	app := fiber.New()
	var got []string
	app.Post("/x", func(c *fiber.Ctx) error {
		got = sessionTokenCandidates(c)
		return nil
	})
	r := httptest.NewRequest("POST", "/x", nil)
	r.Header.Set("Authorization", "Bearer NEWER")
	r.Header.Set("Cookie", "session_token=OLDER")
	if _, err := app.Test(r); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "NEWER" || got[1] != "OLDER" {
		t.Fatalf("candidates = %q, want the Bearer token first, then the cookie", got)
	}
}

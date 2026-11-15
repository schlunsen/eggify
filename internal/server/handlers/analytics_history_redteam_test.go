package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// TestRedTeam_History_NonAdminSeesOnlyOwnSessionRows: /api/history/* returned
// every conversation's commands to any signed-in user.
func TestRedTeam_History_NonAdminSeesOnlyOwnSessionRows(t *testing.T) {
	prev := principal.Enforced()
	defer principal.SetEnforced(prev)
	principal.SetEnforced(true)
	principal.SetAdminResolver(func(u string) bool { return u == "alice" })
	defer principal.SetAdminResolver(nil)

	mine, theirs := uuid.New(), uuid.New()
	h := &AnalyticsHandler{}
	h.SetSessionVisibility(func(user string, ids []uuid.UUID) map[uuid.UUID]bool {
		return map[uuid.UUID]bool{mine: user == "bob"}
	})
	app := fiber.New()
	app.Get("/t", func(c *fiber.Ctx) error {
		who := c.Get("X-Who")
		if who == "apikey" {
			principal.Set(c, principal.Principal{Kind: principal.APIKey})
		} else {
			principal.Set(c, principal.Principal{Kind: principal.User, Username: who})
		}
		allow := h.historyFilter(c, []string{mine.String(), theirs.String(), "hook-conversation"})
		if allow == nil {
			return c.SendString("all")
		}
		if allow(mine.String()) && !allow(theirs.String()) && !allow("hook-conversation") {
			return c.SendString("own")
		}
		return c.SendString("wrong")
	})
	for who, want := range map[string]string{"alice": "all", "apikey": "all", "bob": "own"} {
		req := httptest.NewRequest("GET", "/t", nil)
		req.Header.Set("X-Who", who)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 16)
		n, _ := resp.Body.Read(b)
		if got := string(b[:n]); got != want {
			t.Errorf("%s: filter = %q, want %q", who, got, want)
		}
	}
}

// TestRedTeam_History_NonAdminPagesAreFullAfterFiltering: rows were filtered
// after the SQL limit, so a non-admin's page came back short (or empty) when
// other users' rows filled it.
func TestRedTeam_History_NonAdminPagesAreFullAfterFiltering(t *testing.T) {
	prev := principal.Enforced()
	defer principal.SetEnforced(prev)
	principal.SetEnforced(true)
	principal.SetAdminResolver(func(u string) bool { return u == "alice" })
	defer principal.SetAdminResolver(nil)

	mine, theirs := uuid.NewString(), uuid.NewString()
	h := &AnalyticsHandler{}
	h.SetSessionVisibility(func(user string, ids []uuid.UUID) map[uuid.UUID]bool {
		out := map[uuid.UUID]bool{}
		for _, id := range ids {
			out[id] = id.String() == mine
		}
		return out
	})
	// 600 rows, newest first; every 10th belongs to bob.
	rows := make([]string, 600)
	for i := range rows {
		rows[i] = theirs
		if i%10 == 0 {
			rows[i] = mine
		}
	}
	fetch := func(limit, offset int) ([]string, error) {
		if offset >= len(rows) {
			return nil, nil
		}
		return rows[offset:min(offset+limit, len(rows))], nil
	}
	app := fiber.New()
	app.Get("/t", func(c *fiber.Ctx) error {
		principal.Set(c, principal.Principal{Kind: principal.User, Username: c.Get("X-Who")})
		got, err := fetchHistoryPage(h, c, 10, 5, fetch, func(s string) string { return s })
		if err != nil {
			return err
		}
		return c.JSON(len(got))
	})
	for who, want := range map[string]string{"bob": "10", "alice": "10"} {
		req := httptest.NewRequest("GET", "/t", nil)
		req.Header.Set("X-Who", who)
		resp, _ := app.Test(req)
		b := make([]byte, 8)
		n, _ := resp.Body.Read(b)
		if got := string(b[:n]); got != want {
			t.Errorf("%s: page of %s rows, want %s", who, got, want)
		}
	}
}

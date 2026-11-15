package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/agents"
)

// The web dashboard now paints its session list from GET /api/agent/sessions
// first (the WebSocket's list follows), so the REST list must apply the same
// visibility rule as the socket: a signed-in user gets only their sessions.
func TestRedTeam_REST_SessionListShowsOnlyTheCallersSessions(t *testing.T) {
	db := database.NewTestDB(t)
	alice := insertSession(t, db, "alice")
	bob := insertSession(t, db, "bob")
	sm, err := agents.NewSessionManager(&agents.Config{Model: "sonnet"}, db.GetDB())
	if err != nil {
		t.Fatal(err)
	}
	h := &AgentSessionHandler{agentHandler: &agents.AgentHandler{SessionManager: sm, BackgroundAgentMgr: agents.NewBackgroundAgentManager()}, db: db}

	list := func(username string) map[string]bool {
		app := fiber.New()
		if username != "" {
			app.Use(func(c *fiber.Ctx) error { c.Locals("username", username); return c.Next() })
		}
		app.Get("/api/agent/sessions", h.HandleGetAgentSessions)
		resp, err := app.Test(httptest.NewRequest("GET", "/api/agent/sessions?status=all", nil))
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("list as %q: %v %v", username, resp.StatusCode, err)
		}
		var body struct {
			Sessions []struct {
				ID string `json:"id"`
			} `json:"sessions"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		got := map[string]bool{}
		for _, s := range body.Sessions {
			got[s.ID] = true
		}
		return got
	}

	if got := list("alice"); !got[alice.String()] || got[bob.String()] {
		t.Errorf("alice sees %v, want only her own", got)
	}
	if got := list(""); !got[alice.String()] || !got[bob.String()] {
		t.Errorf("API key sees %v, want all", got)
	}
}

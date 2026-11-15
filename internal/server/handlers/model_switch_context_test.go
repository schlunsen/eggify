package handlers

import (
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/server/agents"
)

type switchContextOwnershipStorage struct {
	agents.SessionStorage
	metadata *agents.SessionMetadata
}

func (s switchContextOwnershipStorage) GetSession(uuid.UUID) (*agents.SessionMetadata, error) {
	if s.metadata == nil {
		return nil, fmt.Errorf("not found")
	}
	return s.metadata, nil
}

func TestModelSwitchContextRejectsUnauthorizedReads(t *testing.T) {
	owner := "session-owner"
	for _, tc := range []struct {
		name     string
		metadata *agents.SessionMetadata
		status   int
		body     string
	}{
		{"different owner", &agents.SessionMetadata{OwnerUserID: &owner}, 403, "access denied"},
		{"missing session", nil, 404, "session not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No database is configured: any attempt to read a snapshot after the
			// ownership check would panic rather than returning the expected denial.
			h := &AgentSessionHandler{agentHandler: &agents.AgentHandler{SessionManager: &agents.SessionManager{
				Storage: switchContextOwnershipStorage{metadata: tc.metadata},
			}}}
			app := fiber.New()
			app.Use(func(c *fiber.Ctx) error { c.Locals("username", "different-user"); return c.Next() })
			app.Get("/sessions/:id/switches/:messageID", h.HandleGetModelSwitchContext)
			response, err := app.Test(httptest.NewRequest("GET", "/sessions/"+uuid.NewString()+"/switches/"+uuid.NewString(), nil))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.status || !strings.Contains(string(body), tc.body) {
				t.Fatalf("got %d: %s", response.StatusCode, body)
			}
		})
	}
}

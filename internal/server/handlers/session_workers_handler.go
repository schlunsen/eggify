package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/schlunsen/eggify/internal/server/agents"
)

// HandleGetSessionWorkers returns what the workers a session started have been
// doing: their newest output lines, in the subagent line format. The sessions
// themselves are in the session list.
func (h *AgentSessionHandler) HandleGetSessionWorkers(c *fiber.Ctx) error {
	if h.agentHandler == nil || h.agentHandler.SessionManager == nil {
		return c.Status(503).JSON(fiber.Map{
			"error": "agent handler not initialized",
		})
	}

	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{
			"error": "invalid session ID",
		})
	}

	// SECURITY: worker output can contain file contents and command output
	if !h.AuthorizeSession(c, sessionID) {
		return nil
	}

	workers := h.agentHandler.SessionManager.WorkerActivity(sessionID)
	// A worker has its starter's owner; check each one all the same.
	if username, ok := c.Locals("username").(string); ok && username != "" && len(workers) > 0 {
		ids := make([]uuid.UUID, 0, len(workers))
		for _, w := range workers {
			ids = append(ids, w.SessionID)
		}
		visible := h.visibleSessionIDs(username, ids)
		kept := workers[:0]
		for _, w := range workers {
			if visible[w.SessionID] {
				kept = append(kept, w)
			}
		}
		workers = kept
	}
	if workers == nil {
		workers = []agents.WorkerActivity{}
	}

	return c.JSON(fiber.Map{"workers": workers})
}

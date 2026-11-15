package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/server/agents"
)

// HandleListPendingRequests lists the tool approvals and questions waiting for
// the user in every session the caller may see, oldest first. Read-only (API
// key allowed, like the session list, which already counts them); answers go
// through the WebSocket's permission_response / user_question_response.
//
//	GET /api/agent/pending → {"permissions": [...], "questions": [...]}
func (h *AgentSessionHandler) HandleListPendingRequests(c *fiber.Ctx) error {
	if h.agentHandler == nil || h.agentHandler.SessionManager == nil {
		return c.JSON(fiber.Map{"permissions": []any{}, "questions": []any{}})
	}
	perms, questions := h.agentHandler.SessionManager.PendingRequests(nil)
	if username, ok := c.Locals("username").(string); ok && username != "" && (len(perms) > 0 || len(questions) > 0) {
		ids := make([]uuid.UUID, 0, len(perms)+len(questions))
		for _, p := range perms {
			ids = append(ids, p.SessionID)
		}
		for _, q := range questions {
			ids = append(ids, q.SessionID)
		}
		visible := h.visibleSessionIDs(username, ids)
		keptPerms := perms[:0]
		for _, p := range perms {
			if visible[p.SessionID] {
				keptPerms = append(keptPerms, p)
			}
		}
		keptQuestions := questions[:0]
		for _, q := range questions {
			if visible[q.SessionID] {
				keptQuestions = append(keptQuestions, q)
			}
		}
		perms, questions = keptPerms, keptQuestions
	}
	if perms == nil {
		perms = []agents.PendingPermissionSummary{}
	}
	if questions == nil {
		questions = []agents.PendingQuestionSummary{}
	}
	return c.JSON(fiber.Map{"permissions": perms, "questions": questions})
}

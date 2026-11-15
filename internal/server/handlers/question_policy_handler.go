// Package handlers - question_policy_handler.go serves the question policy:
// what happens when an agent asks a question nobody answers (global default
// and per-session override; see agents/question_policy.go).
package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/agents"
)

// HandleGetQuestionPolicy returns the global question policy.
//
//	GET /api/agent/question-policy → {"policy": {...}, "is_default": bool}
func (h *AgentSessionHandler) HandleGetQuestionPolicy(c *fiber.Ctx) error {
	policy, isDefault := agents.LoadQuestionPolicy(h.agentHandler.SessionManager.GetRepo())
	return c.JSON(fiber.Map{"policy": policy, "is_default": isDefault})
}

// HandleSetQuestionPolicy changes the global question policy. Privileged:
// mount it behind principal.RequireUser(agents.QuestionPolicyAction).
//
//	PUT /api/agent/question-policy {"mode": "auto_after", "timeout_seconds": 120}
func (h *AgentSessionHandler) HandleSetQuestionPolicy(c *fiber.Ctx) error {
	var body agents.QuestionPolicy
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	sm := h.agentHandler.SessionManager
	if _, err := agents.NormalizeQuestionPolicy(body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	saved, err := agents.SaveQuestionPolicy(sm.GetRepo(), body)
	if err != nil {
		logging.Warning("Failed to save the question policy: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to save the question policy"})
	}
	sm.QuestionPolicyDefaultChanged(saved)
	return c.JSON(fiber.Map{"policy": saved, "is_default": false})
}

// HandleSetSessionQuestionPolicy sets or clears a session's own question
// policy. Privileged: mount it behind principal.RequireUser.
//
//	PUT /api/agent/sessions/:id/question-policy {"policy": {...} | null}
//	→ {"session_id", "policy": {...} | null, "effective": {...}}
func (h *AgentSessionHandler) HandleSetSessionQuestionPolicy(c *fiber.Ctx) error {
	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid session ID"})
	}
	sm := h.agentHandler.SessionManager
	if _, err := sm.Storage.GetSession(sessionID); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "session not found: " + sessionID.String()})
	}
	if !h.AuthorizeSession(c, sessionID) {
		return nil
	}
	var body struct {
		Policy *agents.QuestionPolicy `json:"policy"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if body.Policy != nil {
		if _, err := agents.NormalizeQuestionPolicy(*body.Policy); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
	}
	override, effective, err := sm.SetSessionQuestionPolicy(sessionID, body.Policy)
	if err != nil {
		logging.Warning("Failed to set the question policy of session %s: %v", sessionID, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to save the question policy"})
	}
	return c.JSON(fiber.Map{"session_id": sessionID.String(), "policy": override, "effective": effective})
}

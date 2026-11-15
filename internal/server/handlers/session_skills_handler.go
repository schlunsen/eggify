package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// Skills of one session (web Skills popover, iOS Skills sheet).
//
//	GET /api/agent/sessions/:id/skills → agents.SessionSkillsState
//	PUT /api/agent/sessions/:id/skills {"skill_ids": [...]} → the same, after the change
//
// PUT replaces the skills added to the session (other projects' skills; the
// rest apply on their own). It needs a signed-in user (the route says so): an
// agent with the API key must not pull a project's skill, hooks and all, into
// its own session.
func (h *AgentSessionHandler) HandleGetSessionSkills(c *fiber.Ctx) error {
	sessionID, ok := h.sessionForSkills(c)
	if !ok {
		return nil
	}
	state, err := h.agentHandler.SessionSkills(sessionID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(state)
}

func (h *AgentSessionHandler) HandleSetSessionSkills(c *fiber.Ctx) error {
	sessionID, ok := h.sessionForSkills(c)
	if !ok {
		return nil
	}
	var body struct {
		SkillIDs []string `json:"skill_ids"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request body"})
	}
	state, err := h.agentHandler.SetSessionAddedSkills(sessionID, body.SkillIDs)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(state)
}

// sessionForSkills parses and authorizes the session; it answers itself when not ok.
func (h *AgentSessionHandler) sessionForSkills(c *fiber.Ctx) (uuid.UUID, bool) {
	if h.agentHandler == nil || h.agentHandler.SessionManager == nil {
		_ = c.Status(503).JSON(fiber.Map{"error": "agent handler not initialized"})
		return uuid.Nil, false
	}
	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		_ = c.Status(400).JSON(fiber.Map{"error": "invalid session ID"})
		return uuid.Nil, false
	}
	if _, err := h.agentHandler.SessionManager.Storage.GetSession(sessionID); err != nil {
		_ = c.Status(404).JSON(fiber.Map{"error": "session not found"})
		return uuid.Nil, false
	}
	if !h.AuthorizeSession(c, sessionID) {
		return uuid.Nil, false
	}
	return sessionID, true
}

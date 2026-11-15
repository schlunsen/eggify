package handlers

import (
	"database/sql"
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func (h *AgentSessionHandler) HandleGetModelSwitchContext(c *fiber.Ctx) error {
	if h.agentHandler == nil {
		return c.Status(503).JSON(fiber.Map{"error": "agent handler not initialized"})
	}
	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid session ID"})
	}
	if !h.AuthorizeSession(c, sessionID) {
		return nil
	}
	// The shared validator writes denied/not-found responses directly.
	if c.Response().StatusCode() >= 400 {
		return nil
	}
	messageID, err := uuid.Parse(c.Params("messageID"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid message ID"})
	}
	result, err := h.agentHandler.SessionManager.GetModelSwitchContext(sessionID, messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return c.Status(404).JSON(fiber.Map{"error": "model switch not found"})
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to load switch context"})
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(result)
}

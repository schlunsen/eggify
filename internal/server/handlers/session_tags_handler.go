package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/agents"
)

// sessionTagStore returns the storage that keeps session tags, or nil.
func (h *AgentSessionHandler) sessionTagStore() agents.SessionTagStorage {
	if h.agentHandler == nil || h.agentHandler.SessionManager == nil {
		return nil
	}
	store, _ := h.agentHandler.SessionManager.Storage.(agents.SessionTagStorage)
	return store
}

// HandleSetSessionTags replaces a session's own tags.
//
//	PUT /api/agent/sessions/:id/tags {"tags": ["a", "b"]} → {"tags": [...normalised]}
//
// An empty list removes them. Tags are not a security setting, so the API
// key may set them like any other per-session action.
func (h *AgentSessionHandler) HandleSetSessionTags(c *fiber.Ctx) error {
	store := h.sessionTagStore()
	if store == nil {
		return c.Status(503).JSON(fiber.Map{"error": "session tags are not available"})
	}
	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid session ID"})
	}
	if _, err := h.agentHandler.SessionManager.Storage.GetSession(sessionID); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "session not found: " + sessionID.String()})
	}
	if !h.AuthorizeSession(c, sessionID) {
		return nil
	}

	var body struct {
		Tags []string `json:"tags"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request body"})
	}
	if len(body.Tags) > 100 {
		body.Tags = body.Tags[:100]
	}
	tags, err := store.SetSessionTags(sessionID, body.Tags)
	if err != nil {
		logging.Warning("Failed to save tags of session %s: %v", sessionID, err)
		return c.Status(500).JSON(fiber.Map{"error": "failed to save tags"})
	}
	return c.JSON(fiber.Map{"tags": tags})
}

// HandleListSessionTags returns the tags of every session the caller may see.
//
//	GET /api/agent/session-tags → {"tags": {"<sessionId>": ["a"]}}
func (h *AgentSessionHandler) HandleListSessionTags(c *fiber.Ctx) error {
	store := h.sessionTagStore()
	if store == nil {
		return c.Status(503).JSON(fiber.Map{"error": "session tags are not available"})
	}
	all, err := store.AllSessionTags()
	if err != nil {
		logging.Warning("Failed to list session tags: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "failed to list tags"})
	}
	if username, ok := c.Locals("username").(string); ok && username != "" && len(all) > 0 {
		ids := make([]uuid.UUID, 0, len(all))
		for id := range all {
			ids = append(ids, id)
		}
		visible := h.visibleSessionIDs(username, ids)
		for id := range all {
			if !visible[id] {
				delete(all, id)
			}
		}
	}
	out := make(map[string][]string, len(all))
	for id, tags := range all {
		out[id.String()] = tags
	}
	return c.JSON(fiber.Map{"tags": out})
}

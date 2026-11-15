package handlers

// Usage limits (see agents/usage_limit.go). GET reports the account's latest
// rate-limit state; POST continues a session waiting for the reset right away
// and DELETE drops its wait. POST/DELETE need a signed-in user
// (principal.RequireSignedIn), like continuing sessions after a restart: one
// sends a prompt, the other stops one from being sent.

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/agentruntime"
)

// Privileged-action names for principal.RequireSignedIn.
const (
	ResumeLimitWaitAction = "Continuing a session waiting for the usage limit"
	CancelLimitWaitAction = "Cancelling a session's wait for the usage limit"
)

// codexUsageSource reads Codex's plan usage (set by NewAgentRuntimesHandler,
// which owns the detector that finds the codex CLI).
var codexUsageSource func(ctx context.Context) *agentruntime.CodexUsage

// HandleGetUsageLimit: GET /api/agent/usage-limit → {limit, codex?}.
// limit is Claude's latest report (after a restart, the one from before it
// with restored: true until the CLI sends a new one); with ?codex=1 the answer also carries
// Codex's usage (read from codex app-server at most every two minutes), so
// installs that never ask don't start codex.
func (h *AgentSessionHandler) HandleGetUsageLimit(c *fiber.Ctx) error {
	out := fiber.Map{"limit": nil}
	if h.agentHandler != nil && h.agentHandler.SessionManager != nil {
		out["limit"] = h.agentHandler.SessionManager.UsageLimitForDisplay()
	}
	if c.Query("codex") == "1" && codexUsageSource != nil {
		out["codex"] = codexUsageSource(c.UserContext())
	}
	return c.JSON(out)
}

// limitWaitSession parses :id and checks the caller may see the session.
func (h *AgentSessionHandler) limitWaitSession(c *fiber.Ctx) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return uuid.Nil, c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid session id"})
	}
	if username, _ := c.Locals("username").(string); username != "" {
		if !h.visibleSessionIDs(username, []uuid.UUID{id})[id] {
			return uuid.Nil, c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "session not found"})
		}
	}
	return id, nil
}

// HandleResumeLimitWait: POST /api/agent/sessions/:id/limit-wait/resume.
func (h *AgentSessionHandler) HandleResumeLimitWait(c *fiber.Ctx) error {
	id, err := h.limitWaitSession(c)
	if id == uuid.Nil {
		return err
	}
	if h.agentHandler == nil || !h.agentHandler.SessionManager.ResumeLimitWaitNow(id) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "the session is not waiting for the usage limit"})
	}
	return c.JSON(fiber.Map{"resumed": true})
}

// HandleCancelLimitWait: DELETE /api/agent/sessions/:id/limit-wait.
func (h *AgentSessionHandler) HandleCancelLimitWait(c *fiber.Ctx) error {
	id, err := h.limitWaitSession(c)
	if id == uuid.Nil {
		return err
	}
	if h.agentHandler == nil || !h.agentHandler.SessionManager.CancelLimitWait(id) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "the session is not waiting for the usage limit"})
	}
	return c.JSON(fiber.Map{"cancelled": true})
}

package handlers

import (
	"encoding/json"
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/agents"
	"github.com/schlunsen/eggify/internal/server/principal"
	"github.com/schlunsen/eggify/internal/workspacetrust"
)

// Workspace trust endpoints (see internal/server/agents/workspace_trust.go).
//
//	GET    /api/agent/sessions/:id/workspace-trust  status + what the folder's config would enable
//	POST   /api/agent/sessions/:id/workspace-trust  {"fingerprint": "<as reviewed>"} trust it
//	DELETE /api/agent/sessions/:id/workspace-trust  stop trusting it

// Trusting a folder lets its config run commands on the next turn, so it is a
// human-only action: POST/DELETE need a signed-in user, not the API key an
// agent can read (principal.RequireUser on the routes, principal.Check here).
// With user auth off there is no principal; the change is allowed as "local".
const (
	actionTrustWorkspace  = "Trusting a folder"
	actionRevokeWorkspace = "Revoking folder trust"
)

// workspaceTrustActor names who changed workspace trust, for the log and the
// trusted_by column.
func workspaceTrustActor(c *fiber.Ctx) string {
	if username, ok := principal.Username(c); ok {
		return username
	}
	logging.Warning("SECURITY: workspace trust changed without a user principal (user auth disabled)")
	return "local"
}

type workspaceTrustResponse struct {
	SessionID   string                    `json:"session_id"`
	Path        string                    `json:"path"`
	Status      workspacetrust.Status     `json:"status"`
	LoadsConfig bool                      `json:"loads_config"`
	Summary     string                    `json:"summary"`
	Inventory   *workspacetrust.Inventory `json:"inventory,omitempty"`
	TrustedAt   string                    `json:"trusted_at,omitempty"`
	TrustedBy   string                    `json:"trusted_by,omitempty"`
	// Trusted is what the user trusted last time (for "changed" folders).
	Trusted *workspacetrust.Inventory `json:"trusted_inventory,omitempty"`
}

func workspaceTrustJSON(sessionID uuid.UUID, d workspacetrust.Decision) workspaceTrustResponse {
	r := workspaceTrustResponse{
		SessionID:   sessionID.String(),
		Path:        d.Path,
		Status:      d.Status,
		LoadsConfig: d.LoadProjectConfig(),
		Inventory:   d.Inventory,
	}
	if d.Inventory != nil {
		r.Summary = d.Inventory.Summary()
	}
	if d.Record != nil {
		r.TrustedAt = d.Record.TrustedAt.UTC().Format("2006-01-02T15:04:05Z")
		r.TrustedBy = d.Record.TrustedBy
		if d.Status == workspacetrust.StatusChanged && d.Record.Snapshot != "" {
			var prev workspacetrust.Inventory
			if json.Unmarshal([]byte(d.Record.Snapshot), &prev) == nil {
				r.Trusted = &prev
			}
		}
	}
	return r
}

func (h *AgentSessionHandler) workspaceTrustSession(c *fiber.Ctx) (uuid.UUID, *agents.AgentSession, error) {
	if h.agentHandler == nil {
		return uuid.Nil, nil, c.Status(503).JSON(fiber.Map{"error": "agent handler not initialized"})
	}
	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return uuid.Nil, nil, c.Status(400).JSON(fiber.Map{"error": "invalid session ID"})
	}
	// validateSessionOwnership writes a 403/404 and returns the (usually nil)
	// write error, so also check the status it left behind.
	if err := h.validateSessionOwnership(c, sessionID); err != nil || c.Response().StatusCode() >= 400 {
		return uuid.Nil, nil, err
	}
	session, err := h.agentHandler.SessionManager.GetSession(sessionID)
	if err != nil {
		return uuid.Nil, nil, c.Status(404).JSON(fiber.Map{"error": "session not found"})
	}
	return sessionID, session, nil
}

// HandleGetWorkspaceTrust reports whether the session's folder config is loaded.
func (h *AgentSessionHandler) HandleGetWorkspaceTrust(c *fiber.Ctx) error {
	sessionID, session, err := h.workspaceTrustSession(c)
	if session == nil {
		return err
	}
	d := h.agentHandler.SessionManager.WorkspaceTrustDecision(session)
	return c.JSON(workspaceTrustJSON(sessionID, d))
}

// HandleTrustWorkspace trusts the session's folder with the config the user
// reviewed. The next turn starts a CLI that loads it.
func (h *AgentSessionHandler) HandleTrustWorkspace(c *fiber.Ctx) error {
	sessionID, session, err := h.workspaceTrustSession(c)
	if session == nil {
		return err
	}
	if ok, err := principal.Check(c, actionTrustWorkspace); !ok {
		return err
	}
	who := workspaceTrustActor(c)
	var body struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := c.BodyParser(&body); err != nil || body.Fingerprint == "" {
		return c.Status(400).JSON(fiber.Map{"error": "fingerprint of the reviewed config is required"})
	}
	sm := h.agentHandler.SessionManager
	if session.Options.WorkingDirectory == nil || *session.Options.WorkingDirectory == "" {
		return c.Status(400).JSON(fiber.Map{"error": "session has no working directory"})
	}
	d, err := sm.TrustChecker().Trust(*session.Options.WorkingDirectory, body.Fingerprint, who)
	switch {
	case errors.Is(err, workspacetrust.ErrFingerprintMismatch):
		resp := workspaceTrustJSON(sessionID, d)
		return c.Status(409).JSON(fiber.Map{"error": err.Error(), "current": resp})
	case errors.Is(err, workspacetrust.ErrNothingToTrust):
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	case err != nil:
		logging.Error("Failed to trust workspace %s: %v", *session.Options.WorkingDirectory, err)
		return c.Status(500).JSON(fiber.Map{"error": "failed to trust folder"})
	}
	logging.Info("🛡️ %s trusted workspace %s (fingerprint %s) via session %s", who, d.Path, d.Inventory.Fingerprint, sessionID)
	return c.JSON(workspaceTrustJSON(sessionID, d))
}

// HandleRevokeWorkspaceTrust stops trusting the session's folder.
func (h *AgentSessionHandler) HandleRevokeWorkspaceTrust(c *fiber.Ctx) error {
	sessionID, session, err := h.workspaceTrustSession(c)
	if session == nil {
		return err
	}
	if ok, err := principal.Check(c, actionRevokeWorkspace); !ok {
		return err
	}
	who := workspaceTrustActor(c)
	if session.Options.WorkingDirectory == nil || *session.Options.WorkingDirectory == "" {
		return c.Status(400).JSON(fiber.Map{"error": "session has no working directory"})
	}
	sm := h.agentHandler.SessionManager
	if err := sm.TrustChecker().Revoke(*session.Options.WorkingDirectory); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to revoke trust"})
	}
	d := sm.WorkspaceTrustDecision(session)
	logging.Info("🛡️ %s stopped trusting workspace %s via session %s", who, d.Path, sessionID)
	return c.JSON(workspaceTrustJSON(sessionID, d))
}

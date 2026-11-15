// Package handlers - sandbox_handler.go serves the global sandbox settings
// (default for new sessions + egress allowlist).
package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/agents"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// SandboxAction names the privileged sandbox-settings change for
// principal.RequireUser (routes/sandbox.go): an agent holding the API key must
// not be able to widen its own egress allowlist or turn the default off.
const SandboxAction = "Changing sandbox settings"

// SandboxHandler serves GET/PUT /api/agent/sandbox.
type SandboxHandler struct {
	repo *database.Repository
}

// NewSandboxHandler creates the handler. Writes are guarded by
// principal.RequireUser(SandboxAction) on the route.
func NewSandboxHandler(repo *database.Repository) *SandboxHandler {
	return &SandboxHandler{repo: repo}
}

func (h *SandboxHandler) response(c *fiber.Ctx, prefs agents.SandboxPreferences) fiber.Map {
	return fiber.Map{
		"can_authorize_codex_network": principal.From(c).IsAdmin(),
		"default_enabled":             prefs.DefaultEnabled,
		"allowed_domains":             prefs.AllowedDomains,
		"default_allowed_domains":     agents.DefaultSandboxAllowedDomains,
	}
}

// HandleGet returns the global sandbox settings.
func (h *SandboxHandler) HandleGet(c *fiber.Ctx) error {
	return c.JSON(h.response(c, agents.LoadSandboxPreferences(h.repo)))
}

// HandleUpdate changes the global sandbox settings. Privileged: mount it
// behind principal.RequireUser(SandboxAction).
// Body: {"default_enabled": bool, "allowed_domains": ["example.com", ...]}.
// Omitted fields keep their current value.
func (h *SandboxHandler) HandleUpdate(c *fiber.Ctx) error {
	var body struct {
		DefaultEnabled *bool     `json:"default_enabled"`
		AllowedDomains *[]string `json:"allowed_domains"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	prefs := agents.LoadSandboxPreferences(h.repo)
	if body.DefaultEnabled != nil {
		prefs.DefaultEnabled = *body.DefaultEnabled
	}
	if body.AllowedDomains != nil {
		prefs.AllowedDomains = *body.AllowedDomains
	}
	saved, err := agents.SaveSandboxPreferences(h.repo, prefs)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	logging.Info("🧱 Sandbox settings updated by %v: default=%v, %d allowed domains", c.Locals("username"), saved.DefaultEnabled, len(saved.AllowedDomains))
	return c.JSON(h.response(c, saved))
}

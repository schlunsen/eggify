package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterConfig configures configuration endpoints
func RegisterConfig(api fiber.Router, h *handlers.ConfigHandler) {
	// SECURITY: only a signed-in user may fetch the API key (see HandleGetAPIKey).
	api.Get("/config/api-key", principal.RequireUser("Fetching the API key"), h.HandleGetAPIKey)
	api.Get("/config/cwd", h.HandleGetCWD)
	api.Get("/config/permissions", h.HandleGetProjectPermissions)
	// Project permissions (settings.local.json allow rules) auto-approve tools.
	changePerms := principal.RequireUser("Changing project permissions")
	api.Post("/config/permissions", changePerms, h.HandleAddPermission)
	api.Delete("/config/permissions", changePerms, h.HandleDeletePermission)
	api.Put("/config/permissions", changePerms, h.HandleUpdatePermissions)
}

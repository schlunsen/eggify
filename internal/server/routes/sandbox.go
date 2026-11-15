package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterSandbox configures the global sandbox settings endpoints. Reading is
// open to any authenticated caller; changing them needs a signed-in user
// (principal.RequireUser), never just the API key.
func RegisterSandbox(api fiber.Router, h *handlers.SandboxHandler) {
	if h == nil {
		return
	}
	api.Get("/agent/sandbox", h.HandleGet)
	api.Put("/agent/sandbox", principal.RequireUser(handlers.SandboxAction), h.HandleUpdate)
}

package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterAgentRuntimes configures the agent CLI onboarding endpoints. The
// status list is readable by any authenticated caller; starting, steering
// (pasting a code) or cancelling a CLI login, and reading a flow (it carries
// the sign-in URL), need a signed-in user, never just the API key.
func RegisterAgentRuntimes(api fiber.Router, h *handlers.AgentRuntimesHandler) {
	api.Get("/agent/runtimes", h.HandleList)
	user := principal.RequireUser(handlers.AgentLoginAction)
	api.Post("/agent/runtimes/:runtime/login", user, h.HandleLoginStart)
	api.Get("/agent/runtimes/:runtime/login", user, h.HandleLoginStatus)
	api.Post("/agent/runtimes/:runtime/login/cancel", user, h.HandleLoginCancel)
	api.Post("/agent/runtimes/:runtime/login/code", user, h.HandleLoginCode)
}

package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterLicense configures the license endpoints. They stay reachable while
// Eggify is not activated (the license gate exempts /api/license/*).
// Reading the status and "Check now" (a renewal) are open to any
// authenticated caller, the API key included. Deactivating needs a signed-in
// admin (principal.RequireUser): an agent holding the API key must not give
// the seat away. Activating needs one too once an account exists
// (handlers.ActivateGate); before the first account it is open, since the
// activation page comes before /setup (see server/license.go).
func RegisterLicense(api fiber.Router, h *handlers.LicenseHandler) {
	if h == nil {
		h = handlers.NewLicenseHandler(nil, nil)
	}
	g := api.Group("/license")
	g.Get("/", h.HandleStatus)
	g.Post("/activate", h.ActivateGate(), h.HandleActivate)
	g.Post("/deactivate", principal.RequireUser(handlers.LicenseAction), h.HandleDeactivate)
	g.Post("/check", h.HandleCheck)
}

package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterRefero configures Refero design style proxy and import endpoints
func RegisterRefero(api fiber.Router, h *handlers.ReferoHandler) {
	refero := api.Group("/refero")

	refero.Get("/styles", h.HandleListStyles)   // Proxy: browse paginated styles
	refero.Get("/styles/:id", h.HandleGetStyle) // Proxy: get full style detail
	refero.Post("/import", principal.RequireUser("Importing a design style into a project"), h.HandleImportStyle) // Import style as DESIGN.md into project
}

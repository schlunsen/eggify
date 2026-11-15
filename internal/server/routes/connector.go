package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterConnector configures connector endpoints
func RegisterConnector(api fiber.Router, h *handlers.ConnectorHandler) {
	connectors := api.Group("/connectors")

	// Connectors store third-party credentials that sessions get access to.
	changeConnectors := principal.RequireUser("Changing connectors")
	connectors.Get("/", h.HandleGetConnectors)
	connectors.Post("/:slug/connect", changeConnectors, h.HandleConnect)
	connectors.Delete("/:slug/disconnect", changeConnectors, h.HandleDisconnect)
}

package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
)

// RegisterChangelog configures the read-only "What's new" endpoint.
func RegisterChangelog(api fiber.Router, h *handlers.ChangelogHandler) {
	api.Get("/changelog", h.HandleGetChangelog)
}

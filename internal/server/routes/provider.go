package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterProvider configures provider endpoints
func RegisterProvider(api fiber.Router, h *handlers.ProviderHandler) {
	// Provider rows hold API keys and base URLs: changing them could send
	// prompts (and keys) elsewhere, so only a signed-in user may.
	changeProviders := principal.RequireUser("Changing providers")
	api.Get("/providers", h.HandleGetProviders)
	api.Post("/providers", changeProviders, h.HandleSaveProvider)
	api.Post("/providers/:id/set-default", changeProviders, h.HandleSetProviderAsDefault)
	api.Post("/providers/sync-models", h.HandleSyncModels)
	api.Delete("/providers/current", changeProviders, h.HandleDeleteProvider)
	api.Delete("/providers/:id", changeProviders, h.HandleDeleteProviderByID)
}

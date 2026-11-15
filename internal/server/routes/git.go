package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterGit configures GitHub operations endpoints
func RegisterGit(api fiber.Router, h *handlers.GitHandler) {
	api.Get("/git/search", h.HandleSearchGitHubRepositories)
	api.Get("/git/trending", h.HandleTrendingRepositories)
	// Cloning writes to disk: a signed-in user only (an API key alone gets 403).
	api.Post("/git/clone", principal.RequireUser(handlers.ProjectSetupAction), h.HandleCloneRepository)
}

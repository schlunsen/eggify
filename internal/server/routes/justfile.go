package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterJustfile configures justfile-related endpoints
func RegisterJustfile(api fiber.Router, h *handlers.JustfileHandler) {
	// Recipe listing
	api.Get("/projects/:id/just/recipes", h.HandleGetRecipes)

	// Job execution
	// Runs a recipe (arbitrary shell from the repo's justfile) outside any
	// agent permission check, so only a signed-in user may start one.
	api.Post("/projects/:id/just/run", principal.RequireUser("Running a just recipe"), h.HandleRunRecipe)

	// Job management
	api.Get("/projects/:id/just/jobs", h.HandleGetJobs)
	api.Get("/projects/:id/just/jobs/:jobId", h.HandleGetJob)
	api.Post("/projects/:id/just/jobs/:jobId/stop", h.HandleStopJob)
	api.Delete("/projects/:id/just/jobs/:jobId", h.HandleDeleteJob)

	// Live output streaming (SSE)
	api.Get("/projects/:id/just/jobs/:jobId/stream", h.HandleStreamJob)
}

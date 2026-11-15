package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterProject configures project management endpoints
func RegisterProject(api fiber.Router, h *handlers.ProjectHandler) {
	// New-project flow and the Projects page overview. Registered before
	// /projects/:id so the static segments win. Folder creation, cloning and
	// folder browsing need a signed-in user (an API key alone gets 403).
	setup := principal.RequireUser(handlers.ProjectSetupAction)
	api.Get("/projects/overview", h.HandleProjectsOverview)
	api.Get("/projects/setup/defaults", setup, h.HandleSetupDefaults)
	api.Get("/projects/setup/inspect", setup, h.HandleSetupInspect)
	api.Get("/projects/setup/complete", setup, h.HandleSetupComplete)
	api.Post("/projects/setup", setup, h.HandleSetupProject)
	api.Get("/projects/setup/jobs/:jobId", setup, h.HandleSetupJob)
	api.Delete("/projects/setup/jobs/:jobId", setup, h.HandleCancelSetupJob)

	// Project management endpoints
	api.Get("/projects", h.HandleGetProjects)
	// Creating a project picks a folder the file browser serves and that
	// sessions run in: only a signed-in user may (an API key alone gets 403).
	// Changing a project's folder or system prompt is checked in
	// HandleUpdateProject (other fields stay open to the API key).
	api.Post("/projects", principal.RequireUser(handlers.ProjectChangeAction), h.HandleCreateProject)
	api.Get("/projects/:id", h.HandleGetProject)
	api.Put("/projects/:id", h.HandleUpdateProject)
	api.Delete("/projects/:id", h.HandleDeleteProject)
	api.Get("/projects/:id/sessions", h.HandleGetProjectSessions)
	api.Get("/projects/:id/stats", h.HandleGetProjectStats)
	api.Get("/projects/:id/icon", h.HandleProjectIcon)
	// Project Git page (read-only).
	api.Get("/projects/:id/git/overview", h.HandleProjectGitOverview)
	api.Get("/projects/:id/git/changes", h.HandleProjectGitChanges)

	// Default skills endpoints
	api.Get("/projects/:id/default-skills", h.HandleGetDefaultSkills)
	// Default skills join every new session of the project (hooks included).
	api.Put("/projects/:id/default-skills", principal.RequireUser("Changing a project's default skills"), h.HandleSetDefaultSkills)
	api.Post("/projects/:id/default-skills/toggle", principal.RequireUser("Changing a project's default skills"), h.HandleToggleDefaultSkill)

	// Project area endpoints. An area's context prompt is put into the system
	// prompt of every session started in it and its folder is that session's
	// working directory, so changing areas needs a signed-in user. Detect only
	// reads the folder structure and stores nothing.
	areaChange := principal.RequireUser(handlers.ProjectAreaChangeAction)
	api.Get("/projects/:id/areas", h.HandleGetProjectAreas)
	api.Post("/projects/:id/areas", areaChange, h.HandleCreateProjectArea)
	api.Get("/projects/:id/areas/:areaId", h.HandleGetProjectArea)
	api.Put("/projects/:id/areas/:areaId", areaChange, h.HandleUpdateProjectArea)
	api.Delete("/projects/:id/areas/:areaId", areaChange, h.HandleDeleteProjectArea)
	api.Post("/projects/:id/areas/detect", h.HandleDetectProjectAreas)

	// Area context generation
	api.Post("/areas/generate-context", h.HandleGenerateAreaContext)

	// Feature file endpoints (reads/writes .claude/features/*.md in the project
	// directory; never through symbolic links or outside the project). Writing
	// and deleting files in the user's project needs a signed-in user.
	featureChange := principal.RequireUser(handlers.FeatureChangeAction)
	api.Get("/projects/:id/features", h.HandleListFeatures)
	api.Get("/projects/:id/features/:name", h.HandleGetFeature)
	api.Put("/projects/:id/features/:name", featureChange, h.HandleSaveFeature)
	api.Delete("/projects/:id/features/:name", featureChange, h.HandleDeleteFeature)
}

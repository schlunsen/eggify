package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterPacks configures pack management endpoints
func RegisterPacks(api fiber.Router, h *handlers.PacksHandler) {
	packsGroup := api.Group("/packs")
	// Installing runs the pack's setup command (outside any sandbox) and adds
	// its skills and hooks as approved; enabling writes .claude/skills.
	user := principal.RequireUser("Installing or changing a skill pack")

	// List all available packs
	packsGroup.Get("/", h.HandleListPacks)

	// List installed packs
	packsGroup.Get("/installed", h.HandleGetInstalledPacks)

	// Get a specific pack
	packsGroup.Get("/:name", h.HandleGetPack)

	// Install a pack
	packsGroup.Post("/:name/install", user, h.HandleInstallPack)

	// Enable a pack for a specific project (copies SKILL.md files)
	packsGroup.Post("/:name/enable-project", user, h.HandleEnablePackForProject)

	// Disable a pack for a specific project (removes copied skills)
	packsGroup.Post("/:name/disable-project", user, h.HandleDisablePackForProject)

	// Check if a pack is enabled for a specific project
	packsGroup.Get("/:name/project-status", h.HandlePackProjectStatus)

	// Uninstall a pack
	packsGroup.Delete("/:name", user, h.HandleUninstallPack)
}

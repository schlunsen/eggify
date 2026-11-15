package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterMCPConfig configures per-project .mcp.json management endpoints
func RegisterMCPConfig(api fiber.Router, h *handlers.MCPConfigHandler) {
	// An MCP server is a command the agent CLI starts: only a signed-in user may add or remove one.
	changeMCP := principal.RequireUser("Changing MCP servers")
	api.Get("/projects/:id/mcp-config", h.HandleGetProjectMCPConfig)
	api.Post("/projects/:id/mcp-config/servers", changeMCP, h.HandleAddProjectMCPServer)
	api.Delete("/projects/:id/mcp-config/servers/:name", changeMCP, h.HandleDeleteProjectMCPServer)
}

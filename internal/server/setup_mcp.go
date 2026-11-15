package server

import (
	"os"

	"github.com/schlunsen/eggify/internal/mcp"
)

// setupMCPConfig ensures .mcp.json exists and is properly configured
func (s *Server) setupMCPConfig() {
	// The desktop app's working directory is not a project (Finder starts it
	// in /), so don't drop a .mcp.json there.
	if s.desktop != nil {
		return
	}
	if cwd, err := os.Getwd(); err == nil {
		if configPath, err := mcp.EnsureMCPConfig(cwd); err == nil {
			if !s.quiet {
				println("📋 MCP config ready: " + configPath)
			}
		} else if !s.quiet {
			println("⚠️  Failed to create MCP config: " + err.Error())
		}

		// Update existing .mcp.json if it has old inline shell command
		if err := mcp.UpdateMCPConfigIfNeeded(cwd); err != nil && !s.quiet {
			println("⚠️  Failed to update MCP config: " + err.Error())
		}
	}
}

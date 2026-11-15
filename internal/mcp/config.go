package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// MCPConfig represents the .mcp.json configuration file
type MCPConfig struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// MCPServerConfig represents a single MCP server configuration
type MCPServerConfig struct {
	Description string   `json:"description,omitempty"`
	Command     string   `json:"command"`
	Args        []string `json:"args,omitempty"`
}

// findEggifyBinary finds the path to the eggify binary
// Tries in order: current directory, project root, PATH, common install locations
func findEggifyBinary() (string, error) {
	// Get current working directory
	cwd, _ := os.Getwd()

	// Try current directory first (for development)
	localPaths := []string{
		filepath.Join(cwd, "eggify"),       // ./eggify
		filepath.Join(cwd, "..", "eggify"), // ../eggify (if in subdirectory)
	}

	for _, path := range localPaths {
		if _, err := os.Stat(path); err == nil {
			absPath, _ := filepath.Abs(path)
			return absPath, nil
		}
	}

	// Try to find in PATH
	if path, err := exec.LookPath("eggify"); err == nil {
		absPath, _ := filepath.Abs(path)
		return absPath, nil
	}

	// Try common installation locations
	homeDir, _ := os.UserHomeDir()
	commonPaths := []string{
		"/usr/local/bin/eggify",
		filepath.Join(homeDir, ".local", "bin", "eggify"),
		filepath.Join(homeDir, "go", "bin", "eggify"),
	}

	for _, path := range commonPaths {
		if _, err := os.Stat(path); err == nil {
			absPath, _ := filepath.Abs(path)
			return absPath, nil
		}
	}

	return "", fmt.Errorf("eggify binary not found in current directory, PATH, or common locations")
}

// EnsureMCPConfig creates .mcp.json if it doesn't exist or is empty/invalid
// Returns the absolute path to the config file
func EnsureMCPConfig(projectRoot string) (string, error) {
	configPath := filepath.Join(projectRoot, ".mcp.json")

	// Check if file already exists and is valid
	if info, err := os.Stat(configPath); err == nil && info.Size() > 2 {
		// File exists and has content, verify it's valid JSON
		if data, readErr := os.ReadFile(configPath); readErr == nil {
			var check MCPConfig
			if json.Unmarshal(data, &check) == nil && len(check.MCPServers) > 0 {
				// Valid config with servers defined, nothing to do
				return configPath, nil
			}
		}
	}

	// Find eggify binary path
	cctPath, err := findEggifyBinary()
	if err != nil {
		return "", fmt.Errorf("failed to find eggify binary: %w", err)
	}

	// Create default config
	config := MCPConfig{
		MCPServers: map[string]MCPServerConfig{
			"eggify-tools": {
				Description: "Eggify tools — session handover, skills, memory, deployments, and GPU management",
				Command:     cctPath,
				Args:        []string{"mcp-server"},
			},
		},
	}

	// Marshal to JSON with indentation
	jsonData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal MCP config: %w", err)
	}

	// Write to file
	if err := os.WriteFile(configPath, jsonData, 0644); err != nil {
		return "", fmt.Errorf("failed to write MCP config: %w", err)
	}

	return configPath, nil
}

// GetMCPConfigExample returns an example .mcp.json configuration
func GetMCPConfigExample() string {
	cctPath, _ := findEggifyBinary()
	if cctPath == "" {
		cctPath = "/usr/local/bin/eggify"
	}

	example := fmt.Sprintf(`{
  "mcpServers": {
    "eggify-tools": {
      "description": "Eggify tools — session handover, skills, memory, deployments, and GPU management",
      "command": "%s",
      "args": ["mcp-server"]
    }
  }
}`, cctPath)

	return example
}

// UpdateMCPConfigIfNeeded updates existing .mcp.json to use standalone MCP server if it has old bridge script
func UpdateMCPConfigIfNeeded(projectRoot string) error {
	configPath := filepath.Join(projectRoot, ".mcp.json")

	// Read existing config
	data, err := os.ReadFile(configPath)
	if err != nil {
		// File doesn't exist, nothing to update
		return nil
	}

	// Parse config — if empty or invalid, skip update (EnsureMCPConfig will recreate it)
	var config MCPConfig
	if err := json.Unmarshal(data, &config); err != nil {
		// File is empty or invalid JSON — nothing to update
		return nil
	}

	// Check if eggify-tools exists and uses old methods
	if server, exists := config.MCPServers["eggify-tools"]; exists {
		needsUpdate := false

		// Check if it's using the old inline shell command or mcp-bridge script
		if server.Command == "sh" && len(server.Args) > 0 {
			needsUpdate = true
		} else if filepath.Base(server.Command) == "mcp-bridge" {
			needsUpdate = true
		}

		if needsUpdate {
			// Find eggify binary
			cctPath, err := findEggifyBinary()
			if err != nil {
				// Can't update without eggify binary
				return nil
			}

			// Update to use standalone MCP server
			config.MCPServers["eggify-tools"] = MCPServerConfig{
				Description: "Eggify tools — session handover, skills, memory, deployments, and GPU management",
				Command:     cctPath,
				Args:        []string{"mcp-server"},
			}

			// Write updated config
			jsonData, err := json.MarshalIndent(config, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal updated MCP config: %w", err)
			}

			if err := os.WriteFile(configPath, jsonData, 0644); err != nil {
				return fmt.Errorf("failed to write updated MCP config: %w", err)
			}

			fmt.Printf("✅ Updated .mcp.json to use standalone 'eggify mcp-server' command\n")
		}
	}

	return nil
}

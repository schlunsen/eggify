package server

import (
	"fmt"

	"github.com/schlunsen/eggify/internal/secretstore"
)

// setupConfig loads and initializes the server configuration
func (s *Server) setupConfig() (*ConfigManager, error) {
	// Initialize configuration
	configManager := NewConfigManager(s.claudeDir)
	config, err := configManager.LoadOrCreateConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	// Load environment variable overrides (including tunnel settings, OAuth secrets, etc.)
	if err := configManager.LoadConfigFromEnvironment(config); err != nil {
		return nil, fmt.Errorf("failed to load environment overrides: %w", err)
	}

	// Desktop app: loopback-only plain HTTP (no-op for the CLI)
	s.applyDesktopConfig(config)

	s.config = config

	// Pick the secret store backend before anything loads an encryption key.
	secretstore.Configure(s.claudeDir, config.Auth.SecretStore)

	// Override port from config if not set
	if s.port == 0 {
		s.port = config.Server.Port
	}

	// Use verbose from config if not set explicitly
	if !s.verbose && config.Server.Verbose {
		s.verbose = config.Server.Verbose
	}

	return configManager, nil
}

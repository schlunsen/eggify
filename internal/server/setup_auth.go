package server

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// setupAuthentication initializes API key, user authentication, MFA, and OAuth
func (s *Server) setupAuthentication(configManager *ConfigManager) (string, error) {
	var serverAPIKey string

	// Initialize API key authentication
	if s.config.Auth.Enabled {
		apiKey, err := configManager.EnsureAPIKey()
		if err != nil {
			return "", fmt.Errorf("failed to initialize API key: %w", err)
		}
		serverAPIKey = apiKey
		s.authMiddleware = NewAuthMiddleware(apiKey, true)
	}

	// Always initialize user store for checking if users exist (needed for setup flow)
	if err := s.initializeUserStore(); err != nil {
		return "", err
	}

	// Initialize full user authentication if enabled
	if s.config.Auth.UserAuthEnabled {
		if err := s.setupUserAuthentication(configManager); err != nil {
			return "", err
		}
	}

	s.configurePrivilegedActionPolicy()

	return serverAPIKey, nil
}

// configurePrivilegedActionPolicy decides whether privileged actions
// (approving permissions, YOLO, always-allow rules, provider keys, MCP
// servers, the terminal, ...) require a signed-in user. They do whenever user
// authentication is on. Without it there is no way to tell the human from an
// agent that read the API key, so everything keeps working as before and we
// warn once.
func (s *Server) configurePrivilegedActionPolicy() {
	enforced := s.sessionAuthMiddleware != nil
	principal.SetEnforced(enforced)
	// With several accounts, privileged actions need an admin (user_admin.go).
	if s.userStore != nil {
		principal.SetAdminResolver(s.userStore.MayAdminister)
	}
	if enforced {
		return
	}
	const msg = "User authentication is disabled: agents that read the API key (~/.claude/eggify/.secret) " +
		"can approve their own permissions, enable YOLO mode and change providers. " +
		"Enable user auth (user_auth_enabled) to protect privileged actions against prompt injection."
	logging.Warning("%s", msg)
	if !s.quiet {
		fmt.Printf("⚠️  %s\n", msg)
	}
}

// initializeUserStore initializes the basic user store for checking if users exist
func (s *Server) initializeUserStore() error {
	if s.userStore != nil {
		return nil // Already initialized
	}

	cctDir := filepath.Join(s.claudeDir, "eggify")
	s.userStore = NewUserStore(cctDir)
	s.userStore.SetDatabase(s.db) // Attach database for operations
	if s.config != nil {
		s.userStore.SetSessionLifetime(time.Duration(s.config.Auth.SessionLifetimeHours) * time.Hour)
	}
	if err := s.userStore.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize user store: %w", err)
	}

	return nil
}

// setupUserAuthentication initializes session auth, MFA, and OAuth
// (assumes userStore is already initialized by initializeUserStore)
func (s *Server) setupUserAuthentication(configManager *ConfigManager) error {
	// Ensure user store is initialized
	if err := s.initializeUserStore(); err != nil {
		return err
	}

	// Create session auth middleware
	s.sessionAuthMiddleware = NewSessionAuthMiddleware(s.userStore, true, s.config.Auth.RequireLogin)
	s.sessionAuthMiddleware.SetAllowedOrigins(s.config.CORS.AllowedOrigins)

	if s.userStore.HasUsers() {
		logging.Info("User authentication enabled (%d users)", len(s.userStore.ListUsers()))
	} else {
		// Show warning on first run - important for users to see
		if !s.quiet {
			fmt.Printf("⚠️  User authentication enabled but no users configured\n")
			fmt.Printf("   Use the TUI or API to create an admin user\n")
		}
		logging.Warning("User authentication enabled but no users configured")
	}

	// Initialize MFA manager with a unique encryption key per installation
	// The key is securely generated and stored in ~/.claude/eggify/mfa_encryption.key
	encryptionKey, err := LoadOrGenerateEncryptionKey(filepath.Join(s.claudeDir, "eggify"))
	if err != nil {
		return fmt.Errorf("failed to load or generate MFA encryption key: %w", err)
	}

	mfaManager, err := NewMFAManager(encryptionKey)
	if err != nil {
		return fmt.Errorf("failed to initialize MFA manager: %w", err)
	}
	s.mfaManager = mfaManager
	// SECURITY: attempt locks and the last accepted TOTP step survive restarts.
	if s.db != nil {
		s.mfaThrottle = persistMFAThrottle(s.db.GetDB())
	}
	logging.Info("MFA (Multi-Factor Authentication) system initialized")

	// Initialize OAuth state manager for CSRF protection
	s.oauthStateManager = NewOAuthStateManager(s.db)

	// Log OAuth status if configured
	if s.config.Auth.OAuth != nil && s.config.Auth.OAuth.Enabled {
		enabledProviders := []string{}
		for name, provider := range s.config.Auth.OAuth.Providers {
			if provider.Enabled {
				enabledProviders = append(enabledProviders, name)
			}
		}
		if len(enabledProviders) > 0 {
			logging.Info("OAuth enabled with %d provider(s): %v", len(enabledProviders), enabledProviders)
		}
		for name, provider := range s.config.Auth.OAuth.Providers {
			if provider.Enabled && provider.AutoCreateUser && provider.AllowOpenRegistration {
				logging.Warning("OAuth provider %s has open registration on: anyone with a verified %s account can create an Eggify account. "+
					"Set auth.access_control (allowed_users / allowed_domains) and turn allow_open_registration off unless that is intended.", name, name)
			}
		}
	}

	// Initialize access control validator if configured
	if s.config.Auth.AccessControl != nil && s.config.Auth.AccessControl.Enabled {
		s.accessControl = NewAccessValidator(s.config.Auth.AccessControl, s.db)
		logging.Info("Access control enabled")
	}

	return nil
}

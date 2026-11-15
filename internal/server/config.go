package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pterm/pterm"
)

// Config holds the analytics server configuration
type Config struct {
	TLS      TLSSettings      `json:"tls"`
	Auth     AuthSettings     `json:"auth"`
	Server   ServerSettings   `json:"server"`
	CORS     CORSSettings     `json:"cors"`
	Agent    AgentSettings    `json:"agent"`
	Terminal TerminalSettings `json:"terminal"`
	Tunnel   TunnelSettings   `json:"tunnel"`
}

// TunnelSettings holds tunnel (ngrok) configuration
type TunnelSettings struct {
	Enabled   bool   `json:"enabled"`
	Provider  string `json:"provider"`         // "ngrok" for now
	Domain    string `json:"domain,omitempty"` // e.g. "myapp.ngrok.app"
	AuthToken string `json:"-"`                // Never persist to config file, load from env only
}

// TLSSettings holds TLS configuration
type TLSSettings struct {
	Enabled  bool   `json:"enabled"`
	CertPath string `json:"cert_path,omitempty"`
	KeyPath  string `json:"key_path,omitempty"`
}

// AuthSettings holds authentication configuration
type AuthSettings struct {
	Enabled         bool   `json:"enabled"`
	APIKeyPath      string `json:"api_key_path,omitempty"`
	UserAuthEnabled bool   `json:"user_auth_enabled"` // Enable username/password authentication
	RequireLogin    bool   `json:"require_login"`     // Require login for all endpoints
	// SessionTimeout is unused: logins always lasted 7 days whatever it said,
	// and every config file has the 24 that was written as its default, so
	// it can't tell a choice from a default. SessionLifetimeHours replaced it.
	SessionTimeout int `json:"session_timeout_hours,omitempty"`
	// SessionLifetimeHours is how long a login lasts; 0 (unset) = 7 days.
	SessionLifetimeHours int                  `json:"session_lifetime_hours,omitempty"`
	OAuth                *OAuthSettings       `json:"oauth,omitempty"`          // NEW: OAuth configuration
	AccessControl        *AccessControlConfig `json:"access_control,omitempty"` // NEW: Access control configuration
	AdminUsers           []string             `json:"admin_users,omitempty"`    // NEW: List of admin usernames/emails
	// SecretStore selects where encryption keys live: "auto" (default),
	// "keychain" or "file". EGGIFY_SECRET_STORE overrides it. See internal/secretstore.
	SecretStore string `json:"secret_store,omitempty"`
}

// OAuthSettings holds OAuth2 configuration
type OAuthSettings struct {
	Enabled   bool                      `json:"enabled"`
	Providers map[string]*OAuthProvider `json:"providers"`
}

// OAuthProvider holds configuration for a specific OAuth provider (e.g., Google)
type OAuthProvider struct {
	Enabled        bool     `json:"enabled"`
	ClientID       string   `json:"client_id"`
	ClientSecret   string   `json:"client_secret,omitempty"` // Load from env var, not stored in config
	RedirectURI    string   `json:"redirect_uri"`
	Scopes         []string `json:"scopes"`
	AuthURL        string   `json:"auth_url"`
	TokenURL       string   `json:"token_url"`
	UserInfoURL    string   `json:"user_info_url"`
	AutoCreateUser bool     `json:"auto_create_user"`
	AutoEnableMFA  bool     `json:"auto_enable_mfa"`
	// AllowOpenRegistration lets AutoCreateUser create an account for ANY
	// verified email the provider vouches for. Off by default: without it,
	// accounts are only auto-created when access control is on with an
	// allow list (allowed_users / allowed_domains). See oauthMayAutoCreate.
	AllowOpenRegistration bool `json:"allow_open_registration,omitempty"`
}

// AccessControlConfig holds access control configuration
type AccessControlConfig struct {
	Enabled        bool     `json:"enabled"`
	AllowedUsers   []string `json:"allowed_users"`
	AllowedDomains []string `json:"allowed_domains"`
	AllowFirstUser bool     `json:"allow_first_user"`
	DenyMessage    string   `json:"deny_message,omitempty"`
}

// ServerSettings holds server configuration
type ServerSettings struct {
	Port    int    `json:"port"`
	Host    string `json:"host"`
	Quiet   bool   `json:"quiet"`
	Verbose bool   `json:"verbose"`
}

// CORSSettings holds CORS configuration
type CORSSettings struct {
	AllowedOrigins []string `json:"allowed_origins"`
}

// AgentSettings holds agent configuration
type AgentSettings struct {
	Model                 string           `json:"model"`
	MaxConcurrentSessions int              `json:"max_concurrent_sessions"`
	SessionRetentionDays  int              `json:"session_retention_days"`
	CleanupEnabled        bool             `json:"cleanup_enabled"`
	CleanupIntervalHours  int              `json:"cleanup_interval_hours"`
	Worktree              WorktreeSettings `json:"worktree"`
	DefaultProvider       string           `json:"default_provider,omitempty"` // Provider ID: "claude", "custom", "deepseek", "glm", "kimi"
	DefaultModel          string           `json:"default_model,omitempty"`    // Model name override (e.g., "qwen3.5")
	PermissionMode        string           `json:"permission_mode,omitempty"`  // "default", "acceptEdits", "bypassPermissions", "yolo"
}

// WorktreeSettings holds git worktree configuration
type WorktreeSettings struct {
	Enabled                bool   `json:"enabled"`                   // Feature flag for worktree support
	BaseDirectory          string `json:"base_directory"`            // Subdirectory name for worktrees (default: ".worktrees")
	MaxWorktreesPerProject int    `json:"max_worktrees_per_project"` // Max worktrees per project (default: 10)
	AutoCleanupOnEnd       bool   `json:"auto_cleanup_on_end"`       // Remove worktree when session ends (default: true)
	CleanupOrphanedHours   int    `json:"cleanup_orphaned_hours"`    // Auto-prune orphans after N hours (default: 24)
}

// TerminalSettings holds terminal configuration
type TerminalSettings struct {
	Enabled                bool   `json:"enabled"`
	DefaultShell           string `json:"default_shell"`
	MaxConcurrentTerminals int    `json:"max_concurrent_terminals"`
	IdleTimeoutMinutes     int    `json:"idle_timeout_minutes"`
	MaxOutputBufferKB      int    `json:"max_output_buffer_kb"`
}

// ConfigManager handles configuration loading and saving
type ConfigManager struct {
	configDir  string
	configFile string
	secretFile string
}

// NewConfigManager creates a new configuration manager
func NewConfigManager(claudeDir string) *ConfigManager {
	configDir := filepath.Join(claudeDir, "eggify")
	return &ConfigManager{
		configDir:  configDir,
		configFile: filepath.Join(configDir, "config.json"),
		secretFile: filepath.Join(configDir, ".secret"),
	}
}

// LoadOrCreateConfig loads existing config or creates default
func (cm *ConfigManager) LoadOrCreateConfig() (*Config, error) {
	// Create eggify directory if it doesn't exist
	if err := os.MkdirAll(cm.configDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create eggify directory: %w", err)
	}

	// Check if config file exists
	if _, err := os.Stat(cm.configFile); os.IsNotExist(err) {
		// Create default config
		config := cm.getDefaultConfig()
		if err := cm.SaveConfig(config); err != nil {
			return nil, fmt.Errorf("failed to save default config: %w", err)
		}
		pterm.Info.Println("Created default eggify configuration")
		return config, nil
	}

	// Load existing config
	data, err := os.ReadFile(cm.configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Backfill defaults for fields added in newer versions
	if config.Tunnel.Provider == "" {
		config.Tunnel.Provider = "ngrok"
	}

	return &config, nil
}

// SaveConfig saves configuration to file
func (cm *ConfigManager) SaveConfig(config *Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(cm.configFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// EnsureAPIKey ensures an API key exists, generates one if needed
func (cm *ConfigManager) EnsureAPIKey() (string, error) {
	// Check if secret file exists
	if data, err := os.ReadFile(cm.secretFile); err == nil {
		apiKey := string(data)
		if len(apiKey) > 0 {
			return apiKey, nil
		}
	}

	// Generate new API key
	apiKey, err := cm.generateAPIKey()
	if err != nil {
		return "", fmt.Errorf("failed to generate API key: %w", err)
	}

	// Save to file
	if err := os.WriteFile(cm.secretFile, []byte(apiKey), 0600); err != nil {
		return "", fmt.Errorf("failed to write API key: %w", err)
	}

	pterm.Success.Println("Generated new API key for authentication")
	pterm.Info.Printf("API key saved to: %s\n", cm.secretFile)

	return apiKey, nil
}

// GetAPIKey returns the current API key
func (cm *ConfigManager) GetAPIKey() (string, error) {
	data, err := os.ReadFile(cm.secretFile)
	if err != nil {
		return "", fmt.Errorf("failed to read API key: %w", err)
	}
	return string(data), nil
}

// generateAPIKey generates a random API key
func (cm *ConfigManager) generateAPIKey() (string, error) {
	bytes := make([]byte, 32) // 256 bits
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// getDefaultConfig returns the default configuration
func (cm *ConfigManager) getDefaultConfig() *Config {
	return &Config{
		TLS: TLSSettings{
			Enabled: true,
		},
		Auth: AuthSettings{
			Enabled:              true,
			APIKeyPath:           cm.secretFile,
			UserAuthEnabled:      true,  // Enabled by default for security
			RequireLogin:         false, // Only require login if user auth is enabled
			SessionLifetimeHours: 168,   // 7 days
		},
		Server: ServerSettings{
			Port:  3333,
			Host:  "127.0.0.1", // Localhost only by default for security
			Quiet: false,
		},
		CORS: CORSSettings{
			AllowedOrigins: []string{
				"http://localhost:3333",
				"https://localhost:3333",
				"http://127.0.0.1:3333",
				"https://127.0.0.1:3333",
			},
		},
		Agent: AgentSettings{
			Model:                 "sonnet",
			MaxConcurrentSessions: 10,
			SessionRetentionDays:  30,
			CleanupEnabled:        true,
			CleanupIntervalHours:  24,
			Worktree: WorktreeSettings{
				Enabled:                true,
				BaseDirectory:          ".worktrees",
				MaxWorktreesPerProject: 10,
				AutoCleanupOnEnd:       true,
				CleanupOrphanedHours:   24,
			},
		},
		Terminal: TerminalSettings{
			Enabled:                true,
			DefaultShell:           "/bin/zsh", // Falls back to bash/sh if zsh not available
			MaxConcurrentTerminals: 5,
			IdleTimeoutMinutes:     30,
			MaxOutputBufferKB:      1024,
		},
		Tunnel: TunnelSettings{
			Enabled:  false,
			Provider: "ngrok",
		},
	}
}

// GetConfigPath returns the path to the config file
func (cm *ConfigManager) GetConfigPath() string {
	return cm.configFile
}

// GetSecretPath returns the path to the secret file
func (cm *ConfigManager) GetSecretPath() string {
	return cm.secretFile
}

// EnableUserAuth enables user authentication in the config
func (cm *ConfigManager) EnableUserAuth(requireLogin bool) error {
	config, err := cm.LoadOrCreateConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	config.Auth.UserAuthEnabled = true
	config.Auth.RequireLogin = requireLogin

	if err := cm.SaveConfig(config); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}

// DisableUserAuth disables user authentication in the config
func (cm *ConfigManager) DisableUserAuth() error {
	config, err := cm.LoadOrCreateConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	config.Auth.UserAuthEnabled = false
	config.Auth.RequireLogin = false

	if err := cm.SaveConfig(config); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}

// LoadOAuthSecretsFromEnv loads OAuth client secrets from environment variables
// This ensures secrets are never stored in config.json
func (cm *ConfigManager) LoadOAuthSecretsFromEnv(config *Config) error {
	if config.Auth.OAuth == nil || !config.Auth.OAuth.Enabled {
		return nil
	}

	// Load secrets for each enabled provider
	for providerName, provider := range config.Auth.OAuth.Providers {
		if !provider.Enabled {
			continue
		}

		// Try to load client_secret from environment variable
		// Pattern: EGGIFY_OAUTH_{PROVIDER}_CLIENT_SECRET
		envVar := fmt.Sprintf("EGGIFY_OAUTH_%s_CLIENT_SECRET", strings.ToUpper(providerName))
		if secret := os.Getenv(envVar); secret != "" {
			// Trim any whitespace/newlines that might have been accidentally included
			secret = strings.TrimSpace(secret)
			provider.ClientSecret = secret

			// Never log any part of the secret itself.
			pterm.Info.Printf("Loaded OAuth secret for %s from %s (%d chars)\n", providerName, envVar, len(secret))
		}

		// If client_secret not in config or env, log warning
		if provider.ClientSecret == "" {
			pterm.Warning.Printf("OAuth provider %s: client_secret not found in config.json or %s env var\n", providerName, envVar)
		}
	}

	return nil
}

// LoadConfigFromEnvironment applies environment variable overrides to the config
// Pattern: EGGIFY_{SECTION}_{SUBSECTION}_{FIELD} (uppercase, underscores)
func (cm *ConfigManager) LoadConfigFromEnvironment(config *Config) error {
	// Server settings
	if port := os.Getenv("EGGIFY_SERVER_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			config.Server.Port = p
		}
	}
	if host := os.Getenv("EGGIFY_SERVER_HOST"); host != "" {
		config.Server.Host = host
	}

	// Auth settings
	if enabled := os.Getenv("EGGIFY_AUTH_ENABLED"); enabled != "" {
		config.Auth.Enabled = parseBool(enabled)
	}
	if userAuthEnabled := os.Getenv("EGGIFY_AUTH_USER_AUTH_ENABLED"); userAuthEnabled != "" {
		config.Auth.UserAuthEnabled = parseBool(userAuthEnabled)
	}
	if requireLogin := os.Getenv("EGGIFY_AUTH_REQUIRE_LOGIN"); requireLogin != "" {
		config.Auth.RequireLogin = parseBool(requireLogin)
	}
	if lifetime := os.Getenv("EGGIFY_AUTH_SESSION_LIFETIME_HOURS"); lifetime != "" {
		if hours, err := strconv.Atoi(lifetime); err == nil {
			config.Auth.SessionLifetimeHours = hours
		}
	}

	// OAuth settings
	if oauthEnabled := os.Getenv("EGGIFY_AUTH_OAUTH_ENABLED"); oauthEnabled != "" {
		if config.Auth.OAuth == nil {
			config.Auth.OAuth = &OAuthSettings{
				Providers: make(map[string]*OAuthProvider),
			}
		}
		config.Auth.OAuth.Enabled = parseBool(oauthEnabled)
	}

	// Access control settings
	if acEnabled := os.Getenv("EGGIFY_AUTH_ACCESS_CONTROL_ENABLED"); acEnabled != "" {
		if config.Auth.AccessControl == nil {
			config.Auth.AccessControl = &AccessControlConfig{}
		}
		config.Auth.AccessControl.Enabled = parseBool(acEnabled)
	}
	if acAllowFirstUser := os.Getenv("EGGIFY_AUTH_ACCESS_CONTROL_ALLOW_FIRST_USER"); acAllowFirstUser != "" {
		if config.Auth.AccessControl == nil {
			config.Auth.AccessControl = &AccessControlConfig{}
		}
		config.Auth.AccessControl.AllowFirstUser = parseBool(acAllowFirstUser)
	}
	if acDenyMsg := os.Getenv("EGGIFY_AUTH_ACCESS_CONTROL_DENY_MESSAGE"); acDenyMsg != "" {
		if config.Auth.AccessControl == nil {
			config.Auth.AccessControl = &AccessControlConfig{}
		}
		config.Auth.AccessControl.DenyMessage = acDenyMsg
	}

	// Allowed users (comma-separated)
	if acAllowedUsers := os.Getenv("EGGIFY_AUTH_ACCESS_CONTROL_ALLOWED_USERS"); acAllowedUsers != "" {
		if config.Auth.AccessControl == nil {
			config.Auth.AccessControl = &AccessControlConfig{}
		}
		config.Auth.AccessControl.AllowedUsers = splitEnvList(acAllowedUsers)
	}

	// Allowed domains (comma-separated)
	if acAllowedDomains := os.Getenv("EGGIFY_AUTH_ACCESS_CONTROL_ALLOWED_DOMAINS"); acAllowedDomains != "" {
		if config.Auth.AccessControl == nil {
			config.Auth.AccessControl = &AccessControlConfig{}
		}
		config.Auth.AccessControl.AllowedDomains = splitEnvList(acAllowedDomains)
	}

	// Admin users (comma-separated)
	if adminUsers := os.Getenv("EGGIFY_AUTH_ADMIN_USERS"); adminUsers != "" {
		config.Auth.AdminUsers = splitEnvList(adminUsers)
	}

	// Tunnel settings
	if tunnelEnabled := os.Getenv("EGGIFY_TUNNEL_ENABLED"); tunnelEnabled != "" {
		config.Tunnel.Enabled = parseBool(tunnelEnabled)
	}
	if tunnelDomain := os.Getenv("EGGIFY_TUNNEL_DOMAIN"); tunnelDomain != "" {
		config.Tunnel.Domain = tunnelDomain
	}
	// Load ngrok auth token from environment (EGGIFY_NGROK_AUTHTOKEN takes precedence)
	if authToken := os.Getenv("EGGIFY_NGROK_AUTHTOKEN"); authToken != "" {
		config.Tunnel.AuthToken = authToken
	} else if authToken := os.Getenv("NGROK_AUTHTOKEN"); authToken != "" {
		config.Tunnel.AuthToken = authToken
	}

	// Agent defaults
	if defaultProvider := os.Getenv("EGGIFY_AGENT_DEFAULT_PROVIDER"); defaultProvider != "" {
		config.Agent.DefaultProvider = defaultProvider
	}
	if defaultModel := os.Getenv("EGGIFY_AGENT_DEFAULT_MODEL"); defaultModel != "" {
		config.Agent.DefaultModel = defaultModel
	}
	if permissionMode := os.Getenv("EGGIFY_AGENT_PERMISSION_MODE"); permissionMode != "" {
		config.Agent.PermissionMode = permissionMode
	}

	// Load OAuth secrets from environment
	return cm.LoadOAuthSecretsFromEnv(config)
}

// ValidateConfig validates the configuration and returns errors if invalid
func (cm *ConfigManager) ValidateConfig(config *Config) error {
	// Validate OAuth configuration if enabled
	if config.Auth.OAuth != nil && config.Auth.OAuth.Enabled {
		for providerName, provider := range config.Auth.OAuth.Providers {
			if !provider.Enabled {
				continue
			}

			// Validate required fields
			if provider.ClientID == "" {
				return fmt.Errorf("OAuth provider %s: client_id is required", providerName)
			}
			if provider.RedirectURI == "" {
				return fmt.Errorf("OAuth provider %s: redirect_uri is required", providerName)
			}
			if provider.AuthURL == "" {
				return fmt.Errorf("OAuth provider %s: auth_url is required", providerName)
			}
			if provider.TokenURL == "" {
				return fmt.Errorf("OAuth provider %s: token_url is required", providerName)
			}
			if provider.UserInfoURL == "" {
				return fmt.Errorf("OAuth provider %s: user_info_url is required", providerName)
			}

			// Check if client_secret is set (either in config or environment)
			if provider.ClientSecret == "" {
				envVar := fmt.Sprintf("EGGIFY_OAUTH_%s_CLIENT_SECRET", strings.ToUpper(providerName))
				if os.Getenv(envVar) == "" {
					return fmt.Errorf("OAuth provider %s: client_secret not found in config or %s env var", providerName, envVar)
				}
			}
		}
	}

	return nil
}

// parseBool converts a string to a boolean
// Accepts: "true", "1", "yes", "on" (case-insensitive) -> true
// All other values -> false
func parseBool(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "true" || s == "1" || s == "yes" || s == "on"
}

// splitEnvList splits a comma-separated env value, trimming each item and
// dropping empty ones ("a@x.com, b@y.com" → two entries).
func splitEnvList(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

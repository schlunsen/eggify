package server

import (
	"context"
	crypto_rand "crypto/rand"
	"path/filepath"

	"github.com/schlunsen/eggify/internal/connectors"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/packs"
	"github.com/schlunsen/eggify/internal/perfmon"
	"github.com/schlunsen/eggify/internal/providers"
	"github.com/schlunsen/eggify/internal/server/handlers"
)

// setupHandlers initializes all HTTP handlers
func (s *Server) setupHandlers(configManager *ConfigManager) {
	// Initialize auth handler (handles login, logout, password change, user management)
	s.authHandler = NewAuthHandler(
		s.userStore,
		s.mfaManager,
		s.oauthStateManager,
		s.accessControl,
		s.db,
		s.repo,
		s.config,
		s.config.TLS.Enabled,
	)
	// Signing out closes that session's agent WebSocket connections.
	s.authHandler.onSessionRevoked = s.closeSocketsForToken
	s.userStore.onMintedRevoked = s.closeSocketsForToken

	s.healthHandler = handlers.NewHealthHandler(
		s.processDetector,
		handlers.HealthHandlerConfig{
			TLSEnabled:  s.config.TLS.Enabled,
			AuthEnabled: s.config.Auth.Enabled,
		},
		s.agentConfig,
		s.port,
		s.claudeDir,
		s.quiet,
		s.verbose,
		s.agentHandler,
	)

	// Performance monitor: samples the server every 30 s and keeps the history
	// the dashboard's Settings → Performance reads.
	s.perfSampler = perfmon.New(perfmon.Options{
		DBPath: filepath.Join(s.claudeDir, "eggify", "eggify.db"),
		Extra: func() map[string]float64 {
			m := map[string]float64{}
			if s.wsHub != nil {
				m["ws_clients"] = float64(s.wsHub.ClientCount())
			}
			return m
		},
		Logf: func(format string, args ...any) { logging.Warning(format, args...) },
	})
	var perfCtx context.Context
	perfCtx, s.perfStop = context.WithCancel(context.Background())
	go s.perfSampler.Run(perfCtx)
	s.perfHandler = handlers.NewPerfHandler(s.perfSampler)

	s.providerHandler = handlers.NewProviderHandler(s.repo)
	s.providerHandler.SetRegistryConfig(providers.RegistryConfig{
		Enabled:  true,
		CacheDir: filepath.Join(s.claudeDir, "eggify"),
	})
	s.providerHandler.SetAgentDefaults(handlers.AgentDefaults{
		DefaultProvider: s.config.Agent.DefaultProvider,
		DefaultModel:    s.config.Agent.DefaultModel,
		PermissionMode:  s.config.Agent.PermissionMode,
	})

	// Initialize connector handler with encryption key
	connectorKey, err := connectors.LoadOrGenerateConnectorKey(s.claudeDir)
	if err != nil {
		// SECURITY: Never fall back to a zero key — all encrypted connector credentials
		// would be trivially decryptable. Use a random ephemeral key instead.
		logging.Error("Failed to load/generate connector encryption key: %v (using ephemeral key)", err)
		connectorKey = make([]byte, 32)
		if _, randErr := crypto_rand.Read(connectorKey); randErr != nil {
			logging.Error("FATAL: crypto/rand failed for connector key fallback: %v", randErr)
		}
	}
	s.connectorHandler = handlers.NewConnectorHandler(s.repo, connectorKey)
	s.connectorKey = connectorKey

	// Share connector encryption key with agent session manager and handler
	// This enables hot-pluggable connector credential injection into agent sessions
	if s.agentHandler != nil && s.agentHandler.SessionManager != nil {
		s.agentHandler.SessionManager.SetConnectorEncryptionKey(connectorKey)
	}

	s.analyticsHandler = handlers.NewAnalyticsHandler(
		s.conversationAnalyzer,
		s.conversationParser,
		s.stateCalculator,
		s.processDetector,
		s.shellDetector,
		s.resetTracker,
		s.modelProviderLookup,
		s.repo,
		s.db,
		s.wsHub,
		s.agentHandler,
		s.claudeDir,
		s.quiet,
	)

	s.configHandler = handlers.NewConfigHandler(
		configManager, // implements APIKeyProvider interface
		s.agentHandler,
		s.config.CORS.AllowedOrigins,
	)

	s.settingsHandler = handlers.NewSettingsHandler(s.repo, s.wsHub)

	// Global sandbox settings (writes need a signed-in user; see routes/sandbox.go).
	s.sandboxHandler = handlers.NewSandboxHandler(s.repo)

	// Agent CLI onboarding: install/login status of claude and codex, and the
	// CLIs' own login flows (privileged; see routes/agent_runtimes.go).
	s.agentRuntimes = handlers.NewAgentRuntimesHandler(s.repo)

	s.agentSessionHandler = handlers.NewAgentSessionHandler(
		s.agentHandler,
		s.repo,
		s.db,
		s.claudeDir,
	)
	s.agentSessionHandler.SetConnectorEncryptionKey(connectorKey)

	s.terminalHandlerAPI = handlers.NewTerminalHandler(
		s.terminalManager,
		s.repo,
	)
	// Terminal routes of a session check the caller may use that session.
	s.terminalHandlerAPI.SetSessionAuthorizer(s.agentSessionHandler.AuthorizeSession)

	s.projectHandler = handlers.NewProjectHandler(
		s.repo,
		s.agentHandler,
		s.claudeDir,
		s.verbose,
	)
	s.projectHandler.SetSessionVisibility(s.agentSessionHandler.VisibleSessionIDs)
	s.analyticsHandler.SetSessionVisibility(s.agentSessionHandler.VisibleSessionIDs)

	s.userProfileHandler = handlers.NewUserProfileHandler(s.repo, &userStoreAdapter{store: s.userStore})

	s.avatarHandler = handlers.NewAvatarHandler(s.repo, s.claudeDir)
	s.avatarHandler.SetEncryptionKey(connectorKey)

	s.gitHandler = handlers.NewGitHandler(s.repo, s.claudeDir)

	s.worktreeHandler = handlers.NewWorktreeHandler(s.repo)

	s.mcpConfigHandler = handlers.NewMCPConfigHandler(s.repo)

	s.ttsHandler = handlers.NewTTSHandler(s.claudeDir)

	s.fileBrowserHandler = handlers.NewFileBrowserHandler(s.repo)

	s.justfileHandler = handlers.NewJustfileHandler(s.repo)

	s.modelCacheHandler = handlers.NewModelCacheHandler(s.claudeDir)

	s.skillsHandler = handlers.NewSkillsHandler(s.repo, s.claudeDir)
	s.hooksHandler = handlers.NewHooksHandler(s.repo, s.claudeDir)

	// Initialize pack registry and handler
	packRegistry := packs.NewPackRegistry(s.repo, s.claudeDir)
	s.packsHandler = handlers.NewPacksHandler(packRegistry)

	// Remote access (ngrok): manager + API. Settings are encrypted with the
	// connector key.
	s.setupRemoteAccess(connectorKey)
	s.tunnelHandler = handlers.NewTunnelHandler(s.remoteAccess)

	// Note: appsHandler is initialized early in setupMiddleware() (before auth)
	// so that pre-auth internal routes can be registered.

	// RTK stats handler — reports rtk (Rust Token Killer) compression
	// savings from the local SQLite tracking DB and `rtk gain`. Safely
	// no-ops when rtk is not installed; endpoints degrade gracefully.
	if s.agentHandler != nil {
		s.rtkHandler = handlers.NewRTKHandler(s.agentHandler.SessionManager)
	} else {
		s.rtkHandler = handlers.NewRTKHandler(nil)
	}

	// Refero design style proxy/import handler
	s.referoHandler = handlers.NewReferoHandler()

	// Memory Palace handler
	s.memoryHandler = handlers.NewMemoryHandler(s.repo)
	s.reviewHandler = handlers.NewReviewHandler(s.repo)

	// n0: hand the proxy to agent sessions and wire session ownership checks
	s.wireN0Sessions()

	// Model-provider keys stay in the server: agents get a loopback proxy capability
	s.setupProviderProxy()
}

// Package server provides the Fiber-based HTTP server and REST API for Eggify analytics.
// It serves the analytics dashboard, WebSocket connections, and API endpoints
// for conversation data, process monitoring, and command history.
package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/analytics"
	"github.com/schlunsen/eggify/internal/credproxy"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/n0"
	"github.com/schlunsen/eggify/internal/perfmon"
	"github.com/schlunsen/eggify/internal/remoteaccess"
	"github.com/schlunsen/eggify/internal/server/agents"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/routes"
	"github.com/schlunsen/eggify/internal/server/terminal"
	ws "github.com/schlunsen/eggify/internal/websocket"
)

// Server wraps the Fiber app and analytics components.
// It provides a complete analytics backend with WebSocket support for real-time updates.
type Server struct {
	app                   *fiber.App
	csp                   *contentSecurityPolicy
	conversationAnalyzer  *analytics.ConversationAnalyzer
	conversationParser    *analytics.ConversationParser
	stateCalculator       *analytics.StateCalculator
	processDetector       *analytics.ProcessDetector
	shellDetector         *analytics.ShellDetector
	fileWatcher           *analytics.FileWatcher
	wsHub                 *ws.Hub
	resetTracker          *analytics.ResetTracker
	modelProviderLookup   *analytics.ModelProviderLookup
	db                    *database.Database
	repo                  *database.Repository
	config                *Config
	tlsConfig             *TLSConfig
	authMiddleware        *AuthMiddleware
	sessionAuthMiddleware *SessionAuthMiddleware
	userStore             *UserStore
	accessControl         *AccessValidator // Access control for user domain whitelisting
	mfaManager            *MFAManager      // MFA manager for TOTP and backup codes
	mfaThrottle           *mfaThrottle     // per-account MFA attempt lock (nil: defaultMFAThrottle)
	oauthStateManager     *OAuthStateManager
	agentHandler          *agents.AgentHandler
	agentConfig           *agents.Config
	terminalManager       *terminal.PTYManager
	terminalHandler       *terminal.TerminalHandler
	processManager        *agents.ProcessManager // Process manager for tracking sessions/terminals
	claudeDir             string
	port                  int
	quiet                 bool // Suppress output when running in TUI
	verbose               bool // Enable verbose/debug logging

	// HTTP Handlers
	healthHandler       *handlers.HealthHandler
	perfSampler         *perfmon.Sampler
	perfStop            context.CancelFunc
	perfHandler         *handlers.PerfHandler
	providerHandler     *handlers.ProviderHandler
	connectorHandler    *handlers.ConnectorHandler
	analyticsHandler    *handlers.AnalyticsHandler
	configHandler       *handlers.ConfigHandler
	settingsHandler     *handlers.SettingsHandler
	sandboxHandler      *handlers.SandboxHandler
	agentRuntimes       *handlers.AgentRuntimesHandler
	agentSessionHandler *handlers.AgentSessionHandler
	terminalHandlerAPI  *handlers.TerminalHandler // Renamed to avoid conflict with terminalHandler field
	projectHandler      *handlers.ProjectHandler
	userProfileHandler  *handlers.UserProfileHandler
	avatarHandler       *handlers.AvatarHandler
	gitHandler          *handlers.GitHandler
	worktreeHandler     *handlers.WorktreeHandler
	mcpConfigHandler    *handlers.MCPConfigHandler
	ttsHandler          *handlers.TTSHandler
	fileBrowserHandler  *handlers.FileBrowserHandler
	justfileHandler     *handlers.JustfileHandler
	modelCacheHandler   *handlers.ModelCacheHandler
	skillsHandler       *handlers.SkillsHandler
	hooksHandler        *handlers.HooksHandler
	packsHandler        *handlers.PacksHandler
	tunnelHandler       *handlers.TunnelHandler
	appsHandler         *handlers.AppsHandler
	rtkHandler          *handlers.RTKHandler    // RTK (Rust Token Killer) stats endpoints
	referoHandler       *handlers.ReferoHandler // Refero design style proxy/import
	memoryHandler       *handlers.MemoryHandler // Memory Palace endpoints
	reviewHandler       *handlers.ReviewHandler // Review queue for agent-written memories/skills
	authHandler         *AuthHandler            // Authentication handler (login, logout, user management)

	// Remote access (ngrok tunnel as an extra listener); see remote_access.go.
	remoteAccess *remoteaccess.Manager

	// Licensing (enforced only in licensed builds); see license.go.
	license        licenseService
	licenseStop    context.CancelFunc
	licenseHandler *handlers.LicenseHandler

	// n0 (MCP over OAuth)
	n0Manager  *n0.Manager
	n0Loopback *n0.LoopbackServer
	n0Handler  *handlers.N0Handler

	// Loopback model-provider proxy (keeps provider API keys out of agent envs)
	providerProxy *credproxy.Server

	// Desktop app mode (cmd/eggify-desktop); nil for the CLI. See desktop.go.
	desktop *DesktopOptions

	// Connector encryption key (also signs the resume-after-restart snapshot).
	connectorKey []byte
	// Continue sessions after a restart; see resume_after_restart.go.
	shutdownMu        sync.Mutex
	shutdownReason    string
	resumeWrittenOnce sync.Once
}

// newFiberConfig is the Fiber configuration of the server (shared with tests).
func newFiberConfig(quiet bool) fiber.Config {
	return fiber.Config{
		AppName:               "Claude Code Analytics",
		ServerHeader:          "",
		DisableStartupMessage: quiet, // Suppress Fiber startup banner in quiet mode

		// SECURITY: routes match case-sensitively. With case-insensitive
		// routing GET /API/agent/sessions reached the /api handler while the
		// auth middleware, which compares paths, treated it as a non-API page
		// and let it through. The middleware normalises paths too.
		CaseSensitive: true,

		// SECURITY: no proxy is trusted. ngrok runs in-process and tunnel
		// connections report the visitor's address as their RemoteAddr, so
		// c.IP() is always the TCP peer. Trusting X-Forwarded-For from
		// loopback let any local process (agents included) pick its own c.IP()
		// and dodge the login/MFA/pairing rate limiters. The check stays on
		// with an empty list, because with it off Fiber trusts X-Forwarded-*
		// (Proto, Host) from everyone in c.Protocol() and c.Hostname().
		EnableTrustedProxyCheck: true,
		TrustedProxies:          []string{},
		ProxyHeader:             "",

		// Timeouts. fasthttp clears the deadlines before a WebSocket hijack,
		// so these never cut a socket. ReadTimeout covers reading one whole
		// request (headers + body): 2 min leaves a 50 MB upload ~400 KB/s.
		// WriteTimeout is per response write; long, so big downloads to slow
		// clients still finish. IdleTimeout closes idle keep-alive conns.
		ReadTimeout:  2 * time.Minute,
		WriteTimeout: 15 * time.Minute,
		IdleTimeout:  2 * time.Minute,

		// Increase header size limits to prevent "Request Header Fields Too Large" error
		ReadBufferSize:  16384, // 16KB (default is 4KB)
		WriteBufferSize: 16384, // 16KB (default is 4KB)
		// 50MB stays: image/audio uploads (TTS, attachments, avatars) and
		// large JSON payloads use it. ReadTimeout bounds how long one may take.
		BodyLimit: 50 * 1024 * 1024,
	}
}

// NewServer creates a new Fiber server instance
func NewServer(claudeDir string, port int) *Server {
	return NewServerWithOptions(claudeDir, port, false, false)
}

// NewServerWithOptions creates a new Fiber server instance with options
func NewServerWithOptions(claudeDir string, port int, quiet bool, verbose bool) *Server {
	app := fiber.New(newFiberConfig(quiet))

	return &Server{
		app:       app,
		claudeDir: claudeDir,
		port:      port,
		quiet:     quiet,
		verbose:   verbose,
	}
}

// DisableTLS disables TLS for the server (useful for ngrok)
func (s *Server) DisableTLS() {
	if s.tlsConfig != nil {
		s.tlsConfig.Enabled = false
	} else {
		// Create a disabled TLS config if it doesn't exist
		s.tlsConfig = &TLSConfig{
			Enabled: false,
		}
	}
}

// Setup initializes analytics components and routes
func (s *Server) Setup() error {
	// Disable standard log output when in quiet mode (TUI)
	// This prevents log.Printf calls from writing to stdout
	if s.quiet {
		log.SetOutput(io.Discard)
	}

	// Initialize configuration
	configManager, err := s.setupConfig()
	if err != nil {
		return err
	}

	// Initialize logging
	if err := s.setupLogging(); err != nil {
		return err
	}

	// Initialize tunnel manager if enabled
	s.setupTunnel()

	// Initialize TLS certificates
	if err := s.setupTLS(); err != nil {
		return err
	}

	// Initialize database
	if err := s.setupDatabase(); err != nil {
		return err
	}

	// n0 connections: manager + loopback proxy/callback listener
	s.setupN0()

	// Initialize authentication
	serverAPIKey, err := s.setupAuthentication(configManager)
	if err != nil {
		return err
	}

	// Keep loopback-only paths off the public tunnel; before the desktop
	// routes, which run ahead of every other middleware.
	s.app.Use(s.tunnelPathGuard)

	// Desktop-only routes (no-op for the CLI); before any middleware
	s.registerDesktopRoutes()

	// Setup middleware (CORS, logger, auth)
	if err := s.setupMiddleware(); err != nil {
		return err
	}

	// Initialize agents and agent handler
	if err := s.setupAgents(serverAPIKey); err != nil {
		return err
	}

	// Initialize terminal components
	if err := s.setupTerminal(); err != nil {
		return err
	}

	// Setup MCP configuration
	s.setupMCPConfig()

	// Initialize analytics components
	s.setupAnalyticsComponents()

	// Initialize WebSocket hub
	s.setupWebSocketHub(configManager)

	// Initialize process manager
	s.setupProcessManager()

	// Initialize site generator

	// Initialize HTTP handlers
	s.setupHandlers(configManager)

	// Setup API routes
	s.setupRoutes()

	// Serve static files
	s.ServeStaticFiles()

	// Sessions stopped by the previous shutdown: continue or offer them.
	s.setupResumeAfterRestart()
	if s.agentHandler != nil && s.agentHandler.SessionManager != nil {
		// Sessions that waited for the usage limit before the restart.
		s.agentHandler.SessionManager.StartUsageLimitWaits()
	}

	return nil
}

// setupRoutes configures all API endpoints in an organized manner
func (s *Server) setupRoutes() {
	// Note: App proxy middleware is registered in setupMiddleware() before auth
	// so deployed apps are publicly accessible without login.

	api := s.app.Group("/api")

	// Register route groups in order using routes package
	routes.RegisterHealth(api, s.healthHandler)
	routes.RegisterPerf(api, s.perfHandler)
	routes.RegisterChangelog(api, handlers.NewChangelogHandler())
	routes.RegisterAnalytics(api, s.analyticsHandler)
	routes.RegisterConfig(api, s.configHandler)
	routes.RegisterSettings(api, s.settingsHandler)
	routes.RegisterSandbox(api, s.sandboxHandler)
	routes.RegisterAgentRuntimes(api, s.agentRuntimes)
	routes.RegisterProject(api, s.projectHandler)
	routes.RegisterAvatar(api, s.avatarHandler)
	routes.RegisterProvider(api, s.providerHandler)
	if s.n0Handler != nil {
		s.n0Handler.Register(api) // before the generic /connectors/:slug routes
	}
	routes.RegisterConnector(api, s.connectorHandler)
	routes.RegisterGit(api, s.gitHandler)
	routes.RegisterWorktree(api, s.worktreeHandler)
	routes.RegisterMCPConfig(api, s.mcpConfigHandler)
	routes.RegisterTTS(api, s.ttsHandler)
	routes.RegisterFileBrowser(api, s.fileBrowserHandler)
	routes.RegisterJustfile(api, s.justfileHandler)
	routes.RegisterModelCache(api, s.modelCacheHandler)
	routes.RegisterSkills(api, s.skillsHandler)
	routes.RegisterHooks(api, s.hooksHandler)
	routes.RegisterPacks(api, s.packsHandler)
	routes.RegisterTunnel(api, s.tunnelHandler)
	routes.RegisterMachines(api, handlers.NewMachinesHandler(s.pairedMachines))
	routes.RegisterApps(api, s.appsHandler)
	routes.RegisterRTK(api, s.rtkHandler)
	routes.RegisterRefero(api, s.referoHandler)
	routes.RegisterMemory(api, s.memoryHandler)
	routes.RegisterReview(api, s.reviewHandler)
	routes.RegisterLicense(api, s.licenseHandler)

	// Agent routes (needs app and agentHandler for WebSocket)
	routes.RegisterAgent(api, s.app, s.agentSessionHandler, s.agentHandler, s.config.CORS.AllowedOrigins)

	// Terminal routes (needs app and terminalHandler for WebSocket)
	routes.RegisterTerminal(api, s.app, s.terminalHandlerAPI, s.terminalHandler, s.config.CORS.AllowedOrigins, s.validateWebSocketToken)

	// Auth routes (needs auth handlers struct)
	authHandlers := &routes.AuthHandlers{
		HandleLogin:               s.authHandler.HandleLogin,
		HandleLogout:              s.authHandler.HandleLogout,
		HandleAuthStatus:          s.authHandler.HandleAuthStatus,
		HandleChangePassword:      s.authHandler.HandleChangePassword,
		HandleCreateUser:          s.authHandler.HandleCreateUser,
		HandleListUsers:           s.authHandler.HandleListUsers,
		HandleUpdateUser:          s.authHandler.HandleUpdateUser,
		HandleDeleteUser:          s.authHandler.HandleDeleteUser,
		HandleInitialSetup:        s.handleInitialSetup,
		HandleCreatePairing:       s.handleCreatePairing,
		HandlePairingStatus:       s.handlePairingStatus,
		HandleCancelPairing:       s.handleCancelPairing,
		HandleRedeemPairing:       s.handleRedeemPairing,
		HandleListSessions:        s.handleListSessions,
		HandleRevokeSession:       s.handleRevokeSession,
		HandleMFASetupStart:       s.handleMFASetupStart,
		HandleMFASetupVerify:      s.handleMFASetupVerify,
		HandleMFAVerify:           s.handleMFAVerify,
		HandleMFAStatus:           s.handleMFAStatus,
		HandleMFADisable:          s.handleMFADisable,
		HandleMFABackupCodesRegen: s.handleMFABackupCodesRegenerate,
		HandleMFAAuditLog:         s.handleMFAAuditLog,
		HandleOAuthLogin:          s.handleOAuthLogin,
		HandleOAuthCallback:       s.handleOAuthCallback,
	}
	authConfig := routes.AuthConfig{
		UserAuthEnabled: s.config.Auth.UserAuthEnabled,
		OAuthEnabled:    s.config.Auth.OAuth != nil && s.config.Auth.OAuth.Enabled,
	}
	routes.RegisterAuth(api, authConfig, authHandlers, s.userProfileHandler)

	// WebSocket endpoint (not under /api)
	routes.RegisterWebSocket(s.app, s.wsHub, s.config.CORS.AllowedOrigins)
}

// Start starts the HTTP/HTTPS server
func (s *Server) Start() error {
	// Determine protocol and address
	protocol := "http"
	if s.tlsConfig != nil && s.tlsConfig.Enabled {
		protocol = "https"
	}

	// Bind address - use from config or default to 127.0.0.1
	bindHost := "127.0.0.1"
	if s.config != nil && s.config.Server.Host != "" {
		bindHost = s.config.Server.Host
	}
	addr := fmt.Sprintf("%s:%d", bindHost, s.port)

	if !s.quiet {
		fmt.Printf("🚀 Starting server on %s://%s\n", protocol, addr)
		fmt.Printf("📊 Analytics dashboard: %s://localhost:%d/\n", protocol, s.port)
		fmt.Printf("🔗 API endpoint: %s://localhost:%d/api/data\n", protocol, s.port)

		if s.tlsConfig != nil && s.tlsConfig.Enabled {
			if s.tlsConfig.IsTrusted {
				fmt.Printf("🔒 TLS enabled (%s - trusted certificate)\n", s.tlsConfig.CertType)
				fmt.Printf("✓ PWA installation ready (no browser warnings)\n")
			} else {
				fmt.Printf("🔒 TLS enabled (%s - browser warnings expected)\n", s.tlsConfig.CertType)
				fmt.Printf("ℹ For trusted certificates: brew install mkcert && mkcert -install && eggify cert --regenerate\n")
			}
		}

		if s.authMiddleware != nil {
			configManager := NewConfigManager(s.claudeDir)
			fmt.Printf("🔑 Authentication enabled (API key in %s)\n", configManager.GetSecretPath())
		}
	}

	// Remote access (ngrok) runs as an extra listener next to this one.
	if err := s.autostartRemoteAccess(); err != nil {
		return err
	}

	// Start server with TLS if enabled
	if s.tlsConfig != nil && s.tlsConfig.Enabled {
		return s.app.ListenTLS(addr, s.tlsConfig.CertPath, s.tlsConfig.KeyPath)
	}

	// Start without TLS
	return s.app.Listen(addr)
}

// IsQuiet returns whether the server is in quiet mode.
func (s *Server) IsQuiet() bool {
	return s.quiet
}

// Shutdown gracefully shuts down the server and all its components.
// It stops the file watcher, WebSocket hub, and closes the database.
func (s *Server) Shutdown() error {
	if !s.quiet {
		fmt.Println("🛑 Shutting down server...")
	}

	// Before anything stops: remember the sessions that are working, so the
	// next start can continue them (after an update) or offer to.
	s.writeResumeSnapshot()

	if s.perfStop != nil {
		s.perfStop()
	}
	if s.licenseStop != nil {
		s.licenseStop()
	}

	// Stop remote access first: the public listener goes away before the
	// rest of the server does, and the ngrok agent session is closed right
	// away (a session left open counts against the account's agent session
	// limit until it times out, and a restarted Eggify would need another).
	// Stop is unconditional: it is a no-op when nothing runs, and it also
	// ends a run that is still connecting.
	if s.remoteAccess != nil {
		wasRunning := s.remoteAccess.Status().Running
		_ = s.remoteAccess.Stop()
		if wasRunning && !s.quiet {
			fmt.Println("✅ Remote access stopped")
		}
	}

	// Stop session manager cleanup job
	if s.agentHandler != nil && s.agentHandler.SessionManager != nil {
		s.agentHandler.SessionManager.StopCleanupJob()
		s.agentHandler.SessionManager.StopUsageLimitWaits()
	}

	// Stop the n0 loopback proxy/callback listener
	if s.n0Loopback != nil {
		_ = s.n0Loopback.Close()
	}

	// Stop agent CLI login flows started from the onboarding wizard
	s.agentRuntimes.Close()

	// Stop the provider proxy
	if s.providerProxy != nil {
		_ = s.providerProxy.Close()
	}

	// Close all terminal connections
	if s.terminalHandler != nil {
		if err := s.terminalHandler.CloseAll(); err != nil && !s.quiet {
			fmt.Printf("⚠️  Error closing terminal connections: %v\n", err)
		}
	}

	// Shutdown PTY manager (closes all PTY files and kills processes)
	if s.terminalManager != nil {
		if err := s.terminalManager.Shutdown(); err != nil && !s.quiet {
			fmt.Printf("⚠️  Error shutting down PTY manager: %v\n", err)
		}
	}

	// Cleanup agent sessions
	if s.agentHandler != nil {
		if err := s.agentHandler.Cleanup(); err != nil && !s.quiet {
			fmt.Printf("⚠️  Error cleaning up agent sessions: %v\n", err)
		}
	}

	// Stop file watcher
	if s.fileWatcher != nil {
		if err := s.fileWatcher.Stop(); err != nil && !s.quiet {
			fmt.Printf("⚠️  Error stopping file watcher: %v\n", err)
		}
	}

	// Stop Process Manager before WebSocket hub shutdown
	if s.processManager != nil {
		s.processManager.Stop()
		if !s.quiet {
			fmt.Println("✅ Process Manager stopped")
		}
	}

	// Shutdown WebSocket hub
	if s.wsHub != nil {
		if err := s.wsHub.Shutdown(); err != nil && !s.quiet {
			fmt.Printf("⚠️  Error shutting down WebSocket hub: %v\n", err)
		}
	}

	// Close database
	if s.db != nil {
		if err := s.db.Close(); err != nil && !s.quiet {
			fmt.Printf("⚠️  Error closing database: %v\n", err)
		}
	}

	// Shutdown Fiber app with timeout
	// Create a channel to track Fiber shutdown completion
	fiberDone := make(chan error, 1)
	go func() {
		fiberDone <- s.app.Shutdown()
	}()

	// Wait for Fiber to shutdown with a timeout (5 seconds)
	select {
	case err := <-fiberDone:
		return err
	case <-time.After(5 * time.Second):
		if !s.quiet {
			fmt.Println("⚠️  Fiber shutdown timeout - forcing exit")
		}
		return nil // Force exit even if Fiber doesn't shutdown cleanly
	}
}

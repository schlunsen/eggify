package server

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/n0"
	"github.com/schlunsen/eggify/internal/server/agents"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/routes"
	"github.com/schlunsen/eggify/internal/server/terminal"
	ws "github.com/schlunsen/eggify/internal/websocket"
)

// setupWebSocketHub initializes the WebSocket hub for real-time updates
func (s *Server) setupWebSocketHub(configManager *ConfigManager) {
	// Initialize WebSocket hub
	s.wsHub = ws.NewHub()
	go s.wsHub.Run()

	// Enable WebSocket authentication if any auth middleware is active
	// (API key auth OR user/session auth — both need the API key for WS auth)
	if s.authMiddleware != nil || s.sessionAuthMiddleware != nil {
		// We need to pass the API key to the WebSocket hub
		// Get the API key from the config manager
		apiKeyString, err := configManager.GetAPIKey()
		if err == nil && apiKeyString != "" {
			s.wsHub.SetAPIKey(apiKeyString)
			logging.Info("WebSocket authentication enabled")
		}
		// A signed-in user's session also works, and it is the only thing
		// accepted over remote access (the API key is refused there).
		if s.sessionAuthMiddleware != nil && s.userStore != nil {
			store := s.userStore
			s.wsHub.SetSessionValidator(func(tok string) bool {
				return store.userForSessionToken(tok) != nil
			})
		}
	}

	// Set analytics hub on agent handler for broadcasting tool use events to ActivityHistory
	s.agentHandler.SetAnalyticsHub(s.wsHub)
}

// setupProcessManager initializes the process manager for tracking sessions and terminals
func (s *Server) setupProcessManager() {
	var terminalManagerIface agents.TerminalManagerInterface
	if s.terminalManager != nil {
		terminalAdapter := terminal.NewPTYManagerAdapter(s.terminalManager)
		terminalManagerIface = terminalAdapter
	}

	s.processManager = agents.NewProcessManager(
		s.agentHandler.SessionManager,
		terminalManagerIface,
		s.agentHandler.BroadcastToAll,
	)
	s.processManager.Start()
	logging.Info("Process Manager initialized (broadcast interval: 5s)")
}

// setupTunnel prepares for remote access configured in config.json / env
// (CLI). The manager itself is created in setupRemoteAccess, once the
// encryption key and user store exist. A configured tunnel domain is added to
// the CORS allowed origins (same-origin requests over the tunnel work without
// it; this covers cross-origin clients such as a separately served frontend).
func (s *Server) setupTunnel() {
	if s.config.Tunnel.Domain != "" {
		tunnelOrigin := "https://" + s.config.Tunnel.Domain
		s.config.CORS.AllowedOrigins = append(s.config.CORS.AllowedOrigins, tunnelOrigin)
	}
}

// setupMiddleware configures CORS and authentication middleware
func (s *Server) setupMiddleware() error {
	s.setupContentSecurityPolicy()

	// SECURITY: Add security headers to all responses
	// This prevents clickjacking, MIME sniffing, and other browser-based attacks
	s.app.Use(func(c *fiber.Ctx) error {
		c.Set("X-Frame-Options", "DENY")
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-XSS-Protection", "0") // Disabled in favor of CSP
		c.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Set("Permissions-Policy", "camera=(), microphone=(self), geolocation=()")
		// Strict CSP (inline scripts allowed by hash only); see csp.go.
		// EGGIFY_CSP=report-only|off is the escape hatch.
		s.csp.apply(c)
		// Set HSTS over HTTPS (TLS listener or the ngrok tunnel; never from a header)
		if s.config.TLS.Enabled || requestIsHTTPS(c) {
			c.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		return c.Next()
	})

	// Configure CORS middleware
	corsConfig := cors.Config{
		AllowOrigins: strings.Join(s.config.CORS.AllowedOrigins, ","),
		AllowMethods: "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders: "Origin,Content-Type,Accept,Authorization",
	}
	s.app.Use(cors.New(corsConfig))

	// Request-by-request logging is only useful in verbose mode.
	if s.verbose && !s.quiet {
		s.app.Use(logger.New())
	}

	// Register pre-auth internal routes (e.g. /internal/apps for MCP tools).
	// These MUST be registered before auth middleware so they're accessible
	// from localhost without authentication. They include their own localhost guard.
	s.appsHandler = handlers.NewAppsHandler()
	if s.db != nil {
		s.appsHandler.SetDatabase(s.db.GetDB())
	}
	routes.RegisterAppsPreAuth(s.app, s.appsHandler)

	// n0 public (https) OAuth callback: the browser coming back from n0 may
	// not carry an Eggify session cookie; the flow is protected by the
	// single-use state and the PKCE verifier.
	if s.n0Handler != nil {
		s.app.Get(n0.CallbackPath, s.n0Handler.HandleCallback)
	}

	// App subdomain proxy: intercepts requests to {app}.{subdomain}.{EGGIFY_APPS_BASE_DOMAIN}
	// and proxies them to the registered internal port. Must be before auth
	// middleware so deployed apps are publicly accessible without login.
	s.app.Use(s.appsHandler.AppProxyMiddleware())

	// Apply authentication middleware globally if enabled
	// Note: Session auth middleware is applied INSTEAD of API key middleware if user auth is enabled
	if s.sessionAuthMiddleware != nil {
		s.app.Use(s.sessionAuthMiddleware.Handler())
	} else if s.authMiddleware != nil {
		s.app.Use(s.authMiddleware.Handler())
	}

	// Licensing: 402 license_required for everything but the activation page's
	// needs while not activated (no-op unless enforced). s.license is set later
	// (setupHandlers); the middleware reads it per request.
	s.app.Use(s.licenseGate)

	return nil
}

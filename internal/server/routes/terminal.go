package routes

import (
	"runtime/debug"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/remoteaccess"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
	"github.com/schlunsen/eggify/internal/server/terminal"
)

// RegisterTerminal configures all terminal-related endpoints
//
// validateToken decides whether the credential presented on the terminal
// WebSocket upgrade is valid. It must not be nil: a nil validator rejects
// every connection.
func RegisterTerminal(api fiber.Router, app *fiber.App, h *handlers.TerminalHandler, terminalHandler *terminal.TerminalHandler, allowedOrigins []string, validateToken func(string) bool) {
	// Terminal endpoints (integrated terminal for agent sessions)
	// The terminal is an interactive shell: only a signed-in user may open one.
	api.Post("/agent/sessions/:id/terminal/start", principal.RequireUser("Opening a terminal"), h.HandleStartTerminal)
	api.Delete("/agent/sessions/:id/terminal/stop", h.HandleStopTerminal)
	api.Get("/agent/sessions/:id/terminal/status", h.HandleTerminalStatus)
	api.Get("/agent/sessions/:id/terminals", h.HandleListTerminals)
	api.Get("/terminals/:id/history", h.HandleGetCommandHistory)
	api.Get("/terminal/stats", h.HandleTerminalStats)

	// Terminal WebSocket endpoint
	var authorize func(*fiber.Ctx, string) bool
	if h != nil {
		authorize = h.AuthorizeAgentSession
	}
	registerTerminalWebSocketRoute(app, terminalHandler, allowedOrigins, validateToken, authorize)
}

// registerTerminalWebSocketRoute registers the terminal WebSocket endpoint
// SECURITY: Validates Origin header to prevent Cross-Site WebSocket Hijacking (CSWSH)
//
// authorize (may be nil) checks that the caller may use the agent session's
// terminals; it writes the error response when it refuses.
func registerTerminalWebSocketRoute(app *fiber.App, terminalHandler *terminal.TerminalHandler, allowedOrigins []string, validateToken func(string) bool, authorize func(*fiber.Ctx, string) bool) {
	// Build allowed origin set for validation (reuse shared helper from agent.go)
	allowedOriginSet := buildAllowedOriginSet(allowedOrigins)

	app.Get("/api/agent/sessions/:id/terminal/ws", func(c *fiber.Ctx) error {
		// SECURITY: Validate Origin header to prevent Cross-Site WebSocket Hijacking (CSWSH)
		// The terminal WebSocket is the most dangerous endpoint — it provides direct shell access.
		origin := c.Get("Origin")
		if origin == "" || !isOriginAllowed(origin, c.Hostname(), allowedOriginSet) {
			logging.Warning("WebSocket terminal: rejected connection with missing/disallowed origin: %s", origin)
			return c.Status(fiber.StatusForbidden).SendString("Forbidden: origin not allowed")
		}

		// SECURITY: Require authentication for terminal WebSocket.
		// Candidates: the session cookie (browser), ?token= and a Bearer
		// header. With user auth enabled the validator accepts only a
		// signed-in user's session token, never the API key (which agents can
		// read), so the dashboard's ?token=<api key> is ignored in favour of
		// its cookie.
		// The token must actually be valid — the upgrade is a GET, so the
		// global auth middleware lets it through when require_login is off.
		candidates := []string{c.Cookies("session_token"), c.Query("token")}
		if authHeader := c.Get("Authorization"); len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			candidates = append(candidates, authHeader[7:])
		}
		authorized := false
		sessionToken := "" // the credential that authorized the upgrade
		if validateToken != nil {
			for _, tok := range candidates {
				if tok != "" && validateToken(tok) {
					authorized = true
					sessionToken = tok
					break
				}
			}
			// Auth fully disabled: the validator accepts an empty token.
			// Never over the public tunnel (remote access needs user auth,
			// and with it the validator takes only a session token).
			if !authorized && !remoteaccess.MarkedViaTunnel(c.Locals(remoteaccess.ViaTunnelLocal)) && validateToken("") {
				authorized = true
			}
		}
		if !authorized {
			logging.Warning("WebSocket terminal: rejected connection without a signed-in user from %s", c.IP())
			return c.Status(fiber.StatusUnauthorized).SendString("The terminal requires a signed-in user")
		}

		// SECURITY: the caller must be allowed to see the agent session, and
		// may only attach to terminals it opened itself (checked in
		// HandleWebSocket with the signed-in username).
		if authorize != nil && !authorize(c, c.Params("id")) {
			return nil
		}
		username := ""
		if p := principal.From(c); p.IsUser() {
			username = p.Username
		}

		return websocket.New(func(wsConn *websocket.Conn) {
			// A panic in a hijacked fasthttp connection goroutine would take
			// the whole process down: log it and close this connection.
			defer func() {
				if r := recover(); r != nil {
					logging.Error("PANIC in terminal WebSocket: %v\n%s", r, debug.Stack())
					_ = wsConn.Close()
				}
			}()
			// SECURITY: signing the session out must also end the shell it
			// opened: revocation closes the socket at once, and the watchdog
			// catches every other way the token stops being valid (password
			// change, user deleted or demoted, expiry).
			if sessionToken != "" {
				defer terminalHandler.TrackSessionSocket(database.HashToken(sessionToken), wsConn)()
				defer watchTerminalCredential(wsConn, sessionToken, validateToken, terminalHandler.CloseSignedOut)()
			}
			sessionID := wsConn.Params("id")
			if terminalHandler != nil {
				terminalHandler.HandleWebSocket(wsConn, sessionID, username)
			} else {
				wsConn.WriteJSON(fiber.Map{
					"error": "Terminal functionality is not enabled",
				})
			}
		})(c)
	})
}

// terminalCredentialCheckInterval is how often an open terminal re-checks the
// credential it was opened with.
var terminalCredentialCheckInterval = 5 * time.Second

// watchTerminalCredential calls closeConn as soon as token no longer passes
// validateToken. The returned func stops the watch.
func watchTerminalCredential(c *websocket.Conn, token string, validateToken func(string) bool, closeConn func(*websocket.Conn)) (stop func()) {
	done := make(chan struct{})
	interval := terminalCredentialCheckInterval
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if !validateToken(token) {
					logging.Warning("WebSocket terminal: closing connection whose session is no longer valid")
					closeConn(c)
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

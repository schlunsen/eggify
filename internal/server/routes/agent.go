package routes

import (
	"net"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/agents"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// User represents an authenticated user (needed for WebSocket auth)
type User struct {
	Username string
}

// RegisterAgent configures all agent session endpoints
func RegisterAgent(api fiber.Router, app *fiber.App, h *handlers.AgentSessionHandler, agentHandler *agents.AgentHandler, allowedOrigins []string) {
	// Agent endpoints (serve agents from project directory)
	api.Get("/agents", h.HandleListAgents)
	api.Get("/agents/:name", h.HandleGetAgentDetail)

	// User info endpoints (for resolving user_id to username and avatar)
	api.Get("/users/info/:user_id", h.HandleGetUserInfo)

	// Agent session endpoints (for persistence)
	api.Get("/agent/sessions", h.HandleGetAgentSessions)
	api.Get("/agent/sessions/:id/messages", h.HandleGetAgentMessages)
	api.Get("/agent/sessions/:id/subagents", h.HandleGetSessionSubagents)
	api.Get("/agent/sessions/:id/workers", h.HandleGetSessionWorkers)
	// Audio/video the session rendered, for the chat's players (read-only; files
	// the session named, inside its folders: internal/sessionmedia).
	// Skills of a session: list (why each applies) and add another project's
	// skills to it. Adding needs a signed-in user (hooks come with a skill).
	api.Get("/agent/sessions/:id/skills", h.HandleGetSessionSkills)
	api.Put("/agent/sessions/:id/skills", principal.RequireUser("Changing a session's skills"), h.HandleSetSessionSkills)
	api.Get("/agent/sessions/:id/media", h.HandleListSessionMedia)
	api.Get("/agent/sessions/:id/media/:mediaId", h.HandleGetSessionMedia)
	api.Get("/agent/sessions/:id/media/:mediaId/poster", h.HandleGetSessionMediaPoster)
	api.Post("/agent/sessions/:id/media/:mediaId/convert", h.HandleConvertSessionMedia)
	// An image the agent shows in its reply (![alt](path)), same folder rules.
	api.Get("/agent/sessions/:id/image", h.HandleGetSessionImage)
	// Subagents of every visible session ("All agents" overview, ⇧⌘S).
	api.Get("/agent/subagents", h.HandleListAllSubagents)
	api.Post("/agent/overview/digests", h.HandleOverviewDigests)
	api.Get("/agent/sessions/:id/context", h.HandleGetSessionContext)
	api.Get("/agent/sessions/:id/context-usage", h.HandleGetContextUsage)
	api.Get("/agent/sessions/:id/git-status", h.HandleGetGitStatus)
	api.Get("/agent/sessions/:id/git-remote", h.HandleGetGitRemote)
	api.Get("/agent/sessions/:id/git/diff", h.HandleGetGitDiff)
	api.Post("/agent/sessions/:id/summarize", h.HandleSummarizeSession)
	api.Delete("/agent/sessions/delete-all-but/:id", h.HandleDeleteAllButThisSession)
	api.Patch("/agent/sessions/:id/avatar", h.HandleUpdateSessionAvatar)
	api.Patch("/agent/sessions/:id/view-mode", h.HandleUpdateSessionViewMode)
	// The user's own session tags (web sidebar, iOS). Not a security setting.
	api.Put("/agent/sessions/:id/tags", h.HandleSetSessionTags)
	// Question policy: whether unanswered agent questions are answered with
	// the recommended option. Reading is open; changing it takes the human out
	// of the loop, so only a signed-in user may (an agent holds the API key).
	api.Get("/agent/question-policy", h.HandleGetQuestionPolicy)
	api.Put("/agent/question-policy", principal.RequireUser(agents.QuestionPolicyAction), h.HandleSetQuestionPolicy)
	api.Put("/agent/sessions/:id/question-policy", principal.RequireUser(agents.QuestionPolicyAction), h.HandleSetSessionQuestionPolicy)
	api.Get("/agent/session-tags", h.HandleListSessionTags)
	// What waits for the user across sessions (desktop notch theatre). Read-only.
	api.Get("/agent/pending", h.HandleListPendingRequests)
	// Session options here are the auto-handoff settings (threshold and the
	// prompt the next session starts with): only a signed-in user may set them.
	api.Patch("/agent/sessions/:id/options", principal.RequireUser("Changing session options"), h.HandleUpdateSessionOptions)
	// Model switches are allowed with the API key unless they set a base URL
	// (checked in the handler).
	api.Patch("/agent/sessions/:id/model", h.HandleChangeSessionModel)
	api.Get("/agent/sessions/:id/model-switches/:messageID/context", h.HandleGetModelSwitchContext)

	// Workspace trust: whether the session folder's own Claude config is loaded.
	// POST/DELETE are human-only (principal.RequireUser).
	api.Get("/agent/sessions/:id/workspace-trust", h.HandleGetWorkspaceTrust)
	api.Post("/agent/sessions/:id/workspace-trust", principal.RequireUser("Trusting a folder"), h.HandleTrustWorkspace)
	api.Delete("/agent/sessions/:id/workspace-trust", principal.RequireUser("Revoking folder trust"), h.HandleRevokeWorkspaceTrust)

	// Session connector endpoints (hot-pluggable connector access)
	changeSessionConnectors := principal.RequireUser("Changing a session's connectors")
	api.Get("/agent/sessions/:id/connectors", h.HandleGetSessionConnectors)
	api.Post("/agent/sessions/:id/connectors/:slug/enable", changeSessionConnectors, h.HandleEnableSessionConnector)
	api.Post("/agent/sessions/:id/connectors/:slug/disable", changeSessionConnectors, h.HandleDisableSessionConnector)

	// Agent session handover endpoints
	api.Post("/agent/sessions/:id/handover", h.HandleCreateHandover)
	api.Post("/agent/handover/accept", principal.RequireUser("Accepting a handover"), h.HandleAcceptHandover)

	// Sessions interrupted by a restart: list (and how many work now), continue
	// or dismiss them. Continuing sends prompts, so it is human-only; it acts
	// only on sessions the caller may see, so any signed-in user may.
	api.Get("/agent/resume-after-restart", h.HandleGetResumeAfterRestart)
	api.Post("/agent/resume-after-restart", principal.RequireSignedIn(handlers.ResumeAfterRestartAction), h.HandleResumeAfterRestart)
	api.Delete("/agent/resume-after-restart", principal.RequireSignedIn(handlers.DismissResumeAfterRestartAction), h.HandleDismissResumeAfterRestart)

	// Usage limit: the account's state, and continuing or cancelling a
	// session's wait for the reset (sends or stops a prompt: signed-in user).
	api.Get("/agent/usage-limit", h.HandleGetUsageLimit)
	api.Post("/agent/sessions/:id/limit-wait/resume", principal.RequireSignedIn(handlers.ResumeLimitWaitAction), h.HandleResumeLimitWait)
	api.Delete("/agent/sessions/:id/limit-wait", principal.RequireSignedIn(handlers.CancelLimitWaitAction), h.HandleCancelLimitWait)

	// Which agent tools always need the user's approval (read-only listing)
	api.Get("/agent/tool-confirmation-rules", h.HandleToolConfirmationRules)

	// Directory validation endpoint (GET to allow unauthenticated access from browser)
	api.Get("/agent/validate-directory", h.HandleValidateDirectory)

	// Agent WebSocket endpoint (direct, not proxied)
	// Use custom handler to extract authenticated user BEFORE WebSocket upgrade
	registerAgentWebSocketRoute(app, agentHandler, allowedOrigins)
}

// registerAgentWebSocketRoute registers the agent WebSocket endpoint with security
func registerAgentWebSocketRoute(app *fiber.App, agentHandler *agents.AgentHandler, allowedOrigins []string) {
	// Build a set of allowed origin hosts for fast lookup
	allowedOriginSet := buildAllowedOriginSet(allowedOrigins)

	app.Get("/agent/ws", func(c *fiber.Ctx) error {
		// SECURITY: Validate Origin header to prevent Cross-Site WebSocket Hijacking (CSWSH)
		origin := c.Get("Origin")
		if origin != "" && !isOriginAllowed(origin, c.Hostname(), allowedOriginSet) {
			logging.Warning("WebSocket /agent/ws: rejected connection from disallowed origin: %s", origin)
			return c.Status(fiber.StatusForbidden).SendString("Forbidden: origin not allowed")
		}

		// SECURITY: Extract authenticated user from Fiber context BEFORE WebSocket upgrade
		// This prevents user spoofing after the connection is established
		var authenticatedUser *string

		// Try to get username from SessionAuthMiddleware (user authentication)
		if username := c.Locals("username"); username != nil {
			if userStr, ok := username.(string); ok {
				authenticatedUser = &userStr
				logging.Debug("🔐 WebSocket: Authenticated user extracted from context: %s", userStr)
			}
		}

		// Note: the session middleware identifies the user from the session
		// cookie, an Authorization: Bearer header or, on WebSocket upgrades
		// only, ?token=. A client that sends none of them authenticates with
		// the WebSocket auth message instead.
		// If no username from session auth, try legacy "user" field from SessionAuthMiddleware
		if authenticatedUser == nil {
			if user := c.Locals("user"); user != nil {
				if userObj, ok := user.(*User); ok {
					authenticatedUser = &userObj.Username
					logging.Debug("🔐 WebSocket: Authenticated user extracted from user object: %s", userObj.Username)
				} else if userStr, ok := user.(string); ok {
					authenticatedUser = &userStr
					logging.Debug("🔐 WebSocket: Authenticated user extracted from context: %s", userStr)
				}
			}
		}

		// SECURITY: remember which session token signed this connection in, so
		// it is closed when that session is signed out or revoked. The hash
		// comes from the middleware that identified the user, so it covers
		// every place a token is accepted from (cookie, Bearer, ?token=).
		// A user without a recorded token could never be signed out, so such
		// a connection starts unauthenticated (it can use the auth message).
		tokenHash := principal.SessionTokenHash(c)
		if authenticatedUser != nil && tokenHash == "" {
			logging.Warning("WebSocket /agent/ws: user %s has no recorded session token; not pre-authenticating", *authenticatedUser)
			authenticatedUser = nil
		}
		if authenticatedUser == nil {
			tokenHash = ""
		}

		// Upgrade to WebSocket with user context
		return websocket.New(func(wsConn *websocket.Conn) {
			// The read limit (small until authenticated, then 10MB for
			// base64-encoded images) is set by HandleFiberWebSocket.

			// SECURITY: Set the authenticated user on the WebSocket connection
			// This prevents any spoofing attempts after the initial connection
			agentHandler.SetConnectionSession(wsConn, authenticatedUser, tokenHash)
			if authenticatedUser != nil {
				logging.Info("🔐 WebSocket: User %s connected (message attribution enabled)", *authenticatedUser)
			} else {
				logging.Warning("⚠️ WebSocket: Anonymous connection (no authenticated user)")
			}

			// Call the main WebSocket handler
			agentHandler.HandleFiberWebSocket(wsConn)
		})(c)
	})
}

// buildAllowedOriginSet parses the allowed origins list and returns a set of "host:port" or "host" strings
func buildAllowedOriginSet(allowedOrigins []string) map[string]bool {
	set := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o == "*" {
			set["*"] = true
			continue
		}
		parsed, err := url.Parse(o)
		if err != nil {
			continue
		}
		host := parsed.Hostname()
		port := parsed.Port()
		if port != "" {
			set[host+":"+port] = true
		} else {
			set[host] = true
		}
	}
	return set
}

// isOriginAllowed checks if a WebSocket Origin header is permitted.
// It allows the origin if it matches the Host header or is in the allowed origins set.
// Default ports are ignored on both sides, so "Origin: https://example.com" matches
// "Host: example.com:443" (some clients, e.g. Starscream on iOS, always put the port
// in the Host header, and a proxy such as ngrok passes it through unchanged).
func isOriginAllowed(origin string, requestHost string, allowedOriginSet map[string]bool) bool {
	// Wildcard allows everything
	if allowedOriginSet["*"] {
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}

	originHost := parsed.Hostname()
	originPort := parsed.Port()
	if isDefaultPort(parsed.Scheme, originPort) {
		originPort = ""
	}

	// Allow if Origin matches the request Host header (same-origin)
	reqHost, reqPort := splitHostPort(requestHost)
	if reqPort == "80" || reqPort == "443" {
		reqPort = ""
	}
	if originHost != "" && strings.EqualFold(originHost, reqHost) && originPort == reqPort {
		return true
	}

	// Check against the configured allowed origins set
	if originPort != "" {
		if allowedOriginSet[originHost+":"+originPort] {
			return true
		}
	}
	if allowedOriginSet[originHost] {
		return true
	}

	return false
}

// isDefaultPort reports whether port is the default for scheme (or empty).
func isDefaultPort(scheme, port string) bool {
	switch port {
	case "":
		return true
	case "80":
		return scheme == "http" || scheme == "ws"
	case "443":
		return scheme == "https" || scheme == "wss"
	}
	return false
}

// splitHostPort splits a Host header value ("host", "host:port", "[::1]:port").
func splitHostPort(hostport string) (string, string) {
	if host, port, err := net.SplitHostPort(hostport); err == nil {
		return host, port
	}
	return strings.TrimSuffix(strings.TrimPrefix(hostport, "["), "]"), ""
}

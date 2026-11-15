package server

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// LoginRequest represents a login request
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse represents a login response
// SECURITY: Token is deliberately excluded - session tokens are delivered via HttpOnly cookies only
type LoginResponse struct {
	Username         string    `json:"username"`
	ExpiresAt        time.Time `json:"expires_at,omitempty"`
	MFARequired      bool      `json:"mfa_required,omitempty"`
	TemporaryToken   string    `json:"temporary_token,omitempty"`
	TemporaryExpires time.Time `json:"temporary_expires,omitempty"`
}

// ChangePasswordRequest represents a password change request
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// CreateUserRequest represents a user creation request
type CreateUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"is_admin"`
}

// SetupRequest represents an initial setup request
type SetupRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// SetupResponse represents an initial setup response
// SECURITY: Token is deliberately excluded - session tokens are delivered via HttpOnly cookies only
type SetupResponse struct {
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
	Message   string    `json:"message"`
}

// SessionAuthMiddleware creates a middleware for session authentication
type SessionAuthMiddleware struct {
	userStore    *UserStore
	enabled      bool
	requireLogin bool
}

// NewSessionAuthMiddleware creates a new session authentication middleware
func NewSessionAuthMiddleware(userStore *UserStore, enabled bool, requireLogin bool) *SessionAuthMiddleware {
	return &SessionAuthMiddleware{
		userStore:    userStore,
		enabled:      enabled,
		requireLogin: requireLogin,
	}
}

// handleUnauthorized handles unauthorized requests by either redirecting (for browser requests)
// or returning 401 JSON (for API requests)
func (sam *SessionAuthMiddleware) handleUnauthorized(c *fiber.Ctx) error {
	// Check if this is a browser request
	accept := c.Get("Accept", "")
	isBrowserRequest := strings.Contains(accept, "text/html")

	if isBrowserRequest {
		// Redirect to home page
		return c.Redirect("/", fiber.StatusSeeOther) // 303 See Other
	}

	// For API requests, return 401 JSON
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"error": "Authentication required",
	})
}

// Handler returns the Fiber middleware handler
func (sam *SessionAuthMiddleware) Handler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		path := c.Path()
		// SECURITY: every decision below uses the normalised path (lower
		// case, cleaned); exemptions apply only to a path that was already
		// clean. See authPath.
		p, clean := authPath(path)

		// Skip if authentication is disabled
		if !sam.enabled {
			return c.Next()
		}

		// Identify a signed-in user on every request, including the routes
		// below that pass without auth (WebSocket upgrades and the SPA shell
		// when require_login is off), so privileged WebSocket actions can tell
		// a user from an API-key-only caller.
		sam.identifyUser(c)

		// SECURITY: Setup endpoint is only allowed when no users exist
		if p == "/api/auth/setup" {
			if sam.userStore.HasUsers() {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"error": "Setup already completed. Users already exist.",
				})
			}
			if clean {
				return c.Next()
			}
		}

		// Licensing: before the first account exists, the activation page
		// (shown before /setup) reads the status and activates without a
		// session. See license.go.
		if clean && licensePreAuthPath(p, c.Method()) && !sam.userStore.HasUsers() {
			return c.Next()
		}

		// Always allow other auth endpoints
		if clean && (p == "/api/auth/login" || p == "/api/auth/status" || p == "/api/auth/logout" ||
			p == "/api/auth/mfa/verify" || p == "/api/auth/oauth/authorize" ||
			p == "/api/auth/pairing/redeem" || // the pairing code is the credential
			p == "/api/auth/oauth/callback" || p == "/api/auth/oauth/callback/google") {
			return c.Next()
		}

		// Always allow health, version, and service worker endpoints
		if clean && (p == "/api/health" || p == "/api/version" ||
			p == "/sw.js" || strings.HasPrefix(p, "/workbox-")) {
			return c.Next()
		}

		method := c.Method()

		// If require_login is false, the SPA shell and its client-side routes
		// load without a session (the frontend then asks for a login itself).
		// SECURITY: API reads are NOT exempt. They return transcripts, file
		// contents, shell history, settings and more, so every /api/ GET needs
		// a session or the API key. The public API endpoints are the explicit
		// exemptions above (auth status/login, health, version, OAuth).
		// The /ws and /agent/ws sockets authenticate themselves.
		if !sam.requireLogin && (method == "GET" || method == "OPTIONS") {
			if clean && !isAPIPath(p) {
				return c.Next()
			}
			// Fall through to the auth check for API reads
		}

		// If require_login is true, only allow static assets without auth
		if sam.requireLogin && (method == "GET" || method == "OPTIONS") {
			// Allow static assets only
			if clean && (p == "/" || strings.HasPrefix(p, "/assets/") ||
				strings.HasPrefix(p, "/_nuxt/") || p == "/favicon.ico" || p == "/favicon.svg") {
				return c.Next()
			}
			// All other GET requests require auth when require_login is true
		}

		// Get session token from cookie or Authorization header
		token := c.Cookies("session_token")
		authHeader := c.Get("Authorization")
		if token == "" && authHeader != "" && len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			token = authHeader[7:]
		}
		// WebSocket upgrades cannot set headers; browsers send the cookie, but
		// the terminal socket (and non-browser clients) pass the API key as
		// ?token=. Only honoured on upgrades, so ordinary API URLs never carry
		// credentials.
		if token == "" && websocket.IsWebSocketUpgrade(c) {
			token = c.Query("token")
		}

		if token == "" {
			return sam.handleUnauthorized(c)
		}

		// Try to validate as a session token first. An MFA-pending session is
		// recognised here (ValidateSession alone refuses it) so it can reach
		// the MFA step and nothing else.
		user, session, err := sam.userStore.validateSessionAllowPending(token)
		if err == nil {
			// SECURITY: Check if MFA verification is required but not yet completed
			// This prevents users from bypassing MFA by using a pre-MFA session token
			if session != nil && session.MFARequired && !session.MFAVerified {
				// Only allow MFA verification endpoint and auth status while MFA is pending
				if !clean || (p != "/api/auth/mfa/verify" && p != "/api/auth/status") {
					return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
						"error":        "MFA verification required",
						"mfa_required": true,
					})
				}
			}

			// Valid session token with MFA completed (or not required)
			// Store user in context. A pending session reaching the MFA
			// verify/status endpoints gets no principal.
			if session == nil || !session.MFARequired || session.MFAVerified {
				setUserPrincipal(c, user, token)
			}
			return c.Next()
		}

		// If session validation failed, check if it's an API key
		// API keys can be used for direct API access without user sessions
		isValidAPIKey := sam.userStore.ValidateAPIKey(token)
		if isValidAPIKey {
			if isTunnelRequest(c) {
				return refuseAPIKeyOverTunnel(c)
			}
			// Valid API key - allow access without storing user.
			// SECURITY: the API key is readable by every local process
			// (agents included), so it is an API-key principal: enough for
			// reads and ordinary actions, never for privileged ones.
			c.Locals("authenticated_via_api_key", true)
			if !principal.From(c).IsUser() {
				principal.Set(c, principal.Principal{Kind: principal.APIKey})
			}
			return c.Next()
		}

		// Clear invalid cookie
		c.ClearCookie("session_token")
		return sam.handleUnauthorized(c)
	}
}

// setUserPrincipal records a signed-in user on the request, together with
// the hash of the session token that proved it. WebSocket routes read that
// hash (principal.SessionTokenHash) to tie the socket to the session, so it
// is closed on sign-out whichever way the token arrived.
func setUserPrincipal(c *fiber.Ctx, user *User, token string) {
	c.Locals("user", user)
	c.Locals("username", user.Username)
	principal.Set(c, principal.Principal{Kind: principal.User, Username: user.Username})
	principal.SetSessionTokenHash(c, database.HashToken(token))
}

// sessionTokenCandidates lists the places a session token may arrive, in
// order: the HttpOnly cookie (browser), Authorization: Bearer (iOS REST), and
// ?token= on WebSocket upgrades only.
func sessionTokenCandidates(c *fiber.Ctx) []string {
	var out []string
	if tok := c.Cookies("session_token"); tok != "" {
		out = append(out, tok)
	}
	if h := c.Get("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
		out = append(out, h[7:])
	}
	if websocket.IsWebSocketUpgrade(c) {
		if tok := c.Query("token"); tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

// identifyUser sets the user principal when the request carries a valid,
// MFA-complete session token. It never rejects a request.
func (sam *SessionAuthMiddleware) identifyUser(c *fiber.Ctx) {
	for _, tok := range sessionTokenCandidates(c) {
		if user := sam.userStore.userForSessionToken(tok); user != nil {
			setUserPrincipal(c, user, tok)
			return
		}
	}
}

// userForSessionToken returns the user of a valid session token whose MFA
// step (if any) is complete, or nil.
func (us *UserStore) userForSessionToken(token string) *User {
	if token == "" {
		return nil
	}
	user, err := us.ValidateSession(token)
	if err != nil {
		return nil
	}
	if sess := us.GetSession(token); sess != nil && sess.MFARequired && !sess.MFAVerified {
		return nil
	}
	return user
}

// handleAuthStatus returns the current authentication status
func (s *Server) handleAuthStatus(c *fiber.Ctx) error {
	// Get session token
	token := c.Cookies("session_token")
	if token == "" {
		authHeader := c.Get("Authorization")
		if authHeader != "" && len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			token = authHeader[7:]
		}
	}

	// Check if users exist to determine if initial setup is needed
	hasUsers := s.userStore.HasUsers()
	needsSetup := !hasUsers

	// If setup is needed, enable auth temporarily for the setup flow
	if needsSetup {
		return c.JSON(fiber.Map{
			"enabled":                      true,                 // Auth is enabled during setup
			"authenticated":                false,                // Can't be authenticated during setup
			"requireLogin":                 false,                // Don't require login during setup
			"needs_setup":                  true,                 // Signal that setup is required
			"privileged_actions_protected": principal.Enforced(), // false: API key alone can approve permissions etc.
		})
	}

	// Check if user auth is enabled
	if !s.config.Auth.UserAuthEnabled {
		return c.JSON(fiber.Map{
			"enabled":                      false,
			"authenticated":                false,
			"requireLogin":                 false,
			"needs_setup":                  false,
			"privileged_actions_protected": principal.Enforced(), // false: API key alone can approve permissions etc.
		})
	}

	// Check if OAuth is enabled
	oauthEnabled := s.config.Auth.OAuth != nil && s.config.Auth.OAuth.Enabled
	oauthProviders := make([]string, 0)
	if oauthEnabled && s.config.Auth.OAuth.Providers != nil {
		for providerName, provider := range s.config.Auth.OAuth.Providers {
			if provider.Enabled {
				oauthProviders = append(oauthProviders, providerName)
			}
		}
	}

	// Validate session
	if token != "" {
		user, session, err := s.userStore.validateSessionAllowPending(token)
		if err == nil {

			// Prepare response
			response := fiber.Map{
				"enabled":                      true,
				"authenticated":                true,
				"requireLogin":                 s.config.Auth.RequireLogin,
				"username":                     user.Username,
				"isAdmin":                      user.IsAdmin,
				"oauthEnabled":                 oauthEnabled,
				"oauthProviders":               oauthProviders,
				"needs_setup":                  false,                // Users exist if we're authenticated
				"privileged_actions_protected": principal.Enforced(), // false: API key alone can approve permissions etc.
			}

			// Add MFA status if session exists
			if session != nil {
				response["mfa_required"] = session.MFARequired
				response["mfa_verified"] = session.MFAVerified
				// If MFA required but not verified, include temp token
				if session.MFARequired && !session.MFAVerified {
					response["mfa_temp_token"] = session.MFATempToken
				}
			}

			// Add avatar data if user has an avatar
			if user.AvatarID != nil {
				avatar, err := s.repo.GetAvatarByID(*user.AvatarID)
				if err == nil && avatar != nil {
					response["avatar_id"] = avatar.ID
					response["avatar_name"] = avatar.Name
					response["avatar_color"] = avatar.Color

					// Set avatar image
					if avatar.ImagePath != "" {
						if !strings.HasPrefix(avatar.ImagePath, "http") && !strings.HasPrefix(avatar.ImagePath, "/") {
							response["avatar_image"] = fmt.Sprintf("/api/avatars/%d/image", avatar.ID)
						} else {
							response["avatar_image"] = avatar.ImagePath
						}
					}
				}
			}

			return c.JSON(response)
		}
	}

	return c.JSON(fiber.Map{
		"enabled":                      true,
		"authenticated":                false,
		"requireLogin":                 s.config.Auth.RequireLogin,
		"oauthEnabled":                 oauthEnabled,
		"oauthProviders":               oauthProviders,
		"needs_setup":                  false,                // Users exist if we reach here
		"privileged_actions_protected": principal.Enforced(), // false: API key alone can approve permissions etc.
	})
}

// handleInitialSetup handles initial setup when no users exist
func (s *Server) handleInitialSetup(c *fiber.Ctx) error {
	// SECURITY: the route is public until an account exists. A web page the
	// user visits could post a form to it (no preflight, no cookie needed)
	// and pick the admin password, so only same-origin JSON is accepted.
	if !c.Is("json") || !sameOriginRequest(c) || !s.setupHostAllowed(c) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "Setup must be completed from the Eggify page itself",
		})
	}
	// One setup at a time, so two requests can't both find no users.
	setupMu.Lock()
	defer setupMu.Unlock()

	// SECURITY: Check if users already exist
	if s.userStore.HasUsers() {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "Setup already completed. Users already exist.",
		})
	}

	// Parse request
	var req SetupRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	// Validate username
	if req.Username == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Username is required",
		})
	}

	// Validate password
	if len(req.Password) < 8 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Password must be at least 8 characters",
		})
	}

	// Create the first admin user
	if err := s.userStore.CreateUser(req.Username, req.Password, true); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to create admin user: %v", err),
		})
	}

	// Enable user auth in config if not already enabled
	if !s.config.Auth.UserAuthEnabled {
		configManager := NewConfigManager(s.claudeDir)
		if err := configManager.EnableUserAuth(false); err != nil {
			// Log error but don't fail the setup
			fmt.Printf("Warning: Failed to enable user auth in config: %v\n", err)
		}
		// Update server config in memory
		s.config.Auth.UserAuthEnabled = true
	}

	// Create session token for the new user
	token, err := s.userStore.createUserSession(req.Username, sessionMetaFromRequest(c, "setup"))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "User created but failed to create session",
		})
	}

	// Set session cookie
	c.Cookie(&fiber.Cookie{
		Name:     "session_token",
		Value:    token,
		Path:     "/",
		HTTPOnly: true,
		Secure:   s.config.TLS.Enabled || requestIsHTTPS(c),
		SameSite: "Lax",
		Expires:  time.Now().Add(s.userStore.SessionLifetime()),
	})

	// Return success response (SECURITY: token not included in response body - cookie only)
	expiresAt := time.Now().Add(s.userStore.SessionLifetime())
	return c.JSON(SetupResponse{
		Username:  req.Username,
		ExpiresAt: expiresAt,
		Message:   "Initial setup completed successfully. Welcome to Eggify!",
	})
}

// setupMu serialises the first-run setup.
var setupMu sync.Mutex

// sameOriginRequest reports whether a browser request came from a page on
// this server: an Origin header, when present, must name the request's own
// host (default ports ignored). Requests without one (curl, the iOS app)
// pass; browsers always send Origin on cross-site POSTs.
func sameOriginRequest(c *fiber.Ctx) bool {
	origin := c.Get(fiber.HeaderOrigin)
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false // includes "null"
	}
	return stripDefaultPort(u.Host, u.Scheme) == stripDefaultPort(string(c.Request().Host()), u.Scheme)
}

func stripDefaultPort(host, scheme string) string {
	host = strings.ToLower(host)
	if (scheme == "https" && strings.HasSuffix(host, ":443")) || (scheme == "http" && strings.HasSuffix(host, ":80")) {
		return host[:strings.LastIndex(host, ":")]
	}
	return host
}

// setupHostAllowed refuses DNS rebinding: a page on evil.example whose name
// resolves to 127.0.0.1 sends a matching Origin and Host. A request that came
// in on a loopback listener (the desktop app's plain-http 127.0.0.1:3335)
// must name a loopback host or an IP, or a configured allowed origin.
func (s *Server) setupHostAllowed(c *fiber.Ctx) bool {
	if isTunnelRequest(c) {
		return true
	}
	local, ok := c.Context().LocalAddr().(*net.TCPAddr)
	if !ok || !local.IP.IsLoopback() {
		return true
	}
	host := strings.ToLower(c.Hostname())
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || net.ParseIP(host) != nil {
		return true
	}
	if s.config != nil {
		for _, o := range s.config.CORS.AllowedOrigins {
			if u, err := url.Parse(o); err == nil && strings.EqualFold(u.Hostname(), host) {
				return true
			}
		}
	}
	return false
}

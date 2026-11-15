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
	"github.com/schlunsen/eggify/internal/logging"
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
	// allowedOrigins are the configured extra origins (CORS.AllowedOrigins)
	// a cookie-authenticated state-changing request may come from.
	allowedOrigins []string
}

// SetAllowedOrigins sets the extra origins accepted by the cookie Origin check.
func (sam *SessionAuthMiddleware) SetAllowedOrigins(origins []string) {
	sam.allowedOrigins = origins
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

		// Sign-out is a cookie-authenticated state change as well.
		if p == "/api/auth/logout" && !sam.cookieOriginOK(c, p, clean, sam.authViaCookie(c)) {
			return originRefused(c)
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
		// A valid session token in Authorization: Bearer wins over the cookie
		// (the iOS app keeps the cookie in shared storage and sends both); a
		// cross-site page cannot set that header. Otherwise the cookie is used,
		// and an API key next to it does not change that.
		token := c.Cookies("session_token")
		viaCookie := token != ""
		authHeader := c.Get("Authorization")
		if bearer := bearerToken(authHeader); bearer != "" {
			if token == "" {
				token = bearer
			} else if sam.userStore.userForSessionToken(bearer) != nil {
				token, viaCookie = bearer, false
			}
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
			// SECURITY: a browser attaches the cookie to a cross-site form post
			// (SameSite=Lax still sends it on top-level navigations and to
			// same-site pages such as deployed sandbox apps). A state change
			// authenticated by the cookie must name its own origin.
			if !sam.cookieOriginOK(c, p, clean, viaCookie) {
				return originRefused(c)
			}
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

func originRefused(c *fiber.Ctx) error {
	logging.Warning("SECURITY: refused %s %s: cookie-authenticated request from another origin (Origin %q, Referer %q)",
		c.Method(), c.Path(), c.Get(fiber.HeaderOrigin), c.Get(fiber.HeaderReferer))
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
		"error": "Cross-origin request refused",
		"code":  "origin_not_allowed",
	})
}

// bearerToken returns the token of an "Authorization: Bearer" header, or "".
func bearerToken(h string) string {
	if len(h) > 7 && h[:7] == "Bearer " {
		return h[7:]
	}
	return ""
}

// authViaCookie reports whether a request would be authenticated by the
// session cookie: it has one and no valid session token in a Bearer header.
func (sam *SessionAuthMiddleware) authViaCookie(c *fiber.Ctx) bool {
	if c.Cookies("session_token") == "" {
		return false
	}
	if b := bearerToken(c.Get("Authorization")); b != "" && sam.userStore.userForSessionToken(b) != nil {
		return false
	}
	return true
}

// cookieOriginOK implements the Origin check for cookie-authenticated
// POST/PUT/PATCH/DELETE requests under /api (viaCookie: the cookie, not a
// Bearer session token, authenticated the request). Bearer requests are
// unaffected: a browser never adds that header on its own. The cookie request
// must carry an Origin (else a Referer) naming this server (default ports
// ignored) or a configured allowed origin; neither header, or "null", is
// refused. A path that is not clean (dot segments, "//") fails closed: it is
// checked whatever it cleans to.
func (sam *SessionAuthMiddleware) cookieOriginOK(c *fiber.Ctx, cleanPath string, clean, viaCookie bool) bool {
	switch c.Method() {
	case fiber.MethodPost, fiber.MethodPut, fiber.MethodPatch, fiber.MethodDelete:
	default:
		return true
	}
	if !viaCookie || (clean && !isAPIPath(cleanPath)) {
		return true
	}
	ref := c.Get(fiber.HeaderOrigin)
	if ref == "" {
		ref = c.Get(fiber.HeaderReferer)
	}
	if ref == "" {
		return false
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false // includes "null"
	}
	if stripDefaultPort(u.Host, u.Scheme) == stripDefaultPort(string(c.Request().Host()), u.Scheme) {
		return true
	}
	for _, o := range sam.allowedOrigins {
		if o == "*" {
			return true
		}
		if a, err := url.Parse(o); err == nil && a.Host != "" &&
			stripDefaultPort(a.Host, a.Scheme) == stripDefaultPort(u.Host, u.Scheme) {
			return true
		}
	}
	return false
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
// order: Authorization: Bearer (iOS REST; it wins over a cookie the client
// also holds, as in the Origin check), the HttpOnly cookie (browser), and
// ?token= on WebSocket upgrades only.
func sessionTokenCandidates(c *fiber.Ctx) []string {
	var out []string
	if h := c.Get("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
		out = append(out, h[7:])
	}
	if tok := c.Cookies("session_token"); tok != "" {
		out = append(out, tok)
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

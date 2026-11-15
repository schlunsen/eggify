package routes

import (
	"net"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// AuthHandlers groups all auth-related handler functions
type AuthHandlers struct {
	// AuthHandler methods
	HandleLogin          fiber.Handler
	HandleLogout         fiber.Handler
	HandleAuthStatus     fiber.Handler
	HandleChangePassword fiber.Handler
	HandleCreateUser     fiber.Handler
	HandleListUsers      fiber.Handler
	HandleUpdateUser     fiber.Handler
	HandleDeleteUser     fiber.Handler
	HandleInitialSetup   fiber.Handler // Initial setup endpoint

	// QR-code pairing and signed-in devices (internal/server/pairing.go,
	// user_sessions.go)
	HandleCreatePairing fiber.Handler
	HandlePairingStatus fiber.Handler
	HandleCancelPairing fiber.Handler
	HandleRedeemPairing fiber.Handler
	HandleListSessions  fiber.Handler
	HandleRevokeSession fiber.Handler

	// MFA handler methods (still Server methods until refactored)
	HandleMFASetupStart       fiber.Handler
	HandleMFASetupVerify      fiber.Handler
	HandleMFAVerify           fiber.Handler
	HandleMFAStatus           fiber.Handler
	HandleMFADisable          fiber.Handler
	HandleMFABackupCodesRegen fiber.Handler
	HandleMFAAuditLog         fiber.Handler

	// OAuth handler methods (still Server methods until refactored)
	HandleOAuthLogin    fiber.Handler
	HandleOAuthCallback fiber.Handler
}

// AuthConfig contains auth configuration
type AuthConfig struct {
	UserAuthEnabled bool
	OAuthEnabled    bool
}

// RegisterAuth configures all authentication-related endpoints
func RegisterAuth(api fiber.Router, authConfig AuthConfig, authHandlers *AuthHandlers, userProfileHandler *handlers.UserProfileHandler) {
	auth := api.Group("/auth")

	// Setup endpoint is always available (used for initial user creation)
	// Rate limiter for setup endpoint (10 attempts per hour per IP)
	setupLimiter := limiter.New(limiter.Config{
		Max:          10,
		Expiration:   60 * time.Minute,
		KeyGenerator: clientIPKey,
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error":   "Too many setup attempts",
				"message": "You have exceeded the maximum number of setup attempts. Please try again in 1 hour.",
			})
		},
	})
	auth.Post("/setup", setupLimiter, authHandlers.HandleInitialSetup)

	// Status endpoint is always available (even when auth is disabled)
	// This is needed to check if setup is required
	auth.Get("/status", authHandlers.HandleAuthStatus)

	// Return early if user auth is not enabled
	if !authConfig.UserAuthEnabled {
		return
	}

	// Rate limiter for login endpoint (5 attempts per 15 minutes per IP,
	// IPv6 by /64). The login handler adds a per-username lockout and a
	// global cap on failures (internal/server/login_throttle.go).
	loginLimiter := limiter.New(limiter.Config{
		Max:          5,
		Expiration:   15 * time.Minute,
		KeyGenerator: clientIPKey,
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error":   "Too many login attempts",
				"message": "You have exceeded the maximum number of login attempts. Please try again in 15 minutes.",
			})
		},
	})

	// Rate limiter for password change (3 attempts per 15 minutes per IP)
	passwordLimiter := limiter.New(limiter.Config{
		Max:          3,
		Expiration:   15 * time.Minute,
		KeyGenerator: clientIPKey,
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error":   "Too many password change attempts",
				"message": "You have exceeded the maximum number of password change attempts. Please try again in 15 minutes.",
			})
		},
	})

	// Auth endpoints (using AuthHandler)
	auth.Post("/login", loginLimiter, authHandlers.HandleLogin)
	auth.Post("/logout", authHandlers.HandleLogout)
	auth.Get("/status", authHandlers.HandleAuthStatus)
	auth.Post("/change-password", passwordLimiter, authHandlers.HandleChangePassword)

	// Rate limiter for MFA code checks (per IP, IPv6 by /64). Each
	// temporary token also allows only 5 attempts (counted atomically).
	mfaLimiter := limiter.New(limiter.Config{
		Max:          15,
		Expiration:   15 * time.Minute,
		KeyGenerator: clientIPKey,
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error":   "Too many verification attempts",
				"message": "Too many verification attempts. Please try again in 15 minutes.",
			})
		},
	})

	// MFA endpoints
	auth.Post("/mfa/setup", authHandlers.HandleMFASetupStart)
	auth.Post("/mfa/setup/verify", mfaLimiter, authHandlers.HandleMFASetupVerify)
	auth.Post("/mfa/verify", mfaLimiter, authHandlers.HandleMFAVerify)
	auth.Get("/mfa/status", authHandlers.HandleMFAStatus)
	auth.Post("/mfa/disable", authHandlers.HandleMFADisable)
	auth.Post("/mfa/backup-codes/regenerate", authHandlers.HandleMFABackupCodesRegen)
	auth.Get("/mfa/audit-log", authHandlers.HandleMFAAuditLog)

	// Rate limiter for admin user management (30 attempts per 15 minutes per IP)
	adminLimiter := limiter.New(limiter.Config{
		Max:          30,
		Expiration:   15 * time.Minute,
		KeyGenerator: clientIPKey,
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error":   "Too many requests",
				"message": "You have exceeded the maximum number of admin requests. Please try again in 15 minutes.",
			})
		},
	})

	// Admin-only endpoints (using AuthHandler)
	// User management needs a signed-in (admin) user, never just the API key.
	manageUsers := principal.RequireUser("Managing users")
	auth.Post("/users", adminLimiter, manageUsers, authHandlers.HandleCreateUser)
	auth.Get("/users", adminLimiter, manageUsers, authHandlers.HandleListUsers)
	auth.Patch("/users/:username", adminLimiter, manageUsers, authHandlers.HandleUpdateUser)
	auth.Delete("/users/:username", adminLimiter, manageUsers, authHandlers.HandleDeleteUser)

	registerPairing(auth, authHandlers)

	// OAuth endpoints (if OAuth is enabled)
	if authConfig.OAuthEnabled {
		// Each authorize call stores a state row; limit them per IP.
		oauthLimiter := limiter.New(limiter.Config{
			Max:          20,
			Expiration:   15 * time.Minute,
			KeyGenerator: clientIPKey,
			LimitReached: func(c *fiber.Ctx) error {
				return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
					"error": "Too many sign-in attempts. Please try again in 15 minutes.",
				})
			},
		})
		auth.Get("/oauth/authorize", oauthLimiter, authHandlers.HandleOAuthLogin)
		auth.Post("/oauth/callback", authHandlers.HandleOAuthCallback)
		auth.Get("/oauth/callback/google", authHandlers.HandleOAuthCallback) // Google uses GET for callback
	}

	// User profile endpoints
	auth.Get("/user/profile", userProfileHandler.HandleGetUserProfile)
	auth.Put("/user/profile", userProfileHandler.HandleUpdateUserProfile)
}

// PairingAction and SessionsAction name the pairing/device routes in
// user_required refusals.
const (
	PairingAction  = "Pairing a device"
	SessionsAction = "Managing signed-in devices"
)

// registerPairing adds QR-code pairing and the signed-in devices list.
// Creating a pairing code mints a session for the caller on another device,
// so it needs a signed-in user (never just the API key), like listing and
// revoking sessions. Redeeming is public (the code is the credential) and
// rate limited per IP (the main limit) and, loosely, globally.
func registerPairing(auth fiber.Router, h *AuthHandlers) {
	redeemPerIP := limiter.New(limiter.Config{
		Max:          10,
		Expiration:   15 * time.Minute,
		KeyGenerator: clientIPKey,
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "Too many pairing attempts. Try again in 15 minutes.",
			})
		},
	})
	// The global cap is a backstop against a distributed flood (codes are
	// 32 random bytes, guessing is hopeless). It is high, so one client
	// cannot use it to block pairing for everyone.
	redeemGlobal := limiter.New(limiter.Config{
		Max:          300,
		Expiration:   15 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string { return "pairing-redeem" },
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "Too many pairing attempts. Try again later.",
			})
		},
	})
	auth.Post("/pairing/redeem", redeemPerIP, redeemGlobal, h.HandleRedeemPairing)

	// User-level: any signed-in user may pair their own devices and manage
	// their own signed-in sessions (no admin needed).
	pairing := principal.RequireSignedIn(PairingAction)
	auth.Post("/pairing", pairing, h.HandleCreatePairing)
	auth.Get("/pairing/:id/status", pairing, h.HandlePairingStatus)
	auth.Delete("/pairing/:id", pairing, h.HandleCancelPairing)

	sessions := principal.RequireSignedIn(SessionsAction)
	auth.Get("/sessions", sessions, h.HandleListSessions)
	auth.Delete("/sessions/:id", sessions, h.HandleRevokeSession)
}

// clientIP is the TCP peer address. No proxy header is trusted (server.go),
// so c.IP() is the peer; tunnel connections report the visitor's address.
func clientIP(c *fiber.Ctx) string {
	if ip := c.IP(); ip != "" {
		return ip
	}
	return c.Context().RemoteIP().String()
}

// clientIPKey is the rate-limit key of a request: the IPv4 address, or the
// /64 network of an IPv6 address (one host usually owns a whole /64, so
// per-address keys would give it 2^64 buckets).
func clientIPKey(c *fiber.Ctx) string {
	return IPLimitKey(clientIP(c))
}

// IPLimitKey maps an address to its rate-limit key (see clientIPKey).
func IPLimitKey(addr string) string {
	ip := net.ParseIP(addr)
	if ip == nil {
		return addr
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

package server

import (
	"context"
	"crypto/ed25519"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"

	"github.com/schlunsen/eggify/internal/license"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/agents"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/version"
)

// Licensing (docs/adr/013-licensing.md, docs/licensing-protocol.md).
//
// Only builds with license.Required set (release desktop builds) or
// EGGIFY_LICENSE_REQUIRED=1 enforce it. Then, until the install holds a
// valid license, licenseGate answers every API request and WebSocket upgrade
// with 402 license_required, except what the SPA needs to show the
// activation page and sign in. The agents layer refuses prompts too.
//
// First run: the activation page comes first, before /setup. While no account
// exists, GET /api/license and POST /api/license/activate pass the session
// middleware without a session (like /api/auth/setup) and activation needs no
// user; nothing else works in that window anyway (not activated, no
// account). Then /setup creates the admin. Once an account exists,
// activating and deactivating need a signed-in admin (principal.RequireUser),
// so an agent holding the API key can't change the license.

// licenseFile holds the encrypted license state (install key, token).
func (s *Server) licenseFile() string {
	return filepath.Join(s.claudeDir, "eggify", "license.json")
}

// newLicenseClient is the license server client; tests replace it.
var newLicenseClient = func() *license.Client {
	return &license.Client{BaseURL: license.BaseURLFromEnv(), AppVersion: version.Version}
}

// setupLicense creates the license manager and starts its background
// renewal. key is the connector encryption key.
func (s *Server) setupLicense(key []byte) {
	var store *license.Store
	if st, err := license.NewStore(s.licenseFile(), key); err != nil {
		logging.Error("License store unavailable: %v", err)
	} else {
		store = st
	}
	m := license.NewManager(license.Config{
		Store:  store,
		Client: newLicenseClient(),
		Logf:   func(format string, args ...any) { logging.Info(format, args...) },
	})
	s.license = m
	// A renewed license (or a new relay_until) reconnects a relay that
	// refused this install for its license.
	m.OnChange(func() {
		if ra := s.remoteAccess; ra != nil {
			ra.RetryAfterLicenseChange()
		}
	})
	agents.SetLicenseGate(m.Gate)
	if m.Enforced() {
		logging.Info("License: required by this build; state %s", m.Status().State)
		var ctx context.Context
		ctx, s.licenseStop = context.WithCancel(context.Background())
		go m.Run(ctx)
	}
	hasUsers := func() bool { return s.userStore != nil && s.userStore.HasUsers() }
	s.licenseHandler = handlers.NewLicenseHandler(m, hasUsers)
}

// licenseService is what the server needs of the license manager
// (*license.Manager; a fake in tests).
type licenseService interface {
	Gate() error
	// RelayCredentials is the license token and install key for the Eggify relay.
	RelayCredentials() (string, ed25519.PrivateKey)
}

// licenseRequired reports that licensing is enforced and the install is not
// licensed (the SPA then opens the activation page).
func (s *Server) licenseRequired() bool {
	return s.license != nil && s.license.Gate() != nil
}

// licenseExemptPath reports whether a normalised path (authPath) stays
// reachable while not activated: the license API, sign-in/setup, health and
// version, the API key fetch the SPA authenticates with when user auth is
// off, and everything outside /api except the WebSockets (the SPA shell and
// its assets, so the activation page can load).
func licenseExemptPath(p string) bool {
	switch {
	case p == "/ws" || p == "/agent/ws":
		return false
	case !isAPIPath(p):
		return true
	case p == "/api/license" || strings.HasPrefix(p, "/api/license/"),
		p == "/api/auth" || strings.HasPrefix(p, "/api/auth/"),
		p == "/api/health", p == "/api/version",
		p == "/api/config/api-key":
		return true
	}
	return false
}

// licenseGate is the middleware that refuses everything else with 402
// license_required while the install is not licensed. It runs after the auth
// middleware (unauthenticated callers still get 401) and before every route.
func (s *Server) licenseGate(c *fiber.Ctx) error {
	if s.license == nil {
		return c.Next()
	}
	err := s.license.Gate()
	if err == nil {
		return c.Next()
	}
	p, clean := authPath(c.Path())
	// Exemptions only for a clean path (see authPath); a path with dot
	// segments or "//" is refused rather than guessed at.
	if clean && licenseExemptPath(p) && !websocket.IsWebSocketUpgrade(c) {
		return c.Next()
	}
	if !clean && !isAPIPath(p) && p != "/ws" && p != "/agent/ws" && !websocket.IsWebSocketUpgrade(c) {
		return c.Next() // the SPA fallback for odd page URLs
	}
	return c.Status(fiber.StatusPaymentRequired).JSON(fiber.Map{
		"code":  agents.LicenseRequiredCode,
		"error": err.Error(),
	})
}

// licensePreAuthPath reports the license routes the session middleware lets
// through without a session while no account exists (the activation page
// comes before /setup).
func licensePreAuthPath(p, method string) bool {
	return (p == "/api/license" && method == fiber.MethodGet) ||
		(p == "/api/license/activate" && method == fiber.MethodPost)
}

package routes

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/capability"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterApps registers sandbox app subdomain routes on the api group.
// Only active when EGGIFY_SUBDOMAIN and EGGIFY_APPS_BASE_DOMAIN are set (a hosted sandbox).
func RegisterApps(api fiber.Router, h *handlers.AppsHandler) {
	if !handlers.AppsEnabled() {
		return // Apps not configured, don't register app routes
	}

	// Publishing a local port on a public subdomain is high-impact: through
	// the API a signed-in user does it; agents use deploy_app, which carries
	// a session capability (/internal/apps below).
	changeApps := principal.RequireUser("Publishing apps")
	apps := api.Group("/apps")
	apps.Get("/", h.HandleListApps)
	apps.Post("/", changeApps, h.HandleRegisterApp)
	apps.Delete("/:name", changeApps, h.HandleDeleteApp)
}

// RegisterAppsPreAuth registers apps routes BEFORE auth middleware.
// Called on the raw Fiber app so MCP tools (localhost) can access without auth.
// Only active when EGGIFY_SUBDOMAIN and EGGIFY_APPS_BASE_DOMAIN are set.
//
// SECURITY: These routes are guarded by a localhost-only check. Even though
// they're pre-auth, they reject any request not coming from 127.0.0.1/::1.
// External traffic arrives via Caddy which doesn't proxy /internal/* paths,
// so this is defense-in-depth.
func RegisterAppsPreAuth(app *fiber.App, h *handlers.AppsHandler) {
	if !handlers.AppsEnabled() {
		return
	}

	// Localhost-only guard middleware
	// Uses fasthttp's RemoteAddr directly because Fiber's c.IP() may return
	// empty when the request comes from a trusted proxy (127.0.0.1) without
	// an X-Forwarded-For header.
	localhostOnly := func(c *fiber.Ctx) error {
		addr := c.Context().RemoteAddr().String()
		// addr is "ip:port" — extract the IP part
		ip := addr
		if idx := strings.LastIndex(addr, ":"); idx != -1 {
			ip = addr[:idx]
		}
		if ip != "127.0.0.1" && ip != "::1" && !strings.HasPrefix(ip, "127.") {
			return c.Status(403).JSON(fiber.Map{"error": "internal routes are localhost-only"})
		}
		return c.Next()
	}

	app.Get("/internal/apps", localhostOnly, requireAppsCapability, h.HandleListApps)
	app.Post("/internal/apps", localhostOnly, requireAppsCapability, h.HandleRegisterApp)
	app.Delete("/internal/apps/:name", localhostOnly, requireAppsCapability, h.HandleDeleteApp)
}

// requireAppsCapability admits only callers holding a session's apps
// capability (the eggify-tools process of an agent session, see
// capability.Token). Being on localhost is not enough: every local process,
// including anything an agent starts, is on localhost. Users manage apps
// through the authenticated /api/apps routes instead.
func requireAppsCapability(c *fiber.Ctx) error {
	sid := c.Get(capability.HeaderSessionID)
	auth := c.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	if !strings.HasPrefix(auth, "Bearer ") || !capability.Verify(capability.Apps, sid, token) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "a session apps capability is required"})
	}
	return c.Next()
}

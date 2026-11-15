package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterTunnel configures the remote access (ngrok tunnel) endpoints.
// Reading the status is open to any authenticated caller (it never contains
// the token); everything that changes remote access needs a signed-in admin
// (principal.RequireAdmin; with one account that user is the admin), never
// just the API key and never a non-admin user: a public URL to this machine,
// the ngrok authtoken and the ngrok account's agents belong to its owner.
func RegisterTunnel(api fiber.Router, h *handlers.TunnelHandler) {
	if h == nil {
		h = handlers.NewTunnelHandler(nil)
	}
	tunnel := api.Group("/tunnel")
	tunnel.Get("/status", h.HandleGetStatus)
	requireAdmin := principal.RequireAdmin(handlers.RemoteAccessAction)
	tunnel.Put("/settings", requireAdmin, h.HandleUpdateSettings)
	tunnel.Post("/start", requireAdmin, h.HandleStart)
	tunnel.Post("/stop", requireAdmin, h.HandleStop)
	tunnel.Delete("/token", requireAdmin, h.HandleDeleteToken)
	// Optional ngrok API key: see and stop the account's agent sessions.
	// Listing needs an admin too: it shows IPs and hosts of the user's agents.
	tunnel.Put("/api-key", requireAdmin, h.HandleSetAPIKey)
	tunnel.Delete("/api-key", requireAdmin, h.HandleDeleteAPIKey)
	tunnel.Get("/sessions", requireAdmin, h.HandleListAgentSessions)
	tunnel.Post("/sessions/:id/stop", requireAdmin, h.HandleStopAgentSession)
}

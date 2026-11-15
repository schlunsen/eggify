package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterAnalytics configures analytics and command history endpoints
func RegisterAnalytics(api fiber.Router, h *handlers.AnalyticsHandler) {
	// Host-wide reads: every conversation, process and command on the
	// machine. The API key (hooks, desktop app) and admins read them; a
	// signed-in non-admin gets an empty 200 (never a 403, which the dashboard
	// would toast on every home page visit). /stats and the history lists
	// filter to the caller's visible sessions in the handler.
	empty := principal.EmptyForRestricted
	// Destructive routes need a signed-in admin (or the only account).
	destroy := principal.RequireUser("Clearing analytics data")

	api.Get("/data", empty(fiber.Map{"conversations": []any{}, "activeProcessCount": 0, "claudeDir": ""}), h.HandleGetData)
	api.Get("/processes", empty(fiber.Map{"processes": []any{}, "stats": fiber.Map{}}), h.HandleGetProcesses)
	api.Get("/shells", empty(fiber.Map{"shells": []any{}, "stats": fiber.Map{}}), h.HandleGetShells)
	api.Get("/stats", h.HandleGetStats)

	// Refresh endpoint
	api.Post("/refresh", h.HandleRefresh)

	// Reset endpoints
	api.Post("/reset/archive", destroy, h.HandleResetArchive)
	api.Post("/reset/clear", destroy, h.HandleResetClear)
	api.Post("/reset/soft", destroy, h.HandleResetSoft)
	api.Delete("/reset", destroy, h.HandleClearReset)
	api.Get("/reset/status", empty(fiber.Map{"active": false}), h.HandleResetStatus)

	// Command history endpoints
	api.Get("/history/all", h.HandleGetAllHistory)
	api.Get("/history/shell", h.HandleGetShellHistory)
	api.Get("/history/claude", h.HandleGetClaudeHistory)
	api.Get("/history/stats", empty(fiber.Map{"stats": []any{}, "count": 0}), h.HandleGetCommandStats)
	api.Post("/commands/shell", h.HandleRecordShellCommand)
	api.Post("/commands/claude", h.HandleRecordClaudeCommand)
	api.Delete("/history", destroy, h.HandleClearAllHistory)
	api.Get("/db/stats", empty(fiber.Map{"stats": fiber.Map{}, "db_path": ""}), h.HandleGetDBStats)
}

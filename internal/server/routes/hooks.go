package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterHooks configures hook management and execution log endpoints
func RegisterHooks(api fiber.Router, h *handlers.HooksHandler) {
	hooksGroup := api.Group("/hooks")

	// Hook configuration endpoints. A hook is a shell command Claude Code runs
	// on every matching event, so adding or removing one needs a signed-in user.
	changeHooks := principal.RequireUser("Changing hooks")
	hooksGroup.Get("/", h.HandleGetHooks)
	hooksGroup.Get("/events", h.HandleGetHookEvents)
	hooksGroup.Get("/event/:event", h.HandleGetHooksByEvent)
	hooksGroup.Post("/", changeHooks, h.HandleCreateHook)
	hooksGroup.Delete("/", changeHooks, h.HandleDeleteHook)
	hooksGroup.Post("/validate", h.HandleValidateHook)

	// Hook execution log endpoints (hook scripts report here with the API key)
	hooksGroup.Get("/executions", h.HandleGetExecutions)
	hooksGroup.Get("/executions/stats", h.HandleGetExecutionStats)
	hooksGroup.Get("/executions/:id", h.HandleGetExecution)
	hooksGroup.Post("/executions", h.HandleRecordExecution)
}

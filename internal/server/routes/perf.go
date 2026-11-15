package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
)

// RegisterPerf configures the performance monitor endpoints.
func RegisterPerf(api fiber.Router, h *handlers.PerfHandler) {
	api.Get("/perf", h.HandleGetPerf)
	api.Post("/perf/client", h.HandlePostClient)
	// CPU and memory for the meter next to the machine chip (read-only).
	api.Get("/system/usage", h.HandleGetUsage)
}

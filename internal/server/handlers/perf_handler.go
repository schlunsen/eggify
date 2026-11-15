package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/perfmon"
)

// PerfHandler serves the performance monitor (see internal/perfmon).
type PerfHandler struct {
	sampler *perfmon.Sampler
}

// NewPerfHandler creates the handler.
func NewPerfHandler(s *perfmon.Sampler) *PerfHandler { return &PerfHandler{sampler: s} }

// HandleGetPerf returns the sample history and the current findings.
func (h *PerfHandler) HandleGetPerf(c *fiber.Ctx) error {
	return c.JSON(h.sampler.Report())
}

// HandleGetUsage returns this machine's CPU and memory now, and Eggify's share
// (perfmon.ReadUsage, reused for 3 s).
func (h *PerfHandler) HandleGetUsage(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	return c.JSON(perfmon.ReadUsage())
}

// HandlePostClient stores one dashboard sample. The numbers are clamped, since
// the page is untrusted input to a memory-bounded buffer.
func (h *PerfHandler) HandlePostClient(c *fiber.Ctx) error {
	var in perfmon.ClientSample
	if err := c.BodyParser(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid body"})
	}
	in.Time = time.Now()
	in.DOMNodes = clampInt(in.DOMNodes, 0, 10_000_000)
	in.Animations = clampInt(in.Animations, 0, 1_000_000)
	in.Messages = clampInt(in.Messages, 0, 100_000_000)
	in.Sessions = clampInt(in.Sessions, 0, 1_000_000)
	in.FPS = clampFloat(in.FPS, 0, 1000)
	in.JSHeapMB = clampFloat(in.JSHeapMB, 0, 1_000_000)
	in.LongTasks = clampInt(in.LongTasks, 0, 1_000_000)
	in.MaxStallMS = clampFloat(in.MaxStallMS, 0, 3_600_000)
	if len(in.Page) > 80 {
		in.Page = in.Page[:80]
	}
	h.sampler.AddClient(in)
	return c.SendStatus(fiber.StatusNoContent)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampFloat(v, lo, hi float64) float64 {
	if v != v || v < lo { // NaN or negative
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

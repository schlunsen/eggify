package routes

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"

	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterMachines adds the paired-Macs routes (ADR 014): the iPhone, signed
// in to this Mac, lists the other Macs this Mac's desktop app is paired with
// and has one of them mint a pair link for it. A pair link signs its holder
// in to the other Mac, so both routes need a signed-in admin
// (principal.RequireUser, with one account that user): the API key (any
// local process, agents included) gets 403 user_required, or an agent here
// could mint itself a session on the other Mac. Without the desktop app the
// list is empty and pair codes answer 404.
func RegisterMachines(api fiber.Router, h *handlers.MachinesHandler) {
	if h == nil {
		h = handlers.NewMachinesHandler(nil)
	}
	gate := principal.RequireUser(handlers.PairedMachinesAction)
	perUser := limiter.New(limiter.Config{
		Max:        10,
		Expiration: 15 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			if u, ok := principal.Username(c); ok {
				return "machine-pair-code:" + u
			}
			return "machine-pair-code-ip:" + clientIPKey(c)
		},
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "Too many pair codes. Try again in 15 minutes.",
				"code":  "rate_limited",
			})
		},
	})
	m := api.Group("/machines")
	m.Get("/paired", gate, h.HandleList)
	m.Post("/paired/:id/pair-code", gate, perUser, h.HandlePairCode)
}

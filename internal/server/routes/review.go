package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterReview configures the review queue for agent-written memories and
// skills. Approve/reject are privileged: they need a signed-in user
// (principal.RequireUser), never just the API key.
func RegisterReview(api fiber.Router, h *handlers.ReviewHandler) {
	review := api.Group("/review")
	reviewer := principal.RequireUser(handlers.ReviewAction)

	review.Get("/pending", h.HandleListPending)
	review.Get("/pending/counts", h.HandlePendingCounts)

	review.Post("/memories/:id/approve", reviewer, h.HandleApproveMemory)
	review.Post("/memories/:id/reject", reviewer, h.HandleRejectMemory)
	review.Post("/skills/:id/approve", reviewer, h.HandleApproveSkill)
	review.Post("/skills/:id/reject", reviewer, h.HandleRejectSkill)
}

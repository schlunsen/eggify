package handlers

// Sessions interrupted by a restart (see agents/resume_after_restart.go).
// GET lists the ones offered for "Continue" and how many sessions are working
// now (the desktop update dialog says they will continue). POST continues the
// offered ones, DELETE dismisses them; both need a signed-in user
// (routes/agent.go, principal.RequireUser).

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/server/agents"
)

// Privileged-action names for principal.RequireUser.
const (
	ResumeAfterRestartAction        = "Continuing interrupted sessions"
	DismissResumeAfterRestartAction = "Dismissing interrupted sessions"
)

// resumeVisibility returns a filter for the caller's visible sessions (nil:
// every session, when no user is signed in, i.e. user auth is off or the API
// key reads).
func (h *AgentSessionHandler) resumeVisibility(c *fiber.Ctx, ids []uuid.UUID) func(uuid.UUID) bool {
	username, _ := c.Locals("username").(string)
	if username == "" {
		return nil
	}
	visible := h.visibleSessionIDs(username, ids)
	return func(id uuid.UUID) bool { return visible[id] }
}

func (h *AgentSessionHandler) pendingIDs() []uuid.UUID {
	pending, _ := h.agentHandler.PendingResumes()
	ids := make([]uuid.UUID, 0, len(pending))
	for _, p := range pending {
		ids = append(ids, p.ID)
	}
	return ids
}

// HandleGetResumeAfterRestart: GET /api/agent/resume-after-restart.
func (h *AgentSessionHandler) HandleGetResumeAfterRestart(c *fiber.Ctx) error {
	if h.agentHandler == nil {
		return c.JSON(fiber.Map{"sessions": []any{}, "working_now": 0})
	}
	pending, reason := h.agentHandler.PendingResumes()
	ids := make([]uuid.UUID, 0, len(pending))
	for _, p := range pending {
		ids = append(ids, p.ID)
	}
	allow := h.resumeVisibility(c, ids)

	type item struct {
		ID            string    `json:"id"`
		Why           string    `json:"why"`
		UpdatedAt     time.Time `json:"updated_at"`
		InterruptedAt time.Time `json:"interrupted_at"`
		Subagents     int       `json:"subagents"`
	}
	out := make([]item, 0, len(pending))
	for _, p := range pending {
		if allow != nil && !allow(p.ID) {
			continue
		}
		out = append(out, item{p.ID.String(), p.Why, p.UpdatedAt, p.InterruptedAt, len(p.Subagents)})
	}

	// Sessions working right now, for "N working sessions will continue".
	var working func(uuid.UUID) bool
	if username, _ := c.Locals("username").(string); username != "" {
		sessions := h.agentHandler.SessionManager.ListSessions()
		all := make([]uuid.UUID, 0, len(sessions))
		for _, s := range sessions {
			all = append(all, s.ID)
		}
		visible := h.visibleSessionIDs(username, all)
		working = func(id uuid.UUID) bool { return visible[id] }
	}
	autoResume := true
	if h.repo != nil {
		if setting, err := h.repo.GetUserSetting(agents.ResumeAfterUpdateSettingKey); err == nil && setting != nil {
			autoResume = !strings.EqualFold(strings.TrimSpace(setting.Value), "false")
		}
	}
	return c.JSON(fiber.Map{
		"sessions":    out,
		"reason":      reason,
		"working_now": h.agentHandler.WorkingSessionCount(working),
		// Whether working sessions continue by themselves after an update.
		"auto_resume": autoResume,
	})
}

// HandleResumeAfterRestart: POST /api/agent/resume-after-restart continues
// every offered session the caller can see. Privileged (RequireUser).
func (h *AgentSessionHandler) HandleResumeAfterRestart(c *fiber.Ctx) error {
	allow := h.resumeVisibility(c, h.pendingIDs())
	n := h.agentHandler.ResumePending(allow)
	return c.JSON(fiber.Map{"resumed": n})
}

// HandleDismissResumeAfterRestart: DELETE /api/agent/resume-after-restart
// drops the offer. Privileged (RequireUser).
func (h *AgentSessionHandler) HandleDismissResumeAfterRestart(c *fiber.Ctx) error {
	allow := h.resumeVisibility(c, h.pendingIDs())
	n := h.agentHandler.DismissPending(allow)
	return c.JSON(fiber.Map{"dismissed": n})
}

package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/server/agents"
)

const (
	// allSubagentsLimit caps how many subagents GET /api/agent/subagents returns.
	allSubagentsLimit = 500
	// allSubagentsOutputLines caps each agent's recent_output in that listing;
	// the full output comes from GET /api/agent/sessions/:id/subagents.
	allSubagentsOutputLines = 20
	// allSubagentsDefaultWindow is how far back finished agents are listed
	// when the caller gives no "since".
	allSubagentsDefaultWindow = 24 * time.Hour
)

// HandleListAllSubagents lists the subagents of every session the caller may
// see, for the "All agents" overview (⇧⌘S).
//
//	GET /api/agent/subagents?status=running|all|failed&since=<RFC3339>
//
// Running agents are always listed; finished ones only when they changed
// after "since" (default: the last 24 h). Running agents come first, then
// finished ones, newest first. At most 500 agents, each with its last 20
// output lines.
func (h *AgentSessionHandler) HandleListAllSubagents(c *fiber.Ctx) error {
	if h.agentHandler == nil {
		return c.Status(503).JSON(fiber.Map{"error": "agent handler not initialized"})
	}

	status := c.Query("status", "running")
	switch status {
	case "running", "all", "failed":
	default:
		return c.Status(400).JSON(fiber.Map{"error": "status must be running, all or failed"})
	}

	since := time.Now().Add(-allSubagentsDefaultWindow)
	if raw := c.Query("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "since must be an RFC3339 time"})
		}
		since = parsed
	}

	list := h.agentHandler.ListSubagents(agents.SubagentListFilter{Status: status, Since: since})

	// SECURITY: subagent output can hold file contents and command output, so
	// a signed-in user sees only the subagents of sessions they may see (the
	// same rule as validateSessionOwnership). The API key sees all.
	if username, ok := c.Locals("username").(string); ok && username != "" && len(list) > 0 {
		seen := make(map[uuid.UUID]bool)
		ids := make([]uuid.UUID, 0)
		for _, agent := range list {
			if !seen[agent.ParentSessionID] {
				seen[agent.ParentSessionID] = true
				ids = append(ids, agent.ParentSessionID)
			}
		}
		visible := h.visibleSessionIDs(username, ids)
		kept := list[:0]
		for _, agent := range list {
			if visible[agent.ParentSessionID] {
				kept = append(kept, agent)
			}
		}
		list = kept
	}

	truncated := len(list) > allSubagentsLimit
	if truncated {
		list = list[:allSubagentsLimit]
	}
	running := 0
	for i := range list {
		if agents.MatchesSubagentStatus(list[i], "running") {
			running++
		}
		list[i].KeepNewestOutput(allSubagentsOutputLines)
	}

	return c.JSON(fiber.Map{
		"subagents": list,
		"total":     len(list),
		"running":   running,
		"truncated": truncated,
		"since":     since.UTC().Format(time.RFC3339),
	})
}

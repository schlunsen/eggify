package agents

import (
	"slices"
	"time"

	"github.com/schlunsen/eggify/internal/logging"
)

// SubagentListFilter selects subagents across every session for the
// "All agents" overview (GET /api/agent/subagents).
type SubagentListFilter struct {
	// Status is "running" (running or waiting), "failed" or "all".
	Status string
	// Since drops finished agents last updated before it. Running agents are
	// always kept. Zero keeps everything.
	Since time.Time
	// Limit caps the result (running agents come first).
	Limit int
}

// recentBackgroundAgentLister is implemented by SQLiteSessionStorage.
type recentBackgroundAgentLister interface {
	ListRecentBackgroundAgents(limit int) ([]*BackgroundAgent, error)
}

// subagentScanLimit bounds how many stored rows one listing reads.
const subagentScanLimit = 2000

// AllAgents returns a copy of every agent the manager holds in memory.
func (m *BackgroundAgentManager) AllAgents() []BackgroundAgent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]BackgroundAgent, 0, len(m.agents))
	for _, agent := range m.agents {
		out = append(out, agent.clone())
	}
	return out
}

// MatchesSubagentStatus reports whether an agent passes a status filter.
func MatchesSubagentStatus(agent BackgroundAgent, status string) bool {
	switch status {
	case "running":
		return agent.Status == BackgroundAgentStatusRunning || agent.Status == BackgroundAgentStatusWaiting
	case "failed":
		return agent.Status == BackgroundAgentStatusFailed
	default:
		return true
	}
}

// ListSubagents returns the subagents of every session (stored and live; a
// live agent wins over its stored row) that pass the filter: running first
// (oldest first), then finished ones, most recently updated first. Callers
// apply session visibility.
func (h *AgentHandler) ListSubagents(f SubagentListFilter) []BackgroundAgent {
	byID := make(map[string]BackgroundAgent)
	if h.SessionManager != nil && h.SessionManager.Storage != nil {
		if lister, ok := h.SessionManager.Storage.(recentBackgroundAgentLister); ok {
			stored, err := lister.ListRecentBackgroundAgents(subagentScanLimit)
			if err != nil {
				logging.Debug("Failed to list subagents: %v", err)
			}
			for _, agent := range stored {
				if h.BackgroundAgentMgr != nil {
					if _, live := h.BackgroundAgentMgr.GetAgent(agent.AgentID); !live && settleStaleRunning(agent) {
						if err := h.SessionManager.Storage.UpdateBackgroundAgent(agent); err != nil {
							logging.Debug("Failed to settle stale subagent %s: %v", agent.AgentID, err)
						}
					}
				}
				if agent.Status == BackgroundAgentStatusFailed && IsSubagentInterruption(agent.ErrorMessage) {
					agent.Interrupted = true
				}
				agent.LimitPaused = agent.Status == BackgroundAgentStatusFailed && IsSubagentLimitPause(agent.ErrorMessage)
				byID[agent.AgentID] = *agent
			}
		}
	}
	if h.BackgroundAgentMgr != nil {
		for _, agent := range h.BackgroundAgentMgr.AllAgents() {
			byID[agent.AgentID] = agent
		}
	}

	out := make([]BackgroundAgent, 0, len(byID))
	for _, agent := range byID {
		if !MatchesSubagentStatus(agent, f.Status) {
			continue
		}
		running := MatchesSubagentStatus(agent, "running")
		if !running && !f.Since.IsZero() && subagentLastActivity(agent).Before(f.Since) {
			continue
		}
		out = append(out, agent)
	}
	slices.SortFunc(out, compareSubagentsForListing)
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out
}

// subagentLastActivity is when an agent last changed.
func subagentLastActivity(agent BackgroundAgent) time.Time {
	t := agent.UpdatedAt
	if agent.CompletedAt != nil && agent.CompletedAt.After(t) {
		t = *agent.CompletedAt
	}
	return t
}

func compareSubagentsForListing(a, b BackgroundAgent) int {
	ar, br := MatchesSubagentStatus(a, "running"), MatchesSubagentStatus(b, "running")
	if ar != br {
		if ar {
			return -1
		}
		return 1
	}
	if ar {
		return a.CreatedAt.Compare(b.CreatedAt)
	}
	return subagentLastActivity(b).Compare(subagentLastActivity(a))
}

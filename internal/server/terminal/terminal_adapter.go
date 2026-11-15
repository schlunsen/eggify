package terminal

import (
	"github.com/schlunsen/eggify/internal/server/agents"
)

// PTYManagerAdapter adapts PTYManager to implement agents.TerminalManagerInterface
type PTYManagerAdapter struct {
	manager *PTYManager
}

// NewPTYManagerAdapter creates a new adapter for the PTYManager
func NewPTYManagerAdapter(manager *PTYManager) *PTYManagerAdapter {
	return &PTYManagerAdapter{
		manager: manager,
	}
}

// ListAllSessions returns all terminal sessions as TerminalSessionInfo
func (a *PTYManagerAdapter) ListAllSessions() []agents.TerminalSessionInfo {
	if a.manager == nil {
		return []agents.TerminalSessionInfo{}
	}

	a.manager.mu.RLock()
	defer a.manager.mu.RUnlock()

	var sessions []agents.TerminalSessionInfo
	for _, session := range a.manager.sessions {
		session.mu.RLock()
		info := agents.TerminalSessionInfo{
			SessionID:      session.ID,
			AgentSessionID: session.AgentSessionID,
			Shell:          session.Shell,
			WorkingDir:     session.WorkingDir,
			CreatedAt:      session.CreatedAt,
			Rows:           session.Rows,
			Cols:           session.Cols,
			ExitCode:       session.ExitCode,
			EndedAt:        session.EndedAt,
		}
		session.mu.RUnlock()

		sessions = append(sessions, info)
	}

	return sessions
}

// GetActiveTerminalCount returns the number of active (non-ended) terminal sessions
func (a *PTYManagerAdapter) GetActiveTerminalCount() int {
	if a.manager == nil {
		return 0
	}

	a.manager.mu.RLock()
	defer a.manager.mu.RUnlock()

	count := 0
	for _, session := range a.manager.sessions {
		session.mu.RLock()
		if session.EndedAt == nil {
			count++
		}
		session.mu.RUnlock()
	}

	return count
}

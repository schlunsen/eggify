package n0

import (
	"context"
	"encoding/json"
)

// ToolGuard is asked before the proxy forwards a high-impact tool call (see
// internal/toolguard). It returns false and a message for the agent when the
// call must not run (the user declined, a spending cap is reached, ...).
// It may block until the user answers.
type ToolGuard func(ctx context.Context, sessionID, tool string, args json.RawMessage) (bool, string)

// SetToolGuard installs the guard used by ServeProxy. Without one, calls
// that need the user's approval are refused.
func (m *Manager) SetToolGuard(g ToolGuard) { m.guard.Store(&g) }

func (m *Manager) toolGuard() ToolGuard {
	if p := m.guard.Load(); p != nil {
		return *p
	}
	return nil
}

package agents

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsSubagentInterruption(t *testing.T) {
	interrupted := []string{
		AppRestartedAgentMessage,
		"Agent stopped",
		"Agent killed: by user",
		"Agent cancelled",
		"Sub-agent explorer interrupted",
		"Sub-agent explorer interrupted: last words",
		"[Request interrupted by user for tool use]",
		"  Session ended  ",
	}
	for _, msg := range interrupted {
		assert.True(t, IsSubagentInterruption(msg), msg)
	}
	failed := []string{
		"",
		"Agent failed: exit status 1",
		"Sub-agent explorer failed: boom",
		"go test stopped with 3 failures",
		"Error: the build was interrupted by a compile error", // not at the start
	}
	for _, msg := range failed {
		assert.False(t, IsSubagentInterruption(msg), msg)
	}
}

func TestFailAgentMarksInterruptions(t *testing.T) {
	manager := NewBackgroundAgentManager()
	session := uuid.New()
	var events []BackgroundAgentFailedMessage
	manager.SetBroadcastCallback(func(_ uuid.UUID, msg interface{}) {
		if failed, ok := msg.(BackgroundAgentFailedMessage); ok {
			events = append(events, failed)
		}
	})
	manager.RegisterAgent("stopped", session, "general-purpose", "Stopped one")
	manager.RegisterAgent("broken", session, "general-purpose", "Broken one")
	manager.FailAgent("stopped", "Agent stopped")
	manager.FailAgent("broken", "Agent failed: exit status 1")

	stopped, _ := manager.GetAgent("stopped")
	broken, _ := manager.GetAgent("broken")
	assert.True(t, stopped.Interrupted)
	assert.False(t, broken.Interrupted)
	require.Len(t, events, 2)
	assert.True(t, events[0].Interrupted)
	assert.False(t, events[1].Interrupted)

	// Agents stored before this flag existed are classified when restored.
	manager.RestoreAgent(&BackgroundAgent{AgentID: "old", ParentSessionID: session, Status: BackgroundAgentStatusFailed, ErrorMessage: AppRestartedAgentMessage})
	old, _ := manager.GetAgent("old")
	assert.True(t, old.Interrupted)
}

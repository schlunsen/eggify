package agents

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddOutputLinesBatchesIntoOneEvent(t *testing.T) {
	manager := NewBackgroundAgentManager()
	sessionID := uuid.New()

	var mu sync.Mutex
	var outputs []BackgroundAgentOutputMessage
	manager.SetBroadcastCallback(func(_ uuid.UUID, msg interface{}) {
		if out, ok := msg.(BackgroundAgentOutputMessage); ok {
			mu.Lock()
			outputs = append(outputs, out)
			mu.Unlock()
		}
	})
	manager.RegisterAgent("a", sessionID, "test", "test")

	manager.AddOutputLines("a", []OutputLine{
		{Text: "RESULT_START"},
		{Text: "boom", IsError: true},
		{Text: "RESULT_END"},
	})
	manager.AddOutputLines("a", nil)                             // no-op
	manager.AddOutputLines("missing", []OutputLine{{Text: "x"}}) // unknown agent: no-op

	mu.Lock()
	require.Len(t, outputs, 1)
	msg := outputs[0]
	mu.Unlock()
	assert.Equal(t, "RESULT_END", msg.Output)
	assert.True(t, msg.IsError)
	assert.Equal(t, []string{"RESULT_START", "boom", "RESULT_END"}, msg.Lines)

	agent, ok := manager.GetAgent("a")
	require.True(t, ok)
	assert.Equal(t, 3, agent.OutputLines)
	assert.Equal(t, "RESULT_END", agent.LastOutput)
	assert.Equal(t, []string{"RESULT_START", "boom", "RESULT_END"}, agent.RecentOutput)

	// A single line keeps the old event shape (no lines array).
	manager.AddOutput("a", "one", false)
	mu.Lock()
	require.Len(t, outputs, 2)
	assert.Equal(t, "one", outputs[1].Output)
	assert.Nil(t, outputs[1].Lines)
	mu.Unlock()
}

func TestRecentOutputKeepsNewestLines(t *testing.T) {
	manager := NewBackgroundAgentManager()
	manager.RegisterAgent("a", uuid.New(), "test", "test")

	total := 5*maxRecentOutputLines + 7
	for i := 0; i < total; i++ {
		manager.AddOutput("a", fmt.Sprintf("line %d", i), false)
		manager.mu.RLock()
		n := len(manager.agents["a"].RecentOutput)
		manager.mu.RUnlock()
		require.LessOrEqual(t, n, 2*maxRecentOutputLines, "buffer must stay bounded")
	}

	agent, ok := manager.GetAgent("a")
	require.True(t, ok)
	require.Len(t, agent.RecentOutput, maxRecentOutputLines)
	assert.Equal(t, fmt.Sprintf("line %d", total-maxRecentOutputLines), agent.RecentOutput[0])
	assert.Equal(t, fmt.Sprintf("line %d", total-1), agent.RecentOutput[maxRecentOutputLines-1])
	assert.Equal(t, total, agent.OutputLines)
}

func TestCleanupCompletedAgentsPrunesAllMaps(t *testing.T) {
	manager := NewBackgroundAgentManager()
	sessionID := uuid.New()
	manager.RegisterAgent("old", sessionID, "test", "test")
	manager.RegisterAgent("live", sessionID, "test", "test")
	manager.BindTask("old", "task-old")
	manager.BindTask("live", "task-live")
	manager.NextSequence("old")
	manager.NextSequence("live")
	manager.lastPersistMu.Lock()
	manager.lastPersist["old"] = time.Now()
	manager.lastPersist["live"] = time.Now()
	manager.lastPersistMu.Unlock()
	manager.CompleteAgent("old", "done")

	manager.mu.Lock()
	manager.agents["old"].UpdatedAt = time.Now().Add(-48 * time.Hour)
	manager.mu.Unlock()

	assert.Equal(t, 1, manager.CleanupCompletedAgents(24*time.Hour))

	manager.mu.RLock()
	_, hasAgent := manager.agents["old"]
	_, hasSeq := manager.sequences["old"]
	_, hasLiveSeq := manager.sequences["live"]
	manager.mu.RUnlock()
	manager.lastPersistMu.Lock()
	_, hasPersist := manager.lastPersist["old"]
	_, hasLivePersist := manager.lastPersist["live"]
	manager.lastPersistMu.Unlock()

	assert.False(t, hasAgent)
	assert.False(t, hasSeq)
	assert.False(t, hasPersist)
	assert.Equal(t, "", manager.AgentForTask("task-old"))
	assert.True(t, hasLiveSeq)
	assert.True(t, hasLivePersist)
	assert.Equal(t, "live", manager.AgentForTask("task-live"))
}

func TestStartCleanupEvictsAndStops(t *testing.T) {
	manager := NewBackgroundAgentManager()
	manager.RegisterAgent("old", uuid.New(), "test", "test")
	manager.FailAgent("old", "err")
	manager.mu.Lock()
	manager.agents["old"].UpdatedAt = time.Now().Add(-time.Hour)
	manager.mu.Unlock()

	manager.StartCleanup(10*time.Millisecond, time.Minute)
	manager.StartCleanup(10*time.Millisecond, time.Minute) // second start is a no-op
	require.Eventually(t, func() bool { return manager.Count() == 0 }, time.Second, 5*time.Millisecond)

	manager.StopCleanup()
	manager.StopCleanup() // idempotent

	// After stopping, nothing is evicted any more.
	manager.RegisterAgent("later", uuid.New(), "test", "test")
	manager.CompleteAgent("later", "")
	manager.mu.Lock()
	manager.agents["later"].UpdatedAt = time.Now().Add(-time.Hour)
	manager.mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, 1, manager.Count())
}

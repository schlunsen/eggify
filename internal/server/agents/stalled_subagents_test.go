package agents

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/stretchr/testify/require"
)

// slowRunningStorage delays every write of a running agent, so an output
// write that started before the agent finished lands after its completion.
type slowRunningStorage struct {
	SessionStorage
}

func (s slowRunningStorage) UpdateBackgroundAgent(agent *BackgroundAgent) error {
	if agent.Status == BackgroundAgentStatusRunning {
		time.Sleep(150 * time.Millisecond)
	}
	return s.SessionStorage.UpdateBackgroundAgent(agent)
}

func newStalledTestStorage(t *testing.T) (*SQLiteSessionStorage, uuid.UUID) {
	t.Helper()
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	storage, err := NewSQLiteSessionStorage(db.GetDB())
	require.NoError(t, err)
	sessionID := uuid.New()
	_, err = db.GetDB().Exec(`INSERT INTO agent_sessions (id) VALUES (?)`, sessionID.String())
	require.NoError(t, err)
	return storage, sessionID
}

func waitPersisted(t *testing.T, mgr *BackgroundAgentManager) {
	t.Helper()
	require.Eventually(t, func() bool {
		mgr.persistMu.Lock()
		defer mgr.persistMu.Unlock()
		return len(mgr.persistActive) == 0
	}, 5*time.Second, 10*time.Millisecond)
}

func storedAgent(t *testing.T, storage SessionStorage, sessionID uuid.UUID, agentID string) *BackgroundAgent {
	t.Helper()
	agents, err := storage.GetBackgroundAgentsForSession(sessionID)
	require.NoError(t, err)
	for _, agent := range agents {
		if agent.AgentID == agentID {
			return agent
		}
	}
	t.Fatalf("agent %s not stored", agentID)
	return nil
}

// A completed agent's row must not be overwritten by an output write that
// was still in flight (this left forked skills "running" for days).
func TestFinishedAgentIsNotOverwrittenByALateOutputWrite(t *testing.T) {
	storage, sessionID := newStalledTestStorage(t)
	mgr := NewBackgroundAgentManager()
	mgr.SetStorage(slowRunningStorage{storage})

	mgr.RegisterAgent("toolu_skill", sessionID, "", "")
	waitPersisted(t, mgr)
	mgr.AddOutput("toolu_skill", "TOOL:Bash: go test ./...", false)
	mgr.CompleteAgent("toolu_skill", `Skill "code-review" completed (forked execution).`)
	waitPersisted(t, mgr)

	require.Equal(t, BackgroundAgentStatusCompleted, storedAgent(t, storage, sessionID, "toolu_skill").Status)
}

func TestStopStalledAgentsStopsOnlyQuietRunningAgents(t *testing.T) {
	mgr := NewBackgroundAgentManager()
	sessionID := uuid.New()
	mgr.RegisterAgent("quiet", sessionID, "", "")
	mgr.RegisterAgent("busy", sessionID, "", "")
	mgr.RegisterAgent("done", sessionID, "", "")
	mgr.CompleteAgent("done", "ok")
	mgr.mu.Lock()
	for _, id := range []string{"quiet", "done"} {
		mgr.agents[id].UpdatedAt = time.Now().Add(-7 * time.Hour)
	}
	mgr.mu.Unlock()

	require.Equal(t, 1, mgr.StopStalledAgents(stalledAgentAfter))

	quiet, _ := mgr.GetAgent("quiet")
	require.Equal(t, BackgroundAgentStatusFailed, quiet.Status)
	require.Equal(t, StalledAgentMessage, quiet.ErrorMessage)
	require.True(t, IsSubagentInterruption(quiet.ErrorMessage))
	busy, _ := mgr.GetAgent("busy")
	require.Equal(t, BackgroundAgentStatusRunning, busy.Status)
	done, _ := mgr.GetAgent("done")
	require.Equal(t, BackgroundAgentStatusCompleted, done.Status)

	// A stopped agent that speaks again is running again.
	require.True(t, mgr.ReopenAgent("quiet"))
}

// A stored row still marked running that no agent of this process runs is
// shown, and saved, as stopped.
func TestStoredRunningRowWithoutALiveAgentIsSettled(t *testing.T) {
	storage, sessionID := newStalledTestStorage(t)
	now := time.Now()
	require.NoError(t, storage.SaveBackgroundAgent(&BackgroundAgent{
		AgentID: "toolu_stale", ParentSessionID: sessionID,
		Status: BackgroundAgentStatusRunning, CreatedAt: now, UpdatedAt: now,
	}))

	mgr := NewBackgroundAgentManager()
	mgr.SetStorage(storage)
	h := &AgentHandler{BackgroundAgentMgr: mgr, SessionManager: &SessionManager{Storage: storage}}

	agents := h.SubagentsForSession(sessionID)
	require.Len(t, agents, 1)
	require.Equal(t, BackgroundAgentStatusFailed, agents[0].Status)
	require.True(t, agents[0].Interrupted)
	require.Equal(t, StaleRunningAgentMessage, agents[0].ErrorMessage)
	waitPersisted(t, mgr)
	require.Equal(t, BackgroundAgentStatusFailed, storedAgent(t, storage, sessionID, "toolu_stale").Status)
}

func TestListSubagentsSettlesStaleRowsButKeepsLiveAgents(t *testing.T) {
	storage, sessionID := newStalledTestStorage(t)
	now := time.Now()
	for _, id := range []string{"toolu_stale", "toolu_live"} {
		require.NoError(t, storage.SaveBackgroundAgent(&BackgroundAgent{
			AgentID: id, ParentSessionID: sessionID,
			Status: BackgroundAgentStatusRunning, CreatedAt: now, UpdatedAt: now,
		}))
	}
	mgr := NewBackgroundAgentManager()
	mgr.RegisterAgent("toolu_live", sessionID, "", "")
	h := &AgentHandler{BackgroundAgentMgr: mgr, SessionManager: &SessionManager{Storage: storage}}

	running := h.ListSubagents(SubagentListFilter{Status: "running"})
	require.Len(t, running, 1)
	require.Equal(t, "toolu_live", running[0].AgentID)
	require.Equal(t, BackgroundAgentStatusFailed, storedAgent(t, storage, sessionID, "toolu_stale").Status)
	require.Equal(t, BackgroundAgentStatusRunning, storedAgent(t, storage, sessionID, "toolu_live").Status)
}

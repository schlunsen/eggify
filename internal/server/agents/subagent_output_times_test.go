package agents

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each kept output line has a time (unix ms) next to it, in the same order.
func TestOutputTimesFollowLinesAndEvents(t *testing.T) {
	manager := NewBackgroundAgentManager()
	var mu sync.Mutex
	var outputs []BackgroundAgentOutputMessage
	manager.SetBroadcastCallback(func(_ uuid.UUID, msg interface{}) {
		if out, ok := msg.(BackgroundAgentOutputMessage); ok {
			mu.Lock()
			outputs = append(outputs, out)
			mu.Unlock()
		}
	})
	manager.RegisterAgent("a", uuid.New(), "test", "test")

	at := time.UnixMilli(1_760_000_000_123)
	before := time.Now().UnixMilli()
	manager.AddOutputLines("a", []OutputLine{{Text: "TOOL:Read: a.go", At: at}, {Text: "now"}})
	manager.AddOutput("a", "single", false)
	after := time.Now().UnixMilli()

	agent, _ := manager.GetAgent("a")
	require.Len(t, agent.RecentOutputAt, 3)
	assert.Equal(t, at.UnixMilli(), agent.RecentOutputAt[0])
	for _, got := range agent.RecentOutputAt[1:] {
		assert.True(t, got >= before && got <= after, "line without a time gets now: %d", got)
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, outputs, 2)
	assert.Equal(t, agent.RecentOutputAt[:2], outputs[0].LinesAt, "batched lines carry lines_at")
	assert.Equal(t, agent.RecentOutputAt[1], outputs[0].At, "at is the last line's time")
	assert.Nil(t, outputs[1].LinesAt)
	assert.Equal(t, agent.RecentOutputAt[2], outputs[1].At)

	data, err := json.Marshal(outputs[0])
	require.NoError(t, err)
	assert.Contains(t, string(data), `"lines_at":[1760000000123,`)
	assert.Contains(t, string(data), `"at":`)
}

func TestOutputTimesTrimWithLines(t *testing.T) {
	manager := NewBackgroundAgentManager()
	manager.RegisterAgent("a", uuid.New(), "test", "test")
	total := 3*maxRecentOutputLines + 5
	for i := 0; i < total; i++ {
		manager.AddOutputLines("a", []OutputLine{{Text: fmt.Sprintf("line %d", i), At: time.UnixMilli(int64(1000 + i))}})
	}
	// One batch larger than the cap.
	big := make([]OutputLine, maxRecentOutputLines+10)
	for i := range big {
		big[i] = OutputLine{Text: fmt.Sprintf("big %d", i), At: time.UnixMilli(int64(100_000 + i))}
	}
	manager.AddOutputLines("a", big)

	agent, _ := manager.GetAgent("a")
	require.Len(t, agent.RecentOutput, maxRecentOutputLines)
	require.Len(t, agent.RecentOutputAt, maxRecentOutputLines)
	assert.Equal(t, "big 10", agent.RecentOutput[0])
	assert.Equal(t, int64(100_010), agent.RecentOutputAt[0])
	assert.Equal(t, int64(100_000+maxRecentOutputLines+9), agent.RecentOutputAt[maxRecentOutputLines-1])

	// KeepNewestOutput (the all-agents listing) trims both.
	agent.KeepNewestOutput(20)
	require.Len(t, agent.RecentOutput, 20)
	require.Len(t, agent.RecentOutputAt, 20)
	assert.Equal(t, int64(100_000+maxRecentOutputLines+9), agent.RecentOutputAt[19])
}

// A restored agent from before migration 61 has lines but no times: new lines
// get times, the old ones 0 (unknown), and the two slices stay parallel.
func TestOutputTimesPadRestoredLines(t *testing.T) {
	manager := NewBackgroundAgentManager()
	manager.RestoreAgent(&BackgroundAgent{AgentID: "old", ParentSessionID: uuid.New(),
		Status: BackgroundAgentStatusRunning, RecentOutput: []string{"a", "b"}})

	agent, _ := manager.GetAgent("old")
	assert.Nil(t, agent.RecentOutputAt, "no times known: none served")

	manager.AddOutputLines("old", []OutputLine{{Text: "c", At: time.UnixMilli(5000)}})
	agent, _ = manager.GetAgent("old")
	assert.Equal(t, []string{"a", "b", "c"}, agent.RecentOutput)
	assert.Equal(t, []int64{0, 0, 5000}, agent.RecentOutputAt)
}

func TestOutputTimesPersistRoundTrip(t *testing.T) {
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	defer database.ResetInstance()

	storage, err := NewSQLiteSessionStorage(db.GetDB())
	require.NoError(t, err)
	sessionID := uuid.New()
	_, err = db.GetDB().Exec(`INSERT INTO agent_sessions (id) VALUES (?)`, sessionID.String())
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Second)
	agent := &BackgroundAgent{
		AgentID: "toolu_T", ParentSessionID: sessionID, Status: BackgroundAgentStatusRunning,
		CreatedAt: now, UpdatedAt: now,
		RecentOutput: []string{"TOOL:Read: a.go"}, RecentOutputAt: []int64{1000},
	}
	require.NoError(t, storage.SaveBackgroundAgent(agent))
	agent.RecentOutput = append(agent.RecentOutput, "RESULT_START", "x", "RESULT_END")
	agent.RecentOutputAt = append(agent.RecentOutputAt, 1200, 1200, 3500)
	require.NoError(t, storage.UpdateBackgroundAgent(agent))

	// A row without times (from before migration 61).
	old := &BackgroundAgent{AgentID: "toolu_O", ParentSessionID: sessionID, Status: BackgroundAgentStatusCompleted,
		CreatedAt: now, UpdatedAt: now, RecentOutput: []string{"done"}}
	require.NoError(t, storage.SaveBackgroundAgent(old))

	loaded, err := storage.GetBackgroundAgentsForSession(sessionID)
	require.NoError(t, err)
	require.Len(t, loaded, 2)
	byID := map[string]*BackgroundAgent{loaded[0].AgentID: loaded[0], loaded[1].AgentID: loaded[1]}
	assert.Equal(t, []int64{1000, 1200, 1200, 3500}, byID["toolu_T"].RecentOutputAt)
	assert.Nil(t, byID["toolu_O"].RecentOutputAt)

	var stored *string
	require.NoError(t, db.GetDB().QueryRow(`SELECT recent_output_at FROM background_agents WHERE agent_id = 'toolu_O'`).Scan(&stored))
	assert.Nil(t, stored, "no times are stored as NULL")

	// Times that don't line up with the lines read as none.
	_, err = db.GetDB().Exec(`UPDATE background_agents SET recent_output_at = '[1,2]' WHERE agent_id = 'toolu_O'`)
	require.NoError(t, err)
	loaded, err = storage.GetBackgroundAgentsForSession(sessionID)
	require.NoError(t, err)
	for _, a := range loaded {
		if a.AgentID == "toolu_O" {
			assert.Nil(t, a.RecentOutputAt)
		}
	}
}

// Workflow transcript lines carry their own timestamp; those are kept.
func TestWorkflowAgents_OutputTimesFromTranscript(t *testing.T) {
	x := newSubagentHarness(t)
	dir := workflowRunDir(t)
	const stamp = "2026-10-04T20:28:03.176Z"
	want, err := time.Parse(time.RFC3339Nano, stamp)
	require.NoError(t, err)
	for _, name := range []string{"journal.jsonl", "agent-" + wfAgentOne + ".jsonl", "agent-" + wfAgentTwo + ".jsonl"} {
		lines := fixtureLines(t, filepath.Join("testdata/workflow_run", name))
		if name == "agent-"+wfAgentOne+".jsonl" {
			for i := range lines {
				lines[i] = strings.Replace(lines[i], `{"type":"assistant",`, `{"type":"assistant","timestamp":"`+stamp+`",`, 1)
			}
		}
		appendLines(t, filepath.Join(dir, name), lines...)
	}
	stream := parentStream(t, dir)
	x.feed(t, stream[:launchIndex(t, stream)+1]...)

	one := x.waitStatus(t, wfAgentOne, BackgroundAgentStatusCompleted)
	idx := slices.Index(one.RecentOutput, "TOOL:Bash: echo hello-one")
	require.GreaterOrEqual(t, idx, 0)
	require.Len(t, one.RecentOutputAt, len(one.RecentOutput))
	assert.Equal(t, want.UnixMilli(), one.RecentOutputAt[idx])

	// Without a timestamp the line gets the time it was read.
	two := x.waitStatus(t, wfAgentTwo, BackgroundAgentStatusCompleted)
	require.Len(t, two.RecentOutputAt, len(two.RecentOutput))
	assert.Greater(t, two.RecentOutputAt[0], want.UnixMilli())
}

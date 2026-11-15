package agents

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	claude "github.com/schlunsen/claude-agent-sdk-go"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/stretchr/testify/require"
)

// subagentHarness wires a session reader's routing to a handler, the way
// NewAgentHandler does, and records what the tracker broadcasts.
type subagentHarness struct {
	sm      *SessionManager
	h       *AgentHandler
	mgr     *BackgroundAgentManager
	session *AgentSession

	mu         sync.Mutex
	broadcasts []interface{}
}

func newSubagentHarness(t *testing.T) *subagentHarness {
	t.Helper()
	mgr := NewBackgroundAgentManager()
	x := &subagentHarness{
		sm:      &SessionManager{},
		mgr:     mgr,
		h:       &AgentHandler{BackgroundAgentMgr: mgr},
		session: &AgentSession{Session: Session{ID: uuid.New()}},
	}
	mgr.SetBroadcastCallback(func(_ uuid.UUID, msg interface{}) {
		x.mu.Lock()
		defer x.mu.Unlock()
		x.broadcasts = append(x.broadcasts, msg)
	})
	x.sm.SetSubagentCallback(x.h.handleSubagentTraffic)
	return x
}

// feed routes messages as the session reader does and returns the ones left
// for the parent conversation.
func (x *subagentHarness) feed(t *testing.T, raw ...string) []types.Message {
	t.Helper()
	var parent []types.Message
	for _, line := range raw {
		msg, err := types.UnmarshalMessage([]byte(line))
		require.NoError(t, err, line)
		if !x.sm.routeSubagentTraffic(x.session, msg) {
			parent = append(parent, msg)
		}
	}
	return parent
}

func (x *subagentHarness) agent(t *testing.T, id string) *BackgroundAgent {
	t.Helper()
	agent, ok := x.mgr.GetAgent(id)
	require.True(t, ok, "agent %s not tracked", id)
	return agent
}

func (x *subagentHarness) subagentMessages(agentID string) []SubagentMessageData {
	x.mu.Lock()
	defer x.mu.Unlock()
	var out []SubagentMessageData
	for _, b := range x.broadcasts {
		if m, ok := b.(SubagentMessageData); ok && m.AgentID == agentID {
			out = append(out, m)
		}
	}
	return out
}

func readFixture(t *testing.T, name string) []string {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	require.NoError(t, err)
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		if line := scanner.Text(); line != "" {
			lines = append(lines, line)
		}
	}
	require.NoError(t, scanner.Err())
	return lines
}

// The fixture is a real Claude Code 2.1.280 stream-json recording of a parent
// launching one background agent that runs `echo hello-from-sub` and replies DONE.
func TestBackgroundSubagentStreamIsRoutedToTracker(t *testing.T) {
	x := newSubagentHarness(t)
	const agentID = "toolu_01N3vm4i5RqrF4HU4KwTQSJ4"

	parent := x.feed(t, readFixture(t, "background_subagent_stream.jsonl")...)

	// Only the parent's own conversation is left: the Agent call, its launch
	// result, "LAUNCHED", "FINISHED" and the result.
	require.Len(t, parent, 5)
	for _, msg := range parent {
		require.Empty(t, subagentParentID(msg), "subagent message leaked into the parent stream")
		_, isTask := parseTaskEvent(msg)
		require.False(t, isTask, "task event leaked into the parent stream: %T", msg)
	}

	agent := x.agent(t, agentID)
	require.Equal(t, BackgroundAgentStatusCompleted, agent.Status)
	require.True(t, agent.Background)
	require.Equal(t, "general-purpose", agent.SubagentType)
	require.Equal(t, "Run bash command and reply DONE", agent.Description)
	require.Equal(t, agentID, x.mgr.AgentForTask("a0b0241fd253252a1"))
	require.Subset(t, agent.RecentOutput, []string{"Running in the background", "TOOL:Bash: echo hello-from-sub", "hello-from-sub", "DONE"})
	require.NotContains(t, fmt.Sprint(agent.RecentOutput), "internal metadata", "launch metadata shown as output")
	// The subagent's own messages say which model it runs on.
	require.Equal(t, "claude-haiku-4-5-20251001", agent.Model)
	require.Equal(t, ModelSourceActual, agent.ModelSource)

	var kinds []string
	for _, m := range x.subagentMessages(agentID) {
		kinds = append(kinds, m.MessageType)
	}
	// The recorded thinking blocks are redacted (empty), so they are skipped.
	require.Equal(t, []string{"tool_use", "tool_result", "text"}, kinds)
}

func TestParallelForegroundSubagentsKeepTheirOwnOutput(t *testing.T) {
	x := newSubagentHarness(t)

	parent := x.feed(t,
		`{"type":"assistant","message":{"role":"assistant","content":[
			{"type":"tool_use","id":"toolu_A","name":"Agent","input":{"description":"Review A","subagent_type":"general-purpose"}},
			{"type":"tool_use","id":"toolu_B","name":"Agent","input":{"description":"Review B","subagent_type":"Explore"}}]}}`,
		`{"type":"assistant","parent_tool_use_id":"toolu_A","message":{"role":"assistant","content":[{"type":"text","text":"working on A"}]}}`,
		`{"type":"assistant","parent_tool_use_id":"toolu_B","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_b1","name":"Grep","input":{"pattern":"x"}}]}}`,
		`{"type":"user","parent_tool_use_id":"toolu_B","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_b1","content":"match in b.go"}]}}`,
		`{"type":"assistant","parent_tool_use_id":"toolu_A","message":{"role":"assistant","content":[{"type":"text","text":"A is done"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_B","content":[{"type":"text","text":"B report"}]}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_A","content":[{"type":"text","text":"A report"}]}]}}`,
	)
	require.Len(t, parent, 3, "the Agent calls and their two results stay in the parent")

	a, b := x.agent(t, "toolu_A"), x.agent(t, "toolu_B")
	require.Equal(t, BackgroundAgentStatusCompleted, a.Status)
	require.Equal(t, BackgroundAgentStatusCompleted, b.Status)
	require.False(t, a.Background)
	require.Equal(t, []string{"working on A", "A is done", "A report"}, a.RecentOutput)
	require.Equal(t, []string{"TOOL:Grep: x", "RESULT_START", "match in b.go", "RESULT_END", "B report"}, b.RecentOutput)
}

func TestKilledBackgroundSubagentIsMarkedFailed(t *testing.T) {
	x := newSubagentHarness(t)
	x.feed(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_K","name":"Agent","input":{"description":"Long job","run_in_background":true}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_K","content":[{"type":"text","text":"Async agent launched successfully.\nagentId: task123 (internal ID)"}]}]}}`,
	)
	require.Equal(t, BackgroundAgentStatusRunning, x.agent(t, "toolu_K").Status, "a launch result must not complete the agent")

	// A stopped task can report only through task_updated.
	x.feed(t, `{"type":"system","subtype":"task_updated","task_id":"task123","patch":{"status":"killed"}}`)
	agent := x.agent(t, "toolu_K")
	require.Equal(t, BackgroundAgentStatusFailed, agent.Status)
	require.Equal(t, "Agent killed", agent.ErrorMessage)

	// A late notification does not overwrite the terminal state.
	x.feed(t, `{"type":"system","subtype":"task_notification","task_id":"task123","status":"completed"}`)
	require.Equal(t, BackgroundAgentStatusFailed, x.agent(t, "toolu_K").Status)
}

func TestUnknownTaskEventsAreIgnored(t *testing.T) {
	x := newSubagentHarness(t)
	parent := x.feed(t,
		`{"type":"system","subtype":"task_notification","task_id":"bash-shell-1","status":"completed"}`,
		`{"type":"system","subtype":"background_tasks_changed","tasks":[]}`,
	)
	require.Empty(t, parent, "task events never reach the parent stream")
	require.Zero(t, x.mgr.Count())
}

func TestNestedSubagentDepth(t *testing.T) {
	x := newSubagentHarness(t)
	x.feed(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_outer","name":"Agent","input":{"description":"Outer"}}]}}`,
		`{"type":"assistant","parent_tool_use_id":"toolu_outer","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_inner","name":"Agent","input":{"description":"Inner","subagent_type":"Explore"}}]}}`,
		`{"type":"assistant","parent_tool_use_id":"toolu_inner","message":{"role":"assistant","content":[{"type":"text","text":"deep work"}]}}`,
		`{"type":"user","parent_tool_use_id":"toolu_outer","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_inner","content":"inner report"}]}}`,
	)

	inner := x.agent(t, "toolu_inner")
	require.Equal(t, "toolu_outer", inner.ParentAgentID)
	require.Equal(t, 1, x.mgr.Depth("toolu_inner"))
	require.Equal(t, 0, x.mgr.Depth("toolu_outer"))
	require.Equal(t, BackgroundAgentStatusCompleted, inner.Status)
	require.Equal(t, []string{"deep work", "inner report"}, inner.RecentOutput)
	require.Equal(t, BackgroundAgentStatusRunning, x.agent(t, "toolu_outer").Status)

	msgs := x.subagentMessages("toolu_inner")
	require.NotEmpty(t, msgs)
	require.Equal(t, 1, msgs[0].NestingDepth)
}

func TestRecentOutputIsCapped(t *testing.T) {
	mgr := NewBackgroundAgentManager()
	mgr.RegisterAgent("a", uuid.New(), "", "")
	for i := 0; i < maxRecentOutputLines+25; i++ {
		mgr.AddOutput("a", fmt.Sprintf("line %d", i), false)
	}
	agent, _ := mgr.GetAgent("a")
	require.Len(t, agent.RecentOutput, maxRecentOutputLines)
	require.Equal(t, "line 25", agent.RecentOutput[0])
	require.Equal(t, maxRecentOutputLines+25, agent.OutputLines)

	// Copies handed out are independent of the tracked agent.
	agent.RecentOutput[0] = "changed"
	again, _ := mgr.GetAgent("a")
	require.Equal(t, "line 25", again.RecentOutput[0])
}

func TestBackgroundAgentStorageKeepsOutput(t *testing.T) {
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	require.NoError(t, err)
	defer db.Close()

	storage, err := NewSQLiteSessionStorage(db.GetDB())
	require.NoError(t, err)
	sessionID := uuid.New()
	_, err = db.GetDB().Exec(`INSERT INTO agent_sessions (id) VALUES (?)`, sessionID.String())
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Second)
	agent := &BackgroundAgent{
		AgentID: "toolu_S", ParentSessionID: sessionID, SubagentType: "Explore", Description: "Look around",
		Status: BackgroundAgentStatusRunning, CreatedAt: now, UpdatedAt: now,
		ParentAgentID: "toolu_P", Background: true, RecentOutput: []string{"TOOL:Read", "found it"},
	}
	require.NoError(t, storage.SaveBackgroundAgent(agent))

	agent.RecentOutput = append(agent.RecentOutput, "done")
	agent.Status = BackgroundAgentStatusCompleted
	agent.TaskID = "a0b0task"
	agent.Prompt = "Look around the repo"
	agent.Model, agent.ModelSource = "claude-haiku-4-5-20251001", ModelSourceActual
	require.NoError(t, storage.UpdateBackgroundAgent(agent))

	loaded, err := storage.GetBackgroundAgentsForSession(sessionID)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	require.Equal(t, []string{"TOOL:Read", "found it", "done"}, loaded[0].RecentOutput)
	require.Equal(t, "toolu_P", loaded[0].ParentAgentID)
	require.True(t, loaded[0].Background)
	require.Equal(t, BackgroundAgentStatusCompleted, loaded[0].Status)
	require.Equal(t, "a0b0task", loaded[0].TaskID)
	require.Equal(t, "claude-haiku-4-5-20251001", loaded[0].Model)
	require.Equal(t, ModelSourceActual, loaded[0].ModelSource)
	require.Equal(t, "Look around the repo", loaded[0].Prompt)
}

func TestSubagentToolLineSaysWhatTheToolActsOn(t *testing.T) {
	require.Equal(t, "TOOL:Bash: go test ./... -run X", subagentToolLine("Bash", map[string]interface{}{"command": "go test ./...\n  -run X", "description": "Run tests"}))
	require.Equal(t, "TOOL:Read: /tmp/a.go", subagentToolLine("Read", map[string]interface{}{"file_path": "/tmp/a.go"}))
	require.Equal(t, "TOOL:FileChange: a.go (+1 more)", subagentToolLine("FileChange", map[string]interface{}{"changes": []interface{}{
		map[string]interface{}{"path": "a.go"}, map[string]interface{}{"path": "b.go"}}}))
	require.Equal(t, "TOOL:TodoWrite", subagentToolLine("TodoWrite", map[string]interface{}{"todos": []interface{}{}}))
	long := subagentToolLine("Bash", map[string]interface{}{"command": strings.Repeat("x", 900)})
	require.Equal(t, "TOOL:Bash: "+strings.Repeat("x", subagentCommandMaxRunes)+"…", long)
	longPath := subagentToolLine("Read", map[string]interface{}{"file_path": strings.Repeat("y", 400)})
	require.Equal(t, "TOOL:Read: "+strings.Repeat("y", 160)+"…", longPath)
}

// launchFinishedBackgroundAgent launches background agent toolu_R (task taskR)
// and lets it complete.
func launchFinishedBackgroundAgent(t *testing.T, x *subagentHarness) {
	t.Helper()
	x.feed(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_R","name":"Agent","input":{"description":"Open the PR","run_in_background":true}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_R","content":[{"type":"text","text":"Async agent launched successfully.\nagentId: taskR (internal ID)"}]}]}}`,
		`{"type":"assistant","parent_tool_use_id":"toolu_R","message":{"role":"assistant","content":[{"type":"text","text":"first run done"}]}}`,
		`{"type":"system","subtype":"task_notification","task_id":"taskR","status":"completed"}`,
	)
	require.Equal(t, BackgroundAgentStatusCompleted, x.agent(t, "toolu_R").Status)
}

// A finished agent the parent messages with SendMessage is resumed by the CLI
// and must show as running again until its resumed run finishes.
func TestSendMessageReopensFinishedSubagent(t *testing.T) {
	x := newSubagentHarness(t)
	launchFinishedBackgroundAgent(t, x)

	x.feed(t, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_S","name":"SendMessage","input":{"to":"taskR","message":"also open a second PR"}}]}}`)
	require.Equal(t, BackgroundAgentStatusCompleted, x.agent(t, "toolu_R").Status, "not reopened before the call succeeds")

	x.feed(t, `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_S","content":"Message sent; agent resumed"}]}}`)
	agent := x.agent(t, "toolu_R")
	require.Equal(t, BackgroundAgentStatusRunning, agent.Status)
	require.Nil(t, agent.CompletedAt)

	x.mu.Lock()
	last := x.broadcasts[len(x.broadcasts)-1]
	x.mu.Unlock()
	progress, ok := last.(BackgroundAgentProgressMessage)
	require.True(t, ok, "reopen is broadcast as a progress event, got %T", last)
	require.Equal(t, "running", progress.Status)

	x.feed(t,
		`{"type":"assistant","parent_tool_use_id":"toolu_R","message":{"role":"assistant","content":[{"type":"text","text":"second PR opened"}]}}`,
		`{"type":"system","subtype":"task_notification","task_id":"taskR","status":"completed"}`,
	)
	agent = x.agent(t, "toolu_R")
	require.Equal(t, BackgroundAgentStatusCompleted, agent.Status, "the resumed run completes again")
	require.Equal(t, []string{"Running in the background", "first run done", "second PR opened"}, agent.RecentOutput)
}

// A denied or failed SendMessage does not resume the agent.
func TestFailedSendMessageLeavesSubagentFinished(t *testing.T) {
	x := newSubagentHarness(t)
	launchFinishedBackgroundAgent(t, x)

	x.feed(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_S","name":"SendMessage","input":{"to":"taskR","message":"hi"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_S","is_error":true,"content":"The user doesn't want to proceed"}]}}`,
	)
	require.Equal(t, BackgroundAgentStatusCompleted, x.agent(t, "toolu_R").Status)
}

// After a restart the stored task id still ties a resumed run's completion,
// which names only the task, to its agent.
func TestResumedSubagentCompletesAfterRestart(t *testing.T) {
	x := newSubagentHarness(t)
	x.mgr.RestoreAgent(&BackgroundAgent{
		AgentID: "toolu_R", ParentSessionID: x.session.ID, Status: BackgroundAgentStatusFailed,
		ErrorMessage: AppRestartedAgentMessage, Background: true, TaskID: "taskR",
	})

	x.feed(t,
		`{"type":"assistant","parent_tool_use_id":"toolu_R","message":{"role":"assistant","content":[{"type":"text","text":"back at it"}]}}`,
	)
	require.Equal(t, BackgroundAgentStatusRunning, x.agent(t, "toolu_R").Status)

	x.feed(t, `{"type":"system","subtype":"task_notification","task_id":"taskR","status":"completed"}`)
	require.Equal(t, BackgroundAgentStatusCompleted, x.agent(t, "toolu_R").Status)
}

func TestBindTaskStoresTaskID(t *testing.T) {
	x := newSubagentHarness(t)
	launchFinishedBackgroundAgent(t, x)
	require.Equal(t, "taskR", x.agent(t, "toolu_R").TaskID)
}

// After a server restart task ids are unknown; the resumed agent's own output
// still moves it back to running, and a failed agent loses its error.
func TestResumedSubagentOutputReopensIt(t *testing.T) {
	x := newSubagentHarness(t)
	x.mgr.RegisterSubagent("toolu_F", x.session.ID, "general-purpose", "Coverage", "", true)
	x.mgr.FailAgent("toolu_F", AppRestartedAgentMessage)
	require.True(t, x.agent(t, "toolu_F").Interrupted)

	x.feed(t, `{"type":"assistant","parent_tool_use_id":"toolu_F","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_f1","name":"Bash","input":{"command":"make test"}}]}}`)
	agent := x.agent(t, "toolu_F")
	require.Equal(t, BackgroundAgentStatusRunning, agent.Status)
	require.Empty(t, agent.ErrorMessage)
	require.False(t, agent.Interrupted)
}

func TestTaskRunningEventReopensFinishedSubagent(t *testing.T) {
	x := newSubagentHarness(t)
	launchFinishedBackgroundAgent(t, x)

	x.feed(t, `{"type":"system","subtype":"task_updated","task_id":"taskR","patch":{"status":"running"}}`)
	require.Equal(t, BackgroundAgentStatusRunning, x.agent(t, "toolu_R").Status)
}

// A tool result arriving late (no new turn) and a SendMessage to an unknown
// recipient leave finished agents alone.
func TestFinishedSubagentStaysFinishedWithoutResume(t *testing.T) {
	x := newSubagentHarness(t)
	launchFinishedBackgroundAgent(t, x)

	x.feed(t,
		`{"type":"user","parent_tool_use_id":"toolu_R","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_r9","content":"late"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_S","name":"SendMessage","input":{"to":"someone-else","message":"hi"}}]}}`,
	)
	require.Equal(t, BackgroundAgentStatusCompleted, x.agent(t, "toolu_R").Status)
}

// The WebSocket session list says how many subagents are still working, so
// an idle session whose subagents carry on can say so.
func TestSessionListCarriesRunningSubagents(t *testing.T) {
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	storage, err := NewSQLiteSessionStorage(db.GetDB())
	require.NoError(t, err)

	busy, quiet := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{busy, quiet} {
		_, err = db.GetDB().Exec(`INSERT INTO agent_sessions (id) VALUES (?)`, id.String())
		require.NoError(t, err)
	}
	now := time.Now()
	for i, status := range []BackgroundAgentStatus{BackgroundAgentStatusRunning, BackgroundAgentStatusRunning, BackgroundAgentStatusCompleted} {
		require.NoError(t, storage.SaveBackgroundAgent(&BackgroundAgent{
			AgentID: fmt.Sprintf("toolu_%d", i), ParentSessionID: busy, Status: status, CreatedAt: now, UpdatedAt: now,
		}))
	}

	h := &AgentHandler{SessionManager: &SessionManager{Storage: storage}}
	sessions := []LightSession{{ID: busy}, {ID: quiet}}
	h.addLightSubagentCounts(sessions)
	require.Equal(t, 2, sessions[0].SubagentsRunning)
	require.Zero(t, sessions[1].SubagentsRunning)
}

// Closing a session's Claude CLI kills the subagents running inside it.
func TestClosingClaudeCLIStopsItsSubagents(t *testing.T) {
	x := newSubagentHarness(t)
	other := uuid.New()
	x.mgr.RegisterSubagent("toolu_A", x.session.ID, "general-purpose", "A", "", true)
	x.mgr.RegisterSubagent("toolu_B", x.session.ID, "general-purpose", "B", "", true)
	x.mgr.CompleteAgent("toolu_B", "done")
	x.mgr.RegisterSubagent("toolu_C", other, "general-purpose", "C", "", true)

	x.sm.SetClientClosedCallback(x.h.stopSessionSubagents)
	x.session.client = &claude.Client{} // never connected: Close is a no-op
	require.NoError(t, x.sm.closeClaudeClient(x.session, context.Background()))
	require.Nil(t, x.session.client)

	a := x.agent(t, "toolu_A")
	require.Equal(t, BackgroundAgentStatusFailed, a.Status)
	require.True(t, a.Interrupted, "shown as interrupted, not failed")
	require.Equal(t, BackgroundAgentStatusCompleted, x.agent(t, "toolu_B").Status)
	require.Equal(t, BackgroundAgentStatusRunning, x.agent(t, "toolu_C").Status, "other sessions are untouched")
}

// The Subagents view shows what each agent was asked to do.
func TestSubagentKeepsItsPrompt(t *testing.T) {
	x := newSubagentHarness(t)
	long := strings.Repeat("é", maxSubagentPromptChars)
	x.feed(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_P","name":"Agent","input":{"description":"Review","prompt":"  Review PR #12 for SSRF.\nReport a verdict.  "}}]}}`,
		`{"type":"assistant","parent_tool_use_id":"toolu_P","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_N","name":"Agent","input":{"description":"Nested","prompt":"`+long+`"}}]}}`,
	)
	require.Equal(t, "Review PR #12 for SSRF.\nReport a verdict.", x.agent(t, "toolu_P").Prompt)
	nested := x.agent(t, "toolu_N").Prompt
	require.True(t, strings.HasSuffix(nested, "…"))
	require.LessOrEqual(t, len(nested), maxSubagentPromptChars+len("…"))
	require.True(t, utf8.ValidString(nested), "cut on a rune boundary")

	// A view that is already open learns the prompt from the started event.
	x.mu.Lock()
	defer x.mu.Unlock()
	var startedPrompt string
	for _, b := range x.broadcasts {
		if m, ok := b.(BackgroundAgentStartedMessage); ok && m.Agent.AgentID == "toolu_P" {
			startedPrompt = m.Agent.Prompt
		}
	}
	require.Equal(t, "Review PR #12 for SSRF.\nReport a verdict.", startedPrompt)
}

// An agent first seen through its own messages learns its prompt from the
// parent's Agent call later; an open view hears about it.
func TestLatePromptIsAnnounced(t *testing.T) {
	x := newSubagentHarness(t)
	x.mgr.RegisterSubagent("toolu_L", x.session.ID, "", "", "", true)
	x.mgr.RegisterSubagentWithPrompt("toolu_L", x.session.ID, "general-purpose", "Late", "", true, "Do the late thing")
	require.Equal(t, "Do the late thing", x.agent(t, "toolu_L").Prompt)

	x.mu.Lock()
	defer x.mu.Unlock()
	last, ok := x.broadcasts[len(x.broadcasts)-1].(BackgroundAgentStartedMessage)
	require.True(t, ok)
	require.Equal(t, "Do the late thing", last.Agent.Prompt)
}

// A subagent shows the model its launch asked for until its own messages
// report the one it runs on; a later requested model never overrides that.
func TestSubagentModelRequestedThenActual(t *testing.T) {
	x := newSubagentHarness(t)
	x.feed(t,
		`{"type":"assistant","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_M","name":"Agent","input":{"description":"Scan","prompt":"Scan it","model":"haiku"}}]}}`,
	)
	agent := x.agent(t, "toolu_M")
	require.Equal(t, "haiku", agent.Model, "the parent's own model is not the subagent's")
	require.Equal(t, ModelSourceRequested, agent.ModelSource)

	x.feed(t,
		`{"type":"assistant","parent_tool_use_id":"toolu_M","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"x"}]}}`,
	)
	require.Equal(t, "haiku", x.agent(t, "toolu_M").Model, "placeholder models are ignored")

	x.feed(t,
		`{"type":"assistant","parent_tool_use_id":"toolu_M","message":{"role":"assistant","model":"claude-haiku-4-5-20251001","content":[{"type":"tool_use","id":"toolu_N","name":"Agent","input":{"description":"Nested","prompt":"p","model":"sonnet"}}]}}`,
	)
	agent = x.agent(t, "toolu_M")
	require.Equal(t, "claude-haiku-4-5-20251001", agent.Model)
	require.Equal(t, ModelSourceActual, agent.ModelSource)
	nested := x.agent(t, "toolu_N")
	require.Equal(t, "sonnet", nested.Model)
	require.Equal(t, ModelSourceRequested, nested.ModelSource)

	require.False(t, x.mgr.SetModel("toolu_M", "opus", ModelSourceRequested))
	require.Equal(t, "claude-haiku-4-5-20251001", x.agent(t, "toolu_M").Model)
	require.False(t, x.mgr.SetModel("toolu_M", "claude-haiku-4-5-20251001", ModelSourceActual), "unchanged: no event")

	// An open view hears about the change through a re-announce.
	x.mu.Lock()
	defer x.mu.Unlock()
	var announced string
	for _, b := range x.broadcasts {
		if m, ok := b.(BackgroundAgentStartedMessage); ok && m.Agent.AgentID == "toolu_M" {
			announced = m.Agent.Model + "/" + m.Agent.ModelSource
		}
	}
	require.Equal(t, "claude-haiku-4-5-20251001/actual", announced)
}

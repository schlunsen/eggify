package agents

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	fastws "github.com/fasthttp/websocket"
	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
)

// seedWorkerOf adds a build worker started by parent.
func seedWorkerOf(t *testing.T, w *wsHarness, parent uuid.UUID, owner string) *AgentSession {
	t.Helper()
	return seedOwned(t, w, owner, func(s *Session) {
		s.Options.SpawnedBySessionID = &parent
		s.Options.Worker = &WorkerConfig{Task: "Add the changelog", Mode: "build"}
	})
}

// subscribe registers conn for a session, the way an open chat does.
func (w *wsHarness) subscribe(t *testing.T, conn *fastws.Conn, session uuid.UUID) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{"type": "subscribe_session", "session_id": session.String()}); err != nil {
		t.Fatal(err)
	}
	w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "session_subscribed" })
}

// expectNone fails if a message matching unwanted arrives. It gives messages
// sent from goroutines time to arrive, then reads up to a pong.
func (w *wsHarness) expectNone(t *testing.T, conn *fastws.Conn, unwanted func(map[string]any) bool) {
	t.Helper()
	time.Sleep(150 * time.Millisecond)
	if err := conn.WriteJSON(map[string]any{"type": "ping"}); err != nil {
		t.Fatal(err)
	}
	w.expect(t, conn, func(m map[string]any) bool {
		if unwanted(m) {
			t.Fatalf("unexpected message: %v", m)
		}
		return m["type"] == "pong"
	})
}

func isType(name string) func(map[string]any) bool {
	return func(m map[string]any) bool { return m["type"] == name }
}

// wireLinkedSessions connects the manager to the handler the way
// NewAgentHandler does.
func wireLinkedSessions(w *wsHarness) {
	w.sm.SetLinkedSessionCallback(func(parent uuid.UUID, owner *string, msg interface{}) {
		w.h.sendToParentConnections(parent, uuid.Nil, owner, msg)
	})
}

func assistantMessage(blocks ...types.ContentBlock) *types.AssistantMessage {
	return &types.AssistantMessage{Type: "assistant", Content: blocks}
}

func TestLinkedSessionUpdateGoesToTheParentsConnections(t *testing.T) {
	w := newWSHarness(t)
	worker := seedWorkerOf(t, w, w.session.ID, "")

	parentConn := w.dial(t, "")
	w.subscribe(t, parentConn, w.session.ID)
	other := seedSwitchSession(t, w.sm, "claude", "opus", nil)
	otherConn := w.dial(t, "")
	w.subscribe(t, otherConn, other.ID)

	worker.Status = SessionStatusProcessing
	w.h.broadcastSessionStateUpdate(worker)

	got := w.expect(t, parentConn, isType("linked_session_updated"))
	if got["session_id"] != w.session.ID.String() || got["linked_session_id"] != worker.ID.String() {
		t.Fatalf("wrong ids: %v", got)
	}
	if got["status"] != "processing" {
		t.Fatalf("status = %v, want processing", got["status"])
	}
	session, _ := got["session"].(map[string]any)
	if session["id"] != worker.ID.String() {
		t.Fatalf("session payload is not the worker: %v", session)
	}
	options, _ := session["options"].(map[string]any)
	if options["spawned_by_session_id"] != w.session.ID.String() {
		t.Fatalf("session payload lacks its parent: %v", session)
	}
	if _, ok := options["worker"].(map[string]any); !ok {
		t.Fatalf("session payload lacks the worker config: %v", session)
	}

	// A connection of an unrelated session hears nothing.
	w.expectNone(t, otherConn, isType("linked_session_updated"))
}

func TestLinkedSessionUpdateSkipsConnectionsOfTheChild(t *testing.T) {
	w := newWSHarness(t)
	worker := seedWorkerOf(t, w, w.session.ID, "")

	// This connection has both sessions (a page after a reload): it gets the
	// ordinary session_updated and no second copy.
	conn := w.dial(t, "")
	w.subscribe(t, conn, w.session.ID)
	w.subscribe(t, conn, worker.ID)

	worker.Status = SessionStatusProcessing
	w.h.broadcastSessionStateUpdate(worker)

	w.expect(t, conn, func(m map[string]any) bool {
		if m["type"] == "linked_session_updated" {
			t.Fatalf("got a linked update on a connection registered for the child: %v", m)
		}
		return m["type"] == "session_updated" && m["session_id"] == worker.ID.String() && m["status"] == "processing"
	})
	w.expectNone(t, conn, isType("linked_session_updated"))
}

func TestLinkedSessionUpdateIsNeverBuffered(t *testing.T) {
	w := newWSHarness(t)
	worker := seedWorkerOf(t, w, w.session.ID, "")

	// Nobody is connected to the parent.
	worker.Status = SessionStatusProcessing
	w.h.broadcastSessionStateUpdate(worker)
	time.Sleep(50 * time.Millisecond)

	w.h.sessionBuffersMu.RLock()
	_, buffered := w.h.sessionBuffers[w.session.ID]
	w.h.sessionBuffersMu.RUnlock()
	if buffered {
		t.Fatal("a linked update was buffered for the parent")
	}

	conn := w.dial(t, "")
	w.subscribe(t, conn, w.session.ID)
	w.expectNone(t, conn, isType("linked_session_updated"))
}

func TestLinkedSessionUpdateIgnoresOrdinarySessions(t *testing.T) {
	w := newWSHarness(t)
	plain := seedSwitchSession(t, w.sm, "claude", "opus", nil)
	conn := w.dial(t, "")
	w.subscribe(t, conn, w.session.ID)

	w.h.broadcastSessionStateUpdate(plain)
	w.expectNone(t, conn, isType("linked_session_updated"))
}

func TestRedTeam_LinkedSession_OtherUsersConnectionLearnsNothing(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	wireLinkedSessions(w)
	// The parent has no owner (everyone may open it); the worker is bob's.
	worker := seedWorkerOf(t, w, w.session.ID, "bob")
	seedOwned(t, w, "alice", nil)

	alice := w.dial(t, "alice")
	w.subscribe(t, alice, w.session.ID)
	bob := w.dial(t, "bob")
	w.subscribe(t, bob, w.session.ID)

	worker.Status = SessionStatusProcessing
	w.h.broadcastSessionStateUpdate(worker)
	w.sm.observeWorkerMessage(worker, assistantMessage(&types.TextBlock{Type: "text", Text: "Reading the handlers"}))

	w.expect(t, bob, isType("linked_session_updated"))
	w.expect(t, bob, isType("worker_activity"))
	w.expectNone(t, alice, func(m map[string]any) bool {
		return m["type"] == "linked_session_updated" || m["type"] == "worker_activity"
	})
}

func TestWorkerOutputLines(t *testing.T) {
	at := time.UnixMilli(1_800_000_000_000)
	yes := true

	lines := workerOutputLines(assistantMessage(
		&types.ThinkingBlock{Type: "thinking", Thinking: "hmm"},
		&types.TextBlock{Type: "text", Text: "First I read the file.\n\nThen I edit it."},
		&types.ToolUseBlock{Type: "tool_use", ID: "t1", Name: "Bash", Input: map[string]interface{}{"command": "go test ./..."}},
	), at)
	var texts []string
	for _, l := range lines {
		texts = append(texts, l.Text)
		if !l.At.Equal(at) {
			t.Fatalf("line %q has time %v, want %v", l.Text, l.At, at)
		}
	}
	want := []string{"First I read the file.", "Then I edit it.", "TOOL:Bash: go test ./..."}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("assistant lines = %q, want %q", texts, want)
	}

	result := workerOutputLines(&types.UserMessage{Type: "user", Content: []types.ContentBlock{
		&types.ToolResultBlock{Type: "tool_result", ToolUseID: "t1", Content: "FAIL\texample\nexit status 1", IsError: &yes},
	}}, at)
	if len(result) != 4 || result[0].Text != "RESULT_START" || result[3].Text != "RESULT_END" {
		t.Fatalf("result lines = %+v", result)
	}
	if !result[1].IsError || !result[2].IsError || result[0].IsError {
		t.Fatalf("error flags wrong: %+v", result)
	}

	// The prompt a worker receives and results without text add nothing.
	if got := workerOutputLines(&types.UserMessage{Type: "user", Content: "Build the feature"}, at); len(got) != 0 {
		t.Fatalf("a plain user message gave lines: %+v", got)
	}
	if got := workerOutputLines(&types.UserMessage{Type: "user", Content: []types.ContentBlock{
		&types.ToolResultBlock{Type: "tool_result", ToolUseID: "t2", Content: ""},
	}}, at); len(got) != 0 {
		t.Fatalf("an empty result gave lines: %+v", got)
	}
	if got := workerOutputLines(&types.ResultMessage{Type: "result"}, at); len(got) != 0 {
		t.Fatalf("a result message gave lines: %+v", got)
	}
}

func TestWorkerActivityLogKeepsTheNewestLines(t *testing.T) {
	var log workerActivityLog
	base := time.UnixMilli(1_800_000_000_000)
	for i := 0; i < maxRecentOutputLines+25; i++ {
		log.add([]OutputLine{{Text: fmt.Sprintf("line %d", i), At: base.Add(time.Duration(i) * time.Second)}})
	}
	lines, at := log.snapshot()
	if len(lines) != maxRecentOutputLines || len(at) != maxRecentOutputLines {
		t.Fatalf("kept %d lines / %d times, want %d", len(lines), len(at), maxRecentOutputLines)
	}
	if lines[0] != "line 25" || lines[len(lines)-1] != fmt.Sprintf("line %d", maxRecentOutputLines+24) {
		t.Fatalf("kept %q … %q", lines[0], lines[len(lines)-1])
	}
	if at[0] != base.Add(25*time.Second).UnixMilli() {
		t.Fatalf("times are not parallel to the lines: %d", at[0])
	}

	// A snapshot is a copy.
	lines[0] = "changed"
	if again, _ := log.snapshot(); again[0] != "line 25" {
		t.Fatal("snapshot shares its slice with the log")
	}

	var empty workerActivityLog
	if l, a := empty.snapshot(); l == nil || a == nil {
		t.Fatal("an empty log must give empty lists, not null")
	}
}

func TestObserveWorkerMessageSendsActivityToTheParent(t *testing.T) {
	w := newWSHarness(t)
	worker := seedWorkerOf(t, w, w.session.ID, "")

	type sent struct {
		parent uuid.UUID
		msg    WorkerActivityMessage
	}
	var mu sync.Mutex
	var events []sent
	w.sm.SetLinkedSessionCallback(func(parent uuid.UUID, _ *string, msg interface{}) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, sent{parent, msg.(WorkerActivityMessage)})
	})

	// One line: no lines list.
	w.sm.observeWorkerMessage(worker, assistantMessage(&types.ToolUseBlock{
		Type: "tool_use", ID: "t1", Name: "Read", Input: map[string]interface{}{"file_path": "/repo/CHANGELOG.md"},
	}))
	// Several lines, one of them an error.
	yes := true
	w.sm.observeWorkerMessage(worker, &types.UserMessage{Type: "user", Content: []types.ContentBlock{
		&types.ToolResultBlock{Type: "tool_result", ToolUseID: "t1", Content: "no such file", IsError: &yes},
	}})
	// Nothing to show: no event.
	w.sm.observeWorkerMessage(worker, &types.ResultMessage{Type: "result"})

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	first, second := events[0], events[1]
	if first.parent != w.session.ID || first.msg.SessionID != w.session.ID || first.msg.WorkerSessionID != worker.ID {
		t.Fatalf("wrong ids: %+v", first)
	}
	if first.msg.Type != MessageTypeWorkerActivity || first.msg.Output != "TOOL:Read: /repo/CHANGELOG.md" {
		t.Fatalf("first event = %+v", first.msg)
	}
	if first.msg.Lines != nil || first.msg.LinesAt != nil || first.msg.IsError || first.msg.At == 0 {
		t.Fatalf("a one-line event carries lists, an error or no time: %+v", first.msg)
	}
	if second.msg.Output != "RESULT_END" || !second.msg.IsError {
		t.Fatalf("second event = %+v", second.msg)
	}
	if strings.Join(second.msg.Lines, "|") != "RESULT_START|no such file|RESULT_END" || len(second.msg.LinesAt) != 3 {
		t.Fatalf("second event's lines = %q / %v", second.msg.Lines, second.msg.LinesAt)
	}

	activity := w.sm.WorkerActivity(w.session.ID)
	if len(activity) != 1 || activity[0].SessionID != worker.ID {
		t.Fatalf("WorkerActivity = %+v", activity)
	}
	if got := strings.Join(activity[0].Lines, "|"); got != "TOOL:Read: /repo/CHANGELOG.md|RESULT_START|no such file|RESULT_END" {
		t.Fatalf("kept lines = %q", got)
	}
	if len(activity[0].LinesAt) != 4 {
		t.Fatalf("kept %d times for 4 lines", len(activity[0].LinesAt))
	}
}

func TestObserveWorkerMessageLeavesOtherSessionsAlone(t *testing.T) {
	w := newWSHarness(t)
	called := false
	w.sm.SetLinkedSessionCallback(func(uuid.UUID, *string, interface{}) { called = true })
	text := assistantMessage(&types.TextBlock{Type: "text", Text: "Working"})

	// An ordinary session, and a loop session another session started.
	w.sm.observeWorkerMessage(w.session, text)
	loop := seedSwitchSession(t, w.sm, "claude", "opus", func(s *Session) {
		s.Options.SpawnedBySessionID = &w.session.ID
	})
	w.sm.observeWorkerMessage(loop, text)

	if called {
		t.Fatal("a session that is not a worker sent activity")
	}
	if got := w.sm.WorkerActivity(w.session.ID); len(got) != 0 {
		t.Fatalf("WorkerActivity lists sessions that are not workers: %+v", got)
	}
}

func TestWorkerActivityReachesTheParentsConnection(t *testing.T) {
	w := newWSHarness(t)
	wireLinkedSessions(w)
	worker := seedWorkerOf(t, w, w.session.ID, "")
	conn := w.dial(t, "")
	w.subscribe(t, conn, w.session.ID)

	w.sm.observeWorkerMessage(worker, assistantMessage(&types.ToolUseBlock{
		Type: "tool_use", ID: "t1", Name: "Bash", Input: map[string]interface{}{"command": "go build ./..."},
	}))

	got := w.expect(t, conn, isType("worker_activity"))
	if got["session_id"] != w.session.ID.String() || got["worker_session_id"] != worker.ID.String() {
		t.Fatalf("wrong ids: %v", got)
	}
	if got["output"] != "TOOL:Bash: go build ./..." {
		t.Fatalf("output = %v", got["output"])
	}
	if _, has := got["lines"]; has {
		t.Fatalf("a one-line event carries lines: %v", got)
	}
}

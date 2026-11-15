package agents

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
)

// A worker's activity is what its own messages say it is doing, kept in the
// line format subagents use (text lines, "TOOL:<name>: <target>", result
// blocks) so the session that started it can show the worker like one of its
// subagents. The newest lines live in memory on the worker's session: after a
// restart the log starts empty and fills again as the worker goes on.

// workerActivityLog holds a worker's newest output lines and their times.
type workerActivityLog struct {
	mu    sync.Mutex
	lines []string
	at    []int64 // unix ms, parallel to lines
}

// add appends lines, keeping the newest maxRecentOutputLines.
func (l *workerActivityLog) add(lines []OutputLine) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range lines {
		l.lines = append(l.lines, line.Text)
		l.at = append(l.at, line.At.UnixMilli())
	}
	if extra := len(l.lines) - maxRecentOutputLines; extra > 0 {
		l.lines = append([]string(nil), l.lines[extra:]...)
		l.at = append([]int64(nil), l.at[extra:]...)
	}
}

// snapshot returns copies of the kept lines and their times.
func (l *workerActivityLog) snapshot() ([]string, []int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.lines...), append([]int64{}, l.at...)
}

// WorkerActivity is one worker's kept output for the session that started it.
type WorkerActivity struct {
	SessionID uuid.UUID `json:"session_id"`
	Lines     []string  `json:"lines"`
	LinesAt   []int64   `json:"lines_at"`
}

// workerOutputLines turns one of a worker's messages into output lines, the
// way a subagent's messages are (handleSubagentMessageAt).
func workerOutputLines(msg types.Message, at time.Time) []OutputLine {
	var out []OutputLine
	switch m := msg.(type) {
	case *types.AssistantMessage:
		for _, block := range m.Content {
			switch b := block.(type) {
			case *types.TextBlock:
				for _, line := range outputTextLines(b.Text) {
					out = append(out, OutputLine{Text: line, At: at})
				}
			case *types.ToolUseBlock:
				out = append(out, OutputLine{Text: subagentToolLine(b.Name, b.Input), At: at})
			}
		}
	case *types.UserMessage:
		blocks, ok := m.Content.([]types.ContentBlock)
		if !ok {
			return nil
		}
		for _, block := range blocks {
			result, ok := block.(*types.ToolResultBlock)
			if !ok {
				continue
			}
			text := resultPreview(toolResultText(result.Content))
			if text == "" {
				continue
			}
			out = append(out, resultOutputLines(text, result.IsError != nil && *result.IsError, at)...)
		}
	}
	return out
}

// observeWorkerMessage records what a worker's message says it is doing and
// sends it to the live connections of the session that started the worker.
// Sessions that are not workers are left alone.
func (sm *SessionManager) observeWorkerMessage(session *AgentSession, msg types.Message) {
	sm.mu.RLock()
	isWorker := session.Options.Worker != nil
	var parent uuid.UUID
	if session.Options.SpawnedBySessionID != nil {
		parent = *session.Options.SpawnedBySessionID
	}
	owner := session.OwnerUserID
	sm.mu.RUnlock()
	if !isWorker || parent == uuid.Nil || parent == session.ID {
		return
	}

	lines := workerOutputLines(msg, time.Now())
	if len(lines) == 0 {
		return
	}
	session.workerLog.add(lines)

	// One event per message, shaped like background_agent_output.
	last := lines[len(lines)-1]
	event := WorkerActivityMessage{
		BaseMessage:     BaseMessage{Type: MessageTypeWorkerActivity},
		SessionID:       parent,
		WorkerSessionID: session.ID,
		Output:          last.Text,
		At:              last.At.UnixMilli(),
	}
	for _, line := range lines {
		if line.IsError {
			event.IsError = true
		}
	}
	if len(lines) > 1 {
		event.Lines = make([]string, len(lines))
		event.LinesAt = make([]int64, len(lines))
		for i, line := range lines {
			event.Lines[i] = line.Text
			event.LinesAt[i] = line.At.UnixMilli()
		}
	}
	sm.sendToParentSession(parent, owner, event)
}

// WorkerActivity returns the kept output of every loaded worker the session
// parent started, in no particular order.
func (sm *SessionManager) WorkerActivity(parent uuid.UUID) []WorkerActivity {
	sm.mu.RLock()
	var workers []*AgentSession
	for _, s := range sm.sessions {
		if s.Options.Worker != nil && s.Options.SpawnedBySessionID != nil && *s.Options.SpawnedBySessionID == parent {
			workers = append(workers, s)
		}
	}
	sm.mu.RUnlock()

	out := make([]WorkerActivity, 0, len(workers))
	for _, w := range workers {
		lines, at := w.workerLog.snapshot()
		out = append(out, WorkerActivity{SessionID: w.ID, Lines: lines, LinesAt: at})
	}
	return out
}

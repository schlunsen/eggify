package agents

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/logging"
)

// Workflow subagents.
//
// Agents started by Claude Code's Workflow tool never reach the session's
// stream: the CLI reports the whole workflow as one local_workflow task, and
// its task_progress events (whose per-agent workflow_progress the SDK drops)
// are all the parent sees. The Workflow tool's launch result names the run's
// transcript folder instead:
//
//	{"type":"user", "message":{tool_result for the Workflow call},
//	 "tool_use_result":{"status":"async_launched", "taskType":"local_workflow",
//	                    "taskId":"wxl...", "transcriptDir":".../subagents/workflows/wf_..."}}
//
// The folder holds journal.jsonl ({"type":"started"|"result"|"failed",
// "agentId", "label", "result"}) and one agent-<id>.jsonl transcript per agent,
// whose assistant/user lines have the shape of stream messages. A watcher
// follows the folder while the workflow task runs: agents are registered when
// they start, their transcripts go through handleSubagentMessage like any
// subagent's messages, and the journal completes or fails them. When the task
// ends (task_notification / terminal task_updated) the folder is read once
// more and agents left running are settled; when the session's CLI closes the
// watchers stop and stopSessionSubagents marks the agents stopped.

// workflowPollInterval is how often a running workflow's folder is read
// (tests shorten it).
var workflowPollInterval = time.Second

const (
	// workflowMaxWatch stops a watcher whose workflow never reported its end.
	workflowMaxWatch = 24 * time.Hour
	// workflowReadCap bounds what one poll reads from a file; the rest
	// follows on the next poll.
	workflowReadCap = 8 << 20
	// workflowSubagentType is the type shown for workflow agents.
	workflowSubagentType = "workflow"
)

var (
	workflowRunDirPattern  = regexp.MustCompile(`^wf_[A-Za-z0-9_-]+$`)
	workflowAgentIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// workflowTaskFrame ends the harness framing around a workflow agent's
// computed task; the task follows, indented by two spaces.
const workflowTaskFrame = "The computed task text follows:\n"

// workflowTracker holds the running workflow watchers, by task id.
type workflowTracker struct {
	mu     sync.Mutex
	byTask map[string]*workflowWatch
}

func (h *AgentHandler) workflows() *workflowTracker {
	h.workflowOnce.Do(func() {
		h.workflowSt = &workflowTracker{byTask: map[string]*workflowWatch{}}
	})
	return h.workflowSt
}

// workflowWatch follows one workflow run's transcript folder.
type workflowWatch struct {
	h         *AgentHandler
	sessionID uuid.UUID
	taskID    string
	dir       string

	stop     chan struct{} // closed: stop without settling (the CLI closed)
	finished chan string   // the workflow's terminal status: read once more, settle, stop
	done     chan struct{} // closed when the goroutine has returned
	stopOnce sync.Once

	// Owned by the goroutine.
	journal workflowTail
	agents  map[string]*workflowAgent
	order   []string
}

// workflowAgent is one agent of a watched workflow.
type workflowAgent struct {
	transcript workflowTail
	prompted   bool // its task text was recorded
	ended      bool // it was finished here, or was already finished when the watch began
}

// workflowTail reads whole lines appended to a file since the last read.
type workflowTail struct {
	offset int64
}

// next returns the complete lines added since the last call. A partial last
// line is left for the next call; a file that shrank is read from the start.
func (t *workflowTail) next(path string) [][]byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	if info.Size() < t.offset {
		t.offset = 0
	}
	if info.Size() == t.offset {
		return nil
	}
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil
	}
	buf, err := io.ReadAll(io.LimitReader(f, workflowReadCap))
	if err != nil {
		return nil
	}
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		if len(buf) == workflowReadCap {
			// One line longer than the cap: skip it rather than stall.
			t.offset += int64(len(buf))
		}
		return nil
	}
	t.offset += int64(end + 1)
	var lines [][]byte
	for _, line := range bytes.Split(buf[:end], []byte{'\n'}) {
		if line = bytes.TrimSpace(line); len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}

// skipToEnd moves past everything the file holds now.
func (t *workflowTail) skipToEnd(path string) {
	if info, err := os.Stat(path); err == nil {
		t.offset = info.Size()
	}
}

// isWorkflowRunDir reports whether dir looks like a workflow run's transcript
// folder (<...>/subagents/workflows/wf_<id>), the only folders watched.
func isWorkflowRunDir(dir string) bool {
	if dir == "" || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return false
	}
	parent := filepath.Dir(dir)
	return workflowRunDirPattern.MatchString(filepath.Base(dir)) &&
		filepath.Base(parent) == "workflows" &&
		filepath.Base(filepath.Dir(parent)) == "subagents"
}

// observeWorkflowLaunch starts following a workflow whose launch result msg is.
func (h *AgentHandler) observeWorkflowLaunch(sessionID uuid.UUID, msg *types.UserMessage) {
	result := msg.ToolUseResult
	if result == nil {
		return
	}
	if taskType, _ := result["taskType"].(string); taskType != "local_workflow" {
		return
	}
	taskID, _ := result["taskId"].(string)
	dir, _ := result["transcriptDir"].(string)
	if taskID == "" {
		return
	}
	if !isWorkflowRunDir(dir) {
		logging.Warning("Workflow %s: not following transcript folder %q", taskID, dir)
		return
	}
	h.watchWorkflow(sessionID, taskID, dir)
}

// watchWorkflow starts a watcher for a workflow task unless one runs already.
func (h *AgentHandler) watchWorkflow(sessionID uuid.UUID, taskID, dir string) {
	tracker := h.workflows()
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if _, running := tracker.byTask[taskID]; running {
		return
	}
	w := &workflowWatch{
		h:         h,
		sessionID: sessionID,
		taskID:    taskID,
		dir:       dir,
		stop:      make(chan struct{}),
		finished:  make(chan string, 1),
		done:      make(chan struct{}),
		agents:    map[string]*workflowAgent{},
	}
	tracker.byTask[taskID] = w
	logging.Info("🧭 Following workflow %s for session %s (%s)", taskID, sessionID, dir)
	go w.run()
}

// finishWorkflow tells a task's watcher the workflow ended with status. It
// reports whether the task was a watched workflow.
func (h *AgentHandler) finishWorkflow(taskID, status string) bool {
	tracker := h.workflows()
	tracker.mu.Lock()
	w := tracker.byTask[taskID]
	tracker.mu.Unlock()
	if w == nil {
		return false
	}
	select {
	case w.finished <- status:
	default: // already told
	}
	return true
}

// stopSessionWorkflows stops the session's watchers and waits (briefly) until
// they no longer touch its agents, so the caller can settle them.
func (h *AgentHandler) stopSessionWorkflows(sessionID uuid.UUID) {
	tracker := h.workflows()
	tracker.mu.Lock()
	var stopped []*workflowWatch
	for _, w := range tracker.byTask {
		if w.sessionID == sessionID {
			w.stopOnce.Do(func() { close(w.stop) })
			stopped = append(stopped, w)
		}
	}
	tracker.mu.Unlock()
	for _, w := range stopped {
		select {
		case <-w.done:
		case <-time.After(2 * time.Second):
			logging.Warning("Workflow %s: watcher did not stop in time", w.taskID)
		}
	}
}

func (w *workflowWatch) run() {
	defer func() {
		if r := recover(); r != nil {
			logging.Error("Workflow %s watcher panicked: %v", w.taskID, r)
		}
		tracker := w.h.workflows()
		tracker.mu.Lock()
		if tracker.byTask[w.taskID] == w {
			delete(tracker.byTask, w.taskID)
		}
		tracker.mu.Unlock()
		close(w.done)
	}()

	ticker := time.NewTicker(workflowPollInterval)
	defer ticker.Stop()
	deadline := time.NewTimer(workflowMaxWatch)
	defer deadline.Stop()

	w.poll()
	for {
		select {
		case <-w.stop:
			return
		case status := <-w.finished:
			w.poll()
			w.settle(status)
			return
		case <-deadline.C:
			logging.Warning("Workflow %s: no end reported after %s, no longer followed", w.taskID, workflowMaxWatch)
			return
		case <-ticker.C:
			w.poll()
		}
	}
}

// workflowJournalEntry is one line of a workflow's journal.jsonl.
type workflowJournalEntry struct {
	Type    string          `json:"type"`
	AgentID string          `json:"agentId"`
	Label   string          `json:"label"`
	Phase   string          `json:"phase"`
	Result  json.RawMessage `json:"result"`
}

// poll reads what the journal and the transcripts gained since the last poll.
// Starts are applied first and endings last, so an agent's output is in
// before it is completed.
func (w *workflowWatch) poll() {
	var endings []workflowJournalEntry
	for _, line := range w.journal.next(filepath.Join(w.dir, "journal.jsonl")) {
		var entry workflowJournalEntry
		if json.Unmarshal(line, &entry) != nil || !workflowAgentIDPattern.MatchString(entry.AgentID) {
			continue
		}
		switch entry.Type {
		case "started":
			w.agentStarted(entry)
		case "result", "failed":
			endings = append(endings, entry)
		}
	}
	for _, id := range w.order {
		w.readTranscript(id, w.agents[id])
	}
	for _, entry := range endings {
		w.agentEnded(entry)
	}
}

func (w *workflowWatch) agentStarted(entry workflowJournalEntry) {
	mgr := w.h.BackgroundAgentMgr
	if agent, known := w.agents[entry.AgentID]; known {
		// Started again (a retry under the same id): follow it again.
		if agent.ended && mgr.ReopenAgent(entry.AgentID) {
			agent.ended = false
		}
		return
	}
	agent := &workflowAgent{}
	if existing, ok := mgr.GetAgent(entry.AgentID); ok && existing.Status != BackgroundAgentStatusRunning {
		// Already finished before this watch began (a resumed run replays
		// the journal): leave it and its transcript as they are.
		agent.ended = true
		agent.prompted = true
		agent.transcript.skipToEnd(w.transcriptPath(entry.AgentID))
	} else {
		description := entry.Label
		if description == "" {
			description = "Workflow agent"
		}
		mgr.RegisterSubagent(entry.AgentID, w.sessionID, workflowSubagentType, description, "", true)
	}
	w.agents[entry.AgentID] = agent
	w.order = append(w.order, entry.AgentID)
}

func (w *workflowWatch) transcriptPath(agentID string) string {
	return filepath.Join(w.dir, "agent-"+agentID+".jsonl")
}

// readTranscript feeds an agent's new transcript lines to the tracker.
func (w *workflowWatch) readTranscript(agentID string, agent *workflowAgent) {
	if agent.ended {
		return
	}
	for _, line := range agent.transcript.next(w.transcriptPath(agentID)) {
		var head struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &head) != nil || (head.Type != "assistant" && head.Type != "user") {
			continue
		}
		if head.Type == "user" && !agent.prompted {
			var prompt string
			if json.Unmarshal(head.Message.Content, &prompt) == nil && prompt != "" {
				agent.prompted = true
				w.h.BackgroundAgentMgr.RegisterSubagentWithPrompt(agentID, w.sessionID, workflowSubagentType,
					"", "", true, workflowTaskText(prompt))
				continue
			}
		}
		msg, err := types.UnmarshalMessage(line)
		if err != nil {
			continue
		}
		// The transcript line's own time: a watcher that catches up later
		// (or a resumed run reading old lines) still shows when it happened.
		at, _ := time.Parse(time.RFC3339Nano, head.Timestamp)
		w.h.handleSubagentMessageAt(w.sessionID, agentID, msg, at)
	}
}

func (w *workflowWatch) agentEnded(entry workflowJournalEntry) {
	agent := w.agents[entry.AgentID]
	if agent == nil || agent.ended {
		return
	}
	agent.ended = true
	if entry.Type == "failed" {
		finishSubagent(w.h.BackgroundAgentMgr, entry.AgentID, "failed", "")
		return
	}
	finishSubagent(w.h.BackgroundAgentMgr, entry.AgentID, types.TaskNotificationStatusCompleted,
		workflowResultText(entry.Result))
}

// settle finishes the agents still running when the workflow ended.
func (w *workflowWatch) settle(status string) {
	if status == types.TaskNotificationStatusCompleted {
		status = "stopped" // the workflow returned without waiting for them
	}
	for _, id := range w.order {
		if agent := w.agents[id]; !agent.ended {
			agent.ended = true
			finishSubagent(w.h.BackgroundAgentMgr, id, status, "the workflow ended")
		}
	}
	logging.Info("🧭 Workflow %s ended", w.taskID)
}

// workflowTaskText strips the harness framing from a workflow agent's task.
func workflowTaskText(prompt string) string {
	i := strings.Index(prompt, workflowTaskFrame)
	if i < 0 {
		return prompt
	}
	lines := strings.Split(prompt[i+len(workflowTaskFrame):], "\n")
	for j, line := range lines {
		lines[j] = strings.TrimPrefix(line, "  ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// workflowResultText is an agent's journal result as text.
func workflowResultText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") == nil {
		return pretty.String()
	}
	return string(raw)
}

package agents

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/logging"
)

// Subagent traffic routing.
//
// The Claude CLI streams a subagent's own messages through the parent session's
// stream, tagged with a top-level parent_tool_use_id naming the Agent/Task
// tool_use that launched it, and reports the subagent's lifecycle as task_*
// events keyed by a task id:
//
//	{"type":"assistant", "parent_tool_use_id":"toolu_X", ...}      subagent turn
//	{"type":"user", "message":{tool_result for toolu_X},
//	 "tool_use_result":{"status":"async_launched","agentId":"a0b0..."}}   launch
//	{"type":"system", "subtype":"task_notification", "task_id":"a0b0...",
//	 "status":"completed", ...}                                      completion
//
// task_notification (or a terminal task_updated) is the only completion signal a
// background (run_in_background) agent gets. The SDK parses these into typed
// Task* messages that keep the task id but not the tool_use id, so the task id is
// bound to the agent when its launch result arrives.
//
// None of this belongs to the parent conversation. The session reader takes it
// out of the parent stream before persistence and broadcast, so it is neither
// saved as the parent's messages nor shown in the parent chat, and it does not
// flip an idle parent back to processing. The reader also shows every parent
// message to the tracker once, whether or not a browser is connected, so
// launches are registered and foreground agents completed exactly once.

// subagentTrafficCallback receives each message the session reader sees.
// parentToolUseID is set for a subagent's own messages.
type subagentTrafficCallback func(sessionID uuid.UUID, parentToolUseID string, msg types.Message)

// launchAgentIDPattern finds the task id in a background launch result's text.
var launchAgentIDPattern = regexp.MustCompile(`agentId:\s*([A-Za-z0-9_-]+)`)

// subagentParentID returns the Agent/Task tool_use id a message was produced
// under, or "" when the message belongs to the parent conversation.
func subagentParentID(msg types.Message) string {
	var parent *string
	switch m := msg.(type) {
	case *types.AssistantMessage:
		parent = m.ParentToolUseID
	case *types.UserMessage:
		parent = m.ParentToolUseID
	}
	if parent == nil {
		return ""
	}
	return *parent
}

// taskEvent describes a task_* lifecycle event.
type taskEvent struct {
	taskID    string
	toolUseID string // only when the event carries it
	status    string // terminal status, or "" while the task is still going
	detail    string
	resumed   bool // a task_started or task_updated to running/pending: the task is (again) running
}

// parseTaskEvent recognises task lifecycle events, both as the SDK's typed
// messages and as plain system messages (older SDKs, background_tasks_changed).
func parseTaskEvent(msg types.Message) (taskEvent, bool) {
	switch m := msg.(type) {
	case *types.TaskStartedMessage:
		return taskEvent{taskID: m.TaskID, resumed: true}, true
	case *types.TaskProgressMessage:
		return taskEvent{taskID: m.TaskID}, true
	case *types.TaskNotificationMessage:
		ev := taskEvent{taskID: m.TaskID, status: m.Status}
		if m.Error != nil {
			ev.detail = *m.Error
		}
		ev.toolUseID, _ = m.Data["tool_use_id"].(string)
		return ev, true
	case *types.TaskUpdatedMessage:
		ev := taskEvent{taskID: m.TaskID}
		if m.IsTerminal() {
			ev.status = m.Status
		}
		ev.resumed = isActiveTaskStatus(m.Status)
		return ev, true
	case *types.SystemMessage:
		if m.Subtype != "background_tasks_changed" && !strings.HasPrefix(m.Subtype, "task_") {
			return taskEvent{}, false
		}
		ev := taskEvent{}
		ev.taskID, _ = m.Data["task_id"].(string)
		ev.toolUseID, _ = m.Data["tool_use_id"].(string)
		switch m.Subtype {
		case "task_started":
			ev.resumed = true
		case "task_notification":
			ev.status, _ = m.Data["status"].(string)
			ev.detail, _ = m.Data["summary"].(string)
		case "task_updated":
			if patch, ok := m.Data["patch"].(map[string]interface{}); ok {
				status, _ := patch["status"].(string)
				if types.IsTerminalTaskStatus(status) {
					ev.status = status
				}
				ev.resumed = isActiveTaskStatus(status)
			}
		}
		return ev, true
	}
	return taskEvent{}, false
}

// SetSubagentCallback registers the receiver for subagent traffic.
func (sm *SessionManager) SetSubagentCallback(callback subagentTrafficCallback) {
	sm.subagentCallbackMu.Lock()
	defer sm.subagentCallbackMu.Unlock()
	sm.subagentCallback = callback
}

// routeSubagentTraffic shows msg to the subagent tracker and reports whether it
// was subagent traffic, which the caller must not persist, forward or broadcast
// as a parent message.
func (sm *SessionManager) routeSubagentTraffic(session *AgentSession, msg types.Message) bool {
	parentID := subagentParentID(msg)
	_, isTaskEvent := parseTaskEvent(msg)
	if parentID != "" {
		// A subagent's shell commands still move the session's workspace.
		if sm.observeWorkspaceMessage(session, msg) {
			_, _, _ = sm.RefreshGitBranch(session.ID)
		}
	}

	sm.subagentCallbackMu.RLock()
	callback := sm.subagentCallback
	sm.subagentCallbackMu.RUnlock()
	if callback != nil {
		callback(session.ID, parentID, msg)
	}
	return parentID != "" || isTaskEvent
}

// handleSubagentTraffic feeds what the session reader sees to the
// BackgroundAgentManager, which stores subagent output and broadcasts it.
func (h *AgentHandler) handleSubagentTraffic(sessionID uuid.UUID, parentToolUseID string, msg types.Message) {
	if h.BackgroundAgentMgr == nil {
		return
	}
	if parentToolUseID != "" {
		h.handleSubagentMessage(sessionID, parentToolUseID, msg)
		return
	}
	if ev, ok := parseTaskEvent(msg); ok {
		h.handleTaskEvent(ev)
		return
	}
	h.observeParentMessage(sessionID, msg)
}

// observeParentMessage registers agents the parent launches and applies their
// tool results: a foreground agent's result completes it, a background agent's
// launch result binds its task id.
func (h *AgentHandler) observeParentMessage(sessionID uuid.UUID, msg types.Message) {
	mgr := h.BackgroundAgentMgr
	switch m := msg.(type) {
	case *types.AssistantMessage:
		for _, block := range m.Content {
			tool, ok := block.(*types.ToolUseBlock)
			if !ok {
				continue
			}
			switch tool.Name {
			case "Task", "Agent":
				h.handleTaskToolUse(sessionID, tool)
			case "SendMessage":
				expectResume(mgr, tool)
			}
		}
	case *types.UserMessage:
		// Workflow agents are followed through the run's transcript folder.
		h.observeWorkflowLaunch(sessionID, m)
		blocks, ok := m.Content.([]types.ContentBlock)
		if !ok {
			return
		}
		for _, block := range blocks {
			result, ok := block.(*types.ToolResultBlock)
			if !ok {
				continue
			}
			if mgr.ResumeFromResult(result.ToolUseID, result.IsError != nil && *result.IsError) {
				continue
			}
			if _, tracked := mgr.GetAgent(result.ToolUseID); !tracked {
				continue
			}
			if taskID := launchTaskID(m, result); taskID != "" {
				mgr.BindTask(result.ToolUseID, taskID)
			}
			h.handleToolResult(sessionID, result)
		}
	}
}

// launchTaskID returns the task id from a background agent's launch result.
func launchTaskID(msg *types.UserMessage, result *types.ToolResultBlock) string {
	if msg.ToolUseResult != nil {
		if id, _ := msg.ToolUseResult["agentId"].(string); id != "" {
			return id
		}
	}
	text := toolResultText(result.Content)
	if !isBackgroundAgentLaunchResult(text) {
		return ""
	}
	if match := launchAgentIDPattern.FindStringSubmatch(text); match != nil {
		return match[1]
	}
	return ""
}

// outputTextLines splits an agent's text into output lines: trimmed, without
// empty lines and noise.
func outputTextLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" && !isNoiseLine(trimmed) {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

// resultPreview cuts a tool result's text to what the output lines keep.
func resultPreview(text string) string {
	if len(text) > 300 {
		return text[:300] + "..."
	}
	return text
}

// resultOutputLines frames a tool result's preview as output lines between
// RESULT_START and RESULT_END: at most 8 lines, then how many were left out.
func resultOutputLines(text string, isError bool, at time.Time) []OutputLine {
	out := []OutputLine{{Text: "RESULT_START", At: at}}
	lines := strings.Split(text, "\n")
	shown := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || isNoiseLine(trimmed) {
			continue
		}
		out = append(out, OutputLine{Text: trimmed, IsError: isError, At: at})
		if shown++; shown >= 8 {
			if remaining := len(lines) - i - 1; remaining > 0 {
				out = append(out, OutputLine{Text: fmt.Sprintf("... (%d more lines)", remaining), At: at})
			}
			break
		}
	}
	return append(out, OutputLine{Text: "RESULT_END", At: at})
}

// handleSubagentMessage turns a subagent's own message into output lines and
// subagent_message events.
func (h *AgentHandler) handleSubagentMessage(sessionID uuid.UUID, agentID string, msg types.Message) {
	h.handleSubagentMessageAt(sessionID, agentID, msg, time.Time{})
}

// handleSubagentMessageAt is handleSubagentMessage for a message produced at
// a known time (a workflow transcript line's timestamp); zero means now.
func (h *AgentHandler) handleSubagentMessageAt(sessionID uuid.UUID, agentID string, msg types.Message, at time.Time) {
	mgr := h.BackgroundAgentMgr
	agent, exists := mgr.GetAgent(agentID)
	if !exists {
		// The launching tool_use was missed, e.g. the server restarted mid-run.
		mgr.RegisterSubagent(agentID, sessionID, "", "", "", false)
		agent, _ = mgr.GetAgent(agentID)
	}
	var agentType, agentDesc string
	if agent != nil {
		agentType, agentDesc = agent.SubagentType, agent.Description
	}
	if am, isAssistant := msg.(*types.AssistantMessage); isAssistant && isRateLimitError(am) &&
		(IsUsageLimitText(assistantText(am)) || h.SessionManager == nil || h.SessionManager.limitActive(time.Now())) {
		// The limit stopped this agent: it is paused when it ends.
		mgr.MarkLimitHit(agentID)
	} else if _, isAssistant := msg.(*types.AssistantMessage); isAssistant && agent != nil &&
		agent.Status != BackgroundAgentStatusRunning {
		// A finished agent that speaks again was resumed (SendMessage). This
		// also covers resumes the parent's SendMessage could not be matched to,
		// e.g. after a server restart, when task ids are no longer known.
		mgr.ReopenAgent(agentID)
	}
	if am, isAssistant := msg.(*types.AssistantMessage); isAssistant && am.Model != "" {
		// The model the agent actually runs on (it can change, e.g. on a fallback).
		mgr.SetModel(agentID, am.Model, ModelSourceActual)
	}
	depth := mgr.Depth(agentID)

	// Output lines are collected per message and added in one batch: one
	// state update and one broadcast instead of one per line.
	var out []OutputLine
	addLine := func(text string, isError bool) {
		out = append(out, OutputLine{Text: text, IsError: isError, At: at})
	}
	defer func() { mgr.AddOutputLines(agentID, out) }()

	switch m := msg.(type) {
	case *types.AssistantMessage:
		for _, block := range m.Content {
			switch b := block.(type) {
			case *types.TextBlock:
				if b.Text == "" {
					continue
				}
				for _, line := range outputTextLines(b.Text) {
					addLine(line, false)
				}
				h.broadcastSubagentMessage(sessionID, agentID, agentType, agentDesc,
					"text", b.Text, "", nil, false, mgr.NextSequence(agentID), depth)
			case *types.ThinkingBlock:
				if b.Thinking == "" {
					continue
				}
				h.broadcastSubagentMessage(sessionID, agentID, agentType, agentDesc,
					"thinking", b.Thinking, "", nil, false, mgr.NextSequence(agentID), depth)
			case *types.ToolUseBlock:
				if b.Name == "SendMessage" {
					expectResume(mgr, b)
				}
				if b.Name == "Task" || b.Name == "Agent" {
					// A nested subagent: track it as a child of this one.
					childType, _ := b.Input["subagent_type"].(string)
					childDesc, _ := b.Input["description"].(string)
					background, _ := b.Input["run_in_background"].(bool)
					childPrompt, _ := b.Input["prompt"].(string)
					childModel, _ := b.Input["model"].(string)
					mgr.RegisterLaunchedSubagent(b.ID, sessionID, childType, childDesc, agentID, background, childPrompt, childModel)
				} else {
					addLine(subagentToolLine(b.Name, b.Input), false)
				}
				h.broadcastSubagentMessage(sessionID, agentID, agentType, agentDesc,
					"tool_use", "", b.Name, b.Input, false, mgr.NextSequence(agentID), depth)
			}
		}

	case *types.UserMessage:
		blocks, ok := m.Content.([]types.ContentBlock)
		if !ok {
			return
		}
		for _, block := range blocks {
			result, ok := block.(*types.ToolResultBlock)
			if !ok {
				continue
			}
			mgr.ResumeFromResult(result.ToolUseID, result.IsError != nil && *result.IsError)
			// The result of a nested Agent call completes (or launches) that child.
			if _, isAgent := mgr.GetAgent(result.ToolUseID); isAgent {
				if taskID := launchTaskID(m, result); taskID != "" {
					mgr.BindTask(result.ToolUseID, taskID)
				}
				h.handleToolResult(sessionID, result)
				continue
			}
			text := resultPreview(toolResultText(result.Content))
			if text == "" {
				continue
			}
			isError := result.IsError != nil && *result.IsError
			out = append(out, resultOutputLines(text, isError, at)...)
			h.broadcastSubagentMessage(sessionID, agentID, agentType, agentDesc,
				"tool_result", text, "", nil, isError, mgr.NextSequence(agentID), depth)
		}
	}
}

// handleTaskEvent finishes the agent a terminal task event belongs to.
func (h *AgentHandler) handleTaskEvent(ev taskEvent) {
	mgr := h.BackgroundAgentMgr
	agentID := ev.toolUseID
	if agentID == "" {
		agentID = mgr.AgentForTask(ev.taskID)
	}
	if ev.status == "" {
		// A task that is going again after it finished was resumed.
		if ev.resumed && agentID != "" {
			mgr.ReopenAgent(agentID)
		}
		return
	}
	if h.finishWorkflow(ev.taskID, ev.status) {
		return
	}
	if agentID == "" {
		// Background shells are tasks too; only agents are tracked.
		logging.Debug("Task %s finished (%s) but is not a tracked agent", ev.taskID, ev.status)
		return
	}
	finishSubagent(mgr, agentID, ev.status, ev.detail)
}

// SessionCLIClosedAgentMessage is the error recorded for subagents that were
// running when their session's Claude CLI was closed (interrupt, model switch,
// end, a reload for changed tools/skills/trust). It reads as an interruption.
const SessionCLIClosedAgentMessage = "Agent stopped: the session's Claude CLI was closed"

// stopSessionSubagents marks the session's running subagents stopped: they ran
// inside the Claude CLI that was just closed.
func (h *AgentHandler) stopSessionSubagents(sessionID uuid.UUID) {
	mgr := h.BackgroundAgentMgr
	if mgr == nil {
		return
	}
	// Workflows ran in that CLI too; their watchers stop first so they
	// cannot complete an agent after it was marked stopped.
	h.stopSessionWorkflows(sessionID)
	for _, agent := range mgr.GetRunningAgentsForSession(sessionID) {
		mgr.FailAgent(agent.AgentID, SessionCLIClosedAgentMessage)
	}
}

// isActiveTaskStatus reports whether a task_updated status means the task is running.
func isActiveTaskStatus(status string) bool {
	return status == types.TaskUpdatedStatusRunning || status == types.TaskUpdatedStatusPending
}

// expectResume notes the agent a SendMessage call is addressed to. When the
// call succeeds the CLI resumes a finished agent, whose resumed run reports
// under the same agent, so it is reopened then (ResumeFromResult); a denied
// or failed call leaves it finished. The recipient is the agent's task id (or
// its tool_use id).
func expectResume(mgr *BackgroundAgentManager, tool *types.ToolUseBlock) {
	for _, key := range []string{"to", "recipient"} {
		recipient, _ := tool.Input[key].(string)
		if recipient == "" {
			continue
		}
		agentID := mgr.AgentForTask(recipient)
		if agentID == "" {
			if _, known := mgr.GetAgent(recipient); known {
				agentID = recipient
			}
		}
		if agentID != "" {
			mgr.ExpectResume(tool.ID, agentID)
			return
		}
	}
}

// finishSubagent moves a running agent to its terminal state. Agents that
// already finished (a foreground agent completed by its tool_result) are left alone.
func finishSubagent(mgr *BackgroundAgentManager, agentID, status, detail string) {
	agent, exists := mgr.GetAgent(agentID)
	if !exists || agent.Status != BackgroundAgentStatusRunning {
		return
	}
	if status == types.TaskNotificationStatusCompleted {
		mgr.CompleteAgent(agentID, detail)
		return
	}
	message := "Agent " + status
	if detail != "" {
		message += ": " + detail
	}
	mgr.FailAgent(agentID, message)
}

// subagentToolLine is the output line recorded for a subagent's tool call:
// "TOOL:<name>", or "TOOL:<name>: <what it acts on>" so viewers can show what
// the subagent is doing, not just which tool it picked.
func subagentToolLine(name string, input map[string]interface{}) string {
	if detail := toolInputSummary(input); detail != "" {
		return "TOOL:" + name + ": " + detail
	}
	return "TOOL:" + name
}

// subagentCommandMaxRunes caps a command kept in a subagent's TOOL: line.
const subagentCommandMaxRunes = 600

// toolInputSummary picks the one argument that says what a tool call acts on
// (command, file, pattern, ...) and flattens it to a single short line.
func toolInputSummary(input map[string]interface{}) string {
	flattenTo := func(v string, max int) string {
		v = strings.Join(strings.Fields(v), " ")
		if r := []rune(v); len(r) > max {
			v = string(r[:max]) + "…"
		}
		return v
	}
	flatten := func(v string) string { return flattenTo(v, 160) }
	for _, key := range []string{"command", "cmd", "file_path", "notebook_path", "path", "pattern", "query", "url", "description", "prompt"} {
		if v, ok := input[key].(string); ok && strings.TrimSpace(v) != "" {
			// Commands are what the Subagents view shows in full, so they get more room.
			if key == "command" || key == "cmd" {
				return flattenTo(v, subagentCommandMaxRunes)
			}
			return flatten(v)
		}
	}
	// Codex file changes: {"changes": [{"path": ..., "kind": ...}, ...]}
	if changes, ok := input["changes"].([]interface{}); ok && len(changes) > 0 {
		if first, ok := changes[0].(map[string]interface{}); ok {
			if path, _ := first["path"].(string); path != "" {
				if len(changes) > 1 {
					return flatten(fmt.Sprintf("%s (+%d more)", path, len(changes)-1))
				}
				return flatten(path)
			}
		}
	}
	return ""
}

// toolResultText flattens a tool_result's content into text.
func toolResultText(content interface{}) string {
	var parts []string
	switch c := content.(type) {
	case nil:
		return ""
	case string:
		return c
	case []types.ContentBlock:
		for _, block := range c {
			if text, ok := block.(*types.TextBlock); ok && text.Text != "" {
				parts = append(parts, text.Text)
			}
		}
	case []interface{}:
		for _, item := range c {
			if block, ok := item.(map[string]interface{}); ok {
				if text, ok := block["text"].(string); ok && text != "" {
					parts = append(parts, text)
				}
			}
		}
	default:
		return fmt.Sprintf("%v", c)
	}
	return strings.Join(parts, "\n")
}

// SubagentsForSession lists the subagents a session has run, oldest first.
// Agents missing from memory (after a restart, or once cleaned up) are
// restored from the database, so their output stays viewable.
func (h *AgentHandler) SubagentsForSession(sessionID uuid.UUID) []BackgroundAgent {
	mgr := h.BackgroundAgentMgr
	if mgr == nil {
		return nil
	}
	agents := mgr.GetAgentsForSession(sessionID)
	if h.SessionManager == nil || h.SessionManager.Storage == nil {
		return agents
	}
	dbAgents, err := h.SessionManager.Storage.GetBackgroundAgentsForSession(sessionID)
	if err != nil {
		logging.Debug("Failed to load subagents for session %s: %v", sessionID, err)
		return agents
	}
	restored := 0
	for _, dbAgent := range dbAgents {
		if _, known := mgr.GetAgent(dbAgent.AgentID); known {
			continue
		}
		stale := settleStaleRunning(dbAgent)
		mgr.RestoreAgent(dbAgent)
		if stale {
			mgr.persistAgent(dbAgent, false)
		}
		restored++
	}
	if restored == 0 {
		return agents
	}
	logging.Info("📦 Restored %d background agents from database for session %s", restored, sessionID)
	return mgr.GetAgentsForSession(sessionID)
}

// addLightSubagentCounts fills in the running subagent count of each session
// in a WebSocket session list.
func (h *AgentHandler) addLightSubagentCounts(sessions []LightSession) {
	if h.SessionManager == nil || len(sessions) == 0 {
		return
	}
	// Loops are in memory: filled in even when the count query fails.
	for i := range sessions {
		sessions[i].ScheduledLoop, sessions[i].ScheduledJobs = h.SessionManager.ScheduledFor(sessions[i].ID)
		sessions[i].LimitWait = h.SessionManager.listedLimitWait(sessions[i].ID, sessions[i].Options.LimitWait)
	}
	if h.SessionManager.Storage == nil {
		return
	}
	ids := make([]uuid.UUID, len(sessions))
	for i := range sessions {
		ids[i] = sessions[i].ID
	}
	tags := h.SessionManager.sessionTagsFor(ids)
	for i := range sessions {
		sessions[i].Tags = tags[sessions[i].ID]
	}
	counts, err := h.SessionManager.Storage.CountBackgroundAgentsBySession()
	if err != nil {
		logging.Debug("Failed to count subagents: %v", err)
		return
	}
	for i := range sessions {
		sessions[i].SubagentsRunning = counts[sessions[i].ID].Running
	}
}

// AddSubagentCounts fills in each session's subagent counts for a listing.
func (h *AgentHandler) AddSubagentCounts(sessions []Session) {
	if h.SessionManager == nil || len(sessions) == 0 {
		return
	}
	// Loops are in memory: filled in even when the count query fails.
	for i := range sessions {
		sessions[i].ScheduledLoop, sessions[i].ScheduledJobs = h.SessionManager.ScheduledFor(sessions[i].ID)
		sessions[i].LimitWait = h.SessionManager.listedLimitWait(sessions[i].ID, sessions[i].Options.LimitWait)
	}
	if h.SessionManager.Storage == nil {
		return
	}
	ids := make([]uuid.UUID, len(sessions))
	for i := range sessions {
		ids[i] = sessions[i].ID
	}
	tags := h.SessionManager.sessionTagsFor(ids)
	for i := range sessions {
		sessions[i].Tags = tags[sessions[i].ID]
	}
	counts, err := h.SessionManager.Storage.CountBackgroundAgentsBySession()
	if err != nil {
		logging.Debug("Failed to count subagents: %v", err)
		return
	}
	for i := range sessions {
		c := counts[sessions[i].ID]
		sessions[i].SubagentsRunning, sessions[i].SubagentsTotal = c.Running, c.Total
	}
}

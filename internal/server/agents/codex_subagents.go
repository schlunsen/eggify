package agents

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/schlunsen/claude-agent-sdk-go/types"
	codextypes "github.com/schlunsen/codex-sdk-go/types"
)

// Codex sub-agent routing.
//
// A Codex agent spawns sub-agents as separate threads inside the same
// app-server process, and the app-server streams those threads' notifications
// over the parent's connection, tagged with the child's threadId:
//
//	item subAgentActivity  {kind:"started", agentThreadId, agentPath}   on the spawning thread
//	item/turn events       {threadId: <child>}                           the child's own work
//	item subAgentActivity  {kind:"completed"|"interrupted", ...}        on the spawning thread
//	item collabAgentToolCall {tool:"spawnAgent"|"wait"|..., receiverThreadIds, agentsStates}
//
// The CLI's JSONL output reports the same call as a snake_case
// collab_tool_call item (tool "spawn_agent", ...), which codex-sdk-go types as
// CollabToolCallItem. Both shapes are parsed into that type
// (parseCodexCollabCall), so a CLI that switches naming keeps working.
//
// codexSubagents turns this into the Claude-shaped traffic the subagent
// tracker already understands (see subagent_routing.go): the spawn becomes an
// "Agent" tool_use in the spawning conversation whose id is the agent id, the
// child's items become messages tagged with that id as parent_tool_use_id, and
// the child finishing becomes the Agent tool_result. The child's work is kept
// out of the parent chat, just as it is for Claude subagents.

// codexSubagentToolName is the tool card used for collab calls other than a
// spawn (wait, send input, close, ...).
const codexSubagentToolName = "SubAgent"

type codexChild struct {
	toolID   string // Agent tool_use id, which is also the BackgroundAgent id
	sender   string // thread that spawned it
	name     string
	lastText string
	done     bool
	resumed  bool // it spoke again after finishing (send_input/resume_agent)
}

type codexSubagents struct {
	sm         *SessionManager
	session    *AgentSession
	rootThread string
	children   map[string]*codexChild // child thread id -> child
}

func newCodexSubagents(sm *SessionManager, session *AgentSession, rootThread string) *codexSubagents {
	return &codexSubagents{sm: sm, session: session, rootThread: rootThread, children: map[string]*codexChild{}}
}

// known reports whether events for threadID belong to this run.
func (c *codexSubagents) known(threadID string) bool {
	return threadID == c.rootThread || c.children[threadID] != nil
}

// isCollabItem reports whether an app-server item is sub-agent activity.
func isCollabItem(raw json.RawMessage) bool {
	var item struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &item) != nil {
		return false
	}
	return item.Type == "subAgentActivity" || item.Type == "collabAgentToolCall" ||
		item.Type == codextypes.ItemTypeCollabToolCall
}

// camelToSnake turns app-server values ("spawnAgent", "notFound",
// "inProgress") into the CLI/SDK spelling ("spawn_agent", "not_found",
// "in_progress"). Snake-case input is returned unchanged.
func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// parseCodexCollabCall parses a collab tool call in either the app-server's
// camelCase shape (collabAgentToolCall) or the CLI's snake_case shape
// (collab_tool_call) into the SDK type. It also returns the model the call
// names, which the SDK type doesn't carry.
func parseCodexCollabCall(raw json.RawMessage) (*codextypes.CollabToolCallItem, string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, "", err
	}
	for from, to := range map[string]string{
		"senderThreadId":    "sender_thread_id",
		"receiverThreadIds": "receiver_thread_ids",
		"agentsStates":      "agents_states",
	} {
		if v, ok := fields[from]; ok {
			if _, exists := fields[to]; !exists {
				fields[to] = v
			}
			delete(fields, from)
		}
	}
	normalize := func(key string) {
		var v string
		if json.Unmarshal(fields[key], &v) == nil {
			fields[key], _ = json.Marshal(camelToSnake(v))
		}
	}
	normalize("tool")
	normalize("status")
	var states map[string]map[string]json.RawMessage
	if json.Unmarshal(fields["agents_states"], &states) == nil {
		for _, st := range states {
			var v string
			if json.Unmarshal(st["status"], &v) == nil {
				st["status"], _ = json.Marshal(camelToSnake(v))
			}
		}
		fields["agents_states"], _ = json.Marshal(states)
	}
	var model string
	_ = json.Unmarshal(fields["model"], &model)

	data, err := json.Marshal(fields)
	if err != nil {
		return nil, "", err
	}
	var call codextypes.CollabToolCallItem
	if err := json.Unmarshal(data, &call); err != nil {
		return nil, "", err
	}
	return &call, model, nil
}

// codexWords shows an SDK value as words: "spawn_agent" -> "spawn agent".
func codexWords[T ~string](v T) string {
	return strings.ReplaceAll(string(v), "_", " ")
}

// codexToolID is the Agent tool_use id for a child thread. Thread ids are
// unique, so the id is stable across turns and reloads.
func codexToolID(childThread string) string {
	return "codex_agent_" + childThread
}

// handleCollabItem handles a subAgentActivity item or a collab tool call
// (collabAgentToolCall / collab_tool_call) reported on senderThread.
func (c *codexSubagents) handleCollabItem(senderThread, method string, raw json.RawMessage, state *codexTurnState) {
	var head struct {
		Type          string `json:"type"`
		Kind          string `json:"kind"`
		AgentThreadID string `json:"agentThreadId"`
		AgentPath     string `json:"agentPath"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return
	}

	if head.Type == "subAgentActivity" {
		switch head.Kind {
		case "started":
			c.spawn(senderThread, head.AgentThreadID, codexAgentName(head.AgentPath, head.AgentThreadID), "", "")
		case "completed":
			c.finish(head.AgentThreadID, "completed", "")
		case "interrupted":
			c.finish(head.AgentThreadID, "interrupted", "")
		}
		return
	}

	call, model, err := parseCodexCollabCall(raw)
	if err != nil {
		return
	}
	if call.SenderThreadID != "" {
		senderThread = call.SenderThreadID
	}
	// App-server items say so by method; a bare CLI item by its status.
	completed := method == "item/completed" || (method == "" &&
		(call.Status == codextypes.CollabToolCallCompleted || call.Status == codextypes.CollabToolCallFailed))
	failed := call.Status == codextypes.CollabToolCallFailed
	prompt := codexStr(call.Prompt)

	if call.Tool == codextypes.CollabToolSpawnAgent {
		for _, child := range call.ReceiverThreadIDs {
			c.spawn(senderThread, child, codexAgentName("", child), prompt, model)
		}
		if completed && failed && len(call.ReceiverThreadIDs) == 0 {
			input := map[string]interface{}{"action": codexWords(call.Tool), "prompt": prompt}
			c.emit(senderThread, state.toolUseAndResult(call.ID, codexSubagentToolName, input, "Failed to spawn sub-agent", true))
		}
	} else {
		input := map[string]interface{}{"action": codexWords(call.Tool)}
		if names := c.names(call.ReceiverThreadIDs); len(names) > 0 {
			input["agents"] = names
		}
		if prompt != "" {
			input["prompt"] = prompt
		}
		if !completed {
			c.emit(senderThread, []codexEmit{state.toolUse(call.ID, codexSubagentToolName, input)})
		} else {
			c.emit(senderThread, state.toolUseAndResult(call.ID, codexSubagentToolName, input,
				c.collabResultText(call), failed))
		}
	}

	if !completed {
		return
	}
	threads := make([]string, 0, len(call.AgentsStates))
	for thread := range call.AgentsStates {
		threads = append(threads, thread)
	}
	sort.Strings(threads)
	for _, thread := range threads {
		st := call.AgentsStates[thread]
		switch st.Status {
		case codextypes.CollabAgentCompleted, codextypes.CollabAgentErrored, codextypes.CollabAgentInterrupted,
			codextypes.CollabAgentShutdown, codextypes.CollabAgentNotFound:
			c.finish(thread, codexWords(st.Status), codexStr(st.Message))
		}
	}
}

func (c *codexSubagents) collabResultText(call *codextypes.CollabToolCallItem) string {
	if len(call.AgentsStates) == 0 {
		return codexWords(call.Tool) + " " + codexWords(call.Status)
	}
	threads := make([]string, 0, len(call.AgentsStates))
	for thread := range call.AgentsStates {
		threads = append(threads, thread)
	}
	sort.Strings(threads)
	lines := make([]string, 0, len(threads))
	for _, thread := range threads {
		st := call.AgentsStates[thread]
		line := c.nameOf(thread) + ": " + codexWords(st.Status)
		if msg := codexStr(st.Message); msg != "" {
			line += " — " + msg
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (c *codexSubagents) names(threads []string) []interface{} {
	out := make([]interface{}, 0, len(threads))
	for _, thread := range threads {
		out = append(out, c.nameOf(thread))
	}
	return out
}

func (c *codexSubagents) nameOf(thread string) string {
	if child := c.children[thread]; child != nil {
		return child.name
	}
	return codexAgentName("", thread)
}

// codexAgentName names an agent after the last segment of its agent path
// ("/root/shell_child" -> "shell_child").
func codexAgentName(agentPath, thread string) string {
	if name := path.Base(strings.TrimSpace(agentPath)); name != "" && name != "." && name != "/" {
		return name
	}
	short := thread
	if len(short) > 8 {
		short = short[len(short)-8:]
	}
	return "agent-" + short
}

// spawn registers a child thread and shows its Agent tool card in the
// spawning conversation. A thread reported twice (subAgentActivity and a
// spawnAgent collab call) is registered once.
func (c *codexSubagents) spawn(senderThread, childThread, name, prompt, model string) {
	if childThread == "" {
		return
	}
	if c.children[childThread] != nil {
		return
	}
	child := &codexChild{toolID: codexToolID(childThread), sender: senderThread, name: name}
	c.children[childThread] = child
	description := name
	if prompt != "" {
		description = firstLine(prompt, 80)
	}
	input := map[string]interface{}{"subagent_type": name, "description": description}
	if prompt != "" {
		input["prompt"] = prompt
	}
	if model != "" {
		input["model"] = model
	}
	block := &types.ToolUseBlock{Type: "tool_use", ID: child.toolID, Name: "Agent", Input: input}
	if parent := c.parentToolID(senderThread); parent != "" {
		c.route(parent, &types.AssistantMessage{Type: "assistant", Content: []types.ContentBlock{block}, ParentToolUseID: &parent})
		return
	}
	c.sm.sendDirectToolUse(c.session, child.toolID, "Agent", input)
	c.route("", &types.AssistantMessage{Type: "assistant", Content: []types.ContentBlock{block}})
}

// finish ends a child with the given Codex status, reporting its last reply as
// the Agent tool result. Only the first report counts.
func (c *codexSubagents) finish(childThread, status, detail string) {
	child := c.children[childThread]
	if child == nil || child.done {
		return
	}
	child.done = true
	isErr := status != "completed"
	text := child.lastText
	if text == "" {
		text = detail
	}
	if isErr {
		msg := fmt.Sprintf("Sub-agent %s %s", child.name, status)
		if text != "" {
			msg += ": " + text
		}
		text = msg
	} else if text == "" {
		text = "Sub-agent " + child.name + " completed"
	}
	block := &types.ToolResultBlock{Type: "tool_result", ToolUseID: child.toolID, Content: text, IsError: &isErr}
	if parent := c.parentToolID(child.sender); parent != "" {
		c.route(parent, &types.UserMessage{Type: "user", Content: []types.ContentBlock{block}, ParentToolUseID: &parent})
		return
	}
	// The Agent card already has its result from the first run; a resumed
	// run only finishes the agent again.
	if !child.resumed {
		c.sm.sendDirectToolResult(c.session, child.toolID, text, isErr)
	}
	c.route("", &types.UserMessage{Type: "user", Content: []types.ContentBlock{block}})
}

// finishAll ends children still running when the app-server goes away, which
// takes their threads with it.
func (c *codexSubagents) finishAll(status string) {
	threads := make([]string, 0, len(c.children))
	for thread := range c.children {
		threads = append(threads, thread)
	}
	sort.Strings(threads)
	for _, thread := range threads {
		c.finish(thread, status, "")
	}
}

// childTurnCompleted handles turn/completed on a child thread.
func (c *codexSubagents) childTurnCompleted(childThread, status, errMessage string) {
	switch {
	case errMessage != "":
		c.finish(childThread, "failed", errMessage)
	case status == "failed" || status == "interrupted":
		c.finish(childThread, status, "")
	default:
		c.finish(childThread, "completed", "")
	}
}

// childEmits routes a child thread's mapped items to its agent.
func (c *codexSubagents) childEmits(childThread string, emits []codexEmit) {
	child := c.children[childThread]
	if child == nil {
		return
	}
	if child.done && len(emits) > 0 {
		// A finished child that speaks again was resumed: its new turn
		// reopens the agent (handleSubagentMessage) and must finish it again.
		child.done, child.resumed, child.lastText = false, true, ""
	}
	for _, e := range emits {
		if e.Kind == codexEmitText {
			child.lastText = e.Text
		}
	}
	c.routeEmits(child.toolID, emits)
}

// emit sends emits to the conversation of thread: the chat for the root
// thread, the agent's output for a child.
func (c *codexSubagents) emit(thread string, emits []codexEmit) {
	if parent := c.parentToolID(thread); parent != "" {
		c.routeEmits(parent, emits)
		return
	}
	c.sm.applyCodexEmits(c.session, emits)
}

func (c *codexSubagents) routeEmits(toolID string, emits []codexEmit) {
	parent := toolID
	for _, e := range emits {
		var msg types.Message
		switch e.Kind {
		case codexEmitText:
			msg = &types.AssistantMessage{Type: "assistant", Content: []types.ContentBlock{&types.TextBlock{Type: "text", Text: e.Text}}, ParentToolUseID: &parent}
		case codexEmitThinking:
			msg = &types.AssistantMessage{Type: "assistant", Content: []types.ContentBlock{&types.ThinkingBlock{Type: "thinking", Thinking: e.Text}}, ParentToolUseID: &parent}
		case codexEmitToolUse:
			msg = &types.AssistantMessage{Type: "assistant", Content: []types.ContentBlock{&types.ToolUseBlock{Type: "tool_use", ID: e.ToolID, Name: e.ToolName, Input: e.ToolInput}}, ParentToolUseID: &parent}
		case codexEmitToolResult:
			isErr := e.IsError
			msg = &types.UserMessage{Type: "user", Content: []types.ContentBlock{&types.ToolResultBlock{Type: "tool_result", ToolUseID: e.ToolID, Content: e.Text, IsError: &isErr}}, ParentToolUseID: &parent}
		default:
			continue
		}
		c.route(parent, msg)
	}
}

// parentToolID returns the Agent tool id of thread, or "" for the root thread.
func (c *codexSubagents) parentToolID(thread string) string {
	if child := c.children[thread]; child != nil {
		return child.toolID
	}
	return ""
}

func (c *codexSubagents) route(parentToolUseID string, msg types.Message) {
	c.sm.subagentCallbackMu.RLock()
	callback := c.sm.subagentCallback
	c.sm.subagentCallbackMu.RUnlock()
	if callback != nil {
		callback(c.session.ID, parentToolUseID, msg)
	}
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "…"
	}
	return s
}

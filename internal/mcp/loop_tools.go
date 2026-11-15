package mcp

import (
	"context"
	"encoding/json"

	"github.com/schlunsen/eggify/internal/n0"
)

// Loop and worker sessions started by agents (internal/server/agents/loop_spawn.go,
// worker_spawn.go).
// Everything is done by the Eggify server over the control endpoint, for the
// calling session only: start_loop_session and start_worker_session wait for
// the user's approval on a card in Eggify, and the messaging tools only reach
// linked sessions.

func loopToolDefinitions() []Tool {
	return []Tool{
		{
			Name: "start_loop_session",
			Description: "Propose a new Eggify session that runs in loop mode with permission prompts off (YOLO, not sandboxed, Claude CLI): " +
				"after each turn Eggify runs verify_command and re-prompts the agent with its output until it passes or a limit is hit. " +
				"The user approves, edits or denies it on a card in Eggify; this call waits for that answer. " +
				"On approval it returns the new session_id. The loop's final report arrives in your inbox (at the top of a later prompt); " +
				"use get_loop_session and message_loop_session to follow and steer it. Write the goal so it stands alone: the loop session does not see this conversation.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"goal":              {Type: "string", Description: "What the loop must achieve, self-contained (the loop session does not see your conversation)"},
					"verify_command":    {Type: "string", Description: "Shell command run in the working directory after each turn; exit 0 ends the loop. Make it fail while work is left. Optional: without it the loop runs until the agent reports done or the limits"},
					"working_directory": {Type: "string", Description: "Folder to work in (default: your working directory; relative paths are resolved against it)"},
					"max_iterations":    {Type: "number", Description: "Iteration limit (default 10, max 50)"},
					"timeout_minutes":   {Type: "number", Description: "Time limit in minutes (default 30, max 240)"},
					"model":             {Type: "string", Description: "Model (default: yours if you run on the Claude CLI, else Eggify's default)"},
					"provider":          {Type: "string", Description: "Provider id (default: yours, see model)"},
					"context":           {Type: "string", Description: "Extra notes for the loop's first turn: findings, constraints, files to read"},
				},
				Required: []string{"goal"},
			},
		},
		{
			Name: "start_worker_session",
			Description: "Hand a task to a new Eggify session on another model or runtime and get its report back: a Claude CLI model, an Anthropic-compatible provider (for example provider deepseek, model deepseek-flash) or Codex (provider codex, for example model gpt-6-astra). " +
				"Use it when the user asks for another model to build or review something; for work on your own model prefer your own subagents. " +
				"mode \"build\" may change files in the working directory; mode \"review\" is read-only (it can read, search and run checks, not change files). Both are sandboxed, with permission prompts off inside the sandbox. " +
				"The user approves or denies it on a card in Eggify; this call waits for that answer. On approval it returns the new session_id: then end your turn. " +
				"The worker's reply arrives in your inbox (at the top of a later prompt) each time it finishes a turn; answer it or give it more work with message_loop_session, check on it with get_loop_session, and do not poll. " +
				"A build worker in a git repository works in its own worktree on a new branch started from your last commit (commit what it needs first) and commits there; nothing reaches your branch until you merge it. " +
				"To review a build worker's changes, start a review worker with reviews_session_id set to that worker's session_id: it runs read-only in that worktree and ends its report with a verdict (APPROVE or CHANGES_REQUESTED). On CHANGES_REQUESTED send the findings to the build worker and then ask the reviewer to look again; the number of review rounds is limited, and after the last one you must report to the user instead. " +
				"Write the task so it stands alone: the worker does not see this conversation.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"task":               {Type: "string", Description: "What the worker must do, self-contained (it does not see your conversation)"},
					"mode":               {Type: "string", Description: "build (default; may change files) or review (read-only)"},
					"working_directory":  {Type: "string", Description: "Folder to work in (default: your working directory; relative paths are resolved against it)"},
					"model":              {Type: "string", Description: "Model (default: yours). Required for Codex"},
					"provider":           {Type: "string", Description: "Provider id, for example claude, deepseek or codex (default: yours, see model)"},
					"context":            {Type: "string", Description: "Extra notes for the worker's first turn: the plan, findings, constraints, files to read"},
					"reviews_session_id": {Type: "string", Description: "review mode: the build worker whose work to review; the review runs in its folder and counts as a review round"},
				},
				Required: []string{"task"},
			},
		},
		{
			Name:        "get_loop_session",
			Description: "Show the state of the loop and worker sessions you started: status, last reply and, for loops, iteration and last verify result. Without session_id, lists all of them.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"session_id": {Type: "string", Description: "A loop session you started (optional)"},
				},
			},
		},
		{
			Name:        "message_loop_session",
			Description: "Send a message to a loop or worker session you started. A running loop gets it with its next iteration and a busy worker when its turn ends; an idle one reads it right away (a loop is not restarted).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"session_id": {Type: "string", Description: "The session (from start_loop_session or start_worker_session)"},
					"text":       {Type: "string", Description: "The message"},
				},
				Required: []string{"session_id", "text"},
			},
		},
		{
			Name:        "message_parent_session",
			Description: "Only in a loop or worker session started by another agent session: send that session a question or progress note. Its answer arrives at the top of a later prompt. Your final result is reported automatically; don't send it here. Rate limited.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"text": {Type: "string", Description: "The message"},
				},
				Required: []string{"text"},
			},
		},
	}
}

func (s *MCPServer) callLoopTool(name string, args map[string]interface{}) CallToolResult {
	if s.control == nil {
		return mcpErrorResult(name + " is only available inside an Eggify agent session")
	}
	req := map[string]interface{}{}
	for _, k := range []string{"goal", "verify_command", "working_directory", "model", "provider", "context", "session_id", "text", "task", "mode", "reviews_session_id"} {
		if v, ok := args[k].(string); ok && v != "" {
			req[k] = v
		}
	}
	if v, ok := args["worktree"].(bool); ok {
		req["worktree"] = v
	}
	for _, k := range []string{"max_iterations", "timeout_minutes"} {
		if v, ok := args[k].(float64); ok {
			req[k] = int(v)
		}
	}

	path := map[string]string{
		"start_loop_session":     n0.ControlLoopStartPath,
		"start_worker_session":   n0.ControlWorkerStartPath,
		"get_loop_session":       n0.ControlLoopGetPath,
		"message_loop_session":   n0.ControlLoopMessagePath,
		"message_parent_session": n0.ControlLoopToParentPath,
	}[name]

	var out json.RawMessage
	if err := s.control.post(context.Background(), path, req, &out); err != nil {
		return mcpErrorResult(err.Error())
	}
	pretty := out
	var v interface{}
	if json.Unmarshal(out, &v) == nil {
		if b, err := json.MarshalIndent(v, "", "  "); err == nil {
			pretty = b
		}
	}
	return CallToolResult{Content: []ContentBlock{{Type: "text", Text: string(pretty)}}}
}

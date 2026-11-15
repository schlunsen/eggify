package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	codex "github.com/schlunsen/codex-sdk-go"
	codextypes "github.com/schlunsen/codex-sdk-go/types"
	"github.com/schlunsen/eggify/internal/codexapp"
)

type codexSubmission struct {
	inputs  []codextypes.UserInput
	cleanup func()
}

// All mutable fields are guarded by the session's codexTurnMu.
type codexRun struct {
	ctx       context.Context
	accepting bool
	pending   []codexSubmission
	wake      chan struct{}
	done      chan struct{}
}

func codexAppInputs(inputs []codextypes.UserInput) []map[string]any {
	out := make([]map[string]any, 0, len(inputs))
	for _, in := range inputs {
		if in.Type == codextypes.UserInputLocalImage {
			out = append(out, map[string]any{"type": "localImage", "path": in.Path})
		} else {
			out = append(out, map[string]any{"type": "text", "text": in.Text})
		}
	}
	return out
}

func (sm *SessionManager) runCodexApp(session *AgentSession, run *codexRun, inputs []codextypes.UserInput, sequence int) (err error) {
	client, err := codex.New(nil)
	if err != nil {
		return err
	}
	key, baseURL := sm.resolveCodexConfig(session)
	// With an API key, Codex talks to the provider proxy with a session
	// capability; the real key stays in the Eggify server (provider_proxy.go).
	keyEnv, baseURL := sm.codexProviderEnv(session.ID, key, baseURL)
	env := append(os.Environ(), keyEnv...)
	app, err := codexapp.Start(run.ctx, client.ExecutablePath(), env, codexWorkingDir(session))
	if err != nil {
		return err
	}
	defer app.Close()
	call := func(method string, params, result any) error {
		ctx, cancel := context.WithTimeout(run.ctx, 30*time.Second)
		defer cancel()
		return app.Call(ctx, method, params, result)
	}
	if err = call("initialize", map[string]any{"clientInfo": map[string]any{"name": "eggify", "version": "1.0"}}, nil); err != nil {
		return err
	}
	if err = app.Notify("initialized", map[string]any{}); err != nil {
		return err
	}
	config := sm.codexEggifyToolsConfig(session)
	if config == nil {
		config = map[string]any{}
	}
	// Use a process-local provider so API-key sessions never overwrite the user's
	// codex login credentials on disk.
	if key != "" {
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		config["model_provider"] = "eggify_openai"
		config["model_providers.eggify_openai"] = map[string]any{"name": "OpenAI", "base_url": baseURL, "env_key": "EGGIFY_CODEX_API_KEY", "wire_api": "responses"}
	} else if baseURL != "" {
		config["openai_base_url"] = baseURL
	}

	if effort := codexReasoningEffort(session.Options.EffortLevel); effort != "" {
		config["model_reasoning_effort"] = string(effort)
	}
	params := map[string]any{"approvalPolicy": "never", "config": config}
	wd := codexWorkingDir(session)
	if wd != "" {
		params["cwd"] = wd
	}
	if isSandboxed(&session.Options) {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot protect Codex session secrets: %w", err)
		}
		// filepath.Abs("") is the server's own cwd (often /), never a boundary.
		if strings.TrimSpace(wd) == "" {
			return fmt.Errorf("a sandboxed Codex session needs a working directory")
		}
		wd, err = filepath.Abs(wd)
		if err != nil {
			return fmt.Errorf("cannot resolve Codex workspace boundary: %w", err)
		}
		for k, v := range codexSandboxConfig(home, wd, &session.Options) {
			config[k] = v
		}
		// Explicit on start AND resume: never inherit a thread's old policy.
		params["permissions"] = codexSandboxProfile
		params["cwd"] = wd
		params["runtimeWorkspaceRoots"] = []string{wd}
	} else {
		params["sandbox"] = codexSandboxMode(&session.Options)
	}
	if session.ModelName != "" {
		params["model"] = session.ModelName
	}
	sm.mu.Lock()
	threadID := codexStr(session.Options.CodexThreadID)
	sm.mu.Unlock()
	var replayContext string
	method := "thread/start"
	if threadID != "" {
		method = "thread/resume"
		params["threadId"] = threadID
	} else {
		if sm.historyReplayPending(session) {
			replayContext = sm.historyReplayPrefix(session, sequence)
			if replayContext != "" {
				inputs = append([]codextypes.UserInput{codextypes.TextInput(replayContext)}, inputs...)
			}
		}
		if preamble := sm.withN0Hint(session.ID, joinNonEmpty(codexPreamble(session), sm.skillIndexPrompt(&session.Options))); preamble != "" {
			inputs = append([]codextypes.UserInput{codextypes.TextInput(preamble)}, inputs...)
		}
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err = call(method, params, &thread); err != nil {
		return err
	}
	threadID = thread.Thread.ID
	if threadID == "" {
		return fmt.Errorf("codex app-server returned an empty thread ID")
	}
	var turnID string
	startTurn := func(in []codextypes.UserInput) error {
		var response struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if e := call("turn/start", map[string]any{"threadId": threadID, "input": codexAppInputs(in)}, &response); e != nil {
			return e
		}
		turnID = response.Turn.ID
		if turnID == "" {
			return fmt.Errorf("codex app-server returned an empty turn ID")
		}
		return nil
	}
	if err = startTurn(inputs); err != nil {
		return err
	}
	sm.rememberCodexThread(session, threadID)
	subagents := newCodexSubagents(sm, session, threadID)
	// Sub-agent threads live in this app-server process and end with it.
	defer func() {
		status := "stopped"
		if err != nil {
			status = "interrupted"
		}
		subagents.finishAll(status)
	}()
	sm.captureModelSwitchContext(session.ID, sequence, replayContext, "text")
	start := time.Now()
	states := map[string]*codexTurnState{}
	var usage *codextypes.Usage
	// Images must remain available until all steered input has been consumed.
	var cleanups []func()
	defer func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}()
	for {
		select {
		case <-run.ctx.Done():
			return run.ctx.Err()
		case <-run.wake:
			session.codexTurnMu.Lock()
			pending := run.pending
			run.pending = nil
			session.codexTurnMu.Unlock()
			for _, p := range pending {
				cleanups = append(cleanups, p.cleanup)
			}
			for _, p := range pending {
				err = call("turn/steer", map[string]any{"threadId": threadID, "expectedTurnId": turnID, "input": codexAppInputs(p.inputs)}, nil)
				if err != nil {
					// Only an explicit no-active-turn rejection is safe to retry. A lost
					// acknowledgement may already have applied the input; never duplicate it.
					if !codexNoActiveTurn(err) {
						return err
					}
					if err = startTurn(p.inputs); err != nil {
						return err
					}
				}
			}
		case event, ok := <-app.Events():
			if !ok {
				if err = app.Err(); err != nil {
					return err
				}
				return fmt.Errorf("codex app-server closed before turn completion")
			}
			var p struct {
				ThreadID string          `json:"threadId"`
				TurnID   string          `json:"turnId"`
				Item     json.RawMessage `json:"item"`
				Turn     struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Error  *struct {
						Message string `json:"message"`
					} `json:"error"`
				} `json:"turn"`
				Plan []struct {
					Step   string `json:"step"`
					Status string `json:"status"`
				} `json:"plan"`
				TokenUsage struct {
					ModelContextWindow int `json:"modelContextWindow"`
					Last               struct {
						TotalTokens       int   `json:"totalTokens"`
						InputTokens       int64 `json:"inputTokens"`
						OutputTokens      int64 `json:"outputTokens"`
						CachedInputTokens int64 `json:"cachedInputTokens"`
					} `json:"last"`
				} `json:"tokenUsage"`
			}
			if err = json.Unmarshal(event.Params, &p); err != nil {
				return err
			}
			if p.ThreadID != "" && !subagents.known(p.ThreadID) {
				continue
			}
			child := p.ThreadID != "" && p.ThreadID != threadID
			state := states[p.TurnID]
			if state == nil {
				state = newCodexTurnState()
				states[p.TurnID] = state
			}
			switch event.Method {
			case "turn/plan/updated":
				if child {
					continue
				}
				todos := make([]codextypes.TodoItem, 0, len(p.Plan))
				for _, step := range p.Plan {
					todos = append(todos, codextypes.TodoItem{Text: step.Step, Completed: step.Status == "completed"})
				}
				sm.applyCodexEmits(session, state.handleItemEvent(&codextypes.ItemUpdatedEvent{Type: codextypes.EventTypeItemUpdated, Item: &codextypes.TodoListItem{ID: "plan", Items: todos}}))
			case "item/started", "item/completed":
				if event.Method == "item/completed" {
					var item struct {
						Type     string `json:"type"`
						CWD      string `json:"cwd"`
						Command  string `json:"command"`
						Status   string `json:"status"`
						ExitCode *int   `json:"exitCode"`
					}
					if json.Unmarshal(p.Item, &item) == nil && item.Type == "commandExecution" && item.Status == "completed" && (item.ExitCode == nil || *item.ExitCode == 0) {
						dir := workspaceToolDirectory(map[string]interface{}{"cwd": item.CWD, "command": item.Command}, codexWorkingDir(session))
						sm.observeSessionWorkspace(session, dir)
					}
				}
				sender := threadID
				if child {
					sender = p.ThreadID
				}
				if isCollabItem(p.Item) {
					subagents.handleCollabItem(sender, event.Method, p.Item, state)
					continue
				}
				emits, e := state.handleAppItem(event.Method, p.Item)
				if e != nil {
					return e
				}
				if child {
					subagents.childEmits(p.ThreadID, emits)
				} else {
					sm.applyCodexEmits(session, emits)
				}
			case "thread/tokenUsage/updated":
				if child {
					continue
				}
				u := p.TokenUsage.Last
				reading := codexContextReading(session.ModelName, u.TotalTokens, p.TokenUsage.ModelContextWindow)
				session.codexTurnMu.Lock()
				session.codexContextUsage = reading
				session.codexContextThread = threadID
				session.codexTurnMu.Unlock()
				usage = &codextypes.Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedInputTokens: u.CachedInputTokens}
			case "turn/completed":
				if child {
					var message string
					if p.Turn.Error != nil {
						message = p.Turn.Error.Message
					}
					subagents.childTurnCompleted(p.ThreadID, p.Turn.Status, message)
					continue
				}
				if p.Turn.ID != turnID {
					continue
				}
				if p.Turn.Error != nil {
					return fmt.Errorf("codex turn failed: %s", p.Turn.Error.Message)
				}
				if p.Turn.Status == "failed" {
					return fmt.Errorf("codex turn failed")
				}
				session.codexTurnMu.Lock()
				pending := run.pending
				run.pending = nil
				if len(pending) == 0 {
					run.accepting = false
				}
				session.codexTurnMu.Unlock()
				if len(pending) == 0 {
					sm.sendDirectResultMessage(session, start, 1, codexUsageToLLM(usage))
					return nil
				}
				var next []codextypes.UserInput
				for _, p := range pending {
					next = append(next, p.inputs...)
					cleanups = append(cleanups, p.cleanup)
				}
				if err = startTurn(next); err != nil {
					return err
				}
			}
		}
	}
}

func codexNoActiveTurn(err error) bool {
	var rpc *codexapp.RPCError
	if !errors.As(err, &rpc) {
		return false
	}
	message := strings.ToLower(rpc.Message)
	return strings.Contains(message, "no active turn")
}

// Adapt app-server's camelCase items to the existing SDK event mapper so tool
// pairing, persistence and frontend rendering keep the same behavior.
func (s *codexTurnState) handleAppItem(method string, raw json.RawMessage) ([]codexEmit, error) {
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, err
	}
	var kind string
	if err := json.Unmarshal(item["type"], &kind); err != nil {
		return nil, err
	}
	names := map[string]string{"agentMessage": "agent_message", "commandExecution": "command_execution", "fileChange": "file_change", "mcpToolCall": "mcp_tool_call", "webSearch": "web_search"}
	if name, ok := names[kind]; ok {
		item["type"], _ = json.Marshal(name)
	}
	for from, to := range map[string]string{"aggregatedOutput": "aggregated_output", "exitCode": "exit_code"} {
		if v, ok := item[from]; ok {
			item[to] = v
			delete(item, from)
		}
	}
	if kind == "reasoning" {
		var summary, content []string
		_ = json.Unmarshal(item["summary"], &summary)
		_ = json.Unmarshal(item["content"], &content)
		if len(summary) == 0 {
			summary = content
		}
		item["text"], _ = json.Marshal(strings.Join(summary, "\n"))
	}
	if kind == "fileChange" {
		var changes []map[string]json.RawMessage
		if err := json.Unmarshal(item["changes"], &changes); err != nil {
			return nil, err
		}
		for _, change := range changes {
			var k struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(change["kind"], &k) == nil {
				change["kind"], _ = json.Marshal(k.Type)
			}
		}
		item["changes"], _ = json.Marshal(changes)
	}
	if status, ok := item["status"]; ok && string(status) == `"inProgress"` {
		item["status"] = json.RawMessage(`"in_progress"`)
	}
	eventType := codextypes.EventTypeItemStarted
	if method == "item/completed" {
		eventType = codextypes.EventTypeItemCompleted
	}
	data, err := json.Marshal(map[string]any{"type": eventType, "item": item})
	if err != nil {
		return nil, err
	}
	ev, err := codextypes.ParseThreadEvent(data)
	if err != nil {
		return nil, err
	}
	if ie, ok := ev.(codextypes.ItemEvent); ok {
		return s.handleItemEvent(ie), nil
	}
	return nil, nil
}

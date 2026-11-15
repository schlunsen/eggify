// Package agents - direct_provider.go implements the non-Claude model execution path.
// When a session uses a non-Claude model (qwen, deepseek, gpt, ollama, etc.),
// this code handles the full prompt → model → tools → response cycle using:
//
//   - Go-native MCP client (direct stdio, no Claude CLI)
//   - OpenAI-compatible API client (with function calling)
//   - Agentic tool execution loop
//
// This replaces the Claude CLI's role for non-Claude models, ensuring MCP tools
// (GPU, deploy, sessions, skills, etc.) are available to ALL models.
package agents

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/schlunsen/eggify/internal/capability"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/llmclient"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/mcpclient"
)

// MaxToolLoopIterations prevents infinite tool call loops for non-Claude models.
// Set high to support agentic workflows where models chain many tool calls
// (e.g. running commands on remote machines, multi-step deployments).
const MaxToolLoopIterations = 200

// isClaudeModel returns true if the model name indicates a Claude model.
// Handles full names ("claude-3.5-sonnet"), proxy-style names
// ("anthropic/claude-3.5-sonnet"), and shorthands ("opus", "sonnet", "haiku").
func isClaudeModel(modelName string) bool {
	lower := strings.ToLower(modelName)
	if strings.Contains(lower, "claude") {
		return true
	}
	// Recognize Claude model family shorthands
	claudeShorthands := []string{"opus", "sonnet", "haiku", "fable"}
	for _, s := range claudeShorthands {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// shouldUseDirectProvider checks whether a non-Claude model should use the direct
// OpenAI-compatible path or stay on the Claude SDK path (which supports Anthropic-compatible APIs).
//
// Providers with Anthropic-format URLs (ending in /anthropic or /v1/messages) were set up
// to work with the Claude SDK and should continue using it. Only providers with OpenAI-compatible
// URLs (or no URL at all) need the direct provider path.
func (sm *SessionManager) shouldUseDirectProvider(session *AgentSession) bool {
	// Resolve the raw (pre-normalization) provider URL
	rawBaseURL := ""

	if session.Options.BaseURL != nil && *session.Options.BaseURL != "" {
		rawBaseURL = *session.Options.BaseURL
		logging.Info("shouldUseDirectProvider: got URL from session options: %s", rawBaseURL)
	}

	logging.Info("shouldUseDirectProvider: db=%v, rawBaseURL=%q", sm.db != nil, rawBaseURL)

	if rawBaseURL == "" && sm.db != nil {
		// Determine provider ID
		providerID := ""
		if session.Options.Provider != nil && *session.Options.Provider != "" {
			providerID = *session.Options.Provider
		} else {
			providerID = inferDirectProviderID(session.ModelName)
		}

		for _, pid := range []string{providerID, "custom"} {
			if pid == "" {
				continue
			}
			var dbURL string
			dbErr := sm.db.QueryRow("SELECT COALESCE(NULLIF(custom_url, ''), NULLIF(base_url, ''), '') FROM providers WHERE provider_id = ? LIMIT 1", pid).Scan(&dbURL)
			if dbErr == nil && dbURL != "" {
				rawBaseURL = dbURL
				break
			}
		}
	}

	// If the URL is in Anthropic format (/anthropic endpoint), the provider was set up
	// to work with the Claude SDK. Keep those on the SDK path since they need the
	// Anthropic message format (e.g. DeepSeek, Kimi, GLM).
	// URLs ending in /v1/messages are ambiguous — they could be Anthropic proxies or
	// custom endpoints. Route these through the direct provider since we can normalize
	// the URL to OpenAI-compatible format.
	trimmedURL := strings.TrimRight(rawBaseURL, "/")
	if strings.HasSuffix(trimmedURL, "/anthropic") {
		logging.Info("Direct provider: provider URL %q is Anthropic-compatible, using Claude SDK path", rawBaseURL)
		return false
	}

	// Otherwise, use the direct OpenAI-compatible path (including /v1/messages URLs)
	return true
}

// getOrCreateDirectMCPClient returns the cached MCP client for the session,
// creating and connecting a new one if needed. The client is cached on the
// AgentSession and reused across messages to avoid spawning a new subprocess
// per prompt. Returns nil if connection fails (caller should continue without tools).
func (sm *SessionManager) getOrCreateDirectMCPClient(session *AgentSession) *mcpclient.Client {
	session.directMCPMu.Lock()
	defer session.directMCPMu.Unlock()

	// Tool set changed (e.g. n0 connected/disconnected): reconnect so the
	// cached tools/list is refreshed.
	if session.directMCPClient != nil && session.directToolsStale.CompareAndSwap(true, false) {
		logging.Info("Direct provider: tools changed, reconnecting MCP client for session %s", session.ID)
		session.directMCPClient.Close()
		session.directMCPClient = nil
	}

	// Return existing connected client
	if session.directMCPClient != nil && session.directMCPClient.IsConnected() {
		logging.Debug("Direct provider: reusing cached MCP client for session %s", session.ID)
		return session.directMCPClient
	}

	// Close stale client if it exists but is disconnected
	if session.directMCPClient != nil {
		logging.Debug("Direct provider: closing stale MCP client for session %s", session.ID)
		session.directMCPClient.Close()
		session.directMCPClient = nil
	}

	// Create new client
	eggifyBinary, err := os.Executable()
	if err != nil || eggifyBinary == "" {
		logging.Warning("Direct provider: os.Executable() failed (%v), falling back to PATH", err)
		eggifyBinary = "eggify"
	}

	client := mcpclient.NewClient(eggifyBinary, "mcp-server")
	// Session id + n0 proxy capability for eggify-tools (never n0 tokens).
	env := sm.eggifyToolsEnv(session.ID)
	if env == nil {
		env = map[string]string{}
	}
	env[capability.EnvCoreTools] = "1" // this loop's Bash/Read/Write/... (only here)
	client.SetEnv(env)
	client.SetLogFunc(func(format string, args ...interface{}) {
		logging.Debug(format, args...)
	})

	// Set working directory for MCP subprocess so tools run in the project dir
	if session.Options.WorkingDirectory != nil && *session.Options.WorkingDirectory != "" {
		client.SetWorkDir(*session.Options.WorkingDirectory)
		logging.Info("Direct provider: MCP working directory: %s", *session.Options.WorkingDirectory)
	}

	if err := client.Connect(session.ctx); err != nil {
		logging.Warning("Direct provider: MCP client failed to connect (tools won't be available): %v", err)
		return nil
	}

	session.directMCPClient = client
	logging.Info("Direct provider: created new MCP client for session %s", session.ID)
	return client
}

// directMCPClientFor returns the session's eggify-tools client. Tests replace
// it: the real one re-executes the running binary as the MCP server.
var directMCPClientFor = (*SessionManager).getOrCreateDirectMCPClient

// sendPromptDirect handles the full prompt lifecycle for non-Claude models.
// It bypasses the Claude SDK/CLI entirely and uses:
//   - Direct OpenAI-compatible API calls with streaming + function calling
//   - Go-native MCP client for tool discovery and execution
func (sm *SessionManager) sendPromptDirect(session *AgentSession, sessionID uuid.UUID, prompt string, beforeSequence int) error {
	logging.Info("🔀 Direct provider path: session=%s, model=%s", sessionID, session.ModelName)

	if isDirectClear(prompt) {
		return sm.clearDirectContext(session, sessionID)
	}
	// One turn at a time: a running turn picks this prompt up (it is stored already).
	if session.joinOrStartDirectTurn(prompt) {
		logging.Info("Direct provider: session %s is mid-turn; prompt handed to the running turn", sessionID)
		return nil
	}

	// === 1. Resolve provider configuration ===
	apiKey, baseURL, err := sm.resolveDirectProviderConfig(session)
	if err != nil {
		session.finishDirectTurn(true)
		return fmt.Errorf("direct provider: failed to resolve config: %w", err)
	}

	if baseURL == "" {
		session.finishDirectTurn(true)
		return fmt.Errorf("direct provider: no base URL configured for model %s — configure it in Settings > Providers", session.ModelName)
	}

	logging.Info("Direct provider: using baseURL=%s, model=%s", baseURL, session.ModelName)

	// === 2. Get or create cached MCP client and discover tools ===
	mcpClient := directMCPClientFor(sm, session)

	// Discover MCP tools and convert to OpenAI format
	var openaiTools []llmclient.Tool
	if mcpClient != nil && mcpClient.IsConnected() {
		mcpTools, err := mcpClient.ListTools(session.ctx)
		if err != nil {
			logging.Warning("Direct provider: Failed to list MCP tools: %v", err)
		} else {
			openaiTools = mcpToolsToOpenAI(mcpTools)
			logging.Info("Direct provider: Discovered %d MCP tools for session %s", len(openaiTools), sessionID)
		}
	}

	// Filter out GPU, deploy, session management, and other platform tools
	// that non-Claude models tend to misuse. These models should focus on
	// core coding tools (Bash, Read, Write, Edit, etc.) and only get
	// advanced platform tools when explicitly needed.
	openaiTools = filterDirectProviderTools(openaiTools)

	// Large tool sets: send core tools + find_tools, list the rest by name.
	toolSet := newDirectToolSet(openaiTools, session.directLoadedToolNames())
	// Approved skills: an index in the system prompt, bodies via invoke_skill
	// (always sent when there is something to invoke).
	skillIndex := sm.skillIndexPrompt(&session.Options)
	if skillIndex != "" {
		toolSet.markCore("invoke_skill")
	}
	if toolSet.deferred {
		logging.Info("Direct provider: sending %d of %d tools (rest loadable with %s)", len(toolSet.offered()), len(openaiTools), findToolsName)
	}

	// === 3. Build conversation history ===
	messages, err := sm.buildDirectConversationHistory(sessionID, beforeSequence)
	if err != nil {
		logging.Warning("Direct provider: Failed to load history, starting fresh: %v", err)
		messages = []llmclient.ChatMessage{}
	}

	// Add current user message
	messages = append(messages, llmclient.ChatMessage{
		Role:    "user",
		Content: prompt,
	})

	// === 4. Determine system prompt ===
	systemPrompt := sm.buildDirectSystemPrompt(session, toolSet)
	if skillIndex != "" {
		systemPrompt += "\n\n" + skillIndex
	}

	// Prepend system message
	fullMessages := append([]llmclient.ChatMessage{{
		Role:    "system",
		Content: systemPrompt,
	}}, messages...)

	// === 5. Create OpenAI client ===
	client := llmclient.NewClient(baseURL, apiKey, session.ModelName)

	// === 6. Run the agentic loop in a goroutine ===
	go func() {
		var loopErr error

		defer func() {
			if r := recover(); r != nil {
				logging.Error("Direct provider: PANIC: %v", r)
				// Never leave the session marked mid-turn, or every later
				// prompt would be queued for a turn that no longer runs.
				session.finishDirectTurn(true)
			}

			// NOTE: MCP client is NOT closed here — it is cached on the session
			// and will be reused for subsequent messages. It is closed when the
			// session is deleted or cancelled (see DeleteSession).

			// Update session state — preserve error status if the loop failed.
			// A prompt that started a new turn right after this one ended owns
			// the status now; leave it alone.
			sm.mu.Lock()
			if loopErr != nil {
				errMsg := loopErr.Error()
				session.ErrorMessage = &errMsg
				session.Status = SessionStatusError
			} else if !session.directTurnRunning() {
				session.Status = SessionStatusIdle
			}
			session.UpdatedAt = time.Now()
			sm.mu.Unlock()
			sm.persistSessionState(session)

			sm.broadcastSessionUpdate(session)

			// MESSAGE RECOVERY: Only resend if messages were actually missed.
			// Avoids the "message storm" where all messages are re-broadcast unnecessarily.
			if atomic.LoadInt32(&session.missedMessageCount) > 0 {
				missed := atomic.SwapInt32(&session.missedMessageCount, 0)
				logging.Info("Direct provider: %d messages missed during session %s, scheduling recovery", missed, sessionID)
				go func() {
					time.Sleep(5 * time.Second)
					if err := sm.resendAllMessagesFromDB(session.ID); err != nil {
						logging.Debug("Direct provider: Delayed recovery failed for session %s: %v", sessionID, err)
					}
					sm.broadcastSessionUpdate(session)
				}()
			}

			logging.Info("Direct provider: Session %s execution completed (err=%v)", sessionID, loopErr)

			// Loop mode: verify and re-prompt (or stop) like the Claude SDK path.
			// An interrupted turn has latched the loop as stopped already.
			sm.loopAfterTurn(session, loopErr)
			// Linked-session messages that arrived during the turn.
			sm.drainInbox(session)
		}()

		messages := fullMessages
		for {
			loopErr = sm.runDirectToolLoop(session, sessionID, client, mcpClient, toolSet, messages)
			if loopErr != nil {
				logging.Error("Direct provider: Execution failed for session %s: %v", sessionID, loopErr)
				sm.sendDirectError(session, loopErr)
			}
			if more := session.finishDirectTurn(loopErr != nil); len(more) == 0 {
				break
			}
			// Prompts sent as the turn ended: they are stored, so rebuild from the DB.
			logging.Info("Direct provider: continuing session %s with prompts sent at the end of the turn", sessionID)
			history, err := sm.buildDirectConversationHistory(sessionID, 0)
			if err != nil {
				loopErr = err
				session.finishDirectTurn(true)
				break
			}
			messages = append([]llmclient.ChatMessage{{Role: "system", Content: systemPrompt}}, history...)
		}
	}()

	return nil
}

// runDirectToolLoop implements the agentic loop: model → tools → model → ... → text.
// Uses streaming for real-time text output to the frontend.
func (sm *SessionManager) runDirectToolLoop(
	session *AgentSession,
	sessionID uuid.UUID,
	client *llmclient.Client,
	mcpClient *mcpclient.Client,
	toolSet *directToolSet,
	messages []llmclient.ChatMessage,
) error {
	startTime := time.Now()
	turns := 0

	for iteration := 0; iteration < MaxToolLoopIterations; iteration++ {
		turns++
		// Prompts the user sent while this turn was running join it here.
		messages = appendDirectPrompts(messages, session.takeDirectPrompts())

		tools := toolSet.offered()
		logging.Info("Direct provider: Iteration %d with %d messages, %d tools", iteration+1, len(messages), len(tools))

		// Build request
		req := llmclient.ChatCompletionRequest{
			Messages: messages,
		}
		if len(tools) > 0 {
			req.Tools = tools
			req.ToolChoice = "auto"
		}

		// Stream the model response
		stream, err := client.ChatCompletionStream(session.ctx, req)
		if err != nil {
			sm.sendDirectError(session, err)
			return fmt.Errorf("chat completion stream failed: %w", err)
		}

		if iteration == 0 {
			if snapshot, err := json.MarshalIndent(messages, "", "  "); err == nil {
				sm.captureModelSwitchContext(sessionID, 0, string(snapshot), "json")
			}
		}

		// Accumulate the full response from stream events
		var textContent strings.Builder
		var accToolCalls []llmclient.ToolCall // Accumulated tool calls
		var finishReason string
		var usage *llmclient.Usage

		for event := range stream {
			switch event.Type {
			case llmclient.StreamEventTextDelta:
				textContent.WriteString(event.Text)

			case llmclient.StreamEventToolCallDelta:
				// Accumulate streamed tool call fragments
				for _, tc := range event.ToolCalls {
					idx := 0
					if tc.Index != nil {
						idx = *tc.Index
					}
					// Grow slice if needed
					for len(accToolCalls) <= idx {
						accToolCalls = append(accToolCalls, llmclient.ToolCall{Type: "function"})
					}
					if tc.ID != "" {
						accToolCalls[idx].ID = tc.ID
					}
					if tc.Function.Name != "" {
						accToolCalls[idx].Function.Name += tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						accToolCalls[idx].Function.Arguments += tc.Function.Arguments
					}
				}

			case llmclient.StreamEventDone:
				// Done can arrive more than once (finish chunk, usage chunk,
				// "[DONE]"); a later empty one must not wipe what came before.
				if event.FinishReason != "" {
					finishReason = event.FinishReason
				}
				if event.Usage != nil {
					usage = event.Usage
				}

			case llmclient.StreamEventError:
				sm.sendDirectError(session, event.Error)
				return fmt.Errorf("stream error: %w", event.Error)
			}
		}

		// Send accumulated text as a single message to frontend
		if textContent.Len() > 0 {
			sm.sendDirectTextMessage(session, textContent.String())
		}

		// Build the assistant message for conversation history
		assistantMsg := llmclient.ChatMessage{
			Role: "assistant",
		}
		if textContent.Len() > 0 {
			assistantMsg.Content = textContent.String()
		}
		if len(accToolCalls) > 0 {
			assistantMsg.ToolCalls = accToolCalls
		}

		// Append assistant message to conversation
		messages = append(messages, assistantMsg)

		// If no tool calls, we're done. Only check for tool calls presence — some models
		// (e.g. Qwen) may return finish_reason "stop" alongside tool calls instead of
		// the spec-correct "tool_calls".
		if len(accToolCalls) == 0 {
			if more := session.takeDirectPrompts(); len(more) > 0 {
				messages = appendDirectPrompts(messages, more)
				continue
			}
			logging.Info("Direct provider: Completed after %d turns (finish_reason=%s)", turns, finishReason)
			sm.sendDirectResultMessage(session, startTime, turns, usage)
			return nil
		}

		// Process tool calls
		logging.Info("Direct provider: Model requested %d tool calls", len(accToolCalls))

		for _, tc := range accToolCalls {
			toolName := tc.Function.Name
			toolCallID := tc.ID

			// Parse arguments for frontend display
			var inputMap map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &inputMap); err != nil {
				logging.Warning("Direct provider: Failed to parse tool args for display: %v (raw: %s)", err, tc.Function.Arguments)
				inputMap = map[string]interface{}{"_raw": tc.Function.Arguments}
			}
			sm.sendDirectToolUse(session, toolCallID, toolName, inputMap)

			// find_tools is answered here; it never reaches MCP.
			if toolName == findToolsName && toolSet.deferred {
				query, _ := inputMap["query"].(string)
				found := toolSet.findTools(query)
				session.rememberDirectLoadedTools(toolSet.loaded)
				messages = append(messages, llmclient.ChatMessage{
					Role: "tool", ToolCallID: toolCallID, Content: found,
				})
				sm.finishDirectToolCall(session, sessionID, toolCallID, found, false)
				continue
			}
			if !toolSet.has(toolName) {
				unknown := fmt.Sprintf("Unknown tool %q. Use only tools you have been given", toolName)
				if toolSet.deferred {
					unknown += "; load others with " + findToolsName
				}
				messages = append(messages, llmclient.ChatMessage{
					Role: "tool", ToolCallID: toolCallID, Content: unknown + ".",
				})
				sm.finishDirectToolCall(session, sessionID, toolCallID, unknown+".", true)
				continue
			}
			// A listed tool called by its exact name is loaded on the way.
			if added := toolSet.load(toolName); len(added) > 0 {
				session.rememberDirectLoadedTools(toolSet.loaded)
			}

			// Parse arguments for execution
			args, err := llmclient.ParseToolCallArguments(tc.Function.Arguments)
			if err != nil {
				errMsg := fmt.Sprintf("Failed to parse tool arguments: %v", err)
				messages = append(messages, llmclient.ChatMessage{
					Role: "tool", ToolCallID: toolCallID, Content: errMsg,
				})
				sm.finishDirectToolCall(session, sessionID, toolCallID, errMsg, true)
				continue
			}

			// Check permission
			if !sm.checkDirectToolPermission(session, toolName, args) {
				deniedMsg := fmt.Sprintf("Tool call denied by user: %s", toolName)
				messages = append(messages, llmclient.ChatMessage{
					Role: "tool", ToolCallID: toolCallID, Content: deniedMsg,
				})
				sm.finishDirectToolCall(session, sessionID, toolCallID, deniedMsg, true)
				continue
			}

			// Execute via MCP (per-call timeout is enforced inside mcpclient.CallTool)
			result, err := mcpClient.CallTool(session.ctx, toolName, args)
			if err != nil {
				errMsg := fmt.Sprintf("Tool execution error: %v", err)
				messages = append(messages, llmclient.ChatMessage{
					Role: "tool", ToolCallID: toolCallID, Content: errMsg,
				})
				sm.finishDirectToolCall(session, sessionID, toolCallID, errMsg, true)
				continue
			}

			resultText := extractMCPResultText(result)
			isError := result.IsError != nil && *result.IsError

			messages = append(messages, llmclient.ChatMessage{
				Role: "tool", ToolCallID: toolCallID, Content: truncateMiddle(resultText, directToolResultMaxChars),
			})
			sm.finishDirectToolCall(session, sessionID, toolCallID, resultText, isError)
		}

		// Check for cancellation before next iteration
		select {
		case <-session.ctx.Done():
			return session.ctx.Err()
		default:
		}
	}

	return fmt.Errorf("max tool loop iterations (%d) exceeded", MaxToolLoopIterations)
}

// === Message Sending Helpers ===
// These send SDK-compatible messages to the responseChan so the existing
// streamFiberResponses/broadcastToSession pipeline works unchanged.

func (sm *SessionManager) sendDirectTextMessage(session *AgentSession, text string) {
	textBlock := &types.TextBlock{Type: "text", Text: text}
	msg := &types.AssistantMessage{
		Type:    "assistant",
		Content: []types.ContentBlock{textBlock},
	}

	// Persist to database so messages survive session re-entry
	sm.mu.Lock()
	session.MessageCount++
	seq := session.MessageCount
	sm.mu.Unlock()

	if saved, err := sm.persistSDKMessage(session.ID, seq, msg); err != nil {
		logging.Error("Direct provider: Failed to persist text message: %v", err)
	} else if !saved {
		sm.releaseSequence(session, seq)
	}

	sm.observeWorkerMessage(session, msg)
	logDroppedResponse(session, "text message", sm.sendResponse(session, msg))
}

func (sm *SessionManager) sendDirectToolUse(session *AgentSession, id, name string, input map[string]interface{}) {
	toolBlock := &types.ToolUseBlock{
		Type:  "tool_use",
		ID:    id,
		Name:  name,
		Input: input,
	}
	msg := &types.AssistantMessage{
		Type:    "assistant",
		Content: []types.ContentBlock{toolBlock},
	}

	// Persist tool use to database
	sm.mu.Lock()
	session.MessageCount++
	seq := session.MessageCount
	sm.mu.Unlock()

	if saved, err := sm.persistSDKMessage(session.ID, seq, msg); err != nil {
		logging.Error("Direct provider: Failed to persist tool use message: %v", err)
	} else if !saved {
		sm.releaseSequence(session, seq)
	}

	sm.observeWorkerMessage(session, msg)
	logDroppedResponse(session, "tool use message", sm.sendResponse(session, msg))
}

func (sm *SessionManager) sendDirectToolResult(session *AgentSession, toolCallID, result string, isError bool) {
	toolResultBlock := &types.ToolResultBlock{
		Type:      "tool_result",
		ToolUseID: toolCallID,
		Content:   result,
		IsError:   &isError,
	}
	msg := &types.UserMessage{
		Type:    "user",
		Content: []types.ContentBlock{toolResultBlock},
	}
	sm.observeWorkerMessage(session, msg)
	logDroppedResponse(session, "tool result message", sm.sendResponse(session, msg))
}

// finishDirectToolCall shows a tool result in the UI and stores it with its
// tool call so later prompts replay it. Direct path only (Codex shares
// sendDirectToolResult but keeps its history in its own thread).
func (sm *SessionManager) finishDirectToolCall(session *AgentSession, sessionID uuid.UUID, toolCallID, result string, isError bool) {
	sm.sendDirectToolResult(session, toolCallID, result, isError)
	if err := sm.saveDirectToolResult(sessionID, toolCallID, result, isError); err != nil {
		logging.Warning("Direct provider: could not store result of %s: %v", toolCallID, err)
	}
}

func (sm *SessionManager) sendDirectResultMessage(session *AgentSession, startTime time.Time, turns int, usage *llmclient.Usage) {
	duration := time.Since(startTime)
	resultMsg := &types.ResultMessage{
		Type:          "result",
		DurationMs:    int(duration.Milliseconds()),
		DurationAPIMs: int(duration.Milliseconds()),
		NumTurns:      turns,
		IsError:       false,
	}
	if usage != nil {
		resultMsg.Usage = map[string]interface{}{
			"input_tokens":  usage.PromptTokens,
			"output_tokens": usage.CompletionTokens,
			"total_tokens":  usage.TotalTokens,
		}
	}
	logDroppedResponse(session, "result message", sm.sendResponse(session, resultMsg))
}

func (sm *SessionManager) sendDirectError(session *AgentSession, err error) {
	errMsg := err.Error()
	resultMsg := &types.ResultMessage{
		Type:    "result",
		IsError: true,
		Result:  &errMsg,
	}
	logDroppedResponse(session, "error message", sm.sendResponse(session, resultMsg))
}

// === Permission Handling ===

func (sm *SessionManager) checkDirectToolPermission(session *AgentSession, toolName string, args map[string]interface{}) bool {
	// YOLO mode — auto-approve everything
	if session.Options.DangerouslySkipPermissions != nil && *session.Options.DangerouslySkipPermissions {
		return true
	}

	// Allow-all mode
	if session.Options.PermissionMode != nil && *session.Options.PermissionMode == "allow-all" {
		return true
	}

	// Check always-allow rules with the same matcher as the Claude SDK path:
	// a rule only covers calls whose command prefix / directory it allows.
	if matched, _ := CheckAlwaysAllowRules(session.Options.AlwaysAllowRules, toolName, args); matched {
		return true
	}

	// Ask user via WebSocket
	if !session.IsWebSocketConnected() {
		logging.Warning("Direct provider: WebSocket not connected, denying tool %s", toolName)
		return false
	}

	requestID := uuid.New().String()
	responseChan := make(chan PermissionResponse, 1)

	session.permMu.Lock()
	session.pendingPermissions[requestID] = responseChan
	session.trackPendingPermission(requestID, toolName, args)
	session.permMu.Unlock()

	defer func() {
		session.permMu.Lock()
		delete(session.pendingPermissions, requestID)
		delete(session.pendingPermissionData, requestID)
		session.permMu.Unlock()
	}()

	permReq := &PermissionRequest{
		RequestID:    requestID,
		ToolName:     toolName,
		Input:        args,
		ResponseChan: responseChan,
	}

	select {
	case session.permissionReqChan <- permReq:
	case <-session.ctx.Done():
		return false
	case <-time.After(5 * time.Second):
		return false
	}

	select {
	case response := <-responseChan:
		return response.Approved
	case <-session.ctx.Done():
		return false
	case <-time.After(60 * time.Second):
		return false
	}
}

// === Provider Config Resolution ===

func (sm *SessionManager) resolveDirectProviderConfig(session *AgentSession) (apiKey, baseURL string, err error) {
	// Session-specific overrides
	if session.Options.APIKey != nil && *session.Options.APIKey != "" {
		apiKey = *session.Options.APIKey
	}
	if session.Options.BaseURL != nil && *session.Options.BaseURL != "" {
		baseURL = *session.Options.BaseURL
	}

	// Infer provider from model name
	providerID := ""
	if session.Options.Provider != nil && *session.Options.Provider != "" {
		providerID = *session.Options.Provider
	} else {
		providerID = inferDirectProviderID(session.ModelName)
	}

	// SECURITY: a session base URL gets the session's own key, or the stored
	// key of a provider whose own host it is; never the config/env key or
	// another provider's key (provider_key_scope.go).
	if baseURL != "" {
		if _, verr := llmclient.ValidateBaseURL(baseURL); verr != nil {
			return "", "", fmt.Errorf("session base URL: %w", verr)
		}
		if apiKey == "" && sm.db != nil {
			for _, pid := range []string{providerID, "custom"} {
				if pid == "" {
					continue
				}
				var defaults []string
				if pid == providerID {
					if d := directDefaultBase(providerID); d != "" {
						defaults = append(defaults, d)
					}
				}
				if !sm.storedKeyMayReach(pid, baseURL, defaults...) {
					continue
				}
				var dbKey string
				if dbErr := sm.db.QueryRow("SELECT COALESCE(api_key, '') FROM providers WHERE provider_id = ? LIMIT 1", pid).Scan(&dbKey); dbErr == nil && dbKey != "" {
					apiKey = dbKey
					break
				}
			}
		}
		if apiKey == "" {
			logging.Warning("SECURITY: direct session %s uses base URL %s without a key of its own; no stored key is sent there",
				session.ID, llmclient.Origin(baseURL))
		}
		return apiKey, normalizeBaseURLForOpenAI(baseURL), nil
	}

	// Try database — first with inferred provider ID, then fall back to "custom"
	if sm.db != nil {
		for _, pid := range []string{providerID, "custom"} {
			if pid == "" {
				continue
			}
			if apiKey == "" {
				var dbKey string
				dbErr := sm.db.QueryRow("SELECT api_key FROM providers WHERE provider_id = ? LIMIT 1", pid).Scan(&dbKey)
				if dbErr == nil && dbKey != "" {
					apiKey = dbKey
					logging.Debug("Direct provider: resolved API key from provider %q", pid)
				}
			}
			if baseURL == "" {
				var dbURL string
				dbErr := sm.db.QueryRow("SELECT COALESCE(NULLIF(custom_url, ''), NULLIF(base_url, ''), '') FROM providers WHERE provider_id = ? LIMIT 1", pid).Scan(&dbURL)
				if dbErr == nil && dbURL != "" {
					baseURL = dbURL
					logging.Debug("Direct provider: resolved base URL from provider %q: %s", pid, dbURL)
				}
			}
			if apiKey != "" && baseURL != "" {
				break
			}
		}
	}

	// Fallback to config default
	if apiKey == "" {
		apiKey = sm.config.APIKey
	}

	// Hardcoded provider defaults
	if baseURL == "" {
		baseURL = directDefaultBase(providerID)
	}

	// Convert Anthropic-format URLs to OpenAI-compatible format.
	// Providers in the DB often store Anthropic API paths (e.g. /anthropic, /v1/messages)
	// but the direct provider uses OpenAI-compatible /chat/completions.
	baseURL = normalizeBaseURLForOpenAI(baseURL)

	return apiKey, baseURL, nil
}

// directDefaultBase is the built-in OpenAI-compatible URL of a provider.
func directDefaultBase(providerID string) string {
	switch providerID {
	case "openai":
		return "https://api.openai.com/v1"
	case "deepseek":
		return "https://api.deepseek.com/v1"
	case "glm":
		return "https://open.bigmodel.cn/api/paas/v4"
	case "qwen":
		return "https://dashscope.aliyuncs.com/compatible-mode/v1"
	case "ollama":
		return "http://localhost:11434/v1"
	}
	return ""
}

// normalizeBaseURLForOpenAI converts an Anthropic-format base URL to OpenAI-compatible format.
// The direct provider appends "/chat/completions" so the base URL must end at the version prefix.
//
// Examples:
//
//	"https://api.deepseek.com/anthropic"          → "https://api.deepseek.com/v1"
//	"https://api.moonshot.ai/anthropic"           → "https://api.moonshot.ai/v1"
//	"http://host:4001/v1/messages"                → "http://host:4001/v1"
//	"https://api.openai.com/v1"                   → "https://api.openai.com/v1" (unchanged)
//	"http://localhost:11434/v1"                    → "http://localhost:11434/v1" (unchanged)
func normalizeBaseURLForOpenAI(baseURL string) string {
	if baseURL == "" {
		return baseURL
	}

	baseURL = strings.TrimRight(baseURL, "/")

	// Strip known Anthropic-only path suffixes
	anthropicSuffixes := []string{
		"/v1/messages",
		"/anthropic",
	}
	for _, suffix := range anthropicSuffixes {
		if strings.HasSuffix(baseURL, suffix) {
			baseURL = strings.TrimSuffix(baseURL, suffix)
			// Ensure we have a /v1 path for OpenAI compat
			if !strings.HasSuffix(baseURL, "/v1") {
				baseURL += "/v1"
			}
			return baseURL
		}
	}

	// If no version path at all, add /v1
	if !strings.Contains(baseURL, "/v1") && !strings.Contains(baseURL, "/v2") {
		baseURL += "/v1"
	}

	return baseURL
}

func inferDirectProviderID(modelName string) string {
	lower := strings.ToLower(modelName)
	switch {
	case strings.HasPrefix(lower, "glm-"):
		return "glm"
	case strings.Contains(lower, "gpt-"):
		return "openai"
	case strings.Contains(lower, "deepseek"):
		return "deepseek"
	case strings.Contains(lower, "qwen"):
		return "qwen"
	case strings.Contains(lower, "llama"):
		return "ollama"
	case strings.Contains(lower, "mistral"):
		return "mistral"
	default:
		return "custom"
	}
}

// === Conversation History ===

func (sm *SessionManager) buildDirectConversationHistory(sessionID uuid.UUID, beforeSequence int) ([]llmclient.ChatMessage, error) {
	rows, err := sm.db.Query(`
		SELECT role, content, tool_uses FROM (
			SELECT sequence, role, content, tool_uses FROM agent_messages
			WHERE session_id = ? AND (? <= 0 OR sequence < ?)
			AND sequence > ?
			AND role IN ('user', 'assistant')
			ORDER BY sequence DESC LIMIT 200
		) ORDER BY sequence ASC
	`, sessionID.String(), beforeSequence, beforeSequence, sm.directHistoryStart(sessionID, beforeSequence))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []llmclient.ChatMessage

	for rows.Next() {
		var role, content string
		var toolUsesJSON sql.NullString

		if err := rows.Scan(&role, &content, &toolUsesJSON); err != nil {
			continue
		}

		switch role {
		case "user":
			messages = append(messages, llmclient.ChatMessage{
				Role:    "user",
				Content: content,
			})
		case "assistant":
			msg := llmclient.ChatMessage{
				Role:    "assistant",
				Content: content,
			}

			if toolUsesJSON.Valid && toolUsesJSON.String != "" && toolUsesJSON.String != "null" {
				var toolUses []map[string]interface{}
				if json.Unmarshal([]byte(toolUsesJSON.String), &toolUses) == nil && len(toolUses) > 0 {
					var toolCalls []llmclient.ToolCall
					for _, tu := range toolUses {
						name, _ := tu["name"].(string)
						id, _ := tu["id"].(string)
						input := tu["input"]
						inputJSON, _ := json.Marshal(input)

						toolCalls = append(toolCalls, llmclient.ToolCall{
							ID:   id,
							Type: "function",
							Function: llmclient.FunctionCall{
								Name:      name,
								Arguments: string(inputJSON),
							},
						})
					}
					if len(toolCalls) > 0 {
						msg.ToolCalls = toolCalls
						if content == "" {
							msg.Content = nil
						}
					}

					// Append assistant message first
					messages = append(messages, msg)

					// Then append tool result messages for each tool call
					for _, tu := range toolUses {
						id, _ := tu["id"].(string)
						resultText := historyResultText(tu["result"])
						if resultText == "" {
							resultText = "(tool result not available from history)"
						}
						messages = append(messages, llmclient.ChatMessage{
							Role:       "tool",
							ToolCallID: id,
							Content:    resultText,
						})
					}
					continue // Skip the default append below
				}
			}
			messages = append(messages, msg)
		}
	}

	trimOldToolResults(messages, directHistoryRecentResults, directHistoryOldResultMaxChars)
	return messages, nil
}

// === System Prompt ===

// buildDefaultDirectSystemPrompt is the base prompt for non-Claude models on the
// direct path. Tool schemas travel in the request, so they are not repeated
// here; only deferred tools are listed (by name) so the model can load them.
func buildDefaultDirectSystemPrompt(ts *directToolSet, workDir string) string {
	var b strings.Builder
	b.WriteString("You are a capable coding agent working on the user's machine. You get things done with your tools instead of giving advice.\n")
	if workDir != "" {
		fmt.Fprintf(&b, "\nWorking directory: %s\nBash and the file tools start here. Use paths relative to it instead of cd-ing into it in every command.\n", workDir)
	}
	b.WriteString(`
How to work:
1. Use your tools to do what the user asks. Never tell the user to run a command you can run yourself, and never claim you lack access.
2. Read files before you change or describe them. Do not guess their contents.
3. Call only the tools you have been given, with their exact names and parameters (Bash takes "command").
4. When several tool calls do not depend on each other, make them in the same step.
5. Earlier tool results are part of this conversation. Reuse them instead of running the same command again.
6. Prefer acting over asking when the intent is clear.
7. When you finish, reply briefly: what you did and what you found. The user already sees tool output, so do not paste long output back.
`)
	if ts != nil {
		if catalog := deferredToolCatalog(ts.deferredTools()); catalog != "" {
			fmt.Fprintf(&b, "\nMore tools are available but not loaded. Load them with %s (keywords or names), or call one by its exact name:\n%s\n", findToolsName, catalog)
		}
		if ts.hasPrefix("gpu_") {
			b.WriteString(`
GPU tools: gpu_* control REMOTE RunPod GPU servers that cost real money ($0.39-$3.49/hr). gpu_run runs on the remote server, not locally. Use them only when the user explicitly asks for GPU compute, ML training, CUDA or inference; for everything else use Bash.
`)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (sm *SessionManager) buildDirectSystemPrompt(session *AgentSession, ts *directToolSet) string {
	// Always use the default system prompt that includes tool instructions.
	// If the user has a custom system prompt (not generic defaults), prepend it.
	workDir := ""
	if session.Options.WorkingDirectory != nil {
		workDir = *session.Options.WorkingDirectory
	}
	defaultPrompt := sm.withN0Hint(session.ID, buildDefaultDirectSystemPrompt(ts, workDir))

	if session.Options.SystemPrompt == nil || *session.Options.SystemPrompt == "" || *session.Options.SystemPrompt == "code" {
		return defaultPrompt
	}

	// Skip generic/placeholder prompts that would water down tool instructions
	custom := *session.Options.SystemPrompt
	genericPrompts := []string{
		"you are a helpful ai assistant",
		"you are a helpful assistant",
	}
	for _, g := range genericPrompts {
		if strings.EqualFold(strings.TrimSpace(custom), g) {
			return defaultPrompt
		}
	}

	// Combine: custom context first, then tool instructions
	return custom + "\n\n" + defaultPrompt
}

// === MCP Tool Conversion ===
// Converts MCP tool definitions to OpenAI function calling format.
// This lives here (in agents package) to avoid import cycles.

// directProviderBlockedTools are MCP tools that should NOT be exposed to
// non-Claude models. Session management and skill tools are platform internals
// that non-Claude models don't need. GPU and deploy tools are now allowed —
// their descriptions contain strong warnings about cost and correct usage.
var directProviderBlockedTools = map[string]bool{
	// Session/handover tools — not useful for coding tasks
	"get_current_session":        true,
	"list_sessions":              true,
	"create_handover":            true,
	"accept_handover":            true,
	"create_skill_from_handover": true,
	// Hook tools — platform internals. list_skills/invoke_skill are allowed:
	// they are how these sessions use approved skills (native_skills.go).
	"get_hook_status": true,
}

// filterDirectProviderTools removes platform tools that non-Claude models
// should not have access to, keeping only core coding tools (Bash, Read, etc.).
func filterDirectProviderTools(tools []llmclient.Tool) []llmclient.Tool {
	filtered := make([]llmclient.Tool, 0, len(tools))
	var kept, blocked []string
	for _, tool := range tools {
		if !directProviderBlockedTools[tool.Function.Name] {
			filtered = append(filtered, tool)
			kept = append(kept, tool.Function.Name)
		} else {
			blocked = append(blocked, tool.Function.Name)
		}
	}
	logging.Info("Direct provider: Tool filter: %d kept %v, %d blocked %v", len(kept), kept, len(blocked), blocked)
	return filtered
}

func mcpToolsToOpenAI(tools []mcpclient.Tool) []llmclient.Tool {
	result := make([]llmclient.Tool, 0, len(tools))
	for _, tool := range tools {
		result = append(result, mcpToolToOpenAI(tool))
	}
	return result
}

func mcpToolToOpenAI(tool mcpclient.Tool) llmclient.Tool {
	// Prefer the server's schema verbatim (nested arrays/objects intact).
	if len(tool.RawInputSchema) > 0 {
		var obj map[string]interface{}
		if json.Unmarshal(tool.RawInputSchema, &obj) == nil && obj["type"] == "object" {
			return llmclient.Tool{
				Type: "function",
				Function: llmclient.Function{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.RawInputSchema,
				},
			}
		}
	}

	schema := map[string]interface{}{
		"type": tool.InputSchema.Type,
	}

	if len(tool.InputSchema.Properties) > 0 {
		props := make(map[string]interface{})
		for name, prop := range tool.InputSchema.Properties {
			propSchema := map[string]interface{}{
				"type": prop.Type,
			}
			if prop.Description != "" {
				propSchema["description"] = prop.Description
			}
			if prop.Default != nil {
				propSchema["default"] = prop.Default
			}
			if len(prop.Enum) > 0 {
				propSchema["enum"] = prop.Enum
			}
			props[name] = propSchema
		}
		schema["properties"] = props
	}

	if len(tool.InputSchema.Required) > 0 {
		schema["required"] = tool.InputSchema.Required
	}

	paramsJSON, _ := json.Marshal(schema)

	return llmclient.Tool{
		Type: "function",
		Function: llmclient.Function{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  json.RawMessage(paramsJSON),
		},
	}
}

// extractMCPResultText extracts text content from an MCP CallToolResult.
func extractMCPResultText(result *mcpclient.CallToolResult) string {
	if result == nil {
		return ""
	}
	var texts []string
	for _, block := range result.Content {
		if block.Type == "text" && block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	if len(texts) == 0 {
		data, _ := json.Marshal(result)
		return string(data)
	}
	return strings.Join(texts, "\n")
}

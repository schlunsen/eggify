package agents

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	fiberws "github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/logging"
)

// broadcastSubagentMessage sends an individual subagent message to the frontend
// so every piece of subagent activity is visible in real time for debugging.
func (h *AgentHandler) broadcastSubagentMessage(sessionID uuid.UUID, agentID string, agentType string, description string, msgType string, content string, toolName string, toolInput map[string]interface{}, isError bool, seq int, depth int) {
	if h.BackgroundAgentMgr == nil || h.BackgroundAgentMgr.broadcastCallback == nil {
		return
	}

	msg := SubagentMessageData{
		BaseMessage:  BaseMessage{Type: MessageTypeSubagentMessage},
		SessionID:    sessionID,
		AgentID:      agentID,
		AgentType:    agentType,
		Description:  description,
		MessageType:  msgType,
		Content:      content,
		ToolName:     toolName,
		ToolInput:    toolInput,
		IsError:      isError,
		SequenceNum:  seq,
		NestingDepth: depth,
		Timestamp:    time.Now(),
	}
	h.BackgroundAgentMgr.broadcastCallback(sessionID, msg)
}

// isBackgroundAgentLaunchResult checks if a tool result is a background agent launch
// confirmation (from run_in_background=true) rather than an actual completion.
// Claude Code immediately returns results like "Async agent launched successfully..."
// when the real work is still running asynchronously.
func isBackgroundAgentLaunchResult(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "async agent launched") ||
		strings.Contains(lower, "agent is working in the background") ||
		strings.Contains(lower, "output_file:") && strings.Contains(lower, "agentid:")
}

// isNoiseLine checks if a line is noise that should be filtered from subagent output
func isNoiseLine(line string) bool {
	// Filter system reminder tags
	if strings.HasPrefix(line, "<system-reminder>") || strings.HasPrefix(line, "</system-reminder>") {
		return true
	}
	// Filter XML-like system tags
	if strings.HasPrefix(line, "<system") || strings.HasPrefix(line, "</system") {
		return true
	}
	// Filter local command caveats
	if strings.Contains(line, "<local-command-caveat>") || strings.Contains(line, "</local-command-caveat>") {
		return true
	}
	// Filter command tags
	if strings.HasPrefix(line, "<command-") || strings.HasPrefix(line, "</command-") {
		return true
	}
	// Filter SVG/HTML markup
	if strings.HasPrefix(line, "<svg ") || strings.HasPrefix(line, "<path ") || strings.HasPrefix(line, "<polyline ") {
		return true
	}
	// Filter very long lines of what looks like encoded/binary data
	if len(line) > 500 && !strings.Contains(line, " ") {
		return true
	}
	return false
}

// streamFiberResponses streams Claude responses back to the Fiber WebSocket client
func (h *AgentHandler) streamFiberResponses(senderConn *fiberws.Conn, sessionID uuid.UUID, responseChan chan types.Message) {
	// Send initial "processing" state update to ALL connections for this session
	session, err := h.SessionManager.GetSession(sessionID)
	if err == nil {
		atomic.AddInt32(&session.activeStreamerCount, 1)
		defer func() {
			// Use CompareAndSwap loop to decrement without going below 0.
			// InterruptSession may reset activeStreamerCount to 0 while old
			// streamFiberResponses goroutines are still cleaning up.
			for {
				old := atomic.LoadInt32(&session.activeStreamerCount)
				if old <= 0 {
					break // Already 0 or negative, don't decrement further
				}
				if atomic.CompareAndSwapInt32(&session.activeStreamerCount, old, old-1) {
					break
				}
			}
		}()
		// Broadcast to all connections instead of just the sender
		h.broadcastSessionStateUpdate(session)
	}

	for msg := range responseChan {
		msgType := msg.GetMessageType()

		// FIXUP: Normalize empty Type field using Go type assertion
		if msgType == "" {
			switch msg.(type) {
			case *types.AssistantMessage:
				msgType = "assistant"
			case *types.UserMessage:
				msgType = "user"
			case *types.ResultMessage:
				msgType = "result"
			case *types.SystemMessage:
				msgType = "system"
			}
		}

		// Broadcast message to all connections EXCEPT the sender
		// This ensures other clients (multiple tabs, other users) get updates
		// while avoiding echo where the sender receives their own message
		h.broadcastToSession(sessionID, msg, senderConn)

		// Stop after result message (completion signal)
		if msgType == "result" {
			// Send final "idle" state update after streaming completes to ALL connections
			if session, err := h.SessionManager.GetSession(sessionID); err == nil {
				h.broadcastSessionStateUpdate(session)
			}
			return
		}
	}
}

// Preserve persisted attribution through both live recovery and buffered delivery.
type recoveredAssistantMessage struct {
	types.Message
	Provider string
	Model    string
}

// agentMessageOutput is an SDK message converted for the WebSocket clients.
// It is built once per message and then written to every receiving
// connection, so the message ID, timestamp and payload bytes are identical
// across connections and per-message side effects run exactly once.
type agentMessageOutput struct {
	response AgentMessageResponse
	// toolUseEvents are agent_tool_use events written before the response.
	toolUseEvents []map[string]interface{}
	// agentOutputTools are AgentOutputTool calls that need background-agent
	// bookkeeping.
	agentOutputTools []*types.ToolUseBlock
	// attributeFromConn is set for user messages without an explicit sender:
	// the attribution then comes from the receiving connection's user.
	attributeFromConn bool
}

// extractAssistantContent converts an assistant message's content blocks into
// the frontend payload and collects its tool-use side effects.
func extractAssistantContent(sessionID uuid.UUID, assistantMsg *types.AssistantMessage, out *agentMessageOutput) map[string]interface{} {
	var textContent []string
	var thinkingContent []string
	var toolUses []map[string]interface{}

	for _, block := range assistantMsg.Content {
		switch b := block.(type) {
		case *types.TextBlock:
			textContent = append(textContent, b.Text)
		case *types.ThinkingBlock:
			thinkingContent = append(thinkingContent, b.Thinking)
		case *types.ToolUseBlock:
			toolUses = append(toolUses, map[string]interface{}{
				"id":     b.ID,
				"name":   b.Name,
				"input":  b.Input,
				"status": "running",
			})
			// agent_tool_use event for metrics tracking
			out.toolUseEvents = append(out.toolUseEvents, map[string]interface{}{
				"type":       string(MessageTypeAgentToolUse),
				"session_id": sessionID.String(),
				"tool":       b.Name,
				"parameters": b.Input,
			})
			// Task/Agent launches are tracked once per message by the
			// session reader (observeParentMessage).
			// AskUserQuestion is handled in the permission callback.
			if b.Name == "AgentOutputTool" {
				out.agentOutputTools = append(out.agentOutputTools, b)
			}
		default:
			logging.Debug("Assistant content block %T is not text, thinking or tool_use", block)
		}
	}
	logging.Debug("Assistant message: %d text, %d thinking, %d tool_use blocks (session=%s)",
		len(textContent), len(thinkingContent), len(toolUses), sessionID)

	return map[string]interface{}{
		"type":     "assistant",
		"text":     textContent,
		"thinking": thinkingContent,
		"tools":    toolUses,
	}
}

// buildAgentMessage converts an SDK message into the agent_message response.
// It returns nil when the message must not be sent to the frontend.
// senderUserID optionally carries the sender's UUID and username for user messages.
func (h *AgentHandler) buildAgentMessage(sessionID uuid.UUID, msg types.Message, senderUserID ...string) *agentMessageOutput {
	recovered, isRecovered := msg.(*recoveredAssistantMessage)
	if isRecovered {
		msg = recovered.Message
	}
	msgType := msg.GetMessageType()

	// Parse senderUserID and senderUsername from variadic args
	var userUUID, username string
	if len(senderUserID) > 0 {
		userUUID = senderUserID[0] // UUID
	}
	if len(senderUserID) > 1 {
		username = senderUserID[1] // Actual username
	}

	out := &agentMessageOutput{}
	response := &out.response
	response.Type = MessageTypeAgentMessage
	response.ID = uuid.New().String() // Unique ID for frontend, shared by all connections
	response.SessionID = sessionID
	response.Role = msgType // CRITICAL: Set role for iOS to extract (user, assistant, system)

	// SECURITY: Add user attribution for multi-user chat display.
	// With an explicit sender (broadcast case) use it; otherwise the
	// receiving connection's user is applied at write time (direct send).
	// For assistant/system messages, UserID and Username remain nil.
	if msgType == "user" {
		if userUUID != "" {
			u := userUUID
			response.UserID = &u
			if username != "" {
				n := username
				response.Username = &n
			}
		} else {
			out.attributeFromConn = true
		}
	}

	switch msgType {
	case "assistant":
		if assistantMsg, ok := msg.(*types.AssistantMessage); ok {
			response.Content = extractAssistantContent(sessionID, assistantMsg, out)
		} else {
			logging.Warning("Failed to assert message as AssistantMessage (type=%T)", msg)
		}

	case "user":
		if userMsg, ok := msg.(*types.UserMessage); ok {
			var toolResults []map[string]interface{}
			var textContent string

			// Check if user message content is a slice of ContentBlocks
			if contentBlocks, ok := userMsg.Content.([]types.ContentBlock); ok {
				for _, block := range contentBlocks {
					if toolResultBlock, ok := block.(*types.ToolResultBlock); ok {
						toolResult := map[string]interface{}{
							"tool_use_id": toolResultBlock.ToolUseID,
							"content":     toolResultBlock.Content,
							"is_error":    toolResultBlock.IsError,
							"status":      "completed",
						}

						// Check if tool result content contains image blocks (e.g. from Read on image files)
						// Extract image preview data so the frontend can display inline thumbnails
						if contentArr, ok := toolResultBlock.Content.([]interface{}); ok {
							for _, item := range contentArr {
								if block, ok := item.(map[string]interface{}); ok {
									if block["type"] == "image" {
										toolResult["image_preview"] = block
										break
									}
								}
							}
						}

						toolResults = append(toolResults, toolResult)
					} else if textBlock, ok := block.(*types.TextBlock); ok {
						// Extract actual text content from user
						if textBlock.Text != "" {
							textContent += textBlock.Text + "\n"
						}
					}
				}
				textContent = strings.TrimSpace(textContent)

				// Only send user message if it has actual text, not just tool results
				// Tool-result-only messages are internal SDK messages and shouldn't be displayed
				// EXCEPTION: Allow tool results with image previews to pass through for inline image display
				if textContent == "" && len(toolResults) > 0 {
					hasImagePreview := false
					for _, tr := range toolResults {
						if _, ok := tr["image_preview"]; ok {
							hasImagePreview = true
						}
					}

					if !hasImagePreview {
						logging.Debug("Skipping tool-result-only user message (no text content, no images)")
						return nil
					}
					logging.Debug("Allowing image preview tool result to pass through to frontend")
				}

				response.Content = map[string]interface{}{
					"type":         "user",
					"content":      textContent,
					"tool_results": toolResults,
				}
			} else if strContent, ok := userMsg.Content.(string); ok {
				// If content is already a string, use it directly
				response.Content = map[string]interface{}{
					"type":         "user",
					"content":      strContent,
					"tool_results": toolResults,
				}
			} else {
				// Unknown content type, try to convert
				response.Content = map[string]interface{}{
					"type":         "user",
					"content":      fmt.Sprintf("%v", userMsg.Content),
					"tool_results": toolResults,
				}
			}
		}

	case "result":
		if resultMsg, ok := msg.(*types.ResultMessage); ok {
			// Check if this is an error result - send as agent_error for frontend to display prominently
			if resultMsg.IsError {
				response.Type = MessageTypeAgentError
				errorMsg := "An error occurred during agent execution"
				if resultMsg.Result != nil {
					errorMsg = *resultMsg.Result
				}

				logging.Warning("SDK error result for session %s (%d chars)", sessionID, len(errorMsg))
				response.Content = map[string]interface{}{
					"type":            "agent_error",
					"error":           errorMsg,
					"duration_ms":     resultMsg.DurationMs,
					"duration_api_ms": resultMsg.DurationAPIMs,
					"num_turns":       resultMsg.NumTurns,
					"total_cost_usd":  resultMsg.TotalCostUSD,
					"usage":           resultMsg.Usage,
				}
			} else {
				// Normal successful result
				content := map[string]interface{}{
					"type":        "result",
					"success":     true,
					"num_turns":   resultMsg.NumTurns,
					"duration_ms": resultMsg.DurationMs,
					"is_error":    resultMsg.IsError,
				}
				if resultMsg.TotalCostUSD != nil {
					content["cost_usd"] = *resultMsg.TotalCostUSD
				}
				if resultMsg.Usage != nil {
					content["usage"] = resultMsg.Usage
				}
				response.Content = content
			}
		}

	case "system", "control_request":
		if systemMsg, ok := msg.(*types.SystemMessage); ok {
			// Check if this is a control request (control_request message type)
			if msg.GetMessageType() == "control_request" && systemMsg.Request != nil {
				// AskUserQuestion is handled via the ToolUseBlock in assistant messages, not control_request
				if tool, ok := systemMsg.Request["tool"].(string); ok && tool == "AskUserQuestion" {
					logging.Debug("Skipping AskUserQuestion control_request - handled via ToolUseBlock")
					return nil
				}

				// This is a regular permission request - forward to frontend as permission_request
				logging.Debug("Permission request detected: tool=%v, permission_id=%v", systemMsg.Request["tool"], systemMsg.Request["permission_id"])
				response.Type = MessageTypePermissionRequest
				response.Content = map[string]interface{}{
					"type":          "permission_request",
					"permission_id": systemMsg.Request["permission_id"],
					"tool":          systemMsg.Request["tool"],
					"action":        systemMsg.Request["action"],
					"details":       systemMsg.Request,
				}
			} else {
				// Regular system message
				response.Content = map[string]interface{}{
					"type":    "system",
					"subtype": systemMsg.Subtype,
					"data":    systemMsg.Data,
				}
			}
		}

	default:
		// FALLBACK: The SDK sometimes sends messages with empty Type field.
		// Use Go type assertion to recover and route them correctly.
		if typedMsg, ok := msg.(*types.AssistantMessage); ok {
			logging.Debug("Recovered AssistantMessage with empty Type field, routing as assistant (session=%s)", sessionID)
			response.Content = extractAssistantContent(sessionID, typedMsg, out)
			response.Role = "assistant"
		} else {
			// Truly unknown — log and skip
			logging.Debug("Skipping unknown message type: %q (go_type=%T, session=%s)", msgType, msg, sessionID)
			return nil
		}
	}

	// Include model attribution in the live reply, as well as persisted history.
	if assistant, ok := msg.(*types.AssistantMessage); ok {
		response.ModelName = assistant.Model
	}
	// Add git branch to metadata
	session, err := h.SessionManager.GetSession(sessionID)
	if isRecovered {
		response.Provider, response.ModelName = recovered.Provider, recovered.Model
	} else if err == nil && response.Role == "assistant" {
		response.Provider = effectiveProvider(session)
		if response.ModelName == "" {
			response.ModelName = h.SessionManager.effectiveModel(session)
		}
	}
	metadata := map[string]interface{}{}
	if err == nil && session.GitBranch != "" {
		metadata["git_branch"] = session.GitBranch
	}
	// Add timestamp for frontend
	metadata["created_at"] = time.Now().UTC().Format(time.RFC3339)
	response.Metadata = metadata

	return out
}

// emitAgentMessageSideEffects runs the per-message (not per-connection) side
// effects of a converted message: analytics events and AgentOutputTool
// bookkeeping.
func (h *AgentHandler) emitAgentMessageSideEffects(sessionID uuid.UUID, out *agentMessageOutput) {
	if h.analyticsHub != nil {
		for _, ev := range out.toolUseEvents {
			// For ActivityHistory display
			h.analyticsHub.BroadcastData("agent_tool_use", ev)
		}
	}
	for _, toolUse := range out.agentOutputTools {
		h.handleAgentOutputToolUse(sessionID, toolUse)
	}
}

// writeAgentMessage writes a converted message to one connection: its
// agent_tool_use events, then the response. payload is the pre-marshalled
// response, shared by all connections; it is nil when the response carries
// per-connection attribution and must be marshalled for this connection.
func (h *AgentHandler) writeAgentMessage(c *fiberws.Conn, out *agentMessageOutput, payload []byte) error {
	for _, ev := range out.toolUseEvents {
		if err := h.safeWriteJSON(c, ev); err != nil {
			logging.Debug("Failed to send agent_tool_use event: %v", err)
		}
	}

	if payload == nil {
		response := out.response // shallow copy; only attribution differs
		if out.attributeFromConn {
			if user := h.GetConnectionUser(c); user != nil {
				// Fallback: the username is not known here, so use the user ID.
				response.UserID = user
				response.Username = user
			}
		}
		var err error
		if payload, err = json.Marshal(response); err != nil {
			return fmt.Errorf("marshal agent message: %w", err)
		}
	}

	logging.Debug("Sending agent message: role=%s, type=%s, id=%s, session=%s, bytes=%d",
		out.response.Role, out.response.Type, out.response.ID, out.response.SessionID, len(payload))

	if err := h.safeWriteRaw(c, payload); err != nil {
		logging.Debug("Failed to send agent message %s: %v", out.response.ID, err)
		return err
	}
	return nil
}

// sendFiberAgentMessage sends a Claude message to a single WebSocket client (Fiber version).
// senderUserID provides attribution for user messages from other clients.
func (h *AgentHandler) sendFiberAgentMessage(c *fiberws.Conn, sessionID uuid.UUID, msg types.Message, senderUserID ...string) error {
	out := h.buildAgentMessage(sessionID, msg, senderUserID...)
	if out == nil {
		return nil
	}
	h.emitAgentMessageSideEffects(sessionID, out)
	return h.writeAgentMessage(c, out, nil)
}

// sendSessionUpdate sends a session state update to the WebSocket client
// This ensures the frontend always has the latest session status (idle, processing, error, etc.)
func (h *AgentHandler) sendSessionUpdate(c *fiberws.Conn, sessionID uuid.UUID, session *AgentSession) error {
	update := SessionUpdatedMessage{
		BaseMessage: BaseMessage{Type: MessageTypeSessionUpdated},
		SessionID:   sessionID,
	}

	// Include git branch if available
	if session.GitBranch != "" {
		update.GitBranch = &session.GitBranch
	}

	// Session copy with denormalized avatar data for iOS
	sessionCopy := h.sessionForClients(session)

	// Add full session object for frontend to update its state
	// This is critical for showing accurate status (idle vs processing)
	updateWithSession := map[string]interface{}{
		"type":       string(MessageTypeSessionUpdated),
		"session_id": sessionID.String(),
		"status":     string(session.Status),
		"session":    sessionCopy, // Include session with avatar data
	}

	if session.GitBranch != "" {
		updateWithSession["git_branch"] = session.GitBranch
	}

	logging.Debug("📤 Sending session_updated: sessionID=%s, status=%s", sessionID, session.Status)

	if err := h.safeWriteJSON(c, updateWithSession); err != nil {
		logging.Error("Failed to send session_updated message: %v", err)
		return err
	}

	return nil
}

// sessionForClients returns a copy of the session with its avatar's data
// filled in, as clients expect it in session updates.
func (h *AgentHandler) sessionForClients(session *AgentSession) Session {
	sessionCopy := session.Session
	if session.SelectedAvatarID == nil || h.SessionManager == nil || h.SessionManager.repo == nil {
		return sessionCopy
	}
	dbAvatar, err := h.SessionManager.repo.GetAvatarByID(*session.SelectedAvatarID)
	if err != nil || dbAvatar == nil {
		return sessionCopy
	}
	sessionCopy.SelectedAvatar = &Avatar{
		ID:        dbAvatar.ID,
		ThemeID:   dbAvatar.ThemeID,
		Name:      dbAvatar.Name,
		Type:      string(dbAvatar.Type),
		ImagePath: dbAvatar.ImagePath,
		ImageURL:  dbAvatar.ImageURL,
		Style:     dbAvatar.Style,
		Seed:      dbAvatar.Seed,
		Color:     dbAvatar.Color,
		CreatedAt: dbAvatar.CreatedAt,
	}
	// Also set the ID field for compatibility
	sessionCopy.SelectedAvatarID = &dbAvatar.ID
	return sessionCopy
}

// broadcastSessionStateUpdate is called by SessionManager when session state changes
// It broadcasts the update to all WebSocket clients connected to that session
func (h *AgentHandler) broadcastSessionStateUpdate(session *AgentSession) {
	// A session started by another session is also shown in that session's
	// chat, whose connections are not registered for it.
	h.announceLinkedSession(session)

	h.sessionConnectionsMu.RLock()
	connections := h.sessionConnections[session.ID]
	h.sessionConnectionsMu.RUnlock()

	if len(connections) == 0 {
		logging.Warning("⏳ No connections for session update %s (status=%s), buffering", session.ID, session.Status)
		// Buffer the session state update so it can be replayed on reconnection
		updateWithSession := map[string]interface{}{
			"type":       string(MessageTypeSessionUpdated),
			"session_id": session.ID.String(),
			"status":     string(session.Status),
			"session":    session.Session,
		}
		if session.GitBranch != "" {
			updateWithSession["git_branch"] = session.GitBranch
		}
		h.bufferRawMessage(session.ID, updateWithSession)
		return
	}

	logging.Debug("📢 Broadcasting session update to %d connection(s): session=%s, status=%s",
		len(connections), session.ID, session.Status)

	// Broadcast to all connections for this session
	for _, conn := range connections {
		// Use goroutine to avoid blocking if a connection is slow/dead
		go func(c *fiberws.Conn) {
			if err := h.sendSessionUpdate(c, session.ID, session); err != nil {
				logging.Error("Failed to broadcast session update to connection: %v", err)
			}
		}(conn)
	}
}

// broadcastToSession broadcasts a message to all WebSocket connections for a session except the sender
// This ensures all clients (multiple tabs, reconnected clients, etc.) receive real-time updates
// while excluding the sender to prevent message echo
// senderUserID is optional and used to attribute user messages to the correct sender (UUID)
// senderUsername is optional and used to display the actual username in the UI
// Usage: broadcastToSession(sessionID, msg, conn) or broadcastToSession(sessionID, msg, conn, uuid, username)
//
// The message is converted (and, when it has no per-connection fields,
// marshalled) once, so every connection receives the same message ID and
// bytes, and analytics side effects fire once per message.
func (h *AgentHandler) broadcastToSession(sessionID uuid.UUID, msg types.Message, senderConn *fiberws.Conn, senderUserID ...string) {
	h.sessionConnectionsMu.RLock()
	connections := append([]*fiberws.Conn(nil), h.sessionConnections[sessionID]...)
	h.sessionConnectionsMu.RUnlock()

	msgType := msg.GetMessageType()
	var senderUser string
	var senderUsername string
	if len(senderUserID) > 0 {
		senderUser = senderUserID[0] // UUID
	}
	if len(senderUserID) > 1 {
		senderUsername = senderUserID[1] // Actual username
	}

	if len(connections) == 0 {
		logging.Debug("No WebSocket connections, buffering message (type=%s) for session %s", msgType, sessionID)
		h.bufferSDKMessage(sessionID, msg, senderUserID...)
		return
	}

	// Note: We only skip the sender for their own USER message to prevent echo
	// The sender MUST receive: assistant messages, tool outputs, results, state updates
	// EXCEPTION: Don't skip image content - sender needs to see these
	skipSender := msgType == "user" && !userMessageHasImage(msg)

	targets := make([]*fiberws.Conn, 0, len(connections))
	for _, conn := range connections {
		if skipSender && conn == senderConn {
			continue
		}
		targets = append(targets, conn)
	}
	if len(targets) == 0 {
		return
	}

	out := h.buildAgentMessage(sessionID, msg, senderUser, senderUsername)
	if out == nil {
		return
	}
	h.emitAgentMessageSideEffects(sessionID, out)

	var payload []byte
	if !out.attributeFromConn {
		var err error
		if payload, err = json.Marshal(out.response); err != nil {
			logging.Error("Failed to marshal agent message for session %s: %v", sessionID, err)
			return
		}
	}

	logging.Debug("Broadcasting message (type=%s, id=%s) to %d/%d connection(s) for session %s",
		msgType, out.response.ID, len(targets), len(connections), sessionID)

	for _, conn := range targets {
		// Use goroutine to avoid blocking if a connection is slow/dead
		go func(c *fiberws.Conn) {
			if err := h.writeAgentMessage(c, out, payload); err != nil {
				// Remove dead connection to prevent silent message loss on subsequent broadcasts.
				// Without this, a degraded WebSocket stays in the map and silently drops all future messages.
				h.untrackSessionConnection(sessionID, c)
				logging.Warning("Removed dead connection for session %s after write failure: %v", sessionID, err)
			}
		}(conn)
	}
}

// userMessageHasImage reports whether a user message carries a tool result
// with image content.
func userMessageHasImage(msg types.Message) bool {
	userMsg, ok := msg.(*types.UserMessage)
	if !ok {
		return false
	}
	contentBlocks, ok := userMsg.Content.([]types.ContentBlock)
	if !ok {
		return false
	}
	for _, block := range contentBlocks {
		toolResultBlock, ok := block.(*types.ToolResultBlock)
		if !ok {
			continue
		}
		contentArr, ok := toolResultBlock.Content.([]interface{})
		if !ok {
			continue
		}
		for _, item := range contentArr {
			if blockMap, ok := item.(map[string]interface{}); ok && blockMap["type"] == "image" {
				return true
			}
		}
	}
	return false
}

// forwardPermissionRequests monitors the session's permission request channel
// and forwards requests to the WebSocket client. It runs until stop is closed
// (the session moved to another connection, or this one closed), the session
// ends, or a write fails; gen keeps its exit from clearing a newer forwarder.
func (h *AgentHandler) forwardPermissionRequests(c *fiberws.Conn, sessionID uuid.UUID, session *AgentSession, stop <-chan struct{}, gen uint64) {
	logging.Info("🚀 Permission forwarder started for session %s", sessionID)

	defer func() {
		session.permissionForwarderExited(gen)
		logging.Info("🛑 Permission forwarder stopped for session %s", sessionID)
	}()

	// Create a ticker to periodically check WebSocket connection state
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case permReq, ok := <-session.permissionReqChan:
			if !ok {
				logging.Info("Permission request channel closed for session %s", sessionID)
				return
			}

			// Check if WebSocket is still connected before forwarding
			if !session.IsWebSocketConnected() {
				logging.Warning("⚠️ WebSocket disconnected, denying permission request: %s", permReq.RequestID)
				select {
				case permReq.ResponseChan <- PermissionResponse{
					Approved:    false,
					DenyMessage: "WebSocket connection lost",
				}:
				default:
				}
				continue
			}

			logging.Debug("Permission request received: tool=%s, requestID=%s", permReq.ToolName, permReq.RequestID)

			// HANDLE AskUserQuestion: show each question to the user and hand the
			// answers back to the SDK through UpdatedInput (see ask_user_question.go)
			if permReq.ToolName == "AskUserQuestion" {
				h.handleAskUserQuestion(sessionID, session, permReq)
				continue
			}

			// Generate human-readable description
			description := formatPermissionDescription(permReq.ToolName, permReq.Input)

			// Send permission request to frontend
			response := PermissionRequestMessage{
				BaseMessage:  BaseMessage{Type: MessageTypePermissionRequest},
				SessionID:    sessionID,
				PermissionID: permReq.RequestID,
				Tool:         permReq.ToolName,
				Action:       "use_tool",
				Details:      permReq.Input,
				Description:  description,
				SessionName:  h.SessionManager.sessionDisplayName(&session.Session),
			}

			logging.Debug("📤 WS SENDING PERMISSION REQUEST TO FRONTEND: permissionID=%s, tool=%s, description=%s", permReq.RequestID, permReq.ToolName, description)

			if err := h.safeWriteJSON(c, response); err != nil {
				logging.Error("❌ Failed to send permission request to WebSocket: %v", err)

				// Mark session as disconnected
				session.SetWebSocketConnected(false)

				// Send error response back to callback
				select {
				case permReq.ResponseChan <- PermissionResponse{
					Approved:    false,
					DenyMessage: "Failed to send permission request to frontend (WebSocket error)",
				}:
				default:
				}

				// Clean up any other pending permissions
				session.CleanupPendingPermissions()
				return
			}

			logging.Info("✅ Permission request sent to WebSocket successfully: %s", permReq.RequestID)

		case <-stop:
			return

		case <-ticker.C:
			// Periodically check if WebSocket is still connected
			if !session.IsWebSocketConnected() {
				logging.Info("⏱️ WebSocket connection lost, stopping permission forwarder for session %s", sessionID)
				session.CleanupPendingPermissions()
				return
			}

		case <-session.ctx.Done():
			logging.Info("Session %s context cancelled, stopping permission request forwarding", sessionID)
			session.CleanupPendingPermissions()
			return
		}
	}
}

// handleTaskToolUse processes Task/Agent tool invocations to track subagents (both foreground and background)
func (h *AgentHandler) handleTaskToolUse(sessionID uuid.UUID, toolUse *types.ToolUseBlock) {
	if h.BackgroundAgentMgr == nil {
		return
	}

	// Extract task parameters
	input := toolUse.Input
	runInBackground, _ := input["run_in_background"].(bool)

	// Extract task details
	subagentType, _ := input["subagent_type"].(string)
	description, _ := input["description"].(string)

	// Use the tool use ID as the agent ID
	agentID := toolUse.ID

	if runInBackground {
		log.Printf("🤖 Background agent detected: id=%s, type=%s, desc=%s", agentID, subagentType, description)
	} else {
		log.Printf("🤖 Foreground agent detected: id=%s, type=%s, desc=%s", agentID, subagentType, description)
	}

	// Register the agent (both foreground and background are tracked for the output modal)
	prompt, _ := input["prompt"].(string)
	requestedModel, _ := input["model"].(string)
	h.BackgroundAgentMgr.RegisterLaunchedSubagent(agentID, sessionID, subagentType, description, "", runInBackground, prompt, requestedModel)
}

// handleAgentOutputToolUse processes AgentOutputTool invocations
func (h *AgentHandler) handleAgentOutputToolUse(sessionID uuid.UUID, toolUse *types.ToolUseBlock) {
	if h.BackgroundAgentMgr == nil {
		return
	}

	// Extract the agent ID being queried
	input := toolUse.Input
	agentID, _ := input["agentId"].(string)

	if agentID == "" {
		log.Printf("AgentOutputTool used but no agentId provided")
		return
	}

	// Check if we're tracking this agent
	agent, exists := h.BackgroundAgentMgr.GetAgent(agentID)
	if !exists {
		log.Printf("AgentOutputTool querying unknown agent: %s", agentID)
		return
	}

	log.Printf("📤 AgentOutputTool checking agent: id=%s, status=%s, progress=%.1f%%",
		agentID, agent.Status, agent.Progress*100)

	// If blocking mode, the output will come through tool results
	// We'll update progress when we see the tool result
}

// handleToolResult processes tool result blocks to update tracked subagent status
// When a foreground subagent completes, its result comes back as a ToolResultBlock
// with the tool_use_id matching the original Task/Agent tool invocation
func (h *AgentHandler) handleToolResult(sessionID uuid.UUID, toolResult *types.ToolResultBlock) {
	if h.BackgroundAgentMgr == nil {
		return
	}

	agentID := toolResult.ToolUseID
	agent, exists := h.BackgroundAgentMgr.GetAgent(agentID)
	if !exists {
		// Not a tracked agent tool result, skip
		return
	}

	// Only process if the agent is still running
	if agent.Status != BackgroundAgentStatusRunning {
		return
	}

	// Extract output content from the tool result
	output := toolResultText(toolResult.Content)

	log.Printf("🔍 handleToolResult: agentID=%s, hasOutput=%v, outputLen=%d", agentID, output != "", len(output))

	if toolResult.IsError != nil && *toolResult.IsError {
		log.Printf("❌ Subagent tool result (error): id=%s", agentID)
		h.BackgroundAgentMgr.FailAgent(agentID, output)
	} else {
		// Check if this is a background agent launch confirmation rather than actual completion.
		// When run_in_background=true, Claude Code immediately returns a result like:
		//   "Async agent launched successfully. agentId: <id>..."
		// The real work is still running asynchronously, so we should NOT mark it completed.
		if isBackgroundAgentLaunchResult(output) {
			log.Printf("🚀 Background agent launch detected (not completing): id=%s", agentID)
			// Keep the agent running. The launch text is CLI metadata, not
			// output; completion arrives later as a task notification.
			h.BackgroundAgentMgr.AddOutput(agentID, "Running in the background", false)
		} else {
			log.Printf("✅ Subagent tool result (success): id=%s", agentID)
			// Add the output so the modal can display it
			if output != "" {
				h.BackgroundAgentMgr.AddOutput(agentID, output, false)
			}
			h.BackgroundAgentMgr.CompleteAgent(agentID, output)
		}
	}
}

// broadcastToAllConnections sends a message to all WebSocket connections for a session
func (h *AgentHandler) broadcastToAllConnections(sessionID uuid.UUID, msg interface{}) {
	h.sessionConnectionsMu.RLock()
	connections := h.sessionConnections[sessionID]
	h.sessionConnectionsMu.RUnlock()

	logging.Debug("📡 broadcastToAllConnections: found %d connections for session %s", len(connections), sessionID)

	if len(connections) == 0 {
		logging.Warning("⏳ No WebSocket connections, buffering raw message for session %s", sessionID)
		h.bufferRawMessage(sessionID, msg)
		return
	}

	for i, conn := range connections {
		logging.Debug("📤 Sending to connection %d/%d", i+1, len(connections))
		if err := h.safeWriteJSON(conn, msg); err != nil {
			logging.Error("Failed to broadcast to connection: %v", err)
		}
	}
	logging.Debug("✅ Broadcast complete: sent to %d connection(s)", len(connections))
}

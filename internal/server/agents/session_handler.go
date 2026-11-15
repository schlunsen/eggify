package agents

import (
	"encoding/json"
	"fmt"
	"log"

	fiberws "github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// validateSessionOwnership checks if the requesting user owns the session.
// Returns nil if the user is the owner, or an error if ownership validation fails.
// SECURITY: Prevents unauthorized access to other users' sessions.
func (h *AgentHandler) validateSessionOwnership(sessionID uuid.UUID, username *string) error {
	if username == nil {
		// No authenticated user: auth is disabled, or the connection used the
		// API key. Those keep read access and ordinary actions. SECURITY:
		// privileged actions do not rely on this check; they call
		// RequireUserPrincipal first, which refuses API-key-only connections
		// whenever user auth is enabled.
		return nil
	}

	// Get session metadata from storage to check owner
	sessionMeta, err := h.SessionManager.Storage.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	// If session has no owner set, allow access (legacy sessions before ownership tracking)
	if sessionMeta.OwnerUserID == nil {
		return nil
	}

	// Check if the authenticated user matches the session owner
	// OwnerUserID can be either a username or a UUID, so check both
	if *sessionMeta.OwnerUserID == *username {
		return nil
	}

	// Also check if the owner is a UUID and the username maps to that UUID
	if id := h.userIDFor(*username); id != "" && *sessionMeta.OwnerUserID == id {
		return nil
	}

	logging.Warning("SECURITY: User %s attempted to access session %s owned by %s", *username, sessionID, *sessionMeta.OwnerUserID)
	return fmt.Errorf("access denied: you do not own this session")
}

func (h *AgentHandler) handleFiberCreateSession(c *fiberws.Conn, rawMsg map[string]interface{}, registerSession func(uuid.UUID)) error {
	var msg CreateSessionMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("invalid create_session message: %w", err)
	}
	if msg.Options.CodexNetworkAccess && h.sessionExists(msg.SessionID) {
		// Reopening: the stored choice wins, nothing is granted.
		msg.Options.CodexNetworkAccess = false
	}
	if msg.Options.CodexNetworkAccess {
		// Unlike legacy permission controls, never bypass this check when
		// authentication is disabled: an API key cannot establish user consent.
		if !h.requirePrincipal(c, CodexNetworkAction, func(user *string) bool {
			return principal.FromConnUser(user).IsAdmin()
		}) {
			return nil
		}
		msg.Options.codexNetworkAuthorized = true
	}

	// SECURITY: options that skip permission prompts (YOLO, bypass modes,
	// always-allow rules, loop verify commands, custom base URLs) need a
	// signed-in user; ordinary sessions can still be created with the API key.
	if action := PrivilegedSessionOptions(&msg.Options); action != "" {
		if !h.RequireUserPrincipal(c, action) {
			return nil
		}
	}
	// SECURITY: a question policy that answers for the user sooner than their
	// global policy takes the human out of the loop like YOLO: user only.
	if msg.Options.QuestionPolicy != nil {
		norm, err := NormalizeQuestionPolicy(*msg.Options.QuestionPolicy)
		if err != nil {
			h.sendFiberError(c, "invalid question_policy: "+err.Error())
			return nil
		}
		msg.Options.QuestionPolicy = &norm
	}
	if action := QuestionPolicyAutoAction(&msg.Options); action != "" {
		if !h.RequireUserPrincipal(c, action) {
			return nil
		}
	}
	// SECURITY: opting out of the sandbox while it is the default also needs a
	// user, or an agent could start an unsandboxed session to escape.
	if action := h.SessionManager.SandboxOptOutAction(&msg.Options); action != "" {
		if !h.RequireUserPrincipal(c, action) {
			return nil
		}
	}
	// SECURITY: the inbox is server-owned, and a loop link lets the new session
	// message its "parent". Only a signed-in user who owns that parent may set
	// it (Edit first on a loop card), under the same rules as start_loop_session.
	msg.Options.Inbox = nil
	// A worker's config is written by start_worker_session only.
	msg.Options.Worker = nil
	if parent := msg.Options.SpawnedBySessionID; parent != nil {
		if !h.RequireUserPrincipal(c, LinkLoopSessionAction) {
			return nil
		}
		if err := h.validateSessionOwnership(*parent, h.GetConnectionUser(c)); err != nil {
			return err
		}
		if err := h.SessionManager.checkLoopSpawnAllowed(*parent); err != nil {
			return fmt.Errorf("cannot link the new session to %s: %w", *parent, err)
		}
	}

	// SECURITY: create_session with the id of an existing session reopens it
	// (page reload). Only a caller who may access that session may do so, and
	// the existing owner is never changed: otherwise any connection could take
	// over another user's session by naming its id.
	authenticatedUser := h.GetConnectionUser(c)
	existed := h.sessionExists(msg.SessionID)
	if existed {
		if err := h.authorizeSession(c, msg.SessionID); err != nil {
			return err
		}
	}

	log.Printf("Creating session: %s", msg.SessionID)

	// Create session
	session, err := h.SessionManager.CreateSession(msg.SessionID, msg.Options)
	if err != nil {
		log.Printf("ERROR: Failed to create session: %v", err)
		return err
	}
	if !existed && session.Options.CodexNetworkAccess {
		logging.Info("Codex unrestricted network authorized for sandboxed session %s by %s", session.ID, principal.FromConnUser(authenticatedUser).Username)
	}

	// SECURITY: Set the owner of a NEW session to the authenticated user
	// This prevents impersonation and enables multi-user session tracking
	if authenticatedUser != nil && !existed {
		if err := h.SessionManager.SetSessionOwner(msg.SessionID, authenticatedUser); err != nil {
			log.Printf("WARNING: Failed to set session owner: %v", err)
			// Don't fail the session creation if owner setting fails
		} else {
			log.Printf("✅ Session %s owner set to: %s", msg.SessionID, *authenticatedUser)
		}
	}

	// Register session with this WebSocket connection
	registerSession(msg.SessionID)

	log.Printf("Session created successfully: %s", session.ID)

	// Send session created response
	response := SessionCreatedMessage{
		BaseMessage: BaseMessage{Type: MessageTypeSessionCreated},
		SessionID:   session.ID,
		Session:     *session,
		Status:      "created",
	}

	// DEBUG: Log project_id and working directory
	projectIDStr := "nil"
	if session.ProjectID != nil {
		projectIDStr = *session.ProjectID
	}
	workingDir := "nil"
	if session.Options.WorkingDirectory != nil {
		workingDir = *session.Options.WorkingDirectory
	}
	log.Printf("📋 Sending session_created: ID=%s, ProjectID=%s, WorkingDir=%s", session.ID, projectIDStr, workingDir)

	if err := h.safeWriteJSON(c, response); err != nil {
		log.Printf("ERROR: Failed to send session_created response: %v", err)
		return err
	}

	log.Printf("session_created response sent successfully")
	return nil
}

// handleFiberSendPrompt sends a prompt to an agent session (Fiber version)
// Note: This returns a response channel that must be monitored by the main handler
func (h *AgentHandler) handleFiberSendPrompt(c *fiberws.Conn, rawMsg map[string]interface{}, registerSession func(uuid.UUID)) error {
	var msg SendPromptMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("invalid send_prompt message: %w", err)
	}

	// Check if we have content array (new format) or prompt string (legacy format)
	hasContent := len(msg.Content) > 0
	hasPrompt := msg.Prompt != ""

	if !hasContent && !hasPrompt {
		return fmt.Errorf("either prompt or content must be provided")
	}

	// No turns while Eggify is not activated (code license_required).
	if err := checkLicense(); err != nil {
		h.sendLicenseRequired(c, err)
		return nil
	}

	// SECURITY: Get authenticated user from WebSocket connection context
	// This prevents clients from spoofing other users' identities
	authenticatedUser := h.GetConnectionUser(c)

	// SECURITY: Validate session ownership before allowing prompt send
	if err := h.validateSessionOwnership(msg.SessionID, authenticatedUser); err != nil {
		h.sendFiberError(c, err.Error())
		return err
	}

	// Get the session first (to validate it exists)
	_, err := h.SessionManager.GetSession(msg.SessionID)
	if err != nil {
		return err
	}

	// Register session with this WebSocket connection (handles reconnections)
	// NOTE: registerSession now automatically starts the permission forwarder
	// if it's not already running, so no need to start it explicitly here
	registerSession(msg.SessionID)

	// Send prompt or content to session
	if hasContent {
		// New format: structured content with images
		log.Printf("Sending structured content to session %s (%d blocks) from user %v", msg.SessionID, len(msg.Content), authenticatedUser)
		if err := h.SessionManager.SendPromptWithContent(msg.SessionID, msg.Content, authenticatedUser); err != nil {
			return err
		}
	} else {
		// Legacy format: plain text prompt
		log.Printf("Sending prompt to session %s from user %v: %s", msg.SessionID, authenticatedUser, msg.Prompt)
		if err := h.SessionManager.SendPrompt(msg.SessionID, msg.Prompt, authenticatedUser); err != nil {
			return err
		}
	}

	// CRITICAL FIX: Broadcast the user message to all connections in this session
	// This ensures that when User A sends a message, User B (viewing the same session)
	// can see it in real-time instead of having to reload
	logging.Info("📢 Creating and broadcasting user message to all session connections for session %s", msg.SessionID)

	// Create user message for broadcasting to other clients
	var userMessageContent interface{}
	if hasContent {
		userMessageContent = msg.Content
	} else {
		// Create text block for plain text prompts
		userMessageContent = []types.ContentBlock{
			&types.TextBlock{Text: msg.Prompt},
		}
	}

	userMsg := &types.UserMessage{
		Type:    "user", // CRITICAL: Must set Type field for GetMessageType() to work
		Content: userMessageContent,
	}

	// Broadcast to all connections EXCEPT the sender (to prevent echo)
	// This goes to all other clients viewing this session
	// Pass the authenticated user UUID so User B sees "User A sent this message" not "User B sent this"
	var userUUID string
	var username string
	if authenticatedUser != nil {
		username = *authenticatedUser // Default to the provided username
		// Look up the user's UUID from the database
		repo := database.NewRepository(h.db)
		dbUser, err := repo.GetUser(*authenticatedUser)
		if err == nil && dbUser != nil && dbUser.ID.Valid {
			userUUID = dbUser.ID.String
			logging.Info("📤 User UUID for %s: %s", *authenticatedUser, userUUID)
		} else {
			logging.Warning("⚠️ Failed to lookup UUID for user %s, using username as fallback", *authenticatedUser)
			userUUID = *authenticatedUser
		}
		h.broadcastToSession(msg.SessionID, userMsg, c, userUUID, username)
	} else {
		h.broadcastToSession(msg.SessionID, userMsg, c)
	}

	// Get response channel
	responseChan, err := h.SessionManager.GetResponseChannel(msg.SessionID)
	if err != nil {
		return err
	}

	// Stream responses back to client in a goroutine
	// This allows the handler to process subsequent prompts
	go h.streamFiberResponses(c, msg.SessionID, responseChan)

	return nil
}

// sendFiberAgentMessage sends a Claude message to the WebSocket client (Fiber version)
// senderUserID is optional and used for message attribution when broadcasting user messages from other clients
func (h *AgentHandler) handleFiberEndSession(c *fiberws.Conn, rawMsg map[string]interface{}) error {
	var msg EndSessionMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("invalid end_session message: %w", err)
	}

	// SECURITY: Validate session ownership before allowing end
	authenticatedUser := h.GetConnectionUser(c)
	if err := h.validateSessionOwnership(msg.SessionID, authenticatedUser); err != nil {
		h.sendFiberError(c, err.Error())
		return err
	}

	// End session
	if err := h.SessionManager.EndSession(msg.SessionID); err != nil {
		return err
	}

	// Clean up message buffer for this session
	h.cleanupSessionBuffer(msg.SessionID)

	// Send session ended response
	response := SessionDeletedMessage{
		BaseMessage: BaseMessage{Type: MessageTypeSessionEnded},
		SessionID:   msg.SessionID,
		Status:      string(SessionStatusEnded),
	}
	if err := h.safeWriteJSON(c, response); err != nil {
		return err
	}

	// Send final session state update (now ended) so frontend updates immediately
	// Note: Session is no longer in SessionManager.sessions, so fetch from DB if needed
	// For now, we'll rely on the session_ended message to signal completion
	return nil
}

// handleFiberInterruptSession interrupts an agent session (Fiber version)
func (h *AgentHandler) handleFiberInterruptSession(c *fiberws.Conn, rawMsg map[string]interface{}) error {
	var msg InterruptSessionMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("invalid interrupt_session message: %w", err)
	}

	// SECURITY: Validate session ownership before allowing interrupt
	authenticatedUser := h.GetConnectionUser(c)
	if err := h.validateSessionOwnership(msg.SessionID, authenticatedUser); err != nil {
		h.sendFiberError(c, err.Error())
		return err
	}

	logging.Info("Interrupting session: %s", msg.SessionID)

	// Interrupt session (cancels context but keeps session alive)
	if err := h.SessionManager.InterruptSession(msg.SessionID); err != nil {
		logging.Error("Failed to interrupt session %s: %v", msg.SessionID, err)
		h.sendFiberError(c, fmt.Sprintf("failed to interrupt session: %v", err))
		return err
	}

	// Send session interrupted response
	response := SessionInterruptedMessage{
		BaseMessage: BaseMessage{Type: MessageTypeSessionInterrupted},
		SessionID:   msg.SessionID,
		Status:      "interrupted",
	}
	if err := h.safeWriteJSON(c, response); err != nil {
		return err
	}

	// Send session state update so frontend shows correct "idle" status immediately
	if session, err := h.SessionManager.GetSession(msg.SessionID); err == nil {
		h.sendSessionUpdate(c, msg.SessionID, session)
	}

	return nil
}

// handleFiberStopLoop stops a running autonomous loop without ending the session.
// The agent's current turn finishes normally, but no further iterations are queued.
func (h *AgentHandler) handleFiberStopLoop(c *fiberws.Conn, rawMsg map[string]interface{}) error {
	var msg InterruptSessionMessage // reuses {type, session_id} shape
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("invalid stop_loop message: %w", err)
	}

	// SECURITY: Validate session ownership before allowing loop control
	authenticatedUser := h.GetConnectionUser(c)
	if err := h.validateSessionOwnership(msg.SessionID, authenticatedUser); err != nil {
		h.sendFiberError(c, err.Error())
		return err
	}

	logging.Info("Stopping loop for session: %s", msg.SessionID)
	if err := h.SessionManager.StopLoop(msg.SessionID); err != nil {
		h.sendFiberError(c, fmt.Sprintf("failed to stop loop: %v", err))
		return err
	}

	return nil
}

// handleFiberDeleteSession deletes an agent session (Fiber version)
func (h *AgentHandler) handleFiberDeleteSession(c *fiberws.Conn, rawMsg map[string]interface{}) error {
	var msg DeleteSessionMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("invalid delete_session message: %w", err)
	}

	// SECURITY: Validate session ownership before allowing delete
	authenticatedUser := h.GetConnectionUser(c)
	if err := h.validateSessionOwnership(msg.SessionID, authenticatedUser); err != nil {
		h.sendFiberError(c, err.Error())
		return err
	}

	// Delete session from database
	if err := h.SessionManager.DeleteSession(msg.SessionID); err != nil {
		h.sendFiberError(c, fmt.Sprintf("failed to delete session: %v", err))
		return fmt.Errorf("failed to delete session: %w", err)
	}

	// Send session deleted response
	response := SessionDeletedMessage{
		BaseMessage: BaseMessage{Type: MessageTypeSessionDeleted},
		SessionID:   msg.SessionID,
		Status:      "deleted",
	}
	return h.safeWriteJSON(c, response)
}

// handleFiberListSessions lists all sessions from database (Fiber version)
func (h *AgentHandler) handleFiberListSessions(c *fiberws.Conn, registerSession func(uuid.UUID)) error {
	log.Printf("handleFiberListSessions: Fetching all sessions from database")
	sessions, err := h.SessionManager.ListAllSessions("all")
	if err != nil {
		log.Printf("ERROR: Failed to list sessions: %v", err)
		h.sendFiberError(c, fmt.Sprintf("failed to list sessions: %v", err))
		return fmt.Errorf("failed to list sessions: %w", err)
	}

	// SECURITY: a signed-in user only gets (and is only registered for) the
	// sessions they may see.
	viewer := h.viewerFor(h.GetConnectionUser(c))
	visible := sessions[:0]
	for _, session := range sessions {
		if viewer.canSee(session.OwnerUserID) {
			visible = append(visible, session)
		}
	}
	sessions = visible

	log.Printf("handleFiberListSessions: Found %d visible sessions in database", len(sessions))
	for i, session := range sessions {
		log.Printf("  Session %d: ID=%s, Status=%s, Created=%s", i+1, session.ID, session.Status, session.CreatedAt)

		// Register active sessions with this WebSocket connection
		// This allows reconnection after page reload
		if session.Status == SessionStatusActive || session.Status == SessionStatusIdle || session.Status == SessionStatusProcessing {
			registerSession(session.ID)
			logging.Debug("Registered active session %s with reconnected WebSocket", session.ID)
		}
	}

	// Convert to lightweight sessions for iOS compatibility (1MB message limit)
	lightSessions := make([]LightSession, 0, len(sessions))
	repo := h.SessionManager.GetRepo()
	defaultQuestionPolicy := h.SessionManager.QuestionPolicyDefault()
	for _, session := range sessions {
		ls := LightSession{
			ID:               session.ID,
			CreatedAt:        session.CreatedAt,
			UpdatedAt:        session.UpdatedAt,
			Status:           session.Status,
			MessageCount:     session.MessageCount,
			CostUSD:          session.CostUSD,
			ModelName:        session.ModelName,
			Provider:         session.Provider,
			GitBranch:        session.GitBranch,
			ProjectID:        session.ProjectID,
			SelectedAvatarID: session.SelectedAvatarID,
			Options:          redactedSessionOptions(session.Options), // working_directory etc. for project subscription; no session API key
		}
		ls.QuestionPolicy, ls.QuestionPolicyEffective = listedQuestionPolicy(session.Options.QuestionPolicy, defaultQuestionPolicy)

		// Denormalize avatar data so iOS doesn't need a separate API call
		if session.SelectedAvatarID != nil && repo != nil {
			if dbAvatar, err := repo.GetAvatarByID(*session.SelectedAvatarID); err == nil && dbAvatar != nil {
				ls.SelectedAvatar = &Avatar{
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
			}
		}

		lightSessions = append(lightSessions, ls)
	}

	h.addLightSubagentCounts(lightSessions)

	response := SessionsListMessage{
		BaseMessage: BaseMessage{Type: MessageTypeSessionsList},
		Sessions:    lightSessions,
	}

	// Log response size for monitoring (1MB limit on iOS)
	jsonBytes, _ := json.Marshal(response)
	log.Printf("handleFiberListSessions: Sending response with %d sessions (size: %d bytes)", len(lightSessions), len(jsonBytes))
	if len(jsonBytes) > 900000 { // Warn if approaching 1MB limit
		log.Printf("⚠️ WARNING: Response size is approaching iOS 1MB limit: %d bytes", len(jsonBytes))
	}

	return h.safeWriteJSON(c, response)
}

// handleFiberLoadMessages loads messages for a session with pagination (Fiber version)
func (h *AgentHandler) handleFiberLoadMessages(c *fiberws.Conn, rawMsg map[string]interface{}, registerSession func(uuid.UUID)) error {
	// Parse session ID
	sessionIDStr, ok := rawMsg["session_id"].(string)
	if !ok {
		h.sendFiberError(c, "missing or invalid session_id")
		return fmt.Errorf("missing or invalid session_id")
	}

	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		h.sendFiberError(c, "invalid session ID format")
		return fmt.Errorf("invalid session ID format")
	}

	// SECURITY: Validate session ownership before allowing message loading
	authenticatedUser := h.GetConnectionUser(c)
	if err := h.validateSessionOwnership(sessionID, authenticatedUser); err != nil {
		h.sendFiberError(c, err.Error())
		return err
	}

	// Register session with WebSocket connection (starts git watcher if applicable)
	registerSession(sessionID)

	// Parse pagination params
	limit := 50
	beforeSequence := 0

	if limitVal, ok := rawMsg["limit"].(float64); ok {
		limit = int(limitVal)
	}
	if bsVal, ok := rawMsg["before_sequence"].(float64); ok {
		beforeSequence = int(bsVal)
	}

	// Validate
	if limit < 1 || limit > 1000 {
		limit = 50
	}

	// Get total message count for the badge display
	totalCount, _ := h.SessionManager.Storage.GetMessageCount(sessionID)

	// Get latest messages (or older messages if before_sequence is set)
	messagesPtr, hasMore, err := h.SessionManager.Storage.GetLatestMessages(sessionID, limit, beforeSequence)
	if err != nil {
		h.sendFiberError(c, fmt.Sprintf("failed to load messages: %v", err))
		return fmt.Errorf("failed to load messages: %w", err)
	}

	// Convert []*MessageRecord to []MessageRecord
	messages := make([]MessageRecord, 0, len(messagesPtr))
	for _, msgPtr := range messagesPtr {
		messages = append(messages, *msgPtr)
	}

	// Send response
	response := MessagesLoadedMessage{
		BaseMessage: BaseMessage{Type: MessageTypeMessagesLoaded},
		SessionID:   sessionID,
		Messages:    messages,
		HasMore:     hasMore,
		TotalCount:  totalCount,
		Count:       len(messages),
		Limit:       limit,
		Offset:      beforeSequence, // Keep for backward compat
	}

	return h.safeWriteJSON(c, response)
}

// handleFiberSubscribeSession registers the WebSocket connection to receive real-time updates for a session
func (h *AgentHandler) handleFiberSubscribeSession(c *fiberws.Conn, rawMsg map[string]interface{}, registerSession func(uuid.UUID)) error {
	// Parse session ID
	sessionIDStr, ok := rawMsg["session_id"].(string)
	if !ok {
		h.sendFiberError(c, "missing or invalid session_id")
		return fmt.Errorf("missing or invalid session_id")
	}

	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		h.sendFiberError(c, "invalid session ID format")
		return fmt.Errorf("invalid session ID format: %w", err)
	}

	// SECURITY: Validate session ownership before allowing subscription
	authenticatedUser := h.GetConnectionUser(c)
	if err := h.validateSessionOwnership(sessionID, authenticatedUser); err != nil {
		h.sendFiberError(c, err.Error())
		return err
	}

	logging.Info("📥 handleFiberSubscribeSession: Incoming subscribe request for session %s", sessionID)

	// Check if session exists in SessionManager (active in-memory session)
	session, err := h.SessionManager.GetSession(sessionID)
	if err != nil {
		// Session not in memory - check if it exists in database
		logging.Info("Session %s not in memory, checking database", sessionID)

		// Verify session exists in database by trying to get its message count
		// If this succeeds, the session exists (even if not active)
		_, _, dbErr := h.SessionManager.GetMessages(sessionID, 1, 0)
		if dbErr != nil {
			h.sendFiberError(c, fmt.Sprintf("session not found: %s", sessionID))
			return fmt.Errorf("session not found in memory or database: %w", dbErr)
		}

		// Session exists in database but not in memory
		// Register the connection anyway - if the session becomes active later, it will receive updates
		logging.Debug("Subscribing WebSocket to database session: %s (not currently active)", sessionID)
	} else {
		// Session is active in memory
		logging.Debug("Subscribing WebSocket to active session: %s (status: %s)", sessionID, session.Status)
	}

	// Register this WebSocket connection with the session
	// This works for both active and database-only sessions
	registerSession(sessionID)

	// Check total connections for debugging
	h.sessionConnectionsMu.RLock()
	totalConns := len(h.sessionConnections[sessionID])
	h.sessionConnectionsMu.RUnlock()
	logging.Info("🔗 Session %s now has %d active connection(s)", sessionID, totalConns)

	// Send confirmation
	statusText := "subscribed"
	messageText := fmt.Sprintf("Successfully subscribed to session %s. You will now receive real-time updates.", sessionID)

	if err != nil {
		// Database-only session
		statusText = "subscribed_database"
		messageText = fmt.Sprintf("Successfully subscribed to session %s. This session is not currently active, but you will receive updates if it becomes active.", sessionID)
	}

	response := SessionSubscribedMessage{
		BaseMessage: BaseMessage{Type: MessageTypeSessionSubscribed},
		SessionID:   sessionID,
		Status:      statusText,
		Message:     messageText,
	}

	if err := h.safeWriteJSON(c, response); err != nil {
		return err
	}

	// CRITICAL: Send current session status immediately after subscription
	// This ensures the client gets the current state, not just future updates
	// For active sessions: send the actual status (idle, processing, error, etc.)
	// For database-only sessions: send idle status as default
	if session != nil {
		// Active session - send current status
		logging.Info("📤 📤 📤 Sending current session status on subscribe: sessionID=%s, status=%s", sessionID, session.Status)
		if err := h.sendSessionUpdate(c, sessionID, session); err != nil {
			logging.Error("Failed to send initial session status: %v", err)
			// Don't return error - subscription already confirmed, best effort to send status
		}

		// Tell the client which questions still wait for an answer, so it drops
		// any that stopped waiting while it was away, then re-send those
		// questions so the modal comes back for them (and only for them).
		h.sendQuestionsSync(c, sessionID, session.pendingQuestionMessages())

		// Where the loop is, for clients that opened the chat mid-loop.
		if ev := session.LastLoopEvent(); ev != nil {
			_ = h.safeWriteJSON(c, ev)
		}
	} else {
		// A session that is not live cannot be waiting on a question.
		h.sendQuestionsSync(c, sessionID, nil)

		// Database-only session - fetch actual status from DB instead of hardcoding idle
		// This ensures restart resilience: if the session was processing before restart,
		// we send the real persisted status (not a misleading "idle")
		dbStatus := string(SessionStatusIdle) // default fallback
		if h.SessionManager.Storage != nil {
			if dbSession, dbErr := h.SessionManager.Storage.GetSession(sessionID); dbErr == nil {
				dbStatus = dbSession.Status
				// IMPORTANT: If DB says "processing" but session is not in memory,
				// the process is dead (app restarted or session was evicted).
				// Normalize to "idle" so the frontend doesn't show a false "processing" state.
				if dbStatus == string(SessionStatusProcessing) {
					dbStatus = string(SessionStatusIdle)
					logging.Info("📤 📤 📤 Session %s was 'processing' in DB but not in memory (stale) - normalizing to idle", sessionID)
				} else {
					logging.Info("📤 📤 📤 Sending persisted status '%s' for database-only session: %s", dbStatus, sessionID)
				}
			} else {
				logging.Info("📤 📤 📤 Could not fetch DB status for session %s, defaulting to idle: %v", sessionID, dbErr)
			}
		} else {
			logging.Info("📤 📤 📤 No storage available, sending default idle status for session: %s", sessionID)
		}

		statusUpdate := map[string]interface{}{
			"type":       string(MessageTypeSessionUpdated),
			"session_id": sessionID.String(),
			"status":     dbStatus,
		}
		if err := h.safeWriteJSON(c, statusUpdate); err != nil {
			logging.Error("Failed to send session status: %v", err)
			// Don't return error - subscription already confirmed
		}
	}

	// Send existing background agents for this session
	// First check in-memory, then fall back to database for restart resilience
	if h.BackgroundAgentMgr != nil {
		agents := h.SubagentsForSession(sessionID)
		if len(agents) > 0 {
			agentListMsg := BackgroundAgentsListMessage{
				BaseMessage: BaseMessage{Type: MessageTypeBackgroundAgentsList},
				SessionID:   sessionID,
				Agents:      agents,
			}
			if err := h.safeWriteJSON(c, agentListMsg); err != nil {
				logging.Error("Failed to send background agents list: %v", err)
			} else {
				logging.Info("📦 Sent %d background agents on subscribe for session %s", len(agents), sessionID)
			}
		}
	}

	return nil
}

// handleFiberListBackgroundAgents handles requests for background agents list
func (h *AgentHandler) handleFiberListBackgroundAgents(c *fiberws.Conn, rawMsg map[string]interface{}) error {
	sessionIDStr, ok := rawMsg["session_id"].(string)
	if !ok {
		h.sendFiberError(c, "missing or invalid session_id")
		return fmt.Errorf("missing or invalid session_id")
	}

	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		h.sendFiberError(c, "invalid session ID format")
		return fmt.Errorf("invalid session ID format: %w", err)
	}

	// SECURITY: Validate session ownership before listing background agents
	authenticatedUser := h.GetConnectionUser(c)
	if err := h.validateSessionOwnership(sessionID, authenticatedUser); err != nil {
		h.sendFiberError(c, err.Error())
		return err
	}

	agents := h.SubagentsForSession(sessionID)

	response := BackgroundAgentsListMessage{
		BaseMessage: BaseMessage{Type: MessageTypeBackgroundAgentsList},
		SessionID:   sessionID,
		Agents:      agents,
	}

	return h.safeWriteJSON(c, response)
}

// handleFiberKillAllAgents kills all active agent sessions (Fiber version)
// SECURITY: This is a destructive operation that affects multiple sessions.
// Only authenticated users can invoke this. When auth is enabled, it only kills
// sessions owned by the requesting user (or all sessions if user is admin).
func (h *AgentHandler) handleFiberKillAllAgents(c *fiberws.Conn, rawMsg map[string]interface{}) error {
	// Parse message to extract optional project_id
	var msg KillAllAgentsMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("failed to parse kill_all_agents message: %w", err)
	}

	// SECURITY: admins (and, with user auth off, anyone) end every session;
	// other users only their own. An API-key connection is refused when user
	// auth is on.
	filter, ok := h.bulkSessionFilter(c, "Ending all sessions")
	if !ok {
		return nil
	}
	authenticatedUser := h.GetConnectionUser(c)
	if authenticatedUser != nil {
		logging.Info("SECURITY: User %s requested kill_all_agents (project_id: %v, all users: %v)", *authenticatedUser, msg.ProjectID, filter == nil)
	}

	count := h.SessionManager.EndAllSessionsWhere(msg.ProjectID, filter)

	projectText := ""
	if msg.ProjectID != nil && *msg.ProjectID != "" {
		projectText = fmt.Sprintf(" for project %s", *msg.ProjectID)
	}

	response := map[string]interface{}{
		"type":    "kill_all_agents_response",
		"count":   count,
		"message": fmt.Sprintf("Killed %d agent sessions%s", count, projectText),
	}
	return h.safeWriteJSON(c, response)
}

// handleFiberDeleteAllSessions deletes all sessions from database (Fiber version)
// SECURITY: This is a destructive operation that permanently removes session data.
func (h *AgentHandler) handleFiberDeleteAllSessions(c *fiberws.Conn, rawMsg map[string]interface{}) error {
	// Parse message to extract optional project_id
	var msg DeleteAllSessionsMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("failed to parse delete_all_sessions message: %w", err)
	}

	// SECURITY: same scope as kill_all_agents.
	filter, ok := h.bulkSessionFilter(c, "Deleting all sessions")
	if !ok {
		return nil
	}
	authenticatedUser := h.GetConnectionUser(c)
	if authenticatedUser != nil {
		logging.Info("SECURITY: User %s requested delete_all_sessions (project_id: %v, all users: %v)", *authenticatedUser, msg.ProjectID, filter == nil)
	}

	count, err := h.SessionManager.DeleteAllSessionsWhere(msg.ProjectID, filter)
	if err != nil {
		h.sendFiberError(c, fmt.Sprintf("failed to delete all sessions: %v", err))
		return fmt.Errorf("failed to delete all sessions: %w", err)
	}

	response := AllSessionsDeletedMessage{
		BaseMessage: BaseMessage{Type: MessageTypeAllSessionsDeleted},
		Count:       count,
		ProjectID:   msg.ProjectID, // Include project_id for frontend filtering
	}
	return h.safeWriteJSON(c, response)
}

// handleFiberChangeSessionModel switches a live session to another provider/model.
// Rejected while the session is processing a prompt.
func (h *AgentHandler) handleFiberChangeSessionModel(c *fiberws.Conn, rawMsg map[string]interface{}) error {
	var msg ChangeSessionModelMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("invalid change_session_model message: %w", err)
	}

	// SECURITY: a custom base URL decides where prompts (and keys) are sent.
	if ModelSwitchSetsBaseURL(msg.BaseURL) && !h.RequireUserPrincipal(c, "Setting a custom base URL") {
		return nil
	}

	// SECURITY: only the session owner may change which provider runs it
	// (the provider's API key from the providers table is used for the session).
	authenticatedUser := h.GetConnectionUser(c)
	if err := h.validateSessionOwnership(msg.SessionID, authenticatedUser); err != nil {
		h.sendFiberError(c, err.Error())
		return err
	}

	logging.Info("🔀 Model switch request: session=%s, provider=%s, model=%s", msg.SessionID, msg.Provider, msg.Model)

	response, err := h.SessionManager.ChangeSessionModel(msg.SessionID, msg.Provider, msg.Model, msg.BaseURL)
	if err != nil {
		logging.Error("❌ Failed to switch model: %v", err)
		h.sendFiberError(c, fmt.Sprintf("Failed to switch model: %v", err))
		return err
	}

	return h.safeWriteJSON(c, response)
}

// sendQuestionsSync sends user_questions_sync with the ids of the questions the
// session waits on, followed by each of those questions.
func (h *AgentHandler) sendQuestionsSync(c *fiberws.Conn, sessionID uuid.UUID, pending []*UserQuestionMessage) {
	ids := make([]string, 0, len(pending))
	for _, q := range pending {
		ids = append(ids, q.QuestionID)
	}
	if err := h.safeWriteJSON(c, map[string]interface{}{
		"type":         string(MessageTypeUserQuestionsSync),
		"session_id":   sessionID.String(),
		"question_ids": ids,
	}); err != nil {
		logging.Error("Failed to send question sync: %v", err)
		return
	}
	for _, q := range pending {
		logging.Info("📋 Session %s has pending question, re-sending to frontend: %s", sessionID, q.QuestionID)
		if err := h.safeWriteJSON(c, q); err != nil {
			logging.Error("Failed to send pending question: %v", err)
		}
	}
}

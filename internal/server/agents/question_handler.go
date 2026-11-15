package agents

import (
	"errors"
	"sort"
	"time"

	fiberws "github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// handleFiberUserQuestionResponse handles user_question_response messages from the WebSocket client
func (h *AgentHandler) handleFiberUserQuestionResponse(c *fiberws.Conn, data map[string]interface{}) error {
	// Parse session ID
	sessionIDStr, ok := data["session_id"].(string)
	if !ok {
		return ErrInvalidMessage
	}

	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		return ErrInvalidMessage
	}

	// Parse question ID
	questionID, ok := data["question_id"].(string)
	if !ok {
		return ErrInvalidMessage
	}

	// Parse answers
	answersRaw, ok := data["answers"].([]interface{})
	if !ok {
		logging.Error("Invalid answers format in user_question_response")
		return ErrInvalidMessage
	}

	var answers []string
	for _, answerRaw := range answersRaw {
		if answer, ok := answerRaw.(string); ok {
			answers = append(answers, answer)
		}
	}

	// SECURITY: only someone who may access the session answers its questions.
	if err := h.authorizeSession(c, sessionID); err != nil {
		return nil
	}

	logging.Info("📬 Received user question response: session=%s, question_id=%s, answers=%v",
		sessionID, questionID, answers)

	ack := func() {
		// Acknowledge to the frontend *before* delivering the answer. Delivering
		// first would let the permission forwarder broadcast the next question of
		// a multi-question AskUserQuestion call ahead of this ack, and the
		// frontend would then close the freshly opened modal when the stale ack
		// arrived.
		ackMsg := map[string]interface{}{
			"type":        string(MessageTypeUserQuestionAcknowledged),
			"session_id":  sessionID.String(),
			"question_id": questionID,
		}
		if err := h.safeWriteJSON(c, ackMsg); err != nil {
			logging.Error("Failed to send question acknowledgment: %v", err)
		}
	}
	err = h.answerUserQuestion(sessionID, questionID, answers, h.GetConnectionUser(c), ack)
	if errors.Is(err, ErrConfirmationNeedsUser) {
		h.sendUserRequiredError(c, err)
		return nil
	}
	return err
}

// sendUserRequiredError tells a connection that it may not answer or dismiss
// an approval card: "user_required" for an API-key connection (the dashboard
// shows its sign-in toast), "admin_required" for a signed-in non-admin.
func (h *AgentHandler) sendUserRequiredError(c *fiberws.Conn, err error) {
	code := principal.ErrorCode
	if p := principal.FromConnUser(h.GetConnectionUser(c)); p.IsUser() {
		code = principal.AdminErrorCode
	}
	_ = h.safeWriteJSON(c, map[string]interface{}{
		"type":    "error",
		"code":    code,
		"message": err.Error(),
	})
}

// answerUserQuestion delivers an answer to a waiting question. username is
// the connection's signed-in user (nil for API-key connections): a
// confirmation question only accepts answers from a user, and otherwise
// keeps waiting. beforeDeliver (optional) runs just before the answer is
// handed to the waiting call.
func (h *AgentHandler) answerUserQuestion(sessionID uuid.UUID, questionID string, answers []string, username *string, beforeDeliver func()) error {
	session, err := h.SessionManager.GetSession(sessionID)
	if err != nil {
		logging.Error("Session not found for question response: %v", err)
		return err
	}

	session.questionMu.Lock()
	responseChan, exists := session.pendingQuestions[questionID]
	session.questionMu.Unlock()

	if !exists {
		// Nothing waits on this question any more (the turn ended, the server
		// restarted, or another tab answered it). Say so rather than ack it, so
		// the client doesn't record an answer the agent never received.
		logging.Warning("No pending question found for question_id=%s", questionID)
		h.broadcastQuestionResolved(sessionID, questionID, questionExpired)
		return nil
	}

	if session.questionRequiresUser(questionID) && !principal.AllowedConnUser(username) {
		logging.Warning("🛑 Answer to approval request %s (session %s) rejected: the connection is not a signed-in user", questionID, sessionID)
		return ErrConfirmationNeedsUser
	}

	if beforeDeliver != nil {
		beforeDeliver()
	}

	// Send the answer to the waiting callback
	// NOTE: The permission callback (forwardPermissionRequests) handles cleanup of pendingQuestions
	// after receiving the answer, so we don't delete here to avoid race conditions.
	logging.Info("✅ Sending question answer back to agent for question_id=%s", questionID)
	select {
	case responseChan <- UserQuestionAnswerResponse{
		Answers: answers,
	}:
		logging.Info("✅ Answer successfully sent to SDK callback for question_id=%s", questionID)
	case <-time.After(3 * time.Second):
		logging.Error("❌ Timeout sending answer to SDK callback for question_id=%s", questionID)
	}

	return nil
}

// handleFiberUserQuestionDismiss handles user_question_dismiss: the user
// dismissed a question without answering. A question the agent is waiting on
// is denied with a note that it is no longer relevant; a stale one is simply
// cleared from every client.
func (h *AgentHandler) handleFiberUserQuestionDismiss(c *fiberws.Conn, data map[string]interface{}) error {
	sessionIDStr, _ := data["session_id"].(string)
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		return ErrInvalidMessage
	}
	questionID, _ := data["question_id"].(string)
	if questionID == "" {
		return ErrInvalidMessage
	}
	// SECURITY: only someone who may access the session dismisses its questions.
	if err := h.authorizeSession(c, sessionID); err != nil {
		return nil
	}
	if err := h.dismissUserQuestion(sessionID, questionID, h.GetConnectionUser(c)); err != nil {
		if errors.Is(err, ErrConfirmationNeedsUser) {
			h.sendUserRequiredError(c, err)
		} else {
			h.sendFiberError(c, err.Error())
		}
	}
	return nil
}

// handleFiberUserQuestionHold handles user_question_hold ("Keep waiting"): the
// question's auto-answer timer stops and it waits for the user. Any connection
// that may access the session may ask: it only ever makes the session wait
// for a human.
func (h *AgentHandler) handleFiberUserQuestionHold(c *fiberws.Conn, data map[string]interface{}) error {
	sessionIDStr, _ := data["session_id"].(string)
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		return ErrInvalidMessage
	}
	questionID, _ := data["question_id"].(string)
	if questionID == "" {
		return ErrInvalidMessage
	}
	if err := h.authorizeSession(c, sessionID); err != nil {
		return nil
	}
	if !h.SessionManager.HoldQuestion(sessionID, questionID) {
		logging.Debug("user_question_hold: no waiting question %s in session %s", questionID, sessionID)
	}
	return nil
}

// dismissUserQuestion dismisses a question. The waiting AskUserQuestion call
// broadcasts user_question_resolved itself; for a question nothing waits on,
// it is broadcast here. A confirmation question can only be dismissed by a
// signed-in user (username non-nil); otherwise it keeps waiting.
func (h *AgentHandler) dismissUserQuestion(sessionID uuid.UUID, questionID string, username *string) error {
	session, err := h.SessionManager.GetSession(sessionID)
	if err != nil {
		// The session is no longer live, so nothing can be waiting.
		h.broadcastQuestionResolved(sessionID, questionID, questionExpired)
		return nil
	}
	session.questionMu.Lock()
	answerChan, exists := session.pendingQuestions[questionID]
	session.questionMu.Unlock()
	if !exists {
		h.broadcastQuestionResolved(sessionID, questionID, questionExpired)
		return nil
	}
	if session.questionRequiresUser(questionID) && !principal.AllowedConnUser(username) {
		logging.Warning("🛑 Dismissal of approval request %s (session %s) rejected: the connection is not a signed-in user", questionID, sessionID)
		return ErrConfirmationNeedsUser
	}
	select {
	case answerChan <- UserQuestionAnswerResponse{Dismissed: true}:
		logging.Info("🚫 Question %s dismissed by the user", questionID)
	default:
		// An answer is already on its way; that one wins.
	}
	return nil
}

// pendingQuestionMessages returns the questions the session is waiting on,
// oldest first.
func (session *AgentSession) pendingQuestionMessages() []*UserQuestionMessage {
	session.questionMu.Lock()
	defer session.questionMu.Unlock()
	out := make([]*UserQuestionMessage, 0, len(session.pendingQuestionData))
	for _, q := range session.pendingQuestionData {
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out
}

// StoreQuestionResponse stores a question response channel for later retrieval
func (session *AgentSession) StoreQuestionResponse(questionID string, responseChan chan UserQuestionAnswerResponse) {
	session.questionMu.Lock()
	defer session.questionMu.Unlock()
	session.pendingQuestions[questionID] = responseChan
}

// StopQuestionForwarder marks the question forwarder as stopped
func (session *AgentSession) StopQuestionForwarder() {
	session.questionForwarderMu.Lock()
	defer session.questionForwarderMu.Unlock()
	session.questionForwarderRunning = false
}

// IsQuestionForwarderRunning checks if the question forwarder is running
func (session *AgentSession) IsQuestionForwarderRunning() bool {
	session.questionForwarderMu.Lock()
	defer session.questionForwarderMu.Unlock()
	return session.questionForwarderRunning
}

// SetQuestionForwarderRunning sets the question forwarder running state
func (session *AgentSession) SetQuestionForwarderRunning(running bool) {
	session.questionForwarderMu.Lock()
	defer session.questionForwarderMu.Unlock()
	session.questionForwarderRunning = running
}

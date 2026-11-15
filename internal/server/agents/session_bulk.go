package agents

import (
	"encoding/json"
	"fmt"

	fiberws "github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/logging"
)

// maxBulkSessions caps one end_sessions / delete_sessions message.
const maxBulkSessions = 500

// BulkSessionsMessage is end_sessions / delete_sessions: the sessions the user
// picked in the sidebar's select mode. One message instead of one per session,
// which matters over a slow remote connection.
type BulkSessionsMessage struct {
	BaseMessage
	SessionIDs []uuid.UUID `json:"session_ids"`
}

// BulkSessionFailure is a session the bulk action left alone, and why.
type BulkSessionFailure struct {
	SessionID uuid.UUID `json:"session_id"`
	Error     string    `json:"error"`
}

// SessionsBulkResultMessage answers end_sessions / delete_sessions.
type SessionsBulkResultMessage struct {
	BaseMessage
	Action     string               `json:"action"` // "end" | "delete"
	SessionIDs []uuid.UUID          `json:"session_ids"`
	Failed     []BulkSessionFailure `json:"failed"`
}

// handleFiberBulkSessions ends or deletes several sessions. Each one gets the
// same ownership check as end_session / delete_session; a refused or failed
// session is reported in `failed` and the others still go ahead.
func (h *AgentHandler) handleFiberBulkSessions(c *fiberws.Conn, msgType MessageType, rawMsg map[string]interface{}) error {
	var msg BulkSessionsMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		h.sendFiberError(c, fmt.Sprintf("invalid %s message", msgType))
		return fmt.Errorf("invalid %s message: %w", msgType, err)
	}
	if len(msg.SessionIDs) > maxBulkSessions {
		h.sendFiberError(c, fmt.Sprintf("at most %d sessions at once", maxBulkSessions))
		return nil
	}

	action := "delete"
	if msgType == MessageTypeEndSessions {
		action = "end"
	}
	result := SessionsBulkResultMessage{
		BaseMessage: BaseMessage{Type: MessageTypeSessionsBulkResult},
		Action:      action,
		SessionIDs:  []uuid.UUID{},
		Failed:      []BulkSessionFailure{},
	}

	authenticatedUser := h.GetConnectionUser(c)
	seen := make(map[uuid.UUID]bool, len(msg.SessionIDs))
	for _, id := range msg.SessionIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if err := h.validateSessionOwnership(id, authenticatedUser); err != nil {
			result.Failed = append(result.Failed, BulkSessionFailure{SessionID: id, Error: err.Error()})
			continue
		}
		var err error
		if action == "end" {
			err = h.SessionManager.EndSessionKeepingOrder(id)
		} else {
			err = h.SessionManager.DeleteSession(id)
		}
		if err != nil {
			result.Failed = append(result.Failed, BulkSessionFailure{SessionID: id, Error: err.Error()})
			continue
		}
		h.cleanupSessionBuffer(id)
		result.SessionIDs = append(result.SessionIDs, id)
	}

	logging.Info("Bulk %s: %d sessions done, %d refused or failed", action, len(result.SessionIDs), len(result.Failed))
	return h.safeWriteJSON(c, result)
}

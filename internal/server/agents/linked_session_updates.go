package agents

import (
	"encoding/json"

	fiberws "github.com/gofiber/websocket/v2"
	"github.com/google/uuid"

	"github.com/schlunsen/eggify/internal/logging"
)

// A session started by another session (a worker, an agent-started loop) is
// shown in the chat of the session that started it: its chip in the team
// strip, its row under its parent, what it is doing right now. The parent's
// page is not registered for the child (that would route the child's
// messages and approval cards to it), so the child's session updates and a
// worker's activity are sent to the parent's connections as their own
// message types. They are live only: never buffered for a later connection,
// which reads the session list and GET …/workers instead.

// linkedSessionEventCallback sends msg to the live connections of the
// session parent whose user may see a session owned by owner.
type linkedSessionEventCallback func(parent uuid.UUID, owner *string, msg interface{})

// SetLinkedSessionCallback registers the sender of a child session's events
// to the session that started it.
func (sm *SessionManager) SetLinkedSessionCallback(callback linkedSessionEventCallback) {
	sm.linkedSessionCallbackMu.Lock()
	defer sm.linkedSessionCallbackMu.Unlock()
	sm.linkedSessionCallback = callback
}

// sendToParentSession hands msg to the linked-session callback, if any.
func (sm *SessionManager) sendToParentSession(parent uuid.UUID, owner *string, msg interface{}) {
	sm.linkedSessionCallbackMu.RLock()
	callback := sm.linkedSessionCallback
	sm.linkedSessionCallbackMu.RUnlock()
	if callback != nil {
		callback(parent, owner, msg)
	}
}

// sendToParentConnections sends msg to the connections registered for the
// session parent, except those also registered for skipChild (uuid.Nil: none
// skipped) and those whose user may not see a session owned by owner. With
// no such connection the message is dropped.
func (h *AgentHandler) sendToParentConnections(parent, skipChild uuid.UUID, owner *string, msg interface{}) {
	h.sessionConnectionsMu.RLock()
	var targets []*fiberws.Conn
	for _, conn := range h.sessionConnections[parent] {
		if skipChild != uuid.Nil && containsConn(h.sessionConnections[skipChild], conn) {
			continue
		}
		targets = append(targets, conn)
	}
	h.sessionConnectionsMu.RUnlock()
	if len(targets) == 0 {
		return
	}

	data, err := json.Marshal(msg)
	if err != nil {
		logging.Warning("Linked session event for %s not sent: %v", parent, err)
		return
	}
	for _, conn := range targets {
		// The child has its starter's owner, but a connection registered for
		// the parent before it had one must not learn about the child.
		if !h.viewerFor(h.GetConnectionUser(conn)).canSee(owner) {
			continue
		}
		go func(c *fiberws.Conn) {
			if err := h.safeWriteRaw(c, data); err != nil {
				logging.Debug("Linked session event for %s not delivered: %v", parent, err)
			}
		}(conn)
	}
}

func containsConn(conns []*fiberws.Conn, conn *fiberws.Conn) bool {
	for _, c := range conns {
		if c == conn {
			return true
		}
	}
	return false
}

// announceLinkedSession tells the connections of the session that started
// this one about its state. Connections registered for the session itself
// get session_updated and are skipped.
func (h *AgentHandler) announceLinkedSession(session *AgentSession) {
	parent := session.Options.SpawnedBySessionID
	if parent == nil || *parent == session.ID {
		return
	}
	update := map[string]interface{}{
		"type":              string(MessageTypeLinkedSessionUpdated),
		"session_id":        parent.String(),
		"linked_session_id": session.ID.String(),
		"status":            string(session.Status),
		"session":           h.sessionForClients(session),
	}
	if session.GitBranch != "" {
		update["git_branch"] = session.GitBranch
	}
	h.sendToParentConnections(*parent, session.ID, session.OwnerUserID, update)
}

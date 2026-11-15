package agents

import (
	"encoding/json"
	"fmt"
	"time"

	fiberws "github.com/gofiber/websocket/v2"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/principal"
)

func (h *AgentHandler) handleFiberCreateHandover(c *fiberws.Conn, rawMsg map[string]interface{}) error {
	var msg CreateHandoverMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("invalid create_handover message: %w", err)
	}

	// SECURITY: a handover exports the session's transcript and context; only
	// someone who may access the session can create one.
	if err := h.authorizeSession(c, msg.SessionID); err != nil {
		return nil
	}

	logging.Info("📦 Creating handover for session %s", msg.SessionID)

	// Create handover via SessionManager
	// Carrying permission settings over is a signed-in user's choice only.
	isUser := principal.AllowedConnUser(h.GetConnectionUser(c))
	response, err := h.SessionManager.CreateHandoverFor(isUser, msg.SessionID, msg.Options)
	if err != nil {
		logging.Error("❌ Failed to create handover: %v", err)
		h.sendFiberError(c, fmt.Sprintf("Failed to create handover: %v", err))
		return err
	}

	logging.Info("✅ Handover created: token=%s, expires=%s", safePrefix(response.HandoverToken, 16)+"...", response.ExpiresAt.Format(time.RFC3339))

	// Send response to frontend
	return h.safeWriteJSON(c, response)
}

// handleFiberAcceptHandover accepts and applies a handover
func (h *AgentHandler) handleFiberAcceptHandover(c *fiberws.Conn, rawMsg map[string]interface{}) error {
	var msg AcceptHandoverMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("invalid accept_handover message: %w", err)
	}

	// SECURITY: accepting a handover imports another session's context and
	// permission settings (YOLO, always-allow rules) into a session.
	if !h.RequireUserPrincipal(c, "Accepting a handover") {
		return nil
	}

	// SECURITY: applying a handover to an existing session rewrites it, so the
	// caller must be allowed to access that session.
	if msg.UseExistingSession != nil {
		if err := h.authorizeSession(c, *msg.UseExistingSession); err != nil {
			return nil
		}
	}

	logging.Info("📥 Accepting handover: token=%s, auto_start=%v", safePrefix(msg.HandoverToken, 16)+"...", msg.AutoStart)

	// Apply handover via SessionManager
	// Only a signed-in user may auto-start the new session or give it
	// permission bypass settings.
	response, err := h.SessionManager.AcceptHandoverFor(
		principal.AllowedConnUser(h.GetConnectionUser(c)),
		msg.HandoverToken,
		msg.NewSessionConfig,
		msg.UseExistingSession,
		msg.AutoStart,
		msg.InitialPrompt,
		msg.KeepPermissions,
	)
	if err != nil {
		logging.Error("❌ Failed to accept handover: %v", err)
		h.sendFiberError(c, fmt.Sprintf("Failed to accept handover: %v", err))
		return err
	}

	logging.Info("✅ Handover accepted: session=%s, messages_imported=%d", response.SessionID, response.MessagesImported)

	// A new session belongs to the user who accepted the handover.
	if user := h.GetConnectionUser(c); user != nil && msg.UseExistingSession == nil {
		if err := h.SessionManager.SetSessionOwner(response.SessionID, user); err != nil {
			logging.Warning("Failed to set the owner of handover session %s: %v", response.SessionID, err)
		}
	}

	// Register the connection to this session if it's a new session
	h.sessionConnectionsMu.Lock()
	sessionConns, exists := h.sessionConnections[response.SessionID]
	if !exists {
		sessionConns = []*fiberws.Conn{}
	}
	sessionConns = append(sessionConns, c)
	h.sessionConnections[response.SessionID] = sessionConns
	h.sessionConnectionsMu.Unlock()

	// Send response to frontend
	return h.safeWriteJSON(c, response)
}

// handleFiberSubscribeProject subscribes a session to receive git status updates for a project

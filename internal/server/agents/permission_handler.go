package agents

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	fiberws "github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/logging"
)

func (h *AgentHandler) handleFiberPermissionResponse(c *fiberws.Conn, rawMsg map[string]interface{}) error {
	logging.Info("📥 RAW PERMISSION RESPONSE from frontend: %+v", rawMsg)

	var msg PermissionResponseMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("invalid permission_response message: %w", err)
	}

	// SECURITY: answering a permission request is the human-in-the-loop step,
	// so it needs a signed-in user, not just the API key an agent can read.
	// It is a user-level action (non-admin users must be able to work in
	// their own sessions), but a non-admin may only answer in a session they
	// own explicitly (see requireOwnSessionUnlessAdmin).
	if !h.RequireSignedInPrincipal(c, "Answering a permission request") {
		return nil
	}
	if !h.requireOwnSessionUnlessAdmin(c, msg.SessionID, "Answering a permission request in another user's session") {
		return nil
	}

	// SECURITY: Validate session ownership before allowing permission approval/denial
	// This prevents unauthorized users from approving dangerous operations on other users' sessions
	authenticatedUser := h.GetConnectionUser(c)
	if err := h.validateSessionOwnership(msg.SessionID, authenticatedUser); err != nil {
		h.sendFiberError(c, err.Error())
		return err
	}

	logging.Info("📥 PARSED permission response: sessionID=%s, permissionID='%s', approved=%v",
		msg.SessionID, msg.PermissionID, msg.Approved)

	// SECURITY: the response must name the request it answers. There is no
	// "answer whatever is pending" fallback: a response is bound to the one
	// request the user actually saw.
	if msg.PermissionID == "" {
		h.sendFiberError(c, "permission_response requires permission_id")
		return nil
	}

	// Get the session
	session, err := h.SessionManager.GetSession(msg.SessionID)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	// Find the pending permission request of this session by its ID.
	session.permMu.Lock()
	responseChan, exists := session.pendingPermissions[msg.PermissionID]
	session.permMu.Unlock()

	if !exists {
		logging.Warning("No pending permission request found for session %s (permission_id='%s')", msg.SessionID, msg.PermissionID)
		return fmt.Errorf("no pending permission request found for ID: %s", msg.PermissionID)
	}

	logging.Info("✅ Found pending permission, sending response to callback")

	// Send response to the callback
	response := PermissionResponse{
		Approved:    msg.Approved,
		DenyMessage: "User denied permission",
	}

	select {
	case responseChan <- response:
		log.Printf("Permission response delivered to callback: %s", msg.PermissionID)
	case <-time.After(5 * time.Second):
		log.Printf("ERROR: Timeout delivering permission response to callback")
		return fmt.Errorf("timeout delivering permission response")
	}

	// Send acknowledgement to frontend
	ack := BaseMessage{Type: MessageTypePermissionAcknowledged}
	return h.safeWriteJSON(c, ack)
}

func (h *AgentHandler) handleFiberAddAlwaysAllowRule(c *fiberws.Conn, rawMsg map[string]interface{}) error {
	var msg AddAlwaysAllowRuleMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("invalid add_always_allow_rule message: %w", err)
	}

	// SECURITY: an always-allow rule auto-approves future tool calls.
	if !h.RequireUserPrincipal(c, "Adding an always-allow rule") {
		return nil
	}

	// SECURITY: Validate session ownership before allowing rule addition
	// This prevents unauthorized users from adding always-allow rules to other users' sessions
	authenticatedUser := h.GetConnectionUser(c)
	if err := h.validateSessionOwnership(msg.SessionID, authenticatedUser); err != nil {
		h.sendFiberError(c, err.Error())
		return err
	}

	logging.Info("Adding always-allow rule to session %s: %s (mode: %s)", msg.SessionID, msg.Rule.Description, msg.Rule.MatchMode)

	// Get session to find working directory
	session, err := h.SessionManager.GetSession(msg.SessionID)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	// SECURITY: refuse rules that would cover more than the user can see
	// (Bash(*), Bash(:*), Read(**), /**, ~/**, eggify-tools shell/write tools, ...).
	// The user did approve the request in front of them, so let that one call
	// through, but store nothing.
	if err := ValidateAlwaysAllowRule(msg.Rule); err != nil {
		logging.Warning("❌ BLOCKED always-allow rule for session %s: %v", msg.SessionID, err)
		if msg.PermissionID != "" {
			h.approvePendingPermissionOnce(session, msg.PermissionID)
		}
		return fmt.Errorf("always-allow rule not saved: %w (this request was approved once)", err)
	}

	// The rule is stored in the session options (DB) only and enforced by
	// Eggify's permission callback (MatchesRule). It is not copied into the
	// folder's .claude/settings.local.json: that file is repo config the
	// workspace trust check fingerprints, and anything an agent can also write
	// there must not be able to pass for a rule the user granted.

	// Generate ID and timestamp for response
	if msg.Rule.ID == "" {
		msg.Rule.ID = uuid.New().String()
	}
	msg.Rule.CreatedAt = time.Now()

	// Add rule to in-memory session for immediate effect
	h.SessionManager.mu.Lock()
	session.Options.AlwaysAllowRules = append(session.Options.AlwaysAllowRules, msg.Rule)
	totalRules := len(session.Options.AlwaysAllowRules)
	session.UpdatedAt = time.Now()
	h.SessionManager.mu.Unlock()

	logging.Info("✅ Rule added to session in-memory cache (total rules: %d)", totalRules)
	logging.Info("   Rule details: tool=%s, mode=%s, pattern=%v", msg.Rule.Tool, msg.Rule.MatchMode, msg.Rule.Pattern)

	// Persist updated session to database to preserve all options (including working_directory)
	if err := h.SessionManager.updateSessionInDB(&session.Session); err != nil {
		logging.Error("Failed to update session in database: %v", err)
		// Don't fail the request - the in-memory session is updated
	} else {
		logging.Info("💾 Session persisted to database with updated rules")
	}

	// IMPORTANT: If there's a pending permission request (from the UI that triggered this),
	// we need to approve it FIRST, let Claude process it, THEN reload to pick up the new rule
	if msg.PermissionID != "" {
		logging.Info("📤 Approving pending permission request: %s", msg.PermissionID)

		// Find the pending permission request
		session.permMu.Lock()
		responseChan, exists := session.pendingPermissions[msg.PermissionID]
		session.permMu.Unlock()

		if exists {
			// Send simple approval first (without the rule update)
			// This lets Claude continue with the current request
			select {
			case responseChan <- PermissionResponse{
				Approved:    true,
				DenyMessage: "",
			}:
				logging.Info("✅ Permission approved - Claude will continue processing")
				// Clean up the pending permission immediately after approval
				session.permMu.Lock()
				delete(session.pendingPermissions, msg.PermissionID)
				session.permMu.Unlock()

				// Set flag to reload after next message
				// This ensures Claude completes the current action before we reload
				session.pendingReloadMu.Lock()
				session.pendingReload = true
				session.pendingReloadMu.Unlock()
				logging.Info("📋 Marked session for reload after next message")

			case <-time.After(3 * time.Second):
				logging.Warning("⚠️ Timeout sending permission approval to SDK")
			}
		} else {
			logging.Warning("⚠️ No pending permission found for ID: %s", msg.PermissionID)
			// No pending permission, so just reload the session immediately
			logging.Info("🔄 Reloading session settings to apply new always-allow rule")
			if err := h.SessionManager.ReloadSessionSettings(msg.SessionID); err != nil {
				logging.Error("Failed to reload session settings: %v", err)
			} else {
				go func() {
					time.Sleep(200 * time.Millisecond)
					if err := h.SessionManager.SendPromptSilent(msg.SessionID, "continue"); err != nil {
						logging.Error("Failed to auto-continue session: %v", err)
					}
				}()
			}
		}
	} else {
		// No permission ID provided - just reload the session immediately
		logging.Info("🔄 Reloading session settings to apply new always-allow rule")
		if err := h.SessionManager.ReloadSessionSettings(msg.SessionID); err != nil {
			logging.Error("Failed to reload session settings: %v", err)
		} else {
			go func() {
				time.Sleep(200 * time.Millisecond)
				if err := h.SessionManager.SendPromptSilent(msg.SessionID, "continue"); err != nil {
					logging.Error("Failed to auto-continue session: %v", err)
				}
			}()
		}
	}

	// Send confirmation with the full rule (including generated ID)
	response := AlwaysAllowRulesListMessage{
		BaseMessage: BaseMessage{Type: MessageTypeAlwaysAllowRulesList},
		SessionID:   msg.SessionID,
		Rules:       session.Options.AlwaysAllowRules,
	}

	return h.safeWriteJSON(c, response)
}

// legacySettingsPermissions are the settings.local.json forms older versions
// of Eggify wrote for a rule, so removing the rule also removes its legacy copy.
func legacySettingsPermissions(rule AlwaysAllowRule) []string {
	if rule.MatchMode == RuleMatchExact {
		return []string{FormatExactPermissionString(rule.Tool, rule.Parameters), FormatPermissionString(rule.Tool, rule.Pattern)}
	}
	return []string{FormatPermissionString(rule.Tool, rule.Pattern)}
}

// approvePendingPermissionOnce approves a pending permission request without
// storing a rule.
func (h *AgentHandler) approvePendingPermissionOnce(session *AgentSession, permissionID string) {
	session.permMu.Lock()
	responseChan, exists := session.pendingPermissions[permissionID]
	session.permMu.Unlock()
	if !exists {
		return
	}
	select {
	case responseChan <- PermissionResponse{Approved: true}:
		session.permMu.Lock()
		delete(session.pendingPermissions, permissionID)
		session.permMu.Unlock()
	case <-time.After(3 * time.Second):
		logging.Warning("⚠️ Timeout approving permission %s", permissionID)
	}
}

func (h *AgentHandler) handleFiberRemoveAlwaysAllowRule(c *fiberws.Conn, rawMsg map[string]interface{}) error {
	var msg RemoveAlwaysAllowRuleMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("invalid remove_always_allow_rule message: %w", err)
	}

	if !h.RequireUserPrincipal(c, "Removing an always-allow rule") {
		return nil
	}

	// SECURITY: Validate session ownership before allowing rule removal
	authenticatedUser := h.GetConnectionUser(c)
	if err := h.validateSessionOwnership(msg.SessionID, authenticatedUser); err != nil {
		h.sendFiberError(c, err.Error())
		return err
	}

	logging.Info("Removing always-allow rule %s from session %s", msg.RuleID, msg.SessionID)

	// Get session
	session, err := h.SessionManager.GetSession(msg.SessionID)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	// Get working directory for this session
	workingDir := "."
	if session.Options.WorkingDirectory != nil {
		workingDir = *session.Options.WorkingDirectory
	}

	// Create settings manager
	settingsManager := NewClaudeSettingsManager(workingDir)

	// Find the rule to remove and format its permission string
	h.SessionManager.mu.Lock()
	var ruleToRemove *AlwaysAllowRule
	newRules := []AlwaysAllowRule{}
	for _, rule := range session.Options.AlwaysAllowRules {
		if rule.ID != msg.RuleID {
			newRules = append(newRules, rule)
		} else {
			ruleToRemove = &rule
		}
	}
	session.Options.AlwaysAllowRules = newRules
	session.UpdatedAt = time.Now()
	h.SessionManager.mu.Unlock()

	// Older versions also copied rules into settings.local.json; remove such a
	// legacy copy. This is Eggify's own edit on the user's behalf, so a folder
	// that was trusted with its current config stays trusted.
	if ruleToRemove != nil {
		legacy := legacySettingsPermissions(*ruleToRemove)
		err := h.SessionManager.TrustChecker().WithOwnWrite(workingDir, func() error {
			perms, err := settingsManager.GetAllowedPermissions()
			if err != nil {
				return nil
			}
			for _, permissionStr := range legacy {
				if !containsString(perms, permissionStr) {
					continue // no legacy copy: leave the file untouched
				}
				logging.Info("🗑️ Removing legacy permission from settings.local.json: %s", permissionStr)
				if err := settingsManager.RemovePermission(permissionStr); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			logging.Error("Failed to remove permission from settings: %v", err)
		}
	}

	// Persist updated session to database to preserve all options (including working_directory)
	if err := h.SessionManager.updateSessionInDB(&session.Session); err != nil {
		logging.Error("Failed to update session in database: %v", err)
		// Don't fail the request - the in-memory session is updated
	} else {
		logging.Info("💾 Session persisted to database after removing rule")
	}

	// Send updated rules list
	response := AlwaysAllowRulesListMessage{
		BaseMessage: BaseMessage{Type: MessageTypeAlwaysAllowRulesList},
		SessionID:   msg.SessionID,
		Rules:       session.Options.AlwaysAllowRules,
	}

	return h.safeWriteJSON(c, response)
}

func (h *AgentHandler) handleFiberListAlwaysAllowRules(c *fiberws.Conn, rawMsg map[string]interface{}) error {
	var msg ListAlwaysAllowRulesMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("invalid list_always_allow_rules message: %w", err)
	}

	// SECURITY: Validate session ownership before allowing rule listing
	authenticatedUser := h.GetConnectionUser(c)
	if err := h.validateSessionOwnership(msg.SessionID, authenticatedUser); err != nil {
		h.sendFiberError(c, err.Error())
		return err
	}

	// Get session
	session, err := h.SessionManager.GetSession(msg.SessionID)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	// Send rules list
	response := AlwaysAllowRulesListMessage{
		BaseMessage: BaseMessage{Type: MessageTypeAlwaysAllowRulesList},
		SessionID:   msg.SessionID,
		Rules:       session.Options.AlwaysAllowRules,
	}

	return h.safeWriteJSON(c, response)
}

func (h *AgentHandler) handleFiberToggleYOLOMode(c *fiberws.Conn, rawMsg map[string]interface{}) error {
	var msg ToggleYOLOModeMessage
	msgBytes, _ := json.Marshal(rawMsg)
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return fmt.Errorf("invalid toggle_yolo_mode message: %w", err)
	}

	// SECURITY: YOLO mode disables every permission check.
	if !h.RequireUserPrincipal(c, "Toggling YOLO mode") {
		return nil
	}

	// SECURITY: Validate session ownership before allowing YOLO mode toggle
	// This prevents unauthorized users from disabling all permission checks on other users' sessions
	authenticatedUser := h.GetConnectionUser(c)
	if err := h.validateSessionOwnership(msg.SessionID, authenticatedUser); err != nil {
		h.sendFiberError(c, err.Error())
		return err
	}

	logging.Info("🎯 YOLO Mode toggle request: session=%s, enabled=%v", msg.SessionID, msg.Enabled)

	// Create new options with YOLO flags
	newOptions := SessionOptions{
		DangerouslySkipPermissions:      &msg.Enabled,
		AllowDangerouslySkipPermissions: &msg.Enabled,
	}

	// Reload session with new options (this interrupts and restarts with --resume)
	if err := h.SessionManager.ReloadSessionWithOptions(msg.SessionID, newOptions); err != nil {
		logging.Error("❌ Failed to toggle YOLO mode: %v", err)
		h.sendFiberError(c, fmt.Sprintf("Failed to toggle YOLO mode: %v", err))
		return err
	}

	logging.Info("✅ YOLO Mode toggled successfully")

	// Every open view (the sidebar switch, iOS) reads YOLO from the session's
	// options, so send them the new state.
	if session, err := h.SessionManager.GetSession(msg.SessionID); err == nil {
		h.broadcastSessionStateUpdate(session)
	}

	// Send confirmation to frontend
	statusText := "enabled"
	if !msg.Enabled {
		statusText = "disabled"
	}

	response := YOLOModeToggledMessage{
		BaseMessage: BaseMessage{Type: MessageTypeYOLOModeToggled},
		SessionID:   msg.SessionID,
		Enabled:     msg.Enabled,
		Message:     fmt.Sprintf("YOLO Mode %s - session reloaded with new permissions", statusText),
	}

	return h.safeWriteJSON(c, response)
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

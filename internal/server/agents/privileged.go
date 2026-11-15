package agents

import (
	"strings"

	fiberws "github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// Privileged WebSocket actions.
//
// An agent runs as the same OS user as Eggify and can read the API key, open
// /agent/ws and authenticate with it. Whatever that connection can do, a
// prompt-injected agent can do. So the actions that lift the human out of the
// loop (approving a permission, YOLO mode, always-allow rules, bypass
// permission modes, accepting a handover, pointing a session at another base
// URL) need a USER principal: the connection must have been authenticated
// with a signed-in user's session token (the session_token cookie on the
// upgrade, or a session token as the auth token). With several accounts that
// user must also be an admin. See internal/server/principal.

// ConnectionIsUser reports whether the connection belongs to a signed-in user.
// GetConnectionUser is only ever set from a validated session token.
func (h *AgentHandler) ConnectionIsUser(c *fiberws.Conn) bool {
	return principal.FromConnUser(h.GetConnectionUser(c)).IsUser()
}

// RequireUserPrincipal returns true when the connection may perform the
// privileged action: a signed-in admin, or the only user account (see
// principal.IsAdmin). Otherwise it answers with an error carrying code
// "user_required" (API key only) or "admin_required" (a signed-in non-admin
// while several accounts exist) and returns false; the caller then stops
// without doing anything.
func (h *AgentHandler) RequireUserPrincipal(c *fiberws.Conn, action string) bool {
	return h.requirePrincipal(c, action, principal.AllowedConnUser)
}

// RequireSignedInPrincipal is RequireUserPrincipal for user-level actions
// that need a human but not an admin (answering a permission request in a
// session the user may see).
func (h *AgentHandler) RequireSignedInPrincipal(c *fiberws.Conn, action string) bool {
	return h.requirePrincipal(c, action, principal.AllowedConnSignedIn)
}

func (h *AgentHandler) requirePrincipal(c *fiberws.Conn, action string, allowed func(*string) bool) bool {
	username := h.GetConnectionUser(c)
	if allowed(username) {
		return true
	}
	p := principal.FromConnUser(username)
	code, msg := principal.Refusal(p, action)
	logging.Warning("SECURITY: refused %q on a WebSocket connection (user %q) from %s: %s", action, p.Username, c.RemoteAddr(), code)
	_ = h.safeWriteJSON(c, map[string]interface{}{
		"type":    "error",
		"code":    code,
		"message": msg,
	})
	return false
}

// requireOwnSessionUnlessAdmin lets an admin (or the only user, or anyone
// when user auth is off) through; a signed-in non-admin only for a session
// whose owner is explicitly that user. Sessions without an owner (created
// with the API key, or before ownership tracking) belong to the admin.
// It answers admin_required and returns false otherwise.
func (h *AgentHandler) requireOwnSessionUnlessAdmin(c *fiberws.Conn, sessionID uuid.UUID, action string) bool {
	username := h.GetConnectionUser(c)
	if principal.AllowedConnUser(username) {
		return true
	}
	if username != nil && *username != "" && h.sessionExplicitlyOwnedBy(sessionID, *username) {
		return true
	}
	return h.RequireUserPrincipal(c, action)
}

// sessionExplicitlyOwnedBy reports whether the stored session's owner is
// username (or that user's id).
func (h *AgentHandler) sessionExplicitlyOwnedBy(sessionID uuid.UUID, username string) bool {
	if h.SessionManager == nil || h.SessionManager.Storage == nil {
		return false
	}
	meta, err := h.SessionManager.Storage.GetSession(sessionID)
	if err != nil || meta == nil || meta.OwnerUserID == nil || *meta.OwnerUserID == "" {
		return false
	}
	if *meta.OwnerUserID == username {
		return true
	}
	if h.db == nil {
		return false
	}
	dbUser, err := database.NewRepository(h.db).GetUser(username)
	return err == nil && dbUser != nil && dbUser.ID.Valid && *meta.OwnerUserID == dbUser.ID.String
}

// LinkLoopSessionAction is creating a session linked to a proposing agent
// session (spawned_by_session_id), which lets the two message each other.
const LinkLoopSessionAction = "Linking a session to an agent's loop proposal"

// isBypassPermissionMode reports whether a permission mode skips permission
// prompts (fully or for all tools).
func isBypassPermissionMode(mode string) bool {
	m := strings.ToLower(strings.TrimSpace(mode))
	switch m {
	case "yolo", "bypasspermissions", "allow-all", "dangerously-skip-permissions", "dangerouslyskippermissions":
		return true
	}
	return false
}

// PromptUnattendedSessionAction is the action of sending a prompt to a session
// that runs unattended and unsandboxed.
const PromptUnattendedSessionAction = "Sending a prompt to a session without permission prompts or a sandbox"

// PromptNeedsUserAction returns the action name when a prompt to a session
// with these options needs a signed-in user (it skips permission prompts,
// YOLO / bypass / allow-all, and is not sandboxed), or "". It applies to
// external callers only: internal senders (inbox wake-ups, loop iterations,
// resumes, kickoffs) call SessionManager directly and never pass through the
// WebSocket handler that asks. acceptEdits counts as skipping prompts: edits
// of any file (hooks, scripts the next command runs) need no approval.
func PromptNeedsUserAction(opts *SessionOptions) string {
	if opts == nil || !principal.Enforced() || isSandboxed(opts) {
		return ""
	}
	skips := (opts.DangerouslySkipPermissions != nil && *opts.DangerouslySkipPermissions) ||
		(opts.AllowDangerouslySkipPermissions != nil && *opts.AllowDangerouslySkipPermissions) ||
		(opts.PermissionMode != nil && (isBypassPermissionMode(*opts.PermissionMode) ||
			strings.EqualFold(strings.TrimSpace(*opts.PermissionMode), "acceptEdits")))
	if !skips {
		return ""
	}
	return PromptUnattendedSessionAction
}

// ModelSwitchSetsBaseURL reports whether a model switch points the session at
// a custom base URL (where prompts, and possibly provider keys, are sent).
func ModelSwitchSetsBaseURL(baseURL *string) bool {
	return baseURL != nil && strings.TrimSpace(*baseURL) != ""
}

// PrivilegedSessionOptions returns the action a set of session options
// performs that needs a signed-in user, or "" when they need none.
func PrivilegedSessionOptions(opts *SessionOptions) string {
	if opts == nil {
		return ""
	}
	switch {
	case opts.CodexNetworkAccess:
		return CodexNetworkAction
	case hasSandboxGrants(opts):
		return SandboxGrantAction
	case opts.DangerouslySkipPermissions != nil && *opts.DangerouslySkipPermissions,
		opts.AllowDangerouslySkipPermissions != nil && *opts.AllowDangerouslySkipPermissions:
		return "Enabling YOLO mode"
	case opts.PermissionMode != nil && isBypassPermissionMode(*opts.PermissionMode):
		return "Starting a session that skips permission prompts"
	case len(opts.AlwaysAllowRules) > 0:
		return "Adding always-allow rules"
	case opts.Loop != nil && strings.TrimSpace(opts.Loop.VerifyCommand) != "":
		return "Setting a loop verify command"
	case ModelSwitchSetsBaseURL(opts.BaseURL):
		return "Setting a custom base URL"
	case opts.Team != nil:
		// Its workers start without a card.
		return TeamSetupAction
	case len(opts.EnabledSkillIDs) > 0:
		// Like PUT /agent/sessions/:id/skills: a project skill brings its hooks.
		return "Adding skills to a session"
	case opts.AutoHandoffAfterMessages != nil || opts.AutoHandoffPrompt != nil:
		// Like PATCH .../options: the successor keeps YOLO and rules.
		return "Setting up auto-handoff"
	}
	return ""
}

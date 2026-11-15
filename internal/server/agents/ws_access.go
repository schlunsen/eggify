package agents

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	fiberws "github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// Session visibility on /agent/ws.
//
// A connection without a signed-in user (API key, or user auth off) sees every
// session, as the REST API does; privileged actions are gated separately by
// RequireUserPrincipal. A signed-in user sees their own sessions and sessions
// without an owner (shared, from before ownership tracking or created with
// the API key).

// sessionViewer answers "may this caller see/own that session" for one
// connection, looking the user's UUID up at most once.
type sessionViewer struct {
	h        *AgentHandler
	username *string // nil: no signed-in user (sees everything)
	userID   string
	loaded   bool
}

func (h *AgentHandler) viewerFor(username *string) *sessionViewer {
	return &sessionViewer{h: h, username: username}
}

// ownerIsUser reports whether owner names the viewer (by username or UUID).
func (v *sessionViewer) ownerIsUser(owner *string) bool {
	if v.username == nil || owner == nil {
		return false
	}
	if *owner == *v.username {
		return true
	}
	if !v.loaded {
		v.loaded = true
		v.userID = v.h.userIDFor(*v.username)
	}
	return v.userID != "" && *owner == v.userID
}

// canSee reports whether the viewer may see a session with this owner.
func (v *sessionViewer) canSee(owner *string) bool {
	if v.username == nil || owner == nil {
		return true
	}
	return v.ownerIsUser(owner)
}

// userIDFor returns the database UUID of username, or "".
func (h *AgentHandler) userIDFor(username string) string {
	if h.db == nil {
		return ""
	}
	dbUser, err := database.NewRepository(h.db).GetUser(username)
	if err != nil || dbUser == nil || !dbUser.ID.Valid {
		return ""
	}
	return dbUser.ID.String
}

// userIsAdmin reports whether username is an admin account.
func (h *AgentHandler) userIsAdmin(username string) bool {
	if h.IsAdminUser != nil {
		return h.IsAdminUser(username)
	}
	if h.db == nil {
		return false
	}
	dbUser, err := database.NewRepository(h.db).GetUser(username)
	return err == nil && dbUser != nil && dbUser.IsAdmin
}

// connIsAdmin reports whether the connection is a signed-in admin.
func (h *AgentHandler) connIsAdmin(c *fiberws.Conn) bool {
	u := h.GetConnectionUser(c)
	return u != nil && *u != "" && h.userIsAdmin(*u)
}

// sessionExists reports whether a session is live or stored.
func (h *AgentHandler) sessionExists(id uuid.UUID) bool {
	if _, err := h.SessionManager.GetSession(id); err == nil {
		return true
	}
	if h.SessionManager.Storage == nil {
		return false
	}
	meta, err := h.SessionManager.Storage.GetSession(id)
	return err == nil && meta != nil
}

// authorizeSession checks ownership and tells the client when it is refused.
func (h *AgentHandler) authorizeSession(c *fiberws.Conn, id uuid.UUID) error {
	if err := h.validateSessionOwnership(id, h.GetConnectionUser(c)); err != nil {
		h.sendFiberError(c, err.Error())
		return err
	}
	return nil
}

// bulkSessionFilter decides which sessions kill_all_agents / delete_all_sessions
// may touch for this connection. ok=false means the request was refused (the
// error has been sent). A nil filter means every session.
func (h *AgentHandler) bulkSessionFilter(c *fiberws.Conn, action string) (filter func(owner *string) bool, ok bool) {
	user := h.GetConnectionUser(c)
	if user == nil || *user == "" {
		// API key: with user auth on this is refused (an agent can read the
		// key); with user auth off there is nobody to tell apart.
		if !h.RequireUserPrincipal(c, action) {
			return nil, false
		}
		return nil, true
	}
	if h.userIsAdmin(*user) {
		return nil, true
	}
	// Everyone else: only their own sessions (not shared ones).
	v := h.viewerFor(user)
	return v.ownerIsUser, true
}

// redactedSessionOptions returns a copy of opts safe to send to a client
// listing sessions: session-scoped credentials are removed.
func redactedSessionOptions(opts SessionOptions) SessionOptions {
	out := opts
	out.APIKey = nil
	if out.BaseURL != nil {
		clean := stripURLUserinfo(*out.BaseURL)
		out.BaseURL = &clean
	}
	return out
}

// stripURLUserinfo removes "user:password@" from a URL.
func stripURLUserinfo(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

// projectDirAllowed reports whether a client may watch dir for a session: it
// must be the session's own folder or a known project's folder.
func (h *AgentHandler) projectDirAllowed(session *AgentSession, dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) {
		return false
	}
	clean := filepath.Clean(dir)
	wd := ""
	if session.Options.WorkingDirectory != nil {
		wd = *session.Options.WorkingDirectory
	}
	if wd != "" && pathWithin(clean, filepath.Clean(wd)) {
		return true
	}
	if session.WorktreePath != "" && pathWithin(clean, filepath.Clean(session.WorktreePath)) {
		return true
	}
	// A session without a folder runs in the server's folder (that is what
	// GET /api/agent/sessions/:id/context reports for it).
	if wd == "" {
		if cwd, err := os.Getwd(); err == nil && filepath.Clean(cwd) == clean {
			return true
		}
	}
	if h.db == nil {
		return false
	}
	repo := database.NewRepository(h.db)
	if p, err := repo.GetProjectByPath(clean); err == nil && p != nil {
		return true
	}
	if clean != dir {
		if p, err := repo.GetProjectByPath(dir); err == nil && p != nil {
			return true
		}
	}
	return false
}

// requireAdminUser lets only a signed-in admin through when user auth is on.
func (h *AgentHandler) requireAdminUser(c *fiberws.Conn, action string) bool {
	if !principal.Enforced() {
		return true
	}
	if !h.RequireUserPrincipal(c, action) {
		return false
	}
	if h.connIsAdmin(c) {
		return true
	}
	logging.Warning("SECURITY: refused %q for non-admin user on %s", action, c.RemoteAddr())
	_ = h.safeWriteJSON(c, map[string]interface{}{
		"type":    "error",
		"code":    "admin_required",
		"message": fmt.Sprintf("%s needs an admin account", action),
	})
	return false
}

// MarshalJSON sends a session to clients without its session-scoped
// credentials: every listing, session_updated / session_created and
// handover reply serialises Session, and only list_sessions used to redact
// it (a site-generator session carried the stored provider key in
// options.api_key). Storage marshals Options itself, so it keeps the key.
func (s Session) MarshalJSON() ([]byte, error) {
	type wire Session // same fields, without this method
	w := wire(s)
	w.Options = redactedSessionOptions(s.Options)
	return json.Marshal(w)
}

// MarshalJSON sends stored session metadata to clients (e.g. GET
// /api/projects/:id/sessions) with its options redacted like Session's: the
// raw options JSON holds a session-scoped api_key.
func (m SessionMetadata) MarshalJSON() ([]byte, error) {
	type wire SessionMetadata // same fields, without this method
	w := wire(m)
	if w.OptionsJSON != "" {
		var opts SessionOptions
		if err := json.Unmarshal([]byte(w.OptionsJSON), &opts); err != nil {
			w.OptionsJSON = ""
		} else if b, err := json.Marshal(redactedSessionOptions(opts)); err == nil {
			w.OptionsJSON = string(b)
		} else {
			w.OptionsJSON = ""
		}
	}
	return json.Marshal(w)
}

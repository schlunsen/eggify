package agents

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/n0"
)

// Session control endpoints for eggify-tools.
//
// A session's eggify-tools process runs as the same OS user as Eggify and could
// write eggify.db directly, so anything with a rule attached (approvals,
// handovers) is done by the Eggify server instead: eggify-tools calls these
// loopback-only endpoints with its session id and session capability (the
// same HMAC capability it uses for the n0 proxy), and the server enforces the
// rules for that session.

const maxControlBody = 4 << 20

func writeControlJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ControlHandler returns the http.Handler for n0.ControlPathPrefix.
func (sm *SessionManager) ControlHandler() http.Handler { return http.HandlerFunc(sm.ServeControl) }

// ServeControl authenticates an eggify-tools caller and dispatches to the
// endpoint. Only loopback callers holding the session's capability are
// accepted, and every action is scoped to that session.
func (sm *SessionManager) ServeControl(w http.ResponseWriter, r *http.Request) {
	if !n0.IsLoopbackRequest(r.RemoteAddr, r.Header) {
		writeControlJSON(w, http.StatusForbidden, map[string]string{"error": "loopback callers only"})
		return
	}
	if r.Method != http.MethodPost {
		writeControlJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	sid := r.Header.Get(n0.HeaderSessionID)
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || !n0.VerifyProxyToken(sid, strings.TrimPrefix(auth, "Bearer ")) {
		writeControlJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid session credentials"})
		return
	}
	sessionID, err := uuid.Parse(sid)
	if err != nil {
		writeControlJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid session credentials"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxControlBody+1))
	if err != nil || len(body) > maxControlBody {
		writeControlJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request too large"})
		return
	}

	switch r.URL.Path {
	case n0.ControlToolGuardPath:
		var req struct {
			Tool      string                 `json:"tool"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if json.Unmarshal(body, &req) != nil || req.Tool == "" {
			writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": "tool is required"})
			return
		}
		writeControlJSON(w, http.StatusOK, sm.GuardTool(r.Context(), sessionID, req.Tool, req.Arguments))

	case n0.ControlHandoverCreatePath:
		var req AgentHandoverCreateRequest
		if json.Unmarshal(body, &req) != nil {
			writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		resp, err := sm.CreateHandoverForAgent(sessionID, req)
		if err != nil {
			writeControlJSON(w, controlStatus(err), map[string]string{"error": err.Error()})
			return
		}
		writeControlJSON(w, http.StatusOK, resp)

	case n0.ControlHandoverAcceptPath:
		var req AgentHandoverAcceptRequest
		if json.Unmarshal(body, &req) != nil || req.HandoverToken == "" {
			writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": "handover_token is required"})
			return
		}
		resp, err := sm.AcceptHandoverForAgent(sessionID, req)
		if err != nil {
			writeControlJSON(w, controlStatus(err), map[string]string{"error": err.Error()})
			return
		}
		writeControlJSON(w, http.StatusOK, resp)

	case n0.ControlLoopStartPath:
		var req LoopSessionRequest
		if json.Unmarshal(body, &req) != nil {
			writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		// Blocks until the user answers the card.
		resp, err := sm.StartLoopSessionForAgent(r.Context(), sessionID, req)
		if err != nil {
			writeControlJSON(w, controlStatus(err), map[string]string{"error": err.Error()})
			return
		}
		writeControlJSON(w, http.StatusOK, resp)

	case n0.ControlWorkerStartPath:
		var req WorkerSessionRequest
		if json.Unmarshal(body, &req) != nil {
			writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		// Blocks until the user answers the card.
		resp, err := sm.StartWorkerSessionForAgent(r.Context(), sessionID, req)
		if err != nil {
			writeControlJSON(w, controlStatus(err), map[string]string{"error": err.Error()})
			return
		}
		writeControlJSON(w, http.StatusOK, resp)

	case n0.ControlLoopGetPath:
		var req struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(body, &req)
		target := uuid.Nil
		if req.SessionID != "" {
			id, err := uuid.Parse(req.SessionID)
			if err != nil {
				writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session_id"})
				return
			}
			target = id
		}
		infos, err := sm.GetLoopSessionsForAgent(sessionID, target)
		if err != nil {
			writeControlJSON(w, controlStatus(err), map[string]string{"error": err.Error()})
			return
		}
		writeControlJSON(w, http.StatusOK, map[string]interface{}{"loop_sessions": infos})

	case n0.ControlLoopMessagePath, n0.ControlLoopToParentPath:
		var req struct {
			SessionID string `json:"session_id"`
			Text      string `json:"text"`
		}
		if json.Unmarshal(body, &req) != nil {
			writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		var delivery string
		var err error
		if r.URL.Path == n0.ControlLoopToParentPath {
			delivery, err = sm.MessageParentSessionForAgent(sessionID, req.Text)
		} else {
			target, perr := uuid.Parse(req.SessionID)
			if perr != nil {
				writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id is required"})
				return
			}
			delivery, err = sm.MessageLoopSessionForAgent(sessionID, target, req.Text)
		}
		if err != nil {
			writeControlJSON(w, controlStatus(err), map[string]string{"error": err.Error()})
			return
		}
		writeControlJSON(w, http.StatusOK, map[string]string{"delivery": delivery, "note": deliveryNote(delivery)})

	default:
		writeControlJSON(w, http.StatusNotFound, map[string]string{"error": "unknown endpoint"})
	}
}

func deliveryNote(d string) string {
	switch d {
	case DeliveryWoke:
		return "Delivered: the session was idle and is reading it now."
	case DeliveryQueued:
		return "Queued: the session is busy and gets it with its next prompt."
	case DeliveryStored:
		return "The session has ended; the message was saved and it gets it if the user resumes it."
	}
	return "The message could not be stored."
}

func controlStatus(err error) int {
	if errors.Is(err, ErrHandoverForeignSession) || errors.Is(err, ErrLoopForeignSession) || errors.Is(err, ErrLoopNested) || errors.Is(err, ErrWorkerNested) || errors.Is(err, ErrReviewRoundsExhausted) {
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}

// ErrHandoverForeignSession is returned when an agent tries to export
// another session.
var ErrHandoverForeignSession = errors.New("an agent can only create a handover of its own session")

// AgentHandoverCreateRequest is create_handover from eggify-tools.
type AgentHandoverCreateRequest struct {
	SessionID         string `json:"session_id,omitempty"` // must be the caller's own session (or empty)
	MessageLimit      *int   `json:"message_limit,omitempty"`
	ExpirationMinutes *int   `json:"expiration_minutes,omitempty"`
	HandoverNote      string `json:"handover_note,omitempty"`
}

// AgentHandoverAcceptRequest is accept_handover from eggify-tools.
type AgentHandoverAcceptRequest struct {
	HandoverToken string `json:"handover_token"`
	AutoStart     bool   `json:"auto_start,omitempty"`
	InitialPrompt string `json:"initial_prompt,omitempty"`
}

// AgentHandoverAccepted is the reply to accept_handover from eggify-tools.
type AgentHandoverAccepted struct {
	*HandoverAcceptedMessage
	PendingPrompt string `json:"pending_prompt,omitempty"`
	Started       bool   `json:"started"`
	Note          string `json:"note"`
}

const (
	agentHandoverMaxMessages   = 500
	agentHandoverMaxExpiration = 24 * 60
)

// CreateHandoverForAgent creates a handover of the calling agent's own
// session. Permission settings (yolo / bypass mode, always-allow rules) are
// never carried over from here: only a signed-in user can choose that.
func (sm *SessionManager) CreateHandoverForAgent(caller uuid.UUID, req AgentHandoverCreateRequest) (*HandoverCreatedMessage, error) {
	if req.SessionID != "" {
		id, err := uuid.Parse(req.SessionID)
		if err != nil {
			return nil, errors.New("invalid session_id")
		}
		if id != caller {
			logging.Warning("🛑 Session %s tried to create a handover of session %s", caller, id)
			return nil, ErrHandoverForeignSession
		}
	}
	opts := HandoverOptions{
		IncludeMessages:   true,
		IncludeContext:    true,
		IncludeWorkingDir: true,
		HandoverNote:      req.HandoverNote,
		MessageLimit:      clampPositive(req.MessageLimit, agentHandoverMaxMessages),
		ExpirationMinutes: clampPositive(req.ExpirationMinutes, agentHandoverMaxExpiration),
	}
	return sm.CreateHandover(caller, opts)
}

func clampPositive(v *int, max int) *int {
	if v == nil || *v <= 0 {
		return nil
	}
	n := *v
	if n > max {
		n = max
	}
	return &n
}

// AcceptHandoverForAgent accepts a handover for an agent. The new session is
// created idle: an agent-supplied first prompt is never sent on its own. It
// is recorded in the new session for the user to review and send.
func (sm *SessionManager) AcceptHandoverForAgent(caller uuid.UUID, req AgentHandoverAcceptRequest) (*AgentHandoverAccepted, error) {
	resp, pending, err := sm.applyHandoverIdle(req.HandoverToken, nil, nil, req.AutoStart, req.InitialPrompt, "session "+caller.String())
	if err != nil {
		return nil, err
	}
	// The new session belongs to the calling session's owner: an ownerless
	// one is shared, and it holds the source session's history.
	if owner := sm.sessionOwner(caller); owner != nil && resp != nil {
		if err := sm.SetSessionOwner(resp.SessionID, owner); err != nil {
			logging.Warning("Agent handover: could not set the owner of session %s: %v", resp.SessionID, err)
		}
	}
	out := &AgentHandoverAccepted{HandoverAcceptedMessage: resp, PendingPrompt: pending}
	out.Note = "The new session was created idle. Nothing was sent to it: the user reviews and starts it in Eggify."
	if pending != "" {
		out.Note = "The new session was created idle. The proposed first prompt was NOT sent; it is shown in the new session for the user to review and send."
	}
	return out, nil
}

// CreateHandoverFor creates a handover for a REST/WebSocket caller. Only a
// signed-in user may carry yolo / bypass mode or always-allow rules over;
// for anyone else (e.g. the API key, which agents can read) those options
// are dropped.
func (sm *SessionManager) CreateHandoverFor(callerIsUser bool, sessionID uuid.UUID, options HandoverOptions) (*HandoverCreatedMessage, error) {
	if !callerIsUser && (options.PreserveYOLOMode || options.PreserveAlwaysAllowRules) {
		logging.Warning("🛑 Handover of %s: permission carry-over requested without a signed-in user — not carried", sessionID)
		options.PreserveYOLOMode = false
		options.PreserveAlwaysAllowRules = false
	}
	return sm.CreateHandover(sessionID, options)
}

// AcceptHandoverFor accepts a handover for a REST/WebSocket caller. Only a
// signed-in user may auto-start the new session or set permission bypass
// settings on it; for anyone else the session is created idle and a proposed
// first prompt is left in it for the user to send.
//
// YOLO / bypass mode and always-allow rules stored in the handover are kept
// only when a signed-in user asks for it (keepPermissions); otherwise they are
// dropped from the new session and from the stored handover.
func (sm *SessionManager) AcceptHandoverFor(callerIsUser bool, token string, cfg *SessionOptions, existing *uuid.UUID, autoStart bool, initialPrompt string, keepPermissions bool) (*HandoverAcceptedMessage, error) {
	if callerIsUser {
		return sm.applyHandover(token, cfg, existing, autoStart, initialPrompt, keepPermissions)
	}
	if keepPermissions {
		logging.Warning("🛑 Handover accept: keep_permissions requested without a signed-in user — not kept")
	}
	if cfg != nil {
		c := *cfg
		stripHandoverPermissions(&c, HandoverOptions{})
		cfg = &c
	}
	resp, _, err := sm.applyHandoverIdle(token, cfg, existing, autoStart, initialPrompt, "an API-key client")
	return resp, err
}

// applyHandoverIdle applies a handover without starting a turn. When a first
// prompt was requested (or auto_start asked for the default one), it is saved
// in the new session as a pending prompt for the user. requestedBy is shown
// in that note.
func (sm *SessionManager) applyHandoverIdle(token string, cfg *SessionOptions, existing *uuid.UUID, autoStart bool, initialPrompt, requestedBy string) (*HandoverAcceptedMessage, string, error) {
	// Never keeps the handover's permission settings: the caller is an agent
	// or a client without a signed-in user.
	resp, err := sm.applyHandover(token, cfg, existing, false, "", false)
	if err != nil {
		return nil, "", err
	}
	prompt := strings.TrimSpace(initialPrompt)
	if prompt == "" && autoStart && existing == nil {
		note := ""
		if resp.Session.Options.ContextSummary != nil {
			note = *resp.Session.Options.ContextSummary
		}
		prompt = defaultHandoverPrompt(note)
	}
	if prompt == "" {
		return resp, "", nil
	}
	sm.recordPendingHandoverPrompt(resp.SessionID, prompt, requestedBy)
	return resp, prompt, nil
}

func defaultHandoverPrompt(note string) string {
	if note != "" {
		return "I've received a handover from another session with the following context:\n\n" + note + "\n\nPlease continue working on the tasks from this session."
	}
	return "Please continue working on the tasks from the previous session."
}

// pendingHandoverPromptPrefix starts the system note holding a prompt that
// was proposed for a handed-over session but not sent.
const pendingHandoverPromptPrefix = "📨 Handover prompt waiting for you"

func (sm *SessionManager) recordPendingHandoverPrompt(sessionID uuid.UUID, prompt, requestedBy string) {
	note := pendingHandoverPromptPrefix + " (proposed by " + requestedBy + ", not sent). " +
		"Review it, and send it yourself if you want this session to start:\n\n" + prompt

	sm.mu.Lock()
	session, ok := sm.sessions[sessionID]
	if !ok {
		sm.mu.Unlock()
		logging.Warning("Pending handover prompt for unknown session %s", sessionID)
		return
	}
	session.MessageCount++
	session.UpdatedAt = time.Now()
	seq := session.MessageCount
	snapshot := session.Session
	sm.mu.Unlock()

	if err := sm.saveMessageToDB(sessionID, seq, "system", note, "", nil, nil); err != nil {
		logging.Warning("Failed to save pending handover prompt for %s: %v", sessionID, err)
		return
	}
	if err := sm.updateSessionInDB(&snapshot); err != nil {
		logging.Warning("Failed to update session %s after pending handover prompt: %v", sessionID, err)
	}
	sm.broadcastSessionUpdate(session)
}

// sessionOwner is a session's owner, live or stored, or nil.
func (sm *SessionManager) sessionOwner(id uuid.UUID) *string {
	sm.mu.RLock()
	live, ok := sm.sessions[id]
	var owner *string
	if ok {
		owner = live.OwnerUserID
	}
	sm.mu.RUnlock()
	if ok {
		return owner
	}
	if sm.Storage == nil {
		return nil
	}
	meta, err := sm.Storage.GetSession(id)
	if err != nil || meta == nil {
		return nil
	}
	return meta.OwnerUserID
}

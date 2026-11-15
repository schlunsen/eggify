package agents

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/n0"
	"github.com/schlunsen/eggify/internal/server/principal"
	"github.com/stretchr/testify/require"
)

// seedYoloSession is a session with yolo mode, bypassPermissions and an
// always-allow rule, persisted so CreateHandover can load it.
func seedYoloSession(t *testing.T, sm *SessionManager) *AgentSession {
	t.Helper()
	yes := true
	mode := "bypassPermissions"
	return seedSwitchSession(t, sm, "claude", "opus", func(s *Session) {
		s.Options.DangerouslySkipPermissions = &yes
		s.Options.AllowDangerouslySkipPermissions = &yes
		s.Options.PermissionMode = &mode
		s.Options.AlwaysAllowRules = []AlwaysAllowRule{{ID: "r1", Tool: "Bash", MatchMode: "tool"}}
	})
}

func handoverOptionsOf(t *testing.T, token string) SessionOptions {
	t.Helper()
	rec, err := database.NewRepository(database.GetInstance()).GetHandoverByToken(token)
	require.NoError(t, err)
	var data HandoverData
	require.NoError(t, json.Unmarshal([]byte(rec.HandoverData), &data))
	return data.SessionMetadata.Options
}

func TestHandoverDoesNotCarryPermissionsByDefault(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	src := seedYoloSession(t, sm)

	resp, err := sm.CreateHandover(src.ID, HandoverOptions{IncludeContext: true})
	require.NoError(t, err)
	require.False(t, resp.PermissionsCarried)
	opts := handoverOptionsOf(t, resp.HandoverToken)
	require.Nil(t, opts.DangerouslySkipPermissions)
	require.Nil(t, opts.AllowDangerouslySkipPermissions)
	require.Nil(t, opts.PermissionMode)
	require.Empty(t, opts.AlwaysAllowRules)

	accepted, err := sm.ApplyHandover(resp.HandoverToken, nil, nil, false, "")
	require.NoError(t, err)
	require.Nil(t, accepted.Session.Options.DangerouslySkipPermissions)
	require.Empty(t, accepted.Session.Options.AlwaysAllowRules)

	// A signed-in user may choose to carry them over.
	resp, err = sm.CreateHandoverFor(true, src.ID, HandoverOptions{PreserveYOLOMode: true, PreserveAlwaysAllowRules: true})
	require.NoError(t, err)
	require.True(t, resp.PermissionsCarried)
	opts = handoverOptionsOf(t, resp.HandoverToken)
	require.NotNil(t, opts.DangerouslySkipPermissions)
	require.Len(t, opts.AlwaysAllowRules, 1)

	// An API-key caller (readable by agents) may not.
	withEnforced(t, true)
	require.False(t, principal.AllowedConnUser(nil))
	resp, err = sm.CreateHandoverFor(false, src.ID, HandoverOptions{PreserveYOLOMode: true, PreserveAlwaysAllowRules: true})
	require.NoError(t, err)
	require.False(t, resp.PermissionsCarried)
	opts = handoverOptionsOf(t, resp.HandoverToken)
	require.Nil(t, opts.DangerouslySkipPermissions)
	require.Empty(t, opts.AlwaysAllowRules)
}

func TestConnUserPrincipalForHandovers(t *testing.T) {
	// The shared check (internal/server/principal) used for handover and
	// approval-card decisions on WebSocket connections.
	u := "alice"
	withEnforced(t, true)
	require.True(t, principal.AllowedConnUser(&u))
	require.False(t, principal.AllowedConnUser(nil))
	empty := ""
	require.False(t, principal.AllowedConnUser(&empty))
	principal.SetEnforced(false) // no users exist: keep working
	require.True(t, principal.AllowedConnUser(nil))
}

func TestAgentCreateHandoverOnlyForItsOwnSession(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	caller := seedYoloSession(t, sm)
	other := seedSwitchSession(t, sm, "claude", "opus", nil)

	_, err := sm.CreateHandoverForAgent(caller.ID, AgentHandoverCreateRequest{SessionID: other.ID.String()})
	require.ErrorIs(t, err, ErrHandoverForeignSession)

	_, err = sm.CreateHandoverForAgent(caller.ID, AgentHandoverCreateRequest{SessionID: "nope"})
	require.Error(t, err)

	big, huge := 100000, 999999
	resp, err := sm.CreateHandoverForAgent(caller.ID, AgentHandoverCreateRequest{MessageLimit: &big, ExpirationMinutes: &huge})
	require.NoError(t, err)
	require.Equal(t, caller.ID, resp.SourceSessionID)
	require.False(t, resp.PermissionsCarried)
	require.WithinDuration(t, time.Now().Add(24*time.Hour), resp.ExpiresAt, time.Minute)
	opts := handoverOptionsOf(t, resp.HandoverToken)
	require.Nil(t, opts.DangerouslySkipPermissions, "agents never carry yolo mode over")
	require.Empty(t, opts.AlwaysAllowRules)
}

func TestAgentAcceptHandoverNeverAutoRuns(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	src := seedSwitchSession(t, sm, "claude", "opus", nil)
	created, err := sm.CreateHandover(src.ID, HandoverOptions{HandoverNote: "keep going"})
	require.NoError(t, err)

	out, err := sm.AcceptHandoverForAgent(src.ID, AgentHandoverAcceptRequest{
		HandoverToken: created.HandoverToken,
		AutoStart:     true,
		InitialPrompt: "rm -rf everything and push to main",
	})
	require.NoError(t, err)
	require.False(t, out.Started)
	require.Equal(t, SessionStatusIdle, out.Session.Status)
	require.Equal(t, "rm -rf everything and push to main", out.PendingPrompt)

	sm.mu.RLock()
	newSession := sm.sessions[out.SessionID]
	sm.mu.RUnlock()
	require.NotNil(t, newSession)
	require.Equal(t, SessionStatusIdle, newSession.Status)

	// The proposed prompt is a system note for the user, not a user turn.
	msgs, _, err := sm.Storage.GetMessages(out.SessionID, 10, 0)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, "system", msgs[0].Role)
	require.True(t, strings.HasPrefix(msgs[0].Content, pendingHandoverPromptPrefix))
	require.Contains(t, msgs[0].Content, "rm -rf everything and push to main")

	// auto_start without a prompt proposes the default one, still unsent.
	created, err = sm.CreateHandover(src.ID, HandoverOptions{HandoverNote: "note"})
	require.NoError(t, err)
	out, err = sm.AcceptHandoverForAgent(src.ID, AgentHandoverAcceptRequest{HandoverToken: created.HandoverToken, AutoStart: true})
	require.NoError(t, err)
	require.Contains(t, out.PendingPrompt, "note")
	require.Equal(t, SessionStatusIdle, out.Session.Status)

	// API-key callers on REST/WS get the same treatment.
	created, err = sm.CreateHandover(src.ID, HandoverOptions{})
	require.NoError(t, err)
	yes := true
	resp, err := sm.AcceptHandoverFor(false, created.HandoverToken, &SessionOptions{DangerouslySkipPermissions: &yes}, nil, true, "go", true)
	require.NoError(t, err)
	require.Equal(t, SessionStatusIdle, resp.Session.Status)
	require.Nil(t, resp.Session.Options.DangerouslySkipPermissions)
}

func controlRequest(t *testing.T, sm *SessionManager, path, sid, token, body string, hdr map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set(n0.HeaderSessionID, sid)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	sm.ServeControl(rec, req)
	return rec.Code, rec.Body.String()
}

func TestControlEndpointAuthAndHandoverScope(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	a := seedSwitchSession(t, sm, "claude", "opus", nil)
	b := seedSwitchSession(t, sm, "claude", "opus", nil)
	aid, bid := a.ID.String(), b.ID.String()

	code, _ := controlRequest(t, sm, n0.ControlHandoverCreatePath, aid, "", `{}`, nil)
	require.Equal(t, 401, code)
	code, _ = controlRequest(t, sm, n0.ControlHandoverCreatePath, aid, n0.ProxyToken(bid), `{}`, nil)
	require.Equal(t, 401, code, "another session's capability")
	code, _ = controlRequest(t, sm, n0.ControlHandoverCreatePath, aid, n0.ProxyToken(aid), `{}`, map[string]string{"X-Forwarded-For": "203.0.113.1"})
	require.Equal(t, 403, code)

	// Session a cannot export session b.
	code, body := controlRequest(t, sm, n0.ControlHandoverCreatePath, aid, n0.ProxyToken(aid), `{"session_id":"`+bid+`"}`, nil)
	require.Equal(t, 403, code, body)

	code, body = controlRequest(t, sm, n0.ControlHandoverCreatePath, aid, n0.ProxyToken(aid), `{"handover_note":"n"}`, nil)
	require.Equal(t, 200, code, body)
	var created HandoverCreatedMessage
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	require.Equal(t, a.ID, created.SourceSessionID)

	code, body = controlRequest(t, sm, n0.ControlHandoverAcceptPath, bid, n0.ProxyToken(bid),
		`{"handover_token":"`+created.HandoverToken+`","auto_start":true,"initial_prompt":"do it"}`, nil)
	require.Equal(t, 200, code, body)
	var accepted struct {
		SessionID     uuid.UUID `json:"session_id"`
		Started       bool      `json:"started"`
		PendingPrompt string    `json:"pending_prompt"`
		Session       Session   `json:"session"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &accepted))
	require.False(t, accepted.Started)
	require.Equal(t, "do it", accepted.PendingPrompt)
	require.Equal(t, SessionStatusIdle, accepted.Session.Status)

	// Read-only tool through the guard endpoint passes.
	code, body = controlRequest(t, sm, n0.ControlToolGuardPath, aid, n0.ProxyToken(aid), `{"tool":"gpu_status"}`, nil)
	require.Equal(t, 200, code)
	require.Contains(t, body, `"allowed":true`)

	code, _ = controlRequest(t, sm, "/internal/eggify/unknown", aid, n0.ProxyToken(aid), `{}`, nil)
	require.Equal(t, 404, code)
}

// Permission settings stored in a user-created handover reach the new session
// only when a signed-in user accepts it and asks to keep them. Every other
// accept drops them, from the session and from the stored handover row.
func TestHandoverAcceptKeepsPermissionsOnlyWhenAUserAsks(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	src := seedYoloSession(t, sm)
	withEnforced(t, true)
	newHandover := func() string {
		created, err := sm.CreateHandoverFor(true, src.ID, HandoverOptions{PreserveYOLOMode: true, PreserveAlwaysAllowRules: true})
		require.NoError(t, err)
		require.True(t, created.PermissionsCarried)
		return created.HandoverToken
	}
	requireStripped := func(t *testing.T, o SessionOptions) {
		t.Helper()
		require.Nil(t, o.DangerouslySkipPermissions)
		require.Nil(t, o.AllowDangerouslySkipPermissions)
		require.Nil(t, o.PermissionMode)
		require.Empty(t, o.AlwaysAllowRules)
	}

	// A signed-in user who does not ask: dropped, and scrubbed from the row.
	token := newHandover()
	resp, err := sm.AcceptHandoverFor(true, token, nil, nil, false, "", false)
	require.NoError(t, err)
	requireStripped(t, resp.Session.Options)
	requireStripped(t, handoverOptionsOf(t, token))
	stored, err := sm.Storage.GetSession(resp.SessionID)
	require.NoError(t, err)
	require.NotContains(t, stored.OptionsJSON, "bypassPermissions")
	require.NotContains(t, stored.OptionsJSON, `"dangerously_skip_permissions":true`)

	// The token is single-use: no second accept can pick the settings up.
	_, err = sm.AcceptHandoverFor(true, token, nil, nil, false, "", true)
	require.Error(t, err)

	// An API-key client asking to keep them: dropped.
	token = newHandover()
	resp, err = sm.AcceptHandoverFor(false, token, nil, nil, false, "", true)
	require.NoError(t, err)
	requireStripped(t, resp.Session.Options)
	requireStripped(t, handoverOptionsOf(t, token))

	// An agent via eggify-tools: dropped.
	token = newHandover()
	out, err := sm.AcceptHandoverForAgent(src.ID, AgentHandoverAcceptRequest{HandoverToken: token})
	require.NoError(t, err)
	requireStripped(t, out.Session.Options)
	requireStripped(t, handoverOptionsOf(t, token))

	// A signed-in user who asks: kept.
	token = newHandover()
	resp, err = sm.AcceptHandoverFor(true, token, nil, nil, false, "", true)
	require.NoError(t, err)
	require.NotNil(t, resp.Session.Options.DangerouslySkipPermissions)
	require.True(t, *resp.Session.Options.DangerouslySkipPermissions)
	require.NotNil(t, resp.Session.Options.PermissionMode, "bypass mode kept")
}

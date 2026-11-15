package agents

// Red-team tests for /agent/ws connection handling and session ownership:
// anonymous sockets filling the connection limit, panics on short tokens,
// taking over or answering for another user's session, listing other users'
// sessions (and their keys), bulk kill/delete, the server log, project
// watching and signed-out sessions.

import (
	"bytes"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	fastws "github.com/fasthttp/websocket"
	"github.com/schlunsen/eggify/internal/database"
)

// seedOwned adds an idle session owned by owner ("" = no owner), creating
// the owner's user row (sessions reference users).
func seedOwned(t *testing.T, w *wsHarness, owner string, mutate func(*Session)) *AgentSession {
	t.Helper()
	if owner != "" {
		repo := database.NewRepository(database.GetInstance())
		if u, err := repo.GetUser(owner); err != nil || u == nil {
			now := time.Now()
			if err := repo.CreateUser(&database.DBUser{Username: owner, PasswordHash: "x", AuthMethod: "password", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatalf("create user %s: %v", owner, err)
			}
		}
	}
	return seedSwitchSession(t, w.sm, "claude", "opus", func(s *Session) {
		if owner != "" {
			s.OwnerUserID = strPtr(owner)
		}
		if mutate != nil {
			mutate(s)
		}
	})
}

// dialRaw opens a socket without authenticating.
func (w *wsHarness) dialRaw(t *testing.T) *fastws.Conn {
	t.Helper()
	conn, _, err := fastws.DefaultDialer.Dial(w.url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// expectClosed reads until the server closes the connection.
func expectClosed(t *testing.T, conn *fastws.Conn, within time.Duration) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(within))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				t.Fatalf("connection still open after %s", within)
			}
			return
		}
	}
}

// expectAlive checks the connection still answers a ping.
func (w *wsHarness) expectAlive(t *testing.T, conn *fastws.Conn) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{"type": "ping"}); err != nil {
		t.Fatalf("connection gone: %v", err)
	}
	w.expect(t, conn, func(m map[string]any) bool { return m["type"] == string(MessageTypePong) })
}

func (w *wsHarness) active() int {
	w.h.Mu.Lock()
	defer w.h.Mu.Unlock()
	return w.h.Active
}

func TestRedTeam_WS_AnonymousSocketsDoNotFillTheLimit(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	w.h.Config.MaxConcurrentSessions = 2
	w.h.PreAuthTimeout = 400 * time.Millisecond

	var anon []*fastws.Conn
	for i := 0; i < 6; i++ {
		anon = append(anon, w.dialRaw(t))
	}
	time.Sleep(100 * time.Millisecond)
	if n := w.active(); n != 0 {
		t.Fatalf("unauthenticated sockets counted against the limit: active=%d", n)
	}

	// The real user still gets in.
	conn := w.dial(t, "alice")
	w.expectAlive(t, conn)
	if n := w.active(); n != 1 {
		t.Fatalf("active=%d after one signed-in connection, want 1", n)
	}

	// The anonymous ones are closed once the pre-auth deadline passes.
	for _, c := range anon {
		expectClosed(t, c, 3*time.Second)
	}
	w.expectAlive(t, conn)
}

func TestRedTeam_WS_UnauthenticatedChatterIsClosed(t *testing.T) {
	w := newWSHarness(t)
	conn := w.dialRaw(t)
	for i := 0; i < wsPreAuthMaxMessages; i++ {
		if err := conn.WriteJSON(map[string]any{"type": "list_sessions"}); err != nil {
			break
		}
	}
	expectClosed(t, conn, 3*time.Second)

	// A large message before authentication is refused outright.
	big := w.dialRaw(t)
	_ = big.WriteMessage(fastws.TextMessage, append([]byte(`{"type":"auth","token":"`), append(bytes.Repeat([]byte("a"), wsPreAuthReadLimit+1024), []byte(`"}`)...)...))
	expectClosed(t, big, 3*time.Second)
}

func TestRedTeam_WS_ShortHandoverTokenDoesNotPanic(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	conn := w.dial(t, "alice")
	for _, tok := range []string{"", "x", "0123456789abcde"} {
		if err := conn.WriteJSON(map[string]any{"type": "accept_handover", "handover_token": tok}); err != nil {
			t.Fatal(err)
		}
		w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "error" })
	}
	w.expectAlive(t, conn)
	if got := safePrefix("abc", 16); got != "abc" {
		t.Fatalf("safePrefix = %q", got)
	}
}

func TestRedTeam_WS_PanicInHandlerClosesOnlyThatConnection(t *testing.T) {
	w := newWSHarness(t)
	other := w.dial(t, "bob")
	conn := w.dial(t, "alice")
	// A session manager without storage makes ownership checks panic.
	w.h.SessionManager.mu.Lock()
	saved := w.h.SessionManager.Storage
	w.h.SessionManager.Storage = nil
	w.h.SessionManager.mu.Unlock()
	_ = conn.WriteJSON(map[string]any{"type": "load_messages", "session_id": w.session.ID.String()})
	expectClosed(t, conn, 3*time.Second)
	w.h.SessionManager.mu.Lock()
	w.h.SessionManager.Storage = saved
	w.h.SessionManager.mu.Unlock()
	w.expectAlive(t, other)
}

func TestRedTeam_WS_CreateSessionTakeoverRefused(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	bobs := seedOwned(t, w, "bob", nil)
	conn := w.dial(t, "alice")

	_ = conn.WriteJSON(map[string]any{"type": "create_session", "session_id": bobs.ID.String(), "options": map[string]any{}})
	w.expect(t, conn, isErrorContaining("do not own"))

	meta, err := w.sm.Storage.GetSession(bobs.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.OwnerUserID == nil || *meta.OwnerUserID != "bob" {
		t.Fatalf("create_session changed the owner of bob's session to %v", meta.OwnerUserID)
	}

	// Bob reopening his own session keeps him as the owner.
	bob := w.dial(t, "bob")
	_ = bob.WriteJSON(map[string]any{"type": "create_session", "session_id": bobs.ID.String(), "options": map[string]any{}})
	w.expect(t, bob, func(m map[string]any) bool { return m["type"] == string(MessageTypeSessionCreated) })

	// A shared (unowned) session reopened by a user stays shared.
	shared := seedOwned(t, w, "", nil)
	_ = conn.WriteJSON(map[string]any{"type": "create_session", "session_id": shared.ID.String(), "options": map[string]any{}})
	w.expect(t, conn, func(m map[string]any) bool { return m["type"] == string(MessageTypeSessionCreated) })
	if meta, _ := w.sm.Storage.GetSession(shared.ID); meta.OwnerUserID != nil {
		t.Fatalf("reopening a shared session claimed it for %s", *meta.OwnerUserID)
	}
}

func TestRedTeam_WS_QuestionAnswerByNonOwnerRefused(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	bobs := seedOwned(t, w, "bob", nil)
	ch := make(chan UserQuestionAnswerResponse, 1)
	bobs.questionMu.Lock()
	bobs.pendingQuestions = map[string]chan UserQuestionAnswerResponse{"q1": ch}
	bobs.questionMu.Unlock()

	conn := w.dial(t, "alice")
	sid := bobs.ID.String()
	_ = conn.WriteJSON(map[string]any{"type": "user_question_response", "session_id": sid, "question_id": "q1", "answers": []any{"yes"}})
	w.expect(t, conn, isErrorContaining("do not own"))
	_ = conn.WriteJSON(map[string]any{"type": "user_question_dismiss", "session_id": sid, "question_id": "q1"})
	w.expect(t, conn, isErrorContaining("do not own"))

	select {
	case r := <-ch:
		t.Fatalf("a non-owner answered bob's question: %+v", r)
	default:
	}

	bob := w.dial(t, "bob")
	_ = bob.WriteJSON(map[string]any{"type": "user_question_response", "session_id": sid, "question_id": "q1", "answers": []any{"yes"}})
	select {
	case r := <-ch:
		if len(r.Answers) != 1 || r.Answers[0] != "yes" {
			t.Fatalf("owner's answer delivered as %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the owner's answer was not delivered")
	}
}

func TestRedTeam_WS_ListSessionsFilteredAndSecretsStripped(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	bobs := seedOwned(t, w, "bob", func(s *Session) { s.Options.APIKey = strPtr("sk-bob-secret") })
	alices := seedOwned(t, w, "alice", func(s *Session) {
		s.Options.APIKey = strPtr("sk-alice-secret")
		s.Options.BaseURL = strPtr("https://user:pw-secret@llm.example/v1")
	})
	conn := w.dial(t, "alice")

	_ = conn.WriteJSON(map[string]any{"type": "list_sessions"})
	deadline := time.Now().Add(5 * time.Second)
	var raw []byte
	for {
		_ = conn.SetReadDeadline(deadline)
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(`"sessions_list"`)) {
			raw = data
			break
		}
	}
	body := string(raw)
	if strings.Contains(body, bobs.ID.String()) {
		t.Fatal("list_sessions showed bob's session to alice")
	}
	if !strings.Contains(body, alices.ID.String()) || !strings.Contains(body, w.session.ID.String()) {
		t.Fatal("list_sessions dropped alice's own or a shared session")
	}
	for _, secret := range []string{"sk-alice-secret", "sk-bob-secret", "pw-secret"} {
		if strings.Contains(body, secret) {
			t.Fatalf("list_sessions leaked %q", secret)
		}
	}
	// Alice's connection is not registered for bob's session.
	w.h.sessionConnectionsMu.RLock()
	n := len(w.h.sessionConnections[bobs.ID])
	w.h.sessionConnectionsMu.RUnlock()
	if n != 0 {
		t.Fatal("list_sessions registered alice's connection for bob's session")
	}
}

func TestRedTeam_WS_KillAndDeleteAllLimitedToOwnSessions(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	bobs := seedOwned(t, w, "bob", nil)
	alices := seedOwned(t, w, "alice", nil)

	// API key: refused while user auth is on.
	apiConn := w.dial(t, "")
	_ = apiConn.WriteJSON(map[string]any{"type": "kill_all_agents"})
	got := w.expect(t, apiConn, func(m map[string]any) bool { return m["type"] == "error" })
	if !isUserRequired(got) {
		t.Fatalf("kill_all_agents from the API key: %v", got)
	}
	_ = apiConn.WriteJSON(map[string]any{"type": "delete_all_sessions"})
	if got := w.expect(t, apiConn, func(m map[string]any) bool { return m["type"] == "error" }); !isUserRequired(got) {
		t.Fatalf("delete_all_sessions from the API key: %v", got)
	}

	conn := w.dial(t, "alice")
	_ = conn.WriteJSON(map[string]any{"type": "kill_all_agents"})
	w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "kill_all_agents_response" })
	if _, err := w.sm.GetSession(bobs.ID); err != nil {
		t.Fatal("alice's kill_all_agents ended bob's session")
	}
	if _, err := w.sm.GetSession(w.session.ID); err != nil {
		t.Fatal("alice's kill_all_agents ended a shared session")
	}
	if _, err := w.sm.GetSession(alices.ID); err == nil {
		t.Fatal("alice's own session is still running")
	}

	_ = conn.WriteJSON(map[string]any{"type": "delete_all_sessions"})
	w.expect(t, conn, func(m map[string]any) bool { return m["type"] == string(MessageTypeAllSessionsDeleted) })
	if _, err := w.sm.Storage.GetSession(bobs.ID); err != nil {
		t.Fatal("alice's delete_all_sessions deleted bob's session")
	}
	if _, err := w.sm.Storage.GetSession(alices.ID); err == nil {
		t.Fatal("alice's own session was not deleted")
	}

	// An admin may end everything.
	w.h.IsAdminUser = func(u string) bool { return u == "root" }
	admin := w.dial(t, "root")
	_ = admin.WriteJSON(map[string]any{"type": "kill_all_agents"})
	w.expect(t, admin, func(m map[string]any) bool { return m["type"] == "kill_all_agents_response" })
	if _, err := w.sm.GetSession(bobs.ID); err == nil {
		t.Fatal("an admin's kill_all_agents left bob's session running")
	}
}

func TestRedTeam_WS_DebugLogsNeedAnAdmin(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	w.h.IsAdminUser = func(u string) bool { return u == "root" }

	apiConn := w.dial(t, "")
	_ = apiConn.WriteJSON(map[string]any{"type": "subscribe_debug_logs"})
	if got := w.expect(t, apiConn, func(m map[string]any) bool { return m["type"] == "error" }); !isUserRequired(got) {
		t.Fatalf("debug logs from the API key: %v", got)
	}

	conn := w.dial(t, "alice")
	_ = conn.WriteJSON(map[string]any{"type": "subscribe_debug_logs"})
	if got := w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "error" }); got["code"] != "admin_required" {
		t.Fatalf("debug logs for a non-admin: %v", got)
	}

	admin := w.dial(t, "root")
	_ = admin.WriteJSON(map[string]any{"type": "subscribe_debug_logs"})
	w.expect(t, admin, func(m map[string]any) bool { return m["type"] == string(MessageTypeDebugLogSubscribed) })
	_ = admin.WriteJSON(map[string]any{"type": "unsubscribe_debug_logs"})
}

func TestRedTeam_WS_SubscribeProjectNeedsOwnerAndKnownFolder(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	dir := t.TempDir()
	bobs := seedOwned(t, w, "bob", func(s *Session) { s.Options.WorkingDirectory = strPtr(dir) })

	apiConn := w.dial(t, "")
	_ = apiConn.WriteJSON(map[string]any{"type": "subscribe_project", "session_id": bobs.ID.String(), "project_id": "p", "working_dir": dir})
	if got := w.expect(t, apiConn, func(m map[string]any) bool { return m["type"] == "error" }); !isUserRequired(got) {
		t.Fatalf("subscribe_project from the API key: %v", got)
	}

	alice := w.dial(t, "alice")
	_ = alice.WriteJSON(map[string]any{"type": "subscribe_project", "session_id": bobs.ID.String(), "project_id": "p", "working_dir": dir})
	w.expect(t, alice, isErrorContaining("do not own"))

	bob := w.dial(t, "bob")
	_ = bob.WriteJSON(map[string]any{"type": "subscribe_project", "session_id": bobs.ID.String(), "project_id": "p", "working_dir": "/etc"})
	w.expect(t, bob, isErrorContaining("not this session's folder"))
}

func TestRedTeam_WS_SignedOutSessionClosesSocket(t *testing.T) {
	w := newWSHarness(t)
	valid := map[string]bool{database.HashToken("tok-1"): true, database.HashToken("tok-2"): true}
	w.h.SessionValidator = func(tok string) (string, error) {
		if valid[database.HashToken(tok)] {
			return "alice", nil
		}
		return "", errTestInvalid
	}
	w.h.SessionTokenHashValid = func(hash string) bool { return valid[hash] }

	signIn := func(tok string) *fastws.Conn {
		conn := w.dialRaw(t)
		_ = conn.WriteJSON(map[string]any{"type": "auth", "token": tok})
		w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "auth_success" })
		return conn
	}

	// Logout / device revoked: the server closes that session's sockets.
	c1 := signIn("tok-1")
	other := signIn("tok-2")
	delete(valid, database.HashToken("tok-1"))
	if n := w.h.CloseConnectionsForSessionToken(database.HashToken("tok-1")); n != 1 {
		t.Fatalf("closed %d connections, want 1", n)
	}
	expectClosed(t, c1, 3*time.Second)
	w.expectAlive(t, other)

	// Revoked without the hook (e.g. password change): the next message closes it.
	delete(valid, database.HashToken("tok-2"))
	_ = other.WriteJSON(map[string]any{"type": "ping"})
	expectClosed(t, other, 3*time.Second)
}

// An API-key connection that signs in with a session token in a later auth
// message is tied to that session like any other signed-in connection.
func TestRedTeam_WS_InBandSessionTokenSocketClosesOnRevoke(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	var revoked atomic.Bool
	hash := database.HashToken("tok-inband")
	w.h.SessionValidator = func(tok string) (string, error) {
		if tok == "tok-inband" && !revoked.Load() {
			return "alice", nil
		}
		return "", errTestInvalid
	}
	w.h.SessionTokenHashValid = func(h string) bool { return h == hash && !revoked.Load() }

	conn := w.dial(t, "") // API key first
	_ = conn.WriteJSON(map[string]any{"type": "auth", "token": "tok-inband"})
	w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "auth_success" })
	// The API key again does not untie it from the session.
	_ = conn.WriteJSON(map[string]any{"type": "auth", "token": testWSAPIKey})
	w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "auth_success" })
	w.expectAlive(t, conn)

	revoked.Store(true)
	if n := w.h.CloseConnectionsForSessionToken(hash); n != 1 {
		t.Fatalf("closed %d connections, want 1", n)
	}
	expectClosed(t, conn, 3*time.Second)
}

// A connection that acts as a user without a recorded session token could
// never be signed out, so it is refused. API-key connections are unaffected.
func TestRedTeam_WS_UserWithoutRecordedTokenIsRefused(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	w.h.SessionTokenHashValid = func(string) bool { return true }

	apiKey := w.dial(t, "")
	w.expectAlive(t, apiKey)

	// The harness marks ?as= connections as a user and records no token.
	conn, _, err := fastws.DefaultDialer.Dial(w.url+"?as=alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.WriteJSON(map[string]any{"type": "toggle_yolo_mode", "session_id": w.session.ID.String(), "enabled": true})
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				t.Fatal("connection still open")
			}
			break
		}
		if msg["type"] != "auth_success" && msg["type"] != "auth_error" {
			t.Fatalf("a user connection without a session token was served: %v", msg)
		}
	}
	w.expectAlive(t, apiKey)
}

type testErr string

func (e testErr) Error() string { return string(e) }

const errTestInvalid = testErr("invalid session")

func TestRedTeam_WS_BulkEndAndDeleteOnlyTouchOwnSessions(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	bobs := seedOwned(t, w, "bob", nil)
	alices := seedOwned(t, w, "alice", nil)
	alices2 := seedOwned(t, w, "alice", nil)

	before, err := w.sm.Storage.GetSession(alices.ID)
	if err != nil {
		t.Fatal(err)
	}

	conn := w.dial(t, "alice")
	_ = conn.WriteJSON(map[string]any{"type": "end_sessions", "session_ids": []string{bobs.ID.String(), alices.ID.String()}})
	got := w.expect(t, conn, func(m map[string]any) bool { return m["type"] == string(MessageTypeSessionsBulkResult) })
	if done, _ := got["session_ids"].([]any); len(done) != 1 || done[0] != alices.ID.String() {
		t.Fatalf("end_sessions done = %v, want only alice's", got["session_ids"])
	}
	if failed, _ := got["failed"].([]any); len(failed) != 1 {
		t.Fatalf("end_sessions failed = %v, want bob's", got["failed"])
	}
	if _, err := w.sm.GetSession(bobs.ID); err != nil {
		t.Fatal("alice's end_sessions ended bob's session")
	}
	after, err := w.sm.Storage.GetSession(alices.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != string(SessionStatusEnded) {
		t.Fatalf("alice's session status = %s, want ended", after.Status)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("ending moved updated_at %v → %v (the sidebar order would change)", before.UpdatedAt, after.UpdatedAt)
	}

	_ = conn.WriteJSON(map[string]any{"type": "delete_sessions", "session_ids": []string{bobs.ID.String(), alices.ID.String(), alices2.ID.String()}})
	got = w.expect(t, conn, func(m map[string]any) bool {
		return m["type"] == string(MessageTypeSessionsBulkResult) && m["action"] == "delete"
	})
	if done, _ := got["session_ids"].([]any); len(done) != 2 {
		t.Fatalf("delete_sessions done = %v, want alice's two", got["session_ids"])
	}
	if _, err := w.sm.Storage.GetSession(bobs.ID); err != nil {
		t.Fatal("alice's delete_sessions deleted bob's session")
	}
	for _, s := range []*AgentSession{alices, alices2} {
		if _, err := w.sm.Storage.GetSession(s.ID); err == nil {
			t.Fatalf("alice's session %s was not deleted", s.ID)
		}
	}
}

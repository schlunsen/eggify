package server

import (
	"net"
	"net/http"
	"testing"
	"time"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/agents"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
	"github.com/schlunsen/eggify/internal/server/routes"
)

// agentWSRevocationRig is the real session middleware in front of the real
// /agent/ws route and agent handler, wired like setupAgentHandler does.
type agentWSRevocationRig struct {
	us   *UserStore
	h    *agents.AgentHandler
	addr string
}

func newAgentWSRevocationRig(t *testing.T) *agentWSRevocationRig {
	t.Helper()
	db := database.NewTestDB(t)
	us := NewInMemoryUserStore()
	us.users["alice"] = &User{Username: "alice", IsAdmin: true}

	h, err := agents.NewAgentHandler(&agents.Config{MaxConcurrentSessions: 10}, db)
	if err != nil {
		t.Fatal(err)
	}
	h.APIKey = "rig-api-key"
	h.SessionValidator = agentWSSessionValidator(us)
	h.SessionTokenHashValid = us.sessionHashSignedIn

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(NewSessionAuthMiddleware(us, true, false).Handler())
	routes.RegisterAgent(app.Group("/api"), app, &handlers.AgentSessionHandler{}, h, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	return &agentWSRevocationRig{us: us, h: h, addr: ln.Addr().String()}
}

// agentWSSignIn describes one way a session token reaches /agent/ws.
type agentWSSignIn struct {
	name string
	// dial returns the URL and headers of the upgrade for token.
	dial func(addr, token string) (string, http.Header)
	// inBand: the token is sent in the auth message, not on the upgrade.
	inBand bool
}

var agentWSSignIns = []agentWSSignIn{
	{name: "cookie", dial: func(addr, token string) (string, http.Header) {
		return "ws://" + addr + "/agent/ws", http.Header{"Cookie": []string{"session_token=" + token}}
	}},
	{name: "bearer header", dial: func(addr, token string) (string, http.Header) {
		return "ws://" + addr + "/agent/ws", http.Header{"Authorization": []string{"Bearer " + token}}
	}},
	{name: "query token", dial: func(addr, token string) (string, http.Header) {
		return "ws://" + addr + "/agent/ws?token=" + token, nil
	}},
	{name: "auth message", inBand: true, dial: func(addr, _ string) (string, http.Header) {
		return "ws://" + addr + "/agent/ws", nil
	}},
}

// open signs a socket in with token the given way and waits for auth_success.
func (r *agentWSRevocationRig) open(t *testing.T, how agentWSSignIn, token string) *fws.Conn {
	t.Helper()
	PutTestSession(r.us, token, &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour)})
	url, header := how.dial(r.addr, token)
	conn, _, err := fws.DefaultDialer.Dial(url, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if how.inBand {
		if err := conn.WriteJSON(map[string]any{"type": "auth", "token": token}); err != nil {
			t.Fatal(err)
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil || msg["type"] != "auth_success" {
		t.Fatalf("not signed in: %v %v", msg, err)
	}
	// The socket acts as the user: a privileged message gets past the
	// user_required check (and fails later, on the unknown session).
	_ = conn.WriteJSON(map[string]any{"type": "toggle_yolo_mode", "session_id": "00000000-0000-0000-0000-000000000000", "enabled": true})
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("no answer to a privileged message: %v", err)
	}
	if msg["code"] == "user_required" {
		t.Fatalf("socket is not a user principal: %v", msg)
	}
	// Drain whatever else that message produced.
	_ = conn.WriteJSON(map[string]any{"type": "ping"})
	for msg["type"] != "pong" {
		msg = nil
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("socket gone before the session ended: %v", err)
		}
	}
	return conn
}

// assertAgentWSClosed checks the server closes conn and answers nothing but
// the sign-out notice to a privileged message sent after the session ended.
func assertAgentWSClosed(t *testing.T, conn *fws.Conn) {
	t.Helper()
	_ = conn.WriteJSON(map[string]any{"type": "toggle_yolo_mode", "session_id": "00000000-0000-0000-0000-000000000000", "enabled": true})
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("/agent/ws connection stayed open after its session ended")
			}
			return
		}
		if msg["type"] != "auth_error" {
			t.Fatalf("/agent/ws still answers after its session ended: %v", msg)
		}
	}
}

// Red-team: a socket signed in with ?token=<session token> (or any other way
// a session token is accepted) used to be recorded without its token, so
// signing out, revoking the device or changing the password left it open and
// acting as the user (permission_response, toggle_yolo_mode, create_session,
// send_prompt).
func TestRedTeam_WS_QueryTokenSocketClosesOnRevoke(t *testing.T) {
	principal.SetEnforced(true)
	t.Cleanup(func() { principal.SetEnforced(false) })
	r := newAgentWSRevocationRig(t)

	for _, how := range agentWSSignIns {
		// Logout / device revoked: the hook closes the session's sockets.
		t.Run(how.name+"/sign-out hook", func(t *testing.T) {
			token := "tok-hook-" + how.name[:4]
			conn := r.open(t, how, token)
			other := r.open(t, how, token+"-other")

			if err := r.us.revokeSessionKey(database.HashToken(token)); err != nil {
				t.Fatal(err)
			}
			if n := r.h.CloseConnectionsForSessionToken(database.HashToken(token)); n != 1 {
				t.Fatalf("closed %d connections, want 1", n)
			}
			assertAgentWSClosed(t, conn)

			// Another session of the same user is untouched.
			_ = other.WriteJSON(map[string]any{"type": "ping"})
			_ = other.SetReadDeadline(time.Now().Add(5 * time.Second))
			var msg map[string]any
			if err := other.ReadJSON(&msg); err != nil || msg["type"] != "pong" {
				t.Fatalf("another session's socket was closed too: %v %v", msg, err)
			}
		})
		// The session ends without the hook (password change, expiry, user
		// removed): the socket's own re-check refuses its next message.
		t.Run(how.name+"/no hook", func(t *testing.T) {
			token := "tok-quiet-" + how.name[:4]
			conn := r.open(t, how, token)
			if err := r.us.revokeSessionKey(database.HashToken(token)); err != nil {
				t.Fatal(err)
			}
			assertAgentWSClosed(t, conn)
		})
	}
}

// The middleware records the token that identified the user wherever it
// arrived, and ?token= only counts on WebSocket upgrades.
func TestSessionMiddleware_RecordsTheIdentifyingTokenHash(t *testing.T) {
	us := NewInMemoryUserStore()
	us.users["alice"] = &User{Username: "alice"}
	PutTestSession(us, "sess-a", &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour)})

	app := fiber.New()
	app.Use(NewSessionAuthMiddleware(us, true, false).Handler())
	app.Get("/api/who", func(c *fiber.Ctx) error {
		name, _ := principal.Username(c)
		return c.SendString(name + "|" + principal.SessionTokenHash(c))
	})
	want := "alice|" + database.HashToken("sess-a")

	for name, set := range map[string]func(*http.Request){
		"cookie": func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "session_token", Value: "sess-a"}) },
		"bearer": func(r *http.Request) { r.Header.Set("Authorization", "Bearer sess-a") },
	} {
		req, _ := http.NewRequest("GET", "/api/who", nil)
		set(req)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body := make([]byte, 256)
		n, _ := resp.Body.Read(body)
		if got := string(body[:n]); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}

	// Not an upgrade: a token in the URL identifies nobody.
	req, _ := http.NewRequest("GET", "/api/who?token=sess-a", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("?token= on a plain request: status %d, want 401", resp.StatusCode)
	}
}

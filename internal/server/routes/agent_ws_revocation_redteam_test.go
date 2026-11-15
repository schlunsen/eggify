package routes

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/agents"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// serveAgentWS serves the /agent/ws route behind a stand-in for the session
// middleware: ?token=tok identifies alice and records the token's hash,
// ?nohash=1 identifies her without recording one.
func serveAgentWS(t *testing.T, valid *atomic.Bool) (*agents.AgentHandler, string) {
	t.Helper()
	h, err := agents.NewAgentHandler(&agents.Config{MaxConcurrentSessions: 10}, database.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	h.APIKey = "route-api-key"
	h.SessionTokenHashValid = func(hash string) bool { return hash == database.HashToken("tok") && valid.Load() }

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(func(c *fiber.Ctx) error {
		if c.Query("token") == "tok" || c.Query("nohash") != "" {
			c.Locals("username", "alice")
			principal.Set(c, principal.Principal{Kind: principal.User, Username: "alice"})
		}
		if c.Query("token") == "tok" {
			principal.SetSessionTokenHash(c, database.HashToken("tok"))
		}
		return c.Next()
	})
	registerAgentWebSocketRoute(app, h, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	return h, "ws://" + ln.Addr().String() + "/agent/ws"
}

func dialAgentWS(t *testing.T, url string) *fws.Conn {
	t.Helper()
	conn, _, err := fws.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	return conn
}

// Red-team: the route ties a socket to the session token the middleware
// identified the user with, also when it came as ?token=, so revoking the
// session closes the socket. It used to look only at the cookie and the
// Bearer header and recorded nothing for ?token=.
func TestRedTeam_WS_QueryTokenUpgradeIsTiedToItsSession(t *testing.T) {
	var valid atomic.Bool
	valid.Store(true)
	h, url := serveAgentWS(t, &valid)

	conn := dialAgentWS(t, url+"?token=tok")
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil || msg["type"] != "auth_success" {
		t.Fatalf("not pre-authenticated: %v %v", msg, err)
	}

	valid.Store(false)
	if n := h.CloseConnectionsForSessionToken(database.HashToken("tok")); n != 1 {
		t.Fatalf("closed %d connections, want 1", n)
	}
	_ = conn.WriteJSON(map[string]any{"type": "list_sessions"})
	for {
		msg = nil
		if err := conn.ReadJSON(&msg); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("/agent/ws connection stayed open after its session ended")
			}
			break
		}
		if msg["type"] != "auth_error" {
			t.Fatalf("/agent/ws still answers after its session ended: %v", msg)
		}
	}
}

// Red-team: a user the route cannot tie to a session token is not signed in
// by the upgrade (the socket could never be signed out again).
func TestRedTeam_WS_UserWithoutSessionTokenIsNotPreAuthenticated(t *testing.T) {
	var valid atomic.Bool
	valid.Store(true)
	_, url := serveAgentWS(t, &valid)

	conn := dialAgentWS(t, url+"?nohash=1")
	_ = conn.WriteJSON(map[string]any{"type": "list_sessions"})
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatal(err)
	}
	if msg["type"] != "error" || msg["message"] != "authentication required" {
		t.Fatalf("socket was served without a session token: %v", msg)
	}
}

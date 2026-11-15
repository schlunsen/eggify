package agents

import (
	"fmt"
	"net"
	"testing"

	fastws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	fiberws "github.com/gofiber/websocket/v2"
	"github.com/google/uuid"

	"github.com/schlunsen/eggify/internal/remoteaccess"
)

// newTunnelWSHarness serves HandleFiberWebSocket twice: /local and /tunnel,
// where /tunnel is marked the way the server's tunnelPathGuard marks every
// request that arrived through remote access (the Local carries over to the
// upgraded connection).
func newTunnelWSHarness(t *testing.T) (h *AgentHandler, base string) {
	t.Helper()
	h = &AgentHandler{
		Config:             &Config{MaxConcurrentSessions: 10},
		APIKey:             testWSAPIKey,
		connMu:             map[*fiberws.Conn]*connWriter{},
		sessionConnections: map[uuid.UUID][]*fiberws.Conn{},
		sessionBuffers:     map[uuid.UUID]*MessageBuffer{},
		connUsers:          map[*fiberws.Conn]*string{},
		SessionValidator: func(tok string) (string, error) {
			if tok == "sess-ok" {
				return "alice", nil
			}
			return "", fmt.Errorf("invalid session")
		},
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use("/tunnel", func(c *fiber.Ctx) error {
		c.Locals(remoteaccess.ViaTunnelLocal, true)
		return c.Next()
	})
	handler := func(c *fiber.Ctx) error { return fiberws.New(h.HandleFiberWebSocket)(c) }
	app.Get("/local", handler)
	app.Get("/tunnel", handler)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	return h, "ws://" + ln.Addr().String()
}

func tunnelAuth(t *testing.T, url, token string) (*fastws.Conn, map[string]any) {
	t.Helper()
	conn, _, err := fastws.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.WriteJSON(map[string]any{"type": "auth", "token": token}); err != nil {
		t.Fatal(err)
	}
	w := &wsHarness{}
	return conn, w.expect(t, conn, func(m map[string]any) bool {
		return m["type"] == "auth_success" || m["type"] == "auth_error" || m["type"] == "error"
	})
}

// Red-team: an agent reads the API key and the public tunnel URL and sends
// them out; the attacker then authenticates /agent/ws over remote access to
// send prompts to YOLO sessions. The API key must be refused there.
func TestRedTeam_RemoteAccess_APIKeyRefusedOnAgentSocketOverTunnel(t *testing.T) {
	_, base := newTunnelWSHarness(t)

	_, m := tunnelAuth(t, base+"/tunnel", testWSAPIKey)
	if m["type"] != "auth_error" || m["error"] != remoteaccess.APIKeyRefusedMessage {
		t.Fatalf("API key over the tunnel on /agent/ws: %v", m)
	}

	// Locally the API key still works.
	if _, m := tunnelAuth(t, base+"/local", testWSAPIKey); m["type"] != "auth_success" {
		t.Fatalf("API key locally on /agent/ws: %v", m)
	}
	// A signed-in user's session token works over the tunnel.
	conn, m := tunnelAuth(t, base+"/tunnel", "sess-ok")
	if m["type"] != "auth_success" {
		t.Fatalf("session token over the tunnel on /agent/ws: %v", m)
	}
	// Re-authenticating that connection with the API key is refused but
	// keeps the signed-in connection.
	_ = conn.WriteJSON(map[string]any{"type": "auth", "token": testWSAPIKey})
	w := &wsHarness{}
	if m := w.expect(t, conn, func(m map[string]any) bool {
		return m["type"] == "error" || m["type"] == "auth_error" || m["type"] == "auth_success"
	}); m["type"] != "error" {
		t.Fatalf("API key re-auth over the tunnel: %v", m)
	}
	_ = conn.WriteJSON(map[string]any{"type": "ping"})
	w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "pong" })
}

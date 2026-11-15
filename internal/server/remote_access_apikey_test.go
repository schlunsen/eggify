package server

// Red-team: the API key over remote access. The key is a file every local
// process can read, agents included. A prompt-injected agent can read it and
// the public URL (GET /api/tunnel/status) and send both out; the API key must
// therefore never work through the tunnel. Only a signed-in user's session
// does. Locally nothing changes.

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	fastws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"

	"github.com/schlunsen/eggify/internal/remoteaccess"
	"github.com/schlunsen/eggify/internal/server/routes"
	ws "github.com/schlunsen/eggify/internal/websocket"
)

const rtTunnelAPIKey = "the-api-key"

// newTunnelAuthTestServer is the tunnel plumbing of newRemoteAccessTestServer
// plus the session auth middleware, a REST read and the /ws hub. requireLogin
// is off so the /ws upgrade reaches the hub, which checks the token itself.
func newTunnelAuthTestServer(t *testing.T) (s *Server, tunnelAddr, localAddr string) {
	t.Helper()
	s, prov, mainLn := newRemoteAccessTestServer(t)

	us := NewInMemoryUserStore()
	us.apiKey = rtTunnelAPIKey
	us.users["alice"] = &User{Username: "alice"}
	PutTestSession(us, "sess-ok", &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour)})
	s.userStore = us
	s.sessionAuthMiddleware = NewSessionAuthMiddleware(us, true, false)
	s.app.Use(s.sessionAuthMiddleware.Handler())
	s.app.Get("/api/data", func(c *fiber.Ctx) error { return c.SendString("secret transcripts") })

	hub := ws.NewHub()
	go hub.Run()
	t.Cleanup(func() { _ = hub.Shutdown() })
	hub.SetAPIKey(rtTunnelAPIKey)
	hub.SetSessionValidator(func(tok string) bool { return us.userForSessionToken(tok) != nil })
	routes.RegisterWebSocket(s.app, hub, nil)

	if err := s.remoteAccess.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.remoteAccess.Stop() })
	waitUntil(t, "tunnel connected", func() bool { return s.remoteAccess.Status().Status == remoteaccess.StateConnected })
	return s, prov.addr(), mainLn.Addr().String()
}

func rtGet(t *testing.T, addr, path, bearer string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://"+addr+path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("GET %s%s: %v", addr, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// rtHubDial connects to /ws and returns the first frame.
func rtHubDial(t *testing.T, addr, query string, header http.Header) map[string]any {
	t.Helper()
	conn, resp, err := fastws.DefaultDialer.Dial("ws://"+addr+"/ws"+query, header)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		return map[string]any{"dial_error": err.Error(), "status": code}
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var m map[string]any
	if err := conn.ReadJSON(&m); err != nil {
		t.Fatalf("read first /ws frame: %v", err)
	}
	return m
}

func TestRedTeam_RemoteAccess_APIKeyRefusedOverTunnelREST(t *testing.T) {
	_, tunnel, local := newTunnelAuthTestServer(t)

	code, body := rtGet(t, tunnel, "/api/data", rtTunnelAPIKey)
	if code != fiber.StatusUnauthorized || strings.Contains(body, "secret") {
		t.Fatalf("API key over the tunnel = %d %q, want 401", code, body)
	}
	if !strings.Contains(body, "not accepted over remote access") {
		t.Errorf("401 body should explain why: %q", body)
	}
	// ?token= on an upgrade request is refused the same way.
	req, _ := http.NewRequest("GET", "http://"+tunnel+"/api/data?token="+rtTunnelAPIKey, nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	if resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("?token=<api key> upgrade over the tunnel = %d, want 401", resp.StatusCode)
		}
	}

	// Locally the API key still works.
	if code, body := rtGet(t, local, "/api/data", rtTunnelAPIKey); code != 200 || body != "secret transcripts" {
		t.Fatalf("API key locally = %d %q, want 200", code, body)
	}
	// A signed-in user's session works over the tunnel.
	if code, body := rtGet(t, tunnel, "/api/data", "sess-ok"); code != 200 || body != "secret transcripts" {
		t.Fatalf("session token over the tunnel = %d %q, want 200", code, body)
	}
	// A wrong token is still just unauthorized.
	if code, _ := rtGet(t, tunnel, "/api/data", "nope"); code != fiber.StatusUnauthorized {
		t.Fatalf("bad token over the tunnel = %d", code)
	}
}

func TestRedTeam_RemoteAccess_APIKeyRefusedOnHubSocketOverTunnel(t *testing.T) {
	_, tunnel, local := newTunnelAuthTestServer(t)

	m := rtHubDial(t, tunnel, "?token="+rtTunnelAPIKey, nil)
	if m["type"] == "connected" {
		t.Fatalf("API key over the tunnel joined /ws: %v", m)
	}
	if m["error"] != "Unauthorized" || !strings.Contains(m["message"].(string), "remote access") {
		t.Fatalf("API key over the tunnel on /ws: %v", m)
	}

	// Locally the API key still works.
	if m := rtHubDial(t, local, "?token="+rtTunnelAPIKey, nil); m["type"] != "connected" {
		t.Fatalf("API key locally on /ws: %v", m)
	}
	// A signed-in user's session cookie works over the tunnel.
	h := http.Header{}
	h.Set("Cookie", "session_token=sess-ok")
	if m := rtHubDial(t, tunnel, "", h); m["type"] != "connected" {
		t.Fatalf("session cookie over the tunnel on /ws: %v", m)
	}
}

// With user auth off (API-key-only mode) remote access cannot start, but the
// API-key middleware refuses tunnel requests anyway.
func TestRedTeam_RemoteAccess_APIKeyOnlyModeRefusesTunnel(t *testing.T) {
	s, prov, mainLn := newRemoteAccessTestServer(t)
	s.authMiddleware = NewAuthMiddleware(rtTunnelAPIKey, true)
	s.app.Use(s.authMiddleware.Handler())
	s.app.Get("/api/data", func(c *fiber.Ctx) error { return c.SendString("secret transcripts") })
	if err := s.remoteAccess.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.remoteAccess.Stop()
	waitUntil(t, "tunnel connected", func() bool { return s.remoteAccess.Status().Status == remoteaccess.StateConnected })

	if code, _ := rtGet(t, prov.addr(), "/api/data", rtTunnelAPIKey); code != fiber.StatusUnauthorized {
		t.Fatalf("API key over the tunnel (API-key-only mode) = %d, want 401", code)
	}
	if code, _ := rtGet(t, mainLn.Addr().String(), "/api/data", rtTunnelAPIKey); code != 200 {
		t.Fatalf("API key locally (API-key-only mode) = %d, want 200", code)
	}
}

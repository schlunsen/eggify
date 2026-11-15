package routes

import (
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/principal"
	"github.com/schlunsen/eggify/internal/server/terminal"
)

// openRevocableTerminal serves the terminal route with a validator that takes
// "tok" while valid is true, and returns a connection attached to a running
// shell.
func openRevocableTerminal(t *testing.T, valid *atomic.Bool) (*terminal.TerminalHandler, *fws.Conn) {
	t.Helper()
	cfg := terminal.DefaultConfig()
	cfg.CommandHistoryEnabled = false
	mgr := terminal.NewPTYManager(cfg)
	t.Cleanup(func() { _ = mgr.Shutdown() })
	th := terminal.NewTerminalHandler(mgr, nopTerminalStore{})
	term, err := mgr.SpawnTerminal("revoke-session", t.TempDir(), "/bin/sh")
	if err != nil {
		t.Skipf("cannot spawn a PTY here: %v", err)
	}
	term.SetOwner("alice")

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(func(c *fiber.Ctx) error {
		principal.Set(c, principal.Principal{Kind: principal.User, Username: "alice"})
		return c.Next()
	})
	registerTerminalWebSocketRoute(app, th, nil, func(tok string) bool { return tok == "tok" && valid.Load() }, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })

	h := http.Header{"Origin": []string{"http://" + ln.Addr().String()}}
	url := "ws://" + ln.Addr().String() + "/api/agent/sessions/revoke-session/terminal/ws?token=tok&terminal_id=" + term.ID
	conn, _, err := fws.DefaultDialer.Dial(url, h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var ready terminal.WebSocketMessage
	if err := conn.ReadJSON(&ready); err != nil || ready.Type != "terminal.ready" {
		t.Fatalf("terminal not ready: %+v %v", ready, err)
	}
	return th, conn
}

// assertTerminalClosed checks that conn is closed and never ran input sent
// after the session ended.
func assertTerminalClosed(t *testing.T, conn *fws.Conn) {
	t.Helper()
	_ = conn.WriteJSON(terminal.WebSocketMessage{Type: "terminal.input", Data: "printf 'EGGIFY_%s\\n' AFTER_REVOKE\n"})
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var msg terminal.WebSocketMessage
		if err := conn.ReadJSON(&msg); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("terminal connection stayed open after its session ended")
			}
			return
		}
		if s, ok := msg.Data.(string); ok && strings.Contains(s, "EGGIFY_AFTER_REVOKE") {
			t.Fatal("terminal ran input after its session was signed out")
		}
	}
}

// Red-team: Alice signs a device out while that device has a terminal open.
// The open shell must close with it, not keep taking commands.
func TestRedTeam_TerminalClosedWhenSessionRevoked(t *testing.T) {
	var valid atomic.Bool
	valid.Store(true)
	th, conn := openRevocableTerminal(t, &valid)

	valid.Store(false)
	if n := th.CloseConnectionsForSessionToken(database.HashToken("tok")); n != 1 {
		t.Fatalf("closed %d terminal connections, want 1", n)
	}
	assertTerminalClosed(t, conn)
}

// Red-team: the session stops being valid some other way (password changed,
// user deleted or demoted, expired). The watchdog closes the shell.
func TestRedTeam_TerminalClosedWhenCredentialInvalid(t *testing.T) {
	old := terminalCredentialCheckInterval
	terminalCredentialCheckInterval = 50 * time.Millisecond
	t.Cleanup(func() { terminalCredentialCheckInterval = old })

	var valid atomic.Bool
	valid.Store(true)
	_, conn := openRevocableTerminal(t, &valid)

	valid.Store(false)
	time.Sleep(200 * time.Millisecond)
	assertTerminalClosed(t, conn)
}

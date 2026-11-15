package routes

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/server/principal"
	"github.com/schlunsen/eggify/internal/server/terminal"
)

// nopTerminalStore satisfies terminal.TerminalSessionStore without a database.
type nopTerminalStore struct{}

func (nopTerminalStore) SaveTerminalSession(*terminal.TerminalSession) error { return nil }
func (nopTerminalStore) GetTerminalSession(string) (*terminal.TerminalSession, error) {
	return nil, terminal.ErrTerminalNotFound
}
func (nopTerminalStore) ListTerminalsByAgent(string) ([]*terminal.TerminalSession, error) {
	return nil, nil
}
func (nopTerminalStore) GetActiveTerminalsByAgent(string) ([]*terminal.TerminalSession, error) {
	return nil, nil
}
func (nopTerminalStore) UpdateTerminalActivity(string) error             { return nil }
func (nopTerminalStore) UpdateTerminalEnd(string, *int) error            { return nil }
func (nopTerminalStore) RecordCommand(string, string) error              { return nil }
func (nopTerminalStore) GetCommandHistory(string, int) ([]string, error) { return nil, nil }
func (nopTerminalStore) DeleteTerminalSession(string) error              { return nil }
func (nopTerminalStore) GetStats() (terminal.Stats, error)               { return terminal.Stats{}, nil }

// Red-team: Bob, signed in, opens the terminal WebSocket of a session he may
// see with ?terminal_id= of Alice's running shell. Attaching would take her
// PTY over (her connection is closed and his input goes to her shell).
func TestRedTeam_TerminalWebSocketTakeoverRefused(t *testing.T) {
	mgr := terminal.NewPTYManager(terminal.DefaultConfig())
	t.Cleanup(func() { _ = mgr.Shutdown() })
	th := terminal.NewTerminalHandler(mgr, nopTerminalStore{})

	sessionID := uuid.New().String()
	term, err := mgr.SpawnTerminal(sessionID, t.TempDir(), "")
	if err != nil {
		t.Skipf("cannot spawn a PTY here: %v", err)
	}
	term.SetOwner("alice")

	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		if u := c.Get("X-Test-User"); u != "" {
			principal.Set(c, principal.Principal{Kind: principal.User, Username: u})
		}
		return c.Next()
	})
	registerTerminalWebSocketRoute(app, th, nil, func(string) bool { return true }, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })

	dial := func(user, session string) (string, error) {
		h := http.Header{}
		h.Set("Origin", "http://"+ln.Addr().String())
		h.Set("X-Test-User", user)
		url := "ws://" + ln.Addr().String() + "/api/agent/sessions/" + session + "/terminal/ws?terminal_id=" + term.ID
		conn, _, err := fws.DefaultDialer.Dial(url, h)
		if err != nil {
			return "", err
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var msg terminal.WebSocketMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return "", err
		}
		return msg.Type + " " + msg.Error, nil
	}

	got, err := dial("bob", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "terminal.error") {
		t.Fatalf("bob attaching to alice's terminal got %q, want terminal.error", got)
	}
	// Through another session id, even as the owner.
	if got, err := dial("alice", uuid.New().String()); err != nil || !strings.HasPrefix(got, "terminal.error") {
		t.Fatalf("attaching via another session got %q (%v), want terminal.error", got, err)
	}
	if got, err := dial("alice", sessionID); err != nil || got != "terminal.ready " {
		t.Fatalf("owner attaching got %q (%v), want terminal.ready", got, err)
	}
}

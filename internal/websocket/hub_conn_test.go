package websocket

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/schlunsen/eggify/internal/database"
)

// startHubServer runs a real Fiber server with the hub's handler and returns
// the ws:// URL. Everything is torn down when the test ends.
func startHubServer(t *testing.T) (*Hub, string) {
	t.Helper()
	hub := NewHub()
	go hub.Run()

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/ws", websocket.New(hub.HandleWebSocket()))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = app.Listener(ln) }()

	t.Cleanup(func() {
		hub.Shutdown()
		_ = app.ShutdownWithTimeout(2 * time.Second)
	})
	return hub, "ws://" + ln.Addr().String() + "/ws"
}

func dialHub(t *testing.T, url string) *fws.Conn {
	t.Helper()
	conn, _, err := fws.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	// Consume the welcome frame.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, msg, err := conn.ReadMessage(); err != nil || !bytes.Contains(msg, []byte(`"connected"`)) {
		t.Fatalf("welcome: %q, %v", msg, err)
	}
	return conn
}

func waitForClients(t *testing.T, hub *Hub, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for hub.ClientCount() != n {
		if time.Now().After(deadline) {
			t.Fatalf("ClientCount = %d, want %d", hub.ClientCount(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// broadcastUntilReceived broadcasts marker messages until conn receives one,
// failing if that does not happen within the timeout. Proves Run is alive.
func broadcastUntilReceived(t *testing.T, hub *Hub, conn *fws.Conn, marker string) {
	t.Helper()
	got := make(chan struct{})
	go func() {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if string(msg) == marker {
				close(got)
				return
			}
		}
	}()
	timeout := time.After(5 * time.Second)
	for {
		hub.Broadcast([]byte(marker))
		select {
		case <-got:
			return
		case <-timeout:
			t.Fatalf("client did not receive %q: hub is stuck", marker)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// A client whose connection dies must not wedge the hub: before the fix, a
// failed write in Run sent on the unbuffered unregister channel that only Run
// reads, so Run blocked forever and later registers/broadcasts hung.
func TestHub_BrokenClientDoesNotDeadlock(t *testing.T) {
	hub, url := startHubServer(t)

	broken := dialHub(t, url)
	waitForClients(t, hub, 1)

	// Kill the TCP connection without a close handshake, then keep
	// broadcasting so the server's writes to it fail.
	broken.NetConn().Close()
	for i := 0; i < 50; i++ {
		hub.Broadcast([]byte(fmt.Sprintf("tick-%d", i)))
		time.Sleep(2 * time.Millisecond)
	}

	// Registration and broadcast must still work.
	registered := make(chan *fws.Conn, 1)
	go func() {
		conn, _, err := fws.DefaultDialer.Dial(url, nil)
		if err == nil {
			registered <- conn
		}
	}()
	var fresh *fws.Conn
	select {
	case fresh = <-registered:
		t.Cleanup(func() { fresh.Close() })
	case <-time.After(5 * time.Second):
		t.Fatal("new client could not connect after a broken client")
	}
	broadcastUntilReceived(t, hub, fresh, "after-broken")
	waitForClients(t, hub, 1)
}

// A client that stops reading must not stall delivery to everyone else.
func TestHub_SlowClientDoesNotBlockOthers(t *testing.T) {
	hub, url := startHubServer(t)

	_ = dialHub(t, url) // never reads again
	fast := dialHub(t, url)
	waitForClients(t, hub, 2)

	// Far more than the OS socket buffers hold, so writes to the stalled
	// client block; fewer messages than the per-client queue.
	payload := bytes.Repeat([]byte("x"), 256*1024)
	const n = 100

	received := make(chan int, 1)
	go func() {
		count := 0
		_ = fast.SetReadDeadline(time.Now().Add(10 * time.Second))
		for count < n {
			if _, _, err := fast.ReadMessage(); err != nil {
				break
			}
			count++
		}
		received <- count
	}()

	for i := 0; i < n; i++ {
		hub.Broadcast(payload)
	}

	select {
	case got := <-received:
		if got != n {
			t.Fatalf("fast client received %d/%d messages", got, n)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("fast client starved by a stalled client")
	}

	// A newcomer still registers and receives.
	newcomer := dialHub(t, url)
	broadcastUntilReceived(t, hub, newcomer, "newcomer")
}

func TestClient_CloseIsIdempotent(t *testing.T) {
	cl := newClient(nil)
	cl.close()
	cl.close()
	select {
	case <-cl.quit:
	default:
		t.Fatal("quit not closed")
	}
}

func TestHub_UnregisterUnknownClientIsSafe(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Shutdown()

	cl := newClient(nil)
	hub.register <- cl
	hub.unregister <- cl
	hub.unregister <- cl // already removed
	if got := hub.ClientCount(); got != 0 {
		t.Fatalf("ClientCount = %d, want 0", got)
	}
}

func TestHub_OverflowingClientIsDropped(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Shutdown()

	cl := newClient(nil) // no writer: its queue only fills up
	hub.register <- cl
	for i := 0; i < clientSendBuffer+1; i++ {
		hub.broadcast <- outbound{msg: []byte("m")}
	}
	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("overflowing client was not dropped")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-cl.quit:
	default:
		t.Fatal("dropped client was not closed")
	}
}

// Red-team: signing out (or revoking a device) closes the analytics sockets
// opened with that session; they used to be checked once, at connect, and
// kept receiving every session's events afterwards.
func TestRedTeam_Hub_SessionSocketClosesOnRevoke(t *testing.T) {
	hub, url := startHubServer(t)
	hub.SetAPIKey("k3y")
	hub.SetSessionValidator(func(tok string) bool { return tok == "sess-a" || tok == "sess-b" })

	cookie := map[string][]string{"Cookie": {"session_token=sess-a"}}
	dial := func(u string, h map[string][]string) *fws.Conn {
		t.Helper()
		conn, _, err := fws.DefaultDialer.Dial(u, h)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	signedOut := map[string]*fws.Conn{
		"?token=": dial(url+"?token=sess-a", nil),
		"cookie":  dial(url, cookie),
		// The dashboard may send the API key next to the cookie.
		"cookie + API key": dial(url+"?token=k3y", cookie),
	}
	otherSession := dialHub(t, url+"?token=sess-b")
	apiKeyOnly := dialHub(t, url+"?token=k3y")
	waitForClients(t, hub, 5)

	if n := hub.CloseConnectionsForSessionToken(database.HashToken("sess-a")); n != 3 {
		t.Fatalf("closed %d connections, want 3", n)
	}
	for name, conn := range signedOut {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					t.Fatalf("%s: connection stayed open after its session ended", name)
				}
				break
			}
		}
	}
	waitForClients(t, hub, 2)
	broadcastUntilReceived(t, hub, otherSession, "still-here")
	broadcastUntilReceived(t, hub, apiKeyOnly, "still-here-too")
}

package terminal

import (
	"crypto/subtle"
	"log"
	"sync"
	"time"

	"github.com/gofiber/websocket/v2"
)

// trackedSocket is a terminal connection opened with a session token.
// gofiber pools *websocket.Conn and reuses it once the handler returns, so a
// connection is only touched from outside its handler while closed is false,
// under mu.
type trackedSocket struct {
	tokenHash string
	mu        sync.Mutex
	closed    bool
}

// TrackSessionSocket remembers that c was opened with the session token whose
// hash is tokenHash, so CloseConnectionsForSessionToken can close it when that
// session is signed out. Call the returned func before the handler returns;
// after it, c is never touched again.
func (h *TerminalHandler) TrackSessionSocket(tokenHash string, c *websocket.Conn) (untrack func()) {
	if h == nil || tokenHash == "" || c == nil {
		return func() {}
	}
	ts := &trackedSocket{tokenHash: tokenHash}
	h.socketsMu.Lock()
	if h.tokenSockets == nil {
		h.tokenSockets = make(map[*websocket.Conn]*trackedSocket)
	}
	h.tokenSockets[c] = ts
	h.socketsMu.Unlock()
	return func() {
		h.socketsMu.Lock()
		delete(h.tokenSockets, c)
		h.socketsMu.Unlock()
		ts.mu.Lock()
		ts.closed = true
		h.kickedConns.Delete(c)
		ts.mu.Unlock()
	}
}

// CloseConnectionsForSessionToken closes every terminal WebSocket opened with
// the session token whose hash is tokenHash (sign-out, device revoked). The
// PTYs keep running; reattaching needs a valid session. It returns how many
// connections were closed.
func (h *TerminalHandler) CloseConnectionsForSessionToken(tokenHash string) int {
	if h == nil || tokenHash == "" {
		return 0
	}
	type match struct {
		c  *websocket.Conn
		ts *trackedSocket
	}
	var matches []match
	h.socketsMu.Lock()
	for c, ts := range h.tokenSockets {
		if subtle.ConstantTimeCompare([]byte(ts.tokenHash), []byte(tokenHash)) == 1 {
			matches = append(matches, match{c, ts})
		}
	}
	h.socketsMu.Unlock()
	n := 0
	for _, m := range matches {
		if h.closeSignedOut(m.c, m.ts) {
			n++
		}
	}
	return n
}

// CloseSignedOut ends a tracked terminal connection whose session is no
// longer valid. Untracked or finished connections are left alone.
func (h *TerminalHandler) CloseSignedOut(c *websocket.Conn) {
	if h == nil || c == nil {
		return
	}
	h.socketsMu.Lock()
	ts := h.tokenSockets[c]
	h.socketsMu.Unlock()
	if ts != nil {
		h.closeSignedOut(c, ts)
	}
}

// closeSignedOut runs outside the connection's read loop. fasthttp's hijacked
// connection ignores Close until the handler returns, so the connection is
// marked as kicked (the loop drops anything it still reads) and its read
// deadline is moved to now, which unblocks the pending read.
func (h *TerminalHandler) closeSignedOut(c *websocket.Conn, ts *trackedSocket) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.closed {
		return false
	}
	ts.closed = true
	log.Printf("closing terminal WebSocket %s: its session was signed out", c.RemoteAddr())
	h.kickedConns.Store(c, struct{}{})
	_ = c.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "session signed out"),
		time.Now().Add(time.Second))
	_ = c.SetReadDeadline(time.Now())
	_ = c.Close()
	return true
}

// connKicked reports whether closeSignedOut was called for c.
func (h *TerminalHandler) connKicked(c *websocket.Conn) bool {
	_, kicked := h.kickedConns.Load(c)
	return kicked
}

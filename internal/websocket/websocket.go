// Package websocket provides real-time communication for the analytics dashboard.
// It implements a WebSocket hub that manages client connections and broadcasts
// updates when conversation data changes.
package websocket

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/remoteaccess"
)

const (
	// writeWait bounds a single write to a client so a stalled peer can never
	// pin its writer goroutine indefinitely.
	writeWait = 10 * time.Second

	// clientSendBuffer is the number of messages queued per client. A client
	// that falls this far behind is dropped instead of slowing everyone else.
	clientSendBuffer = 256
)

// client is one connected dashboard. All writes to conn after registration
// happen on the client's own writer goroutine (writePump), so the hub never
// blocks on a slow or broken peer.
type client struct {
	conn *websocket.Conn
	send chan []byte
	// sessionHash is the hash of the session token the client presented, or
	// "" for an API-key-only client. Set before the client is registered.
	sessionHash string
	// username is the signed-in user behind the connection, "" for an
	// API-key-only client. It scopes which events the client receives.
	username string
	// token is the session token the client presented (kept only to
	// re-validate it periodically); never sent anywhere.
	token string

	closeOnce sync.Once
	quit      chan struct{} // closed by close(); stops writePump
	done      chan struct{} // closed when writePump has returned
}

func newClient(conn *websocket.Conn) *client {
	return &client{
		conn: conn,
		send: make(chan []byte, clientSendBuffer),
		quit: make(chan struct{}),
		done: make(chan struct{}),
	}
}

// close stops the writer and closes the connection. It is safe to call many
// times and from any goroutine. Closing the conn also unblocks the read loop
// in HandleWebSocket, which then unregisters the client. fasthttp's hijacked
// connection ignores Close until the handler returns, so the read deadline is
// moved to now as well, which ends a pending read. The handler's own close()
// runs before it returns (and waits for a concurrent one), so the conn is
// never touched after Fiber recycles it.
func (cl *client) close() {
	cl.closeOnce.Do(func() {
		close(cl.quit)
		if cl.conn != nil {
			_ = cl.conn.SetReadDeadline(time.Now())
			cl.conn.Close()
		}
	})
}

// enqueue queues a message without blocking. It returns false when the
// client's buffer is full.
func (cl *client) enqueue(message []byte) bool {
	select {
	case cl.send <- message:
		return true
	default:
		return false
	}
}

// writePump writes queued messages to the connection until the client is
// closed or a write fails.
func (cl *client) writePump() {
	defer close(cl.done)
	defer recoverConn("analytics WebSocket writer", cl.conn)
	for {
		select {
		case <-cl.quit:
			return
		case message := <-cl.send:
			_ = cl.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := cl.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				cl.close()
				return
			}
		}
	}
}

// Hub manages WebSocket connections and provides real-time updates to connected clients.
// It is safe for concurrent use and supports graceful shutdown via context cancellation.
//
// Run never writes to a connection itself: broadcasts are queued on each
// client's buffered send channel and written by that client's goroutine, and a
// client whose buffer overflows is dropped. Run therefore never blocks on a
// peer and never sends on its own channels.
type Hub struct {
	clients     map[*client]bool
	broadcast   chan outbound
	register    chan *client
	unregister  chan *client
	mutex       sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	apiKey      string // API key for authentication (optional)
	authEnabled bool   // Whether authentication is required
	// sessionValid reports whether a token is a signed-in user's session
	// (optional). Over the public tunnel only such a session is accepted.
	sessionValid func(token string) bool
	// identify maps a session token to its user (optional); with policy it
	// scopes events to the connections that may see them.
	identify func(token string) (username string, ok bool)
	policy   *AccessPolicy
	// recheckEvery is how often a user socket's session is re-validated.
	recheckEvery time.Duration
}

// AccessPolicy decides who receives which analytics events.
type AccessPolicy struct {
	// Enforced reports whether user auth is on. When it is off every client
	// (the API key included) receives every event.
	Enforced func() bool
	// IsAdmin reports whether username is an admin (or the only account).
	IsAdmin func(username string) bool
	// CanSee reports whether username may see the agent session.
	CanSee func(username, sessionID string) bool
}

// outbound is a queued message with its audience: scoped messages go only to
// the users in allowed (nil allowed with scoped set means nobody).
type outbound struct {
	msg     []byte
	scoped  bool
	allowed map[string]bool
}

// SetAccessPolicy turns on per-user event scoping. identify resolves a
// session token to its user.
func (h *Hub) SetAccessPolicy(identify func(token string) (string, bool), policy AccessPolicy) {
	h.identify = identify
	h.policy = &policy
}

// NewHub creates a new WebSocket hub with context support for graceful shutdown.
func NewHub() *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	return &Hub{
		clients:     make(map[*client]bool),
		broadcast:   make(chan outbound, 256),
		register:    make(chan *client),
		unregister:  make(chan *client),
		ctx:         ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
		authEnabled: false, // Auth is disabled by default

		recheckEvery: 5 * time.Second,
	}
}

// SetAPIKey sets the API key for WebSocket authentication
func (h *Hub) SetAPIKey(apiKey string) {
	h.apiKey = apiKey
	h.authEnabled = apiKey != ""
}

// SetSessionValidator lets the hub accept a signed-in user's session token
// (cookie or ?token=) as well as the API key. Over the public tunnel it is
// the only credential accepted.
func (h *Hub) SetSessionValidator(valid func(token string) bool) {
	h.sessionValid = valid
}

// authResult is the outcome of authorize.
type authResult int

const (
	authOK authResult = iota
	authInvalid
	// authAPIKeyOverTunnel: the API key, presented through the public tunnel.
	authAPIKeyOverTunnel
)

// authorize decides whether a connection presenting tokens may join.
// Locally the API key works as before; a valid session token works too.
// Through the tunnel (viaTunnel) the API key is refused: every local
// process, agents included, can read it, so it must not grant access from
// the public internet.
func (h *Hub) authorize(tokens []string, viaTunnel bool) authResult {
	sawAPIKey := false
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		if h.apiKey != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(h.apiKey)) == 1 {
			if !viaTunnel {
				return authOK
			}
			sawAPIKey = true
			continue
		}
		if h.sessionValid != nil && h.sessionValid(tok) {
			return authOK
		}
	}
	if sawAPIKey {
		return authAPIKeyOverTunnel
	}
	return authInvalid
}

// presentedSessionHash returns the hash of the first of tokens that is a
// signed-in user's session token, or "". A client that also sent the API key
// is still tied to its session, so signing out closes it.
func (h *Hub) presentedSessionHash(tokens []string) string {
	hash, _ := h.presentedSession(tokens)
	return hash
}

// presentedSession is presentedSessionHash that also returns the token.
func (h *Hub) presentedSession(tokens []string) (hash, token string) {
	if h.sessionValid == nil {
		return "", ""
	}
	for _, tok := range tokens {
		if tok == "" || (h.apiKey != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(h.apiKey)) == 1) {
			continue
		}
		if h.sessionValid(tok) {
			return database.HashToken(tok), tok
		}
	}
	return "", ""
}

// CloseConnectionsForSessionToken closes every client that connected with the
// session token whose hash (database.HashToken) is tokenHash: sign-out or a
// revoked device. It returns how many were closed.
func (h *Hub) CloseConnectionsForSessionToken(tokenHash string) int {
	if h == nil || tokenHash == "" {
		return 0
	}
	h.mutex.RLock()
	var matches []*client
	for cl := range h.clients {
		if subtle.ConstantTimeCompare([]byte(cl.sessionHash), []byte(tokenHash)) == 1 {
			matches = append(matches, cl)
		}
	}
	h.mutex.RUnlock()
	for _, cl := range matches {
		// Ends the client's read loop, which unregisters it.
		cl.close()
	}
	return len(matches)
}

// Run starts the hub's main loop and blocks until Shutdown is called.
// It handles client registration, unregistration, and message broadcasting.
func (h *Hub) Run() {
	defer close(h.done)

	for {
		select {
		case <-h.ctx.Done():
			// Graceful shutdown: close all clients
			h.mutex.Lock()
			for cl := range h.clients {
				cl.close()
			}
			h.clients = make(map[*client]bool)
			h.mutex.Unlock()
			return

		case cl := <-h.register:
			h.mutex.Lock()
			h.clients[cl] = true
			h.mutex.Unlock()

		case cl := <-h.unregister:
			// Unregistering a client that was already dropped is a no-op.
			h.mutex.Lock()
			delete(h.clients, cl)
			h.mutex.Unlock()
			cl.close()

		case ob := <-h.broadcast:
			h.mutex.Lock()
			for cl := range h.clients {
				if ob.scoped && !ob.allowed[cl.username] {
					continue
				}
				if !cl.enqueue(ob.msg) {
					// Too slow (or its writer already failed): drop it.
					delete(h.clients, cl)
					cl.close()
				}
			}
			h.mutex.Unlock()
		}
	}
}

// Broadcast sends a message to all connected clients.
// It is non-blocking and safe to call from multiple goroutines.
// SECURITY: with an access policy installed use BroadcastData (admins only) or
// BroadcastSessionData; Broadcast itself reaches every client.
func (h *Hub) Broadcast(message []byte) {
	h.send(outbound{msg: message})
}

func (h *Hub) send(ob outbound) {
	select {
	case h.broadcast <- ob:
	case <-h.ctx.Done():
		// Hub is shutting down, ignore broadcast
	default:
		// Channel is full, drop message to avoid blocking
	}
}

// ClientCount returns the number of currently connected clients.
func (h *Hub) ClientCount() int {
	h.mutex.RLock()
	defer h.mutex.RUnlock()
	return len(h.clients)
}

// Shutdown gracefully shuts down the hub and closes all client connections.
// It blocks until the main loop has exited and all clients are closed.
func (h *Hub) Shutdown() error {
	h.cancel()
	<-h.done
	return nil
}

// HandleWebSocket returns a WebSocket handler function for Fiber.
// It manages the connection lifecycle, registration, and message reading.
func (h *Hub) HandleWebSocket() func(*websocket.Conn) {
	return func(c *websocket.Conn) {
		// A panic in a hijacked fasthttp connection goroutine would take the
		// whole process down: log it and close this connection instead.
		defer recoverConn("analytics WebSocket", c)
		// SECURITY: Limit maximum incoming message size to 1MB to prevent memory exhaustion
		c.SetReadLimit(1 * 1024 * 1024)

		// Validate authentication if enabled (always over the public tunnel)
		viaTunnel := remoteaccess.MarkedViaTunnel(c.Locals(remoteaccess.ViaTunnelLocal))
		sessionHash, sessionToken := "", ""
		if h.authEnabled || viaTunnel {
			tokens := []string{h.extractWebSocketToken(c), c.Cookies("session_token")}
			// Remember the session behind this connection, so it is closed
			// when that session is signed out.
			sessionHash, sessionToken = h.presentedSession(tokens)
			if res := h.authorize(tokens, viaTunnel); res != authOK {
				msg := "Invalid or missing API key. Use ?token=<api-key> query parameter."
				if res == authAPIKeyOverTunnel {
					msg = remoteaccess.APIKeyRefusedMessage
				}
				// Send unauthorized error message before closing
				_ = c.SetWriteDeadline(time.Now().Add(writeWait))
				c.WriteJSON(fiber.Map{
					"error":   "Unauthorized",
					"message": msg,
				})
				c.Close()
				return
			}
		}

		cl := newClient(c)
		cl.sessionHash = sessionHash
		if sessionToken != "" && h.identify != nil {
			if name, ok := h.identify(sessionToken); ok {
				cl.username = name
				cl.token = sessionToken
			}
		}
		// A socket acting as a user whose identity cannot be resolved gets
		// the scoped events of nobody (username ""), like an API-key client.

		// Queue the welcome message before the writer starts so it is the
		// first frame the client sees.
		if welcome, err := json.Marshal(fiber.Map{
			"type":    "connected",
			"message": "WebSocket connected",
			"time":    time.Now(),
		}); err == nil {
			cl.enqueue(welcome)
		}

		go cl.writePump()

		// Fiber recycles the Conn once this handler returns, so the writer
		// must have stopped before then.
		defer func() {
			select {
			case h.unregister <- cl:
			case <-h.ctx.Done():
				// Hub is shutting down; Run closes registered clients.
			}
			cl.close()
			<-cl.done
		}()

		// Register the client
		select {
		case h.register <- cl:
		case <-h.ctx.Done():
			return
		}

		// Re-validate the session while the socket lives: sign-out and
		// revocation close it at once, but expiry, a password change, a
		// deleted or demoted user or a lost MFA step only show here.
		if cl.token != "" && h.sessionValid != nil && h.recheckEvery > 0 {
			go h.watchSession(cl)
		}

		// Read messages from client (mostly for keepalive and ping/pong).
		// A failed write closes the conn, which ends this loop too.
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}
}

// watchSession closes cl when the session token it connected with stops being
// a valid signed-in session. close() only moves the read deadline and closes
// the conn (a no-op once the handler's own close ran), so the conn is never
// touched after the handler returns.
func (h *Hub) watchSession(cl *client) {
	t := time.NewTicker(h.recheckEvery)
	defer t.Stop()
	for {
		select {
		case <-cl.quit:
			return
		case <-h.ctx.Done():
			return
		case <-t.C:
			if !h.sessionValid(cl.token) {
				cl.close()
				return
			}
		}
	}
}

// extractWebSocketToken extracts the API token from WebSocket connection query parameters
func (h *Hub) extractWebSocketToken(c *websocket.Conn) string {
	// Try to get token from query parameters using the Query method
	// The Fiber WebSocket connection supports this method
	queryStr := c.Query("token")
	if queryStr != "" {
		return queryStr
	}

	// Fallback: check the underlying HTTP request
	// Access the request object from the WebSocket connection's NetConn
	// which provides access to the original HTTP request URI and query parameters
	if c != nil {
		// Try to extract from the WebSocket connection's internals
		// The query string may be available through URL parameters
		return ""
	}

	return ""
}

// SendUpdate sends an update notification to all clients.
// This is a convenience method that formats the update as JSON.
// Deprecated: Use BroadcastData for sending updates with data payloads.
func (h *Hub) SendUpdate(updateType string, data interface{}) {
	// Note: data parameter is currently unused for simplicity
	// In production, consider proper JSON marshaling with encoding/json
	_ = data
	message := []byte(fmt.Sprintf(`{"type":"%s","time":"%s"}`, updateType, time.Now().Format(time.RFC3339)))
	allowed, scoped := h.audience("", false)
	h.send(outbound{msg: message, scoped: scoped, allowed: allowed})
}

// BroadcastData sends a structured event that belongs to no session (settings,
// resets, history clears). With an access policy only admins receive it.
func (h *Hub) BroadcastData(eventType string, data interface{}) {
	h.broadcastEvent(eventType, "", false, data)
}

// BroadcastSessionData sends a structured event about one agent session or
// conversation. With an access policy it reaches only the signed-in users who
// may see that session, and admins; an event whose session does not exist
// reaches admins only.
func (h *Hub) BroadcastSessionData(eventType, sessionID string, data interface{}) {
	h.broadcastEvent(eventType, sessionID, true, data)
}

// audience computes who may receive an event. A nil policy (auth off) means
// everyone: the returned scoped flag is false.
func (h *Hub) audience(sessionID string, perSession bool) (map[string]bool, bool) {
	p := h.policy
	if p == nil || p.Enforced == nil || !p.Enforced() {
		return nil, false
	}
	h.mutex.RLock()
	names := map[string]bool{}
	for cl := range h.clients {
		if cl.username != "" {
			names[cl.username] = false
		}
	}
	h.mutex.RUnlock()
	for name := range names {
		switch {
		case p.IsAdmin != nil && p.IsAdmin(name):
			names[name] = true
		case perSession && sessionID != "" && p.CanSee != nil && p.CanSee(name, sessionID):
			names[name] = true
		}
	}
	return names, true
}

func (h *Hub) broadcastEvent(eventType, sessionID string, perSession bool, data interface{}) {
	payload := map[string]interface{}{
		"event":     eventType,
		"data":      data,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		// Log error but don't block - WebSocket updates are non-critical
		fmt.Printf("Error marshaling WebSocket data: %v\n", err)
		return
	}

	allowed, scoped := h.audience(sessionID, perSession)
	h.send(outbound{msg: jsonData, scoped: scoped, allowed: allowed})
}

// recoverConn turns a panic in a WebSocket goroutine into a log line and a
// closed connection.
func recoverConn(where string, c *websocket.Conn) {
	if r := recover(); r != nil {
		logging.Error("PANIC in %s: %v\n%s", where, r, debug.Stack())
		if c != nil {
			_ = c.Close()
		}
	}
}

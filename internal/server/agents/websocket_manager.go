package agents

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	fiberws "github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/remoteaccess"
)

// SetConnectionUser sets the authenticated user for a WebSocket connection (SECURITY: for multi-user)
// This should be set when the connection is established, based on auth context
func (h *AgentHandler) SetConnectionUser(conn *fiberws.Conn, username *string) {
	h.connUsersLock.Lock()
	defer h.connUsersLock.Unlock()
	h.connUsers[conn] = username
}

// GetConnectionUser retrieves the authenticated user for a WebSocket connection (SECURITY: prevents spoofing)
func (h *AgentHandler) GetConnectionUser(conn *fiberws.Conn) *string {
	h.connUsersLock.RLock()
	defer h.connUsersLock.RUnlock()
	return h.connUsers[conn]
}

// ClearConnectionUser removes the authenticated user mapping for a WebSocket connection
func (h *AgentHandler) ClearConnectionUser(conn *fiberws.Conn) {
	h.connUsersLock.Lock()
	defer h.connUsersLock.Unlock()
	delete(h.connUsers, conn)
	delete(h.connTokenHashes, conn)
}

// SetConnectionSession records, in one step, the signed-in user of a
// connection and the hash (database.HashToken) of the session token that
// proved it, so it can be closed when that session is signed out. One step, so
// the keepalive never sees a user without its token (which counts as revoked,
// see connectionSessionRevoked). A nil username clears the hash too.
func (h *AgentHandler) SetConnectionSession(conn *fiberws.Conn, username *string, tokenHash string) {
	h.connUsersLock.Lock()
	defer h.connUsersLock.Unlock()
	if h.connTokenHashes == nil {
		h.connTokenHashes = make(map[*fiberws.Conn]string)
	}
	h.connUsers[conn] = username
	if username == nil || tokenHash == "" {
		delete(h.connTokenHashes, conn)
		return
	}
	h.connTokenHashes[conn] = tokenHash
}

// CloseConnectionsForSessionToken closes every /agent/ws connection that was
// authenticated with the session token whose hash is tokenHash (sign-out,
// device revoked). It returns how many were closed.
func (h *AgentHandler) CloseConnectionsForSessionToken(tokenHash string) int {
	if tokenHash == "" {
		return 0
	}
	// SECURITY: collect the connLife values, not the conns, while the hash map
	// still lists them (a handler clears its entry before it ends its life, so
	// a life found here belongs to the connection that presented the token),
	// and close through them. Looking a *Conn pointer up later could find the
	// life of another connection Fiber recycled that pointer for.
	h.connUsersLock.RLock()
	var lives []*connLife
	for conn, hash := range h.connTokenHashes {
		if subtle.ConstantTimeCompare([]byte(hash), []byte(tokenHash)) == 1 {
			if v, ok := h.connLives.Load(conn); ok {
				lives = append(lives, v.(*connLife))
			}
		}
	}
	h.connUsersLock.RUnlock()
	for _, life := range lives {
		h.closeLife(life)
	}
	return len(lives)
}

// closeSignedOut tells a connection its session ended and closes it.
//
// It may run outside the connection's read loop. fasthttp's hijacked
// connection ignores Close until the handler returns, so the connection is
// marked as kicked (the loop stops at its next message) and its read
// deadline is moved to now, which unblocks the pending read.
//
// SECURITY: gofiber recycles a *websocket.Conn once its handler returns, so
// a conn is only touched from outside the handler while its connLife says the
// handler is still running, under the life's mutex (the handler marks it
// ended, under the same mutex, before returning). Closing a signed-out
// session's socket used to touch a conn Fiber might already have handed to
// another connection (a data race, under -race a failure).
func (h *AgentHandler) closeSignedOut(conn *fiberws.Conn) {
	v, ok := h.connLives.Load(conn)
	if !ok {
		return // the handler has returned: nothing left to close
	}
	h.closeLife(v.(*connLife))
}

// closeLife closes the connection of a life whose handler is still running.
func (h *AgentHandler) closeLife(life *connLife) {
	conn := life.conn
	life.mu.Lock()
	defer life.mu.Unlock()
	if life.ended {
		return
	}
	logging.Info("Closing WebSocket %s: its session was signed out", conn.RemoteAddr())
	h.kickedConns.Store(conn, struct{}{})
	_ = h.safeWriteJSON(conn, map[string]interface{}{
		"type":  "auth_error",
		"error": "session signed out",
	})
	_ = conn.SetReadDeadline(time.Now())
	_ = conn.Close()
}

// connLife tells whether a connection's handler is still running.
type connLife struct {
	conn  *fiberws.Conn
	mu    sync.Mutex
	ended bool
}

// beginConnLife registers a connection whose handler starts. The returned
// func marks it ended and forgets it; call it before the handler returns.
func (h *AgentHandler) beginConnLife(conn *fiberws.Conn) (end func()) {
	life := &connLife{conn: conn}
	h.connLives.Store(conn, life)
	return func() {
		life.mu.Lock()
		life.ended = true
		h.connLives.Delete(conn)
		h.kickedConns.Delete(conn)
		life.mu.Unlock()
	}
}

// connKicked reports whether closeSignedOut was called for the connection.
func (h *AgentHandler) connKicked(conn *fiberws.Conn) bool {
	_, kicked := h.kickedConns.Load(conn)
	return kicked
}

// connectionSessionRevoked reports whether the connection was authenticated
// with a session token that is no longer valid.
//
// SECURITY: a connection that acts as a user but has no recorded token could
// never be signed out, so it counts as revoked too. API-key connections (no
// user) and servers without user accounts (no SessionTokenHashValid) are not
// affected.
func (h *AgentHandler) connectionSessionRevoked(conn *fiberws.Conn) bool {
	if h.SessionTokenHashValid == nil {
		return false
	}
	h.connUsersLock.RLock()
	hash := h.connTokenHashes[conn]
	user := h.connUsers[conn]
	h.connUsersLock.RUnlock()
	if hash == "" {
		return user != nil
	}
	return !h.SessionTokenHashValid(hash)
}

// BroadcastToAll broadcasts a message to all connected agent WebSocket clients
// SECURITY: Only sends to connections that have registered sessions (session-scoped).
// Connections that have not registered any session are excluded to prevent
// information leakage to unauthenticated or unassociated clients.
func (h *AgentHandler) BroadcastToAll(event string, data interface{}) {
	// Use session-scoped connections instead of allConnections to ensure
	// only clients with active sessions receive broadcast messages
	h.sessionConnectionsMu.RLock()
	// Deduplicate connections (a single conn may be registered for multiple sessions)
	seen := make(map[*fiberws.Conn]bool)
	var connections []*fiberws.Conn
	for _, conns := range h.sessionConnections {
		for _, conn := range conns {
			if !seen[conn] {
				seen[conn] = true
				connections = append(connections, conn)
			}
		}
	}
	h.sessionConnectionsMu.RUnlock()

	if len(connections) == 0 {
		return
	}

	message := map[string]interface{}{
		"type": event,
		"data": data,
	}

	// Broadcast to session-scoped clients only
	for _, conn := range connections {
		go func(c *fiberws.Conn) {
			if err := h.safeWriteJSON(c, message); err != nil {
				logging.Debug("Failed to broadcast to client: %v", err)
			}
		}(conn)
	}
}

// BroadcastToUser broadcasts a message only to WebSocket connections belonging to
// the specified user. This prevents cross-user information leakage in multi-user deployments.
func (h *AgentHandler) BroadcastToUser(username string, event string, data interface{}) {
	h.allConnectionsMu.RLock()
	connections := h.allConnections
	h.allConnectionsMu.RUnlock()

	if len(connections) == 0 {
		return
	}

	message := map[string]interface{}{
		"type": event,
		"data": data,
	}

	for _, conn := range connections {
		connUser := h.GetConnectionUser(conn)
		if connUser == nil || *connUser != username {
			continue
		}
		go func(c *fiberws.Conn) {
			if err := h.safeWriteJSON(c, message); err != nil {
				logging.Debug("Failed to broadcast to user client: %v", err)
			}
		}(conn)
	}
}

// getConnWriter gets or creates the write state for a specific connection
func (h *AgentHandler) getConnWriter(conn *fiberws.Conn) *connWriter {
	h.connMuLock.RLock()
	w, exists := h.connMu[conn]
	h.connMuLock.RUnlock()

	if exists {
		return w
	}

	h.connMuLock.Lock()
	defer h.connMuLock.Unlock()

	// Double-check in case another goroutine created it
	if w, exists := h.connMu[conn]; exists {
		return w
	}

	if h.connMu == nil {
		h.connMu = make(map[*fiberws.Conn]*connWriter)
	}
	w = &connWriter{}
	h.connMu[conn] = w
	return w
}

// writeConn runs one write on conn through its connWriter: serialised, with a
// write deadline, and closing the connection when the write fails or too many
// writes are waiting (see connWriter).
func (h *AgentHandler) writeConn(conn *fiberws.Conn, data []byte) error {
	w := h.getConnWriter(conn)
	err := w.write(func() error {
		_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
		return conn.WriteMessage(fiberws.TextMessage, data)
	}, func() {
		logging.Warning("Closing WebSocket %s after a failed or backed-up write", conn.RemoteAddr())
		_ = conn.Close()
	})
	return err
}

// removeConnMutex removes the mutex for a connection. It must only be called
// once the connection itself has closed (HandleFiberWebSocket cleanup): a
// connection can be subscribed to several sessions, and dropping its mutex
// while it is still in use would let a later writer create a second mutex for
// the same connection and race the first one into a concurrent write.
func (h *AgentHandler) removeConnMutex(conn *fiberws.Conn) {
	h.connMuLock.Lock()
	defer h.connMuLock.Unlock()
	delete(h.connMu, conn)
}

// safeWriteJSON safely writes JSON to a Fiber WebSocket connection with per-connection mutex protection
// This prevents "concurrent write to websocket connection" panics when multiple goroutines
// try to write simultaneously, while allowing parallel writes to different connections.
// Each write is bounded by wsWriteWait so a stalled client cannot pin the
// writing goroutine (and everything queued behind its mutex) indefinitely, and
// a failed write closes the connection so the client reconnects.
// The value is marshalled before taking the lock: an encoding error is the
// caller's problem, not the connection's.
func (h *AgentHandler) safeWriteJSON(conn *fiberws.Conn, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return h.writeConn(conn, data)
}

// safeWriteRaw writes an already-encoded JSON text frame with the same
// locking and deadline as safeWriteJSON. It lets a broadcast marshal a
// message once and send identical bytes to every connection.
func (h *AgentHandler) safeWriteRaw(conn *fiberws.Conn, data []byte) error {
	return h.writeConn(conn, data)
}

// WebSocket keepalive tuning.
//
// Without a heartbeat a broken connection is invisible to both ends: neither
// side writes, so nothing fails, and the socket sits half-open indefinitely.
// Server-side pings keep genuinely idle connections alive through proxies that
// reap silent TCP sessions, and the read deadline bounds how long a dead peer
// can keep occupying a connection slot.
const (
	// wsPingPeriod is how often the server sends a protocol-level ping frame.
	wsPingPeriod = 30 * time.Second
	// wsPongWait is how long the server tolerates silence before treating the
	// connection as dead. It must be comfortably larger than wsPingPeriod so
	// that one dropped ping cannot close a healthy connection. Browsers and
	// URLSession reply to ping frames automatically, so any live client keeps
	// this refreshed without cooperating explicitly.
	wsPongWait = 90 * time.Second
	// wsWriteWait bounds how long a control-frame write may block.
	wsWriteWait = 10 * time.Second

	// wsPreAuthTimeout is how long a connection may stay unauthenticated
	// before it is closed (AgentHandler.PreAuthTimeout overrides it).
	wsPreAuthTimeout = 10 * time.Second
	// wsPreAuthMaxMessages is how many non-auth messages an unauthenticated
	// connection may send before it is closed.
	wsPreAuthMaxMessages = 3
	// wsPreAuthReadLimit caps a message before authentication; an auth
	// message is tiny.
	wsPreAuthReadLimit = 64 * 1024
	// WSMaxMessageBytes caps a message once authenticated (base64 images).
	WSMaxMessageBytes = 10 * 1024 * 1024
)

func (h *AgentHandler) preAuthTimeout() time.Duration {
	if h.PreAuthTimeout > 0 {
		return h.PreAuthTimeout
	}
	return wsPreAuthTimeout
}

// recoverWS turns a panic in a WebSocket goroutine into a log line and a
// closed connection. Without it, a panic in a hijacked fasthttp connection
// goroutine takes the whole process down.
func recoverWS(where string, closeConn func()) {
	if r := recover(); r != nil {
		logging.Error("PANIC in %s: %v\n%s", where, r, debug.Stack())
		if closeConn != nil {
			closeConn()
		}
	}
}

// HandleFiberWebSocket returns a Fiber WebSocket handler function
// This is compatible with Fiber's WebSocket middleware
func (h *AgentHandler) HandleFiberWebSocket(c *fiberws.Conn) {
	// Registered first so it runs last, after the cleanup below.
	defer recoverWS("agent WebSocket "+c.RemoteAddr().String(), func() { _ = c.Close() })
	log.Printf("HandleFiberWebSocket: New WebSocket connection from %s", c.RemoteAddr())
	endLife := h.beginConnLife(c)
	// Runs after the cleanup below (LIFO) and before the handler returns.
	defer endLife()

	// SECURITY: a connection only counts against MaxConcurrentSessions (and
	// joins the broadcast list) once it has authenticated, so anonymous
	// sockets cannot fill the limit and lock the real user out.
	counted := false
	admit := func() bool {
		if counted {
			return true
		}
		h.Mu.Lock()
		if h.Active >= h.Config.MaxConcurrentSessions {
			h.Mu.Unlock()
			logging.Warning("Max concurrent sessions reached: %d/%d", h.Active, h.Config.MaxConcurrentSessions)
			_ = h.safeWriteJSON(c, map[string]interface{}{
				"type":    "error",
				"message": "max concurrent sessions reached",
			})
			return false
		}
		h.Active++
		log.Printf("HandleFiberWebSocket: Active connections: %d/%d", h.Active, h.Config.MaxConcurrentSessions)
		h.Mu.Unlock()
		counted = true

		// Add this connection to the global list for broadcasting
		h.allConnectionsMu.Lock()
		h.allConnections = append(h.allConnections, c)
		h.allConnectionsMu.Unlock()
		return true
	}

	// Track which sessions are connected via this WebSocket
	connectedSessions := make(map[uuid.UUID]bool)
	var connectedSessionsMu sync.Mutex

	// Helper function to register a session with this WebSocket
	registerSession := func(sessionID uuid.UUID) {
		connectedSessionsMu.Lock()
		defer connectedSessionsMu.Unlock()

		// If session was previously connected via another WebSocket, clean it up first
		session, err := h.SessionManager.GetSession(sessionID)
		if err != nil {
			logging.Error("Failed to get session %s: %v", sessionID, err)
			return
		}

		// Already registered on this connection (list_sessions, load_messages,
		// create_session and subscribe_session all register): keep it tracked and
		// its forwarder running, but do not treat it as a reconnect. Doing so
		// denied pending approvals and started another forwarder every time.
		if connectedSessions[sessionID] {
			if running, owner := session.permissionForwarderState(); running && owner == c {
				session.SetWebSocketConnected(true)
				h.trackSessionConnection(sessionID, c)
				return
			}
		}

		if running, owner := session.permissionForwarderState(); session.IsWebSocketConnected() && running && owner != c {
			logging.Warning("Session %s moved to another WebSocket - cleaning up old WebSocket state", sessionID)
			session.CleanupPendingPermissions()
		}
		session.SetWebSocketConnected(true)

		// Start the permission forwarder immediately when the session connects, so
		// permission requests work before the first prompt. Idempotent per
		// connection; a forwarder bound to an older connection is stopped.
		if stop, gen, started := session.StartPermissionForwarder(c); started {
			logging.Info("🚀 Starting permission forwarder for session %s on WebSocket connect", sessionID)
			go h.forwardPermissionRequests(c, sessionID, session, stop, gen)
		}

		// Track this connection for session state updates
		h.trackSessionConnection(sessionID, c)

		// Wire up git status callback so session receives updates from ProjectWatchers
		session.gitStatusCallbackMu.Lock()
		session.gitStatusCallback = func(status *GitStatusData) {
			// Send git status update to this WebSocket client
			msg := GitStatusUpdateMessage{
				BaseMessage: BaseMessage{Type: MessageTypeGitStatusUpdate},
				SessionID:   sessionID,
				Status:      status,
				Timestamp:   time.Now(),
			}

			// Include project ID if this session is subscribed to a project
			if session.ProjectID != nil {
				msg.ProjectID = *session.ProjectID
			}

			logging.Info("📤 Sending git status update to WebSocket for session %s (project=%s): branch=%s, clean=%v", sessionID, msg.ProjectID, status.Branch, status.Clean)
			if err := h.safeWriteJSON(c, msg); err != nil {
				logging.Error("Failed to send git status update: %v", err)
				session.SetWebSocketConnected(false)
			}
		}
		session.gitStatusCallbackMu.Unlock()

		connectedSessions[sessionID] = true
		logging.Info("Session %s registered with WebSocket connection", sessionID)

		// NOTE: Git watching is now subscription-based via subscribe_project messages
		// See handleFiberSubscribeProject for the subscription flow
	}

	defer func() {
		if counted {
			h.Mu.Lock()
			h.Active--
			active := h.Active
			h.Mu.Unlock()
			logging.Debug("WebSocket connection closed, active connections: %d", active)

			// Remove this connection from the global broadcast list
			h.allConnectionsMu.Lock()
			for i, conn := range h.allConnections {
				if conn == c {
					h.allConnections = append(h.allConnections[:i], h.allConnections[i+1:]...)
					break
				}
			}
			h.allConnectionsMu.Unlock()
		}
		h.ClearConnectionUser(c)
		h.kickedConns.Delete(c)

		// Clean up all sessions connected via this WebSocket
		connectedSessionsMu.Lock()
		for sessionID := range connectedSessions {
			// Untrack connection
			h.untrackSessionConnection(sessionID, c)

			if session, err := h.SessionManager.GetSession(sessionID); err == nil {
				// The session may have moved to another connection meanwhile
				// (another tab, a reconnect that raced this close): leave that
				// connection's forwarder and pending approvals alone.
				if running, owner := session.permissionForwarderState(); running && owner != c {
					logging.Info("WebSocket closed for session %s, which now uses another connection", sessionID)
					continue
				}
				logging.Info("Disconnecting session %s due to WebSocket close", sessionID)
				session.SetWebSocketConnected(false)
				session.CleanupPendingPermissions()
				session.StopPermissionForwarder()

				// Unsubscribe from all projects
				session.subscribedProjectsMu.Lock()
				subscribedProjects := make(map[string]bool)
				for projectID := range session.subscribedProjects {
					subscribedProjects[projectID] = true
				}
				session.subscribedProjectsMu.Unlock()

				for projectID := range subscribedProjects {
					if err := h.ProjectWatcherMgr.UnsubscribeFromProject(projectID, session.GetID()); err != nil {
						logging.Warning("Failed to unsubscribe session %s from project %s: %v", sessionID, projectID, err)
					}
				}

				// Clear the git status callback
				session.gitStatusCallbackMu.Lock()
				session.gitStatusCallback = nil
				session.gitStatusCallbackMu.Unlock()
			}
		}
		connectedSessionsMu.Unlock()

		// Clean up debug log subscription if any
		CleanupDebugLogSubscription(c)

		// The connection is gone: only now is it safe to drop its write mutex.
		h.removeConnMutex(c)
	}()

	log.Printf("Fiber WebSocket connection established from %s", c.RemoteAddr().String())
	logging.Info("WebSocket connection established from %s", c.RemoteAddr().String())

	// Track authentication state.
	// If the user was already authenticated via SessionAuthMiddleware (cookie-based session),
	// skip the API key auth handshake entirely. This avoids an extra HTTP round-trip
	// (fetching the API key) on the client side, which is especially slow over ngrok.
	authenticated := false
	var authed atomic.Bool // mirrors authenticated for the pong handler
	if connUser := h.GetConnectionUser(c); connUser != nil {
		if !admit() {
			return
		}
		authenticated = true
		authed.Store(true)
		logging.Info("WebSocket pre-authenticated via session for user %s from %s", *connUser, c.RemoteAddr())
		_ = h.safeWriteJSON(c, map[string]interface{}{
			"type": "auth_success",
		})
	}

	// Until it authenticates, a connection gets a small read limit and a
	// short deadline (wsPreAuthTimeout); after that the normal limit and the
	// keepalive deadline apply.
	if authenticated {
		c.SetReadLimit(WSMaxMessageBytes)
		_ = c.SetReadDeadline(time.Now().Add(wsPongWait))
	} else {
		c.SetReadLimit(wsPreAuthReadLimit)
		_ = c.SetReadDeadline(time.Now().Add(h.preAuthTimeout()))
	}

	// Start the keepalive. A pong, or any other inbound frame, extends the read
	// deadline; if none arrives within wsPongWait the blocking ReadJSON below
	// fails and the deferred cleanup untracks this connection.
	c.SetPongHandler(func(string) error {
		if !authed.Load() || h.connKicked(c) {
			return nil // the pre-auth deadline (or the kick) stands
		}
		return c.SetReadDeadline(time.Now().Add(wsPongWait))
	})

	heartbeatDone := make(chan struct{})
	defer close(heartbeatDone)
	go func() {
		defer recoverWS("agent WebSocket heartbeat", func() { _ = c.Close() })
		ticker := time.NewTicker(wsPingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatDone:
				return
			case <-ticker.C:
				// A signed-in connection whose session was signed out or
				// expired is closed even when it is idle.
				if h.connectionSessionRevoked(c) {
					h.closeSignedOut(c)
					return
				}
				// WriteControl is safe to call concurrently with other writers,
				// so unlike safeWriteJSON this needs no per-connection mutex.
				if err := c.WriteControl(fiberws.PingMessage, nil, time.Now().Add(wsWriteWait)); err != nil {
					logging.Debug("Heartbeat ping failed for %s: %v", c.RemoteAddr(), err)
					return
				}
			}
		}
	}()

	// SECURITY: Rate limiting state for this connection
	// Uses a sliding window approach: track message count per second
	const rateLimitPerSecond = 30
	const rateLimitViolationsMax = 5
	var rateLimitCount int
	var rateLimitWindowStart time.Time = time.Now()
	var rateLimitViolations int
	preAuthMessages := 0

	// Main message loop
	for {
		var rawMsg map[string]interface{}
		if err := c.ReadJSON(&rawMsg); err != nil {
			if !authenticated {
				logging.Debug("Closing unauthenticated WebSocket %s: %v", c.RemoteAddr(), err)
			} else if fiberws.IsUnexpectedCloseError(err, fiberws.CloseGoingAway, fiberws.CloseAbnormalClosure) {
				log.Printf("Error receiving message: %v", err)
			}
			return
		}

		if h.connKicked(c) {
			return
		}

		// Inbound traffic proves the peer is alive, so extend the deadline. The
		// pong handler does this too, but a chatty client may never idle long
		// enough for a ping to be sent in the first place. Before
		// authentication the pre-auth deadline stands.
		if authenticated {
			_ = c.SetReadDeadline(time.Now().Add(wsPongWait))
		}

		// SECURITY: a connection signed in with a session token stops working
		// as soon as that session is signed out or revoked.
		if authenticated && h.connectionSessionRevoked(c) {
			h.closeSignedOut(c)
			return
		}

		// SECURITY: Rate limiting check
		now := time.Now()
		if now.Sub(rateLimitWindowStart) >= time.Second {
			// Reset the window
			rateLimitCount = 0
			rateLimitWindowStart = now
		}
		rateLimitCount++
		if rateLimitCount > rateLimitPerSecond {
			rateLimitViolations++
			logging.Warning("Rate limit exceeded for %s (%d msgs/sec, violation %d/%d)",
				c.RemoteAddr(), rateLimitCount, rateLimitViolations, rateLimitViolationsMax)
			h.sendFiberError(c, "rate limit exceeded: too many messages")
			if rateLimitViolations >= rateLimitViolationsMax {
				logging.Warning("Closing connection %s due to repeated rate limit violations", c.RemoteAddr())
				h.safeWriteJSON(c, map[string]interface{}{
					"type":    "error",
					"message": "connection closed: repeated rate limit violations",
				})
				c.Close()
				return
			}
			continue
		}

		msgType, ok := rawMsg["type"].(string)
		if !ok {
			log.Printf("ERROR: Missing or invalid message type in: %+v", rawMsg)
			h.sendFiberError(c, "missing or invalid message type")
			continue
		}

		// Handle authentication first (log AFTER to avoid leaking tokens)
		if MessageType(msgType) == MessageTypeAuth {
			wasAuthenticated := authenticated
			shouldClose, err := h.handleFiberAuth(c, rawMsg, &authenticated)
			if err != nil {
				log.Printf("ERROR: Failed to authenticate: %v", err)
			}
			// SECURITY: Close connection and stop read loop on auth failure
			// to prevent unauthenticated clients from consuming server resources
			if shouldClose {
				c.Close()
				return
			}
			if authenticated && !wasAuthenticated {
				if !admit() {
					c.Close()
					return
				}
				authed.Store(true)
				c.SetReadLimit(WSMaxMessageBytes)
				_ = c.SetReadDeadline(time.Now().Add(wsPongWait))
			}
			continue
		}

		// Require authentication for all other messages
		if !authenticated {
			preAuthMessages++
			log.Printf("ERROR: Message received before authentication: type=%s", msgType)
			h.sendFiberError(c, "authentication required")
			if preAuthMessages >= wsPreAuthMaxMessages {
				logging.Warning("Closing WebSocket %s: %d messages without authenticating", c.RemoteAddr(), preAuthMessages)
				c.Close()
				return
			}
			continue
		}

		log.Printf("📥 WS INCOMING: type=%s, sessionID=%v", msgType, rawMsg["session_id"])

		// Route message to appropriate handler. A panic in a handler closes
		// this connection instead of the whole process.
		panicked, err := h.routeFiberMessageSafe(c, MessageType(msgType), rawMsg, registerSession)
		if panicked {
			c.Close()
			return
		}
		if err != nil {
			log.Printf("ERROR: Failed to handle message type %s: %v", msgType, err)
			h.sendFiberError(c, fmt.Sprintf("message handling failed: %v", err))
		}
	}
}

// routeFiberMessageSafe runs routeFiberMessage and reports a panic instead of
// letting it unwind the connection goroutine.
func (h *AgentHandler) routeFiberMessageSafe(c *fiberws.Conn, msgType MessageType, rawMsg map[string]interface{}, registerSession func(uuid.UUID)) (panicked bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			logging.Error("PANIC handling %s from %s: %v\n%s", msgType, c.RemoteAddr(), r, debug.Stack())
			h.sendFiberError(c, "internal error")
			panicked, err = true, fmt.Errorf("internal error")
		}
	}()
	return false, h.routeFiberMessage(c, msgType, rawMsg, registerSession)
}

// sendFiberError sends an error message via Fiber WebSocket
func (h *AgentHandler) sendFiberError(c *fiberws.Conn, errMsg string) {
	err := h.safeWriteJSON(c, map[string]interface{}{
		"type":    "error",
		"message": errMsg,
	})
	if err != nil {
		log.Printf("Failed to send error message: %v", err)
	}
}

// handleFiberAuth handles WebSocket authentication.
// Returns (shouldClose, error) - if shouldClose is true, the caller must close the connection and stop the read loop.
func (h *AgentHandler) handleFiberAuth(c *fiberws.Conn, rawMsg map[string]interface{}, authenticated *bool) (bool, error) {
	token, ok := rawMsg["token"].(string)
	if !ok || token == "" {
		_ = h.safeWriteJSON(c, map[string]interface{}{
			"type":  "auth_error",
			"error": "missing authentication token",
		})
		return true, fmt.Errorf("missing authentication token")
	}

	// Try API key authentication first (if configured)
	if h.APIKey != "" {
		// Use constant-time comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(token), []byte(h.APIKey)) == 1 {
			// SECURITY: the API key is a file every local process can read,
			// agents included. Over the public tunnel (remote access) it is
			// refused; only a signed-in user's session token works there.
			if remoteaccess.MarkedViaTunnel(c.Locals(remoteaccess.ViaTunnelLocal)) {
				logging.Warning("WebSocket: refused the API key over remote access from %s", c.RemoteAddr())
				if *authenticated {
					// Keep an already signed-in connection as it was.
					return false, h.safeWriteJSON(c, map[string]interface{}{
						"type":    "error",
						"message": remoteaccess.APIKeyRefusedMessage,
					})
				}
				_ = h.safeWriteJSON(c, map[string]interface{}{
					"type":  "auth_error",
					"error": remoteaccess.APIKeyRefusedMessage,
				})
				return true, fmt.Errorf("API key refused over remote access")
			}
			*authenticated = true
			logging.Info("WebSocket authenticated via API key from %s", c.RemoteAddr())
			return false, h.safeWriteJSON(c, map[string]interface{}{
				"type": "auth_success",
			})
		}
	}

	// API key didn't match (or not configured) — try validating as a session token.
	// This allows iOS/mobile clients to authenticate via their session token
	// when the Cookie header isn't forwarded (e.g. through ngrok).
	if h.SessionValidator != nil {
		if username, err := h.SessionValidator(token); err == nil && username != "" {
			*authenticated = true
			h.SetConnectionSession(c, &username, database.HashToken(token))
			logging.Info("WebSocket authenticated via session token for user %s from %s", username, c.RemoteAddr())
			return false, h.safeWriteJSON(c, map[string]interface{}{
				"type": "auth_success",
			})
		} else if err != nil {
			logging.Warning("WebSocket session token validation failed for %s: %v", c.RemoteAddr(), err)
		}
	}

	// A connection that is already authenticated (e.g. with the API key) may
	// present a session token to become a signed-in user for privileged
	// actions. If that token is rejected, keep the connection as it was.
	if *authenticated {
		logging.Warning("WebSocket re-authentication with an invalid session token from %s; keeping the existing authentication", c.RemoteAddr())
		return false, h.safeWriteJSON(c, map[string]interface{}{
			"type":    "error",
			"message": "session token rejected; this connection is not signed in",
		})
	}

	logging.Warning("WebSocket authentication failed: invalid token from %s", c.RemoteAddr())
	_ = h.safeWriteJSON(c, map[string]interface{}{
		"type":  "auth_error",
		"error": "invalid authentication token",
	})
	return true, fmt.Errorf("invalid authentication token")
}

// handleFiberPing responds to ping with pong (Fiber version)
func (h *AgentHandler) handleFiberPing(c *fiberws.Conn) error {
	response := BaseMessage{Type: MessageTypePong}
	return h.safeWriteJSON(c, response)
}

// BroadcastToAllConnections broadcasts a message to all connected WebSocket clients
func (h *AgentHandler) BroadcastToAllConnections(message interface{}) {
	h.sessionConnectionsMu.RLock()
	defer h.sessionConnectionsMu.RUnlock()

	// Count total connections
	totalConnections := 0
	for _, conns := range h.sessionConnections {
		totalConnections += len(conns)
	}

	if totalConnections == 0 {
		logging.Debug("No agent connections to broadcast to")
		return
	}

	logging.Info("📢 Broadcasting message to %d agent connection(s)", totalConnections)

	// Broadcast to all connections across all sessions
	for _, connections := range h.sessionConnections {
		for _, conn := range connections {
			// Use goroutine to avoid blocking
			go func(c *fiberws.Conn) {
				if err := h.safeWriteJSON(c, message); err != nil {
					logging.Error("Failed to broadcast message: %v", err)
				}
			}(conn)
		}
	}
}

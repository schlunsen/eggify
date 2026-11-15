package remoteaccess

import (
	"net"
	"sync"
)

// tunnelListener wraps a provider Listener for the HTTP server:
//
//   - Close is idempotent and returns nil after the first call. fasthttp keeps
//     every listener it ever served and closes them all on Shutdown, returning
//     the first error, so a tunnel that was stopped earlier must not make the
//     server's shutdown fail.
//   - Accept after Close reports net.ErrClosed, which fasthttp treats as a
//     clean stop.
//   - Accepted connections are tagged (IsTunnelConn), so middleware can tell
//     public requests from local ones, and never report a loopback or
//     unspecified remote address: loopback-only endpoints must not be reachable
//     through the tunnel even if the edge sent such a client address.
type tunnelListener struct {
	Listener
	once   sync.Once
	mu     sync.Mutex
	closed bool
	err    error
}

func newTunnelListener(l Listener) *tunnelListener { return &tunnelListener{Listener: l} }

func (l *tunnelListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		l.mu.Lock()
		closed := l.closed
		l.mu.Unlock()
		if closed {
			return nil, net.ErrClosed
		}
		return nil, err
	}
	return &tunnelConn{Conn: c}, nil
}

func (l *tunnelListener) Close() error {
	l.once.Do(func() {
		l.mu.Lock()
		l.closed = true
		l.mu.Unlock()
		l.err = l.Listener.Close()
	})
	return nil
}

// tunnelConn marks a connection that arrived through the public tunnel.
type tunnelConn struct{ net.Conn }

// publicPlaceholderAddr replaces loopback/unspecified client addresses
// (TEST-NET-1, never routable, never loopback).
var publicPlaceholderAddr = &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 0}

func (c *tunnelConn) RemoteAddr() net.Addr {
	a := c.Conn.RemoteAddr()
	tcp, ok := a.(*net.TCPAddr)
	if !ok || tcp == nil || tcp.IP == nil || tcp.IP.IsLoopback() || tcp.IP.IsUnspecified() {
		return publicPlaceholderAddr
	}
	return a
}

// IsTunnelConn reports whether c arrived through the public tunnel.
func IsTunnelConn(c net.Conn) bool {
	_, ok := c.(*tunnelConn)
	return ok
}

// ViaTunnelLocal is the Fiber Locals key the server sets to true on every
// request that arrived through the tunnel. Locals carry over to the
// WebSocket connection after an upgrade, where the hijacked net.Conn no
// longer shows the tunnel wrapper, so socket handlers read this instead.
const ViaTunnelLocal = "eggify_via_tunnel"

// APIKeyRefusedMessage is the error given when the API key is presented over
// the tunnel. Every local process (agents included) can read the API key, so
// it must never grant access from the public internet; only a signed-in
// user's session works there.
const APIKeyRefusedMessage = "The API key is not accepted over remote access; sign in"

// MarkedViaTunnel reports whether a ViaTunnelLocal value marks a tunnel request.
func MarkedViaTunnel(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

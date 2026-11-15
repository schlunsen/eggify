package relay

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"time"
)

var errHelloRead = errors.New("client hello read")

// recordingConn feeds a TLS server handshake from the real connection while
// keeping every byte it read; it never writes.
type recordingConn struct {
	net.Conn
	r   io.Reader
	buf bytes.Buffer
}

func (c *recordingConn) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.buf.Write(p[:n])
	if c.buf.Len() > maxHelloBytes {
		return n, errors.New("client hello too large")
	}
	return n, err
}

func (c *recordingConn) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// maxHelloBytes bounds what is read while looking for the ClientHello.
const maxHelloBytes = 64 << 10

// peekServerName reads c's TLS ClientHello and returns its SNI and the bytes
// read so far, which must be sent on before the rest of c. It never answers
// the handshake.
func peekServerName(c net.Conn, timeout time.Duration) (string, []byte, error) {
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	defer func() { _ = c.SetReadDeadline(time.Time{}) }()
	rc := &recordingConn{Conn: c, r: c}
	var name string
	err := tls.Server(rc, &tls.Config{
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			name = h.ServerName
			return nil, errHelloRead
		},
	}).Handshake()
	if name == "" {
		if err == nil {
			err = errors.New("no server name in client hello")
		}
		return "", nil, err
	}
	return name, rc.buf.Bytes(), nil
}

// prefixConn reads prefix first, then the connection.
type prefixConn struct {
	net.Conn
	r io.Reader
}

func newPrefixConn(c net.Conn, prefix []byte) *prefixConn {
	return &prefixConn{Conn: c, r: io.MultiReader(bytes.NewReader(prefix), c)}
}

func (c *prefixConn) Read(p []byte) (int, error) { return c.r.Read(p) }

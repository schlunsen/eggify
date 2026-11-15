package relay

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// ClientConfig is what an install needs to connect to a relay.
type ClientConfig struct {
	// Addr is the relay's host:port.
	Addr string
	// ServerName is the relay's name (default: Addr's host). Tests dial an
	// IP and set it.
	ServerName string
	// Identity is the install's TLS identity.
	Identity *Identity
	// RootCAs validates the relay's certificate (nil: system roots).
	RootCAs *x509.CertPool
}

// RefusedError is a refusal from the relay. Permanent ones (bad version,
// failed proof) won't go away by retrying.
type RefusedError struct {
	Msg       string
	Permanent bool
	// Code is the relay's reason (Code* constants; "" from older relays).
	Code string
}

func (e *RefusedError) Error() string { return "relay refused: " + e.Msg }

// Listener yields visitors' connections, TLS-terminated with the install's
// identity, as an ordinary net.Listener.
type Listener struct {
	session *yamux.Session
	tlsCfg  *tls.Config
	url     string
	relay   string

	mu     sync.Mutex
	closed bool
}

// Dial connects to the relay, proves the identity and returns the listener.
func Dial(ctx context.Context, cfg ClientConfig) (*Listener, error) {
	host, _, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return nil, err
	}
	if cfg.ServerName != "" {
		host = cfg.ServerName
	}
	d := &tls.Dialer{Config: &tls.Config{
		ServerName: host,
		RootCAs:    cfg.RootCAs,
		MinVersion: tls.VersionTLS12,
	}}
	nc, err := d.DialContext(ctx, "tcp", cfg.Addr)
	if err != nil {
		return nil, err
	}
	c := nc.(*tls.Conn)
	ok := false
	defer func() {
		if !ok {
			_ = c.Close()
		}
	}()
	deadline := time.Now().Add(handshakeTimeout)
	if dl, has := ctx.Deadline(); has && dl.Before(deadline) {
		deadline = dl
	}
	_ = c.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	br := bufio.NewReaderSize(c, maxLineBytes)
	hello := helloMsg{V: ProtocolVersion, SPKI: cfg.Identity.SPKI}
	if err := writeLine(c, hello); err != nil {
		return nil, err
	}
	var ch challengeMsg
	if err := readLine(br, &ch); err != nil {
		return nil, ctxErr(ctx, err)
	}
	if len(ch.Nonce) < 16 {
		return nil, errors.New("relay sent a bad challenge")
	}
	sig, err := signProof(cfg.Identity, host, ch.Nonce)
	if err != nil {
		return nil, err
	}
	proof := proofMsg{Sig: sig}
	if err := writeLine(c, proof); err != nil {
		return nil, err
	}
	var res resultMsg
	if err := readLine(br, &res); err != nil {
		return nil, ctxErr(ctx, err)
	}
	if !res.OK {
		return nil, &RefusedError{Msg: res.Error, Permanent: res.Permanent, Code: res.Code}
	}
	if res.ID != cfg.Identity.ID() {
		return nil, errors.New("relay assigned an unexpected name")
	}
	_ = c.SetDeadline(time.Time{})

	ycfg := yamux.DefaultConfig()
	ycfg.LogOutput = io.Discard
	session, err := yamux.Server(&bufferedConn{Conn: c, r: br}, ycfg)
	if err != nil {
		return nil, err
	}
	ok = true
	return &Listener{session: session, tlsCfg: cfg.Identity.ServerTLSConfig(), url: res.URL, relay: host}, nil
}

func ctxErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// URL is the install's public address.
func (l *Listener) URL() string { return l.url }

// Relay is the relay's host name.
func (l *Listener) Relay() string { return l.relay }

// Done is closed when the relay session ends.
func (l *Listener) Done() <-chan struct{} { return l.session.CloseChan() }

// Accept returns the next visitor, TLS-terminated with the identity.
func (l *Listener) Accept() (net.Conn, error) {
	for {
		stream, err := l.session.AcceptStream()
		if err != nil {
			l.mu.Lock()
			closed := l.closed
			l.mu.Unlock()
			if closed {
				return nil, net.ErrClosed
			}
			return nil, err
		}
		c, err := readVisitor(stream)
		if err != nil {
			_ = stream.Close()
			continue
		}
		return tls.Server(c, l.tlsCfg), nil
	}
}

// Close ends the relay session.
func (l *Listener) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	return l.session.Close()
}

// Addr is the relay session's local address.
func (l *Listener) Addr() net.Addr { return l.session.LocalAddr() }

// visitorConn is a relayed stream that reports the visitor's address.
type visitorConn struct {
	net.Conn
	r      *bufio.Reader
	remote net.Addr
}

func (c *visitorConn) Read(p []byte) (int, error) { return c.r.Read(p) }
func (c *visitorConn) RemoteAddr() net.Addr       { return c.remote }

// unknownVisitor is used when the relay's header has no usable address
// (TEST-NET-1: never routable, never loopback).
var unknownVisitor = &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1)}

func readVisitor(stream *yamux.Stream) (net.Conn, error) {
	_ = stream.SetReadDeadline(time.Now().Add(handshakeTimeout))
	br := bufio.NewReaderSize(stream, 512)
	var h streamHeader
	if err := readLine(br, &h); err != nil {
		return nil, err
	}
	_ = stream.SetReadDeadline(time.Time{})
	var remote net.Addr = unknownVisitor
	if ap, err := netip.ParseAddrPort(h.Addr); err == nil && !ap.Addr().IsLoopback() && !ap.Addr().IsUnspecified() {
		remote = net.TCPAddrFromAddrPort(ap)
	}
	return &visitorConn{Conn: stream, r: br, remote: remote}, nil
}

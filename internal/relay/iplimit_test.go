package relay

import (
	"crypto/tls"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)} }

func TestIPKeyGroupsIPv6ByTheir64(t *testing.T) {
	a := ipKey(&net.TCPAddr{IP: net.ParseIP("2001:db8:1:2:aaaa::1"), Port: 1})
	b := ipKey(&net.TCPAddr{IP: net.ParseIP("2001:db8:1:2:bbbb:cccc:dddd:eeee"), Port: 2})
	c := ipKey(&net.TCPAddr{IP: net.ParseIP("2001:db8:1:3::1"), Port: 3})
	if a != b || a == c {
		t.Fatalf("/64 grouping wrong: %s %s %s", a, b, c)
	}
	if k := ipKey(&net.TCPAddr{IP: net.ParseIP("192.0.2.7"), Port: 9}); k != "192.0.2.7" {
		t.Fatalf("ipv4 key %q", k)
	}
	if ipKey(&net.TCPAddr{IP: net.ParseIP("::ffff:192.0.2.7")}) != "192.0.2.7" {
		t.Fatal("v4-mapped address should key as IPv4")
	}
}

func TestRedTeam_Relay_OneAddressCannotHoldUnlimitedPendingHandshakes(t *testing.T) {
	l := newIPLimiter(IPLimits{PendingPerIP: 5}, newClock().now)
	for i := 0; i < 5; i++ {
		if !l.accept("203.0.113.9") {
			t.Fatalf("connection %d refused below the limit", i)
		}
	}
	if l.accept("203.0.113.9") {
		t.Fatal("6th pending connection from one address admitted")
	}
	if !l.accept("203.0.113.10") {
		t.Fatal("another address was refused")
	}
	l.classified("203.0.113.9")
	if !l.accept("203.0.113.9") {
		t.Fatal("a freed slot was not reusable")
	}
}

func TestRedTeam_Relay_NewConnectionsPerMinuteAreCapped(t *testing.T) {
	clk := newClock()
	l := newIPLimiter(IPLimits{ConnsPerMinutePerIP: 10, PendingPerIP: -1}, clk.now)
	for i := 0; i < 10; i++ {
		if !l.accept("198.51.100.1") {
			t.Fatalf("connection %d refused", i)
		}
	}
	if l.accept("198.51.100.1") {
		t.Fatal("11th connection within a minute admitted")
	}
	clk.advance(61 * time.Second)
	if !l.accept("198.51.100.1") {
		t.Fatal("window did not reset after a minute")
	}
}

func TestRedTeam_Relay_VisitorStreamsPerAddressAreCapped(t *testing.T) {
	l := newIPLimiter(IPLimits{StreamsPerIP: 3}, newClock().now)
	for i := 0; i < 3; i++ {
		if !l.beginStream("k") {
			t.Fatal("stream refused below the limit")
		}
	}
	if l.beginStream("k") || l.rejected.Load() != 1 {
		t.Fatal("4th concurrent stream from one address admitted")
	}
	l.endStream("k")
	if !l.beginStream("k") {
		t.Fatal("ended stream did not free its slot")
	}
}

// Installs have their own, higher caps: more than a visitor's pending cap can
// connect from one address, and a flood of install connections is still bounded.
func TestRedTeam_Relay_ControlConnectionsHaveTheirOwnHigherCaps(t *testing.T) {
	clk := newClock()
	l := newIPLimiter(IPLimits{ControlPerIP: 40, ControlPendingPerIP: 40, ControlPerMinutePerIP: 100}, clk.now)
	if l.lim.ControlPerIP <= l.lim.PendingPerIP || l.lim.ControlPerMinutePerIP <= 0 {
		t.Fatal("control cap is not higher than the visitor-side cap by default")
	}
	for i := 0; i < 40; i++ {
		if !l.beginControl("k") {
			t.Fatalf("install %d refused", i)
		}
		l.handshakeDone("k")
	}
	if l.beginControl("k") {
		t.Fatal("41st concurrent install connection admitted")
	}
	l.endControl("k", true)
	if !l.beginControl("k") {
		t.Fatal("closed install did not free its slot")
	}
	// Handshakes in flight are capped separately (a slow handshake flood).
	l2 := newIPLimiter(IPLimits{ControlPerIP: 100, ControlPendingPerIP: 3, ControlPerMinutePerIP: -1}, clk.now)
	for i := 0; i < 3; i++ {
		l2.beginControl("k")
	}
	if l2.beginControl("k") {
		t.Fatal("4th handshake in flight admitted")
	}
	// New installs per minute.
	l3 := newIPLimiter(IPLimits{ControlPerIP: -1, ControlPendingPerIP: -1, ControlPerMinutePerIP: 5}, clk.now)
	for i := 0; i < 5; i++ {
		l3.beginControl("k")
		l3.endControl("k", true)
	}
	if l3.beginControl("k") {
		t.Fatal("6th install connection within a minute admitted")
	}
}

func TestIPLimiterForgetsIdleAddresses(t *testing.T) {
	clk := newClock()
	l := newIPLimiter(IPLimits{}, clk.now)
	for i := 0; i < 50; i++ {
		l.accept(net.IPv4(10, 0, 0, byte(i)).String())
		l.classified(net.IPv4(10, 0, 0, byte(i)).String())
	}
	clk.advance(3 * time.Minute)
	l.accept("x")
	l.mu.Lock()
	n := len(l.by)
	l.mu.Unlock()
	if n > 2 {
		t.Fatalf("%d idle addresses kept", n)
	}
}

// fakeListener hands out in-memory connections that claim a TCP source address.
type fakeListener struct {
	ch   chan net.Conn
	done chan struct{}
}

func (f *fakeListener) Accept() (net.Conn, error) {
	select {
	case c := <-f.ch:
		return c, nil
	case <-f.done:
		return nil, net.ErrClosed
	}
}
func (f *fakeListener) Close() error   { close(f.done); return nil }
func (f *fakeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443} }

type addrConn struct {
	net.Conn
	addr net.Addr
}

func (c addrConn) RemoteAddr() net.Addr { return c.addr }

// Many connections from one address that never send a ClientHello (slow-loris):
// only PendingPerIP of them are kept, the rest are closed without being read.
func TestRedTeam_Relay_SlowlorisFromOneAddressIsClosedWithoutWork(t *testing.T) {
	srv, err := NewServer(ServerConfig{
		ControlName: "relay.example.test", Suffix: "r.example.test", ControlTLS: &tls.Config{},
		HelloTimeout: time.Minute, IPLimits: IPLimits{PendingPerIP: 4, ConnsPerMinutePerIP: -1},
	})
	if err != nil {
		t.Fatal(err)
	}
	ln := &fakeListener{ch: make(chan net.Conn, 64), done: make(chan struct{})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	attacker := &net.TCPAddr{IP: net.ParseIP("203.0.113.50"), Port: 4000}
	var clients []net.Conn
	for i := 0; i < 20; i++ {
		client, server := net.Pipe()
		clients = append(clients, client)
		ln.ch <- addrConn{Conn: server, addr: attacker}
	}
	// The refused ones are closed: reading from them ends at once.
	closed := 0
	for _, c := range clients {
		_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		if _, err := c.Read(make([]byte, 1)); err == io.EOF || err == io.ErrClosedPipe {
			closed++
		}
	}
	if closed != 16 {
		t.Fatalf("%d of 20 connections were closed, want 16 (4 kept waiting)", closed)
	}
	if srv.RejectedByLimits() != 16 {
		t.Fatalf("RejectedByLimits = %d", srv.RejectedByLimits())
	}
	// Another address is not affected.
	client, server := net.Pipe()
	defer client.Close()
	ln.ch <- addrConn{Conn: server, addr: &net.TCPAddr{IP: net.ParseIP("203.0.113.51"), Port: 1}}
	_ = client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := client.Read(make([]byte, 1)); err == io.EOF || err == io.ErrClosedPipe {
		t.Fatal("a different address was refused")
	}
	for _, c := range clients {
		c.Close()
	}
}

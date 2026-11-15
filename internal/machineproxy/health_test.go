package machineproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// switchableRelay lets a test take the machine away (dials go nowhere) and
// bring it back, and counts dials.
func switchableRelay(t *testing.T, fm *fakeMachine) (down *atomic.Bool, dials *atomic.Int32) {
	t.Helper()
	useRelay(t, fm.relayAddr) // restores newTransport on cleanup
	down, dials = &atomic.Bool{}, &atomic.Int32{}
	newTransport = func(m Machine) (*http.Transport, error) {
		tr, err := pinnedTransport(m)
		if err != nil {
			return nil, err
		}
		tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			dials.Add(1)
			addr := fm.relayAddr
			if down.Load() {
				addr = "127.0.0.1:1" // nothing listens
			}
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}
		return tr, nil
	}
	return down, dials
}

func serveProxy(t *testing.T, fm *fakeMachine) *running {
	t.Helper()
	p, err := New(Config{Machine: fm.m, Token: func() string { return testToken }, Gate: testGate})
	if err != nil {
		t.Fatal(err)
	}
	lns, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = p.Serve(lns...) }()
	t.Cleanup(func() { _ = p.Close() })
	for p.Origin() == "" {
		time.Sleep(time.Millisecond)
	}
	return &running{p: p, local: &seen{}}
}

// An asleep machine: after a few failed requests the page's API calls are
// answered at once (no dial through the relay), page loads still try, and the
// first answer from the machine ends the pause.
func TestProxy_UnreachableMachineIsAnsweredAtOnceUntilItAnswers(t *testing.T) {
	fm := startMachine(t)
	down, dials := switchableRelay(t, fm)
	r := serveProxy(t, fm)
	c := windowClient(t, r)
	r.p.rp.Transport.(*http.Transport).CloseIdleConnections()

	down.Store(true)
	for i := 0; i < unreachableAfter; i++ {
		if resp, _ := get(t, c, r.p.Origin()+"/api/agent/sessions", nil); resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("request %d: %d", i, resp.StatusCode)
		}
	}
	before := dials.Load()
	resp, body := get(t, c, r.p.Origin()+"/api/agent/sessions", nil)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "Studio is not reachable") || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("paused answer: %d %q %v", resp.StatusCode, body, resp.Header)
	}
	if dials.Load() != before {
		t.Fatal("a paused API call still dialed the relay")
	}

	// The machine wakes up; a page load (the error page's Try again) gets
	// through during the pause and ends it.
	down.Store(false)
	if resp, _ := get(t, c, r.p.Origin()+"/agents", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("page load during the pause: %d", resp.StatusCode)
	}
	if resp, _ := get(t, c, r.p.Origin()+"/api/agent/sessions", nil); resp.StatusCode == http.StatusBadGateway {
		t.Fatal("still paused after the machine answered")
	}
}

func TestReachabilityPauseGrowsAndResets(t *testing.T) {
	var r reachability
	now := time.Unix(1000, 0)
	for i := 0; i < unreachableAfter-1; i++ {
		r.failed(now)
	}
	if r.pausedFor(now) != 0 {
		t.Fatal("paused before enough failures")
	}
	r.failed(now)
	if got := r.pausedFor(now); got != unreachablePauseMin {
		t.Fatalf("first pause %v", got)
	}
	for i := 0; i < 20; i++ {
		r.failed(now)
	}
	if got := r.pausedFor(now); got != unreachablePauseMax {
		t.Fatalf("capped pause %v", got)
	}
	r.ok()
	if r.pausedFor(now) != 0 {
		t.Fatal("still paused after an answer")
	}
}

// A slow request's log line says how it reached the machine.
func TestProxy_SlowLogSaysHowTheConnectionWasMade(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	old := warnf
	warnf = func(format string, args ...interface{}) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	t.Cleanup(func() { warnf = old })

	fm := startMachine(t)
	newTransport = transportVia("127.0.0.1:1") // nothing listens
	t.Cleanup(func() { newTransport = pinnedTransport })
	r := serveProxy(t, fm)
	c := windowClient(t, r)
	get(t, c, r.p.Origin()+"/api/agent/sessions", nil)
	mu.Lock()
	defer mu.Unlock()
	if len(lines) == 0 || !strings.Contains(lines[len(lines)-1], "(no connection") {
		t.Fatalf("log lines: %q", lines)
	}

	tr := &connTrace{got: true, reused: true}
	if tr.String() != "reused connection" {
		t.Fatal(tr.String())
	}
	t0 := time.Unix(0, 0)
	tr = &connTrace{got: true, dnsStart: t0, dnsDone: t0.Add(3 * time.Millisecond), connectStart: t0, connectDone: t0.Add(48 * time.Millisecond), tlsStart: t0, tlsDone: t0.Add(101 * time.Millisecond)}
	if got := tr.String(); got != "new connection: dns 3ms, relay 48ms, tls 101ms" {
		t.Fatal(got)
	}
}

func TestDialWithDNSFallbackUsesTheLastAddress(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	const name = "abc.r.example.test:8443"
	dnsBroken := false
	dial := dialWithDNSFallback(func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == name {
			if dnsBroken {
				return nil, &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "abc.r.example.test", IsNotFound: true}}
			}
			addr = ln.Addr().String() // "resolved"
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	})
	c, err := dial(context.Background(), "tcp", name)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	dnsBroken = true
	c, err = dial(context.Background(), "tcp", name)
	if err != nil {
		t.Fatalf("no fallback: %v", err)
	}
	c.Close()

	_, err = dial(context.Background(), "tcp", "never-seen.example.test:8443")
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Fatalf("unknown host: %v", err)
	}
}

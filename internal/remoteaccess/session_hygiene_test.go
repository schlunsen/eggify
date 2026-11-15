package remoteaccess

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// countingProvider tracks how many "agent sessions" (open listeners) exist
// at once. Eggify must never hold more than one, and every session it opens
// must be closed.
type countingProvider struct {
	mu       sync.Mutex
	open     int
	maxOpen  int
	opened   int
	closed   int
	failNext []error
	lastOC   OpenConfig
	delay    time.Duration
	lns      []*countingListener
}

type countingListener struct {
	net.Listener
	p    *countingProvider
	once sync.Once
}

func (l *countingListener) URL() string    { return "https://hygiene.fake.test" }
func (l *countingListener) Region() string { return "eu" }
func (l *countingListener) Close() error {
	l.once.Do(func() {
		l.p.mu.Lock()
		l.p.open--
		l.p.closed++
		l.p.mu.Unlock()
	})
	return l.Listener.Close()
}

func (p *countingProvider) Name() string { return "fake" }

func (p *countingProvider) Open(ctx context.Context, oc OpenConfig) (Listener, error) {
	p.mu.Lock()
	p.lastOC = oc
	var err error
	if len(p.failNext) > 0 {
		err, p.failNext = p.failNext[0], p.failNext[1:]
	}
	delay := p.delay
	p.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	ln, lerr := net.Listen("tcp", "127.0.0.1:0")
	if lerr != nil {
		return nil, lerr
	}
	l := &countingListener{Listener: ln, p: p}
	p.mu.Lock()
	p.open++
	p.opened++
	if p.open > p.maxOpen {
		p.maxOpen = p.open
	}
	p.lns = append(p.lns, l)
	p.mu.Unlock()
	return l, nil
}

func (p *countingProvider) snapshot() (open, maxOpen, opened, closed int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.open, p.maxOpen, p.opened, p.closed
}

func (p *countingProvider) last() *countingListener {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.lns) == 0 {
		return nil
	}
	return p.lns[len(p.lns)-1]
}

func assertHygiene(t *testing.T, p *countingProvider, wantOpen int) {
	t.Helper()
	waitFor(t, fmt.Sprintf("%d open session(s)", wantOpen), func() bool {
		open, _, _, _ := p.snapshot()
		return open == wantOpen
	})
	open, maxOpen, opened, closed := p.snapshot()
	if maxOpen > 1 {
		t.Fatalf("Eggify held %d agent sessions at once (opened %d, closed %d)", maxOpen, opened, closed)
	}
	if opened-closed != open {
		t.Fatalf("opened %d, closed %d, open %d", opened, closed, open)
	}
}

func TestHygieneStartIsIdempotent(t *testing.T) {
	p := &countingProvider{}
	m := newTestManager(t, p, httpServe, "tok_1234567890")
	for i := 0; i < 5; i++ {
		if err := m.Start(); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
	for i := 0; i < 3; i++ {
		_ = m.Start()
	}
	time.Sleep(50 * time.Millisecond)
	assertHygiene(t, p, 1)
	if _, _, opened, _ := p.snapshot(); opened != 1 {
		t.Fatalf("Start while running opened %d sessions", opened)
	}
	_ = m.Stop()
	assertHygiene(t, p, 0)
}

func TestHygieneStopClosesSession(t *testing.T) {
	p := &countingProvider{}
	m := newTestManager(t, p, httpServe, "tok_1234567890")
	_ = m.Start()
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
	_ = m.Stop()
	_, _, opened, closed := p.snapshot()
	if opened != 1 || closed != 1 {
		t.Fatalf("Stop must close the session: opened %d closed %d", opened, closed)
	}
}

func TestHygieneConcurrentStartStop(t *testing.T) {
	p := &countingProvider{delay: 2 * time.Millisecond}
	m := newTestManager(t, p, httpServe, "tok_1234567890")
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if (i+g)%2 == 0 {
					_ = m.Start()
				} else {
					_ = m.Stop()
				}
			}
		}(g)
	}
	wg.Wait()
	_ = m.Stop()
	assertHygiene(t, p, 0)
}

func TestHygieneReconnectNeverOverlaps(t *testing.T) {
	p := &countingProvider{failNext: []error{errors.New("net down"), errors.New("net down")}}
	m := newTestManager(t, p, httpServe, "tok_1234567890")
	_ = m.Start()
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
	// Drop the tunnel a few times; the manager re-opens it each time.
	for i := 0; i < 3; i++ {
		l := p.last()
		_ = l.Listener.Close() // the tunnel dies under the server (not via Close)
		waitFor(t, "new session", func() bool { return p.last() != l && m.Status().Status == StateConnected })
	}
	_ = m.Stop()
	_, maxOpen, opened, closed := p.snapshot()
	if maxOpen > 1 || opened != closed {
		t.Fatalf("reconnect loop leaked sessions: max %d opened %d closed %d", maxOpen, opened, closed)
	}
}

func TestHygieneSettingsChangeRestartsCleanly(t *testing.T) {
	p := &countingProvider{}
	m := newTestManager(t, p, httpServe, "tok_1234567890")
	_ = m.Start()
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
	tok2, dom := "tok_0987654321", "me.ngrok.app"
	prot := Protection{Mode: ProtectionBasic, BasicUser: "me", BasicPassword: "password123"}
	for _, u := range []Update{{AuthToken: &tok2}, {Domain: &dom}, {Protection: &prot}} {
		restarted, err := m.UpdateSettings(u)
		if err != nil || !restarted {
			t.Fatalf("update %+v: restarted=%v err=%v", u, restarted, err)
		}
		waitFor(t, "reconnected", func() bool { return m.Status().Status == StateConnected })
		assertHygiene(t, p, 1)
	}
	_ = m.Stop()
	assertHygiene(t, p, 0)
}

func TestHygienePermanentErrorLeavesNoSession(t *testing.T) {
	p := &countingProvider{failNext: []error{&PermanentError{Err: errors.New("limit"), Code: ErrCodeAgentLimit}}}
	m := newTestManager(t, p, httpServe, "tok_1234567890")
	_ = m.Start()
	waitFor(t, "error", func() bool { return m.Status().Status == StateError })
	st := m.Status()
	if st.ErrorCode != ErrCodeAgentLimit || st.Running {
		t.Fatalf("status = %+v", st)
	}
	assertHygiene(t, p, 0)
	// Try again works and still holds one session.
	_ = m.Start()
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
	assertHygiene(t, p, 1)
	if m.Status().ErrorCode != "" {
		t.Fatal("error code must clear on a new start")
	}
	_ = m.Stop()
}

// "Stop agent" from the ngrok dashboard/API: ngrok-go closes the session;
// the manager must switch off instead of reconnecting (which would just
// take another session slot).
func TestHygieneRemoteStopDoesNotReconnect(t *testing.T) {
	p := &countingProvider{}
	m := newTestManager(t, p, httpServe, "tok_1234567890")
	_ = m.Start()
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
	p.mu.Lock()
	oc := p.lastOC
	p.mu.Unlock()
	if oc.OnRemoteStop == nil {
		t.Fatal("no remote stop handler")
	}
	_ = p.last().Listener.Close() // ngrok-go closes the session itself
	go oc.OnRemoteStop()
	waitFor(t, "stopped", func() bool {
		st := m.Status()
		return st.Status == StateDisconnected && !st.Running
	})
	time.Sleep(100 * time.Millisecond) // > backoff: no reconnect may happen
	if _, _, opened, _ := p.snapshot(); opened != 1 {
		t.Fatalf("remote stop reconnected: %d sessions opened", opened)
	}
	assertHygiene(t, p, 0)
	if msg := m.Status().Message; msg == "" {
		t.Fatal("expected a message explaining the remote stop")
	}
}

func TestAgentMetadataIdentifiesInstall(t *testing.T) {
	p := &countingProvider{}
	m := newTestManager(t, p, httpServe, "tok_1234567890")
	m.cfg.Hostname = "my:mac"
	_ = m.Start()
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
	p.mu.Lock()
	md := p.lastOC.Metadata
	p.mu.Unlock()
	host, inst, nonce, ok := parseAgentMetadata(md)
	if !ok || host != "my-mac" || inst == "" || inst != m.Settings().InstallID || nonce == "" {
		t.Fatalf("metadata %q: host=%q inst=%q nonce=%q ok=%v", md, host, inst, nonce, ok)
	}
	_ = m.Stop()
	// The install id persists; the nonce changes per start.
	_ = m.Start()
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
	p.mu.Lock()
	md2 := p.lastOC.Metadata
	p.mu.Unlock()
	_, inst2, nonce2, _ := parseAgentMetadata(md2)
	if inst2 != inst || nonce2 == nonce {
		t.Fatalf("second start metadata %q (first %q)", md2, md)
	}
	_ = m.Stop()
}

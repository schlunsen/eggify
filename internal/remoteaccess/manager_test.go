package remoteaccess

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeListener is a loopback TCP listener posing as a tunnel.
type fakeListener struct {
	net.Listener
	url string
}

func (f *fakeListener) URL() string    { return f.url }
func (f *fakeListener) Region() string { return "eu" }

// fakeProvider returns scripted errors, then real loopback listeners.
type fakeProvider struct {
	mu     sync.Mutex
	errs   []error // returned by successive Open calls before succeeding
	opens  int
	lns    []*fakeListener
	lastOC OpenConfig
	block  chan struct{} // if set, Open blocks until closed or ctx done
}

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) Open(ctx context.Context, oc OpenConfig) (Listener, error) {
	p.mu.Lock()
	p.opens++
	p.lastOC = oc
	block := p.block
	var err error
	if len(p.errs) > 0 {
		err, p.errs = p.errs[0], p.errs[1:]
	}
	p.mu.Unlock()
	if block != nil {
		select {
		case <-block:
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
	p.mu.Lock()
	fl := &fakeListener{Listener: ln, url: fmt.Sprintf("https://t%d.fake.test", p.opens)}
	p.lns = append(p.lns, fl)
	p.mu.Unlock()
	return fl, nil
}

func (p *fakeProvider) openCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.opens
}

func (p *fakeProvider) last() *fakeListener {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.lns) == 0 {
		return nil
	}
	return p.lns[len(p.lns)-1]
}

// httpServe serves a tiny HTTP handler, like the Fiber app would.
func httpServe(ln net.Listener) error {
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hello "+r.RemoteAddr)
	})}
	return srv.Serve(ln)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func newTestManager(t *testing.T, p Provider, serve ServeFunc, token string) *Manager {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "remote_access.json"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(Config{Provider: p, Serve: serve, Store: store, MinBackoff: 10 * time.Millisecond, MaxBackoff: 40 * time.Millisecond})
	if token != "" {
		if _, err := m.UpdateSettings(Update{AuthToken: &token}); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func get(t *testing.T, addr string) string {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET %s: %v", addr, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestStartStopRestartLifecycle(t *testing.T) {
	p := &fakeProvider{}
	m := newTestManager(t, p, httpServe, "tok_1234567890abcd")

	if st := m.Status(); st.Status != StateDisconnected || st.Running {
		t.Fatalf("initial status = %+v", st)
	}
	var events []string
	var evMu sync.Mutex
	m.Subscribe(func(s Status) { evMu.Lock(); events = append(events, s.Status); evMu.Unlock() })

	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
	st := m.Status()
	if st.PublicURL != "https://t1.fake.test" || st.Region != "eu" || st.ConnectedSince == nil || !st.Running {
		t.Fatalf("connected status = %+v", st)
	}
	if got := get(t, p.last().Addr().String()); !strings.HasPrefix(got, "hello ") {
		t.Fatalf("tunnel not served: %q", got)
	}
	// Start while running is a no-op.
	if err := m.Start(); err != nil || p.openCount() != 1 {
		t.Fatalf("second Start: err=%v opens=%d", err, p.openCount())
	}

	addr := p.last().Addr().String()
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); st.Status != StateDisconnected || st.PublicURL != "" || st.Running {
		t.Fatalf("stopped status = %+v", st)
	}
	if _, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		t.Fatal("tunnel listener still accepting after Stop")
	}
	// Stop twice is fine.
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}

	// Restart.
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "reconnected", func() bool { return m.Status().Status == StateConnected })
	if m.Status().PublicURL != "https://t2.fake.test" {
		t.Fatalf("restart URL = %q", m.Status().PublicURL)
	}
	_ = m.Stop()

	evMu.Lock()
	defer evMu.Unlock()
	want := []string{StateConnecting, StateConnected, StateDisconnected, StateConnecting, StateConnected, StateDisconnected}
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

// The tunnel is an extra listener: closing it leaves the main one serving.
func TestTunnelIsAnAdditionalListener(t *testing.T) {
	mainLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") })}
	go func() { _ = srv.Serve(mainLn) }()
	defer srv.Close()

	p := &fakeProvider{}
	m := newTestManager(t, p, srv.Serve, "tok_1234567890abcd")
	for i := 0; i < 3; i++ {
		if err := m.Start(); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
		if got := get(t, p.last().Addr().String()); got != "ok" {
			t.Fatalf("tunnel: %q", got)
		}
		if err := m.Stop(); err != nil {
			t.Fatal(err)
		}
		if got := get(t, mainLn.Addr().String()); got != "ok" {
			t.Fatalf("main listener after stop %d: %q", i, got)
		}
	}
}

func TestReconnectWithBackoff(t *testing.T) {
	p := &fakeProvider{errs: []error{errors.New("dial tcp: no route"), errors.New("dial tcp: no route")}}
	m := newTestManager(t, p, httpServe, "tok_1234567890abcd")
	var sawRetry atomic.Bool
	m.Subscribe(func(s Status) {
		if s.Status == StateConnecting && strings.Contains(s.Message, "retrying") {
			sawRetry.Store(true)
		}
	})
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "connected after retries", func() bool { return m.Status().Status == StateConnected })
	if p.openCount() != 3 || !sawRetry.Load() {
		t.Fatalf("opens=%d sawRetry=%v", p.openCount(), sawRetry.Load())
	}
	if m.Status().Error != "" {
		t.Fatalf("error not cleared: %+v", m.Status())
	}

	// The tunnel drops (listener closed under the server): re-opened.
	_ = p.last().Listener.Close()
	waitFor(t, "re-opened after drop", func() bool {
		return p.openCount() == 4 && m.Status().Status == StateConnected
	})
	_ = m.Stop()
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	m := NewManager(Config{MinBackoff: time.Second, MaxBackoff: 8 * time.Second})
	var got []time.Duration
	for i := 1; i <= 6; i++ {
		got = append(got, m.backoff(i))
	}
	want := []time.Duration{1, 2, 4, 8, 8, 8}
	for i := range want {
		if got[i] != want[i]*time.Second {
			t.Fatalf("backoff = %v", got)
		}
	}
}

func TestPermanentErrorStopsRetrying(t *testing.T) {
	p := &fakeProvider{errs: []error{&PermanentError{Err: errors.New("ngrok rejected the authtoken (ERR_NGROK_105)")}}}
	m := newTestManager(t, p, httpServe, "tok_1234567890abcd")
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "error state", func() bool { return m.Status().Status == StateError })
	time.Sleep(60 * time.Millisecond)
	st := m.Status()
	if p.openCount() != 1 || st.Running || !strings.Contains(st.Error, "ERR_NGROK_105") {
		t.Fatalf("opens=%d status=%+v", p.openCount(), st)
	}
	// Start again after a permanent error works (e.g. after fixing the token).
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
	_ = m.Stop()
}

func TestStopWhileConnecting(t *testing.T) {
	p := &fakeProvider{block: make(chan struct{})}
	m := newTestManager(t, p, httpServe, "tok_1234567890abcd")
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "open called", func() bool { return p.openCount() == 1 })
	if st := m.Status(); st.Status != StateConnecting || !st.Running {
		t.Fatalf("status = %+v", st)
	}
	done := make(chan struct{})
	go func() { _ = m.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop blocked while connecting")
	}
	if m.Status().Status != StateDisconnected {
		t.Fatalf("status = %+v", m.Status())
	}
}

func TestStartRefusals(t *testing.T) {
	p := &fakeProvider{}
	m := newTestManager(t, p, httpServe, "")
	if err := m.Start(); !errors.Is(err, ErrNoToken) {
		t.Fatalf("no token: %v", err)
	}
	guardErr := &GuardError{Code: "user_auth_disabled", Msg: "needs user auth"}
	store, _ := NewStore(filepath.Join(t.TempDir(), "x.json"), make([]byte, 32))
	m2 := NewManager(Config{Provider: p, Serve: httpServe, Store: store, Env: EnvSettings{AuthToken: "env_token_123456"},
		Guard: func() error { return guardErr }})
	if err := m2.Start(); !errors.Is(err, guardErr) {
		t.Fatalf("guard: %v", err)
	}
	if p.openCount() != 0 {
		t.Fatal("provider opened despite refusal")
	}
}

func TestEnvTokenFallbackAndPrecedence(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "x.json"), make([]byte, 32))
	p := &fakeProvider{}
	m := NewManager(Config{Provider: p, Serve: httpServe, Store: store,
		Env: EnvSettings{AuthToken: "env_token_123456", Domain: "env.ngrok.app"}})
	v := m.View()
	if !v.TokenSet || v.TokenSource != "env" || v.TokenHint != "…3456" {
		t.Fatalf("env view = %+v", v)
	}
	tok := "stored_token_abcdef"
	if _, err := m.UpdateSettings(Update{AuthToken: &tok}); err != nil {
		t.Fatal(err)
	}
	if v := m.View(); v.TokenSource != "settings" || v.TokenHint != "…cdef" {
		t.Fatalf("stored view = %+v", v)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "open", func() bool { return p.openCount() == 1 })
	p.mu.Lock()
	oc := p.lastOC
	p.mu.Unlock()
	if oc.AuthToken != tok || oc.Domain != "env.ngrok.app" {
		t.Fatalf("open config token/domain = %q/%q", oc.AuthToken, oc.Domain)
	}
	_ = m.Stop()
}

func TestSettingsChangeRestartsRunningTunnel(t *testing.T) {
	p := &fakeProvider{}
	m := newTestManager(t, p, httpServe, "tok_1234567890abcd")
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
	auto := true
	if restarted, err := m.UpdateSettings(Update{Autostart: &auto}); err != nil || restarted {
		t.Fatalf("autostart change: restarted=%v err=%v", restarted, err)
	}
	d := "mine.ngrok.app"
	restarted, err := m.UpdateSettings(Update{Domain: &d})
	if err != nil || !restarted {
		t.Fatalf("domain change: restarted=%v err=%v", restarted, err)
	}
	waitFor(t, "reconnected", func() bool { return m.Status().Status == StateConnected && p.openCount() == 2 })
	p.mu.Lock()
	gotDomain := p.lastOC.Domain
	p.mu.Unlock()
	if gotDomain != d {
		t.Fatalf("domain = %q", gotDomain)
	}
	// Deleting the token stops the tunnel.
	if err := m.DeleteToken(); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); st.Status != StateDisconnected || m.View().TokenSet {
		t.Fatalf("after delete: %+v %+v", st, m.View())
	}
}

func TestTunnelConnsNeverLookLoopback(t *testing.T) {
	p := &fakeProvider{}
	var remote atomic.Value
	var tagged atomic.Bool
	serve := func(ln net.Listener) error {
		for {
			c, err := ln.Accept()
			if err != nil {
				return err
			}
			remote.Store(c.RemoteAddr().String())
			tagged.Store(IsTunnelConn(c))
			c.Close()
		}
	}
	m := newTestManager(t, p, serve, "tok_1234567890abcd")
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
	c, err := net.Dial("tcp", p.last().Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	waitFor(t, "accept", func() bool { return remote.Load() != nil })
	if r := remote.Load().(string); strings.HasPrefix(r, "127.") || !tagged.Load() {
		t.Fatalf("remote=%s tagged=%v: tunnel conns must not look local", r, tagged.Load())
	}
	_ = m.Stop()
}

func TestTunnelListenerCloseIdempotent(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	tl := newTunnelListener(&fakeListener{Listener: ln})
	if err := tl.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tl.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil (fasthttp Shutdown closes every listener)", err)
	}
	if _, err := tl.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after Close = %v", err)
	}
}

func TestStoreEncryptionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "remote_access.json")
	key := make([]byte, 32)
	key[0] = 7
	s, err := NewStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	in := Settings{AuthToken: "2abcSECRETtokenValue_xyz", Domain: "me.ngrok.app", Autostart: true,
		Protection: Protection{Mode: ProtectionOAuth, OAuthProvider: "google", AllowEmails: []string{"me@example.com"}}}
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	for _, leak := range []string{"SECRETtoken", "me.ngrok.app", "me@example.com", "autostart"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("plaintext %q in settings file: %s", leak, raw)
		}
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
	out, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if out.AuthToken != in.AuthToken || out.Domain != in.Domain || !out.Autostart || out.Protection.AllowEmails[0] != "me@example.com" {
		t.Fatalf("round trip = %+v", out)
	}

	// Wrong key: an error, never a silent plaintext fallback.
	other := make([]byte, 32)
	s2, _ := NewStore(path, other)
	if _, err := s2.Load(); err == nil {
		t.Fatal("loaded with the wrong key")
	}
	// Tampering (e.g. an agent flipping autostart) is detected.
	tampered := strings.Replace(string(raw), "\"ciphertext\":\"", "\"ciphertext\":\"AAAA", 1)
	_ = os.WriteFile(path, []byte(tampered), 0o600)
	if _, err := s.Load(); err == nil {
		t.Fatal("tampered file accepted")
	}
	// Missing file: empty settings.
	s3, _ := NewStore(filepath.Join(dir, "none.json"), key)
	if st, err := s3.Load(); err != nil || st.AuthToken != "" || st.Protection.Mode != ProtectionNone {
		t.Fatalf("missing file: %+v %v", st, err)
	}
}

func TestValidation(t *testing.T) {
	for _, bad := range []string{"", "short", "has space in it 123", "tab\tinside_token"} {
		if _, err := NormalizeToken(bad); err == nil {
			t.Errorf("token %q accepted", bad)
		}
	}
	if tok, err := NormalizeToken("  2abcdefghijklmnop_QRS  "); err != nil || tok != "2abcdefghijklmnop_QRS" {
		t.Errorf("token trim: %q %v", tok, err)
	}
	for in, want := range map[string]string{"": "", "https://My.ngrok.app/": "my.ngrok.app", "a.b": "a.b"} {
		if got, err := NormalizeDomain(in); err != nil || got != want {
			t.Errorf("domain %q = %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"nodot", "a.b/path", "evil.com; script-src *", ".lead.com"} {
		if _, err := NormalizeDomain(bad); err == nil {
			t.Errorf("domain %q accepted", bad)
		}
	}
	if _, err := (Protection{Mode: "oauth", OAuthProvider: "google"}).Normalize(); err == nil {
		t.Error("oauth without allow list accepted")
	}
	if _, err := (Protection{Mode: "oauth", OAuthProvider: "evil", AllowEmails: []string{"a@b.co"}}).Normalize(); err == nil {
		t.Error("unknown oauth provider accepted")
	}
	if _, err := (Protection{Mode: "basic", BasicUser: "u", BasicPassword: "short"}).Normalize(); err == nil {
		t.Error("short basic password accepted")
	}
	p, err := (Protection{Mode: "oauth", OAuthProvider: "Google", AllowDomains: []string{"@Example.com"}}).Normalize()
	if err != nil || p.OAuthProvider != "google" || p.AllowDomains[0] != "example.com" {
		t.Errorf("oauth normalize = %+v %v", p, err)
	}
}

func TestViewNeverContainsSecrets(t *testing.T) {
	p := &fakeProvider{}
	m := newTestManager(t, p, httpServe, "tok_SUPERSECRET_9876")
	prot := Protection{Mode: ProtectionBasic, BasicUser: "me", BasicPassword: "hunter2hunter2"}
	if _, err := m.UpdateSettings(Update{Protection: &prot}); err != nil {
		t.Fatal(err)
	}
	s := fmt.Sprintf("%+v %+v", m.View(), m.Status())
	if strings.Contains(s, "SUPERSECRET") || strings.Contains(s, "hunter2") {
		t.Fatalf("secret in view: %s", s)
	}
	if !m.View().Protection.BasicPasswordSet {
		t.Fatal("basic_password_set false")
	}
	// Re-saving basic protection without the password keeps it.
	prot.BasicPassword = ""
	if _, err := m.UpdateSettings(Update{Protection: &prot}); err != nil {
		t.Fatalf("resave without password: %v", err)
	}
	if m.Settings().Protection.BasicPassword != "hunter2hunter2" {
		t.Fatal("basic password lost")
	}
}

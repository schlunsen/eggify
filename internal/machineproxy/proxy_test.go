package machineproxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"

	"github.com/schlunsen/eggify/internal/relay"
)

const (
	testRelayName = "relay.test"
	testSuffix    = "r.test"
	testToken     = "paired-session-token"
	testGate      = "per-launch-gate"
)

// seen is what the fake machine received.
type seen struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (s *seen) add(r *http.Request) {
	s.mu.Lock()
	s.reqs = append(s.reqs, r.Clone(context.Background()))
	s.mu.Unlock()
}

func (s *seen) last(t *testing.T) *http.Request {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reqs) == 0 {
		t.Fatal("the machine got no request")
	}
	return s.reqs[len(s.reqs)-1]
}

func (s *seen) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

// fakeMachine is another Eggify reached through a real relay.Server: it
// answers like the server's auth (Bearer session token), sets a cookie,
// redirects, and echoes on a WebSocket.
type fakeMachine struct {
	m         Machine
	relayAddr string
	seen      *seen
	identity  *relay.Identity
	pairPin   string // pin put into minted pair links ("" = none, like ngrok)
}

func startRelay(t *testing.T) (string, *x509.CertPool) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: testRelayName}, DNSNames: []string{testRelayName},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	srv, err := relay.NewServer(relay.ServerConfig{
		ControlName: testRelayName,
		ControlTLS:  &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}},
		Suffix:      testSuffix,
		PublicPort:  8443,
		Logf:        func(string, ...any) {}, // it logs disconnects after the test ends
	})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String(), pool
}

func newIdentity(t *testing.T) *relay.Identity {
	t.Helper()
	pemKey, err := relay.GenerateIdentityPEM()
	if err != nil {
		t.Fatal(err)
	}
	id, err := relay.ParseIdentity(pemKey)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func startMachine(t *testing.T) *fakeMachine {
	t.Helper()
	relayAddr, pool := startRelay(t)
	id := newIdentity(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l, err := relay.Dial(ctx, relay.ClientConfig{Addr: relayAddr, ServerName: testRelayName, Identity: id, RootCAs: pool})
	if err != nil {
		t.Fatalf("install dial: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	fm := &fakeMachine{m: Machine{Name: "Studio", BaseURL: l.URL(), Pin: id.Pin()}, relayAddr: relayAddr, seen: &seen{}, identity: id}
	fm.pairPin = id.Pin()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/pairing", func(w http.ResponseWriter, r *http.Request) {
		fm.seen.add(r)
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Authentication required"})
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		link := "eggify://pair?u=" + url.QueryEscape(body["base_url"]) + "&c=fresh-code"
		if body["base_url"] == fm.m.BaseURL && fm.pairPin != "" {
			link += "&p=" + fm.pairPin
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"pair_url": link, "code": "fresh-code", "expires_at": time.Now().Add(5 * time.Minute)})
	})
	mux.HandleFunc("/api/auth/status", func(w http.ResponseWriter, r *http.Request) {
		fm.seen.add(r)
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session_token", Value: "remote-cookie"})
		w.Header().Set("Strict-Transport-Security", "max-age=63072000")
		_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": true})
	})
	mux.HandleFunc("/api/system/usage", func(w http.ResponseWriter, r *http.Request) {
		fm.seen.add(r)
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"cpu_percent": 42.5, "mem_used_bytes": 8 << 30, "mem_total_bytes": 16 << 30, "cores": 8, "available": true})
	})
	mux.HandleFunc("/api/auth/pairing/redeem", func(w http.ResponseWriter, r *http.Request) {
		fm.seen.add(r)
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "good-code" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "This pairing code is invalid, used or expired.", "code": "pairing_invalid"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"token": testToken, "username": "dev", "session_id": "s1", "expires_at": time.Now().Add(time.Hour)})
	})
	// The machine's own login: a wrong password, a login that needs MFA (no
	// cookie yet) and the MFA step that sets the session cookie.
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		fm.seen.add(r)
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["password"] != "right" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"mfa_required": true, "temporary_token": "tmp"})
	})
	mux.HandleFunc("/api/auth/mfa/verify", func(w http.ResponseWriter, r *http.Request) {
		fm.seen.add(r)
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "123456" {
			http.SetCookie(w, &http.Cookie{Name: "session_token", Value: strings.Repeat("e", 64)})
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session_token", Value: strings.Repeat("ab", 32), HttpOnly: true})
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "verified"})
	})
	mux.HandleFunc("/go-login", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fm.m.BaseURL+"/login?next=%2Fagents", http.StatusFound)
	})
	mux.HandleFunc("/go-lookalike", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fm.m.BaseURL+"0/x", http.StatusFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fm.seen.add(r)
		_, _ = io.WriteString(w, "page "+r.URL.Path)
	})
	// Served by fasthttp like the real server (Fiber): net/http's hijack
	// leaves tls.Conn with a cached yamux timeout error, so WebSockets
	// over the relay would break in a net/http fake but not in Eggify.
	pages := fasthttpadaptor.NewFastHTTPHandler(mux)
	up := websocket.FastHTTPUpgrader{CheckOrigin: func(ctx *fasthttp.RequestCtx) bool {
		// Like isOriginAllowed: Origin must match Host.
		u, err := url.Parse(string(ctx.Request.Header.Peek("Origin")))
		return err == nil && u.Host == string(ctx.Host())
	}}
	handler := func(ctx *fasthttp.RequestCtx) {
		if string(ctx.Path()) != "/agent/ws" {
			pages(ctx)
			return
		}
		var r http.Request
		_ = fasthttpadaptor.ConvertRequest(ctx, &r, true)
		fm.seen.add(&r)
		if string(ctx.Request.Header.Peek("Authorization")) != "Bearer "+testToken {
			ctx.SetStatusCode(http.StatusUnauthorized)
			return
		}
		_ = up.Upgrade(ctx, func(c *websocket.Conn) {
			for {
				mt, msg, err := c.ReadMessage()
				if err != nil {
					return
				}
				_ = c.WriteMessage(mt, append([]byte("echo:"), msg...))
			}
		})
	}
	go func() { _ = fasthttp.Serve(l, handler) }()
	return fm
}

// transportVia dials the test relay instead of the machine's DNS name, with
// the real pinned TLS config.
func transportVia(relayAddr string) func(Machine) (*http.Transport, error) {
	return func(m Machine) (*http.Transport, error) {
		tr, err := pinnedTransport(m)
		if err != nil {
			return nil, err
		}
		tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, relayAddr)
		}
		return tr, nil
	}
}

func useRelay(t *testing.T, relayAddr string) {
	t.Helper()
	old := newTransport
	newTransport = transportVia(relayAddr)
	t.Cleanup(func() { newTransport = old })
}

type running struct {
	p     *Proxy
	local *seen
}

func startProxy(t *testing.T, fm *fakeMachine, mutate func(*Config)) *running {
	t.Helper()
	useRelay(t, fm.relayAddr)
	local := &seen{}
	cfg := Config{
		Machine: fm.m,
		Token:   func() string { return testToken },
		Gate:    testGate,
		Local: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			local.add(r)
			_, _ = io.WriteString(w, "wails runtime")
		}),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	p, err := New(cfg)
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
	return &running{p: p, local: local}
}

// windowClient is the app's window: it went through the bootstrap, so it
// holds the gate cookie (and, like any page on 127.0.0.1, the local
// dashboard's session cookie, which must never leave this Mac).
// enterGate loads p's bootstrap page, as the desktop window does, so c holds
// the gate cookie of that proxy.
func enterGate(t *testing.T, c *http.Client, p *Proxy) {
	t.Helper()
	resp, err := c.Get(p.BootstrapURL("/agents"))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap: %d", resp.StatusCode)
	}
}

func windowClient(t *testing.T, r *running) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(r.p.BootstrapURL("/agents"))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(r.p.Origin(), "http://localhost:") {
		t.Fatalf("origin %q: proxies must not share 127.0.0.1 with the local dashboard", r.p.Origin())
	}
	// A page of its own that moves on (not a redirect): the next request is
	// then same-site and carries the gate cookie in WebKit too.
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(page), `http-equiv="refresh" content="0;url=/agents"`) {
		t.Fatalf("bootstrap: %d %q", resp.StatusCode, page)
	}
	u, _ := url.Parse(r.p.Origin())
	jar.SetCookies(u, []*http.Cookie{{Name: "session_token", Value: "LOCAL-MAC-SESSION"}})
	return c
}

func get(t *testing.T, c *http.Client, u string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestProxy_ForwardsAsPairedUserThroughRelay(t *testing.T) {
	fm := startMachine(t)
	r := startProxy(t, fm, nil)
	c := windowClient(t, r)

	resp, body := get(t, c, r.p.Origin()+"/api/auth/status", map[string]string{
		"Authorization": "Bearer LOCAL-API-KEY",
		"Origin":        r.p.Origin(),
		"Referer":       r.p.Origin() + "/agents",
	})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "authenticated") {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
	got := fm.seen.last(t)
	if got.Header.Get("Authorization") != "Bearer "+testToken {
		t.Errorf("Authorization = %q, want the paired token only", got.Header.Get("Authorization"))
	}
	if got.Header.Get("Cookie") != "" {
		t.Errorf("local cookies reached the machine: %q", got.Header.Get("Cookie"))
	}
	if got.Header.Get("Referer") != "" {
		t.Errorf("Referer forwarded: %q", got.Header.Get("Referer"))
	}
	want, _ := url.Parse(fm.m.BaseURL)
	if got.Host != want.Host {
		t.Errorf("Host = %q, want %q", got.Host, want.Host)
	}
	if o := got.Header.Get("Origin"); o != "https://"+want.Host {
		t.Errorf("Origin = %q, want the machine's own", o)
	}
	for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded"} {
		if got.Header.Get(h) != "" {
			t.Errorf("%s forwarded: %q", h, got.Header.Get(h))
		}
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Errorf("machine's Set-Cookie reached the window: %q", resp.Header.Get("Set-Cookie"))
	}
	if resp.Header.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS reached the loopback host")
	}
}

func TestProxy_RefusesWithoutGateCookie(t *testing.T) {
	fm := startMachine(t)
	r := startProxy(t, fm, nil)
	// Any other local process (an agent with the API key) has no gate cookie.
	c := &http.Client{Timeout: 5 * time.Second}
	for _, p := range []string{"/", "/api/auth/status", "/wails/runtime"} {
		resp, _ := get(t, c, r.p.Origin()+p, map[string]string{"Authorization": "Bearer LOCAL-API-KEY"})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s without gate: %d, want 403", p, resp.StatusCode)
		}
	}
	// A wrong gate in the bootstrap sets nothing.
	resp, _ := get(t, c, r.p.Origin()+BootstrapPath+"?t=guess&next=/", nil)
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("Set-Cookie") != "" {
		t.Errorf("bootstrap with a wrong token: %d %q", resp.StatusCode, resp.Header.Get("Set-Cookie"))
	}
	if fm.seen.count() != 0 {
		t.Errorf("the machine got %d requests", fm.seen.count())
	}
	if r.local.count() != 0 {
		t.Error("the Wails handler was reached without the gate")
	}
}

func TestProxy_RefusesOtherOrigins(t *testing.T) {
	fm := startMachine(t)
	r := startProxy(t, fm, nil)
	c := windowClient(t, r)
	// Other proxies share the host and so the gate cookie; their Origin
	// gives them away, like any other page.
	for _, o := range []string{"http://127.0.0.1:3335", "http://localhost:3335", "http://localhost:1", "null"} {
		resp, _ := get(t, c, r.p.Origin()+"/api/auth/status", map[string]string{"Origin": o})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("Origin %s: %d, want 403", o, resp.StatusCode)
		}
	}
	if fm.seen.count() != 0 {
		t.Fatal("cross-origin request reached the machine")
	}
}

func TestProxy_KeepsDesktopPathsLocal(t *testing.T) {
	fm := startMachine(t)
	r := startProxy(t, fm, nil)
	c := windowClient(t, r)

	resp, body := get(t, c, r.p.Origin()+"/wails/runtime", nil)
	if resp.StatusCode != http.StatusOK || body != "wails runtime" || r.local.count() != 1 {
		t.Fatalf("/wails: %d %q (local %d)", resp.StatusCode, body, r.local.count())
	}
	for _, p := range []string{"/__desktop/bootstrap", "/internal/eggify/loop/start", "/INTERNAL/x", "/a/../internal/x", "/%2e%2e/internal", "//api/x", "/api%2fx"} {
		resp, _ := get(t, c, r.p.Origin()+p, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, resp.StatusCode)
		}
	}
	if fm.seen.count() != 0 {
		t.Errorf("desktop/internal paths reached the machine (%d)", fm.seen.count())
	}
}

func TestProxy_StripsTokenQuery(t *testing.T) {
	fm := startMachine(t)
	r := startProxy(t, fm, nil)
	c := windowClient(t, r)
	get(t, c, r.p.Origin()+"/agents?token=LOCAL-API-KEY&x=1", nil)
	got := fm.seen.last(t)
	if got.URL.Query().Has("token") || got.URL.Query().Get("x") != "1" {
		t.Fatalf("query = %q", got.URL.RawQuery)
	}
}

func TestProxy_RewritesRedirectsToItself(t *testing.T) {
	fm := startMachine(t)
	r := startProxy(t, fm, nil)
	c := windowClient(t, r)
	resp, _ := get(t, c, r.p.Origin()+"/go-login", nil)
	if loc := resp.Header.Get("Location"); loc != r.p.Origin()+"/login?next=%2Fagents" {
		t.Fatalf("Location = %q", loc)
	}
	// Only the machine's own origin is rewritten, not a host that starts
	// with its name.
	resp, _ = get(t, c, r.p.Origin()+"/go-lookalike", nil)
	if loc := resp.Header.Get("Location"); loc != fm.m.BaseURL+"0/x" {
		t.Fatalf("lookalike Location = %q", loc)
	}
}

func TestProxy_WebSocketCarriesPairedToken(t *testing.T) {
	fm := startMachine(t)
	r := startProxy(t, fm, nil)
	c := windowClient(t, r)
	u, _ := url.Parse(r.p.Origin())
	hdr := http.Header{}
	hdr.Set("Origin", r.p.Origin())
	hdr.Set("Cookie", cookieHeader(c.Jar.Cookies(u)))
	ws, resp, err := websocket.DefaultDialer.Dial("ws://"+u.Host+"/agent/ws?token=LOCAL-API-KEY", hdr)
	if err != nil {
		st := 0
		if resp != nil {
			st = resp.StatusCode
		}
		t.Fatalf("ws dial: %v (%d)", err, st)
	}
	defer ws.Close()
	if err := ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatal(err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil || string(msg) != `echo:{"type":"ping"}` {
		t.Fatalf("read %q %v", msg, err)
	}
	got := fm.seen.last(t)
	if got.Header.Get("Authorization") != "Bearer "+testToken || got.Header.Get("Cookie") != "" || got.URL.Query().Has("token") {
		t.Fatalf("upgrade headers: auth=%q cookie=%q query=%q", got.Header.Get("Authorization"), got.Header.Get("Cookie"), got.URL.RawQuery)
	}
}

func cookieHeader(cs []*http.Cookie) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

func TestProxy_WrongPinNeverConnects(t *testing.T) {
	fm := startMachine(t)
	impostor := newIdentity(t)
	// An address whose label matches the impostor's pin, but the relay
	// routes it to... nobody with that key. Use the real machine's address
	// with the impostor's pin: Validate refuses the pair.
	if _, err := New(Config{Machine: Machine{BaseURL: fm.m.BaseURL, Pin: impostor.Pin()}, Token: func() string { return testToken }, Gate: testGate}); err == nil {
		t.Fatal("address and pin of different keys were accepted")
	}
	// The pinned TLS check itself: dial the real machine expecting another key.
	tr, err := pinnedTransport(fm.m)
	if err != nil {
		t.Fatal(err)
	}
	tr.TLSClientConfig = PinnedTLSConfig(strings.SplitN(strings.TrimPrefix(fm.m.BaseURL, "https://"), ":", 2)[0], impostor.Pin())
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, fm.relayAddr)
	}
	if _, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get(fm.m.BaseURL + "/"); err == nil || !strings.Contains(err.Error(), "pin") {
		t.Fatalf("connected to a key other than the pinned one: %v", err)
	}
	if fm.seen.count() != 0 {
		t.Fatal("request reached the machine")
	}
}

func TestProxy_UnauthorizedIsReported(t *testing.T) {
	fm := startMachine(t)
	called := make(chan struct{}, 1)
	r := startProxy(t, fm, func(c *Config) {
		c.Token = func() string { return "revoked" }
		c.OnUnauthorized = func() { called <- struct{}{} }
	})
	c := windowClient(t, r)
	resp, _ := get(t, c, r.p.Origin()+"/api/auth/status", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", resp.StatusCode)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("OnUnauthorized not called")
	}
	// The page keeps polling; the owner is told once.
	for i := 0; i < 5; i++ {
		get(t, c, r.p.Origin()+"/api/auth/status", nil)
	}
	select {
	case <-called:
		t.Fatal("OnUnauthorized reported again within a minute")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestProxy_UnreachableMachineShowsAPage(t *testing.T) {
	fm := startMachine(t)
	r := startProxy(t, fm, nil)
	c := windowClient(t, r)
	newTransport = transportVia("127.0.0.1:1") // nothing listens
	p2, err := New(Config{Machine: fm.m, Token: func() string { return testToken }, Gate: testGate})
	if err != nil {
		t.Fatal(err)
	}
	lns, _ := Listen()
	go func() { _ = p2.Serve(lns...) }()
	defer p2.Close()
	for p2.Origin() == "" {
		time.Sleep(time.Millisecond)
	}
	enterGate(t, c, p2) // the gate is per proxy: the first proxy's cookie does not open this one
	resp, body := get(t, c, p2.Origin()+"/agents", nil)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "Studio is not reachable") {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
}

// Failed and slow requests are logged with method and path (never the query),
// so "the other Mac didn't load" leaves a trace on this Mac.
func TestProxy_LogsFailedAndSlowRequests(t *testing.T) {
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
	r := startProxy(t, fm, nil)
	c := windowClient(t, r)
	newTransport = transportVia("127.0.0.1:1") // nothing listens
	p2, err := New(Config{Machine: fm.m, Token: func() string { return testToken }, Gate: testGate})
	if err != nil {
		t.Fatal(err)
	}
	lns, _ := Listen()
	go func() { _ = p2.Serve(lns...) }()
	defer p2.Close()
	for p2.Origin() == "" {
		time.Sleep(time.Millisecond)
	}
	enterGate(t, c, p2) // the gate is per proxy: the first proxy's cookie does not open this one
	get(t, c, p2.Origin()+"/api/agent/sessions?token=secret", nil)

	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 1 || !strings.Contains(lines[0], "Machine proxy Studio: GET /api/agent/sessions failed (") {
		t.Fatalf("log lines: %q", lines)
	}
	if strings.Contains(lines[0], "secret") {
		t.Fatalf("query leaked into the log: %q", lines[0])
	}
}

// The machine chip's meter: the other Mac's CPU and memory, read through the
// relay as the paired user.
func TestProxy_FetchUsageThroughRelay(t *testing.T) {
	fm := startMachine(t)
	r := startProxy(t, fm, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	u, err := r.p.FetchUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if u.CPUPercent != 42.5 || u.MemTotalBytes != 16<<30 || !u.Available {
		t.Fatalf("%+v", u)
	}
}

func TestRedeem_ThroughRelay(t *testing.T) {
	fm := startMachine(t)
	useRelay(t, fm.relayAddr)
	link := "eggify://pair?u=" + url.QueryEscape(fm.m.BaseURL) + "&c=good-code&p=" + fm.m.Pin
	p, err := Redeem(context.Background(), link, "MacBook")
	if err != nil {
		t.Fatal(err)
	}
	if p.Token != testToken || p.Machine.Pin != fm.m.Pin || p.Machine.BaseURL != fm.m.BaseURL {
		t.Fatalf("pairing = %+v", p)
	}
	got := fm.seen.last(t)
	if got.URL.Path != "/api/auth/pairing/redeem" {
		t.Fatalf("path %q", got.URL.Path)
	}

	bad := "eggify://pair?u=" + url.QueryEscape(fm.m.BaseURL) + "&c=used&p=" + fm.m.Pin
	if _, err := Redeem(context.Background(), bad, "MacBook"); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("used code: %v", err)
	}
}

func TestParsePairLink(t *testing.T) {
	id := newIdentity(t)
	base := "https://" + id.ID() + ".r.example.com"
	ok := "eggify://pair?u=" + url.QueryEscape(base) + "&c=abc&p=" + id.Pin()
	m, code, err := ParsePairLink(ok)
	if err != nil || code != "abc" || m.BaseURL != base {
		t.Fatalf("%+v %q %v", m, code, err)
	}
	other := newIdentity(t)
	for name, link := range map[string]string{
		"no pin (ngrok)":     "eggify://pair?u=https%3A%2F%2Fx.ngrok.app&c=abc",
		"pin of another key": "eggify://pair?u=" + url.QueryEscape(base) + "&c=abc&p=" + other.Pin(),
		"http":               "eggify://pair?u=" + url.QueryEscape("http://"+id.ID()+".r.example.com") + "&c=abc&p=" + id.Pin(),
		"path":               "eggify://pair?u=" + url.QueryEscape(base+"/x") + "&c=abc&p=" + id.Pin(),
		"no code":            "eggify://pair?u=" + url.QueryEscape(base) + "&p=" + id.Pin(),
		"not pair":           "eggify://agents?u=" + url.QueryEscape(base) + "&c=abc&p=" + id.Pin(),
		"https link":         "https://pair?u=" + url.QueryEscape(base) + "&c=abc&p=" + id.Pin(),
	} {
		if _, _, err := ParsePairLink(link); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestProxy_BootstrapSetsExtraCookies(t *testing.T) {
	fm := startMachine(t)
	r := startProxy(t, fm, func(c *Config) {
		c.BootstrapCookies = []*http.Cookie{{Name: "eggify_desktop", Value: "wails", Path: "/"}}
	})
	c := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, _ := get(t, c, r.p.BootstrapURL("/x"), nil)
	var names []string
	for _, ck := range resp.Cookies() {
		names = append(names, ck.Name)
		if ck.Name == r.p.GateCookieName() && (!ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode) {
			t.Errorf("gate cookie must be HttpOnly + SameSite=Lax: %+v", ck)
		}
	}
	if strings.Join(names, ",") != r.p.GateCookieName()+",eggify_desktop" {
		t.Fatalf("cookies = %v", names)
	}
}

func TestProxy_BootstrapNextCannotInjectMarkup(t *testing.T) {
	fm := startMachine(t)
	r := startProxy(t, fm, nil)
	c := &http.Client{Timeout: 5 * time.Second}
	_, body := get(t, c, r.p.Origin()+BootstrapPath+"?t="+url.QueryEscape(testGate)+"&next="+url.QueryEscape(`/x"><script>alert(1)</script>`), nil)
	if strings.Contains(body, "<script>") {
		t.Fatalf("next was not escaped: %s", body)
	}
	_, body = get(t, c, r.p.Origin()+BootstrapPath+"?t="+url.QueryEscape(testGate)+"&next="+url.QueryEscape("//evil.example/"), nil)
	if !strings.Contains(body, "url=/\"") {
		t.Fatalf("protocol-relative next kept: %s", body)
	}
}

func TestCreatePairing_ThroughRelay(t *testing.T) {
	fm := startMachine(t)
	useRelay(t, fm.relayAddr)
	pc, err := CreatePairing(context.Background(), fm.m, testToken, "iPhone")
	if err != nil {
		t.Fatal(err)
	}
	m, code, err := ParsePairLink(pc.PairURL)
	if err != nil || code != "fresh-code" || m.BaseURL != fm.m.BaseURL || m.Pin != fm.m.Pin {
		t.Fatalf("pair link %q: %+v %q %v", pc.PairURL, m, code, err)
	}
	if pc.ExpiresAt.IsZero() {
		t.Fatal("no expiry")
	}
	got := fm.seen.last(t)
	if got.URL.Path != "/api/auth/pairing" || got.Header.Get("Authorization") != "Bearer "+testToken {
		t.Fatalf("request %s with %q", got.URL.Path, got.Header.Get("Authorization"))
	}
}

func TestCreatePairing_RefusedToken(t *testing.T) {
	fm := startMachine(t)
	useRelay(t, fm.relayAddr)
	if _, err := CreatePairing(context.Background(), fm.m, "revoked", "iPhone"); err != ErrSignedOut {
		t.Fatalf("revoked token: %v, want ErrSignedOut", err)
	}
}

// The link must point at the machine's own address and key: one without a
// pin (relay off, ngrok) or with another key's pin is never handed on.
func TestCreatePairing_RefusesLinksForOtherKeys(t *testing.T) {
	fm := startMachine(t)
	useRelay(t, fm.relayAddr)
	fm.pairPin = ""
	if _, err := CreatePairing(context.Background(), fm.m, testToken, "iPhone"); err == nil || !strings.Contains(err.Error(), "relay") {
		t.Fatalf("no pin: %v", err)
	}
	fm.pairPin = newIdentity(t).Pin()
	if _, err := CreatePairing(context.Background(), fm.m, testToken, "iPhone"); err == nil {
		t.Fatal("link with another key's pin accepted")
	}
}

// Warm opens connections through the relay ahead of a switch, without the
// paired token, and the page's next request reuses one of them.
func TestProxy_WarmOpensConnectionsWithoutCredentials(t *testing.T) {
	fm := startMachine(t)
	r := startProxy(t, fm, nil)
	before := fm.seen.count()
	if err := r.p.Warm(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	if got := fm.seen.count() - before; got != 3 {
		t.Fatalf("warm requests = %d, want 3", got)
	}
	w := fm.seen.last(t)
	if w.Method != http.MethodHead || w.URL.Path != "/api/health" || w.Header.Get("Authorization") != "" || w.Header.Get("Cookie") != "" {
		t.Fatalf("warm request = %s %s auth=%q", w.Method, w.URL.Path, w.Header.Get("Authorization"))
	}
	// The idle connections are reused: a page request opens no new one.
	var dials atomic.Int32
	tr := r.p.tr.(*http.Transport)
	inner := tr.DialContext
	tr.DialContext = func(ctx context.Context, n, a string) (net.Conn, error) {
		dials.Add(1)
		return inner(ctx, n, a)
	}
	c := windowClient(t, r)
	if resp, _ := get(t, c, r.p.Origin()+"/agents", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if dials.Load() != 0 {
		t.Fatalf("the page request dialed %d new connections", dials.Load())
	}
}

func TestProxy_WarmReportsAnUnreachableMachineAndKeepWarmStops(t *testing.T) {
	fm := startMachine(t)
	useRelay(t, fm.relayAddr)
	newTransport = transportVia("127.0.0.1:1") // nothing listens
	p, err := New(Config{Machine: fm.m, Token: func() string { return testToken }, Gate: testGate})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Warm(context.Background(), 2); err == nil {
		t.Fatal("warm of an unreachable machine succeeded")
	}
	done := make(chan struct{})
	go func() { p.KeepWarm(5*time.Millisecond, 1); close(done) }()
	time.Sleep(30 * time.Millisecond)
	_ = p.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("KeepWarm did not stop on Close")
	}
}

// A machine that signed this Mac out shows its login page in the window.
// Signing in there must reconnect: the session cookie never reaches the
// window, so the proxy hands it to OnSignIn (which becomes the token). Before,
// it was dropped and the login page came back after every successful login.
func TestProxy_SignInOnTheMachineReplacesTheToken(t *testing.T) {
	fm := startMachine(t)
	var mu sync.Mutex
	token := "revoked"
	var signIns []string
	r := startProxy(t, fm, func(c *Config) {
		c.Token = func() string { mu.Lock(); defer mu.Unlock(); return token }
		c.OnSignIn = func(tok string) {
			mu.Lock()
			defer mu.Unlock()
			token = tok
			signIns = append(signIns, tok)
		}
	})
	c := windowClient(t, r)
	post := func(path, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, r.p.Origin()+path, strings.NewReader(body))
		req.Header.Set("Origin", r.p.Origin())
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp
	}

	// Wrong password, the MFA prompt, a wrong code (even with a cookie): no change.
	post("/api/auth/login", `{"username":"dev","password":"wrong"}`)
	post("/api/auth/login", `{"username":"dev","password":"right"}`)
	post("/api/auth/mfa/verify", `{"temporary_token":"tmp","code":"000000"}`)
	mu.Lock()
	if len(signIns) != 0 {
		mu.Unlock()
		t.Fatalf("token replaced before a finished sign-in: %v", signIns)
	}
	mu.Unlock()

	resp := post("/api/auth/mfa/verify", `{"temporary_token":"tmp","code":"123456"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify: %d", resp.StatusCode)
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Errorf("the machine's cookie reached the window: %q", resp.Header.Get("Set-Cookie"))
	}
	want := strings.Repeat("ab", 32)
	mu.Lock()
	got := append([]string(nil), signIns...)
	mu.Unlock()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("OnSignIn calls %v, want the new session token once", got)
	}

	// The page's next request goes out as the new session.
	get(t, c, r.p.Origin()+"/api/agents", nil)
	if h := fm.seen.last(t).Header.Get("Authorization"); h != "Bearer "+want {
		t.Fatalf("next request sent %q, want the new token", h)
	}
}

func TestSignInToken_OnlyFinishedSignIns(t *testing.T) {
	tok := strings.Repeat("ab", 32)
	mk := func(method, path string, status int, cookie *http.Cookie) *http.Response {
		req, _ := http.NewRequest(method, "https://x"+path, nil)
		h := http.Header{}
		if cookie != nil {
			h.Add("Set-Cookie", cookie.String())
		}
		return &http.Response{Request: req, StatusCode: status, Header: h}
	}
	ok := &http.Cookie{Name: "session_token", Value: tok}
	cases := []struct {
		name string
		resp *http.Response
		want string
	}{
		{"password login", mk("POST", "/api/auth/login", 200, ok), tok},
		{"mfa step", mk("POST", "/api/auth/mfa/verify", 200, ok), tok},
		{"failed", mk("POST", "/api/auth/mfa/verify", 401, ok), ""},
		{"GET", mk("GET", "/api/auth/login", 200, ok), ""},
		{"other path", mk("POST", "/api/auth/status", 200, ok), ""},
		{"logout clears", mk("POST", "/api/auth/login", 200, &http.Cookie{Name: "session_token", Value: tok, MaxAge: -1}), ""},
		{"other cookie", mk("POST", "/api/auth/login", 200, &http.Cookie{Name: "other", Value: tok}), ""},
		{"not a token", mk("POST", "/api/auth/login", 200, &http.Cookie{Name: "session_token", Value: "short"}), ""},
		{"no cookie", mk("POST", "/api/auth/login", 200, nil), ""},
	}
	for _, tc := range cases {
		if got := signInToken(tc.resp); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

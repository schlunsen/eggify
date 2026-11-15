package machineproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/schlunsen/eggify/internal/relay"
)

// No sockets: the proxy is driven through ServeHTTP with a recorder and a fake
// transport, its origin set as Serve would.
type countingTransport struct{ n atomic.Int32 }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("machine")), Request: r}, nil
}

func offlineProxy(t *testing.T, origin string) (*Proxy, *countingTransport, *int) {
	t.Helper()
	pemKey, err := relay.GenerateIdentityPEM()
	if err != nil {
		t.Fatal(err)
	}
	id, err := relay.ParseIdentity(pemKey)
	if err != nil {
		t.Fatal(err)
	}
	rid, _ := relay.IDFromPin(id.Pin())
	tr := &countingTransport{}
	local := 0
	p, err := New(Config{
		Machine: Machine{Name: "Studio", BaseURL: "https://" + rid + ".r.example.test", Pin: id.Pin()},
		Token:   func() string { return testToken }, Gate: testGate, Transport: tr,
		Local: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { local++; _, _ = io.WriteString(w, "wails") }),
	})
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.origin = origin
	p.mu.Unlock()
	return p, tr, &local
}

// gateFor is what the window holds after the bootstrap.
func gateFor(t *testing.T, p *Proxy) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest("GET", BootstrapPath+"?t="+testGate+"&next=/", nil))
	for _, c := range rec.Result().Cookies() {
		if c.Name == p.GateCookieName() {
			return c
		}
	}
	t.Fatalf("bootstrap set no gate cookie: %v", rec.Result().Header)
	return nil
}

func do(p *Proxy, method, path string, ck *http.Cookie, hdr map[string]string) int {
	r := httptest.NewRequest(method, path, strings.NewReader(""))
	if ck != nil {
		r.AddCookie(ck)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, r)
	return rec.Code
}

// The gate cookie is sent by every page on localhost, whatever its port. A page
// served at another port (a dev server, another machine's proxy) must not be
// able to drive this proxy with it.
func TestRedTeam_MachineProxy_PageOnAnotherPortCannotUseTheGateCookie(t *testing.T) {
	p, tr, local := offlineProxy(t, "http://localhost:3401")
	ck := gateFor(t, p)

	// Another proxy's cookie (other port, same launch gate) opens nothing here.
	other, _, _ := offlineProxy(t, "http://localhost:3402")
	if got := gateFor(t, other); got.Name == ck.Name || got.Value == ck.Value {
		t.Fatalf("two proxies share a gate cookie: %v %v", got, ck)
	}
	wrong := &http.Cookie{Name: ck.Name, Value: gateFor(t, other).Value}
	if c := do(p, "GET", "/api/auth/status", wrong, nil); c != http.StatusForbidden {
		t.Fatalf("another proxy's cookie value accepted: %d", c)
	}
	// The raw launch token is not the cookie either.
	if c := do(p, "GET", "/api/auth/status", &http.Cookie{Name: ck.Name, Value: testGate}, nil); c != http.StatusForbidden {
		t.Fatalf("raw gate accepted: %d", c)
	}

	attacker := map[string]string{"Origin": "http://localhost:3000"}
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		if c := do(p, m, "/api/agent/sessions", ck, attacker); c != http.StatusForbidden {
			t.Errorf("%s from another port: %d, want 403", m, c)
		}
	}
	if c := do(p, "GET", "/api/agent/sessions", ck, map[string]string{"Origin": "http://localhost:3000", "Sec-Fetch-Site": "same-site"}); c != http.StatusForbidden {
		t.Errorf("CORS GET from another port: %d", c)
	}
	// A simple GET (<img>, navigation) from a same-site page has no Origin but says so.
	for _, site := range []string{"same-site", "cross-site"} {
		if c := do(p, "GET", "/api/auth/status", ck, map[string]string{"Sec-Fetch-Site": site}); c != http.StatusForbidden {
			t.Errorf("GET with Sec-Fetch-Site %s: %d", site, c)
		}
	}
	// Without Origin nothing may change state, and a WebSocket needs the own Origin.
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH", "OPTIONS"} {
		if c := do(p, m, "/api/agent/sessions", ck, nil); c != http.StatusForbidden {
			t.Errorf("%s without Origin: %d, want 403", m, c)
		}
	}
	if c := do(p, "GET", "/agent/ws", ck, map[string]string{"Upgrade": "websocket", "Connection": "Upgrade"}); c != http.StatusForbidden {
		t.Errorf("WebSocket without Origin: %d", c)
	}
	if c := do(p, "GET", "/agent/ws", ck, map[string]string{"Upgrade": "websocket", "Connection": "Upgrade", "Origin": "http://localhost:3000"}); c != http.StatusForbidden {
		t.Errorf("WebSocket from another port: %d", c)
	}
	if c := do(p, "POST", "/wails/runtime", ck, attacker); c != http.StatusForbidden || *local != 0 {
		t.Errorf("/wails from another port: %d (reached %d)", c, *local)
	}
	if tr.n.Load() != 0 {
		t.Fatalf("%d requests reached the machine", tr.n.Load())
	}
}

func TestMachineProxy_TheWindowOwnRequestsStillWork(t *testing.T) {
	p, tr, local := offlineProxy(t, "http://localhost:3401")
	ck := gateFor(t, p)
	own := "http://localhost:3401"
	cases := []struct {
		method, path string
		hdr          map[string]string
	}{
		{"GET", "/agents", nil}, // old WebKit: no Fetch Metadata
		{"GET", "/agents", map[string]string{"Sec-Fetch-Site": "none"}},
		{"GET", "/api/agent/sessions", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": own}},
		{"HEAD", "/api/health", map[string]string{"Sec-Fetch-Site": "same-origin"}},
		{"POST", "/api/agent/sessions", map[string]string{"Origin": own, "Sec-Fetch-Site": "same-origin"}},
		{"PUT", "/api/x", map[string]string{"Origin": own}},
		{"GET", "/agent/ws", map[string]string{"Origin": own, "Upgrade": "websocket", "Connection": "Upgrade"}},
	}
	for _, c := range cases {
		if got := do(p, c.method, c.path, ck, c.hdr); got == http.StatusForbidden {
			t.Errorf("%s %s %v refused", c.method, c.path, c.hdr)
		}
	}
	if got := do(p, "POST", "/wails/runtime", ck, map[string]string{"Origin": own, "Sec-Fetch-Site": "same-origin"}); got != 200 || *local != 1 {
		t.Errorf("/wails/runtime: %d (local %d)", got, *local)
	}
	if tr.n.Load() == 0 {
		t.Error("nothing reached the machine")
	}
	// The bootstrap stays reachable without a cookie (it sets it) and without Origin.
	if c := do(p, "GET", BootstrapPath+"?t="+testGate+"&next=/x", nil, map[string]string{"Sec-Fetch-Site": "cross-site"}); c != 200 {
		t.Errorf("bootstrap: %d", c)
	}
}

// A page (or the machine) must never be able to carry the desktop bridge's
// marker through the proxy to anything.
func TestMachineProxy_StripsTheBridgeMarkerBeforeForwarding(t *testing.T) {
	p, tr, _ := offlineProxy(t, "http://localhost:3401")
	ck := gateFor(t, p)
	var seen http.Header
	p.rp.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = r.Header.Clone()
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	_ = tr
	if c := do(p, "GET", "/api/x", ck, map[string]string{BridgeMarkerHeader: "page-chosen"}); c != 200 {
		t.Fatalf("status %d", c)
	}
	if seen.Get(BridgeMarkerHeader) != "" {
		t.Fatalf("marker forwarded: %q", seen.Get(BridgeMarkerHeader))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

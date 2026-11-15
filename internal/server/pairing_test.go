package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/remoteaccess"
	"github.com/schlunsen/eggify/internal/server/principal"
	"github.com/schlunsen/eggify/internal/server/routes"
)

// pairingTestEnv is a server with the real auth routes, a real database and
// two users, served on a loopback listener.
type pairingTestEnv struct {
	t      *testing.T
	s      *Server
	app    *fiber.App
	base   string // http://127.0.0.1:<port>
	apiKey string
	alice  string // alice's session token (admin)
	bob    string // bob's session token
}

func newPairingTestEnv(t *testing.T) *pairingTestEnv {
	t.Helper()
	prev := principal.Enforced()
	t.Cleanup(func() { principal.SetEnforced(prev) })
	principal.SetEnforced(true)

	db := NewTestDB(t)
	us := NewUserStore(t.TempDir())
	us.SetDatabase(db)
	if err := us.Initialize(); err != nil {
		t.Fatal(err)
	}
	us.apiKey = "the-api-key"
	for _, u := range []struct {
		name  string
		admin bool
	}{{"alice", true}, {"bob", false}} {
		if err := us.CreateUser(u.name, "password123", u.admin); err != nil {
			t.Fatal(err)
		}
	}
	alice, err := us.createUserSession("alice", SessionMeta{DeviceName: "Safari on macOS", CreatedVia: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := us.createUserSession("bob", SessionMeta{CreatedVia: "password"})
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{db: db, userStore: us}
	app := fiber.New()
	app.Use(NewSessionAuthMiddleware(us, true, false).Handler())
	routes.RegisterAuth(app.Group("/api"), routes.AuthConfig{UserAuthEnabled: true}, &routes.AuthHandlers{
		HandleCreatePairing: s.handleCreatePairing,
		HandlePairingStatus: s.handlePairingStatus,
		HandleCancelPairing: s.handleCancelPairing,
		HandleRedeemPairing: s.handleRedeemPairing,
		HandleListSessions:  s.handleListSessions,
		HandleRevokeSession: s.handleRevokeSession,
	}, nil)
	app.Get("/api/whoami", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"kind": string(principal.From(c).Kind), "user": principal.From(c).Username})
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })

	return &pairingTestEnv{t: t, s: s, app: app, base: "http://" + ln.Addr().String(), apiKey: "the-api-key", alice: alice, bob: bob}
}

func (e *pairingTestEnv) do(method, path, bearer string, body any) (int, map[string]any) {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.base+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (e *pairingTestEnv) createPairing(bearer string) map[string]any {
	e.t.Helper()
	code, out := e.do("POST", "/api/auth/pairing", bearer, map[string]any{})
	if code != 200 {
		e.t.Fatalf("create pairing = %d %v", code, out)
	}
	return out
}

func (e *pairingTestEnv) redeem(code string) (int, map[string]any) {
	return e.do("POST", "/api/auth/pairing/redeem", "", map[string]any{
		"code": code, "device_name": "Dev's iPhone", "device_model": "iPhone17,1",
	})
}

func TestPairing_CreateRedeemAndUseSession(t *testing.T) {
	e := newPairingTestEnv(t)
	p := e.createPairing(e.alice)

	code, _ := p["code"].(string)
	raw, err := urlSafeDecodeLen(code)
	if err != nil || raw < 32 {
		t.Fatalf("code %q: want >= 32 random bytes base64url (%d, %v)", code, raw, err)
	}
	if p["base_url"] != e.base {
		t.Errorf("base_url = %v, want the request origin %s", p["base_url"], e.base)
	}
	pairURL, _ := p["pair_url"].(string)
	u, err := url.Parse(pairURL)
	if err != nil || u.Scheme != "eggify" || u.Host != "pair" || u.Query().Get("u") != e.base || u.Query().Get("c") != code {
		t.Fatalf("pair_url = %q", pairURL)
	}
	exp, _ := time.Parse(time.RFC3339, p["expires_at"].(string))
	if d := time.Until(exp); d < 4*time.Minute || d > 5*time.Minute+5*time.Second {
		t.Errorf("expires in %v, want ~5m", d)
	}
	if p["warning"] == nil {
		t.Error("a loopback base URL should carry a warning")
	}

	// Only the hash is stored.
	var stored string
	if err := e.s.db.GetDB().QueryRow(`SELECT code_hash FROM pairing_codes WHERE id = ?`, p["id"]).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != database.HashToken(code) {
		t.Fatal("pairing_codes.code_hash is not the hash of the code")
	}

	st, out := e.do("GET", "/api/auth/pairing/"+p["id"].(string)+"/status", e.alice, nil)
	if st != 200 || out["status"] != "pending" {
		t.Fatalf("status before redeem = %d %v", st, out)
	}

	st, out = e.redeem(code)
	if st != 200 {
		t.Fatalf("redeem = %d %v", st, out)
	}
	token, _ := out["token"].(string)
	if token == "" || out["username"] != "alice" || out["is_admin"] != true {
		t.Fatalf("redeem response = %v", out)
	}

	// The token is an ordinary user session: Bearer auth works and it counts
	// as a signed-in user for privileged actions.
	st, who := e.do("GET", "/api/whoami", token, nil)
	if st != 200 || who["kind"] != "user" || who["user"] != "alice" {
		t.Fatalf("whoami with the paired token = %d %v", st, who)
	}

	st, out = e.do("GET", "/api/auth/pairing/"+p["id"].(string)+"/status", e.alice, nil)
	if st != 200 || out["status"] != "redeemed" || out["device_name"] != "Dev's iPhone" {
		t.Fatalf("status after redeem = %d %v", st, out)
	}

	// The devices list shows the phone, created via pairing.
	st, out = e.do("GET", "/api/auth/sessions", e.alice, nil)
	if st != 200 {
		t.Fatalf("list sessions = %d", st)
	}
	var phoneID string
	var sawCurrent bool
	for _, raw := range out["sessions"].([]any) {
		s := raw.(map[string]any)
		if s["username"] != "alice" {
			t.Errorf("alice sees someone else's session: %v", s)
		}
		if s["created_via"] == "pairing" && s["device_name"] == "Dev's iPhone" {
			phoneID = s["id"].(string)
		}
		if s["current"] == true {
			sawCurrent = true
		}
	}
	if phoneID == "" || !sawCurrent {
		t.Fatalf("sessions = %v", out["sessions"])
	}

	// Signing the phone out revokes its token.
	if st, out := e.do("DELETE", "/api/auth/sessions/"+phoneID, e.alice, nil); st != 200 {
		t.Fatalf("revoke = %d %v", st, out)
	}
	if st, _ := e.do("GET", "/api/whoami", token, nil); st != 401 {
		t.Fatalf("revoked phone token still works: %d", st)
	}
}

func urlSafeDecodeLen(s string) (int, error) {
	b, err := base64RawURL(s)
	return len(b), err
}

// Attack: an agent with the API key mints a pairing code (a session for
// itself) or lists/revokes the user's sessions.
func TestRedTeam_Pairing_APIKeyCannotCreatePairingOrManageDevices(t *testing.T) {
	e := newPairingTestEnv(t)
	for _, rc := range []struct{ method, path string }{
		{"POST", "/api/auth/pairing"},
		{"GET", "/api/auth/pairing/x/status"},
		{"DELETE", "/api/auth/pairing/x"},
		{"GET", "/api/auth/sessions"},
		{"DELETE", "/api/auth/sessions/x"},
	} {
		st, out := e.do(rc.method, rc.path, e.apiKey, map[string]any{})
		if st != 403 || out["code"] != principal.ErrorCode {
			t.Errorf("%s %s with the API key = %d %v, want 403 user_required", rc.method, rc.path, st, out)
		}
	}
	var n int
	e.s.db.GetDB().QueryRow(`SELECT COUNT(*) FROM pairing_codes`).Scan(&n)
	if n != 0 {
		t.Fatalf("API key created %d pairing codes", n)
	}
}

// Attack: a pairing code that was seen once (a screenshot, a shoulder
// surfer, a log) is replayed, or used after it expired or was replaced.
func TestRedTeam_Pairing_ReplayExpiredAndReplacedCodesFail(t *testing.T) {
	e := newPairingTestEnv(t)

	p := e.createPairing(e.alice)
	code := p["code"].(string)
	if st, _ := e.redeem(code); st != 200 {
		t.Fatalf("first redeem = %d", st)
	}
	st, out := e.redeem(code)
	if st != 401 || out["code"] != "pairing_invalid" || out["token"] != nil {
		t.Fatalf("replayed code = %d %v, want 401 pairing_invalid", st, out)
	}
	genericMsg := out["error"]

	// Expired.
	p = e.createPairing(e.alice)
	if _, err := e.s.db.GetDB().Exec(`UPDATE pairing_codes SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Second).Unix(), p["id"]); err != nil {
		t.Fatal(err)
	}
	st, out = e.redeem(p["code"].(string))
	if st != 401 || out["error"] != genericMsg {
		t.Fatalf("expired code = %d %v", st, out)
	}
	if _, s := e.do("GET", "/api/auth/pairing/"+p["id"].(string)+"/status", e.alice, nil); s["status"] != "expired" {
		t.Errorf("expired status = %v", s)
	}

	// A new code invalidates the previous unused one.
	first := e.createPairing(e.alice)
	second := e.createPairing(e.alice)
	if st, out := e.redeem(first["code"].(string)); st != 401 || out["error"] != genericMsg {
		t.Fatalf("replaced code = %d %v", st, out)
	}
	if st, _ := e.redeem(second["code"].(string)); st != 200 {
		t.Fatalf("newest code = %d", st)
	}

	// Cancelled.
	p = e.createPairing(e.alice)
	if st, _ := e.do("DELETE", "/api/auth/pairing/"+p["id"].(string), e.alice, nil); st != 200 {
		t.Fatalf("cancel = %d", st)
	}
	if st, _ := e.redeem(p["code"].(string)); st != 401 {
		t.Fatalf("cancelled code = %d", st)
	}

	// Unknown code, and the stored hash itself (read from eggify.db).
	p = e.createPairing(e.alice)
	var stored string
	e.s.db.GetDB().QueryRow(`SELECT code_hash FROM pairing_codes WHERE id = ?`, p["id"]).Scan(&stored)
	for _, bad := range []string{"not-a-code", stored} {
		if st, out := e.redeem(bad); st != 401 || out["error"] != genericMsg {
			t.Fatalf("redeem %q = %d %v", bad, st, out)
		}
	}
}

// Another user's pairing and sessions are invisible; admins may revoke any
// session.
func TestPairing_OwnershipAndAdminRevoke(t *testing.T) {
	e := newPairingTestEnv(t)
	p := e.createPairing(e.alice)
	if st, _ := e.do("GET", "/api/auth/pairing/"+p["id"].(string)+"/status", e.bob, nil); st != 404 {
		t.Errorf("bob reads alice's pairing status: %d", st)
	}
	if st, _ := e.do("DELETE", "/api/auth/pairing/"+p["id"].(string), e.bob, nil); st != 404 {
		t.Errorf("bob cancels alice's pairing: %d", st)
	}
	aliceID := e.s.userStore.sessionIDForToken(e.alice)
	bobID := e.s.userStore.sessionIDForToken(e.bob)
	if st, _ := e.do("DELETE", "/api/auth/sessions/"+aliceID, e.bob, nil); st != 404 {
		t.Errorf("bob revokes alice's session: %d", st)
	}
	if _, err := e.s.userStore.ValidateSession(e.alice); err != nil {
		t.Fatal("alice signed out by bob")
	}
	_, out := e.do("GET", "/api/auth/sessions", e.bob, nil)
	for _, raw := range out["sessions"].([]any) {
		if raw.(map[string]any)["username"] != "bob" {
			t.Errorf("bob sees %v", raw)
		}
	}
	// Admin.
	if st, _ := e.do("DELETE", "/api/auth/sessions/"+bobID, e.alice, nil); st != 200 {
		t.Errorf("admin revoke = %d", st)
	}
	if _, err := e.s.userStore.ValidateSession(e.bob); err == nil {
		t.Error("bob's session survived an admin revoke")
	}
}

// Redeem over plain http from a non-loopback peer is refused, even when the
// request claims X-Forwarded-Proto: https.
func TestRedTeam_Pairing_RedeemRefusedOverPlainHTTP(t *testing.T) {
	e := newPairingTestEnv(t)
	p := e.createPairing(e.alice)
	body, _ := json.Marshal(map[string]string{"code": p["code"].(string)})
	req := httptest.NewRequest("POST", "/api/auth/pairing/redeem", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := e.app.Test(req) // app.Test: not TLS, not a loopback peer
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("plain http redeem = %d, want 403", resp.StatusCode)
	}
	// The code was not consumed.
	if st, _ := e.redeem(p["code"].(string)); st != 200 {
		t.Fatalf("code consumed by the refused request: %d", st)
	}
}

// The pairing code never reaches the log, on success or failure.
func TestRedTeam_Pairing_CodeNotLogged(t *testing.T) {
	logger := sharedTestLogger(t)
	e := newPairingTestEnv(t)
	p := e.createPairing(e.alice)
	code := p["code"].(string)
	e.redeem(code)
	e.redeem(code)
	tok := ""
	p2 := e.createPairing(e.alice)
	if st, out := e.redeem(p2["code"].(string)); st == 200 {
		tok, _ = out["token"].(string)
	}

	data, err := os.ReadFile(logger.GetLogFilePath())
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	if !strings.Contains(log, "Pairing "+p["id"].(string)) {
		t.Fatalf("pairing attempts are not logged")
	}
	for _, secret := range []string{code, p2["code"].(string), database.HashToken(code), tok} {
		if secret != "" && strings.Contains(log, secret) {
			t.Fatalf("a pairing secret was logged")
		}
	}
}

func TestDescribeUserAgent(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15": "Safari on macOS",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36":           "Chrome on Windows",
		"curl/8.7.1": "curl",
		"":           "",
	} {
		if got := describeUserAgent(ua); got != want {
			t.Errorf("describeUserAgent(%q) = %q, want %q", ua, got, want)
		}
	}
}

func base64RawURL(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

// Brute force: redeem is rate limited per IP.
func TestRedTeam_Pairing_RedeemIsRateLimited(t *testing.T) {
	e := newPairingTestEnv(t)
	limited := false
	for i := 0; i < 15; i++ {
		if st, _ := e.redeem("guess-" + string(rune('a'+i))); st == fiber.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("15 redeem attempts from one IP were not rate limited")
	}
}

// fakeRelayProvider poses as the Eggify relay: a loopback listener with a
// relay-style public URL.
type fakeRelayProvider struct{}

type fakeRelayListener struct{ net.Listener }

func (fakeRelayListener) URL() string    { return "https://abcdefghijklmnopqrstuvwxyz234567.r.test" }
func (fakeRelayListener) Region() string { return "relay.test" }

func (fakeRelayProvider) Name() string { return remoteaccess.ModeRelay }
func (fakeRelayProvider) Open(context.Context, remoteaccess.OpenConfig) (remoteaccess.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	return fakeRelayListener{ln}, nil
}

func TestPairing_RelayAddressCarriesTheCertificatePin(t *testing.T) {
	e := newPairingTestEnv(t)
	store, err := remoteaccess.NewStore(t.TempDir()+"/ra.json", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	m := remoteaccess.NewManager(remoteaccess.Config{
		Provider:      fakeRelayProvider{},
		RelayProvider: fakeRelayProvider{},
		// A source build has no default relay; the test supplies one.
		DefaultRelayAddr: "relay.example.com:8443",
		Serve: func(ln net.Listener) error {
			for {
				c, err := ln.Accept()
				if err != nil {
					return err
				}
				_ = c.Close()
			}
		},
		Store: store,
	})
	e.s.remoteAccess = m

	// ngrok mode: no pin (ngrok's edge presents its own certificate).
	if out := e.createPairing(e.alice); out["cert_pin"] != nil || strings.Contains(out["pair_url"].(string), "&p=") {
		t.Fatalf("pin without relay mode: %v", out)
	}

	mode := remoteaccess.ModeRelay
	if _, err := m.UpdateSettings(remoteaccess.Update{Mode: &mode}); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop() })
	deadline := time.Now().Add(5 * time.Second)
	for m.Status().Status != remoteaccess.StateConnected {
		if time.Now().After(deadline) {
			t.Fatalf("relay not connected: %+v", m.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}
	pin := m.RelayPin()
	if len(pin) != 43 {
		t.Fatalf("pin = %q", pin)
	}

	out := e.createPairing(e.alice)
	if out["base_url"] != "https://abcdefghijklmnopqrstuvwxyz234567.r.test" || out["cert_pin"] != pin {
		t.Fatalf("relay pairing = %v", out)
	}
	u, err := url.Parse(out["pair_url"].(string))
	if err != nil || u.Query().Get("p") != pin || u.Query().Get("c") == "" {
		t.Fatalf("pair_url = %v", out["pair_url"])
	}

	// Another address (e.g. typed by the user) gets no pin: its certificate
	// isn't this install's relay key.
	code, other := e.do("POST", "/api/auth/pairing", e.alice, map[string]any{"base_url": "https://other.example.com"})
	if code != 200 || other["cert_pin"] != nil || strings.Contains(other["pair_url"].(string), "&p=") {
		t.Fatalf("pin for a non-relay address: %d %v", code, other)
	}
}

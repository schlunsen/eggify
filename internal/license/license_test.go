package license

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Serials (docs/licensing-protocol.md vectors) ---

func TestSerialVectors(t *testing.T) {
	for body, want := range map[string]string{
		"0000000000000000000": "EGG-00000-00000-00000-00000",
		"ABCDEFGHJKMNPQRSTVW": "EGG-ABCDE-FGHJK-MNPQR-STVWT",
		"ZZZZZZZZZZZZZZZZZZZ": "EGG-ZZZZZ-ZZZZZ-ZZZZZ-ZZZZ0",
		"7H3K9Q2M4X8R6T1V5N0": "EGG-7H3K9-Q2M4X-8R6T1-V5N0T",
	} {
		if got := SerialFromBody(body); got != want {
			t.Errorf("SerialFromBody(%s) = %s, want %s", body, got, want)
		}
		norm, err := NormalizeSerial(want)
		if err != nil || FormatSerial(norm) != want {
			t.Errorf("NormalizeSerial(%s) = %q, %v", want, norm, err)
		}
	}
}

func TestNormalizeSerialStartingWithEGG(t *testing.T) {
	const serial = "EGG00000000000000001"
	for _, in := range []string{"EGG-EGG00-00000-00000-00001", serial, "EGGEGG00000000000000001"} {
		if got, err := NormalizeSerial(in); err != nil || got != serial {
			t.Errorf("NormalizeSerial(%q) = %q, %v", in, got, err)
		}
	}
}

func TestNormalizeSerial(t *testing.T) {
	got, err := NormalizeSerial("egg 7h3k9-q2m4x-8r6t1-v5n0t")
	if err != nil || got != "7H3K9Q2M4X8R6T1V5N0T" {
		t.Fatalf("got %q, %v", got, err)
	}
	// O→0, I/L→1, underscores dropped.
	if got, err := NormalizeSerial("EGG_7H3K9_Q2M4X_8R6TI_V5NOT"); err != nil || got != "7H3K9Q2M4X8R6T1V5N0T" {
		t.Fatalf("ambiguous letters: %q, %v", got, err)
	}
	for _, bad := range []string{
		"EGG-7H3K9-Q2M4X-8R6T1-V5N0A", // wrong check character
		"EGG-7H3K9-Q2M4X-8R6T1-V5N0",  // too short
		"EGG-7H3K9-Q2M4X-8R6T1-V5N0TT",
		"EGG-7H3K9-Q2M4X-8R6T1-V5NUT", // U is not in the alphabet
		"",
	} {
		if _, err := NormalizeSerial(bad); !errors.Is(err, ErrInvalidSerial) {
			t.Errorf("NormalizeSerial(%q) = %v, want ErrInvalidSerial", bad, err)
		}
	}
	if SerialHint("7H3K9Q2M4X8R6T1V5N0T") != "EGG-…-V5N0T" {
		t.Error("serial hint")
	}
}

// A single wrong character is caught, except 0 <-> Z in the body: their
// values are 0 and 31, the same mod 31 (the protocol's check character can't
// tell them apart; the vector ZZZ…Z → check 0 shows it).
func TestCheckCharCatchesSingleErrors(t *testing.T) {
	norm := "7H3K9Q2M4X8R6T1V5N0T"
	for i := 0; i < SerialLen; i++ {
		for _, c := range []byte(Alphabet) {
			if c == norm[i] {
				continue
			}
			if i < SerialLen-1 && (c == '0' && norm[i] == 'Z' || c == 'Z' && norm[i] == '0') {
				continue
			}
			mut := norm[:i] + string(c) + norm[i+1:]
			if _, err := NormalizeSerial(mut); err == nil {
				t.Fatalf("%s accepted", mut)
			}
		}
	}
}

// --- Tokens ---

type testKeys struct {
	serverPub  ed25519.PublicKey
	serverPriv ed25519.PrivateKey
}

func newTestKeys(t *testing.T) testKeys {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return testKeys{pub, priv}
}

func makeToken(priv ed25519.PrivateKey, c Claims) string {
	payload, _ := json.Marshal(c)
	sig := ed25519.Sign(priv, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestVerifyToken(t *testing.T) {
	k := newTestKeys(t)
	installPub, _, _ := ed25519.GenerateKey(rand.Reader)
	id := InstallID(installPub)
	now := time.Unix(1792000000, 0)
	good := Claims{V: 1, SerialID: "s1", Install: id, Plan: "standard", Features: []string{"app", "relay"},
		Machine: "Mac", IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * 24 * time.Hour).Unix()}

	c, err := verifyToken(makeToken(k.serverPriv, good), id, now, k.serverPub)
	if err != nil || c.Plan != "standard" || !c.Has("app") {
		t.Fatalf("valid token: %+v %v", c, err)
	}

	// Unknown fields are ignored.
	raw, _ := json.Marshal(map[string]any{"v": 1, "install": id, "expires_at": good.ExpiresAt, "features": []string{"app"}, "future": true})
	tok := base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(k.serverPriv, raw))
	if _, err := verifyToken(tok, id, now, k.serverPub); err != nil {
		t.Fatalf("unknown field: %v", err)
	}

	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := verifyToken(makeToken(k.serverPriv, good), InstallID(otherPub), now, k.serverPub); !errors.Is(err, ErrTokenWrongInstall) {
		t.Errorf("wrong install: %v", err)
	}
	if c, err := verifyToken(makeToken(k.serverPriv, good), id, time.Unix(good.ExpiresAt, 0), k.serverPub); !errors.Is(err, ErrTokenExpired) || c == nil {
		t.Errorf("expired at expires_at: %v", err)
	}
	v2 := good
	v2.V = 2
	if _, err := verifyToken(makeToken(k.serverPriv, v2), id, now, k.serverPub); !errors.Is(err, ErrTokenVersion) {
		t.Errorf("v2: %v", err)
	}
	// Tampered payload: another plan with the old signature.
	tok = makeToken(k.serverPriv, good)
	p, s, _ := strings.Cut(tok, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(p)
	payload = []byte(strings.Replace(string(payload), "standard", "unlimited", 1))
	if _, err := verifyToken(base64.RawURLEncoding.EncodeToString(payload)+"."+s, id, now, k.serverPub); !errors.Is(err, ErrTokenSignature) {
		t.Errorf("tampered: %v", err)
	}
	// Signed by another key.
	other := newTestKeys(t)
	if _, err := verifyToken(makeToken(other.serverPriv, good), id, now, k.serverPub); !errors.Is(err, ErrTokenSignature) {
		t.Errorf("wrong key: %v", err)
	}
	// And the built-in key never accepts a test-signed token.
	if _, err := VerifyToken(makeToken(k.serverPriv, good), id, now); !errors.Is(err, ErrTokenSignature) {
		t.Errorf("built-in key: %v", err)
	}
	for _, bad := range []string{"", "abc", "a.b.c", "!!.??"} {
		if _, err := verifyToken(bad, id, now, k.serverPub); !errors.Is(err, ErrTokenMalformed) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestServerPublicKeyIsTheSpecKey(t *testing.T) {
	if len(serverPublicKey) != 32 || base64.StdEncoding.EncodeToString(serverPublicKey) != "LbjYx4fbRqbyIzHknzQ09vRBliThR1ZH03+Dp3QgJP4=" {
		t.Fatal("server public key differs from docs/licensing-protocol.md")
	}
}

// --- Signed requests ---

func TestSignedMessageFormat(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize) // all zero: a fixed key
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	key := InstallKeyString(pub)
	if len(key) != 43 || strings.ContainsAny(key, "+/=") {
		t.Fatalf("install_key %q is not base64url without padding", key)
	}
	sum := sha256.Sum256(pub)
	if InstallID(pub) != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatal("install id")
	}

	want := "eggify-license-v1\nactivate\n" + key + "\n1792000000\n7H3K9Q2M4X8R6T1V5N0T"
	if got := SignedMessage("activate", key, 1792000000, "7H3K9Q2M4X8R6T1V5N0T"); got != want {
		t.Fatalf("message:\n%q\nwant\n%q", got, want)
	}
	if got := SignedMessage("renew", key, 5, ""); got != "eggify-license-v1\nrenew\n"+key+"\n5\n" {
		t.Fatalf("renew message %q", got)
	}
	sig, err := base64.RawURLEncoding.DecodeString(SignRequest(priv, "activate", 1792000000, "7H3K9Q2M4X8R6T1V5N0T"))
	if err != nil || !ed25519.Verify(pub, []byte(want), sig) {
		t.Fatal("signature does not verify over the message")
	}
	// The zero-seed public key is a fixed value; log it for cross-checking
	// with the Rust server's vectors.
	t.Logf("zero-seed install_key=%s install_id=%s sig=%s", key, InstallID(pub), hex.EncodeToString(sig[:8]))
}

// --- Store ---

func TestStoreEncryptsAndIs0600(t *testing.T) {
	dir := t.TempDir()
	key := make([]byte, 32)
	s, _ := NewStore(filepath.Join(dir, "license.json"), key)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	if err := s.Save(State{InstallKey: priv, Token: "secret-token", SerialHint: "EGG-…-V5N0T"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(s.Path())
	if strings.Contains(string(raw), "secret-token") || strings.Contains(string(raw), "V5N0T") {
		t.Fatal("license file is not encrypted")
	}
	if fi, _ := os.Stat(s.Path()); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	st, err := s.Load()
	if err != nil || st.Token != "secret-token" || st.PrivateKey() == nil {
		t.Fatalf("load: %+v %v", st, err)
	}
	other := make([]byte, 32)
	other[0] = 1
	s2, _ := NewStore(s.Path(), other)
	if _, err := s2.Load(); err == nil {
		t.Fatal("another key decrypted the file")
	}
}

// --- Manager against a fake license server ---

type fakeActivation struct {
	serial  string
	machine string
}

// fakeServer implements docs/licensing-protocol.md's API in-process.
type fakeServer struct {
	t     *testing.T
	keys  testKeys
	now   func() time.Time
	mu    sync.Mutex
	seats int
	known map[string]bool // normalised serials
	revok map[string]bool
	acts  map[string]fakeActivation // install id → activation
	calls map[string]int
}

func newFakeServer(t *testing.T, keys testKeys, now func() time.Time, serials ...string) (*fakeServer, *httptest.Server) {
	f := &fakeServer{t: t, keys: keys, now: now, seats: 3, known: map[string]bool{}, revok: map[string]bool{},
		acts: map[string]fakeActivation{}, calls: map[string]int{}}
	for _, s := range serials {
		n, err := NormalizeSerial(s)
		if err != nil {
			t.Fatal(err)
		}
		f.known[n] = true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/activate", func(w http.ResponseWriter, r *http.Request) { f.handle(w, r, "activate") })
	mux.HandleFunc("POST /v1/renew", func(w http.ResponseWriter, r *http.Request) { f.handle(w, r, "renew") })
	mux.HandleFunc("POST /v1/deactivate", func(w http.ResponseWriter, r *http.Request) { f.handle(w, r, "deactivate") })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv
}

func writeErr(w http.ResponseWriter, status int, code string, extra map[string]any) {
	body := map[string]any{"error": code, "message": "server says " + code}
	for k, v := range extra {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeServer) handle(w http.ResponseWriter, r *http.Request, action string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[action]++
	var req struct {
		Serial     string `json:"serial"`
		InstallKey string `json:"install_key"`
		Timestamp  int64  `json:"timestamp"`
		Signature  string `json:"signature"`
		Machine    string `json:"machine"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request", nil)
		return
	}
	if d := f.now().Unix() - req.Timestamp; d > 300 || d < -300 {
		writeErr(w, 401, "stale_timestamp", nil)
		return
	}
	pub, err := base64.RawURLEncoding.DecodeString(req.InstallKey)
	sig, err2 := base64.RawURLEncoding.DecodeString(req.Signature)
	serial := ""
	if action == "activate" {
		serial = req.Serial
	}
	msg := SignedMessage(action, req.InstallKey, req.Timestamp, serial)
	if err != nil || err2 != nil || len(pub) != 32 || !ed25519.Verify(pub, []byte(msg), sig) {
		writeErr(w, 401, "bad_signature", nil)
		return
	}
	id := InstallID(pub)
	issue := func(serial, machine string) map[string]any {
		now := f.now()
		exp := now.Add(30 * 24 * time.Hour)
		tok := makeToken(f.keys.serverPriv, Claims{V: 1, SerialID: "id-" + serial[:4], Install: id, Plan: "standard",
			Features: []string{"app", "relay"}, Machine: machine, IssuedAt: now.Unix(), ExpiresAt: exp.Unix()})
		return map[string]any{"token": tok, "expires_at": exp.Unix(), "serial_hint": SerialHint(serial)}
	}
	switch action {
	case "activate":
		n, err := NormalizeSerial(req.Serial)
		if err != nil {
			writeErr(w, 400, "invalid_serial", nil)
			return
		}
		if !f.known[n] {
			writeErr(w, 404, "unknown_serial", nil)
			return
		}
		if f.revok[n] {
			writeErr(w, 403, "serial_revoked", nil)
			return
		}
		used := 0
		var list []map[string]any
		for aid, a := range f.acts {
			if a.serial == n && aid != id {
				used++
				list = append(list, map[string]any{"machine": a.machine, "activated_at": 1, "last_seen_at": 2})
			}
		}
		if used >= f.seats {
			writeErr(w, 409, "seats_full", map[string]any{"activations": list})
			return
		}
		f.acts[id] = fakeActivation{serial: n, machine: req.Machine}
		out := issue(n, req.Machine)
		out["seats"], out["seats_used"] = f.seats, used+1
		_ = json.NewEncoder(w).Encode(out)
	case "renew":
		a, ok := f.acts[id]
		if !ok {
			writeErr(w, 404, "not_activated", nil)
			return
		}
		if f.revok[a.serial] {
			writeErr(w, 403, "serial_revoked", nil)
			return
		}
		_ = json.NewEncoder(w).Encode(issue(a.serial, a.machine))
	case "deactivate":
		delete(f.acts, id)
		_, _ = w.Write([]byte("{}"))
	}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

const testSerial = "EGG-7H3K9-Q2M4X-8R6T1-V5N0T"

func newTestManager(t *testing.T, baseURL string, keys testKeys, clk *clock, store *Store) *Manager {
	t.Helper()
	if store == nil {
		var err error
		store, err = NewStore(filepath.Join(t.TempDir(), "license.json"), make([]byte, 32))
		if err != nil {
			t.Fatal(err)
		}
	}
	return NewManager(Config{
		Store:     store,
		Client:    &Client{BaseURL: baseURL},
		Machine:   func() string { return "Test Mac" },
		Enforced:  func() bool { return true },
		Now:       clk.Now,
		publicKey: keys.serverPub,
	})
}

// closedURL is a URL nothing listens on (the server is "offline").
func closedURL(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	u := "http://" + ln.Addr().String()
	ln.Close()
	return u
}

func TestManagerActivateRenewOffline(t *testing.T) {
	keys := newTestKeys(t)
	clk := &clock{t: time.Unix(1792000000, 0)}
	fake, srv := newFakeServer(t, keys, clk.Now, testSerial)
	m := newTestManager(t, srv.URL, keys, clk, nil)

	if st := m.Status(); st.State != StateUnlicensed || !st.Required {
		t.Fatalf("fresh: %+v", st)
	}
	if m.Gate() == nil {
		t.Fatal("gate open without a license")
	}

	// A typo is caught before any request.
	if _, err := m.Activate(context.Background(), "EGG-7H3K9-Q2M4X-8R6T1-V5N0A", ""); err == nil || fake.calls["activate"] != 0 {
		t.Fatalf("bad check char: %v, calls %d", err, fake.calls["activate"])
	}
	st, err := m.Activate(context.Background(), "egg 7h3k9-q2m4x-8r6t1-v5n0t", "")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateActive || st.Machine != "Test Mac" || st.SerialHint != "EGG-…-V5N0T" || st.Plan != "standard" || st.Seats != 3 {
		t.Fatalf("after activate: %+v", st)
	}
	if m.Gate() != nil {
		t.Fatal("gate closed with a valid license")
	}

	// The state survives a restart (same store).
	m2 := newTestManager(t, srv.URL, keys, clk, m.cfg.Store)
	if m2.Status().State != StateActive {
		t.Fatalf("reloaded: %+v", m2.Status())
	}

	// Renew: a fresh token from a later time.
	clk.Add(10 * 24 * time.Hour)
	before := m.Status().ExpiresAt
	if _, err := m.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after := m.Status().ExpiresAt; !after.After(*before) {
		t.Fatalf("renew did not extend: %v → %v", before, after)
	}

	// Offline: the token keeps working until it expires.
	m.cfg.Client.BaseURL = closedURL(t)
	clk.Add(29 * 24 * time.Hour)
	_, err = m.CheckNow(context.Background())
	if !IsOffline(err) {
		t.Fatalf("offline renew: %v", err)
	}
	st = m.Status()
	if st.State != StateActive || st.RenewError == "" || m.Gate() != nil {
		t.Fatalf("offline but within 30 days: %+v", st)
	}
	clk.Add(2 * 24 * time.Hour)
	if st := m.Status(); st.State != StateExpired || m.Gate() == nil {
		t.Fatalf("offline past expiry: %+v", st)
	}

	// Back online: renewal brings it back.
	m.cfg.Client.BaseURL = srv.URL
	if st, err := m.CheckNow(context.Background()); err != nil || st.State != StateActive {
		t.Fatalf("back online: %+v %v", st, err)
	}
}

func TestManagerRevokedAndDeactivatedElsewhere(t *testing.T) {
	keys := newTestKeys(t)
	clk := &clock{t: time.Unix(1792000000, 0)}
	fake, srv := newFakeServer(t, keys, clk.Now, testSerial)
	m := newTestManager(t, srv.URL, keys, clk, nil)
	if _, err := m.Activate(context.Background(), testSerial, ""); err != nil {
		t.Fatal(err)
	}

	n, _ := NormalizeSerial(testSerial)
	fake.mu.Lock()
	fake.revok[n] = true
	fake.mu.Unlock()
	_, err := m.CheckNow(context.Background())
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != CodeSerialRevoked {
		t.Fatalf("revoked renew: %v", err)
	}
	st := m.Status()
	if st.State != StateUnlicensed || !strings.Contains(st.Error, "revoked") || m.Gate() == nil {
		t.Fatalf("after revoke: %+v", st)
	}

	// Deactivated by the admin: not_activated removes the token too.
	fake.mu.Lock()
	fake.revok[n] = false
	fake.mu.Unlock()
	if _, err := m.Activate(context.Background(), testSerial, ""); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.acts = map[string]fakeActivation{}
	fake.mu.Unlock()
	if _, err := m.CheckNow(context.Background()); err == nil {
		t.Fatal("renew of a removed activation succeeded")
	}
	if st := m.Status(); st.State != StateUnlicensed || st.Error == "" {
		t.Fatalf("after not_activated: %+v", st)
	}
}

func TestManagerSeatsFullAndDeactivate(t *testing.T) {
	keys := newTestKeys(t)
	clk := &clock{t: time.Unix(1792000000, 0)}
	_, srv := newFakeServer(t, keys, clk.Now, testSerial)
	var ms []*Manager
	for i := 0; i < 3; i++ {
		m := newTestManager(t, srv.URL, keys, clk, nil)
		if _, err := m.Activate(context.Background(), testSerial, "Mac "+string(rune('A'+i))); err != nil {
			t.Fatal(err)
		}
		ms = append(ms, m)
	}
	fourth := newTestManager(t, srv.URL, keys, clk, nil)
	_, err := fourth.Activate(context.Background(), testSerial, "Mac D")
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != CodeSeatsFull || len(ae.Activations) != 3 {
		t.Fatalf("fourth seat: %v", err)
	}
	for _, name := range []string{"Mac A", "Mac B", "Mac C", "Settings → License"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("seats_full message %q lacks %q", err.Error(), name)
		}
	}

	// Deactivating one frees its seat.
	if st, err := ms[0].Deactivate(context.Background()); err != nil || st.State != StateUnlicensed {
		t.Fatalf("deactivate: %+v %v", st, err)
	}
	if _, err := fourth.Activate(context.Background(), testSerial, "Mac D"); err != nil {
		t.Fatalf("after a deactivation: %v", err)
	}

	// Offline deactivation changes nothing (the seat would stay taken).
	ms[1].cfg.Client.BaseURL = closedURL(t)
	if _, err := ms[1].Deactivate(context.Background()); !IsOffline(err) || ms[1].Status().State != StateActive {
		t.Fatalf("offline deactivate: %v %+v", err, ms[1].Status())
	}
}

func TestManagerUnknownSerialAndCopiedToken(t *testing.T) {
	keys := newTestKeys(t)
	clk := &clock{t: time.Unix(1792000000, 0)}
	_, srv := newFakeServer(t, keys, clk.Now, testSerial)
	m := newTestManager(t, srv.URL, keys, clk, nil)
	_, err := m.Activate(context.Background(), SerialFromBody("0000000000000000000"), "")
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != CodeUnknownSerial {
		t.Fatalf("unknown serial: %v", err)
	}

	// A token copied from another install is invalid here.
	a := newTestManager(t, srv.URL, keys, clk, nil)
	if _, err := a.Activate(context.Background(), testSerial, ""); err != nil {
		t.Fatal(err)
	}
	st := m.state()
	st.Token = a.state().Token
	if err := m.commit(st); err != nil {
		t.Fatal(err)
	}
	if s := m.Status(); s.State != StateInvalid || m.Gate() == nil {
		t.Fatalf("copied token: %+v", s)
	}
}

func TestManagerNotEnforced(t *testing.T) {
	m := NewManager(Config{Enforced: func() bool { return false }})
	if m.Gate() != nil || m.Status().Required {
		t.Fatal("gate closed while not enforced")
	}
	var nilM *Manager
	if nilM.Gate() != nil {
		t.Fatal("nil manager gate")
	}
}

func TestEnforcedSwitch(t *testing.T) {
	prev := Required
	t.Cleanup(func() { Required = prev })
	Required = ""
	t.Setenv("EGGIFY_LICENSE_REQUIRED", "")
	if Enforced() {
		t.Fatal("dev build enforced")
	}
	t.Setenv("EGGIFY_LICENSE_REQUIRED", "1")
	if !Enforced() {
		t.Fatal("env switch")
	}
	t.Setenv("EGGIFY_LICENSE_REQUIRED", "")
	Required = "true"
	if !Enforced() {
		t.Fatal("ldflag switch")
	}
}

func TestBaseURL(t *testing.T) {
	old := DefaultBaseURL
	t.Cleanup(func() { DefaultBaseURL = old })
	t.Setenv("EGGIFY_LICENSE_URL", "")
	DefaultBaseURL = ""
	if BaseURLFromEnv() != "" {
		t.Fatal("a source build has no license server:", BaseURLFromEnv())
	}
	DefaultBaseURL = "https://license.example.com"
	if BaseURLFromEnv() != "https://license.example.com" {
		t.Fatal(BaseURLFromEnv())
	}
	t.Setenv("EGGIFY_LICENSE_URL", "http://127.0.0.1:8471/")
	if BaseURLFromEnv() != "http://127.0.0.1:8471" {
		t.Fatal(BaseURLFromEnv())
	}
}

// Without any server address (no ldflag, no env) activation fails in plain words
// and sends nothing.
func TestActivateWithoutServerAddress(t *testing.T) {
	old := DefaultBaseURL
	t.Cleanup(func() { DefaultBaseURL = old })
	DefaultBaseURL = ""
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&Client{}).Activate(context.Background(), priv, testSerial, "Mac")
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != CodeNoServer || !strings.Contains(err.Error(), "EGGIFY_LICENSE_URL") {
		t.Fatalf("got %v", err)
	}
}

// The background loop renews at start and retries with backoff while offline.
func TestManagerRunRenewsAndRetries(t *testing.T) {
	keys := newTestKeys(t)
	clk := &clock{t: time.Unix(1792000000, 0)}
	fake, srv := newFakeServer(t, keys, clk.Now, testSerial)
	m := newTestManager(t, srv.URL, keys, clk, nil)
	if _, err := m.Activate(context.Background(), testSerial, ""); err != nil {
		t.Fatal(err)
	}
	m.cfg.RenewEvery, m.cfg.RenewJitter = time.Hour, 0
	m.cfg.RetryMin, m.cfg.RetryMax = 10*time.Millisecond, 20*time.Millisecond
	<-m.wake // drop the activation's wake-up

	// Offline first: the loop keeps retrying.
	m.cfg.Client.BaseURL = closedURL(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for m.state().LastResult != CodeOffline && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if m.state().LastResult != CodeOffline {
		t.Fatalf("no offline attempt: %q", m.state().LastResult)
	}
	// Back online: the next retry renews.
	m.opMu.Lock()
	m.cfg.Client.BaseURL = srv.URL
	m.opMu.Unlock()
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		n := fake.calls["renew"]
		fake.mu.Unlock()
		if n > 0 && m.state().LastResult == "ok" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if m.state().LastResult != "ok" {
		t.Fatalf("not renewed after coming back: %q", m.state().LastResult)
	}
	cancel()
	<-done
}

// The relay gets the token and the install key it was issued to, only while
// the license is valid.
func TestManagerRelayCredentials(t *testing.T) {
	keys := newTestKeys(t)
	clk := &clock{t: time.Unix(1792000000, 0)}
	_, srv := newFakeServer(t, keys, clk.Now, testSerial)
	m := newTestManager(t, srv.URL, keys, clk, nil)

	if token, key := m.RelayCredentials(); token != "" || key != nil {
		t.Fatal("credentials without a license")
	}
	if _, err := m.Activate(context.Background(), testSerial, ""); err != nil {
		t.Fatal(err)
	}
	token, key := m.RelayCredentials()
	if token == "" || key == nil {
		t.Fatal("no credentials after activating")
	}
	claims, err := verifyToken(token, InstallID(key.Public().(ed25519.PublicKey)), clk.Now(), keys.serverPub)
	if err != nil || !claims.Has(FeatureApp) {
		t.Fatalf("token doesn't belong to the returned key: %v", err)
	}

	clk.t = clk.t.Add(31 * 24 * time.Hour) // offline past the token's expiry
	if token, _ := m.RelayCredentials(); token != "" {
		t.Fatal("expired token handed to the relay")
	}
}

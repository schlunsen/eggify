package license

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func activated(t *testing.T) (*Manager, *clock, testKeys, *httptest.Server) {
	t.Helper()
	keys := newTestKeys(t)
	clk := &clock{t: time.Unix(1792000000, 0)}
	_, srv := newFakeServer(t, keys, clk.Now, testSerial)
	m := newTestManager(t, srv.URL, keys, clk, nil)
	if _, err := m.Activate(context.Background(), testSerial, ""); err != nil {
		t.Fatal(err)
	}
	<-m.wake // the activation's wake-up
	return m, clk, keys, srv
}

// A failed manual check must not reset the background schedule (Run treats a
// wake-up as success), a good one does.
func TestCheckNowFailureDoesNotWakeSchedule(t *testing.T) {
	m, _, _, srv := activated(t)
	m.cfg.Client.BaseURL = closedURL(t)
	if _, err := m.CheckNow(context.Background()); !IsOffline(err) {
		t.Fatalf("want offline, got %v", err)
	}
	select {
	case <-m.wake:
		t.Fatal("a failed check woke the renewal loop (it would cancel the backoff)")
	default:
	}
	m.cfg.Client.BaseURL = srv.URL
	if _, err := m.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.wake:
	default:
		t.Fatal("a successful check should restart the schedule")
	}
}

// Turning the clock back must not revive an expired license, also after a
// restart.
func TestClockRollbackExpiresLicense(t *testing.T) {
	m, clk, keys, srv := activated(t)
	start := clk.Now()
	clk.Add(20 * 24 * time.Hour)
	if m.Gate() != nil {
		t.Fatal("gate closed with a valid license")
	}
	// A renewal attempt persists the high-water mark.
	if _, err := m.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	clk.t = start // back to the activation day
	if m.Gate() == nil {
		t.Fatal("rolled-back clock kept the license valid")
	}
	if s := m.Status(); s.State != StateExpired {
		t.Fatalf("status after rollback: %+v", s)
	}
	// Survives a restart: the mark is in the encrypted state.
	m2 := newTestManager(t, srv.URL, keys, clk, m.cfg.Store)
	if m2.Gate() == nil {
		t.Fatal("restart forgot the high-water mark")
	}
	// A few minutes of correction are tolerated.
	clk.t = start.Add(20*24*time.Hour - 3*time.Minute)
	if m2.Gate() != nil {
		t.Fatal("small clock correction locked the app")
	}
}

func brokenStore(t *testing.T) *Store {
	t.Helper()
	file := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(filepath.Join(file, "license.json"), make([]byte, 32)) // parent is a file: Save fails
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A revoked serial stops the app at once even when the file can't be written,
// and the write is retried later.
func TestRevocationAppliesWhenSaveFails(t *testing.T) {
	keys := newTestKeys(t)
	clk := &clock{t: time.Unix(1792000000, 0)}
	fake, srv := newFakeServer(t, keys, clk.Now, testSerial)
	m := newTestManager(t, srv.URL, keys, clk, nil)
	if _, err := m.Activate(context.Background(), testSerial, ""); err != nil {
		t.Fatal(err)
	}
	good := m.cfg.Store
	m.cfg.Store = brokenStore(t)
	n, _ := NormalizeSerial(testSerial)
	fake.mu.Lock()
	fake.revok[n] = true
	fake.mu.Unlock()

	if _, err := m.CheckNow(context.Background()); err == nil {
		t.Fatal("revoked renewal should fail")
	}
	if m.Gate() == nil || m.Status().State != StateUnlicensed {
		t.Fatalf("revoked license still works: %+v", m.Status())
	}
	m.mu.RLock()
	dirty := m.dirty
	m.mu.RUnlock()
	if !dirty {
		t.Fatal("failed write not remembered")
	}
	m.cfg.Store = good
	m.flush()
	st, err := good.Load()
	if err != nil || st.Token != "" {
		t.Fatalf("retry did not persist the removal: %v token=%q", err, st.Token)
	}
}

// Deactivate frees the seat on the server, so a disk error afterwards is not a
// failure of the call.
func TestDeactivateSucceedsWhenSaveFails(t *testing.T) {
	m, _, _, _ := activated(t)
	m.cfg.Store = brokenStore(t)
	st, err := m.Deactivate(context.Background())
	if err != nil {
		t.Fatalf("deactivate reported an error after the server freed the seat: %v", err)
	}
	if st.State != StateUnlicensed || m.Gate() == nil {
		t.Fatalf("still licensed after deactivate: %+v", st)
	}
}

// A slow renewal does not hold up Deactivate, and its late answer is dropped.
func TestSlowRenewDoesNotBlockDeactivate(t *testing.T) {
	keys := newTestKeys(t)
	clk := &clock{t: time.Unix(1792000000, 0)}
	_, srv := newFakeServer(t, keys, clk.Now, testSerial)
	target, _ := url.Parse(srv.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	entered, release := make(chan struct{}), make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/renew" {
			close(entered)
			<-release
		}
		proxy.ServeHTTP(w, r)
	}))
	defer slow.Close()
	m := newTestManager(t, slow.URL, keys, clk, nil)
	if _, err := m.Activate(context.Background(), testSerial, ""); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { _, err := m.CheckNow(context.Background()); errc <- err }()
	<-entered

	done := make(chan error, 1)
	go func() { _, err := m.Deactivate(context.Background()); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Deactivate blocked behind the renewal's network call")
	}
	close(release)
	if err := <-errc; err != nil {
		t.Fatalf("superseded renewal: %v", err)
	}
	if s := m.Status(); s.State != StateUnlicensed {
		t.Fatalf("the late renewal revived the license: %+v", s)
	}
}

func TestAcceptedServerKeysAndBadKeys(t *testing.T) {
	k1, k2 := newTestKeys(t), newTestKeys(t)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_ = pub
	_, priv := k2.serverPub, k2.serverPriv
	install := InstallID(pub)
	now := time.Unix(1792000000, 0)
	tok := makeToken(priv, Claims{V: 1, Install: install, Features: []string{"app"}, IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()})
	if _, err := VerifyTokenWithKeys(tok, install, now, k1.serverPub); err == nil {
		t.Fatal("token from an unknown key accepted")
	}
	if _, err := VerifyTokenWithKeys(tok, install, now, k1.serverPub, k2.serverPub); err != nil {
		t.Fatalf("rotated key not accepted: %v", err)
	}
	// Garbage keys must not panic.
	if _, err := VerifyTokenWithKeys(tok, install, now, nil, ed25519.PublicKey{1, 2}); err == nil {
		t.Fatal("bad keys accepted a token")
	}
	if _, err := VerifyTokenWithKeys(tok, install, now); err == nil {
		t.Fatal("no keys accepted a token")
	}
	if err := AddServerPublicKeys("not base64"); err == nil {
		t.Fatal("bad key accepted")
	}
	// The manager honours Config.PublicKeys.
	m := NewManager(Config{Enforced: func() bool { return true }, PublicKeys: []ed25519.PublicKey{k1.serverPub, k2.serverPub}})
	if len(m.keys()) != 2 {
		t.Fatal("PublicKeys not used")
	}
	if got := ServerPublicKeys(); len(got) == 0 || string(got[0]) != string(serverPublicKey) {
		t.Fatal("the built-in key must stay first")
	}
}

// The relay only gets credentials for a token that covers the relay.
func TestRelayCredentialsNeedRelayEntitlement(t *testing.T) {
	m, clk, keys, _ := activated(t)
	if tok, _ := m.RelayCredentials(); tok == "" {
		t.Fatal("entitled license refused")
	}
	st := m.state()
	now := clk.Now()
	until := now.Add(-time.Minute).Unix()
	mk := func(features []string, relayUntil *int64) {
		c := Claims{V: 1, Install: st.installID(), Features: features, IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(), RelayUntil: relayUntil}
		s := m.state()
		s.Token = makeToken(keys.serverPriv, c)
		m.opMu.Lock()
		_ = m.commit(s)
		m.opMu.Unlock()
	}
	mk([]string{"app"}, nil)
	if tok, _ := m.RelayCredentials(); tok != "" {
		t.Fatal("token without the relay feature handed out")
	}
	mk([]string{"app", "relay"}, &until)
	if tok, _ := m.RelayCredentials(); tok != "" {
		t.Fatal("token with an ended relay subscription handed out")
	}
	if m.Gate() != nil {
		t.Fatal("the app itself must still be licensed")
	}
}

func TestOnChangeFiresOnTokenChange(t *testing.T) {
	m, _, _, _ := activated(t)
	ch := make(chan struct{}, 4)
	m.OnChange(func() { ch <- struct{}{} })
	if _, err := m.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Same-second renewals can produce an identical token; deactivate always changes it.
	if _, err := m.Deactivate(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("OnChange not called")
	}
}

// The state-write retry tick must not push the renewal back: with a tick far
// more often than the renewal interval, renewals still happen.
func TestRunRenewsEvenWithFrequentFlushTicks(t *testing.T) {
	keys := newTestKeys(t)
	clk := &clock{t: time.Unix(1792000000, 0)}
	fake, srv := newFakeServer(t, keys, clk.Now, testSerial)
	m := newTestManager(t, srv.URL, keys, clk, nil)
	if _, err := m.Activate(context.Background(), testSerial, ""); err != nil {
		t.Fatal(err)
	}
	<-m.wake
	m.cfg.FlushEvery = 5 * time.Millisecond
	m.cfg.RenewEvery, m.cfg.RenewJitter = 120*time.Millisecond, -1
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		n := fake.calls["renew"]
		fake.mu.Unlock()
		if n >= 3 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	t.Fatalf("only %d renewals; the flush tick keeps resetting the renewal timer", fake.calls["renew"])
}

// A clock that was briefly ahead raises the mark; after it is corrected a
// successful check against the server (which says what time it is) ends the
// lockout instead of waiting for real time to catch up.
func TestCheckNowHealsAClockThatWasAhead(t *testing.T) {
	m, clk, _, _ := activated(t)
	start := clk.Now()
	clk.Add(time.Hour) // wrong clock, an hour ahead
	if m.Gate() != nil {
		t.Fatal("gate closed with a valid license")
	}
	clk.t = start // corrected
	if m.Gate() == nil {
		t.Fatal("expected the high-water mark to lock the app after the correction")
	}
	if _, err := m.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Gate(); err != nil {
		t.Fatalf("still locked after a successful check: %v", err)
	}
}

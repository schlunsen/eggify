package remoteaccess

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schlunsen/eggify/internal/relay"
)

// startTestRelay runs a relay for relay.test on loopback; only the returned
// pool trusts its certificate.
func startTestRelay(t *testing.T) (string, *x509.CertPool) { return startTestRelayWith(t, nil) }

func startTestRelayWith(t *testing.T, mut func(*relay.ServerConfig)) (string, *x509.CertPool) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "relay.test"}, DNSNames: []string{"relay.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true,
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	cfg := relay.ServerConfig{
		ControlName: "relay.test", Suffix: "r.test", Logf: t.Logf,
		ControlTLS: &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}},
	}
	if mut != nil {
		mut(&cfg)
	}
	srv, err := relay.NewServer(cfg)
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

func pinnedGet(t *testing.T, relayAddr, host, pin string) (string, error) {
	t.Helper()
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := &tls.Dialer{Config: &tls.Config{ServerName: host, InsecureSkipVerify: true,
				VerifyConnection: func(cs tls.ConnectionState) error {
					if relay.PinFromSPKI(cs.PeerCertificates[0].RawSubjectPublicKeyInfo) != pin {
						return errors.New("pin mismatch")
					}
					return nil
				}}}
			return d.DialContext(ctx, "tcp", relayAddr)
		},
	}}
	resp, err := c.Get("https://" + host + "/")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), nil
}

func TestRelayMode_ConnectsWithoutNgrokTokenAndKeepsItsIdentity(t *testing.T) {
	relayAddr, pool := startTestRelay(t)
	path := filepath.Join(t.TempDir(), "remote_access.json")
	key := make([]byte, 32)
	newManager := func() *Manager {
		store, err := NewStore(path, key)
		if err != nil {
			t.Fatal(err)
		}
		return NewManager(Config{
			Provider:      &fakeProvider{},
			RelayProvider: &RelayProvider{RootCAs: pool, ServerName: "relay.test"},
			Serve: func(ln net.Listener) error {
				return http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					io.WriteString(w, "eggify")
				}))
			},
			Store: store, MinBackoff: 10 * time.Millisecond, MaxBackoff: 40 * time.Millisecond,
		})
	}

	m := newManager()
	mode, addr := ModeRelay, relayAddr
	if _, err := m.UpdateSettings(Update{Mode: &mode}); err != nil {
		t.Fatal(err)
	}
	// Relay addresses are host names; tests reach the relay by IP through
	// the provider's ServerName, so store it directly.
	m.mu.Lock()
	m.settings.RelayAddr = addr
	m.mu.Unlock()

	if err := m.Start(); err != nil {
		t.Fatalf("start without an ngrok token: %v", err)
	}
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
	st := m.Status()
	if st.Provider != ModeRelay || !strings.HasSuffix(st.PublicURL, ".r.test") {
		t.Fatalf("status = %+v", st)
	}
	pin := m.RelayPin()
	if pin == "" || m.View().RelayPin != pin || m.View().Mode != ModeRelay {
		t.Fatalf("pin %q view %+v", pin, m.View())
	}
	host := strings.TrimPrefix(st.PublicURL, "https://")
	if body, err := pinnedGet(t, relayAddr, host, pin); err != nil || body != "eggify" {
		t.Fatalf("pinned GET: %q %v", body, err)
	}
	firstURL := st.PublicURL
	_ = m.Stop()

	// A restarted Eggify keeps its address and pin.
	m2 := newManager()
	m2.mu.Lock()
	m2.settings.RelayAddr = addr
	m2.mu.Unlock()
	if m2.RelayPin() != pin {
		t.Fatal("the relay identity was not kept")
	}
	if err := m2.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "reconnected", func() bool { return m2.Status().Status == StateConnected })
	if m2.Status().PublicURL != firstURL {
		t.Fatalf("address changed: %s → %s", firstURL, m2.Status().PublicURL)
	}
	_ = m2.Stop()
}

func TestRelayMode_ConnectionsAreMarkedAsTunnel(t *testing.T) {
	relayAddr, pool := startTestRelay(t)
	marked := make(chan bool, 1)
	store, _ := NewStore(filepath.Join(t.TempDir(), "x.json"), make([]byte, 32))
	m := NewManager(Config{
		RelayProvider: &RelayProvider{RootCAs: pool, ServerName: "relay.test"},
		Serve: func(ln net.Listener) error {
			for {
				c, err := ln.Accept()
				if err != nil {
					return err
				}
				select {
				case marked <- IsTunnelConn(c):
				default:
				}
				_ = c.Close()
			}
		},
		Store: store,
	})
	mode := ModeRelay
	if _, err := m.UpdateSettings(Update{Mode: &mode}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.settings.RelayAddr = relayAddr
	m.mu.Unlock()
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
	host := strings.TrimPrefix(m.Status().PublicURL, "https://")
	_, _ = pinnedGet(t, relayAddr, host, m.RelayPin())
	select {
	case ok := <-marked:
		if !ok {
			t.Fatal("a relayed connection was not marked as a tunnel connection")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no connection arrived")
	}
}

func TestRelayMode_SettingsValidation(t *testing.T) {
	m := newTestManager(t, &fakeProvider{}, httpServe, "")
	relayMode := ModeRelay
	if _, err := m.UpdateSettings(Update{Mode: &relayMode}); err == nil {
		t.Fatal("relay mode accepted without a relay provider")
	}
	bad := "ftp://x"
	m.cfg.RelayProvider = &RelayProvider{}
	if _, err := m.UpdateSettings(Update{RelayAddr: &bad}); err == nil {
		t.Fatal("bad relay address accepted")
	}
	for in, want := range map[string]string{
		"relay.example.com":               "relay.example.com:443",
		"https://Relay.Example.com:8443/": "relay.example.com:8443",
		"":                                "",
	} {
		if got, err := NormalizeRelayAddr(in); err != nil || got != want {
			t.Fatalf("NormalizeRelayAddr(%q) = %q, %v", in, got, err)
		}
	}
	unknown := "carrier-pigeon"
	if _, err := m.UpdateSettings(Update{Mode: &unknown}); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

// A source build has no default relay: relay mode without any address (setting,
// EGGIFY_RELAY_ADDR or ldflag) refuses to start, in plain words, and dials nothing.
func TestRelayMode_NoAddressRefusesToStart(t *testing.T) {
	old := DefaultRelayAddr
	t.Cleanup(func() { DefaultRelayAddr = old })
	DefaultRelayAddr = ""
	store, err := NewStore(filepath.Join(t.TempDir(), "remote_access.json"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(Config{
		Provider: &fakeProvider{}, RelayProvider: &RelayProvider{},
		Serve: func(net.Listener) error { return nil }, Store: store,
	})
	mode := ModeRelay
	if _, err := m.UpdateSettings(Update{Mode: &mode}); err != nil {
		t.Fatal(err)
	}
	if v := m.View(); !v.RelayAvailable || v.DefaultRelayAddr != "" {
		t.Fatalf("view = %+v", v)
	}
	if err := m.Start(); !errors.Is(err, ErrNoRelayAddr) {
		t.Fatalf("Start() = %v, want ErrNoRelayAddr", err)
	}
	if m.Status().Running {
		t.Fatal("started without an address")
	}
}

package relay

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

const testRelayName = "relay.test"
const testSuffix = "r.test"

// testRelay starts a relay on a loopback port with a certificate for
// relay.test that only the returned pool trusts.
func testRelay(t *testing.T) (*Server, string, *x509.CertPool) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: testRelayName},
		DNSNames:     []string{testRelayName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)

	srv, err := NewServer(ServerConfig{
		ControlName: testRelayName,
		ControlTLS:  &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}},
		Suffix:      testSuffix,
		PublicPort:  8443,
		Logf:        t.Logf,
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
	return srv, ln.Addr().String(), pool
}

func newIdentity(t *testing.T) *Identity {
	t.Helper()
	pemKey, err := GenerateIdentityPEM()
	if err != nil {
		t.Fatal(err)
	}
	id, err := ParseIdentity(pemKey)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func dialInstall(t *testing.T, addr string, pool *x509.CertPool, id *Identity) *Listener {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l, err := Dial(ctx, ClientConfig{Addr: addr, ServerName: testRelayName, Identity: id, RootCAs: pool})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// pinnedClient is what the iOS app does: ignore names and issuers, accept
// only the pinned key.
func pinnedClient(relayAddr, pin string) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialTLSContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				host, _, _ := net.SplitHostPort(addr)
				d := &tls.Dialer{Config: &tls.Config{
					ServerName:         host,
					InsecureSkipVerify: true, // replaced by the pin check below
					VerifyConnection: func(cs tls.ConnectionState) error {
						if len(cs.PeerCertificates) == 0 || PinFromSPKI(cs.PeerCertificates[0].RawSubjectPublicKeyInfo) != pin {
							return errors.New("pin mismatch")
						}
						return nil
					},
				}}
				return d.DialContext(ctx, "tcp", relayAddr)
			},
		},
	}
}

func serveEcho(l net.Listener) {
	_ = http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello %s from %s", r.URL.Path, r.RemoteAddr)
	}))
}

func TestRelay_VisitorReachesInstallEndToEnd(t *testing.T) {
	srv, addr, pool := testRelay(t)
	id := newIdentity(t)
	l := dialInstall(t, addr, pool, id)
	if want := "https://" + id.ID() + "." + testSuffix + ":8443"; l.URL() != want {
		t.Fatalf("URL = %q, want %q", l.URL(), want)
	}
	if srv.Installs() != 1 {
		t.Fatalf("installs = %d", srv.Installs())
	}
	go serveEcho(l)

	resp, err := pinnedClient(addr, id.Pin()).Get("https://" + id.ID() + "." + testSuffix + "/ping")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// The visitor came from loopback; installs never see a loopback peer.
	if got := string(body); !strings.HasPrefix(got, "hello /ping from 192.0.2.1") {
		t.Fatalf("body = %q", got)
	}
}

func TestRelay_WrongPinIsRefusedByClient(t *testing.T) {
	_, addr, pool := testRelay(t)
	id := newIdentity(t)
	l := dialInstall(t, addr, pool, id)
	go serveEcho(l)
	other := newIdentity(t)
	if _, err := pinnedClient(addr, other.Pin()).Get("https://" + id.ID() + "." + testSuffix + "/"); err == nil {
		t.Fatal("a client pinning another key connected")
	}
}

func TestRelay_UnknownNameIsDropped(t *testing.T) {
	_, addr, _ := testRelay(t)
	ghost := newIdentity(t)
	if _, err := pinnedClient(addr, ghost.Pin()).Get("https://" + ghost.ID() + "." + testSuffix + "/"); err == nil {
		t.Fatal("reached an install that isn't connected")
	}
}

func TestRelay_CannotClaimAnotherInstallsName(t *testing.T) {
	_, addr, pool := testRelay(t)
	victim := newIdentity(t)
	attacker := newIdentity(t)
	// Claims the victim's key, signs with its own.
	forged := &Identity{Certificate: attacker.Certificate, SPKI: victim.SPKI}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := Dial(ctx, ClientConfig{Addr: addr, ServerName: testRelayName, Identity: forged, RootCAs: pool})
	var refused *RefusedError
	if !errors.As(err, &refused) || !refused.Permanent {
		t.Fatalf("forged identity: err = %v, want a permanent refusal", err)
	}
}

func TestRelay_InstallRejectsUntrustedRelay(t *testing.T) {
	_, addr, _ := testRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// System roots don't trust the test relay's certificate.
	if _, err := Dial(ctx, ClientConfig{Addr: addr, ServerName: testRelayName, Identity: newIdentity(t)}); err == nil {
		t.Fatal("connected to a relay with an untrusted certificate")
	}
}

func TestRelay_NewestConnectionWins(t *testing.T) {
	srv, addr, pool := testRelay(t)
	id := newIdentity(t)
	first := dialInstall(t, addr, pool, id)
	second := dialInstall(t, addr, pool, id)
	select {
	case <-first.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the older session was not closed")
	}
	if _, err := first.Accept(); err == nil {
		t.Fatal("Accept on a replaced session succeeded")
	}
	go serveEcho(second)
	resp, err := pinnedClient(addr, id.Pin()).Get("https://" + id.ID() + "." + testSuffix + "/")
	if err != nil {
		t.Fatalf("get via the newer session: %v", err)
	}
	resp.Body.Close()
	if srv.Installs() != 1 {
		t.Fatalf("installs = %d", srv.Installs())
	}
}

func TestRelay_CloseEndsAccept(t *testing.T) {
	_, addr, pool := testRelay(t)
	l := dialInstall(t, addr, pool, newIdentity(t))
	_ = l.Close()
	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after Close = %v, want net.ErrClosed", err)
	}
}

func TestIdentity_RoundTripAndNames(t *testing.T) {
	pemKey, _ := GenerateIdentityPEM()
	a, err := ParseIdentity(pemKey)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ParseIdentity(pemKey)
	if a.ID() != b.ID() || a.Pin() != b.Pin() {
		t.Fatal("the same key gave different names")
	}
	if !ValidID(a.ID()) || len(a.Pin()) != 43 {
		t.Fatalf("id %q pin %q", a.ID(), a.Pin())
	}
	for _, bad := range []string{"", "short", strings.Repeat("A", IDLength), strings.Repeat("1", IDLength)} {
		if ValidID(bad) {
			t.Fatalf("ValidID(%q) = true", bad)
		}
	}
	if _, err := ParseIdentity("not a key"); err == nil {
		t.Fatal("parsed garbage")
	}
}

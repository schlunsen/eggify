package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// issueToken makes a license token the way the license server does.
func issueToken(t *testing.T, server ed25519.PrivateKey, install ed25519.PublicKey, features []string, expires time.Time, relayUntil ...time.Time) string {
	t.Helper()
	sum := sha256.Sum256(install)
	claims := map[string]any{
		"v": 1, "serial_id": "s1", "install": b64url.EncodeToString(sum[:]), "plan": "standard",
		"features": features, "machine": "test", "issued_at": time.Now().Unix(), "expires_at": expires.Unix(),
		"relay_until": nil,
	}
	if len(relayUntil) > 0 {
		claims["relay_until"] = relayUntil[0].Unix()
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return b64url.EncodeToString(payload) + "." + b64url.EncodeToString(ed25519.Sign(server, payload))
}

func tryDial(addr string, pool *x509.CertPool, id *Identity, lic *LicenseCredentials) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := ClientConfig{Addr: addr, ServerName: testRelayName, Identity: id, RootCAs: pool}
	if lic != nil {
		cfg.License = func() *LicenseCredentials { return lic }
	}
	l, err := Dial(ctx, cfg)
	if err == nil {
		_ = l.Close()
	}
	return err
}

func refusedPermanently(t *testing.T, err error) {
	t.Helper()
	var refused *RefusedError
	if !errors.As(err, &refused) || !refused.Permanent || refused.Msg != SubscribersOnlyMessage {
		t.Fatalf("want a permanent subscribers-only refusal, got %v", err)
	}
}

func TestRelay_OpenRelayAdmitsEveryone(t *testing.T) {
	_, addr, pool := testRelay(t)
	if err := tryDial(addr, pool, newIdentity(t), nil); err != nil {
		t.Fatalf("a relay without admission rules refused: %v", err)
	}
}

func TestRelay_AllowlistAdmitsListedInstallsOnly(t *testing.T) {
	srv, addr, pool := testRelay(t)
	listed, other := newIdentity(t), newIdentity(t)
	path := filepath.Join(t.TempDir(), "allowed.txt")
	if err := os.WriteFile(path, []byte("# owner's Mac\n"+listed.ID()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.cfg.Allowlist = &Allowlist{Path: path}

	if err := tryDial(addr, pool, listed, nil); err != nil {
		t.Fatalf("listed install refused: %v", err)
	}
	refusedPermanently(t, tryDial(addr, pool, other, nil))

	// The file is re-read when it changes.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte(listed.ID()+"\n"+other.ID()+"  # added later\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tryDial(addr, pool, other, nil); err != nil {
		t.Fatalf("install added to the file still refused: %v", err)
	}
}

func TestRedTeam_Relay_LicenseAdmission(t *testing.T) {
	srv, addr, pool := testRelay(t)
	serverPub, serverKey, _ := ed25519.GenerateKey(rand.Reader)
	srv.cfg.Allowlist = &Allowlist{Path: filepath.Join(t.TempDir(), "none.txt")}
	srv.cfg.LicenseKey = serverPub

	_, installKey, _ := ed25519.GenerateKey(rand.Reader)
	installPub := installKey.Public().(ed25519.PublicKey)
	good := issueToken(t, serverKey, installPub, []string{"app", "relay"}, time.Now().Add(30*24*time.Hour))

	if err := tryDial(addr, pool, newIdentity(t), &LicenseCredentials{Token: good, Key: installKey}); err != nil {
		t.Fatalf("licensed install refused: %v", err)
	}
	subscribed := issueToken(t, serverKey, installPub, []string{"app", "relay"}, time.Now().Add(time.Hour), time.Now().Add(365*24*time.Hour))
	if err := tryDial(addr, pool, newIdentity(t), &LicenseCredentials{Token: subscribed, Key: installKey}); err != nil {
		t.Fatalf("install with a running relay subscription refused: %v", err)
	}

	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	_, forgerKey, _ := ed25519.GenerateKey(rand.Reader)
	cases := map[string]*LicenseCredentials{
		"no license":           nil,
		"no relay feature":     {Token: issueToken(t, serverKey, installPub, []string{"app"}, time.Now().Add(time.Hour)), Key: installKey},
		"someone else's token": {Token: good, Key: otherKey},
		"forged signature":     {Token: issueToken(t, forgerKey, installPub, []string{"app", "relay"}, time.Now().Add(time.Hour)), Key: installKey},
		"garbage":              {Token: "not-a-token", Key: installKey},
		"two dots":             {Token: good + ".x", Key: installKey},
	}
	for name, lic := range cases {
		t.Run(name, func(t *testing.T) {
			refusedPermanently(t, tryDial(addr, pool, newIdentity(t), lic))
		})
	}
}

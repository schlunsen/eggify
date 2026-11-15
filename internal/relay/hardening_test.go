package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func dialWith(t *testing.T, addr string, pool interface{}, cfg ClientConfig) (*Listener, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return Dial(ctx, cfg)
}

// Expired tokens and ended relay subscriptions are refused with their own code
// and without the permanent flag: renewing the license fixes them.
func TestRelay_RefusalCodes(t *testing.T) {
	srv, addr, pool := testRelay(t)
	serverPub, serverKey, _ := ed25519.GenerateKey(rand.Reader)
	srv.cfg.Allowlist = &Allowlist{Path: filepath.Join(t.TempDir(), "none.txt")}
	srv.cfg.LicenseKey = serverPub
	_, installKey, _ := ed25519.GenerateKey(rand.Reader)
	installPub := installKey.Public().(ed25519.PublicKey)

	check := func(name string, lic *LicenseCredentials, code string, permanent bool) {
		t.Run(name, func(t *testing.T) {
			err := tryDial(addr, pool, newIdentity(t), lic)
			var refused *RefusedError
			if !errors.As(err, &refused) || refused.Code != code || refused.Permanent != permanent {
				t.Fatalf("want code %q permanent=%v, got %v (%+v)", code, permanent, err, refused)
			}
		})
	}
	feat := []string{"app", "relay"}
	check("expired", &LicenseCredentials{Token: issueToken(t, serverKey, installPub, feat, time.Now().Add(-time.Minute)), Key: installKey}, CodeLicenseExpired, false)
	check("relay ended", &LicenseCredentials{Token: issueToken(t, serverKey, installPub, feat, time.Now().Add(time.Hour), time.Now().Add(-time.Minute)), Key: installKey}, CodeRelayEnded, false)
	check("no license", nil, CodeSubscribersOnly, true)
	check("no relay feature", &LicenseCredentials{Token: issueToken(t, serverKey, installPub, []string{"app"}, time.Now().Add(time.Hour)), Key: installKey}, CodeSubscribersOnly, true)
}

// A license that ends while the install is connected closes its session.
func TestRelay_AdmissionEndsWithTheToken(t *testing.T) {
	srv, addr, pool := testRelay(t)
	srv.cfg.AdmissionRecheck = 50 * time.Millisecond
	serverPub, serverKey, _ := ed25519.GenerateKey(rand.Reader)
	srv.cfg.Allowlist = &Allowlist{Path: filepath.Join(t.TempDir(), "none.txt")}
	srv.cfg.LicenseKey = serverPub
	_, installKey, _ := ed25519.GenerateKey(rand.Reader)
	installPub := installKey.Public().(ed25519.PublicKey)

	dial := func(lic *LicenseCredentials) *Listener {
		t.Helper()
		l, err := dialWith(t, addr, pool, ClientConfig{Addr: addr, ServerName: testRelayName, Identity: newIdentity(t), RootCAs: pool,
			License: func() *LicenseCredentials { return lic }})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = l.Close() })
		return l
	}
	// The short token's relay_until is two seconds away, its expiry far off.
	short := dial(&LicenseCredentials{Token: issueToken(t, serverKey, installPub, []string{"app", "relay"}, time.Now().Add(time.Hour), time.Now().Add(2*time.Second)), Key: installKey})
	long := dial(&LicenseCredentials{Token: issueToken(t, serverKey, installPub, []string{"app", "relay"}, time.Now().Add(time.Hour)), Key: installKey})
	select {
	case <-short.Done():
	case <-time.After(6 * time.Second):
		t.Fatal("relay kept an install whose relay subscription ended")
	}
	select {
	case <-long.Done():
		t.Fatal("relay dropped an install with a valid license")
	default:
	}
}

// An allowlisted install taken off the list is dropped; one that stays is not.
func TestRelay_AllowlistRemovalDropsInstall(t *testing.T) {
	srv, addr, pool := testRelay(t)
	srv.cfg.AdmissionRecheck = 50 * time.Millisecond
	kept, removed := newIdentity(t), newIdentity(t)
	path := filepath.Join(t.TempDir(), "allowed.txt")
	if err := os.WriteFile(path, []byte(kept.ID()+"\n"+removed.ID()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.cfg.Allowlist = &Allowlist{Path: path}
	dial := func(id *Identity) *Listener {
		l, err := dialWith(t, addr, pool, ClientConfig{Addr: addr, ServerName: testRelayName, Identity: id, RootCAs: pool})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = l.Close() })
		return l
	}
	lk, lr := dial(kept), dial(removed)
	if err := os.WriteFile(path, []byte(kept.ID()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-lr.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("install removed from the allowlist stayed connected")
	}
	select {
	case <-lk.Done():
		t.Fatal("install still on the allowlist was dropped")
	case <-time.After(300 * time.Millisecond):
	}
}

// One license cannot run more than MaxIDsPerLicense relay addresses.
func TestRelay_LicenseIDCap(t *testing.T) {
	srv, addr, pool := testRelay(t)
	srv.cfg.MaxIDsPerLicense = 2
	serverPub, serverKey, _ := ed25519.GenerateKey(rand.Reader)
	srv.cfg.Allowlist = &Allowlist{Path: filepath.Join(t.TempDir(), "none.txt")}
	srv.cfg.LicenseKey = serverPub
	_, installKey, _ := ed25519.GenerateKey(rand.Reader)
	lic := &LicenseCredentials{Token: issueToken(t, serverKey, installKey.Public().(ed25519.PublicKey), []string{"app", "relay"}, time.Now().Add(time.Hour)), Key: installKey}
	dial := func(id *Identity) (*Listener, error) {
		return dialWith(t, addr, pool, ClientConfig{Addr: addr, ServerName: testRelayName, Identity: id, RootCAs: pool,
			License: func() *LicenseCredentials { return lic }})
	}
	first, second := newIdentity(t), newIdentity(t)
	l1, err := dial(first)
	if err != nil {
		t.Fatal(err)
	}
	defer l1.Close()
	l2, err := dial(second)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	_, err = dial(newIdentity(t))
	var refused *RefusedError
	if !errors.As(err, &refused) || refused.Code != CodeTooManyInstalls || refused.Permanent {
		t.Fatalf("third relay address of one license: %v", err)
	}
	// The same address reconnecting (newest wins) is not a new id.
	l1b, err := dial(first)
	if err != nil {
		t.Fatalf("reconnect of a held address refused: %v", err)
	}
	l1b.Close()
}

// The bound proof is checked when sent; a strict relay refuses the plain one.
func TestRelay_BoundLicenseProof(t *testing.T) {
	srv, addr, pool := testRelay(t)
	serverPub, serverKey, _ := ed25519.GenerateKey(rand.Reader)
	srv.cfg.Allowlist = &Allowlist{Path: filepath.Join(t.TempDir(), "none.txt")}
	srv.cfg.LicenseKey = serverPub
	_, installKey, _ := ed25519.GenerateKey(rand.Reader)
	lic := &LicenseCredentials{Token: issueToken(t, serverKey, installKey.Public().(ed25519.PublicKey), []string{"app", "relay"}, time.Now().Add(time.Hour)), Key: installKey}
	legacy := func() error {
		l, err := dialWith(t, addr, pool, ClientConfig{Addr: addr, ServerName: testRelayName, Identity: newIdentity(t), RootCAs: pool,
			License: func() *LicenseCredentials { return lic }, legacyProofOnly: true})
		if err == nil {
			l.Close()
		}
		return err
	}
	if err := tryDial(addr, pool, newIdentity(t), lic); err != nil {
		t.Fatalf("v1+v2 proof refused: %v", err)
	}
	if err := legacy(); err != nil {
		t.Fatalf("an app that only sends the plain proof refused by a default relay: %v", err)
	}
	srv.cfg.RequireBoundLicenseProof = true
	if err := tryDial(addr, pool, newIdentity(t), lic); err != nil {
		t.Fatalf("bound proof refused by a strict relay: %v", err)
	}
	var refused *RefusedError
	if err := legacy(); !errors.As(err, &refused) || !refused.Permanent {
		t.Fatalf("plain proof on a strict relay: %v", err)
	}
	// A proof made for one relay identity does not verify for another.
	id, other := newIdentity(t), newIdentity(t)
	nonce := []byte("0123456789abcdef0123456789abcdef")
	sig := ed25519.Sign(installKey, licenseProofMessageBound(testRelayName, nonce, other.SPKI))
	if ed25519.Verify(installKey.Public().(ed25519.PublicKey), licenseProofMessageBound(testRelayName, nonce, id.SPKI), sig) {
		t.Fatal("bound proof verified for a different relay identity")
	}
}

func TestAllowlistContentHashAndNoCachingOfErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowed.txt")
	a := &Allowlist{Path: path}
	if a.Has("abc") {
		t.Fatal("missing file lists somebody")
	}
	// The failed read is not cached: the file appearing is seen at once.
	if err := os.WriteFile(path, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !a.Has("abc") {
		t.Fatal("file created after a failed read not picked up")
	}
	// Same size, same mtime: only the content differs.
	info, _ := os.Stat(path)
	if err := os.WriteFile(path, []byte("abd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if a.Has("abc") || !a.Has("abd") {
		t.Fatal("same-size edit with an unchanged mtime not noticed")
	}
}

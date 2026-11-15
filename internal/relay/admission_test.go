package relay

import (
	"context"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tryDial(addr string, pool *x509.CertPool, id *Identity) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := ClientConfig{Addr: addr, ServerName: testRelayName, Identity: id, RootCAs: pool}
	l, err := Dial(ctx, cfg)
	if err == nil {
		_ = l.Close()
	}
	return err
}

func refusedPermanently(t *testing.T, err error) {
	t.Helper()
	var refused *RefusedError
	if !errors.As(err, &refused) || !refused.Permanent || refused.Msg != NotAllowedMessage {
		t.Fatalf("want a permanent not-allowed refusal, got %v", err)
	}
}

func TestRelay_OpenRelayAdmitsEveryone(t *testing.T) {
	_, addr, pool := testRelay(t)
	if err := tryDial(addr, pool, newIdentity(t)); err != nil {
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

	if err := tryDial(addr, pool, listed); err != nil {
		t.Fatalf("listed install refused: %v", err)
	}
	refusedPermanently(t, tryDial(addr, pool, other))

	// The file is re-read when it changes.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte(listed.ID()+"\n"+other.ID()+"  # added later\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tryDial(addr, pool, other); err != nil {
		t.Fatalf("install added to the file still refused: %v", err)
	}
}

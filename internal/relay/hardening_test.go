package relay

import (
	"context"
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

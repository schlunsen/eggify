//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestClearQuarantineInProcess(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Eggify.app")
	bin := filepath.Join(app, "Contents", "MacOS", "eggify-desktop")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, []byte("x"), 0o755)
	plain := filepath.Join(app, "Contents", "Info.plist")
	os.WriteFile(plain, []byte("x"), 0o644)
	const attr = "com.apple.quarantine"
	for _, p := range []string{app, bin} {
		if err := unix.Setxattr(p, attr, []byte("0081;00000000;Safari;"), 0); err != nil {
			t.Skipf("cannot set xattr here: %v", err)
		}
	}
	if err := clearQuarantine(app); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{app, bin, plain} {
		if _, err := unix.Getxattr(p, attr, nil); err == nil {
			t.Errorf("%s still quarantined", p)
		}
	}
}

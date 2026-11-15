package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// installFixture serves a verified-looking update for an installed fake
// Eggify.app, so Install can be driven up to the bundle swap.
type installFixture struct {
	installed string
	newU      func() *Updater
}

func newInstallFixture(t *testing.T, dmgURL string) installFixture {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("install path is macOS-only")
	}
	work := t.TempDir()
	src := filepath.Join(work, "src", "Eggify.app", "Contents", "MacOS")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "eggify-desktop"), []byte("new"), 0o755)
	zipPath := filepath.Join(work, "update.zip")
	if out, err := execCommand("ditto", "-c", "-k", "--keepParent", filepath.Join(work, "src", "Eggify.app"), zipPath); err != nil {
		t.Fatalf("ditto: %v %s", err, out)
	}
	zipBytes, _ := os.ReadFile(zipPath)
	sum := sha256.Sum256(zipBytes)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.json":
			dmg := ""
			if dmgURL != "" {
				dmg = fmt.Sprintf(`,"dmg_url":%q`, strings.ReplaceAll(dmgURL, "HOST", r.Host))
			}
			fmt.Fprintf(w, `{"version":"2.1.0","platforms":{"%s-%s":{"url":"http://%s/u.zip","sha256":"%s","size":%d%s}}}`,
				runtime.GOOS, runtime.GOARCH, r.Host, hex.EncodeToString(sum[:]), len(zipBytes), dmg)
		case "/u.zip":
			w.Write(zipBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	installed := filepath.Join(work, "Applications", "Eggify.app")
	os.MkdirAll(filepath.Join(installed, "Contents", "MacOS"), 0o755)
	os.WriteFile(filepath.Join(installed, "Contents", "MacOS", "eggify-desktop"), []byte("old"), 0o755)
	return installFixture{installed: installed, newU: func() *Updater {
		u := &Updater{
			manifestURL: srv.URL + "/latest.json",
			current:     "2.0.0",
			client:      srv.Client(),
			verifier:    fakeVerifier{sig: bundleSignature{TeamID: expectedTeamID, Identifier: bundleID, Version: "2.1.0"}},
			osVersion:   func() string { return "27.0" },
			appBundle:   func() (string, error) { return installed, nil },
			logf:        t.Logf,
		}
		u.info = UpdateInfo{CurrentVersion: "2.0.0", Phase: "idle"}
		return u
	}}
}

func (f installFixture) installedBinary() string {
	b, _ := os.ReadFile(filepath.Join(f.installed, "Contents", "MacOS", "eggify-desktop"))
	return string(b)
}

// macOS App Management denies the swap with EPERM: the installed app is left
// alone, the state says why (error_code app_management) and nothing relaunches.
func TestInstallReportsAppManagementDenial(t *testing.T) {
	f := newInstallFixture(t, "")
	relaunched := false
	relaunchAfterExit = func(string) error { relaunched = true; return nil }
	defer func() { relaunchAfterExit = scheduleRelaunch }()

	for name, swapErr := range map[string]error{
		"errno":      syscall.EPERM,
		"link error": &os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.EPERM},
	} {
		u := f.newU()
		u.swap = func(string, string) error { return swapErr }
		if _, err := u.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
		err := u.Install(context.Background(), func() { t.Error("quit after a denied install") })
		if !errors.Is(err, errAppManagementBlocked) {
			t.Fatalf("%s: err = %v, want errAppManagementBlocked", name, err)
		}
		st := u.State()
		if st.Phase != "error" || st.ErrorCode != errorCodeAppManagement || !strings.Contains(st.Error, "App Management") {
			t.Fatalf("%s: state %+v", name, st)
		}
		if f.installedBinary() != "old" || relaunched {
			t.Fatalf("%s: app replaced or relaunched after a denial", name)
		}
	}

	// Any other failure has no error code, and a new attempt clears it.
	u := f.newU()
	u.swap = func(string, string) error { return syscall.EXDEV }
	u.Check(context.Background())
	if err := u.Install(context.Background(), nil); err == nil || errors.Is(err, errAppManagementBlocked) {
		t.Fatalf("EXDEV: err = %v", err)
	}
	if st := u.State(); st.ErrorCode != "" {
		t.Fatalf("EXDEV classified as %q", st.ErrorCode)
	}
}

// Quarantine is cleared in-process; a failure there is logged, not fatal.
// Staging dirs left by earlier attempts are swept.
func TestInstallSweepsStaleStagingAndToleratesQuarantineFailure(t *testing.T) {
	f := newInstallFixture(t, "")
	relaunchAfterExit = func(string) error { return nil }
	defer func() { relaunchAfterExit = scheduleRelaunch }()
	parent := filepath.Dir(f.installed)
	stale := filepath.Join(parent, stagingPrefix+"old123")
	os.MkdirAll(filepath.Join(stale, "Eggify.app"), 0o755)

	u := f.newU()
	stripped := ""
	u.stripQuarantine = func(app string) error { stripped = app; return errors.New("xattr denied") }
	u.Check(context.Background())
	done := make(chan struct{})
	if err := u.Install(context.Background(), func() { close(done) }); err != nil {
		t.Fatalf("install: %v", err)
	}
	<-done
	if f.installedBinary() != "new" {
		t.Fatal("update not installed")
	}
	if !strings.HasSuffix(stripped, "Eggify.app") {
		t.Fatalf("quarantine strip got %q", stripped)
	}
	if left, _ := filepath.Glob(filepath.Join(parent, stagingPrefix+"*")); len(left) != 0 {
		t.Fatalf("staging dirs left: %v", left)
	}
}

// The update state offers the release's DMG for a manual install, and falls
// back to the stable alias when the manifest has none (or a foreign one).
func TestCheckOffersDownloadURL(t *testing.T) {
	useTestEndpoints(t)
	for name, tc := range map[string]struct{ dmg, want string }{
		"from manifest": {"http://HOST/Eggify-2.1.0.dmg", "/Eggify-2.1.0.dmg"},
		"none":          {"", defaultDownloadURL},
		"foreign host":  {"https://evil.example/Eggify.dmg", defaultDownloadURL},
	} {
		f := newInstallFixture(t, tc.dmg)
		info, err := f.newU().Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(info.DownloadURL, tc.want) {
			t.Errorf("%s: download_url = %q, want …%s", name, info.DownloadURL, tc.want)
		}
	}
}

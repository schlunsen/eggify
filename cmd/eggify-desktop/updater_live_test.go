package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestLiveUpdate runs the real updater against the published manifest
// (https://updates.example.com/download/latest.json, or EGGIFY_UPDATE_MANIFEST_URL):
// real download, SHA-256, codesign, TeamIdentifier, Gatekeeper and swap, but
// into a fake "installed" Eggify.app inside t.TempDir() and with a fake older
// current version. Nothing in /Applications is touched and nothing relaunches.
//
//	EGGIFY_LIVE_UPDATE=1 go test -run TestLiveUpdate -v .
func TestLiveUpdate(t *testing.T) {
	if os.Getenv("EGGIFY_LIVE_UPDATE") != "1" {
		t.Skip("set EGGIFY_LIVE_UPDATE=1 to run against the published release")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	manifestURL := defaultManifestURL // set via -ldflags -X main.defaultManifestURL, or EGGIFY_UPDATE_MANIFEST_URL
	if v := os.Getenv("EGGIFY_UPDATE_MANIFEST_URL"); v != "" {
		manifestURL = v
	}
	installed := filepath.Join(t.TempDir(), "Applications", "Eggify.app")
	if err := os.MkdirAll(filepath.Join(installed, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(installed, "Contents", "MacOS", "eggify-desktop"), []byte("old"), 0o755)

	var relaunched string
	relaunchAfterExit = func(p string) error { relaunched = p; return nil }
	defer func() { relaunchAfterExit = scheduleRelaunch }()

	u := &Updater{
		manifestURL: manifestURL,
		current:     "1.0.0",
		client:      &http.Client{Timeout: 30 * time.Second},
		verifier:    platformVerifier{},
		osVersion:   platformOSVersion,
		appBundle:   func() (string, error) { return installed, nil },
		logf:        t.Logf,
	}
	u.info = UpdateInfo{CurrentVersion: "1.0.0", Phase: "idle"}

	info, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	t.Logf("check: %+v", info)
	if !info.UpdateAvailable {
		t.Fatalf("expected an update from 1.0.0, got %+v", info)
	}
	quit := make(chan struct{})
	if err := u.Install(context.Background(), func() { close(quit) }); err != nil {
		t.Fatalf("install: %v", err)
	}
	<-quit
	if relaunched != installed {
		t.Fatalf("relaunch %q, want %q", relaunched, installed)
	}
	out, err := exec.Command("codesign", "-dv", "--verbose=4", installed).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "TeamIdentifier="+expectedTeamID) {
		t.Fatalf("installed bundle not ours: %v %s", err, out)
	}
	if out, err := exec.Command("spctl", "--assess", "--type", "exec", installed).CombinedOutput(); err != nil {
		t.Fatalf("installed bundle rejected by Gatekeeper: %v %s", err, out)
	}
	t.Logf("installed %s into %s; state %+v", info.LatestVersion, installed, u.State())

	// A second install attempt with the real version (2.0.0 == published) is refused.
	u2 := &Updater{manifestURL: manifestURL, current: info.LatestVersion, client: u.client,
		verifier: platformVerifier{}, osVersion: platformOSVersion,
		appBundle: func() (string, error) { return installed, nil }, logf: t.Logf}
	u2.info = UpdateInfo{CurrentVersion: info.LatestVersion, Phase: "idle"}
	if info2, _ := u2.Check(context.Background()); info2.UpdateAvailable {
		t.Fatal("same version offered as update")
	}
	if err := u2.Install(context.Background(), nil); err == nil {
		t.Fatal("install without an available update should fail")
	}
}

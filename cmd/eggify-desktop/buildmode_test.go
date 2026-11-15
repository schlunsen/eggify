package main

import (
	"os"
	"path/filepath"
	"testing"
)

func setRelease(t *testing.T, v string) {
	t.Helper()
	old := releaseBuild
	releaseBuild = v
	t.Cleanup(func() { releaseBuild = old })
}

// A release build ignores the testing knobs: an attacker who can set this
// app's environment must not redirect the updater to its own manifest or
// inject script into the window that holds the signed-in session.
func TestRedTeam_Desktop_ReleaseBuildIgnoresTestingKnobs(t *testing.T) {
	script := filepath.Join(t.TempDir(), "inject.js")
	if err := os.WriteFile(script, []byte("document.title='pwned'"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EGGIFY_UPDATE_MANIFEST_URL", "http://127.0.0.1:9/latest.json")
	t.Setenv("EGGIFY_UPDATE_FAKE_VERSION", "0.0.1")
	t.Setenv("EGGIFY_DESKTOP_DIAG", "1")
	t.Setenv("EGGIFY_DESKTOP_DIAG_SCRIPT", script)

	setRelease(t, "true")
	u := newUpdater()
	if u.manifestURL != defaultManifestURL {
		t.Fatalf("release build used the overridden manifest %q", u.manifestURL)
	}
	if u.current == "0.0.1" {
		t.Fatal("release build used EGGIFY_UPDATE_FAKE_VERSION")
	}
	if got := diagExtraJS(); got != "" {
		t.Fatalf("release build loaded the diag script: %q", got)
	}
	if devOnlyEnv("EGGIFY_DESKTOP_DIAG") != "" {
		t.Fatal("release build reads EGGIFY_DESKTOP_DIAG")
	}

	// A normal (non-release) build still honours them, so the tests that use them work.
	setRelease(t, "")
	u = newUpdater()
	if u.manifestURL != "http://127.0.0.1:9/latest.json" || u.current != "0.0.1" {
		t.Fatalf("dev build lost its knobs: %q %q", u.manifestURL, u.current)
	}
	if diagExtraJS() == "" {
		t.Fatal("dev build ignored the diag script")
	}
}

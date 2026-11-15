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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const sampleManifest = `{
  "version": "2.1.0",
  "published_at": "2026-10-03T12:00:00Z",
  "release_url": "https://updates.example.com/#download",
  "min_os": "13.4",
  "platforms": {
    "darwin-arm64": {
      "url": "https://updates.example.com/download/Eggify-2.1.0-darwin-arm64.zip",
      "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "size": 1234,
      "dmg_url": "https://updates.example.com/download/Eggify-2.1.0.dmg"
    }
  }
}`

func TestParseManifest(t *testing.T) {
	m, err := parseManifest([]byte(sampleManifest))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "2.1.0" || m.MinOS != "13.4" {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	p, err := m.platform("darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if p.Size != 1234 || !strings.HasSuffix(p.URL, ".zip") {
		t.Fatalf("unexpected platform: %+v", p)
	}
	if _, err := m.platform("darwin", "amd64"); err == nil {
		t.Fatal("expected missing platform error")
	}
}

func TestParseManifestRejectsBadInput(t *testing.T) {
	for name, in := range map[string]string{
		"not json":     `<html>`,
		"no version":   `{"platforms":{"darwin-arm64":{"url":"x"}}}`,
		"bad version":  `{"version":"latest","platforms":{"darwin-arm64":{"url":"x"}}}`,
		"no platforms": `{"version":"2.0.0"}`,
	} {
		if _, err := parseManifest([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	m, err := parseManifest([]byte(`{"version":"v2.0.0","platforms":{"darwin-arm64":{"url":"https://updates.example.com/a.zip","sha256":"nothex"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "2.0.0" {
		t.Fatalf("v prefix not stripped: %q", m.Version)
	}
	if _, err := m.platform("darwin", "arm64"); err == nil {
		t.Fatal("expected invalid sha256 to be rejected")
	}
}

func TestVersionComparison(t *testing.T) {
	cases := []struct {
		current, latest string
		newer           bool
	}{
		{"2.0.0", "2.0.1", true},
		{"2.0.9", "2.0.10", true},
		{"2.0.0", "2.0.0", false},
		{"2.1.0", "2.0.9", false},
		{"v2.0.0", "2.1", true},
		{"2.0", "2.0.0", false},
		{"2.0.0", "3.0.0-beta", true},
		{"dev", "9.9.9", false},
		{"", "9.9.9", false},
		{"2.0.0", "garbage", false},
	}
	for _, c := range cases {
		if got := isNewerVersion(c.current, c.latest); got != c.newer {
			t.Errorf("isNewerVersion(%q, %q) = %v, want %v", c.current, c.latest, got, c.newer)
		}
	}
	if compareVersions("13.4", "13.4.1") >= 0 || compareVersions("27.0", "13.4") <= 0 {
		t.Error("compareVersions OS ordering wrong")
	}
}

// The source default for expectedTeamID is empty (no team: no update installs);
// the tests use a fake one, injected like the release build does.
func TestMain(m *testing.M) {
	expectedTeamID = "ABCDE12345"
	os.Exit(m.Run())
}

// useTestEndpoints sets the endpoints a release build injects with -ldflags -X.
func useTestEndpoints(t *testing.T) {
	t.Helper()
	oldM, oldD := defaultManifestURL, defaultDownloadURL
	defaultManifestURL = "https://updates.example.com/download/latest.json"
	defaultDownloadURL = "https://updates.example.com/download/Eggify.dmg"
	t.Cleanup(func() { defaultManifestURL, defaultDownloadURL = oldM, oldD })
}

// A source build has no endpoints and no team id: nothing is trusted, and the
// updater cannot fetch or install anything.
func TestNoEndpointsMeansNothingTrusted(t *testing.T) {
	if defaultManifestURL != "" || defaultDownloadURL != "" {
		t.Skip("endpoints injected at build time")
	}
	oldTeam := expectedTeamID
	expectedTeamID = ""
	t.Cleanup(func() { expectedTeamID = oldTeam })
	if err := validateUpdateURL("https://updates.example.com/x.zip", ""); err == nil {
		t.Error("host trusted without any injected endpoint")
	}
	if _, err := newUpdater().fetchManifest(context.Background()); err == nil {
		t.Error("fetchManifest with no manifest URL should fail")
	}
	good := bundleSignature{TeamID: "ABCDE12345", Identifier: bundleID, Version: "2.1.0"}
	if err := checkSignature(good, expectedTeamID, bundleID, "2.1.0", "2.0.0"); err == nil {
		t.Error("empty expected team id must refuse the update")
	}
}

func TestValidateUpdateURL(t *testing.T) {
	useTestEndpoints(t)
	ok := []string{"https://updates.example.com/download/Eggify.zip"}
	bad := []string{
		"http://updates.example.com/download/Eggify.zip",
		"https://evil.example/Eggify.zip",
		"https://updates.example.com.evil.example/x.zip",
		"https://nzero.pro/download/nzero-darwin.zip",
		"file:///tmp/x.zip",
	}
	for _, u := range ok {
		if err := validateUpdateURL(u, defaultManifestURL); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range bad {
		if err := validateUpdateURL(u, defaultManifestURL); err == nil {
			t.Errorf("%s: expected rejection", u)
		}
	}
	// Overridden manifest (testing): its host is trusted, http only on loopback.
	if err := validateUpdateURL("http://127.0.0.1:9999/x.zip", "http://127.0.0.1:9999/latest.json"); err != nil {
		t.Errorf("loopback override: %v", err)
	}
	if err := validateUpdateURL("http://staging.example/x.zip", "http://staging.example/latest.json"); err == nil {
		t.Error("plain http to a non-loopback host must be refused")
	}
}

func TestVerifyFileSHA256(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.zip")
	if err := os.WriteFile(p, []byte("egg"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("egg"))
	good := hex.EncodeToString(sum[:])
	if err := verifyFileSHA256(p, good); err != nil {
		t.Fatal(err)
	}
	if err := verifyFileSHA256(p, strings.ToUpper(good)); err != nil {
		t.Fatalf("uppercase hex should match: %v", err)
	}
	if err := verifyFileSHA256(p, strings.Repeat("0", 64)); err == nil {
		t.Fatal("expected mismatch")
	}
	if err := verifyFileSHA256(p, ""); err == nil {
		t.Fatal("expected empty checksum to be refused")
	}
}

func TestCheckSignature(t *testing.T) {
	good := bundleSignature{TeamID: "ABCDE12345", Identifier: bundleID, Version: "2.1.0"}
	if err := checkSignature(good, "ABCDE12345", bundleID, "2.1.0", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	cases := map[string]bundleSignature{
		"other team":       {TeamID: "ZZZZZ99999", Identifier: bundleID, Version: "2.1.0"},
		"unsigned":         {TeamID: "", Identifier: bundleID, Version: "2.1.0"},
		"other app":        {TeamID: "ABCDE12345", Identifier: "com.example.other", Version: "2.1.0"},
		"version mismatch": {TeamID: "ABCDE12345", Identifier: bundleID, Version: "2.0.5"},
	}
	for name, sig := range cases {
		if err := checkSignature(sig, "ABCDE12345", bundleID, "2.1.0", "2.0.0"); err == nil {
			t.Errorf("%s: expected refusal", name)
		}
	}
	// Downgrade/replay: a genuine older build advertised as "latest".
	old := bundleSignature{TeamID: "ABCDE12345", Identifier: bundleID, Version: "1.9.0"}
	if err := checkSignature(old, "ABCDE12345", bundleID, "1.9.0", "2.0.0"); err == nil {
		t.Error("expected downgrade to be refused")
	}
	if err := checkSignature(good, "", bundleID, "2.1.0", "2.0.0"); err == nil {
		t.Error("expected refusal without a compiled-in team")
	}
}

func TestParseCodesignInfo(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	out := "Executable=/x/Eggify.app/Contents/MacOS/eggify-desktop\nIdentifier=com.eggify.desktop.wails\nFormat=app bundle with Mach-O thin (arm64)\nAuthority=Developer ID Application: Example Dev (ABCDE12345)\nTeamIdentifier=ABCDE12345\n"
	team, id := parseCodesignInfo(out)
	if team != "ABCDE12345" || id != "com.eggify.desktop.wails" {
		t.Fatalf("got team=%q id=%q", team, id)
	}
	if team, _ := parseCodesignInfo("TeamIdentifier=not set\n"); team != "" {
		t.Fatalf("ad-hoc should yield empty team, got %q", team)
	}
}

func TestFindAppBundle(t *testing.T) {
	d := t.TempDir()
	if _, err := findAppBundle(d); err == nil {
		t.Fatal("expected error for empty dir")
	}
	os.Mkdir(filepath.Join(d, "Eggify.app"), 0o755)
	if p, err := findAppBundle(d); err != nil || filepath.Base(p) != "Eggify.app" {
		t.Fatalf("got %q, %v", p, err)
	}
	os.Mkdir(filepath.Join(d, "Other.app"), 0o755)
	if _, err := findAppBundle(d); err == nil {
		t.Fatal("expected error for two bundles")
	}
}

// fakeVerifier stands in for codesign/spctl.
type fakeVerifier struct {
	sig bundleSignature
	err error
}

func (f fakeVerifier) Verify(string) (bundleSignature, error) { return f.sig, f.err }

// TestUpdaterCheckAndInstallRefusals drives Check/Install against a local
// manifest server, with a fake verifier, up to the point where the bundle
// would be swapped in. Nothing outside t.TempDir() is touched.
func TestUpdaterCheckAndInstallRefusals(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("install path is macOS-only")
	}
	// A zip with a fake Eggify.app, made with ditto like the release.
	work := t.TempDir()
	app := filepath.Join(work, "src", "Eggify.app", "Contents", "MacOS")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(app, "eggify-desktop"), []byte("new"), 0o755)
	zipPath := filepath.Join(work, "update.zip")
	if out, err := execCommand("ditto", "-c", "-k", "--keepParent", filepath.Join(work, "src", "Eggify.app"), zipPath); err != nil {
		t.Fatalf("ditto: %v %s", err, out)
	}
	zipBytes, _ := os.ReadFile(zipPath)
	sum := sha256.Sum256(zipBytes)
	goodSum := hex.EncodeToString(sum[:])

	var manifestSum string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.json":
			fmt.Fprintf(w, `{"version":"2.1.0","min_os":"13.4","platforms":{"%s-%s":{"url":"http://%s/u.zip","sha256":"%s","size":%d}}}`,
				runtime.GOOS, runtime.GOARCH, r.Host, manifestSum, len(zipBytes))
		case "/u.zip":
			w.Write(zipBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	installed := filepath.Join(work, "Applications", "Eggify.app")
	os.MkdirAll(filepath.Join(installed, "Contents", "MacOS"), 0o755)
	os.WriteFile(filepath.Join(installed, "Contents", "MacOS", "eggify-desktop"), []byte("old"), 0o755)

	newU := func(v bundleVerifier) *Updater {
		u := &Updater{
			manifestURL: srv.URL + "/latest.json",
			current:     "2.0.0",
			client:      srv.Client(),
			verifier:    v,
			osVersion:   func() string { return "27.0" },
			appBundle:   func() (string, error) { return installed, nil },
			logf:        t.Logf,
		}
		u.info = UpdateInfo{CurrentVersion: "2.0.0", Phase: "idle"}
		return u
	}
	readInstalled := func() string {
		b, _ := os.ReadFile(filepath.Join(installed, "Contents", "MacOS", "eggify-desktop"))
		return string(b)
	}

	// Install before any check is refused.
	if err := newU(fakeVerifier{}).Install(context.Background(), nil); err == nil {
		t.Fatal("install without check should fail")
	}

	// Checksum mismatch.
	manifestSum = strings.Repeat("0", 64)
	u := newU(fakeVerifier{sig: bundleSignature{TeamID: expectedTeamID, Identifier: bundleID, Version: "2.1.0"}})
	info, err := u.Check(context.Background())
	if err != nil || !info.UpdateAvailable || info.LatestVersion != "2.1.0" {
		t.Fatalf("check: %+v %v", info, err)
	}
	if err := u.Install(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum failure, got %v", err)
	}
	if readInstalled() != "old" {
		t.Fatal("app replaced despite checksum failure")
	}

	// Signature failure / wrong team: never swapped in.
	manifestSum = goodSum
	for name, v := range map[string]fakeVerifier{
		"gatekeeper": {err: errors.New("rejected")},
		"wrong team": {sig: bundleSignature{TeamID: "ZZZZZZZZZZ", Identifier: bundleID, Version: "2.1.0"}},
		"wrong id":   {sig: bundleSignature{TeamID: expectedTeamID, Identifier: "com.other", Version: "2.1.0"}},
	} {
		u := newU(v)
		if _, err := u.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := u.Install(context.Background(), nil); err == nil {
			t.Errorf("%s: expected refusal", name)
		}
		if readInstalled() != "old" {
			t.Fatalf("%s: app replaced despite refusal", name)
		}
		if st := u.State(); st.Phase != "error" || st.Error == "" {
			t.Errorf("%s: state not error: %+v", name, st)
		}
	}

	// Happy path: verified bundle is swapped in, relaunch and quit requested.
	var relaunched string
	relaunchAfterExit = func(p string) error { relaunched = p; return nil }
	defer func() { relaunchAfterExit = scheduleRelaunch }()
	quit := make(chan struct{})
	u = newU(fakeVerifier{sig: bundleSignature{TeamID: expectedTeamID, Identifier: bundleID, Version: "2.1.0"}})
	if _, err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := u.Install(context.Background(), func() { close(quit) }); err != nil {
		t.Fatalf("install: %v", err)
	}
	<-quit
	if readInstalled() != "new" || relaunched != installed || u.State().Phase != "restarting" {
		t.Fatalf("installed=%q relaunched=%q state=%+v", readInstalled(), relaunched, u.State())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(installed), ".Eggify-update-*")); len(left) != 0 {
		t.Fatalf("staging dir left behind: %v", left)
	}

	// Blocked by min_os.
	u = newU(fakeVerifier{})
	u.osVersion = func() string { return "12.7" }
	if info, _ := u.Check(context.Background()); info.UpdateAvailable || info.Blocked == "" {
		t.Fatalf("expected min_os block: %+v", info)
	}

	// Up to date.
	u = newU(fakeVerifier{})
	u.current = "2.1.0"
	if info, _ := u.Check(context.Background()); info.UpdateAvailable {
		t.Fatal("same version must not be offered")
	}
}

// TestSwapBundles checks the atomic exchange on a real APFS temp dir.
func TestSwapBundles(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	d := t.TempDir()
	cur, nw := filepath.Join(d, "Eggify.app"), filepath.Join(d, "stage", "Eggify.app")
	os.MkdirAll(cur, 0o755)
	os.MkdirAll(nw, 0o755)
	os.WriteFile(filepath.Join(cur, "v"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(nw, "v"), []byte("new"), 0o644)
	if err := swapBundles(cur, nw); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(cur, "v")); string(b) != "new" {
		t.Fatalf("current = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(nw, "v")); string(b) != "old" {
		t.Fatalf("staged = %q", b)
	}
}

func execCommand(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// TestPlatformVerifierRejectsAdhoc runs the real codesign/spctl verifier on an
// ad-hoc signed bundle: it must never pass the policy.
func TestPlatformVerifierRejectsAdhoc(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	app := filepath.Join(t.TempDir(), "Eggify.app")
	os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755)
	plist := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>eggify-desktop</string><key>CFBundleIdentifier</key><string>` + bundleID + `</string>
<key>CFBundleShortVersionString</key><string>9.9.9</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`
	os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644)
	exe, _ := os.ReadFile("/usr/bin/true")
	os.WriteFile(filepath.Join(app, "Contents", "MacOS", "eggify-desktop"), exe, 0o755)
	if out, err := execCommand("codesign", "--force", "--sign", "-", app); err != nil {
		t.Skipf("codesign unavailable: %v %s", err, out)
	}
	sig, err := platformVerifier{}.Verify(app)
	if err == nil {
		err = checkSignature(sig, expectedTeamID, bundleID, "9.9.9", "2.0.0")
	}
	if err == nil {
		t.Fatalf("ad-hoc bundle accepted: %+v", sig)
	}
	t.Logf("refused as expected: %v", err)
}

// Pushed states (the Wails event the dashboard listens to) must say whether
// the app can update itself; 2.0.0/2.1.0 left it out, so the dashboard only
// offered a manual download.
func TestPushedStateCarriesCanInstall(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bundle func() (string, error)
		want   bool
	}{
		{"installed app", func() (string, error) { return "/Applications/Eggify.app", nil }, runtime.GOOS == "darwin"},
		{"disk image", func() (string, error) { return "", errors.New("running from the disk image") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []UpdateInfo
			u := &Updater{appBundle: tc.bundle, logf: t.Logf}
			u.onChange = func(info UpdateInfo) { got = append(got, info) }
			u.update(func(info *UpdateInfo) { info.UpdateAvailable = true; info.LatestVersion = "9.9.9" })
			if len(got) != 1 || got[0].CanInstall != tc.want {
				t.Fatalf("pushed %+v, want CanInstall=%v", got, tc.want)
			}
			if u.State().CanInstall != tc.want {
				t.Fatalf("State().CanInstall = %v, want %v", u.State().CanInstall, tc.want)
			}
		})
	}
}

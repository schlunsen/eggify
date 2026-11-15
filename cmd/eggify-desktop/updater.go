package main

// Self-updater for the Wails desktop app, modelled on the n0 desktop app
// (desktop/updater.go + selfupdate.go there).
//
// Flow:
//
//  1. Check: fetch the manifest (latest.json at defaultManifestURL, injected at
//     build time, published by `just desktop-deploy`) and compare its version with ours.
//  2. Install (only when the user clicks "Restart to update"):
//     download the update ZIP named by the manifest → verify its SHA-256 →
//     extract it with ditto next to the running .app → verify the extracted
//     bundle (codesign --verify --deep --strict, TeamIdentifier, bundle id,
//     Gatekeeper/notarisation, bundle version == manifest version) →
//     swap it with the running .app atomically (renamex_np RENAME_SWAP) →
//     relaunch once this process has exited.
//
// The SHA-256 comes from the same server as the ZIP, so it only proves the
// download is intact. The code-signature checks are the authenticity control:
// a compromised web server cannot get anything installed that Apple has not
// notarised for our team under our bundle id, and it cannot roll users back
// to an older (signed) version either.
//
// Wails' own updater is not used (wailsapp/wails#6186).
//
// macOS App Management (Ventura and later): a process may modify or replace a
// notarised app bundle only if it is signed by the same team as that app, or
// if the user allowed it in System Settings → Privacy & Security → App
// Management (kTCCServiceSystemPolicyAppBundles). Every write to a bundle is
// therefore done by this process itself (same team as the installed app),
// never by a helper tool such as xattr or a shell script, which are signed by
// Apple and would be denied and reported as "Eggify was prevented from
// modifying apps on your Mac". If the swap is still denied (EPERM), the
// installed app is left as it was and the user gets a clear message with
// "Open App Management settings", "Download update" and "Try again"; nothing
// retries on its own.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/schlunsen/eggify/internal/version"
)

// Service endpoints, empty in the source and injected into release builds with
// -ldflags "-X main.defaultManifestURL=… -X main.defaultDownloadURL=…"
// (scripts/build-desktop-wails.sh: EGGIFY_BUILD_UPDATE_MANIFEST_URL,
// EGGIFY_BUILD_DOWNLOAD_URL). Without a manifest URL the updater is off, like
// EGGIFY_DISABLE_UPDATES=1. They are variables: -X cannot set constants.
var (
	// defaultManifestURL is where `just desktop-deploy` publishes latest.json.
	defaultManifestURL = ""
	// defaultDownloadURL is the signed DMG for a manual install.
	defaultDownloadURL = ""
)

const (
	// bundleID must match CFBundleIdentifier in build/darwin/Info.plist. Our
	// Apple team signs other apps too, so the team check alone is not enough.
	bundleID = appID

	updateCheckInterval = 6 * time.Hour
	// updateShowEvent asks the dashboard to (re)open its update dialog.
	updateShowEvent = "eggify:update-show"
	// errorCodeAppManagement marks an install that macOS App Management denied.
	errorCodeAppManagement = "app_management"
	// stagingPrefix names the staging dirs created next to the installed app.
	stagingPrefix    = ".Eggify-update-"
	firstCheckDelay  = 2 * time.Second // right after launch, once the window is up
	maxManifestBytes = 64 << 10
	maxUpdateBytes   = 1 << 30 // refuse anything over 1 GiB
)

// errAppManagementBlocked: macOS refused to let Eggify replace its own bundle.
var errAppManagementBlocked = errors.New("Eggify couldn't replace itself because macOS App Management is blocking it. " +
	"Allow Eggify in System Settings → Privacy & Security → App Management, or install the update manually")

// isAppManagementDenial reports whether err is the EPERM ("Operation not
// permitted") macOS returns when App Management (or another system policy)
// blocks a change to an app bundle. Plain permission problems are EACCES and
// are caught earlier by checkWritable.
func isAppManagementDenial(err error) bool {
	return err != nil && errors.Is(err, syscall.EPERM)
}

// classifyInstallError turns an App Management denial into
// errAppManagementBlocked (keeping the cause); other errors pass through.
func classifyInstallError(err error) error {
	if isAppManagementDenial(err) && !errors.Is(err, errAppManagementBlocked) {
		return fmt.Errorf("%w (%v)", errAppManagementBlocked, err)
	}
	return err
}

// relaunchAfterExit is scheduleRelaunch; a variable so tests can stub it.
var relaunchAfterExit = scheduleRelaunch

// expectedTeamID is the Apple Developer team that must have signed an update.
// Set at build time with -ldflags "-X main.expectedTeamID=..." (APPLE_TEAM_ID in
// scripts/build-desktop-wails.sh); deliberately not readable from the
// environment at run time. Empty in the source: checkSignature then refuses
// every update, so a build without a team id never installs anything.
var expectedTeamID = ""

// trustedUpdateHost reports whether host may serve the manifest and update
// archives: the hosts of the injected manifest and download URLs.
func trustedUpdateHost(host string) bool {
	for _, raw := range []string{defaultManifestURL, defaultDownloadURL} {
		if raw == "" {
			continue
		}
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" && u.Hostname() == host {
			return true
		}
	}
	return false
}

// updateManifest is latest.json. Platform keys are "<goos>-<goarch>".
type updateManifest struct {
	Version     string                      `json:"version"`
	PublishedAt string                      `json:"published_at"`
	ReleaseURL  string                      `json:"release_url"`
	MinOS       string                      `json:"min_os"`
	Platforms   map[string]manifestPlatform `json:"platforms"`
}

type manifestPlatform struct {
	URL       string `json:"url"` // update ZIP (ditto -c -k --keepParent Eggify.app)
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	DMGURL    string `json:"dmg_url,omitempty"`
	DMGSHA256 string `json:"dmg_sha256,omitempty"`
	DMGSize   int64  `json:"dmg_size,omitempty"`
}

// UpdateInfo is what the dashboard sees (DesktopService.UpdateState).
type UpdateInfo struct {
	CurrentVersion  string  `json:"current_version"`
	LatestVersion   string  `json:"latest_version"`
	UpdateAvailable bool    `json:"update_available"`
	PublishedAt     string  `json:"published_at,omitempty"`
	ReleaseURL      string  `json:"release_url,omitempty"`
	CheckedAt       string  `json:"checked_at,omitempty"`
	CheckError      string  `json:"check_error,omitempty"`
	Blocked         string  `json:"blocked,omitempty"` // e.g. needs a newer macOS
	CanInstall      bool    `json:"can_install"`       // running from an .app we can replace
	Phase           string  `json:"phase"`             // idle|checking|downloading|verifying|installing|restarting|error
	Percent         float64 `json:"percent"`
	Error           string  `json:"error,omitempty"`
	// ErrorCode classifies Error: "app_management" when macOS App Management
	// blocked the install (the page then offers settings / download / retry).
	ErrorCode string `json:"error_code,omitempty"`
	// DownloadURL is the signed DMG for installing this update by hand.
	DownloadURL string `json:"download_url,omitempty"`
}

// bundleSignature is what the platform verifier reports about an .app.
type bundleSignature struct {
	TeamID     string
	Identifier string
	Version    string // CFBundleShortVersionString
}

// bundleVerifier checks an extracted .app (codesign + Gatekeeper on macOS).
// Verify must fail if the signature is invalid or Gatekeeper rejects it.
type bundleVerifier interface {
	Verify(appPath string) (bundleSignature, error)
}

// Updater owns the update state. Safe for concurrent use.
type Updater struct {
	manifestURL string
	current     string
	client      *http.Client
	verifier    bundleVerifier
	osVersion   func() string
	appBundle   func() (string, error)
	// swap and stripQuarantine default to swapBundles / clearQuarantine;
	// tests inject failures.
	swap            func(current, newApp string) error
	stripQuarantine func(app string) error
	onChange        func(UpdateInfo)
	logf            func(string, ...any)

	mu       sync.Mutex
	info     UpdateInfo
	manifest *updateManifest
	busy     bool
}

func newUpdater() *Updater {
	current := version.Version
	// Testing aid: pretend to be an older build to exercise the update path.
	if v := os.Getenv("EGGIFY_UPDATE_FAKE_VERSION"); v != "" {
		current = v
	}
	manifestURL := defaultManifestURL
	if v := os.Getenv("EGGIFY_UPDATE_MANIFEST_URL"); v != "" {
		manifestURL = v
	}
	u := &Updater{
		manifestURL: manifestURL,
		current:     strings.TrimPrefix(current, "v"),
		client:      &http.Client{Timeout: 30 * time.Second},
		verifier:    platformVerifier{},
		osVersion:   platformOSVersion,
		appBundle:   runningAppBundle,
		logf:        dlog.Printf,
	}
	u.info = UpdateInfo{CurrentVersion: u.current, Phase: "idle"}
	return u
}

// State returns a snapshot of the update state.
func (u *Updater) State() UpdateInfo {
	u.mu.Lock()
	defer u.mu.Unlock()
	info := u.info
	info.CanInstall = u.canInstall()
	return info
}

// canInstall reports whether this process runs from an .app bundle that can be
// replaced in place (not from the disk image or App Translocation).
func (u *Updater) canInstall() bool {
	_, err := u.appBundle()
	return err == nil && runtime.GOOS == "darwin"
}

func (u *Updater) update(f func(*UpdateInfo)) {
	u.mu.Lock()
	f(&u.info)
	info := u.info
	cb := u.onChange
	u.mu.Unlock()
	// Every pushed state carries can_install too; without it the dashboard
	// thought the app could not update itself and only offered a download.
	info.CanInstall = u.canInstall()
	if cb != nil {
		cb(info)
	}
}

// Check fetches the manifest and records whether a newer version exists.
func (u *Updater) Check(ctx context.Context) (UpdateInfo, error) {
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return u.State(), nil
	}
	u.mu.Unlock()

	m, err := u.fetchManifest(ctx)
	now := time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		u.logf("update check failed: %v", err)
		u.update(func(i *UpdateInfo) { i.CheckedAt, i.CheckError = now, err.Error() })
		return u.State(), err
	}
	latest := strings.TrimPrefix(m.Version, "v")
	plat, platErr := m.platform(runtime.GOOS, runtime.GOARCH)
	downloadURL := defaultDownloadURL
	if platErr == nil && plat.DMGURL != "" && validateUpdateURL(plat.DMGURL, u.manifestURL) == nil {
		downloadURL = plat.DMGURL
	}
	avail := isNewerVersion(u.current, latest) && platErr == nil
	blocked := ""
	if avail && m.MinOS != "" && runtime.GOOS == "darwin" {
		if osv := u.osVersion(); osv != "" && compareVersions(osv, m.MinOS) < 0 {
			blocked = fmt.Sprintf("Eggify %s needs macOS %s or later", latest, m.MinOS)
		}
	}
	u.mu.Lock()
	u.manifest = m
	u.mu.Unlock()
	u.update(func(i *UpdateInfo) {
		i.LatestVersion = latest
		i.UpdateAvailable = avail && blocked == ""
		i.Blocked = blocked
		i.PublishedAt = m.PublishedAt
		i.ReleaseURL = m.ReleaseURL
		i.CheckedAt = now
		i.CheckError = ""
		i.DownloadURL = downloadURL
	})
	if avail {
		u.logf("update available: %s -> %s", u.current, latest)
	}
	return u.State(), nil
}

// RunPeriodic checks shortly after launch and then every updateCheckInterval.
func (u *Updater) RunPeriodic(ctx context.Context) {
	t := time.NewTimer(firstCheckDelay)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _ = u.Check(ctx)
			t.Reset(updateCheckInterval)
		}
	}
}

func (u *Updater) fetchManifest(ctx context.Context) (*updateManifest, error) {
	if u.manifestURL == "" {
		return nil, errors.New("this build has no update server configured")
	}
	if err := validateUpdateURL(u.manifestURL, u.manifestURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.manifestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", "Eggify-Desktop/"+u.current)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch manifest: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	return parseManifest(body)
}

// parseManifest decodes and sanity-checks latest.json.
func parseManifest(b []byte) (*updateManifest, error) {
	var m updateManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	m.Version = strings.TrimPrefix(strings.TrimSpace(m.Version), "v")
	if !validVersion(m.Version) {
		return nil, fmt.Errorf("manifest has invalid version %q", m.Version)
	}
	if len(m.Platforms) == 0 {
		return nil, errors.New("manifest lists no platforms")
	}
	return &m, nil
}

// platform returns the entry for goos-goarch, with a usable URL and SHA-256.
func (m *updateManifest) platform(goos, goarch string) (manifestPlatform, error) {
	p, ok := m.Platforms[goos+"-"+goarch]
	if !ok {
		return manifestPlatform{}, fmt.Errorf("no update published for %s-%s", goos, goarch)
	}
	if p.URL == "" {
		return manifestPlatform{}, fmt.Errorf("manifest entry %s-%s has no url", goos, goarch)
	}
	if !isSHA256Hex(p.SHA256) {
		return manifestPlatform{}, fmt.Errorf("manifest entry %s-%s has no valid sha256", goos, goarch)
	}
	return p, nil
}

// Install downloads, verifies and installs the update found by the last
// Check, then relaunches the app via quit. It returns once the new bundle is
// in place (or on error); quit is called afterwards.
func (u *Updater) Install(ctx context.Context, quit func()) error {
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return errors.New("an update is already in progress")
	}
	m := u.manifest
	avail := u.info.UpdateAvailable
	u.busy = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.busy = false
		u.mu.Unlock()
	}()

	// Install runs only when the user asks for it (dashboard or tray); a
	// failure, App Management included, is reported once and never retried
	// automatically.
	fail := func(err error) error {
		code := ""
		if errors.Is(err, errAppManagementBlocked) {
			code = errorCodeAppManagement
		}
		u.logf("update failed: %v", err)
		u.update(func(i *UpdateInfo) {
			i.Phase, i.Error, i.ErrorCode = "error", err.Error(), code
			if code != "" {
				i.Error = errAppManagementBlocked.Error() + "."
			}
		})
		return err
	}
	if m == nil || !avail {
		return fail(errors.New("no update available; check for updates first"))
	}
	if runtime.GOOS != "darwin" {
		return fail(fmt.Errorf("self-update is not supported on %s", runtime.GOOS))
	}
	plat, err := m.platform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return fail(err)
	}
	if err := validateUpdateURL(plat.URL, u.manifestURL); err != nil {
		return fail(err)
	}
	current, err := u.appBundle()
	if err != nil {
		return fail(err)
	}
	parent := filepath.Dir(current)
	if err := checkWritable(parent); err != nil {
		return fail(fmt.Errorf("cannot write to %s (move Eggify.app to /Applications): %w", parent, err))
	}

	// Leftovers of an earlier attempt (e.g. an old bundle that could not be
	// deleted) would otherwise pile up, hidden, next to the app.
	u.removeStaleStaging(parent)

	// Stage next to the running app so the final swap is a same-volume rename.
	stage, err := os.MkdirTemp(parent, stagingPrefix)
	if err != nil {
		return fail(fmt.Errorf("create staging dir: %w", err))
	}
	defer func() {
		if err := os.RemoveAll(stage); err != nil {
			u.logf("update: could not remove %s: %v", stage, err)
		}
	}()

	u.update(func(i *UpdateInfo) { i.Phase, i.Percent, i.Error, i.ErrorCode = "downloading", 0, "", "" })
	zipPath := filepath.Join(stage, "update.zip")
	if err := u.download(ctx, plat.URL, zipPath, plat.Size); err != nil {
		return fail(fmt.Errorf("download: %w", err))
	}

	u.update(func(i *UpdateInfo) { i.Phase, i.Percent = "verifying", 100 })
	if err := verifyFileSHA256(zipPath, plat.SHA256); err != nil {
		return fail(err)
	}
	extractDir := filepath.Join(stage, "extracted")
	if err := extractZip(zipPath, extractDir); err != nil {
		return fail(fmt.Errorf("extract: %w", err))
	}
	newApp, err := findAppBundle(extractDir)
	if err != nil {
		return fail(err)
	}
	sig, err := u.verifier.Verify(newApp)
	if err != nil {
		return fail(fmt.Errorf("update signature verification failed: %w", err))
	}
	if err := checkSignature(sig, expectedTeamID, bundleID, m.Version, u.current); err != nil {
		return fail(fmt.Errorf("update refused: %w", err))
	}
	u.logf("update %s verified: team %s, bundle %s", sig.Version, sig.TeamID, sig.Identifier)
	// In-process, not via /usr/bin/xattr: see the App Management note above.
	strip := u.stripQuarantine
	if strip == nil {
		strip = clearQuarantine
	}
	if err := strip(newApp); err != nil {
		u.logf("update: clearing quarantine: %v", err)
	}

	u.update(func(i *UpdateInfo) { i.Phase = "installing" })
	swap := u.swap
	if swap == nil {
		swap = swapBundles
	}
	// The swap is atomic: on failure the installed app is unchanged.
	if err := swap(current, newApp); err != nil {
		return fail(fmt.Errorf("install: %w", classifyInstallError(err)))
	}
	// newApp now holds the old version; the deferred RemoveAll deletes it.

	u.update(func(i *UpdateInfo) { i.Phase = "restarting" })
	if err := relaunchAfterExit(current); err != nil {
		u.logf("relaunch helper: %v (start Eggify again manually)", err)
	}
	if quit != nil {
		go func() {
			time.Sleep(500 * time.Millisecond) // let the UI show "Restarting…"
			quit()
		}()
	}
	return nil
}

// removeStaleStaging deletes staging dirs left next to the app by earlier
// attempts. Failures are only logged.
func (u *Updater) removeStaleStaging(parent string) {
	old, _ := filepath.Glob(filepath.Join(parent, stagingPrefix+"*"))
	for _, p := range old {
		if err := os.RemoveAll(p); err != nil {
			u.logf("update: could not remove stale %s: %v", p, err)
		}
	}
}

func (u *Updater) download(ctx context.Context, rawURL, dest string, wantSize int64) error {
	if wantSize > maxUpdateBytes {
		return fmt.Errorf("update is too large (%d bytes)", wantSize)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Eggify-Desktop/"+u.current)
	client := &http.Client{
		// The body timeout is the context; don't follow redirects off our host.
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return validateUpdateURL(r.URL.String(), u.manifestURL)
		},
		Transport: u.client.Transport,
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	total := resp.ContentLength
	if total <= 0 {
		total = wantSize
	}
	out, err := os.Create(dest) // #nosec G304 -- path inside our staging dir
	if err != nil {
		return err
	}
	defer out.Close()

	var done int64
	last := time.Now()
	buf := make([]byte, 64<<10)
	body := io.LimitReader(resp.Body, maxUpdateBytes+1)
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if done > maxUpdateBytes {
				return errors.New("update exceeds the size limit")
			}
			if time.Since(last) > 200*time.Millisecond && total > 0 {
				pct := float64(done) / float64(total) * 100
				u.update(func(i *UpdateInfo) { i.Percent = pct })
				last = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if wantSize > 0 && done != wantSize {
		return fmt.Errorf("size mismatch: manifest says %d bytes, got %d", wantSize, done)
	}
	u.update(func(i *UpdateInfo) { i.Percent = 100 })
	return out.Sync()
}

// ── pure helpers (unit-tested) ─────────────────────────────────────────────

// validateUpdateURL allows https URLs on trusted hosts. When the manifest URL
// was overridden for testing (EGGIFY_UPDATE_MANIFEST_URL), its host is trusted
// too, and plain http is accepted for loopback hosts only. The signature
// checks still apply to anything downloaded from there.
func validateUpdateURL(raw, manifestURL string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid update URL: %w", err)
	}
	host := u.Hostname()
	trusted := trustedUpdateHost(host)
	if !trusted && manifestURL != "" && manifestURL != defaultManifestURL {
		if mu, err := url.Parse(manifestURL); err == nil && mu.Hostname() == host {
			trusted = true
		}
	}
	if !trusted {
		return fmt.Errorf("untrusted update host %q", host)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(host) {
			return nil
		}
	}
	return fmt.Errorf("update URL must use https, got %q", u.Scheme)
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// checkSignature applies the policy to what the verifier reported: our team,
// our bundle id, exactly the advertised version, and newer than what runs.
func checkSignature(sig bundleSignature, team, id, manifestVersion, current string) error {
	if team == "" {
		return errors.New("no expected team id compiled in")
	}
	if sig.TeamID != team {
		return fmt.Errorf("signed by team %q, expected %q", sig.TeamID, team)
	}
	if sig.Identifier != id {
		return fmt.Errorf("bundle id %q, expected %q", sig.Identifier, id)
	}
	if compareVersions(sig.Version, manifestVersion) != 0 {
		return fmt.Errorf("bundle version %q does not match manifest version %q", sig.Version, manifestVersion)
	}
	if !isNewerVersion(current, sig.Version) {
		return fmt.Errorf("bundle version %q is not newer than %q", sig.Version, current)
	}
	return nil
}

func verifyFileSHA256(path, want string) error {
	if !isSHA256Hex(want) {
		return errors.New("no valid sha256 to verify against")
	}
	got, err := hashFile(path)
	if err != nil {
		return fmt.Errorf("hash download: %w", err)
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", want, got)
	}
	return nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- our staging dir
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func validVersion(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.Atoi(numericPrefix(p)); err != nil {
			return false
		}
	}
	return true
}

// isNewerVersion reports whether latest > current. Dev/empty builds never update.
func isNewerVersion(current, latest string) bool {
	current = strings.TrimPrefix(current, "v")
	latest = strings.TrimPrefix(latest, "v")
	if current == "" || current == "dev" || !validVersion(latest) {
		return false
	}
	return compareVersions(latest, current) > 0
}

// compareVersions compares dotted numeric versions ("2.0.10" > "2.0.9").
// Pre-release suffixes are ignored ("2.1.0-beta" == "2.1.0").
func compareVersions(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	n := max(len(pa), len(pb), 3)
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(numericPrefix(pa[i]))
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(numericPrefix(pb[i]))
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func numericPrefix(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return "x" // not a number
	}
	return s[:i]
}

// findAppBundle returns the single .app directly inside dir.
func findAppBundle(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var found []string
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".app") {
			found = append(found, filepath.Join(dir, e.Name()))
		}
	}
	switch len(found) {
	case 0:
		return "", errors.New("no .app in the update archive")
	case 1:
		return found[0], nil
	default:
		return "", errors.New("more than one .app in the update archive")
	}
}

func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".eggify-write-test-")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// runningAppBundle returns /path/Eggify.app when running from a bundle.
func runningAppBundle() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	// .../Eggify.app/Contents/MacOS/eggify-desktop
	app := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if !strings.HasSuffix(app, ".app") || filepath.Base(filepath.Dir(exe)) != "MacOS" {
		return "", errors.New("not running from an .app bundle (self-update disabled)")
	}
	// Running from a mounted DMG or App Translocation: can't replace in place.
	if strings.HasPrefix(app, "/Volumes/") || strings.Contains(app, "/AppTranslocation/") {
		return "", errors.New("Eggify is running from the disk image; move it to /Applications to enable updates")
	}
	return app, nil
}

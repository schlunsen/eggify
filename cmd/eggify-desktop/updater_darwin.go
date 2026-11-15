//go:build darwin

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// platformVerifier checks an .app with codesign and Gatekeeper, like n0's
// verifyDarwinSignature and the pre-publish gate in scripts/desktop-release.sh.
type platformVerifier struct{}

func (platformVerifier) Verify(app string) (bundleSignature, error) {
	var sig bundleSignature
	// 1. Signature and sealed resources intact (nested dylibs included).
	if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", app).CombinedOutput(); err != nil { // #nosec G204 -- our staging path
		return sig, fmt.Errorf("codesign --verify: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// 2. Who signed it, and as what.
	out, err := exec.Command("codesign", "-dv", "--verbose=4", app).CombinedOutput() // #nosec G204
	if err != nil {
		return sig, fmt.Errorf("codesign -dv: %w: %s", err, strings.TrimSpace(string(out)))
	}
	sig.TeamID, sig.Identifier = parseCodesignInfo(string(out))
	// 3. Gatekeeper: Developer ID + notarised.
	if out, err := exec.Command("spctl", "--assess", "--type", "exec", app).CombinedOutput(); err != nil { // #nosec G204
		return sig, fmt.Errorf("Gatekeeper rejected the bundle: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// 4. Version from the (sealed) Info.plist.
	v, err := exec.Command("plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-", app+"/Contents/Info.plist").Output() // #nosec G204
	if err != nil {
		return sig, fmt.Errorf("read bundle version: %w", err)
	}
	sig.Version = strings.TrimSpace(string(v))
	return sig, nil
}

// extractZip unpacks with ditto: the archive is made by `ditto -c -k
// --keepParent`, and Go's archive/zip would turn its AppleDouble entries into
// files inside the bundle and break the signature seal.
func extractZip(zipPath, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if out, err := exec.Command("ditto", "-x", "-k", zipPath, dest).CombinedOutput(); err != nil { // #nosec G204
		return fmt.Errorf("ditto: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// clearQuarantine drops quarantine flags from an already verified bundle. It
// runs in this process (same team as the installed app) rather than through
// /usr/bin/xattr, which App Management would treat as another app modifying a
// bundle. Only files that carry the flag are touched.
func clearQuarantine(app string) error {
	const attr = "com.apple.quarantine"
	var firstErr error
	_ = filepath.WalkDir(app, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if _, gerr := unix.Lgetxattr(p, attr, nil); gerr != nil {
			return nil // not set
		}
		if rerr := unix.Lremovexattr(p, attr); rerr != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", p, rerr)
		}
		return nil
	})
	return firstErr
}

// swapBundles atomically exchanges the running app with the new one
// (renamex_np RENAME_SWAP); afterwards newApp holds the old version.
// Both paths are on the same volume (the update is staged next to the app).
func swapBundles(current, newApp string) error {
	err := unix.RenamexNp(newApp, current, unix.RENAME_SWAP)
	if err == nil {
		return nil
	}
	if !errors.Is(err, unix.ENOTSUP) && !errors.Is(err, unix.EINVAL) {
		return err
	}
	// Filesystem without RENAME_SWAP: two renames with rollback (n0's approach).
	backup := current + ".old"
	_ = os.RemoveAll(backup)
	if err := os.Rename(current, backup); err != nil {
		return err
	}
	if err := os.Rename(newApp, current); err != nil {
		_ = os.Rename(backup, current)
		return err
	}
	return os.Rename(backup, newApp)
}

// scheduleRelaunch starts a detached helper that waits for this process to
// exit (single-instance lock, loopback port) and then opens the app again.
func scheduleRelaunch(app string) error {
	script := `while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; sleep 0.5; exec /usr/bin/open "$2"`
	cmd := exec.Command("/bin/sh", "-c", script, "eggify-relaunch", strconv.Itoa(os.Getpid()), app) // #nosec G204
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

func platformOSVersion() string {
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// parseCodesignInfo extracts TeamIdentifier and Identifier from `codesign -dv`.
func parseCodesignInfo(out string) (team, id string) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "TeamIdentifier="); ok {
			team = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "Identifier="); ok {
			id = strings.TrimSpace(v)
		}
	}
	if team == "not set" {
		team = ""
	}
	return team, id
}

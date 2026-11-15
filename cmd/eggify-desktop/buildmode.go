package main

import "os"

// releaseBuild is "true" in builds made by scripts/build-desktop-wails.sh
// (-X main.releaseBuild=true). Release builds ignore the environment knobs
// that exist for testing: a process that can set this app's environment (an
// agent that edits a launcher, a plist or a shell profile) must not be able to
// point the updater at its own manifest, pretend to be an older version, or
// inject script into the window.
var releaseBuild = ""

func isReleaseBuild() bool { return releaseBuild == "true" }

// devOnlyEnv reads a testing knob: always empty in a release build.
func devOnlyEnv(name string) string {
	if isReleaseBuild() {
		return ""
	}
	return os.Getenv(name)
}

// diagExtraJS is the extra script EGGIFY_DESKTOP_DIAG_SCRIPT names (test
// harness only), empty in release builds.
func diagExtraJS() string {
	f := devOnlyEnv("EGGIFY_DESKTOP_DIAG_SCRIPT")
	if f == "" {
		return ""
	}
	extra, err := os.ReadFile(f)
	if err != nil {
		return ""
	}
	return "\n" + string(extra)
}

// Package shellenv imports the user's login-shell environment into the
// current process.
//
// macOS GUI apps started from Finder/Dock get a minimal environment (PATH is
// /usr/bin:/bin:/usr/sbin:/sbin, no API keys), so the desktop app copies the
// environment of `$SHELL -l -i -c env` before the server reads its config.
// This is the Go port of the Tauri app's load_shell_env (src-tauri/src/sidecar.rs).
package shellenv

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// marker separates anything the interactive shell prints (motd, prompt
// plugins) from the environment dump.
const marker = "__EGGIFY_SHELL_ENV_BEGIN__"

// DefaultTimeout bounds how long a slow shell config may delay startup.
const DefaultTimeout = 5 * time.Second

// Read runs the login shell and returns its environment. shell defaults to
// $SHELL, then /bin/zsh.
func Read(ctx context.Context, shell string) (map[string]string, error) {
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	if shell == "" {
		shell = "/bin/zsh"
	}
	cmd := exec.CommandContext(ctx, shell, "-l", "-i", "-c", "printf '%s\\0' "+marker+"; command env -0")
	cmd.Stdin = nil
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, err
	}
	return Parse(out), nil
}

// Parse decodes the NUL-separated `env -0` output that follows the marker.
// Values may contain newlines.
func Parse(out []byte) map[string]string {
	if i := bytes.Index(out, []byte(marker+"\x00")); i >= 0 {
		out = out[i+len(marker)+1:]
	}
	env := make(map[string]string)
	for _, entry := range bytes.Split(out, []byte{0}) {
		k, v, ok := strings.Cut(string(entry), "=")
		if !ok || k == "" || strings.ContainsAny(k, "\n\x00") {
			continue
		}
		env[k] = v
	}
	return env
}

// alwaysFromShell are taken from the shell even when already set, because
// the GUI launcher sets a useless default for them.
var alwaysFromShell = map[string]bool{"PATH": true}

// neverFromShell describe the shell process itself, not the user's setup.
var neverFromShell = map[string]bool{"_": true, "PWD": true, "OLDPWD": true, "SHLVL": true}

// Apply sets the variables from env that are not already set (PATH is always
// replaced). Variables the launcher set explicitly therefore win, e.g.
// EGGIFY_* overrides or an isolated HOME for tests. Returns how many were set.
func Apply(env map[string]string) int {
	n := 0
	for k, v := range env {
		if neverFromShell[k] {
			continue
		}
		if _, set := os.LookupEnv(k); set && !alwaysFromShell[k] {
			continue
		}
		if os.Setenv(k, v) == nil {
			n++
		}
	}
	return n
}

// Import reads the login-shell environment (bounded by DefaultTimeout) and
// applies it. Errors are returned for logging; the process keeps its own
// environment then.
func Import() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	env, err := Read(ctx, "")
	if err != nil {
		return 0, err
	}
	return Apply(env), nil
}

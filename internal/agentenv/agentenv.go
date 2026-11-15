// Package agentenv builds the environment of processes that run on behalf of
// an agent (Claude CLI, Codex, eggify-tools, loop verify commands, one-shot
// digests, login flows). Agents run as the Eggify user and may be
// prompt-injected, so they get an allowlisted copy of the server's
// environment, never the whole of it: the server's own secrets
// (EGGIFY_*, ngrok and cloud credentials, anything named *_TOKEN, *_SECRET,
// *_PASSWORD, *_API_KEY) stay behind unless Eggify sets them on purpose.
//
// EGGIFY_AGENT_ENV=inherit restores the old behaviour (everything is passed
// on); EGGIFY_AGENT_ENV_ALLOW=NAME1,NAME2 passes extra names through.
package agentenv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/schlunsen/claude-agent-sdk-go/types"
)

const (
	// ModeEnv selects the policy: "inherit" passes everything on.
	ModeEnv = "EGGIFY_AGENT_ENV"
	// AllowEnv lists extra variable names (comma separated) to pass on.
	AllowEnv = "EGGIFY_AGENT_ENV_ALLOW"
)

// Runtime names what the child is: the Claude CLI and Codex are given the
// vendor variables they run on, other children are not.
type Runtime int

const (
	// Generic children (commands, MCP servers, login status checks).
	Generic Runtime = iota
	// Claude is the Claude CLI.
	Claude
	// Codex is the codex CLI / app-server.
	Codex
	// Login is a `claude auth status` / `codex login` check: it sees both
	// vendors' variables, like the sessions it reports on.
	Login
)

var allowedNames = toSet(
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LANGUAGE", "TERM", "COLORTERM", "TMPDIR", "TMP", "TEMP", "TZ",
	"PWD", "SHLVL", "DISPLAY", "WAYLAND_DISPLAY", "EDITOR", "VISUAL", "PAGER", "COLUMNS", "LINES",
	"SSH_AUTH_SOCK", "SSH_AGENT_PID", "__CF_USER_TEXT_ENCODING", "__CFBundleIdentifier",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "no_proxy", "all_proxy",
	"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "SSL_CERT_DIR", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE",
	"GOPATH", "GOROOT", "GOFLAGS", "GOBIN", "GOPROXY", "GONOSUMDB", "GONOSUMCHECK", "GOPRIVATE", "GOTOOLCHAIN", "GOCACHE", "GOMODCACHE", "GOOS", "GOARCH", "CGO_ENABLED",
	"CARGO_HOME", "RUSTUP_HOME", "RUSTUP_TOOLCHAIN", "RUSTFLAGS", "JAVA_HOME", "ANDROID_HOME", "ANDROID_SDK_ROOT", "ANDROID_NDK_HOME",
	"NVM_DIR", "NVM_BIN", "NVM_INC", "PNPM_HOME", "VOLTA_HOME", "BUN_INSTALL", "FNM_DIR", "FNM_MULTISHELL_PATH", "ASDF_DIR", "ASDF_DATA_DIR", "MISE_DATA_DIR",
	"PYENV_ROOT", "PYENV_VERSION", "VIRTUAL_ENV", "PYTHONPATH", "PYTHONUSERBASE", "PIPX_HOME", "UV_CACHE_DIR", "POETRY_HOME", "RBENV_ROOT", "GEM_HOME", "GEM_PATH", "BUNDLE_PATH",
	"DEVELOPER_DIR", "SDKROOT", "CC", "CXX", "CFLAGS", "CXXFLAGS", "LDFLAGS", "CPPFLAGS", "PKG_CONFIG_PATH", "MACOSX_DEPLOYMENT_TARGET",
	"CI", "NO_COLOR", "FORCE_COLOR", "GIT_PAGER", "GIT_EDITOR", "GIT_SSH", "GIT_SSH_COMMAND", "ZDOTDIR", "DOCKER_HOST", "DOCKER_CONTEXT", "KUBECONFIG", "GNUPGHOME", "LESS",
	"MAX_THINKING_TOKENS", "DISABLE_TELEMETRY", "DISABLE_AUTOUPDATER", "DISABLE_ERROR_REPORTING", "DISABLE_NON_ESSENTIAL_MODEL_CALLS", "MCP_TIMEOUT", "MCP_TOOL_TIMEOUT", "BASH_DEFAULT_TIMEOUT_MS", "BASH_MAX_TIMEOUT_MS", "API_TIMEOUT_MS",
)

// allowedPrefixes are families of harmless configuration variables.
var allowedPrefixes = []string{"GIT_AUTHOR_", "GIT_COMMITTER_", "MISE_", "ASDF_", "DIRENV_", "LC_", "XDG_", "CONDA_", "HOMEBREW_", "MAMBA_", "NPM_CONFIG_CACHE", "ITERM_", "TERM_", "VSCODE_", "COMPOSER_HOME"}

// neverPassed are names that change what code a child runs or where git
// works; they are not in the allowlist and are listed for the record and the
// tests (the user may still name one in EGGIFY_AGENT_ENV_ALLOW).
var neverPassed = toSet(
	"GIT_DIR", "GIT_WORK_TREE", "GIT_ASKPASS", "SSH_ASKPASS", "BASH_ENV", "ENV", "PROMPT_COMMAND",
	"NODE_OPTIONS", "PYTHONSTARTUP", "LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH",
)

// cloudSelectors are non-secret cloud settings the Claude CLI always gets.
var cloudSelectors = toSet(
	"AWS_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE",
	"CLOUD_ML_REGION", "GOOGLE_CLOUD_PROJECT",
)

// secretSuffixes mark names that hold a credential. Such a name is dropped
// unless Eggify sets it on purpose or the user allows it.
var secretSuffixes = []string{"_SECRET", "_TOKEN", "_PASSWORD", "_PASSWD", "_API_KEY", "_APIKEY", "_ACCESS_KEY", "_SECRET_KEY", "_PRIVATE_KEY", "_KEY", "_CREDENTIALS", "_AUTH"}

// vendorPrefixes are the variables each runtime is given on purpose
// (provider keys and the capability the provider proxy sets are written by
// Eggify itself; the rest is the CLI's own configuration).
func vendorPrefixes(rt Runtime) []string {
	switch rt {
	case Claude:
		return []string{"CLAUDE_", "ANTHROPIC_"}
	case Codex:
		return []string{"CODEX_", "OPENAI_"}
	case Login:
		return []string{"CLAUDE_", "ANTHROPIC_", "CODEX_", "OPENAI_"}
	}
	return nil
}

// cloudPrefixes are kept for the Claude CLI when it runs on that cloud
// (CLAUDE_CODE_USE_BEDROCK / _VERTEX / _FOUNDRY).
func cloudPrefixes(rt Runtime, environ map[string]string) []string {
	if rt != Claude && rt != Login {
		return nil
	}
	var out []string
	on := func(k string) bool {
		v := strings.ToLower(environ[k])
		return v != "" && v != "0" && v != "false"
	}
	if on("CLAUDE_CODE_USE_BEDROCK") {
		out = append(out, "AWS_")
	}
	if on("CLAUDE_CODE_USE_VERTEX") {
		out = append(out, "GOOGLE_", "CLOUD_ML_REGION", "VERTEX_")
	}
	if on("CLAUDE_CODE_USE_FOUNDRY") {
		out = append(out, "AZURE_")
	}
	return out
}

// Inherit reports whether the old behaviour (pass everything) is selected.
func Inherit() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(ModeEnv)), "inherit")
}

func userAllowed() map[string]bool {
	out := map[string]bool{}
	for _, n := range strings.Split(os.Getenv(AllowEnv), ",") {
		if n = strings.TrimSpace(n); n != "" {
			out[n] = true
		}
	}
	return out
}

func toSet(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func isSecretName(name string) bool {
	u := strings.ToUpper(name)
	for _, s := range secretSuffixes {
		if strings.HasSuffix(u, s) {
			return true
		}
	}
	return false
}

// Allowed reports whether a variable of the server's environment may reach
// an agent child of the given runtime. environ is the server's environment
// as a map (used to see which cloud the Claude CLI runs on).
func allowed(name string, rt Runtime, environ map[string]string, extra map[string]bool) bool {
	if extra[name] {
		return true
	}
	if (rt == Claude || rt == Login) && cloudSelectors[name] {
		return true
	}
	if neverPassed[name] || strings.HasPrefix(name, "DYLD_") || strings.HasPrefix(name, "GIT_CONFIG_") {
		return false
	}
	if strings.HasPrefix(name, "EGGIFY_") || name == "NGROK_AUTHTOKEN" {
		return false
	}
	for _, p := range vendorPrefixes(rt) {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	for _, p := range cloudPrefixes(rt, environ) {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	if isSecretName(name) {
		return false
	}
	if allowedNames[name] {
		return true
	}
	for _, p := range allowedPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func environMap(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			m[k] = v
		}
	}
	return m
}

// Filter returns the entries of environ an agent child of runtime rt may
// have. With EGGIFY_AGENT_ENV=inherit it returns environ unchanged.
func Filter(environ []string, rt Runtime) []string {
	if Inherit() {
		return append([]string(nil), environ...)
	}
	m := environMap(environ)
	if rt == Claude || rt == Login {
		for k, v := range claudeSettingsEnv() {
			if _, ok := m[k]; !ok {
				m[k] = v
			}
		}
	}
	extra := userAllowed()
	out := make([]string, 0, len(environ)/2)
	for _, kv := range environ {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		if allowed(k, rt, m, extra) {
			out = append(out, kv)
		}
	}
	return out
}

// Child returns the filtered server environment followed by extra
// "NAME=value" entries, which Eggify sets on purpose and are never filtered
// (later entries win, as with exec.Cmd.Env).
func Child(rt Runtime, extra ...string) []string {
	return append(Filter(os.Environ(), rt), extra...)
}

// Unset returns the names of the server's environment an agent child of
// runtime rt must not have, except those in keep (set on purpose for the
// child). overlay is extra environment the child will have (explicit options
// and the Claude settings' env block), which decides the cloud switches.
func Unset(rt Runtime, overlay map[string]string, keep map[string]string) []string {
	if Inherit() {
		return nil
	}
	m := environMap(os.Environ())
	for k, v := range overlay {
		m[k] = v
	}
	if rt == Claude || rt == Login {
		for k, v := range claudeSettingsEnv() {
			if _, ok := m[k]; !ok {
				m[k] = v
			}
		}
	}
	extra := userAllowed()
	var out []string
	for _, kv := range os.Environ() {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		if _, set := keep[k]; set {
			continue
		}
		if !allowed(k, rt, m, extra) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// claudeSettingsEnv is the "env" block of ~/.claude/settings.json: the CLI
// applies it itself, and it may switch the CLI to Bedrock/Vertex/Foundry.
func claudeSettingsEnv() map[string]string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return nil
	}
	var st struct {
		Env map[string]any `json:"env"`
	}
	if json.Unmarshal(data, &st) != nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range st.Env {
		out[k] = fmt.Sprint(v)
	}
	return out
}

// Dropped lists the names Filter would remove, for tests and diagnostics.
func Dropped(environ []string, rt Runtime) []string {
	keep := map[string]bool{}
	for _, kv := range Filter(environ, rt) {
		k, _, _ := strings.Cut(kv, "=")
		keep[k] = true
	}
	var out []string
	for k := range environMap(environ) {
		if !keep[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// ScrubClaudeOptions keeps the server's secrets and code-changing variables
// away from the Claude CLI. The Claude Agent SDK starts the CLI with the
// server's whole environment and can only set names, never unset them, and an
// empty value is not an unset one (GIT_AUTHOR_NAME= makes git refuse to
// commit, ZDOTDIR= skips ~/.zshrc, GIT_DIR= breaks every git call). So the
// names are really removed by a small launcher script that the CLI path
// points to (`env -u NAME ... <claude> "$@"`). Where no launcher can be made
// (no claude found, no /usr/bin/env), only secret-shaped names are blanked,
// and every other inherited variable reaches the CLI untouched. Names set in
// opts.Env (credentials, connector keys, the provider-proxy capability,
// cloud switches) are never removed.
func ScrubClaudeOptions(opts *types.ClaudeAgentOptions) *types.ClaudeAgentOptions {
	if opts == nil || Inherit() {
		return opts
	}
	if opts.CLIPath != nil && strings.Contains(*opts.CLIPath, string(filepath.Separator)+"launchers"+string(filepath.Separator)+"claude-") {
		return opts // already scrubbed (options are reused across restarts)
	}
	names := Unset(Claude, opts.Env, opts.Env)
	if len(names) == 0 {
		return opts
	}
	if launcher, err := writeLauncher(realClaudePath(opts), names); err == nil {
		return opts.WithCLIPath(launcher)
	}
	for _, name := range names {
		if secretShaped(name) {
			opts = opts.WithEnvVar(name, "")
		}
	}
	return opts
}

// secretShaped names are blanked when they cannot be unset: server secrets,
// cloud credentials and anything named like a credential.
func secretShaped(name string) bool {
	return strings.HasPrefix(name, "EGGIFY_") || strings.HasPrefix(name, "NGROK_") || strings.HasPrefix(name, "AWS_") ||
		strings.HasPrefix(name, "AZURE_") || name == "GOOGLE_APPLICATION_CREDENTIALS" || isSecretName(name)
}

func realClaudePath(opts *types.ClaudeAgentOptions) string {
	if opts.CLIPath != nil && *opts.CLIPath != "" {
		return *opts.CLIPath
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(home, ".claude", "local", "claude"), filepath.Join(home, ".npm-global", "bin", "claude"),
		"/usr/local/bin/claude", filepath.Join(home, ".local", "bin", "claude"),
		filepath.Join(home, "node_modules", ".bin", "claude"), filepath.Join(home, ".yarn", "bin", "claude"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// shellQuote single-quotes s for /bin/sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// writeLauncher writes (once per content) a script that unsets names and
// runs the real CLI, and returns its path.
func writeLauncher(real string, names []string) (string, error) {
	if real == "" || runtime.GOOS == "windows" {
		return "", errors.New("no launcher")
	}
	if _, err := os.Stat("/usr/bin/env"); err != nil {
		return "", err
	}
	if abs, err := filepath.Abs(real); err == nil {
		real = abs
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# Eggify: starts the Claude CLI without the server's secrets (internal/agentenv).\nexec /usr/bin/env")
	// Every name is single-quoted, so unusual ones (my.token, deploy-key,
	// BASH_FUNC_x%%) are unset too instead of being left inherited.
	for _, n := range names {
		if n == "" || strings.ContainsAny(n, "=\x00") {
			continue
		}
		b.WriteString(" -u " + shellQuote(n))
	}
	b.WriteString(" " + shellQuote(real) + " \"$@\"\n")
	script := b.String()
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".claude", "eggify", "launchers")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(script))
	path := filepath.Join(dir, "claude-"+hex.EncodeToString(sum[:8])+".sh")
	if cur, err := os.ReadFile(path); err == nil && string(cur) == script {
		return path, nil
	}
	tmp, err := os.CreateTemp(dir, ".launcher-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.WriteString(script); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	if err := os.Chmod(tmp.Name(), 0o700); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return path, nil
}

var logOnce sync.Once

// LogPolicy logs once, names only (never values), which inherited variables
// agent children will not get, and how to change that.
func LogPolicy(logf func(format string, args ...any)) {
	logOnce.Do(func() {
		if Inherit() {
			logf("Agent environment: %s=inherit, agents get the server's whole environment", ModeEnv)
			return
		}
		dropped := Dropped(os.Environ(), Generic)
		if len(dropped) == 0 {
			return
		}
		const max = 40
		shown := dropped
		more := ""
		if len(shown) > max {
			shown, more = dropped[:max], fmt.Sprintf(" and %d more", len(dropped)-max)
		}
		logf("Agent environment: %d inherited variables are not passed to agents (%s%s). Pass some on with %s=NAME1,NAME2, or restore the old behaviour with %s=inherit",
			len(dropped), strings.Join(shown, ", "), more, AllowEnv, ModeEnv)
	})
}

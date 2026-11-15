// Package agents - sandbox.go keeps Eggify's own secrets out of agent reach and
// implements the optional per-session "Sandboxed" mode.
//
// Every Claude SDK session gets permission DENY rules (passed with --settings)
// for Eggify's private state and agent credential files, so the Read/Edit/Write
// tools cannot open them, even in YOLO mode. Those rules alone do not stop
// Bash (`cat ~/.claude/eggify/.secret`) — only the sandbox does.
//
// A sandboxed session additionally turns on Claude Code's built-in Bash
// sandbox (macOS Seatbelt / Linux bubblewrap):
//   - the same paths are unreadable for every sandboxed command,
//   - writes to config that runs code outside the sandbox (.mcp.json,
//     .git/hooks, .git/config, .claude settings) are refused,
//   - network egress goes through the CLI's proxy and only the allowlisted
//     domains are reachable (strictAllowlist: no prompting),
//   - commands cannot opt out (allowUnsandboxedCommands: false),
//   - provider credentials are removed from the command environment,
//   - the CLI refuses to start if the sandbox cannot be set up.
//
// WebFetch/WebSearch run inside the CLI, not in the sandbox, so sandboxed
// sessions get an "ask" rule for them (ask rules hold in YOLO mode too) and the
// permission callback auto-approves only WebFetch calls to allowlisted hosts.
//
// Codex sessions map Sandboxed onto a Codex permission profile (workspace
// write, same filesystem denies, network off unless authorized); see codexSandboxConfig.
// Direct (OpenAI-compatible) sessions run their tools inside the eggify-tools MCP
// server, which has no sandbox, so Sandboxed is refused for them.
package agents

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
)

// user_settings keys for the global sandbox preferences. They are reserved:
// the generic /api/settings endpoints refuse to write them (see
// IsReservedSettingKey), so only the privileged sandbox endpoint changes them.
const (
	SandboxSettingDefaultKey = "sandbox.default_enabled"
	SandboxSettingDomainsKey = "sandbox.allowed_domains"
	sandboxSettingKeyPrefix  = "sandbox."
)

// DefaultSandboxAllowedDomains is the egress allowlist used until the user
// edits it in Settings: package registries and GitHub, so installs and git
// over HTTPS keep working. Model provider hosts are not needed here — the CLI
// talks to the model outside the sandbox.
var DefaultSandboxAllowedDomains = []string{
	"registry.npmjs.org",
	"registry.yarnpkg.com",
	"proxy.golang.org",
	"sum.golang.org",
	"pypi.org",
	"files.pythonhosted.org",
	"github.com",
	"api.github.com",
	"codeload.github.com",
	"objects.githubusercontent.com",
	"raw.githubusercontent.com",
	"crates.io",
	"static.crates.io",
	"index.crates.io",
}

// sandboxDeniedEnvVars are removed from the environment of sandboxed commands:
// the credentials the CLI itself needs to reach the model, which a command
// never does.
var sandboxDeniedEnvVars = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"CLAUDE_API_KEY",
	"CLAUDE_CODE_OAUTH_TOKEN",
	"OPENAI_API_KEY",
	"CODEX_API_KEY",
	"EGGIFY_CODEX_API_KEY",
	"EGGIFY_API_KEY",
}

// SandboxPreferences are the global sandbox settings.
type SandboxPreferences struct {
	// DefaultEnabled is applied to new sessions that do not choose explicitly.
	// On until the user saves a choice (Autopilot is the default way to run).
	DefaultEnabled bool `json:"default_enabled"`
	// DefaultIsImplicit is true while the user never saved the default: the
	// session paths that cannot be sandboxed then start unsandboxed instead of failing.
	DefaultIsImplicit bool `json:"-"`
	// AllowedDomains is the egress allowlist for sandboxed sessions.
	AllowedDomains []string `json:"allowed_domains"`
}

// IsReservedSettingKey reports whether a user_settings key may only be written
// through a dedicated, privileged endpoint.
func IsReservedSettingKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	return strings.HasPrefix(k, sandboxSettingKeyPrefix) || k == QuestionPolicySettingKey
}

// isSandboxed reports whether the session runs in Sandboxed mode.
func isSandboxed(opts *SessionOptions) bool {
	return opts != nil && opts.Sandboxed != nil && *opts.Sandboxed
}

var domainPattern = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,62}$`)

// NormalizeSandboxDomains validates and normalises an allowlist: lowercase,
// trimmed, de-duplicated, sorted. Entries must be host names, optionally with a
// leading "*." wildcard; URLs, ports, paths, IPs and bare TLDs are rejected so
// a typo cannot silently open everything.
func NormalizeSandboxDomains(in []string) ([]string, error) {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
		if d == "" {
			continue
		}
		if !domainPattern.MatchString(d) {
			return nil, fmt.Errorf("invalid domain %q: use a host name like example.com or *.example.com", raw)
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out, nil
}

// LoadSandboxPreferences reads the global preferences; missing or invalid
// values fall back to on (an explicit saved false is respected) +
// DefaultSandboxAllowedDomains. Without a database (unit tests) it stays off.
func LoadSandboxPreferences(repo *database.Repository) SandboxPreferences {
	prefs := SandboxPreferences{AllowedDomains: append([]string(nil), DefaultSandboxAllowedDomains...)}
	if repo == nil {
		return prefs
	}
	prefs.DefaultEnabled, prefs.DefaultIsImplicit = true, true
	if s, err := repo.GetUserSetting(SandboxSettingDefaultKey); err == nil && s != nil {
		prefs.DefaultEnabled = strings.EqualFold(strings.TrimSpace(s.Value), "true")
		prefs.DefaultIsImplicit = false
	}
	if s, err := repo.GetUserSetting(SandboxSettingDomainsKey); err == nil && s != nil {
		var domains []string
		if err := json.Unmarshal([]byte(s.Value), &domains); err != nil {
			logging.Warning("sandbox: ignoring unreadable allowlist setting: %v", err)
		} else if norm, err := NormalizeSandboxDomains(domains); err != nil {
			logging.Warning("sandbox: ignoring invalid allowlist setting: %v", err)
		} else {
			prefs.AllowedDomains = norm
		}
	}
	return prefs
}

// SaveSandboxPreferences validates and stores the global preferences.
// Callers must have checked that the request comes from a privileged principal.
func SaveSandboxPreferences(repo *database.Repository, prefs SandboxPreferences) (SandboxPreferences, error) {
	if repo == nil {
		return prefs, fmt.Errorf("database not available")
	}
	norm, err := NormalizeSandboxDomains(prefs.AllowedDomains)
	if err != nil {
		return prefs, err
	}
	prefs.AllowedDomains = norm
	data, err := json.Marshal(norm)
	if err != nil {
		return prefs, err
	}
	if err := repo.SetUserSetting(&database.UserSetting{
		Key: SandboxSettingDomainsKey, Value: string(data), ValueType: "json",
		Description: "Egress allowlist for sandboxed agent sessions",
	}); err != nil {
		return prefs, err
	}
	// Only a choice the user made is written: saving the allowlist alone must
	// not turn the built-in default into a saved one.
	if !prefs.DefaultIsImplicit {
		if err := repo.SetUserSetting(&database.UserSetting{
			Key: SandboxSettingDefaultKey, Value: fmt.Sprintf("%t", prefs.DefaultEnabled), ValueType: "boolean",
			Description: "New agent sessions are sandboxed by default",
		}); err != nil {
			return prefs, err
		}
	}
	return prefs, nil
}

// SandboxPreferences returns the current global preferences.
func (sm *SessionManager) SandboxPreferences() SandboxPreferences {
	return LoadSandboxPreferences(sm.repo)
}

// SandboxOptOutAction is the privileged action of creating an unsandboxed
// session while sandboxing is the default.
const SandboxOptOutAction = "Starting an unsandboxed session"

// SandboxOptOutAction returns SandboxOptOutAction when opts explicitly turn
// sandboxing off although the global default is on, else "". Such a session
// needs a signed-in user: an agent holding the API key must not be able to
// start an unsandboxed session to escape its own sandbox. Leaving the field
// unset follows the default and needs nothing.
func (sm *SessionManager) SandboxOptOutAction(opts *SessionOptions) string {
	if opts == nil || opts.Sandboxed == nil || *opts.Sandboxed {
		return ""
	}
	if !sm.SandboxPreferences().DefaultEnabled {
		return ""
	}
	return SandboxOptOutAction
}

// protectedPaths lists Eggify's private state and agent credential files.
// dirs and files may be neither read nor edited (dirs recursively);
// editDirs may be read but not edited, so an agent can still read Eggify's logs
// in ~/.claude/eggify/logs while debugging but cannot change Eggify's own state.
type protectedPaths struct {
	dirs     []string
	files    []string
	editDirs []string
}

// eggifyProtectedPaths returns the paths no agent tool should read or write.
// When the session works inside one of the protected directories (a user
// working on Eggify's own config), that directory is not denied wholesale —
// only the secret-bearing files in it are — so normal project work still
// functions.
func eggifyProtectedPaths(home, workingDir string) protectedPaths {
	var p protectedPaths
	if home == "" {
		return p
	}
	eggifyDir := filepath.Join(home, ".claude", "eggify")
	analyticsDir := filepath.Join(home, ".claude", "analytics")

	// The secret-bearing files are never readable. The rest of ~/.claude/eggify
	// (logs, config) stays readable, so agents can follow the docs' advice to
	// read ~/.claude/eggify/logs, but nothing in it may be edited unless the
	// session is working on Eggify's own config there.
	p.files = append(p.files,
		filepath.Join(eggifyDir, ".secret"),
		filepath.Join(eggifyDir, "eggify.db"),
		filepath.Join(eggifyDir, "eggify.db-wal"),
		filepath.Join(eggifyDir, "eggify.db-shm"),
		filepath.Join(eggifyDir, "mfa_encryption.key"),
		// Encrypted remote access (ngrok) settings: authtoken, domain, autostart.
		filepath.Join(eggifyDir, "remote_access.json"),
		// Encrypted paired machines (desktop app): session tokens for other Macs.
		filepath.Join(eggifyDir, "desktop-machines.json"),
		// Encrypts connector credentials and n0 tokens (secretstore.LegacyPath).
		filepath.Join(home, ".claude", "connector_encryption.key"),
	)
	p.dirs = append(p.dirs, filepath.Join(eggifyDir, "certs"), filepath.Join(eggifyDir, "secrets"))
	if workingDir == "" || !pathWithin(cleanAbs(workingDir), cleanAbs(eggifyDir)) {
		p.editDirs = append(p.editDirs, eggifyDir)
	}
	p.files = append(p.files,
		filepath.Join(analyticsDir, ".secret"),
		filepath.Join(home, ".codex", "auth.json"),
		filepath.Join(home, ".claude", ".credentials.json"),
	)
	dirs, files := desktopWebViewStores(runtime.GOOS, home)
	p.dirs = append(p.dirs, dirs...)
	p.files = append(p.files, files...)
	return p
}

// ProtectedPathsForTools returns where eggify-tools' own file tools (Read,
// Write, Edit, Glob, Grep of the direct loop) must not go: noRead may be
// neither read nor written, noWrite may not be written. Each entry comes with
// its symlink-resolved spelling. It is the deny list of every Claude session
// plus the credential folders of the user's other tools; a folder the session
// works inside is not denied wholesale (as in eggifyProtectedPaths), only the
// secret-bearing files in it.
func ProtectedPathsForTools(home, workingDir string) (noRead, noWrite []string) {
	if home == "" {
		return nil, nil
	}
	p := eggifyProtectedPaths(home, workingDir)
	add := func(dst *[]string, paths ...string) {
		for _, x := range paths {
			*dst = append(*dst, withResolved(x)...)
		}
	}
	add(&noRead, p.files...)
	add(&noRead, p.dirs...)
	add(&noWrite, p.files...)
	add(&noWrite, p.dirs...)
	add(&noWrite, p.editDirs...)
	inside := func(dir string) bool {
		return workingDir != "" && pathWithin(cleanAbs(workingDir), cleanAbs(dir))
	}
	for _, rel := range []string{".ssh", ".gnupg", ".aws", ".codex", filepath.Join("Library", "Keychains")} {
		d := filepath.Join(home, rel)
		if !inside(d) {
			add(&noRead, d)
			add(&noWrite, d)
		}
	}
	// Claude Code's own configuration (settings, hooks, skills, commands,
	// plugins) and the user's config folder: readable, never writable.
	for _, d := range []string{filepath.Join(home, ".claude"), filepath.Join(home, ".config")} {
		if !inside(d) {
			add(&noWrite, d)
		}
	}
	return noRead, noWrite
}

// desktopWebViewIDs are the names WebKit files the desktop app's website data
// under: the Wails bundle id (cmd/eggify-desktop appID, Info.plist), the Tauri
// build's identifier (src-tauri/tauri.conf.json), and the executable's name,
// which is what an unbundled dev build (`go run`, a bare binary) is filed under.
var desktopWebViewIDs = []string{"com.eggify.desktop.wails", "com.eggify.desktop", "eggify-desktop"}

// desktopWebViewStores returns where WebKit keeps the desktop app's website
// data on macOS. The window loads the dashboard in a WKWebView, and WebKit
// writes the signed-in user's session_token cookie, as sent (the database only
// holds its hash), to ~/Library/HTTPStorages/<id>.binarycookies
// (~/Library/Cookies on older systems). Whoever reads that file is the user,
// not "a local process" like the API key. The website data and cache folders
// next to it (localStorage, IndexedDB, cached API responses) are denied too.
// Like every path here: the deny rules stop the file tools and the sandbox
// stops sandboxed commands, but Bash outside the sandbox can still read them.
// Other systems keep nothing there, so they get no entries.
func desktopWebViewStores(goos, home string) (dirs, files []string) {
	if goos != "darwin" || home == "" {
		return nil, nil
	}
	lib := filepath.Join(home, "Library")
	for _, id := range desktopWebViewIDs {
		files = append(files,
			filepath.Join(lib, "HTTPStorages", id+".binarycookies"),
			filepath.Join(lib, "Cookies", id+".binarycookies"),
		)
		dirs = append(dirs,
			filepath.Join(lib, "HTTPStorages", id),
			filepath.Join(lib, "WebKit", id),
			filepath.Join(lib, "Caches", id),
		)
	}
	return dirs, files
}

// pathWithin reports whether path equals dir or lies inside it.
func pathWithin(path, dir string) bool {
	if path == dir {
		return true
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

func cleanAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return filepath.Clean(p)
}

// withResolved returns p plus its symlink-resolved form when that differs, so
// rules hold whichever spelling a tool uses.
func withResolved(p string) []string {
	out := []string{p}
	if r, err := filepath.EvalSymlinks(p); err == nil && r != p {
		out = append(out, r)
	}
	return out
}

// permissionPath renders an absolute path for a permission rule ("//abs").
func permissionPath(p string) string {
	return "/" + filepath.ToSlash(p)
}

// protectedDenyRules builds Read/Edit deny rules for the protected paths.
// Read rules cover every file-reading tool and Edit rules every file-editing
// tool (verified against Claude Code 2.1.280).
func protectedDenyRules(p protectedPaths) []string {
	var rules []string
	for _, d := range p.dirs {
		for _, v := range withResolved(d) {
			rules = append(rules, fmt.Sprintf("Read(%s/**)", permissionPath(v)), fmt.Sprintf("Edit(%s/**)", permissionPath(v)))
		}
	}
	for _, f := range p.files {
		for _, v := range withResolved(f) {
			rules = append(rules, fmt.Sprintf("Read(%s)", permissionPath(v)), fmt.Sprintf("Edit(%s)", permissionPath(v)))
		}
	}
	for _, d := range p.editDirs {
		for _, v := range withResolved(d) {
			rules = append(rules, fmt.Sprintf("Edit(%s/**)", permissionPath(v)))
		}
	}
	return rules
}

// sandboxWriteProtected lists files that, if written by a sandboxed command,
// would run code outside the sandbox later (MCP servers, git hooks, Claude
// settings hooks). Relative to the working directory, plus user-level config.
func sandboxWriteProtected(home, workingDir string) []string {
	var out []string
	if workingDir != "" {
		wd := cleanAbs(workingDir)
		out = append(out,
			filepath.Join(wd, ".mcp.json"),
			filepath.Join(wd, ".git", "hooks"),
			filepath.Join(wd, ".git", "config"),
			// Ways to point git at a config the agent wrote: the server's own
			// git (status, diff) runs outside the sandbox. internal/gitexec
			// neutralises the known command keys; these keep a planted
			// filter definition (+ info/attributes) from being found.
			filepath.Join(wd, ".git", "commondir"),
			filepath.Join(wd, ".git", "config.worktree"),
			filepath.Join(wd, ".git", "info"),
			filepath.Join(wd, ".claude", "settings.json"),
			filepath.Join(wd, ".claude", "settings.local.json"),
		)
		// In a linked worktree .git is a file naming the gitdir.
		if fi, err := os.Lstat(filepath.Join(wd, ".git")); err == nil && !fi.IsDir() {
			out = append(out, filepath.Join(wd, ".git"))
		}
	}
	if home != "" {
		out = append(out,
			filepath.Join(home, ".claude", "settings.json"),
			filepath.Join(home, ".claude", "settings.local.json"),
			filepath.Join(home, ".claude.json"),
			// Other sessions' env files (sourced in their commands) and the CLI
			// launchers: protected whatever folder this session works in, which
			// matters when it works inside ~/.claude/eggify (no editDirs then).
			sandboxEnvDir(home),
			filepath.Join(home, ".claude", "eggify", "launchers"),
		)
	}
	return out
}

// claudeSessionSettings is the JSON passed to the CLI with --settings.
// Flag settings outrank project and local settings for scalar keys; arrays
// (allow/deny lists, sandbox paths and domains) are merged across sources.
type claudeSessionSettings struct {
	Permissions claudeSettingsPermissions `json:"permissions"`
	Sandbox     *claudeSandboxSettings    `json:"sandbox,omitempty"`
}

type claudeSettingsPermissions struct {
	Deny []string `json:"deny,omitempty"`
	Ask  []string `json:"ask,omitempty"`
}

// claudeSandboxSettings mirrors the `sandbox` settings schema of Claude Code
// 2.1.280 (keys taken from the CLI's own settings schema).
type claudeSandboxSettings struct {
	Enabled bool `json:"enabled"`
	// EnableWeakerNetworkIsolation lets Go programs verify TLS certificates
	// through trustd (gh, and anything built in Go).
	EnableWeakerNetworkIsolation bool                      `json:"enableWeakerNetworkIsolation,omitempty"`
	FailIfUnavailable            bool                      `json:"failIfUnavailable"`
	AutoAllowBashIfSandboxed     bool                      `json:"autoAllowBashIfSandboxed"`
	AllowUnsandboxedCommands     bool                      `json:"allowUnsandboxedCommands"`
	Network                      claudeSandboxNetwork      `json:"network"`
	Filesystem                   claudeSandboxFilesystem   `json:"filesystem"`
	Credentials                  *claudeSandboxCredentials `json:"credentials,omitempty"`
}

type claudeSandboxNetwork struct {
	AllowedDomains  []string `json:"allowedDomains"`
	StrictAllowlist bool     `json:"strictAllowlist"`
	// AllowUnixSockets (the ssh-agent socket and its folder) and
	// AllowLocalBinding (dev servers) are only set by grants.
	AllowUnixSockets  []string `json:"allowUnixSockets,omitempty"`
	AllowLocalBinding bool     `json:"allowLocalBinding,omitempty"`
}

type claudeSandboxFilesystem struct {
	DenyRead   []string `json:"denyRead,omitempty"`
	DenyWrite  []string `json:"denyWrite,omitempty"`
	AllowRead  []string `json:"allowRead,omitempty"`
	AllowWrite []string `json:"allowWrite,omitempty"`
}

type claudeSandboxCredentials struct {
	EnvVars []claudeSandboxEnvVar `json:"envVars,omitempty"`
}

type claudeSandboxEnvVar struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}

// eggifyToolsCoreTools are eggify-tools' Bash/Read/Write/... for direct sessions.
var eggifyToolsCoreTools = []string{"Bash", "Read", "Write", "Edit", "Glob", "Grep"}

// buildClaudeSessionSettings returns the --settings payload for a session
// without grants; the cache folder is the one of its working directory.
func buildClaudeSessionSettings(home, workingDir string, sandboxed bool, allowedDomains []string) claudeSessionSettings {
	return buildClaudeSessionSettingsWith(home, workingDir, sandboxed, allowedDomains, nil)
}

// buildClaudeSessionSettingsWith is buildClaudeSessionSettings with the cache
// folder and grants a session was given (nil: no grants).
func buildClaudeSessionSettingsWith(home, workingDir string, sandboxed bool, allowedDomains []string, extras *sandboxExtras) claudeSessionSettings {
	if extras == nil {
		extras = &sandboxExtras{CacheDir: sandboxCacheDir(home, workingDir)}
	}
	paths := eggifyProtectedPaths(home, workingDir)
	settings := claudeSessionSettings{
		Permissions: claudeSettingsPermissions{Deny: protectedDenyRules(paths)},
	}
	// eggify-tools' own coding tools are for direct sessions only (it serves
	// them only there, capability.EnvCoreTools); deny them here as well, since
	// they would run outside the CLI's sandbox and deny rules. Deny rules hold
	// in YOLO mode.
	for _, t := range eggifyToolsCoreTools {
		settings.Permissions.Deny = append(settings.Permissions.Deny, "mcp__eggify-tools__"+t)
	}
	if !sandboxed {
		return settings
	}
	// GPU file sync and remote commands run outside the sandbox too.
	for _, t := range []string{"gpu_run", "gpu_push", "gpu_pull"} {
		settings.Permissions.Deny = append(settings.Permissions.Deny, "mcp__eggify-tools__"+t)
	}

	// Agent tools must not rewrite config that runs code outside the sandbox.
	for _, p := range sandboxWriteProtected(home, workingDir) {
		if strings.HasSuffix(p, string(filepath.Separator)+"hooks") || p == sandboxEnvDir(home) || p == filepath.Join(home, ".claude", "eggify", "launchers") {
			settings.Permissions.Deny = append(settings.Permissions.Deny, fmt.Sprintf("Edit(%s/**)", permissionPath(p)))
			continue
		}
		settings.Permissions.Deny = append(settings.Permissions.Deny, fmt.Sprintf("Edit(%s)", permissionPath(p)))
	}
	// Web tools run in the CLI process, outside the sandbox's network proxy.
	settings.Permissions.Ask = []string{"WebFetch", "WebSearch"}

	// Personal credentials are hidden from sandboxed sessions (a grant can leave
	// some readable), for the file tools as well as for commands.
	personal := hiddenPersonalSecrets(grantGOOS, home, workingDir, extras.Unhide)
	settings.Permissions.Deny = append(settings.Permissions.Deny, protectedDenyRules(personal)...)

	var denyRead []string
	for _, d := range append(append([]string{}, paths.dirs...), personal.dirs...) {
		denyRead = append(denyRead, withResolved(d)...)
	}
	for _, f := range append(append([]string{}, paths.files...), personal.files...) {
		denyRead = append(denyRead, withResolved(f)...)
	}
	envVars := make([]claudeSandboxEnvVar, 0, len(sandboxDeniedEnvVars))
	for _, name := range sandboxDeniedEnvVars {
		envVars = append(envVars, claudeSandboxEnvVar{Name: name, Mode: "deny"})
	}
	domains := allowedDomains
	if domains == nil {
		domains = []string{}
	}
	settings.Sandbox = &claudeSandboxSettings{
		Enabled:                  true,
		FailIfUnavailable:        true,
		AutoAllowBashIfSandboxed: true,
		AllowUnsandboxedCommands: false,
		Network: claudeSandboxNetwork{
			AllowedDomains: domains, StrictAllowlist: true,
			AllowUnixSockets: extras.UnixSockets, AllowLocalBinding: extras.AllowLocalBinding,
		},
		EnableWeakerNetworkIsolation: extras.WeakerNetworkIsolation,
		Filesystem: claudeSandboxFilesystem{
			DenyRead:   denyRead,
			DenyWrite:  append(sandboxWriteProtected(home, workingDir), paths.editDirs...),
			AllowRead:  extras.AllowRead,
			AllowWrite: sandboxAllowWrite(extras),
		},
		Credentials: &claudeSandboxCredentials{EnvVars: envVars},
	}
	return settings
}

// sandboxAllowWrite is the session's cache folder plus the folders a write
// grant opens, each with its symlink-resolved spelling.
func sandboxAllowWrite(x *sandboxExtras) []string {
	var out []string
	if x.CacheDir != "" {
		out = append(out, withResolved(x.CacheDir)...)
	}
	for _, p := range x.AllowWrite {
		out = append(out, withResolved(p)...)
	}
	return out
}

// sessionWorkingDir returns the session's working directory, or "".
func sessionWorkingDir(opts *SessionOptions) string {
	if opts == nil || opts.WorkingDirectory == nil {
		return ""
	}
	return strings.TrimSpace(*opts.WorkingDirectory)
}

// sandboxWideningKeys are project-settings keys that would widen a sandbox:
// they merge into (or override) what Eggify passes with --settings. Verified
// against Claude Code 2.1.280: a repo's sandbox.filesystem.allowRead re-opens
// ~/.claude/eggify and its sandbox.network.allowedDomains adds egress.
var sandboxWideningKeys = []string{"allowRead", "allowWrite", "allowedDomains", "allowUnixSockets",
	"allowAllUnixSockets", "allowLocalBinding", "allowMachLookup", "enableWeakerNetworkIsolation",
	"enableWeakerNestedSandbox", "httpProxyPort", "socksProxyPort", "disabled", "ignoreViolations",
	"excludedCommands"}

// projectSandboxOverrides lists sandbox keys configured in the project's
// .claude/settings.json or .claude/settings.local.json. A sandboxed session
// refuses to start while any are present, because they would silently widen
// Eggify's sandbox (arrays merge across settings sources).
func projectSandboxOverrides(workingDir string) []string {
	if workingDir == "" {
		return nil
	}
	var found []string
	for _, name := range []string{"settings.json", "settings.local.json"} {
		path := filepath.Join(workingDir, ".claude", name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal(data, &parsed); err != nil {
			continue
		}
		raw, ok := parsed["sandbox"]
		if !ok {
			continue
		}
		var keys []string
		collectJSONKeys(raw, &keys)
		for _, k := range keys {
			for _, w := range sandboxWideningKeys {
				if k == w {
					found = append(found, fmt.Sprintf(".claude/%s: sandbox …%s", name, k))
				}
			}
		}
	}
	return found
}

func collectJSONKeys(raw json.RawMessage, out *[]string) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return
	}
	for k, v := range obj {
		*out = append(*out, k)
		collectJSONKeys(v, out)
	}
}

// ErrSandboxUnsupported is returned when a sandboxed session would run on the
// direct (OpenAI-compatible) path, whose tools cannot be sandboxed.
var ErrSandboxUnsupported = fmt.Errorf("sandboxed mode is only available for Claude and Codex sessions: this provider runs its tools in Eggify's own process, which has no sandbox — start an unsandboxed session to use it")

// applyClaudeSessionSettings passes the session's settings (deny rules, and the
// sandbox for sandboxed sessions) to the CLI. It must be called on every path
// that builds options before claude.NewClient(), next to applyRTKHook.
func (sm *SessionManager) applyClaudeSessionSettings(opts *types.ClaudeAgentOptions, options *SessionOptions) (*types.ClaudeAgentOptions, error) {
	return sm.applyClaudeSessionSettingsFor(uuid.Nil, nil, opts, options)
}

// applyClaudeSessionSettingsFor is applyClaudeSessionSettings for the session
// sessionID; notice (optional) shows a line in its chat.
func (sm *SessionManager) applyClaudeSessionSettingsFor(sessionID uuid.UUID, notice func(string), opts *types.ClaudeAgentOptions, options *SessionOptions) (*types.ClaudeAgentOptions, error) {
	if opts == nil {
		return opts, nil
	}
	home, _ := os.UserHomeDir()
	wd := sessionWorkingDir(options)
	sandboxed := isSandboxed(options)
	var domains []string
	if sandboxed {
		// Only a folder whose settings files are loaded can widen the sandbox;
		// untrusted folders run with --setting-sources "" (applyWorkspaceTrust,
		// which callers apply first).
		if overrides := projectSandboxOverrides(wd); len(overrides) > 0 && loadsProjectSettings(opts) {
			return opts, fmt.Errorf("this project's Claude settings change the sandbox (%s), which would widen a sandboxed session — remove those keys or start an unsandboxed session", strings.Join(overrides, ", "))
		}
		domains = sm.SandboxPreferences().AllowedDomains
	}
	var extras *sandboxExtras
	if sandboxed {
		sid := sessionID.String()
		if sessionID == uuid.Nil {
			sid = "adhoc"
		}
		extras = resolveSandboxExtrasFor(sid, home, sm.sandboxProjectRoot(options), options, opts.Env)
		for _, n := range extras.Notices {
			if notice != nil {
				notice(n)
			}
		}
		// Grant variables (GH_TOKEN, GIT_SSH_COMMAND, git config) go into the CLI's
		// environment; they survive agentenv's allowlist and the launcher's
		// `env -u` (ScrubClaudeOptions keeps names in opts.Env).
		for k, v := range extras.Env {
			opts.WithEnvVar(k, v)
		}
		// The cache variables must reach sandboxed commands only, not the hooks and
		// MCP servers the CLI starts outside the sandbox (they would resolve packages
		// from a folder the agent can write): CLAUDE_ENV_FILE is sourced before each
		// sandboxed Bash command. Skipped when the session works inside Eggify's own
		// folder, where the sandbox does not write-protect the file.
		if len(extras.CacheEnv) > 0 && !(wd != "" && pathWithin(cleanAbs(wd), cleanAbs(filepath.Join(home, ".claude", "eggify")))) {
			prior := opts.Env["CLAUDE_ENV_FILE"]
			if prior == "" {
				prior = grantGetenv("CLAUDE_ENV_FILE")
			}
			if path, err := writeSandboxEnvFile(home, sid, prior, extras.CacheEnv); err != nil {
				logging.Warning("sandbox: cannot write the environment file: %v", err)
			} else {
				opts.WithEnvVar("CLAUDE_ENV_FILE", path)
			}
		}
	}
	settings := buildClaudeSessionSettingsWith(home, wd, sandboxed, domains, extras)
	if autopilotSession(options) {
		// A trusted project's permissions.allow must not approve a file write on its own:
		// ask rules send these tools to the permission callback (autopilotDecide).
		settings.Permissions.Ask = appendUnique(settings.Permissions.Ask, readOnlyDeniedTools...)
	}
	if options != nil && options.ReadOnly {
		if !sandboxed || wd == "" {
			return opts, fmt.Errorf("a read-only session needs the sandbox and a working directory")
		}
		applyReadOnlySettings(&settings, wd)
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return opts, fmt.Errorf("failed to build session settings: %w", err)
	}
	if sandboxed {
		logging.Info("🧱 Sandboxed session: Bash sandbox on, %d egress domains allowed, grants %v, %d extra paths", len(domains), options.SandboxGrants, len(options.SandboxGrantPaths))
	}
	return opts.WithSettings(string(data)), nil
}

// readOnlyDeniedTools are the CLI's file-changing tools, denied outright in a
// read-only session (deny rules hold with permission prompts off).
var readOnlyDeniedTools = []string{"Edit", "Write", "MultiEdit", "NotebookEdit"}

// applyReadOnlySettings turns a sandboxed session's settings into a read-only
// one (SessionOptions.ReadOnly): the file-changing tools are denied and the
// working directory is write-denied for sandboxed commands, so the agent can
// read, search and run checks but not change the folder. Commands can still
// write to their temporary directory.
func applyReadOnlySettings(settings *claudeSessionSettings, workingDir string) {
	settings.Permissions.Deny = append(settings.Permissions.Deny, readOnlyDeniedTools...)
	if settings.Sandbox != nil {
		settings.Sandbox.Filesystem.DenyWrite = append(settings.Sandbox.Filesystem.DenyWrite, withResolved(workingDir)...)
	}
}

// readOnlySession reports whether a session may not change its workspace:
// SessionOptions.ReadOnly, or the "read-only" permission mode Codex sessions
// have always mapped to its read-only profile.
func readOnlySession(opts *SessionOptions) bool {
	if opts == nil {
		return false
	}
	return opts.ReadOnly || strings.EqualFold(strings.TrimSpace(codexStr(opts.PermissionMode)), "read-only")
}

// loadsProjectSettings reports whether the CLI will read the folder's
// .claude/settings*.json: true when no --setting-sources flag is set (the CLI
// then loads every source) or when it names project or local.
func loadsProjectSettings(opts *types.ClaudeAgentOptions) bool {
	if len(opts.SettingSources) == 0 {
		return true
	}
	for _, s := range opts.SettingSources {
		if s == types.SettingSourceProject || s == types.SettingSourceLocal {
			return true
		}
	}
	return false
}

// sandboxWebToolDecision decides a WebFetch/WebSearch permission request in a
// sandboxed session. handled=false means the tool is not a web tool (or the
// session is not sandboxed) and normal handling applies. For web tools,
// allow=true auto-approves (WebFetch to an allowlisted host); allow=false
// means the user must be asked — always-allow rules are not consulted.
func sandboxWebToolDecision(sandboxed bool, toolName string, input map[string]interface{}, allowedDomains []string) (handled, allow bool) {
	if !sandboxed || (toolName != "WebFetch" && toolName != "WebSearch") {
		return false, false
	}
	if toolName == "WebSearch" {
		return true, false
	}
	raw, _ := input["url"].(string)
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return true, false
	}
	return true, hostAllowed(strings.ToLower(u.Hostname()), allowedDomains)
}

// codexSandboxProfile is the Codex permission profile used by sandboxed
// sessions (Codex >= 0.155 permission profiles, verified with `codex sandbox`).
const codexSandboxProfile = "eggify_sandboxed"

// CodexNetworkAction names the user-only grant, independent of filesystem access.
const CodexNetworkAction = "Enabling Codex network access"

// codexSandboxConfig returns the thread config for a sandboxed Codex session:
// a permission profile that extends the built-in workspace profile, denies
// Eggify's secrets and keeps code-running config read-only. Network is off
// unless the user explicitly authorized this session at creation. Enabled
// networking is unrestricted: domain rules only work with Codex's network
// proxy, which Eggify does not configure for direct SSH/Git/package commands.
// The profile replaces the
// `sandbox` mode parameter (Codex rejects both together), so a sandboxed
// session never gets danger-full-access, even in YOLO mode.
func codexSandboxConfig(home, workingDir string, opts *SessionOptions) map[string]any {
	paths := eggifyProtectedPaths(home, workingDir)
	fs := map[string]any{}
	for _, p := range sandboxWriteProtected(home, workingDir) {
		fs[p] = "read"
	}
	for _, d := range paths.editDirs {
		for _, v := range withResolved(d) {
			fs[v] = "read"
		}
	}
	for _, d := range paths.dirs {
		for _, v := range withResolved(d) {
			fs[v] = "deny"
		}
	}
	for _, f := range paths.files {
		for _, v := range withResolved(f) {
			fs[v] = "deny"
		}
	}
	parent := ":workspace"
	if readOnlySession(opts) {
		parent = ":read-only"
	}
	// The app-server needs provider credentials, but command subprocesses do
	// not. Dotted keys merge into the user's own shell_environment_policy
	// (inherit, include_only, exclude, set) instead of replacing it; `set` is
	// applied last, so an empty value wins over anything inherited.
	cfg := map[string]any{"shell_environment_policy.ignore_default_excludes": false}
	for _, name := range sandboxDeniedEnvVars {
		cfg["shell_environment_policy.set."+name] = ""
	}
	cfg["default_permissions"] = codexSandboxProfile
	cfg["permissions."+codexSandboxProfile] = map[string]any{
		"extends":    parent,
		"filesystem": fs,
		"network":    map[string]any{"enabled": opts != nil && isSandboxed(opts) && opts.CodexNetworkAccess},
	}
	return cfg
}

// failPrompt marks a prompt that could not start as failed, so the session
// shows the reason instead of staying "processing".
func (sm *SessionManager) failPrompt(session *AgentSession, err error) error {
	sm.mu.Lock()
	msg := err.Error()
	session.ErrorMessage = &msg
	session.Status = SessionStatusError
	sm.mu.Unlock()
	logging.Warning("Prompt for session %s refused: %v", session.ID, err)
	return err
}

// hostAllowed matches a host against allowlist entries ("example.com" exactly,
// "*.example.com" any subdomain).
func hostAllowed(host string, allowed []string) bool {
	for _, d := range allowed {
		if strings.HasPrefix(d, "*.") {
			if strings.HasSuffix(host, d[1:]) {
				return true
			}
			continue
		}
		if host == d {
			return true
		}
	}
	return false
}

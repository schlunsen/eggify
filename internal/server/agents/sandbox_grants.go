// Package agents - sandbox_grants.go makes sandboxed Claude sessions usable
// every day (docs/sandbox-grants.md): a per-project cache folder with the build
// tools' cache variables, personal credentials hidden, and the grants a signed-in
// admin can give a session at creation (ssh agent, GitHub, cloud credentials,
// npm login, local servers, extra folders).
//
// Everything here only shapes the settings and environment the Claude CLI
// starts with; the CLI's sandbox enforces them. Codex and direct sessions have
// no grants yet.
package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/agentenv"
	"github.com/schlunsen/eggify/internal/logging"
)

// SandboxGrantAction is the privileged action of giving a sandboxed session
// access beyond its folder: an agent holding the API key must not be able to
// start a session that can use the user's keys or accounts.
const SandboxGrantAction = "Giving a sandboxed session access to your files or accounts"

// maxSandboxGrantPaths caps the extra folders and files of one session.
const maxSandboxGrantPaths = 10

// Grant ids (SessionOptions.SandboxGrants).
const (
	GrantSSH       = "ssh"
	GrantGitHub    = "github"
	GrantCloud     = "cloud"
	GrantNPM       = "npm"
	GrantLocalhost = "localhost"
)

// SandboxGrantPath is an extra folder or file a sandboxed session may read,
// and optionally write.
type SandboxGrantPath struct {
	Path  string `json:"path"`
	Write bool   `json:"write,omitempty"`
}

// SandboxGrantInfo describes one grant for the Create Session dialog: what it
// opens and, in plain words, what the agent can then do.
type SandboxGrantInfo struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Risk        string `json:"risk"`
}

// SandboxGrantStatus is a grant plus whether this machine can give it now.
type SandboxGrantStatus struct {
	SandboxGrantInfo
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// sandboxGrantTable is the one place the grants' wording lives (docs/sandbox-grants.md).
var sandboxGrantTable = []SandboxGrantInfo{
	{GrantSSH, "SSH keys", "Use the keys loaded in your ssh-agent for git and ssh to allowed hosts.",
		"The session can authenticate and sign with your keys on the hosts it can reach (such as GitHub), but cannot read a private key file."},
	{GrantGitHub, "GitHub", "Sign in to GitHub with your gh login (token and git credentials).",
		"The agent can act as you on GitHub, and its commands can read the token. TLS trust-store lookups are enabled."},
	{GrantCloud, "Cloud credentials", "Make ~/.aws, ~/.azure, ~/.config/gcloud and ~/.kube readable.",
		"The agent can read and use those credentials. TLS trust-store lookups are enabled."},
	{GrantNPM, "npm login", "Make ~/.npmrc readable.",
		"The agent can publish packages as you."},
	{GrantLocalhost, "Local servers", "Start dev servers and reach services on localhost.",
		"The agent can start and reach local servers, and it also reaches every other service on this Mac's localhost, Eggify included."},
}

func knownSandboxGrant(id string) bool {
	for _, g := range sandboxGrantTable {
		if g.ID == id {
			return true
		}
	}
	return false
}

// Seams for the parts that touch the machine; tests replace them.
var (
	grantGetenv     = os.Getenv
	grantExecutable = os.Executable
	grantGOOS       = runtime.GOOS
	grantGHToken    = ghAuthToken
	grantGHPath     = findGH
)

// SandboxGrantAvailability lists every grant with whether it can be given on
// this machine now. It never returns a secret.
func SandboxGrantAvailability() []SandboxGrantStatus {
	out := make([]SandboxGrantStatus, 0, len(sandboxGrantTable))
	for _, g := range sandboxGrantTable {
		st := SandboxGrantStatus{SandboxGrantInfo: g, Available: true}
		if reason := grantUnavailableReason(g.ID); reason != "" {
			st.Available, st.Reason = false, reason
		}
		out = append(out, st)
	}
	return out
}

// grantUnavailableReason is "" when the grant can be given now.
func grantUnavailableReason(id string) string {
	switch id {
	case GrantSSH:
		sock := strings.TrimSpace(grantGetenv("SSH_AUTH_SOCK"))
		if sock == "" {
			return "No ssh-agent is running"
		}
		if fi, err := os.Stat(sock); err != nil || fi.Mode()&os.ModeSocket == 0 {
			return "The ssh-agent socket does not exist"
		}
		if _, err := sshProxyCommandPath(); err != nil {
			return "Eggify's own path can't be used as an ssh proxy command"
		}
		if sshGrantAddsSocketFolder {
			if reason := sshSocketFolderProblem(userHome(), sock); reason != "" {
				return reason
			}
		}
	case GrantGitHub:
		return githubAvailability()
	}
	return ""
}

// sshGrantAddsSocketFolder says whether the socket's folder is opened next to
// the socket itself. The first live test of the grant needed the folder (the raw
// SSH_AUTH_SOCK value alone was refused), but it was only tried with the
// unresolved spelling (/var/run/… vs the resolved /private/var/run/…); the socket
// is now always opened in both spellings, so this can be flipped to false to
// test whether that suffices. Whether a folder entry is recursive is unknown and
// is treated as recursive: with the folder opened, the grant is only available
// when the folder is private to the agent (sshSocketFolderProblem).
const sshGrantAddsSocketFolder = true

func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}

var tempFolderRe = regexp.MustCompile(`^(/private)?/var/folders/[^/]+/[^/]+/T$`)

// unsafeSocketDir reports whether a folder is shared with other things (so
// opening it for the ssh-agent socket would open their sockets too): the home
// folder, the temporary and run folders, and anything holding or inside a
// hidden credential folder (~/.ssh would open ControlMaster sockets,
// ~/.gnupg gpg-agent's own).
func unsafeSocketDir(home, dir string) bool {
	dir = filepath.Clean(dir)
	shared := []string{"/", "/tmp", "/private", "/private/tmp", "/var", "/var/run", "/private/var", "/private/var/run", "/run", "/var/tmp", "/private/var/tmp"}
	if home != "" {
		shared = append(shared, home, cleanAbs(home))
	}
	for _, d := range shared {
		if dir == filepath.Clean(d) {
			return true
		}
	}
	if tempFolderRe.MatchString(dir) {
		return true
	}
	dirs, files := personalSecretPaths(grantGOOS, home)
	for _, d := range append(dirs, files...) {
		for _, v := range withResolved(d) {
			if overlaps(dir, filepath.Clean(v)) {
				return true
			}
		}
	}
	return false
}

// sshSocketFolderProblem is "" when the ssh-agent socket's folder (in both
// spellings) can be opened safely: not shared, and holding no other socket.
func sshSocketFolderProblem(home, sock string) string {
	for _, v := range withResolved(sock) {
		dir := filepath.Dir(v)
		if unsafeSocketDir(home, dir) {
			return "The ssh-agent socket is in a shared folder"
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "The ssh-agent socket's folder can't be read"
		}
		for _, e := range entries {
			if e.Name() != filepath.Base(v) && e.Type()&os.ModeSocket != 0 {
				return "The ssh-agent socket's folder holds other sockets"
			}
		}
	}
	return ""
}

// ghAvailCache keeps the answer of "is gh signed in" for a short while, since
// the dialog asks every time it opens and `gh auth token` is a process start.
var ghAvailCache struct {
	mu     sync.Mutex
	at     time.Time
	reason string
}

func githubAvailability() string {
	ghAvailCache.mu.Lock()
	defer ghAvailCache.mu.Unlock()
	if !ghAvailCache.at.IsZero() && time.Since(ghAvailCache.at) < 30*time.Second {
		return ghAvailCache.reason
	}
	reason := ""
	if grantGHPath() == "" {
		reason = "gh is not installed"
	} else if tok, err := grantGHToken(); err != nil || tok == "" {
		reason = "gh is not signed in"
	}
	ghAvailCache.at, ghAvailCache.reason = time.Now(), reason
	return reason
}

func findGH() string {
	if p, err := exec.LookPath("gh"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh", "/usr/bin/gh", filepath.Join(home, ".local", "bin", "gh")} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// ghAuthToken runs `gh auth token` outside any sandbox, with an agent-style
// clean environment (the server's own secrets stay behind), for at most 5 s.
// The token is returned to the caller only: never log it.
func ghAuthToken() (string, error) {
	gh := grantGHPath()
	if gh == "" {
		return "", fmt.Errorf("gh is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, "auth", "token")
	cmd.Env = agentenv.Child(agentenv.Generic, "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh auth token failed")
	}
	return strings.TrimSpace(out.String()), nil
}

// sshProxyCommandPath returns this executable's path when it can be written
// into a ProxyCommand: no quote, percent sign or control character.
func sshProxyCommandPath() (string, error) {
	exe, err := grantExecutable()
	if err != nil || exe == "" {
		return "", fmt.Errorf("cannot find the eggify executable")
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if !filepath.IsAbs(exe) {
		return "", fmt.Errorf("executable path is not absolute")
	}
	for _, r := range exe {
		if r < 0x20 || r == 0x7f || r == '\'' || r == '%' {
			return "", fmt.Errorf("executable path has a character that cannot be quoted")
		}
	}
	return exe, nil
}

// gitSSHCommand is the GIT_SSH_COMMAND of the ssh grant: ssh through
// `eggify sandbox-connect`, which tunnels via the sandbox's HTTP proxy. The
// ProxyCommand is single-quoted for git's shell and its own words escaped for
// the shell ssh runs it with.
func gitSSHCommand(exe string) string {
	var esc strings.Builder
	for _, r := range exe {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+@:,=", r)) {
			esc.WriteByte('\\')
		}
		esc.WriteRune(r)
	}
	return "ssh -o ProxyCommand='" + esc.String() + " sandbox-connect %h %p'"
}

// ---- Cache folder and build tool variables (every sandboxed session) ----

// sandboxCacheRoot is the parent of every project's cache folder.
func sandboxCacheRoot(home string) string {
	return filepath.Join(home, ".cache", "eggify-sandbox")
}

// sandboxCacheKey is a short stable name for a project folder; class names the
// trust class that shares it (see sandboxCacheClass).
func sandboxCacheKey(root string, class ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(append([]string{cleanAbs(root)}, class...), "|")))
	return hex.EncodeToString(sum[:])[:12]
}

// sandboxCacheClass is the one rule for who shares a cache folder. Go does not
// re-verify build-cache entries or extracted modules, so whatever a session can
// write to its cache, the sessions that use that cache later will run: a cache is
// shared only between sessions of the same trust.
//   - a session another session started (worker, loop): its own cache per
//     spawning session and mode, never the project's;
//   - a read-only session: a cache no writable session uses;
//   - a session with grants (credentials in its environment): a cache sessions
//     without grants cannot write.
//
// Plain user sessions of a project share one.
func sandboxCacheClass(o *SessionOptions) []string {
	if o == nil {
		return nil
	}
	if o.SpawnedBySessionID != nil {
		mode := "build"
		if o.ReadOnly {
			mode = "ro"
		}
		class := []string{"spawned", o.SpawnedBySessionID.String(), mode}
		if hasSandboxGrants(o) {
			// A user starting a granted session from "Edit first" on a loop card.
			class = append(class, "granted")
		}
		return class
	}
	var class []string
	if o.ReadOnly {
		class = append(class, "ro")
	}
	if hasSandboxGrants(o) {
		class = append(class, "granted")
	}
	return class
}

// sandboxCacheDirFor is the cache folder of a session of the project at root.
func sandboxCacheDirFor(home, root string, o *SessionOptions) string {
	if home == "" || strings.TrimSpace(root) == "" {
		return ""
	}
	return filepath.Join(sandboxCacheRoot(home), sandboxCacheKey(root, sandboxCacheClass(o)...))
}

// sandboxCacheDir is the writable cache of the project containing root.
func sandboxCacheDir(home, root string) string {
	if home == "" || strings.TrimSpace(root) == "" {
		return ""
	}
	return filepath.Join(sandboxCacheRoot(home), sandboxCacheKey(root))
}

// sandboxCacheVar moves one tool's cache into the session's cache folder.
// Only variables that move a cache belong here: never ones that move
// credentials or configuration (CARGO_HOME, GRADLE_USER_HOME, HOME).
type sandboxCacheVar struct {
	Tool string
	Name string
	Sub  string // folder inside the cache folder
	Note string
}

var sandboxCacheVars = []sandboxCacheVar{
	{"go", "GOCACHE", "go-build", "build cache"},
	{"go", "GOPATH", "gopath", "the checksum database cache lives in $GOPATH/pkg/sumdb"},
	{"go", "GOMODCACHE", filepath.Join("gopath", "pkg", "mod"), "an inherited GOMODCACHE would win over GOPATH and point at the read-only module cache"},
	{"npm", "npm_config_cache", "npm", "npm and npx"},
	{"uv", "UV_CACHE_DIR", "uv", ""},
	{"pip", "PIP_CACHE_DIR", "pip", ""},
	{"yarn", "YARN_CACHE_FOLDER", "yarn", ""},
	{"bun", "BUN_INSTALL_CACHE_DIR", "bun", ""},
}

// realGoModCache is the user's own module cache: $GOMODCACHE, else
// $GOPATH/pkg/mod, else ~/go/pkg/mod.
func realGoModCache(home string, getenv func(string) string) string {
	if v := strings.TrimSpace(getenv("GOMODCACHE")); v != "" {
		return v
	}
	if gp := strings.TrimSpace(getenv("GOPATH")); gp != "" {
		return filepath.Join(filepath.SplitList(gp)[0], "pkg", "mod")
	}
	return filepath.Join(home, "go", "pkg", "mod")
}

// goProxyValue lets the new module cache be fed from the user's existing one
// without network (and without the toolchain download that a sandbox would
// redirect to a host that is not allowlisted). "" when there is nothing to feed from.
func goProxyValue(home string, getenv func(string) string) string {
	dl := filepath.Join(realGoModCache(home, getenv), "cache", "download")
	if fi, err := os.Stat(dl); err != nil || !fi.IsDir() || strings.ContainsAny(dl, ",|") {
		return ""
	}
	user := strings.TrimSpace(getenv("GOPROXY"))
	if user == "" {
		user = "https://proxy.golang.org"
	}
	for _, suffix := range []string{",direct", "|direct"} {
		user = strings.TrimSuffix(user, suffix)
	}
	u := url.URL{Path: dl}
	return "file://" + u.EscapedPath() + "," + user + ",direct"
}

// sandboxCacheEnv returns the cache variables for a session. existing is the
// session's explicit environment, which wins over the server's for GOFLAGS.
func sandboxCacheEnv(home, cacheDir string, getenv func(string) string, existing map[string]string) map[string]string {
	env := map[string]string{}
	if cacheDir == "" {
		return env
	}
	for _, v := range sandboxCacheVars {
		env[v.Name] = filepath.Join(cacheDir, v.Sub)
	}
	flags := existing["GOFLAGS"]
	if flags == "" {
		flags = getenv("GOFLAGS")
	}
	if !strings.Contains(flags, "-modcacherw") {
		flags = strings.TrimSpace(flags + " -modcacherw")
	}
	env["GOFLAGS"] = flags
	if p := goProxyValue(home, getenv); p != "" {
		env["GOPROXY"] = p
	}
	return env
}

// ---- Personal secrets (every sandboxed session) ----

// personalSecret is a path under the user's home that sandboxed commands and
// the file tools cannot read.
type personalSecret struct {
	rel  []string // path below home
	dir  bool
	macs bool // only on macOS
}

var personalSecrets = []personalSecret{
	{rel: []string{".ssh"}, dir: true},
	{rel: []string{".gnupg"}, dir: true},
	{rel: []string{".aws"}, dir: true},
	{rel: []string{".azure"}, dir: true},
	{rel: []string{".config", "gcloud"}, dir: true},
	{rel: []string{".config", "gh"}, dir: true},
	{rel: []string{".kube"}, dir: true},
	{rel: []string{".docker", "config.json"}},
	{rel: []string{".netrc"}},
	{rel: []string{".npmrc"}},
	{rel: []string{".pypirc"}},
	{rel: []string{".cargo", "credentials"}},
	{rel: []string{".cargo", "credentials.toml"}},
	{rel: []string{".gem", "credentials"}},
	{rel: []string{".config", "eggify"}, dir: true},
	{rel: []string{".git-credentials"}},                      // git's credential store (plain-text tokens)
	{rel: []string{".pgpass"}},                               // PostgreSQL passwords
	{rel: []string{".my.cnf"}},                               // MySQL client config, often with a password
	{rel: []string{".appstoreconnect"}, dir: true},           // App Store Connect API keys
	{rel: []string{".config", "op"}, dir: true},              // 1Password CLI
	{rel: []string{".terraform.d", "credentials.tfrc.json"}}, // Terraform Cloud token
	{rel: []string{".vault-token"}},                          // HashiCorp Vault CLI token
	{rel: []string{".config", "doctl"}, dir: true},           // DigitalOcean CLI token
	{rel: []string{".config", "hub"}, dir: true},             // GitHub hub CLI token
	{rel: []string{"Library", "Application Support", "1Password"}, dir: true, macs: true},
	{rel: []string{"Library", "Group Containers", "2BUA8C4S2C.com.1password"}, dir: true, macs: true},
	// Shell and tool histories.
	{rel: []string{".zsh_history"}},
	{rel: []string{".zhistory"}},
	{rel: []string{".bash_history"}},
	{rel: []string{".python_history"}},
	{rel: []string{".node_repl_history"}},
	{rel: []string{".psql_history"}},
	{rel: []string{".mysql_history"}},
	{rel: []string{".sqlite_history"}},
	{rel: []string{".lesshst"}},
	{rel: []string{".local", "share", "fish", "fish_history"}},
	// Browsers.
	{rel: []string{".mozilla"}, dir: true},
	{rel: []string{".config", "google-chrome"}, dir: true},
	{rel: []string{".config", "chromium"}, dir: true},
	{rel: []string{"Library", "Keychains"}, dir: true, macs: true},
	{rel: []string{"Library", "Cookies"}, dir: true, macs: true},
	{rel: []string{"Library", "Safari"}, dir: true, macs: true},
	{rel: []string{"Library", "Mail"}, dir: true, macs: true},
	{rel: []string{"Library", "Messages"}, dir: true, macs: true},
	{rel: []string{"Library", "Application Support", "Google", "Chrome"}, dir: true, macs: true},
	{rel: []string{"Library", "Application Support", "Firefox"}, dir: true, macs: true},
	{rel: []string{"Library", "Application Support", "BraveSoftware"}, dir: true, macs: true},
	{rel: []string{"Library", "Application Support", "Microsoft Edge"}, dir: true, macs: true},
	{rel: []string{"Library", "Application Support", "Arc"}, dir: true, macs: true},
}

// personalSecretPaths returns every entry under home that exists on this
// platform, whatever the session's folder (used to refuse grants over them).
func personalSecretPaths(goos, home string) (dirs, files []string) {
	if home == "" {
		return nil, nil
	}
	for _, s := range personalSecrets {
		if s.macs && goos != "darwin" {
			continue
		}
		p := filepath.Join(append([]string{home}, s.rel...)...)
		if s.dir {
			dirs = append(dirs, p)
		} else {
			files = append(files, p)
		}
	}
	return dirs, files
}

// hiddenPersonalSecrets is the part that is hidden from this session: not an
// entry that contains the session's working directory (the same rule as
// Eggify's own folder) and not one a grant opens (unhide, absolute paths).
func hiddenPersonalSecrets(goos, home, workingDir string, unhide []string) protectedPaths {
	dirs, files := personalSecretPaths(goos, home)
	open := map[string]bool{}
	for _, u := range unhide {
		open[u] = true
	}
	keep := func(in []string) []string {
		var out []string
		for _, p := range in {
			if open[p] || (workingDir != "" && pathWithin(cleanAbs(workingDir), cleanAbs(p))) {
				continue
			}
			out = append(out, p)
		}
		return out
	}
	return protectedPaths{dirs: keep(dirs), files: keep(files)}
}

// ---- Grants ----

// sandboxExtras is everything beyond the base sandbox that a session gets:
// its cache folder and what its grants open.
type sandboxExtras struct {
	CacheDir               string
	AllowRead              []string
	AllowWrite             []string
	UnixSockets            []string
	AllowLocalBinding      bool
	WeakerNetworkIsolation bool
	Unhide                 []string // personal entries a grant leaves readable
	// CacheEnv are the build tools' cache variables. They reach sandboxed Bash
	// commands only, through CLAUDE_ENV_FILE (writeSandboxEnvFile): hooks and
	// MCP servers run outside the sandbox and must not use a folder the agent
	// can write.
	CacheEnv map[string]string
	// Env goes into the CLI's environment (GH_TOKEN, GIT_SSH_COMMAND, git config).
	Env map[string]string
	// Notices are shown in the chat (a grant left out at start).
	Notices []string
}

// grantUnhides are the personal entries each grant leaves readable.
var grantUnhides = map[string][][]string{
	GrantCloud: {{".aws"}, {".azure"}, {".config", "gcloud"}, {".kube"}},
	GrantNPM:   {{".npmrc"}},
}

// hasSandboxGrants reports whether the options carry any grant.
func hasSandboxGrants(opts *SessionOptions) bool {
	return opts != nil && (len(opts.SandboxGrants) > 0 || len(opts.SandboxGrantPaths) > 0)
}

// errGrantsNeedClaudeSandbox is the error for a grant on a session that cannot take one.
var errGrantsNeedClaudeSandbox = fmt.Errorf("sandbox grants need a sandboxed session on the Claude CLI: Codex and OpenAI-compatible sessions do not support grants yet")

// NormalizeSandboxGrants validates grant ids and paths and returns them in
// canonical form: known ids in table order without duplicates, paths with ~
// expanded and cleaned, at most maxSandboxGrantPaths. It does not check that a
// grant is available or that the session can take it.
func NormalizeSandboxGrants(home, workingDir string, ids []string, paths []SandboxGrantPath) ([]string, []SandboxGrantPath, error) {
	want := map[string]bool{}
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			continue
		}
		if !knownSandboxGrant(id) {
			return nil, nil, fmt.Errorf("unknown sandbox grant %q", id)
		}
		want[id] = true
	}
	var outIDs []string
	for _, g := range sandboxGrantTable {
		if want[g.ID] {
			outIDs = append(outIDs, g.ID)
		}
	}
	if len(paths) > maxSandboxGrantPaths {
		return nil, nil, fmt.Errorf("at most %d extra folders or files", maxSandboxGrantPaths)
	}
	type entry struct {
		resolved, shown string
		write           bool
	}
	var entries []entry
	seen := map[string]int{}
	for _, gp := range paths {
		resolved, shown, err := resolveGrantPath(home, workingDir, gp)
		if err != nil {
			return nil, nil, err
		}
		if i, dup := seen[resolved]; dup {
			entries[i].write = entries[i].write || gp.Write
			continue
		}
		seen[resolved] = len(entries)
		entries = append(entries, entry{resolved, shown, gp.Write})
	}
	// A component of the path the session itself can write (its folder, its cache,
	// another write grant) could be swapped for a link to somewhere else later.
	for i, e := range entries {
		scopes := []string{sandboxCacheRoot(home)}
		if workingDir != "" {
			scopes = append(scopes, withResolved(workingDir)...)
			scopes = append(scopes, cleanAbs(workingDir))
		}
		for j, o := range entries {
			if j != i && o.write {
				scopes = append(scopes, o.resolved)
			}
		}
		if q := writableComponent(home, e.shown, scopes); q != "" {
			return nil, nil, fmt.Errorf("grant path %q cannot be given: %s is writable by this session, so it could be swapped for a link to another folder", e.shown, q)
		}
		if q := writableComponent(home, e.resolved, scopes); q != "" {
			return nil, nil, fmt.Errorf("grant path %q cannot be given: %s is writable by this session, so it could be swapped for a link to another folder", e.shown, q)
		}
	}
	// The resolved path is what is stored, shown and re-checked at every start.
	var outPaths []SandboxGrantPath
	for _, e := range entries {
		outPaths = append(outPaths, SandboxGrantPath{Path: e.resolved, Write: e.write})
	}
	return outIDs, outPaths, nil
}

// grantWriteRefused are places a write grant is never given even inside the
// home folder: shell start-up files, and folders that run code at login.
var grantWriteRefused = [][]string{
	{".zshrc"}, {".zshenv"}, {".zprofile"}, {".zlogin"}, {".bashrc"}, {".bash_profile"}, {".profile"},
	{".gitconfig"}, {".config", "git"}, {"Library", "LaunchAgents"},
}

// writableComponent returns the first component of p below home (p itself
// included) that lies inside one of the scopes, or "".
func writableComponent(home, p string, scopes []string) string {
	home = filepath.Clean(home)
	p = filepath.Clean(p)
	for q := p; q != home && q != "/" && q != "." && pathWithin(q, home); q = filepath.Dir(q) {
		for _, s := range scopes {
			if pathWithin(q, filepath.Clean(s)) {
				return q
			}
		}
	}
	return ""
}

func overlaps(a, b string) bool { return pathWithin(a, b) || pathWithin(b, a) }

// resolveGrantPath checks one extra path and returns its symlink-resolved form
// (what the sandbox gets) and the cleaned absolute spelling to show and store.
// It refuses anything outside the home folder, the home folder itself, paths that
// do not exist, and anything that is, contains or lies inside a place Eggify or
// the personal-secrets list protects (use a grant id to open those).
func resolveGrantPath(home, workingDir string, gp SandboxGrantPath) (resolved, shown string, err error) {
	p := strings.TrimSpace(gp.Path)
	if p == "" {
		return "", "", fmt.Errorf("a grant path is empty")
	}
	if home == "" {
		return "", "", fmt.Errorf("the home folder is unknown, so a path cannot be granted")
	}
	switch {
	case p == "~":
		p = home
	case strings.HasPrefix(p, "~/"):
		p = filepath.Join(home, p[2:])
	}
	if !filepath.IsAbs(p) || strings.ContainsRune(p, 0) {
		return "", "", fmt.Errorf("grant path %q must be absolute or start with ~/", gp.Path)
	}
	shown = filepath.Clean(p)
	resolved, err = filepath.EvalSymlinks(shown)
	if err != nil {
		return "", "", fmt.Errorf("grant path %q does not exist", gp.Path)
	}
	resolved = filepath.Clean(resolved)
	realHome := cleanAbs(home)
	if resolved == realHome || !pathWithin(resolved, realHome) {
		return "", "", fmt.Errorf("grant path %q must be inside your home folder (not the home folder itself)", gp.Path)
	}
	if hit := protectedOverlap(home, workingDir, resolved, gp.Write); hit != "" {
		return "", "", fmt.Errorf("grant path %q cannot be given: it is or touches %s", gp.Path, hit)
	}
	return resolved, shown, nil
}

// protectedOverlap returns what a grant path (resolved) collides with, or "".
func protectedOverlap(home, workingDir, resolved string, write bool) string {
	check := func(label string, paths ...string) string {
		for _, p := range paths {
			for _, v := range withResolved(p) {
				if overlaps(resolved, filepath.Clean(v)) {
					return label
				}
			}
		}
		return ""
	}
	e := eggifyProtectedPaths(home, workingDir)
	if hit := check("Eggify's private files", append(append(append([]string{}, e.dirs...), e.files...), e.editDirs...)...); hit != "" {
		return hit
	}
	dirs, files := personalSecretPaths(grantGOOS, home)
	if hit := check("a hidden credential location (use a grant for it)", append(dirs, files...)...); hit != "" {
		return hit
	}
	if hit := check("the sandbox cache folders", sandboxCacheRoot(home)); hit != "" {
		return hit
	}
	if write {
		if hit := check("a file Claude Code or Eggify runs code from", sandboxWriteProtected(home, workingDir)...); hit != "" {
			return hit
		}
		var startup []string
		for _, rel := range grantWriteRefused {
			startup = append(startup, filepath.Join(append([]string{home}, rel...)...))
		}
		if hit := check("a shell start-up or login item location", startup...); hit != "" {
			return hit
		}
	}
	return ""
}

// ValidateSandboxGrantsForCreate is the creation-time check: the session must
// be sandboxed and on the Claude CLI, every grant must be available now, and the
// options are rewritten in canonical form.
func (sm *SessionManager) ValidateSandboxGrantsForCreate(options *SessionOptions) error {
	if !hasSandboxGrants(options) {
		options.SandboxGrants, options.SandboxGrantPaths = nil, nil
		return nil
	}
	if !isSandboxed(options) {
		return fmt.Errorf("sandbox grants need a sandboxed session: turn the sandbox on, or remove the grants")
	}
	probe := &AgentSession{Session: Session{ModelName: codexStr(options.Model), Options: *options}}
	if sm.resolveExecutionPath(probe) != ExecutionPathClaudeSDK {
		return errGrantsNeedClaudeSandbox
	}
	home, _ := os.UserHomeDir()
	ids, paths, err := NormalizeSandboxGrants(home, sessionWorkingDir(options), options.SandboxGrants, options.SandboxGrantPaths)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if reason := grantUnavailableReason(id); reason != "" {
			return fmt.Errorf("the %q grant is not available: %s", id, reason)
		}
	}
	options.SandboxGrants, options.SandboxGrantPaths = ids, paths
	return nil
}

// resolveSandboxExtras builds what a session gets: the cache folder (created
// 0700) with its variables, and the grants' settings and environment. A grant
// that stopped being available (the ssh-agent quit, gh signed out, a folder was
// removed) is left out with a warning; the session still starts, without it.
// existing is the session's explicit environment (only read).
func resolveSandboxExtras(home, projectRoot string, options *SessionOptions, existing map[string]string) *sandboxExtras {
	return resolveSandboxExtrasFor("adhoc", home, projectRoot, options, existing)
}

// resolveSandboxExtrasFor is resolveSandboxExtras for the session sid.
func resolveSandboxExtrasFor(sid, home, projectRoot string, options *SessionOptions, existing map[string]string) *sandboxExtras {
	wd := sessionWorkingDir(options)
	x := &sandboxExtras{Env: map[string]string{}, CacheEnv: map[string]string{}}
	if projectRoot == "" {
		projectRoot = wd
	}
	if dir := sandboxCacheDirFor(home, projectRoot, options); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			logging.Warning("sandbox: cannot create the cache folder %s: %v", dir, err)
		} else {
			x.CacheDir = dir
			x.CacheEnv = sandboxCacheEnv(home, dir, grantGetenv, existing)
		}
	}
	if !hasSandboxGrants(options) {
		return x
	}
	for _, id := range options.SandboxGrants {
		if reason := grantUnavailableReason(id); reason != "" {
			logging.Warning("sandbox: grant %q left out: %s", id, reason)
			continue
		}
		switch id {
		case GrantSSH:
			x.addSSH(home, existing)
		case GrantGitHub:
			x.addGitHub(home, sid, existing)
		case GrantCloud:
			x.WeakerNetworkIsolation = true
		case GrantLocalhost:
			x.AllowLocalBinding = true
		}
		for _, rel := range grantUnhides[id] {
			x.Unhide = append(x.Unhide, filepath.Join(append([]string{home}, rel...)...))
		}
	}
	for _, gp := range options.SandboxGrantPaths {
		resolved, _, err := resolveGrantPath(home, wd, gp)
		if err == nil && resolved != filepath.Clean(gp.Path) {
			err = fmt.Errorf("%q now leads to %q", gp.Path, resolved)
		}
		if err != nil {
			// Stored paths are resolved at creation: one that leads elsewhere now was swapped.
			logging.Warning("sandbox: grant path left out: %v", err)
			x.Notices = append(x.Notices, fmt.Sprintf("🧱 The granted path %s no longer leads where it did when the session was created, so it was left out.", gp.Path))
			continue
		}
		x.AllowRead = appendUnique(x.AllowRead, resolved)
		if gp.Write {
			x.AllowWrite = appendUnique(x.AllowWrite, resolved)
		}
	}
	return x
}

func (x *sandboxExtras) addSSH(home string, existing map[string]string) {
	sock := strings.TrimSpace(grantGetenv("SSH_AUTH_SOCK"))
	// Both spellings of the socket; its folder too while sshGrantAddsSocketFolder
	// (the raw value alone was refused in the first live test).
	for _, v := range withResolved(sock) {
		x.UnixSockets = appendUnique(x.UnixSockets, v)
		if sshGrantAddsSocketFolder {
			x.UnixSockets = appendUnique(x.UnixSockets, filepath.Dir(v))
		}
	}
	for _, f := range []string{filepath.Join(home, ".ssh", "known_hosts"), filepath.Join(home, ".ssh", "config")} {
		x.AllowRead = appendUnique(x.AllowRead, withResolved(f)...)
	}
	if exe, err := sshProxyCommandPath(); err == nil {
		if prev := strings.TrimSpace(existing["GIT_SSH_COMMAND"]); prev == "" && grantGetenv("GIT_SSH_COMMAND") != "" {
			logging.Info("sandbox: ssh grant replaces the server's GIT_SSH_COMMAND for this session")
		}
		x.Env["GIT_SSH_COMMAND"] = gitSSHCommand(exe)
	}
}

func (x *sandboxExtras) addGitHub(home, sid string, existing map[string]string) {
	tok, err := grantGHToken()
	if err != nil || tok == "" {
		logging.Warning("sandbox: github grant left out: gh is not signed in")
		return
	}
	x.WeakerNetworkIsolation = true
	x.Env["GH_TOKEN"] = tok // environment only: never settings, argv or logs
	// gh refuses to start when it cannot read its config folder. The folder is
	// empty, made by the server under ~/.claude/eggify (which the sandbox cannot
	// write) and private to this session, so nothing an agent plants there can
	// run later in a session holding the token.
	dir := filepath.Join(sandboxEnvDir(home), "gh-"+sid)
	if err := os.MkdirAll(dir, 0o700); err == nil {
		x.Env["GH_CONFIG_DIR"] = dir
	} else {
		logging.Warning("sandbox: cannot create gh's config folder: %v", err)
	}
	// git over HTTPS asks gh, merged with any git config the session already sets.
	const key, value = "credential.https://github.com.helper", "!gh auth git-credential"
	n, _ := strconv.Atoi(existing["GIT_CONFIG_COUNT"])
	for i := 0; i < n; i++ {
		if existing["GIT_CONFIG_KEY_"+strconv.Itoa(i)] == key && existing["GIT_CONFIG_VALUE_"+strconv.Itoa(i)] == value {
			return // already there (options are reused across CLI restarts)
		}
	}
	x.Env["GIT_CONFIG_KEY_"+strconv.Itoa(n)] = key
	x.Env["GIT_CONFIG_VALUE_"+strconv.Itoa(n)] = value
	x.Env["GIT_CONFIG_COUNT"] = strconv.Itoa(n + 1)
}

func appendUnique(list []string, add ...string) []string {
	for _, a := range add {
		if a == "" || a == "." {
			continue
		}
		found := false
		for _, l := range list {
			if l == a {
				found = true
				break
			}
		}
		if !found {
			list = append(list, a)
		}
	}
	return list
}

// sandboxProjectRoot is the folder the session's cache is keyed by: its
// project's folder when the working directory is inside a known project (the
// longest such folder), else the working directory.
func (sm *SessionManager) sandboxProjectRoot(options *SessionOptions) string {
	wd := sessionWorkingDir(options)
	if wd == "" || sm.repo == nil {
		return wd
	}
	cwd := cleanAbs(wd)
	best, bestLen := "", 0
	consider := func(path string) {
		if path = strings.TrimSpace(path); path == "" {
			return
		}
		if c := cleanAbs(path); pathWithin(cwd, c) && len(c) > bestLen {
			best, bestLen = path, len(c)
		}
	}
	if options.ProjectID != nil && *options.ProjectID != "" {
		if p, err := sm.repo.GetProject(*options.ProjectID); err == nil && p != nil {
			consider(p.Path)
		}
	}
	if best == "" {
		if projects, err := sm.repo.GetActiveProjects(); err == nil {
			for _, p := range projects {
				consider(p.Path)
			}
		}
	}
	if best == "" {
		return wd
	}
	return best
}

// ---- Environment file for sandboxed commands ----

// sandboxEnvDir holds, per session, the file CLAUDE_ENV_FILE names and the
// folder gh reads its config from. It is under ~/.claude/eggify: readable for
// sandboxed commands (only the secret files in it are hidden) and write-denied
// for them (and for the Edit tool), so an agent cannot change what its next
// command sources.
func sandboxEnvDir(home string) string {
	return filepath.Join(home, ".claude", "eggify", "sandbox-env")
}

func sandboxEnvFile(home, sid string) string {
	return filepath.Join(sandboxEnvDir(home), sid+".sh")
}

// sandboxShellQuote single-quotes s for sh.
func sandboxShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// writeSandboxEnvFile writes the shell file the Claude CLI sources before each
// sandboxed Bash command (CLAUDE_ENV_FILE): `export NAME='value'` lines, 0600.
// prior is an env file the server's own environment already named; it is sourced
// first instead of being dropped.
func writeSandboxEnvFile(home, sid, prior string, vars map[string]string) (string, error) {
	dir := sandboxEnvDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := sandboxEnvFile(home, sid)
	var b strings.Builder
	b.WriteString("# Eggify: build cache variables for sandboxed commands. Generated; changes are overwritten.\n")
	if prior != "" && prior != path {
		fmt.Fprintf(&b, "[ -r %s ] && . %s\n", sandboxShellQuote(prior), sandboxShellQuote(prior))
	}
	names := make([]string, 0, len(vars))
	for n := range vars {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "export %s=%s\n", n, sandboxShellQuote(vars[n]))
	}
	tmp, err := os.CreateTemp(dir, ".env-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return path, os.Rename(tmp.Name(), path)
}

// removeSandboxEnv deletes a session's env file and gh folder.
func removeSandboxEnv(sessionID uuid.UUID) {
	home := userHome()
	if home == "" || sessionID == uuid.Nil {
		return
	}
	_ = os.Remove(sandboxEnvFile(home, sessionID.String()))
	_ = os.RemoveAll(filepath.Join(sandboxEnvDir(home), "gh-"+sessionID.String()))
}

// processStart is when this server process began; nothing newer is swept.
var processStart = time.Now()

// sandboxEnvMaxAge is how old an entry must be to be swept.
const sandboxEnvMaxAge = 24 * time.Hour

// SweepSandboxEnv removes stale env files and gh folders at startup. Another
// Eggify instance (the CLI server and the desktop app) may share ~/.claude/eggify
// and have live sessions, so only entries older than a day, older than this
// process, and of no session this process holds are removed.
func SweepSandboxEnv(sm *SessionManager) {
	home := userHome()
	if home == "" {
		return
	}
	cutoff := time.Now().Add(-sandboxEnvMaxAge)
	if processStart.Before(cutoff) {
		cutoff = processStart
	}
	live := func(id string) bool {
		u, err := uuid.Parse(id)
		if err != nil || sm == nil {
			return false
		}
		sm.mu.RLock()
		defer sm.mu.RUnlock()
		_, ok := sm.sessions[u]
		return ok
	}
	sweepSandboxEnvDir(sandboxEnvDir(home), cutoff, live)
}

func sweepSandboxEnvDir(dir string, cutoff time.Time, live func(string) bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		id := strings.TrimSuffix(strings.TrimPrefix(e.Name(), "gh-"), ".sh")
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) || live(id) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

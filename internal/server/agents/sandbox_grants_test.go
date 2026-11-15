package agents

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/stretchr/testify/require"
)

const testGHToken = "gho_TESTTOKENVALUE0123456789abcdef"

// grantTestEnv stands in for the machine: the server's environment, gh and the
// eggify executable, restored at the end of the test.
type grantTestEnv struct {
	env map[string]string
	gh  string // "" = not installed
	tok string // "" = not signed in
	exe string
}

func useGrantEnv(t *testing.T, e *grantTestEnv) {
	t.Helper()
	oldGet, oldExe, oldOS, oldTok, oldPath := grantGetenv, grantExecutable, grantGOOS, grantGHToken, grantGHPath
	ghAvailCache.at = ghAvailCache.at.Add(-24 * 3600e9)
	grantGetenv = func(k string) string { return e.env[k] }
	grantExecutable = func() (string, error) { return e.exe, nil }
	grantGOOS = "darwin"
	grantGHPath = func() string { return e.gh }
	grantGHToken = func() (string, error) {
		if e.tok == "" {
			return "", os.ErrNotExist
		}
		return e.tok, nil
	}
	t.Cleanup(func() {
		grantGetenv, grantExecutable, grantGOOS, grantGHToken, grantGHPath = oldGet, oldExe, oldOS, oldTok, oldPath
		ghAvailCache.at = ghAvailCache.at.Add(-24 * 3600e9)
	})
}

// grantHome makes a home folder with a project and the places grants touch.
func grantHome(t *testing.T) (home, wd string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	home = filepath.Join(root, "home")
	wd = filepath.Join(home, "projects", "app")
	for _, d := range []string{wd, filepath.Join(home, ".ssh"), filepath.Join(home, ".aws"), filepath.Join(home, "data")} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	return home, wd
}

// sshSocket makes a real unix socket (skips where the sandbox forbids it).
func sshSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ag")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("cannot listen on a unix socket here: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	return sock
}

func sandboxedOptions(wd string, grants ...string) *SessionOptions {
	return &SessionOptions{Sandboxed: boolp(true), WorkingDirectory: &wd, Provider: strp("claude"), Model: strp("opus"), SandboxGrants: grants}
}

func sandboxSection(t *testing.T, s claudeSessionSettings) map[string]any {
	t.Helper()
	sb, ok := settingsJSON(t, s)["sandbox"].(map[string]any)
	require.True(t, ok, "settings have a sandbox section")
	return sb
}

func strList(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			out = append(out, x.(string))
		}
	}
	return out
}

// ---- Parts 1 and 2: every sandboxed session ----

func TestSandboxedSession_HidesPersonalSecretsAndKeepsCaches(t *testing.T) {
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	home, wd := grantHome(t)
	s := buildClaudeSessionSettings(home, wd, true, []string{"example.com"})
	sb := sandboxSection(t, s)
	fs := sb["filesystem"].(map[string]any)
	denyRead := strList(fs["denyRead"])
	for _, rel := range []string{".ssh", ".gnupg", ".aws", ".azure", ".config/gcloud", ".config/gh", ".kube",
		".docker/config.json", ".netrc", ".npmrc", ".pypirc", ".cargo/credentials", ".cargo/credentials.toml",
		".gem/credentials", ".config/eggify", ".zsh_history", ".bash_history", "Library/Keychains", "Library/Cookies",
		"Library/Mail", "Library/Messages", "Library/Application Support/Google/Chrome"} {
		require.Contains(t, denyRead, filepath.Join(home, rel), rel)
	}
	require.NotContains(t, denyRead, filepath.Join(home, ".gitconfig"), "~/.gitconfig stays readable")
	// The file tools get matching deny rules.
	require.Contains(t, s.Permissions.Deny, "Read(/"+filepath.ToSlash(filepath.Join(home, ".ssh"))+"/**)")
	require.Contains(t, s.Permissions.Deny, "Edit(/"+filepath.ToSlash(filepath.Join(home, ".ssh"))+"/**)")
	require.Contains(t, s.Permissions.Deny, "Read(/"+filepath.ToSlash(filepath.Join(home, ".npmrc"))+")")
	// The cache folder is the only extra writable place.
	cache := sandboxCacheDir(home, wd)
	require.Equal(t, []string{cache}, strList(fs["allowWrite"]))
	require.Contains(t, cache, filepath.Join(home, ".cache", "eggify-sandbox"))
	require.NotContains(t, cache, filepath.Join(".claude", "eggify"))
	require.NotContains(t, strList(fs["allowWrite"]), "/tmp")
	for _, key := range []string{"allowRead"} {
		require.Nil(t, fs[key], key)
	}
	// No grant, no widening.
	net := sb["network"].(map[string]any)
	for _, key := range []string{"allowUnixSockets", "allowLocalBinding"} {
		require.Nil(t, net[key], key)
	}
	require.Nil(t, sb["enableWeakerNetworkIsolation"])
}

func TestUnsandboxedSessionIsUnchanged(t *testing.T) {
	home, wd := grantHome(t)
	s := buildClaudeSessionSettings(home, wd, false, nil)
	require.Nil(t, s.Sandbox)
	for _, rule := range s.Permissions.Deny {
		require.NotContains(t, rule, ".ssh", "personal secrets are only hidden in sandboxed sessions")
		require.NotContains(t, rule, ".npmrc")
	}
}

func TestPersonalSecretContainingWorkingDirIsNotHidden(t *testing.T) {
	home, _ := grantHome(t)
	wd := filepath.Join(home, ".config", "gh", "work")
	require.NoError(t, os.MkdirAll(wd, 0o755))
	s := buildClaudeSessionSettings(home, wd, true, nil)
	fs := sandboxSection(t, s)["filesystem"].(map[string]any)
	require.NotContains(t, strList(fs["denyRead"]), filepath.Join(home, ".config", "gh"))
	require.Contains(t, strList(fs["denyRead"]), filepath.Join(home, ".ssh"), "the others stay hidden")
	gh := "/" + filepath.ToSlash(filepath.Join(home, ".config", "gh"))
	require.NotContains(t, s.Permissions.Deny, "Read("+gh+"/**)")
}

func TestSandboxDefaultDomainsGainCrates(t *testing.T) {
	for _, d := range []string{"raw.githubusercontent.com", "crates.io", "static.crates.io", "index.crates.io"} {
		require.Contains(t, DefaultSandboxAllowedDomains, d)
	}
	n := len(DefaultSandboxAllowedDomains)
	require.Equal(t, "objects.githubusercontent.com", DefaultSandboxAllowedDomains[n-5], "existing order is kept, new entries come last")
	norm, err := NormalizeSandboxDomains(DefaultSandboxAllowedDomains)
	require.NoError(t, err)
	require.Len(t, norm, n)
}

func TestSandboxCacheEnvTable(t *testing.T) {
	home, _ := grantHome(t)
	cache := filepath.Join(home, ".cache", "eggify-sandbox", "k")
	dl := filepath.Join(home, "go", "pkg", "mod", "cache", "download")
	require.NoError(t, os.MkdirAll(dl, 0o755))

	env := sandboxCacheEnv(home, cache, func(k string) string {
		if k == "GOFLAGS" {
			return "-mod=mod"
		}
		return ""
	}, nil)
	require.Equal(t, filepath.Join(cache, "go-build"), env["GOCACHE"])
	require.Equal(t, filepath.Join(cache, "gopath"), env["GOPATH"])
	require.Equal(t, filepath.Join(cache, "gopath", "pkg", "mod"), env["GOMODCACHE"], "an inherited GOMODCACHE must not win")
	require.Equal(t, "-mod=mod -modcacherw", env["GOFLAGS"])
	require.Equal(t, "file://"+dl+",https://proxy.golang.org,direct", env["GOPROXY"])
	for name, sub := range map[string]string{"npm_config_cache": "npm", "UV_CACHE_DIR": "uv", "PIP_CACHE_DIR": "pip", "YARN_CACHE_FOLDER": "yarn", "BUN_INSTALL_CACHE_DIR": "bun"} {
		require.Equal(t, filepath.Join(cache, sub), env[name], name)
	}
	for _, never := range []string{"CARGO_HOME", "GRADLE_USER_HOME", "HOME", "XDG_CACHE_HOME", "RUSTUP_HOME"} {
		_, set := env[never]
		require.False(t, set, "%s moves credentials or config and is never redirected", never)
	}
	for _, v := range sandboxCacheVars {
		require.NotEmpty(t, v.Tool)
		require.False(t, strings.Contains(v.Name, "CARGO") || strings.Contains(v.Name, "GRADLE") || v.Name == "HOME")
	}

	// -modcacherw is not added twice (options are reused across CLI restarts).
	again := sandboxCacheEnv(home, cache, func(string) string { return "" }, map[string]string{"GOFLAGS": env["GOFLAGS"]})
	require.Equal(t, "-mod=mod -modcacherw", again["GOFLAGS"])
	// The user's own GOPROXY is kept in the middle; their trailing direct is not doubled.
	custom := sandboxCacheEnv(home, cache, func(k string) string {
		if k == "GOPROXY" {
			return "https://goproxy.example,direct"
		}
		return ""
	}, nil)
	require.Equal(t, "file://"+dl+",https://goproxy.example,direct", custom["GOPROXY"])
	// No module cache to feed from: no file proxy.
	none := sandboxCacheEnv(t.TempDir(), cache, func(string) string { return "" }, nil)
	_, hasProxy := none["GOPROXY"]
	require.False(t, hasProxy)
}

func TestRealGoModCachePrecedence(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	require.Equal(t, "/m", realGoModCache("/h", get(map[string]string{"GOMODCACHE": "/m", "GOPATH": "/p"})))
	require.Equal(t, "/p1/pkg/mod", realGoModCache("/h", get(map[string]string{"GOPATH": "/p1:/p2"})))
	require.Equal(t, "/h/go/pkg/mod", realGoModCache("/h", get(nil)))
}

func TestSandboxCacheKeyIsStablePerProject(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	require.Equal(t, sandboxCacheKey(a), sandboxCacheKey(a))
	require.NotEqual(t, sandboxCacheKey(a), sandboxCacheKey(b))
	require.Len(t, sandboxCacheKey(a), 12)
}

func TestApplyClaudeSessionSettings_SetsCacheEnvAndCreatesFolder(t *testing.T) {
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	home, wd := grantHome(t)
	t.Setenv("HOME", home)
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	opts := types.NewClaudeAgentOptions()
	out, err := sm.applyClaudeSessionSettings(opts, sandboxedOptions(wd))
	require.NoError(t, err)
	cache := sandboxCacheDir(home, wd)
	fi, err := os.Stat(cache)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), fi.Mode().Perm())
	// The cache variables are NOT in the CLI's environment (hooks and MCP servers
	// run outside the sandbox); only CLAUDE_ENV_FILE is.
	for _, v := range sandboxCacheVars {
		_, set := out.Env[v.Name]
		require.False(t, set, v.Name)
	}
	for _, n := range []string{"GOFLAGS", "GOPROXY"} {
		_, set := out.Env[n]
		require.False(t, set, n)
	}
	envFile := out.Env["CLAUDE_ENV_FILE"]
	require.Equal(t, sandboxEnvFile(home, "adhoc"), envFile)
	fi, err = os.Stat(envFile)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	content, err := os.ReadFile(envFile)
	require.NoError(t, err)
	require.Contains(t, string(content), "export GOCACHE='"+filepath.Join(cache, "go-build")+"'\n")
	require.Contains(t, string(content), "export GOFLAGS='-modcacherw'")
	var settings map[string]any
	require.NoError(t, json.Unmarshal([]byte(*out.Settings), &settings))
	fs := settings["sandbox"].(map[string]any)["filesystem"].(map[string]any)
	require.Contains(t, strList(fs["allowWrite"]), cache)
	require.NotContains(t, strList(fs["denyRead"]), sandboxEnvDir(home), "the env file must stay readable in the sandbox")

	// An unsandboxed session gets neither.
	plain := types.NewClaudeAgentOptions()
	wdc := wd
	out, err = sm.applyClaudeSessionSettings(plain, &SessionOptions{Sandboxed: boolp(false), WorkingDirectory: &wdc})
	require.NoError(t, err)
	_, set := out.Env["GOCACHE"]
	require.False(t, set)
	_, set = out.Env["CLAUDE_ENV_FILE"]
	require.False(t, set, "an unsandboxed session gets no env file")
}

func TestProjectRootKeysTheCache(t *testing.T) {
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	home, wd := grantHome(t)
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	sm.repo = database.NewRepository(database.GetInstance())
	projectRoot := filepath.Join(home, "projects")
	require.NoError(t, sm.repo.CreateProject(&database.Project{ID: "p1", Name: "p", Path: projectRoot, IsActive: true}))
	sub := filepath.Join(wd, "pkg")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.Equal(t, projectRoot, sm.sandboxProjectRoot(sandboxedOptions(wd)))
	require.Equal(t, projectRoot, sm.sandboxProjectRoot(sandboxedOptions(sub)), "same cache for every folder of the project")
	outside := t.TempDir()
	require.Equal(t, outside, sm.sandboxProjectRoot(sandboxedOptions(outside)))
}

// ---- Part 3: grants ----

func TestGrant_SSH(t *testing.T) {
	sock := sshSocket(t)
	home, wd := grantHome(t)
	exe := filepath.Join(home, "bin dir", "eggify")
	useGrantEnv(t, &grantTestEnv{env: map[string]string{"SSH_AUTH_SOCK": sock}, exe: exe})
	x := resolveSandboxExtras(home, wd, sandboxedOptions(wd, GrantSSH), nil)
	realSock, _ := filepath.EvalSymlinks(sock)
	require.Contains(t, x.UnixSockets, sock)
	require.Contains(t, x.UnixSockets, filepath.Dir(sock))
	require.Contains(t, x.UnixSockets, realSock)
	require.Contains(t, x.UnixSockets, filepath.Dir(realSock))
	require.ElementsMatch(t, []string{filepath.Join(home, ".ssh", "known_hosts"), filepath.Join(home, ".ssh", "config")}, x.AllowRead)
	require.Equal(t, "ssh -o ProxyCommand='"+strings.ReplaceAll(exe, " ", `\ `)+" sandbox-connect %h %p'", x.Env["GIT_SSH_COMMAND"])
	require.Empty(t, x.Unhide, "the private keys stay hidden")

	s := buildClaudeSessionSettingsWith(home, wd, true, nil, x)
	sb := sandboxSection(t, s)
	require.Contains(t, strList(sb["network"].(map[string]any)["allowUnixSockets"]), sock)
	fs := sb["filesystem"].(map[string]any)
	require.Contains(t, strList(fs["denyRead"]), filepath.Join(home, ".ssh"), "~/.ssh is still hidden")
	require.Contains(t, strList(fs["allowRead"]), filepath.Join(home, ".ssh", "known_hosts"))
	require.Nil(t, sb["network"].(map[string]any)["allowLocalBinding"])
}

func TestGrant_SSHUnavailable(t *testing.T) {
	home, wd := grantHome(t)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}, exe: filepath.Join(home, "eggify")})
	require.Equal(t, "No ssh-agent is running", grantUnavailableReason(GrantSSH))
	useGrantEnv(t, &grantTestEnv{env: map[string]string{"SSH_AUTH_SOCK": filepath.Join(home, "missing.sock")}, exe: filepath.Join(home, "eggify")})
	require.Contains(t, grantUnavailableReason(GrantSSH), "does not exist")
	useGrantEnv(t, &grantTestEnv{env: map[string]string{"SSH_AUTH_SOCK": filepath.Join(home, ".ssh")}, exe: filepath.Join(home, "eggify")})
	require.Contains(t, grantUnavailableReason(GrantSSH), "does not exist", "a folder is not a socket")
	// An executable path that cannot be quoted makes the grant unavailable instead of unsafe.
	sock := sshSocket(t)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{"SSH_AUTH_SOCK": sock}, exe: filepath.Join(home, "it's", "eggify")})
	require.NotEmpty(t, grantUnavailableReason(GrantSSH))
	// Left out at start when it vanished; the session still starts.
	x := resolveSandboxExtras(home, wd, sandboxedOptions(wd, GrantSSH), nil)
	require.Empty(t, x.UnixSockets)
	require.Empty(t, x.Env["GIT_SSH_COMMAND"])
}

func TestGitSSHCommandQuoting(t *testing.T) {
	require.Equal(t, "ssh -o ProxyCommand='/usr/local/bin/eggify sandbox-connect %h %p'", gitSSHCommand("/usr/local/bin/eggify"))
	require.Equal(t, `ssh -o ProxyCommand='/Applications/My\ App.app/eggify\ x sandbox-connect %h %p'`, gitSSHCommand("/Applications/My App.app/eggify x"))
	require.Equal(t, `ssh -o ProxyCommand='/a/\$b\;c sandbox-connect %h %p'`, gitSSHCommand("/a/$b;c"))
}

func TestGrant_GitHub(t *testing.T) {
	home, wd := grantHome(t)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}, gh: "/usr/bin/gh", tok: testGHToken})
	existing := map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "user.name", "GIT_CONFIG_VALUE_0": "x"}
	x := resolveSandboxExtras(home, wd, sandboxedOptions(wd, GrantGitHub), existing)
	require.Equal(t, testGHToken, x.Env["GH_TOKEN"])
	require.Equal(t, filepath.Join(sandboxEnvDir(home), "gh-adhoc"), x.Env["GH_CONFIG_DIR"], "gh's config folder is not in the agent-writable cache")
	require.NotContains(t, x.Env["GH_CONFIG_DIR"], x.CacheDir)
	fi, err := os.Stat(x.Env["GH_CONFIG_DIR"])
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), fi.Mode().Perm())
	require.True(t, x.WeakerNetworkIsolation)
	require.Equal(t, "2", x.Env["GIT_CONFIG_COUNT"], "merged with the session's own git config")
	require.Equal(t, "credential.https://github.com.helper", x.Env["GIT_CONFIG_KEY_1"])
	require.Equal(t, "!gh auth git-credential", x.Env["GIT_CONFIG_VALUE_1"])
	_, clobbered := x.Env["GIT_CONFIG_KEY_0"]
	require.False(t, clobbered)

	// Applying twice (a restarted CLI reuses its options) does not stack entries.
	for k, v := range x.Env {
		existing[k] = v
	}
	again := resolveSandboxExtras(home, wd, sandboxedOptions(wd, GrantGitHub), existing)
	_, count := again.Env["GIT_CONFIG_COUNT"]
	require.False(t, count)

	// The token is for the environment only.
	s := buildClaudeSessionSettingsWith(home, wd, true, nil, x)
	data, err := json.Marshal(s)
	require.NoError(t, err)
	require.NotContains(t, string(data), testGHToken)
	require.NotContains(t, string(data), "GH_TOKEN")
	require.Equal(t, true, sandboxSection(t, s)["enableWeakerNetworkIsolation"])
	for _, name := range sandboxDeniedEnvVars {
		require.NotEqual(t, "GH_TOKEN", name, "the credential deny list must not strip the token")
	}
	for _, v := range sandboxSection(t, s)["credentials"].(map[string]any)["envVars"].([]any) {
		require.NotEqual(t, "GH_TOKEN", v.(map[string]any)["name"])
	}
}

func TestGrant_GitHubUnavailable(t *testing.T) {
	home, wd := grantHome(t)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	require.Equal(t, "gh is not installed", grantUnavailableReason(GrantGitHub))
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}, gh: "/usr/bin/gh"})
	require.Equal(t, "gh is not signed in", grantUnavailableReason(GrantGitHub))
	x := resolveSandboxExtras(home, wd, sandboxedOptions(wd, GrantGitHub), nil)
	_, has := x.Env["GH_TOKEN"]
	require.False(t, has)
	require.False(t, x.WeakerNetworkIsolation)
}

func TestGrant_CloudNPMLocalhost(t *testing.T) {
	home, wd := grantHome(t)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})

	cloud := resolveSandboxExtras(home, wd, sandboxedOptions(wd, GrantCloud), nil)
	s := buildClaudeSessionSettingsWith(home, wd, true, nil, cloud)
	denyRead := strList(sandboxSection(t, s)["filesystem"].(map[string]any)["denyRead"])
	for _, rel := range []string{".aws", ".azure", ".config/gcloud", ".kube"} {
		require.NotContains(t, denyRead, filepath.Join(home, rel), rel)
	}
	require.Contains(t, denyRead, filepath.Join(home, ".ssh"))
	require.Contains(t, denyRead, filepath.Join(home, ".npmrc"))
	require.Equal(t, true, sandboxSection(t, s)["enableWeakerNetworkIsolation"])
	for _, r := range s.Permissions.Deny {
		require.NotContains(t, r, ".aws")
	}

	npm := resolveSandboxExtras(home, wd, sandboxedOptions(wd, GrantNPM), nil)
	s = buildClaudeSessionSettingsWith(home, wd, true, nil, npm)
	denyRead = strList(sandboxSection(t, s)["filesystem"].(map[string]any)["denyRead"])
	require.NotContains(t, denyRead, filepath.Join(home, ".npmrc"))
	require.Contains(t, denyRead, filepath.Join(home, ".aws"))
	require.Nil(t, sandboxSection(t, s)["enableWeakerNetworkIsolation"])

	lh := resolveSandboxExtras(home, wd, sandboxedOptions(wd, GrantLocalhost), nil)
	s = buildClaudeSessionSettingsWith(home, wd, true, nil, lh)
	require.Equal(t, true, sandboxSection(t, s)["network"].(map[string]any)["allowLocalBinding"])
}

func TestGrantPaths(t *testing.T) {
	home, wd := grantHome(t)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	data := filepath.Join(home, "data")
	link := filepath.Join(home, "datalink")
	require.NoError(t, os.Symlink(data, link))
	outside := t.TempDir()

	opts := sandboxedOptions(wd)
	opts.SandboxGrantPaths = []SandboxGrantPath{{Path: "~/data"}, {Path: link + "/../data/", Write: true}, {Path: filepath.Join(home, "projects")}}
	_, paths, err := NormalizeSandboxGrants(home, wd, nil, opts.SandboxGrantPaths)
	require.NoError(t, err)
	require.Len(t, paths, 2, "the same folder twice is one entry, and write wins")
	require.Equal(t, SandboxGrantPath{Path: data, Write: true}, paths[0])
	opts.SandboxGrantPaths = paths
	x := resolveSandboxExtras(home, wd, opts, nil)
	require.Contains(t, x.AllowRead, data)
	require.Equal(t, []string{data}, x.AllowWrite, "only the write grant is writable")
	s := buildClaudeSessionSettingsWith(home, wd, true, nil, x)
	fs := sandboxSection(t, s)["filesystem"].(map[string]any)
	require.Contains(t, strList(fs["allowWrite"]), data)
	require.Contains(t, strList(fs["allowRead"]), filepath.Join(home, "projects"))

	refused := map[string]SandboxGrantPath{
		"empty":               {Path: ""},
		"relative":            {Path: "data"},
		"missing":             {Path: "~/nope"},
		"outside home":        {Path: outside},
		"home itself":         {Path: "~"},
		"slash":               {Path: "/"},
		"ssh":                 {Path: "~/.ssh"},
		"inside ssh":          {Path: "~/.ssh/known_hosts"},
		"aws":                 {Path: "~/.aws"},
		"contains aws":        {Path: home + "/../" + filepath.Base(home)},
		"eggify state":        {Path: "~/.claude/eggify"},
		"claude":              {Path: "~/.claude"},
		"sandbox caches":      {Path: "~/.cache/eggify-sandbox"},
		"write shell startup": {Path: "~/.zshrc", Write: true},
	}
	for _, name := range []string{".zshrc"} {
		require.NoError(t, os.WriteFile(filepath.Join(home, name), nil, 0o600))
	}
	for _, d := range []string{".claude/eggify", ".cache/eggify-sandbox"} {
		require.NoError(t, os.MkdirAll(filepath.Join(home, d), 0o755))
	}
	for name, gp := range refused {
		_, _, err := NormalizeSandboxGrants(home, wd, nil, []SandboxGrantPath{gp})
		require.Error(t, err, name)
	}
	// A symlink out of the home folder is judged by where it leads.
	esc := filepath.Join(home, "escape")
	require.NoError(t, os.Symlink(outside, esc))
	_, _, err = NormalizeSandboxGrants(home, wd, nil, []SandboxGrantPath{{Path: esc}})
	require.Error(t, err)
	// Reading a place that only a write grant would change is fine.
	_, _, err = NormalizeSandboxGrants(home, wd, nil, []SandboxGrantPath{{Path: "~/.zshrc"}})
	require.NoError(t, err)

	// At most 10.
	many := make([]SandboxGrantPath, 11)
	for i := range many {
		many[i] = SandboxGrantPath{Path: "~/data"}
	}
	_, _, err = NormalizeSandboxGrants(home, wd, nil, many)
	require.ErrorContains(t, err, "at most 10")
	// Unknown ids are refused; known ones come back in table order.
	_, _, err = NormalizeSandboxGrants(home, wd, []string{"root"}, nil)
	require.ErrorContains(t, err, "unknown sandbox grant")
	ids, _, err := NormalizeSandboxGrants(home, wd, []string{"localhost", " SSH ", "ssh"}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{GrantSSH, GrantLocalhost}, ids)
}

func TestGrantPathProtectedByEggifyWhenWorkingInsideIt(t *testing.T) {
	home, _ := grantHome(t)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	cfg := filepath.Join(home, ".claude", "eggify")
	require.NoError(t, os.MkdirAll(filepath.Join(cfg, "logs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfg, ".secret"), []byte("k"), 0o600))
	// Working inside Eggify's folder does not make its secret grantable.
	_, _, err := NormalizeSandboxGrants(home, cfg, nil, []SandboxGrantPath{{Path: cfg}})
	require.Error(t, err)
}

func TestGrantAvailabilityListsEveryGrantWithWords(t *testing.T) {
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	list := SandboxGrantAvailability()
	require.Len(t, list, 5)
	byID := map[string]SandboxGrantStatus{}
	for _, g := range list {
		byID[g.ID] = g
		require.NotEmpty(t, g.Title)
		require.NotEmpty(t, g.Description)
		require.NotEmpty(t, g.Risk)
	}
	require.False(t, byID[GrantSSH].Available)
	require.Equal(t, "No ssh-agent is running", byID[GrantSSH].Reason)
	require.False(t, byID[GrantGitHub].Available)
	require.True(t, byID[GrantCloud].Available)
	require.True(t, byID[GrantNPM].Available)
	require.True(t, byID[GrantLocalhost].Available)
	require.Contains(t, strings.ToLower(byID[GrantLocalhost].Risk), "eggify", "localhost must warn that Eggify is reachable too")
	require.Contains(t, byID[GrantLocalhost].Risk, "every other service")
	data, err := json.Marshal(list)
	require.NoError(t, err)
	require.NotContains(t, string(data), testGHToken)
}

// ---- Who may grant, and where grants do not travel ----

func TestPrivilegedSessionOptionsNeedAUserForGrants(t *testing.T) {
	require.Equal(t, SandboxGrantAction, PrivilegedSessionOptions(&SessionOptions{SandboxGrants: []string{GrantLocalhost}}))
	require.Equal(t, SandboxGrantAction, PrivilegedSessionOptions(&SessionOptions{SandboxGrantPaths: []SandboxGrantPath{{Path: "~/x"}}}))
	require.Empty(t, PrivilegedSessionOptions(&SessionOptions{Sandboxed: boolp(true)}))
}

func TestCreateSessionGrants(t *testing.T) {
	home, wd := grantHome(t)
	t.Setenv("HOME", home)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()

	// Not sandboxed: refused with a clear error.
	_, err := sm.CreateSession(uuid.New(), SessionOptions{Sandboxed: boolp(false), WorkingDirectory: &wd, SandboxGrants: []string{GrantLocalhost}})
	require.ErrorContains(t, err, "need a sandboxed session")
	// Codex and OpenAI-compatible sessions: not supported yet, and it says so.
	_, err = sm.CreateSession(uuid.New(), SessionOptions{Sandboxed: boolp(true), WorkingDirectory: &wd, Provider: strp("codex"), Model: strp("gpt-6-astra"), SandboxGrants: []string{GrantLocalhost}})
	require.ErrorContains(t, err, "Codex")
	// Unknown id and unavailable grant.
	_, err = sm.CreateSession(uuid.New(), *sandboxedOptions(wd, "root"))
	require.ErrorContains(t, err, "unknown sandbox grant")
	_, err = sm.CreateSession(uuid.New(), *sandboxedOptions(wd, GrantSSH))
	require.ErrorContains(t, err, "No ssh-agent is running")

	created, err := sm.CreateSession(uuid.New(), *sandboxedOptions(wd, "LOCALHOST", GrantNPM))
	require.NoError(t, err)
	require.Equal(t, []string{GrantNPM, GrantLocalhost}, created.Options.SandboxGrants)
	// They are in the session JSON clients read, and in the stored options.
	data, err := json.Marshal(created)
	require.NoError(t, err)
	require.Contains(t, string(data), `"sandbox_grants":["npm","localhost"]`)

	// Fixed at creation: updates, reopen and restore cannot change them.
	updated, err := sm.UpdateSessionOptions(created.ID, func(o *SessionOptions) {
		o.SandboxGrants = []string{GrantCloud}
		o.SandboxGrantPaths = []SandboxGrantPath{{Path: "~/data"}}
	})
	require.NoError(t, err)
	require.Equal(t, []string{GrantNPM, GrantLocalhost}, updated.Options.SandboxGrants)
	require.Empty(t, updated.Options.SandboxGrantPaths)
	plain := seedSwitchSession(t, sm, "claude", "opus", nil)
	updated, err = sm.UpdateSessionOptions(plain.ID, func(o *SessionOptions) { o.SandboxGrants = []string{GrantCloud} })
	require.NoError(t, err)
	require.Empty(t, updated.Options.SandboxGrants, "an update cannot add grants")
	sm.mu.Lock()
	delete(sm.sessions, created.ID)
	sm.mu.Unlock()
	restored, err := sm.CreateSession(created.ID, SessionOptions{SandboxGrants: []string{GrantCloud}})
	require.NoError(t, err)
	require.Equal(t, []string{GrantNPM, GrantLocalhost}, restored.Options.SandboxGrants)
	reopened, err := sm.CreateSession(plain.ID, SessionOptions{SandboxGrants: []string{GrantCloud}})
	require.NoError(t, err)
	require.Empty(t, reopened.Options.SandboxGrants, "reopening cannot add grants to an existing session")

	// A model switch off the Claude CLI would silently drop them: refused.
	_, err = sm.ChangeSessionModel(created.ID, "codex", "gpt-6-astra", nil)
	require.ErrorContains(t, err, "grants")
}

func TestRedTeam_HandoverDropsSandboxGrants(t *testing.T) {
	opts := SessionOptions{Sandboxed: boolp(true), SandboxGrants: []string{GrantGitHub}, SandboxGrantPaths: []SandboxGrantPath{{Path: "~/x", Write: true}}}
	for _, keep := range []HandoverOptions{{}, {PreserveYOLOMode: true, PreserveAlwaysAllowRules: true}} {
		o := opts
		stripHandoverPermissions(&o, keep)
		require.Empty(t, o.SandboxGrants, "even with keep_permissions")
		require.Empty(t, o.SandboxGrantPaths)
		require.True(t, isSandboxed(&o))
	}
}

func TestRedTeam_WorkerAndLoopSessionsHaveNoGrants(t *testing.T) {
	home, wd := grantHome(t)
	t.Setenv("HOME", home)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	// Even if a caller put grants next to a worker's config, they are dropped.
	opts := *sandboxedOptions(wd, GrantLocalhost)
	opts.Worker = &WorkerConfig{Task: "x", Mode: WorkerModeBuild}
	parent := uuid.New()
	opts.SpawnedBySessionID = &parent
	created, err := sm.CreateSession(uuid.New(), opts)
	require.NoError(t, err)
	require.Empty(t, created.Options.SandboxGrants)
	require.Empty(t, created.Options.SandboxGrantPaths)

}

func TestSandboxEnvFileContentAndQuoting(t *testing.T) {
	home, _ := grantHome(t)
	odd := filepath.Join(home, "it's a dir", "cache")
	path, err := writeSandboxEnvFile(home, "sid-1", "/etc/my env's file.sh", map[string]string{"GOCACHE": odd, "B": "x"})
	require.NoError(t, err)
	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	dfi, err := os.Stat(sandboxEnvDir(home))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dfi.Mode().Perm())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	want := "[ -r '/etc/my env'\\''s file.sh' ] && . '/etc/my env'\\''s file.sh'\n" +
		"export B='x'\n" +
		"export GOCACHE='" + strings.ReplaceAll(odd, "'", `'\''`) + "'\n"
	require.Contains(t, string(data), want)
	// The shell reads it back as the exact value.
	out, err := exec.Command("/bin/sh", "-c", ". '"+path+"' >/dev/null 2>&1; printf %s \"$GOCACHE\"").Output()
	require.NoError(t, err)
	require.Equal(t, odd, string(out))

	// A server-set CLAUDE_ENV_FILE is sourced first, not dropped; ours is not sourced from itself.
	self, err := writeSandboxEnvFile(home, "sid-2", sandboxEnvFile(home, "sid-2"), map[string]string{"A": "1"})
	require.NoError(t, err)
	data, _ = os.ReadFile(self)
	require.NotContains(t, string(data), "[ -r")

	// Removal and the startup sweep.
	id := uuid.New()
	_, err = writeSandboxEnvFile(home, id.String(), "", map[string]string{"A": "1"})
	require.NoError(t, err)
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxEnvDir(home), "gh-"+id.String()), 0o700))
	removeSandboxEnv(id)
	_, err = os.Stat(sandboxEnvFile(home, id.String()))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(sandboxEnvDir(home), "gh-"+id.String()))
	require.True(t, os.IsNotExist(err))
}

func TestSweepSandboxEnvKeepsFreshAndLiveEntries(t *testing.T) {
	home, _ := grantHome(t)
	dir := sandboxEnvDir(home)
	old, fresh, liveOld := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{old, fresh, liveOld} {
		_, err := writeSandboxEnvFile(home, id, "", map[string]string{"A": "1"})
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "gh-"+id), 0o700))
	}
	longAgo := time.Now().Add(-48 * time.Hour)
	for _, id := range []string{old, liveOld} {
		require.NoError(t, os.Chtimes(sandboxEnvFile(home, id), longAgo, longAgo))
		require.NoError(t, os.Chtimes(filepath.Join(dir, "gh-"+id), longAgo, longAgo))
	}
	sweepSandboxEnvDir(dir, time.Now().Add(-sandboxEnvMaxAge), func(id string) bool { return id == liveOld })
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	require.False(t, exists(sandboxEnvFile(home, old)), "an old entry is removed")
	require.False(t, exists(filepath.Join(dir, "gh-"+old)))
	require.True(t, exists(sandboxEnvFile(home, fresh)), "a fresh one (another instance's session) is kept")
	require.True(t, exists(filepath.Join(dir, "gh-"+fresh)))
	require.True(t, exists(sandboxEnvFile(home, liveOld)), "an entry of a session this process holds is kept")
}

func TestSandboxEnvAndLaunchersAreWriteProtectedWhateverTheWorkingDir(t *testing.T) {
	home, _ := grantHome(t)
	inside := filepath.Join(home, ".claude", "eggify", "logs")
	require.NoError(t, os.MkdirAll(inside, 0o755))
	for _, wd := range []string{filepath.Join(home, "projects", "app"), inside, filepath.Join(home, ".claude", "eggify")} {
		prot := sandboxWriteProtected(home, wd)
		require.Contains(t, prot, sandboxEnvDir(home), wd)
		require.Contains(t, prot, filepath.Join(home, ".claude", "eggify", "launchers"), wd)
		s := buildClaudeSessionSettings(home, wd, true, nil)
		fs := sandboxSection(t, s)["filesystem"].(map[string]any)
		require.Contains(t, strList(fs["denyWrite"]), sandboxEnvDir(home), wd)
		require.Contains(t, s.Permissions.Deny, "Edit(/"+filepath.ToSlash(sandboxEnvDir(home))+"/**)", wd)
		require.Contains(t, s.Permissions.Deny, "Edit(/"+filepath.ToSlash(filepath.Join(home, ".claude", "eggify", "launchers"))+"/**)", wd)
	}
}

func TestGrantedSpawnedSessionDoesNotShareTheWorkersCache(t *testing.T) {
	home, wd := grantHome(t)
	parent := uuid.New()
	worker := sandboxCacheDirFor(home, wd, &SessionOptions{SpawnedBySessionID: &parent})
	granted := sandboxCacheDirFor(home, wd, &SessionOptions{SpawnedBySessionID: &parent, SandboxGrants: []string{GrantNPM}})
	require.NotEqual(t, worker, granted)
	require.NotEqual(t, granted, sandboxCacheDirFor(home, wd, &SessionOptions{SandboxGrants: []string{GrantNPM}}))
}

func TestGrantScopesUseTheResolvedWorkingDir(t *testing.T) {
	home, _ := grantHome(t)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	realWD := filepath.Join(home, "real-wd")
	require.NoError(t, os.MkdirAll(filepath.Join(realWD, "sub"), 0o755))
	linkWD := filepath.Join(home, "link-wd")
	require.NoError(t, os.Symlink(realWD, linkWD))
	// The session is started through a link; a path inside the real folder is still its own.
	_, _, err := NormalizeSandboxGrants(home, linkWD, nil, []SandboxGrantPath{{Path: filepath.Join(realWD, "sub")}})
	require.ErrorContains(t, err, "writable by this session")
}

func TestSSHGrantReadsResolveSymlinks(t *testing.T) {
	home, wd := grantHome(t)
	sock := sshSocket(t)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{"SSH_AUTH_SOCK": sock}, exe: filepath.Join(home, "eggify")})
	real := filepath.Join(home, "dotfiles", "known_hosts")
	require.NoError(t, os.MkdirAll(filepath.Dir(real), 0o755))
	require.NoError(t, os.WriteFile(real, nil, 0o600))
	require.NoError(t, os.Symlink(real, filepath.Join(home, ".ssh", "known_hosts")))
	x := resolveSandboxExtras(home, wd, sandboxedOptions(wd, GrantSSH), nil)
	require.Contains(t, x.AllowRead, filepath.Join(home, ".ssh", "known_hosts"))
	require.Contains(t, x.AllowRead, real, "the symlink target is opened too")
}

func TestMorePersonalSecretsAreHidden(t *testing.T) {
	home, wd := grantHome(t)
	s := buildClaudeSessionSettings(home, wd, true, nil)
	denyRead := strList(sandboxSection(t, s)["filesystem"].(map[string]any)["denyRead"])
	for _, rel := range []string{".git-credentials", ".pgpass", ".my.cnf", ".appstoreconnect", ".config/op",
		".terraform.d/credentials.tfrc.json", ".vault-token", ".config/doctl", ".config/hub",
		"Library/Application Support/1Password", "Library/Group Containers/2BUA8C4S2C.com.1password"} {
		require.Contains(t, denyRead, filepath.Join(home, rel), rel)
	}
}

func TestSandboxCacheClassesAreSeparate(t *testing.T) {
	home, wd := grantHome(t)
	parentA, parentB := uuid.New(), uuid.New()
	plain := &SessionOptions{}
	granted := &SessionOptions{SandboxGrants: []string{GrantNPM}}
	readOnly := &SessionOptions{ReadOnly: true}
	buildWorker := &SessionOptions{SpawnedBySessionID: &parentA}
	reviewWorker := &SessionOptions{SpawnedBySessionID: &parentA, ReadOnly: true}
	otherParent := &SessionOptions{SpawnedBySessionID: &parentB}
	dirs := map[string]string{}
	for name, o := range map[string]*SessionOptions{"plain": plain, "granted": granted, "ro": readOnly, "build": buildWorker, "review": reviewWorker, "other parent": otherParent, "ro+granted": {ReadOnly: true, SandboxGrants: []string{GrantNPM}}} {
		d := sandboxCacheDirFor(home, wd, o)
		for prev, pd := range dirs {
			require.NotEqual(t, pd, d, "%s and %s share a cache", prev, name)
		}
		dirs[name] = d
	}
	// Same class and project: same folder.
	require.Equal(t, dirs["plain"], sandboxCacheDirFor(home, wd, &SessionOptions{}))
	require.Equal(t, dirs["plain"], sandboxCacheDir(home, wd), "the settings builder's default is the plain class")
	require.Equal(t, dirs["granted"], sandboxCacheDirFor(home, wd, &SessionOptions{SandboxGrants: []string{GrantSSH}}))
	require.Equal(t, dirs["build"], sandboxCacheDirFor(home, wd, &SessionOptions{SpawnedBySessionID: &parentA}))
	// Another project: another folder.
	require.NotEqual(t, dirs["plain"], sandboxCacheDirFor(home, filepath.Join(home, "data"), plain))
	// Through the resolver (what a session actually gets).
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	user := resolveSandboxExtras(home, wd, sandboxedOptions(wd), nil)
	worker := resolveSandboxExtras(home, wd, &SessionOptions{Sandboxed: boolp(true), WorkingDirectory: &wd, SpawnedBySessionID: &parentA}, nil)
	require.NotEqual(t, user.CacheDir, worker.CacheDir)
}

func TestUnsafeSocketDirs(t *testing.T) {
	home, _ := grantHome(t)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	for _, dir := range []string{
		home, "/tmp", "/private/tmp", "/var/run", "/private/var/run", "/var/folders/ab/cdef/T", "/private/var/folders/ab/cdef/T", "/",
		filepath.Join(home, ".ssh"), filepath.Join(home, ".gnupg"), filepath.Join(home, ".ssh", "agent"),
		filepath.Join(home, ".config"), filepath.Join(home, "Library", "Keychains"),
	} {
		require.True(t, unsafeSocketDir(home, dir), dir)
	}
	for _, dir := range []string{"/private/tmp/com.apple.launchd.AbCd", "/var/folders/ab/cdef/T/ssh-XYZ", filepath.Join(home, "run", "agent"), "/run/user/501/keyring"} {
		require.False(t, unsafeSocketDir(home, dir), dir)
	}
	for _, shared := range []string{filepath.Join(home, ".ssh"), filepath.Join(home, ".gnupg"), home, "/tmp", "/private/var/run"} {
		require.Equal(t, "The ssh-agent socket is in a shared folder", sshSocketFolderProblem(home, filepath.Join(shared, "agent.sock")), shared)
	}
}

func TestSSHSocketFolderRules(t *testing.T) {
	home, _ := grantHome(t)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	// Another socket in the folder makes it unavailable; a lone socket is fine.
	dir, err := os.MkdirTemp("", "sk")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	lone := filepath.Join(dir, "agent.sock")
	l1, err := net.Listen("unix", lone)
	if err != nil {
		t.Skipf("no unix sockets here: %v", err)
	}
	defer l1.Close()
	require.Empty(t, sshSocketFolderProblem(home, lone))
	l2, err := net.Listen("unix", filepath.Join(dir, "other.sock"))
	require.NoError(t, err)
	defer l2.Close()
	require.Equal(t, "The ssh-agent socket's folder holds other sockets", sshSocketFolderProblem(home, lone))
	// A regular file next to it is fine.
	require.NoError(t, os.Remove(filepath.Join(dir, "other.sock")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes"), nil, 0o600))
	require.Empty(t, sshSocketFolderProblem(home, lone))

	// A socket in a shared or hidden folder: the grant is unavailable with a plain reason.
	for _, shared := range []string{filepath.Join(home, ".ssh"), filepath.Join(home, ".gnupg"), home} {
		require.Equal(t, "The ssh-agent socket is in a shared folder", sshSocketFolderProblem(home, filepath.Join(shared, "agent.sock")), shared)
	}
	// The socket itself is always given in both spellings; the folder only with the fallback on.
	link := filepath.Join(t.TempDir(), "link.sock")
	require.NoError(t, os.Symlink(lone, link))
	x := &sandboxExtras{Env: map[string]string{}}
	useGrantEnv(t, &grantTestEnv{env: map[string]string{"SSH_AUTH_SOCK": link}, exe: filepath.Join(home, "eggify")})
	x.addSSH(home, nil)
	realLone, _ := filepath.EvalSymlinks(lone)
	require.Contains(t, x.UnixSockets, link)
	require.Contains(t, x.UnixSockets, realLone)
	require.True(t, sshGrantAddsSocketFolder, "the verified configuration opens the folder")
	require.Contains(t, x.UnixSockets, filepath.Dir(realLone))
}

func TestGrantPathStoredResolvedAndSwapIsNotFollowed(t *testing.T) {
	home, wd := grantHome(t)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	real1 := filepath.Join(home, "real1")
	real2 := filepath.Join(home, "real2")
	require.NoError(t, os.MkdirAll(real1, 0o755))
	require.NoError(t, os.MkdirAll(real2, 0o755))
	link := filepath.Join(home, "link")
	require.NoError(t, os.Symlink(real1, link))

	_, paths, err := NormalizeSandboxGrants(home, wd, nil, []SandboxGrantPath{{Path: "~/link"}})
	require.NoError(t, err)
	require.Equal(t, []SandboxGrantPath{{Path: real1}}, paths, "the resolved path is stored")

	opts := sandboxedOptions(wd)
	opts.SandboxGrantPaths = paths
	x := resolveSandboxExtras(home, wd, opts, nil)
	require.Equal(t, []string{real1}, x.AllowRead)
	require.Empty(t, x.Notices)

	// The link is swapped after creation: the stored folder is still the one given.
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(real2, link))
	x = resolveSandboxExtras(home, wd, opts, nil)
	require.Equal(t, []string{real1}, x.AllowRead)
	// The stored folder itself being replaced by a link elsewhere is caught and noticed.
	require.NoError(t, os.RemoveAll(real1))
	require.NoError(t, os.Symlink(real2, real1))
	x = resolveSandboxExtras(home, wd, opts, nil)
	require.Empty(t, x.AllowRead)
	require.Len(t, x.Notices, 1)
	require.Contains(t, x.Notices[0], "no longer leads")
}

func TestGrantPathRefusedWhenAComponentIsWritableBySession(t *testing.T) {
	home, wd := grantHome(t)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	// Inside the working directory.
	inside := filepath.Join(wd, "vendor", "x")
	require.NoError(t, os.MkdirAll(inside, 0o755))
	_, _, err := NormalizeSandboxGrants(home, wd, nil, []SandboxGrantPath{{Path: inside}})
	require.ErrorContains(t, err, "writable by this session")
	// A link inside the working directory that leads to a folder outside it.
	target := filepath.Join(home, "data")
	link := filepath.Join(wd, "to-data")
	require.NoError(t, os.Symlink(target, link))
	_, _, err = NormalizeSandboxGrants(home, wd, nil, []SandboxGrantPath{{Path: link}})
	require.ErrorContains(t, err, "writable by this session")
	// Below another write grant.
	w := filepath.Join(home, "work")
	require.NoError(t, os.MkdirAll(filepath.Join(w, "sub"), 0o755))
	_, _, err = NormalizeSandboxGrants(home, wd, nil, []SandboxGrantPath{{Path: w, Write: true}, {Path: filepath.Join(w, "sub")}})
	require.ErrorContains(t, err, "writable by this session")
	// The sandbox cache root.
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxCacheRoot(home), "k"), 0o755))
	_, _, err = NormalizeSandboxGrants(home, wd, nil, []SandboxGrantPath{{Path: filepath.Join(sandboxCacheRoot(home), "k")}})
	require.Error(t, err)
	// A write grant on its own, and a read grant next to it, are fine.
	_, _, err = NormalizeSandboxGrants(home, wd, nil, []SandboxGrantPath{{Path: w, Write: true}, {Path: target}})
	require.NoError(t, err)
}

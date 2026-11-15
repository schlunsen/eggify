package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/stretchr/testify/require"
)

// autopilotEnv is a home folder with a project, a folder outside it, links and a grant folder.
type autopilotEnv struct {
	home, wd, data string
	scope          autopilotScope
}

// fakeClaudeTemp moves the CLI's temp folders away from the test's own temp folder.
func fakeClaudeTemp(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "autopilot-fake-claude-tmp")
	autopilotTempRoots = func(int) []string { return []string{dir} }
	t.Cleanup(func() { autopilotTempRoots = nil })
	return dir
}

func newAutopilotEnv(t *testing.T, mutate func(*SessionOptions)) autopilotEnv {
	t.Helper()
	fakeClaudeTemp(t)
	home, wd := grantHome(t)
	data := filepath.Join(home, "data")
	require.NoError(t, os.MkdirAll(filepath.Join(wd, ".git", "hooks"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(wd, ".claude"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".zshrc"), []byte("# rc\n"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(home, ".zshrc"), filepath.Join(wd, "rc-link")))
	require.NoError(t, os.Symlink(filepath.Join(home, "Library", "LaunchAgents"), filepath.Join(wd, "agents-link")))
	require.NoError(t, os.Symlink(filepath.Join(home, "nowhere"), filepath.Join(wd, "dangling")))
	require.NoError(t, os.Symlink(filepath.Join(wd, "src"), filepath.Join(wd, "inner-link")))
	require.NoError(t, os.MkdirAll(filepath.Join(wd, "src"), 0o755))
	opts := sandboxedOptions(wd)
	opts.DangerouslySkipPermissions = boolp(true)
	if mutate != nil {
		mutate(opts)
	}
	return autopilotEnv{home: home, wd: wd, data: data, scope: newAutopilotScope(home, opts, []string{"github.com"})}
}

func write(path string) map[string]interface{} { return map[string]interface{}{"file_path": path} }

func TestAutopilotDecide_FileTools(t *testing.T) {
	e := newAutopilotEnv(t, nil)
	cases := []struct {
		name  string
		tool  string
		input map[string]interface{}
		allow bool
	}{
		{"write inside project", "Write", write(filepath.Join(e.wd, "main.go")), true},
		{"edit inside project", "Edit", write(filepath.Join(e.wd, "src", "a.go")), true},
		{"multiedit inside project", "MultiEdit", write(filepath.Join(e.wd, "a.go")), true},
		{"relative path is the project's", "Write", write("notes.md"), true},
		{"new file in new sub-folder", "Write", write(filepath.Join(e.wd, "new", "deep", "x.txt")), true},
		{"link inside the project to a folder inside it", "Write", write(filepath.Join(e.wd, "inner-link", "x.txt")), true},
		{"notebook inside project", "NotebookEdit", map[string]interface{}{"notebook_path": filepath.Join(e.wd, "n.ipynb")}, true},
		{"notebook outside", "NotebookEdit", map[string]interface{}{"notebook_path": filepath.Join(e.home, "n.ipynb")}, false},
		{"file in home", "Write", write(filepath.Join(e.home, "probe.txt")), false},
		{"LaunchAgent", "Write", write(filepath.Join(e.home, "Library", "LaunchAgents", "x.plist")), false},
		{".. out of the project", "Write", write(filepath.Join(e.wd, "..", "..", "probe.txt")), false},
		{"relative .. out of the project", "Edit", write("../../.zshrc"), false},
		{"link to ~/.zshrc", "Edit", write(filepath.Join(e.wd, "rc-link")), false},
		{"file through a link to LaunchAgents", "Write", write(filepath.Join(e.wd, "agents-link", "x.plist")), false},
		{"dangling link", "Write", write(filepath.Join(e.wd, "dangling")), false},
		{"new file below a dangling link", "Write", write(filepath.Join(e.wd, "dangling", "x")), false},
		{"git hook", "Write", write(filepath.Join(e.wd, ".git", "hooks", "pre-commit")), false},
		{"git config", "Edit", write(filepath.Join(e.wd, ".git", "config")), false},
		{".mcp.json", "Write", write(filepath.Join(e.wd, ".mcp.json")), false},
		{"project claude settings", "Write", write(filepath.Join(e.wd, ".claude", "settings.json")), false},
		{"claude hooks", "Write", write(filepath.Join(e.wd, ".claude", "hooks", "h.sh")), false},
		{"protected path in other case", "Write", write(filepath.Join(e.wd, ".GIT", "hooks", "x")), false},
		{"own CLI scratchpad", "Write", write(filepath.Join(claudeScratchRoots(0, e.wd)[0], "sess", "scratchpad", "a.txt")), true},
		{"another working directory's scratchpad", "Write", write(filepath.Join(claudeScratchRoots(0, "/Users/other/project")[0], "sess", "a.txt")), false},
		{"CLI settings file in the shared temp folder", "Write", write(filepath.Join(claudeTempRoots(0)[0], "claude-settings-0123abcd.json")), false},
		{"bash-edit-diff in the shared temp folder", "Write", write(filepath.Join(claudeTempRoots(0)[0], "bash-edit-diff", "x")), false},
		{"scratchpad tasks folder", "Write", write(filepath.Join(claudeScratchRoots(0, e.wd)[0], "sess", "tasks", "t.output")), false},
		{"relative path out of the project", "Write", write("../outside.txt"), false},
		{"other folder in /tmp", "Write", write("/tmp/elsewhere/x"), false},
		{"folder outside, not granted", "Write", write(filepath.Join(e.data, "x")), false},
		{"empty path", "Write", write(""), false},
		{"tilde path", "Write", write("~/x"), false},
		{"unknown built-in tool that names a file", "FutureWriter", write(filepath.Join(e.home, "x")), false},
		{"unknown built-in tool inside the project", "FutureWriter", write(filepath.Join(e.wd, "x")), true},
		{"read outside the project", "Read", write(filepath.Join(e.home, ".zshrc")), true},
		{"glob", "Glob", map[string]interface{}{"pattern": "**/*.go"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := autopilotDecide(e.scope, c.tool, c.input)
			require.True(t, d.Handled)
			require.Equal(t, c.allow, d.Allow, d.Message)
			if !c.allow {
				require.Contains(t, d.Message, "sandboxed")
			}
		})
	}
}

func TestAutopilotDecide_DenialNamesTheFolder(t *testing.T) {
	e := newAutopilotEnv(t, nil)
	d := autopilotDecide(e.scope, "Write", write(filepath.Join(e.home, "x")))
	require.Contains(t, d.Message, "can only change files inside "+e.wd)
	require.Contains(t, d.Message, "Ask the user to add this folder")
	d = autopilotDecide(e.scope, "Write", write(filepath.Join(e.wd, "rc-link")))
	require.Contains(t, d.Message, "symbolic link")
}

func TestAutopilotDecide_WriteGrant(t *testing.T) {
	e := newAutopilotEnv(t, func(o *SessionOptions) {
		o.SandboxGrantPaths = []SandboxGrantPath{{Path: "~/data", Write: true}}
	})
	// "~/data" is stored resolved at creation: the test's home is not the real one.
	e.scope = newAutopilotScope(e.home, &SessionOptions{Sandboxed: boolp(true), WorkingDirectory: &e.wd,
		SandboxGrantPaths: []SandboxGrantPath{{Path: e.data, Write: true}}}, nil)
	require.True(t, autopilotDecide(e.scope, "Write", write(filepath.Join(e.data, "out.csv"))).Allow)
	// A read-only path grant opens nothing for the file tools.
	ro := newAutopilotScope(e.home, &SessionOptions{Sandboxed: boolp(true), WorkingDirectory: &e.wd,
		SandboxGrantPaths: []SandboxGrantPath{{Path: e.data}}}, nil)
	require.False(t, autopilotDecide(ro, "Write", write(filepath.Join(e.data, "out.csv"))).Allow)
	// A grant never reaches protected files.
	require.False(t, autopilotDecide(e.scope, "Write", write(filepath.Join(e.home, ".claude", "settings.json"))).Allow)
}

func TestAutopilotDecide_ReadOnlySession(t *testing.T) {
	e := newAutopilotEnv(t, func(o *SessionOptions) { o.ReadOnly = true })
	for _, tool := range []string{"Write", "Edit", "MultiEdit"} {
		d := autopilotDecide(e.scope, tool, write(filepath.Join(e.wd, "a.go")))
		require.False(t, d.Allow, tool)
		require.Contains(t, d.Message, "read-only")
	}
	require.True(t, autopilotDecide(e.scope, "Read", write(filepath.Join(e.wd, "a.go"))).Allow)
	require.True(t, autopilotDecide(e.scope, "Bash", map[string]interface{}{"command": "go version"}).Allow)
}

func TestAutopilotDecide_WideWorkingDirectoryGivesNoWriteScope(t *testing.T) {
	fakeClaudeTemp(t)
	home, _ := grantHome(t)
	for _, wd := range []string{home, filepath.Dir(home), "/"} {
		wd := wd
		sc := newAutopilotScope(home, &SessionOptions{Sandboxed: boolp(true), WorkingDirectory: &wd}, nil)
		require.False(t, autopilotDecide(sc, "Write", write(filepath.Join(home, "x.txt"))).Allow, wd)
		require.True(t, autopilotDecide(sc, "Write", write(filepath.Join(claudeScratchRoots(os.Getuid(), cleanAbs(wd))[0], "x"))).Allow)
	}
}

func TestAutopilotDecide_BashWebAndOthers(t *testing.T) {
	e := newAutopilotEnv(t, nil)
	cases := []struct {
		name    string
		tool    string
		input   map[string]interface{}
		handled bool
		allow   bool
	}{
		{"sandboxed command", "Bash", map[string]interface{}{"command": "go version"}, true, true},
		{"command asking to leave the sandbox", "Bash", map[string]interface{}{"command": "ls", "dangerouslyDisableSandbox": true}, true, false},
		{"fetch from an allowlisted host", "WebFetch", map[string]interface{}{"url": "https://github.com/x"}, true, true},
		{"fetch from another host", "WebFetch", map[string]interface{}{"url": "https://example.org"}, true, false},
		{"web search", "WebSearch", map[string]interface{}{"query": "go"}, true, false},
		{"mcp tool", "mcp__eggify-tools__list_sessions", map[string]interface{}{}, true, true},
		{"mcp tool that names a file", "mcp__x__save", write("/etc/x"), true, true},
		{"subagent launch", "Agent", map[string]interface{}{"prompt": "x"}, true, true},
		{"todo", "TodoWrite", map[string]interface{}{"todos": []interface{}{}}, true, true},
		{"a question goes to the user", "AskUserQuestion", map[string]interface{}{"questions": []interface{}{}}, false, false},
		{"plan approval goes to the user", "ExitPlanMode", map[string]interface{}{"plan": "x"}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := autopilotDecide(e.scope, c.tool, c.input)
			require.Equal(t, c.handled, d.Handled)
			require.Equal(t, c.allow, d.Allow, d.Message)
		})
	}
	web := autopilotDecide(e.scope, "WebFetch", map[string]interface{}{"url": "https://example.org"})
	require.Contains(t, web.Message, "Settings → Sandbox")
}

func TestAutopilotSessionRecognisesEveryPromptsOffSpelling(t *testing.T) {
	mk := func(sandboxed bool, mutate func(*SessionOptions)) *SessionOptions {
		o := &SessionOptions{Sandboxed: boolp(sandboxed)}
		mutate(o)
		return o
	}
	for name, mutate := range map[string]func(*SessionOptions){
		"dangerously skip": func(o *SessionOptions) { o.DangerouslySkipPermissions = boolp(true) },
		"yolo":             func(o *SessionOptions) { o.PermissionMode = strp("yolo") },
		"bypass":           func(o *SessionOptions) { o.PermissionMode = strp("bypassPermissions") },
		"allow-all":        func(o *SessionOptions) { o.PermissionMode = strp("allow-all") },
	} {
		require.True(t, autopilotSession(mk(true, mutate)), name)
		require.False(t, autopilotSession(mk(false, mutate)), name+" unsandboxed")
	}
	for name, mutate := range map[string]func(*SessionOptions){
		"nothing":             func(o *SessionOptions) {},
		"safety switch only":  func(o *SessionOptions) { o.AllowDangerouslySkipPermissions = boolp(true) },
		"prompts on":          func(o *SessionOptions) { o.PermissionMode = strp("default") },
		"explicitly not yolo": func(o *SessionOptions) { o.DangerouslySkipPermissions = boolp(false) },
	} {
		require.False(t, autopilotSession(mk(true, mutate)), name)
	}
}

// The SDK options of a sandboxed session with prompts off carry neither the
// bypass mode nor the skip flags; an unsandboxed one still does.
func TestBuildSDKOptions_AutopilotHasNoBypass(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	for name, mode := range map[string]func(*SessionOptions){
		"dangerously": func(o *SessionOptions) {
			o.DangerouslySkipPermissions = boolp(true)
			o.AllowDangerouslySkipPermissions = boolp(true)
		},
		"yolo mode": func(o *SessionOptions) { o.PermissionMode = strp("yolo") },
		"allow-all": func(o *SessionOptions) { o.PermissionMode = strp("allow-all") },
		"worker-ish": func(o *SessionOptions) {
			o.Worker = &WorkerConfig{Task: "x", Mode: WorkerModeBuild}
			o.DangerouslySkipPermissions = boolp(true)
			o.AllowDangerouslySkipPermissions = boolp(true)
		},
	} {
		o := SessionOptions{Sandboxed: boolp(true)}
		mode(&o)
		got := sm.buildSDKOptions(o)
		require.NotNil(t, got.PermissionMode, name)
		require.Equal(t, types.PermissionModeDefault, *got.PermissionMode, name)
		require.False(t, got.DangerouslySkipPermissions, name)
		require.False(t, got.AllowDangerouslySkipPermissions, name)

		o.Sandboxed = boolp(false)
		got = sm.buildSDKOptions(o)
		if name == "allow-all" {
			require.Equal(t, types.PermissionModeBypassPermissions, *got.PermissionMode, name)
			continue
		}
		require.True(t, got.DangerouslySkipPermissions || (got.PermissionMode != nil && *got.PermissionMode == types.PermissionModeBypassPermissions), name+" unsandboxed stays YOLO")
		if name != "yolo mode" || got.DangerouslySkipPermissions {
			require.True(t, got.AllowDangerouslySkipPermissions, name)
		}
	}
}

// The permission callback answers an Autopilot session at once, without a card
// and without a connected browser.
func TestPermissionCallback_AutopilotAnswersAtOnce(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	fakeClaudeTemp(t)
	home, wd := grantHome(t)
	t.Setenv("HOME", home)
	s := seedSwitchSession(t, sm, "claude", "opus", func(s *Session) {
		s.Options.Sandboxed = boolp(true)
		s.Options.DangerouslySkipPermissions = boolp(true)
		s.Options.WorkingDirectory = &wd
	})
	cb := sm.createPermissionCallback(s)
	ask := func(tool string, in map[string]interface{}) interface{} {
		r, err := cb(context.Background(), tool, in, types.ToolPermissionContext{})
		require.NoError(t, err)
		return r
	}
	require.IsType(t, types.PermissionResultAllow{}, ask("Write", write(filepath.Join(wd, "a.go"))))
	deny := ask("Write", write(filepath.Join(home, "Library", "LaunchAgents", "x.plist")))
	require.IsType(t, types.PermissionResultDeny{}, deny)
	require.IsType(t, types.PermissionResultAllow{}, ask("mcp__eggify-tools__start_worker_session", map[string]interface{}{}))
	require.Empty(t, s.pendingPermissions, "nothing waits for a human")
}

func TestRedTeam_SandboxedYOLO_WriteToolCannotLeaveTheProject(t *testing.T) {
	e := newAutopilotEnv(t, nil)
	for _, target := range []string{
		filepath.Join(e.home, "eggify-yolo-probe.txt"),
		filepath.Join(e.home, ".zshrc"),
		filepath.Join(e.home, ".ssh", "authorized_keys"),
		filepath.Join(e.home, "Library", "LaunchAgents", "eggify-yolo-probe.plist"),
		filepath.Join(e.wd, "rc-link"),
		filepath.Join(e.wd, "agents-link", "p.plist"),
		filepath.Join(e.wd, "src", "..", "..", "..", "x"),
		filepath.Join(e.wd, ".git", "hooks", "post-checkout"),
		filepath.Join(e.wd, ".mcp.json"),
		filepath.Join(e.home, ".claude", "settings.json"),
		filepath.Join(e.home, ".claude", "eggify", "eggify.db"),
	} {
		for _, tool := range []string{"Write", "Edit", "MultiEdit"} {
			require.False(t, autopilotDecide(e.scope, tool, write(target)).Allow, "%s %s", tool, target)
		}
	}
	// ...while the project itself stays writable.
	require.True(t, autopilotDecide(e.scope, "Write", write(filepath.Join(e.wd, "src", "main.go"))).Allow)
}

func TestRedTeam_SandboxedYOLO_WorkerCannotPlantALaunchAgent(t *testing.T) {
	for _, mode := range []string{WorkerModeBuild, WorkerModeReview} {
		e := newAutopilotEnv(t, func(o *SessionOptions) {
			o.Worker = &WorkerConfig{Task: "build", Mode: mode}
			o.AllowDangerouslySkipPermissions = boolp(true)
			o.ReadOnly = mode == WorkerModeReview
		})
		require.True(t, autopilotSession(&SessionOptions{Sandboxed: boolp(true), DangerouslySkipPermissions: boolp(true), Worker: &WorkerConfig{}}))
		plist := filepath.Join(e.home, "Library", "LaunchAgents", "com.evil.plist")
		require.False(t, autopilotDecide(e.scope, "Write", write(plist)).Allow, mode)
		require.False(t, autopilotDecide(e.scope, "Write", write(filepath.Join(e.wd, "agents-link", "com.evil.plist"))).Allow, mode)
		// A command asking to run outside the sandbox is refused too.
		require.False(t, autopilotDecide(e.scope, "Bash", map[string]interface{}{"command": "cp x " + plist, "dangerouslyDisableSandbox": true}).Allow, mode)
	}
}

// Turning the YOLO switch off must turn prompts off the mode too, else a stale
// "yolo" mode keeps them off (and starts an unsandboxed CLI in bypass mode).
func TestToggleYOLO_PermissionModeFollowsTheFlag(t *testing.T) {
	require.Equal(t, "default", *permissionModeForYOLO(strp("yolo"), false))
	require.Equal(t, "default", *permissionModeForYOLO(strp("bypassPermissions"), false))
	require.Equal(t, "default", *permissionModeForYOLO(strp("allow-all"), false))
	require.Equal(t, "default", *permissionModeForYOLO(nil, false))
	require.Equal(t, "read-only", *permissionModeForYOLO(strp("read-only"), false))
	require.Equal(t, "yolo", *permissionModeForYOLO(strp("default"), true))

	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	for _, sandboxed := range []bool{true, false} {
		// Codex path: the reload only saves the options (no CLI to restart).
		s := seedSwitchSession(t, sm, "codex", "gpt-6-astra", func(s *Session) {
			s.Options.Sandboxed = boolp(sandboxed)
			s.Options.PermissionMode = strp("yolo")
			s.Options.DangerouslySkipPermissions = boolp(true)
			s.Options.AllowDangerouslySkipPermissions = boolp(true)
		})
		off := SessionOptions{DangerouslySkipPermissions: boolp(false), AllowDangerouslySkipPermissions: boolp(false)}
		require.NoError(t, sm.ReloadSessionWithOptions(s.ID, off))
		require.Equal(t, "default", *s.Options.PermissionMode)
		require.False(t, promptsOff(&s.Options), "prompts are on after switching off")
		require.False(t, autopilotSession(&s.Options))
		// What a Claude CLI restart would be built from: no bypass.
		got := sm.buildSDKOptions(s.Options)
		require.True(t, got.PermissionMode == nil || *got.PermissionMode == types.PermissionModeDefault)
		require.False(t, got.DangerouslySkipPermissions)

		on := SessionOptions{DangerouslySkipPermissions: boolp(true), AllowDangerouslySkipPermissions: boolp(true)}
		require.NoError(t, sm.ReloadSessionWithOptions(s.ID, on))
		require.Equal(t, "yolo", *s.Options.PermissionMode)
		require.True(t, promptsOff(&s.Options))
		require.Equal(t, sandboxed, autopilotSession(&s.Options))
	}
}

// A trusted project's permissions.allow must not approve file writes on its own.
func TestAutopilotSettingsAskForFileTools(t *testing.T) {
	home, wd := grantHome(t)
	t.Setenv("HOME", home)
	useGrantEnv(t, &grantTestEnv{env: map[string]string{}})
	sm := &SessionManager{}
	ask := func(o SessionOptions) []string {
		opts, err := sm.applyClaudeSessionSettings(types.NewClaudeAgentOptions().WithSettingSources(types.SettingSource("")), &o)
		require.NoError(t, err)
		var s claudeSessionSettings
		require.NoError(t, json.Unmarshal([]byte(*opts.Settings), &s))
		return s.Permissions.Ask
	}
	auto := SessionOptions{WorkingDirectory: &wd, Sandboxed: boolp(true), DangerouslySkipPermissions: boolp(true)}
	require.Subset(t, ask(auto), []string{"Edit", "Write", "MultiEdit", "NotebookEdit", "WebFetch", "WebSearch"})
	prompts := SessionOptions{WorkingDirectory: &wd, Sandboxed: boolp(true)}
	require.NotContains(t, ask(prompts), "Write")
}

func TestSandboxProjectSettingsExcludedCommandsRefused(t *testing.T) {
	dir := t.TempDir()
	writeTrustFile(t, dir, ".claude/settings.json", `{"sandbox":{"excludedCommands":["curl"]}}`)
	require.NotEmpty(t, projectSandboxOverrides(dir))
	sm := &SessionManager{}
	_, err := sm.applyClaudeSessionSettings(types.NewClaudeAgentOptions(), &SessionOptions{WorkingDirectory: &dir, Sandboxed: boolp(true)})
	require.Error(t, err)
}

// While the default is implicit, an old client's blind sandboxed=true does not fail a
// direct session; once the user saved a default it is kept (and the prompt then errors).
func TestImplicitDefaultDoesNotBreakDirectSessions(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	sm.repo = database.NewRepository(database.GetInstance())
	direct := func() SessionOptions {
		return SessionOptions{Provider: strp("ollama"), Model: strp("llama3"), BaseURL: strp("http://localhost:11434/v1"), Sandboxed: boolp(true)}
	}
	o := direct()
	sm.applyConfigDefaults(&o)
	require.False(t, isSandboxed(&o))
	require.True(t, o.sandboxDowngraded)

	// Codex keeps an explicit true; Claude too.
	c := SessionOptions{Provider: strp("codex"), Model: strp("gpt-6-astra"), Sandboxed: boolp(true)}
	sm.applyConfigDefaults(&c)
	require.True(t, isSandboxed(&c))
	require.False(t, c.sandboxDowngraded)

	_, err := SaveSandboxPreferences(sm.repo, SandboxPreferences{DefaultEnabled: true, AllowedDomains: DefaultSandboxAllowedDomains})
	require.NoError(t, err)
	o = direct()
	sm.applyConfigDefaults(&o)
	require.True(t, isSandboxed(&o), "a saved default brings the old behaviour back")
	require.False(t, o.sandboxDowngraded)
}

// Saving only the allowlist keeps the default implicit.
func TestSaveAllowlistOnlyKeepsDefaultImplicit(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	repo := database.NewRepository(database.GetInstance())
	sm.repo = repo
	prefs := LoadSandboxPreferences(repo)
	require.True(t, prefs.DefaultIsImplicit)
	prefs.AllowedDomains = []string{"example.com"}
	_, err := SaveSandboxPreferences(repo, prefs)
	require.NoError(t, err)
	got := LoadSandboxPreferences(repo)
	require.True(t, got.DefaultIsImplicit)
	require.True(t, got.DefaultEnabled)
	require.Equal(t, []string{"example.com"}, got.AllowedDomains)
}

package agents

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	codextypes "github.com/schlunsen/codex-sdk-go/types"
	"github.com/schlunsen/eggify/internal/database"
)

func boolp(b bool) *bool { return &b }

func hasString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// settingsJSON renders the settings the CLI receives and decodes them
// generically, so the test checks the real key names.
func settingsJSON(t *testing.T, s claudeSessionSettings) map[string]any {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBuildClaudeSessionSettings_UnsandboxedHasOnlyDenyRules(t *testing.T) {
	home := "/home/u"
	s := buildClaudeSessionSettings(home, "/work/proj", false, nil)
	for _, want := range []string{
		"Read(//home/u/.claude/eggify/.secret)",
		"Read(//home/u/.claude/eggify/eggify.db)",
		"Read(//home/u/.claude/eggify/mfa_encryption.key)",
		"Read(//home/u/.claude/eggify/desktop-machines.json)",
		"Read(//home/u/.claude/connector_encryption.key)",
		"Read(//home/u/.claude/eggify/certs/**)",
		"Edit(//home/u/.claude/eggify/**)",
		"Read(//home/u/.claude/analytics/.secret)",
		"Edit(//home/u/.claude/analytics/.secret)",
		"Read(//home/u/.codex/auth.json)",
		"Read(//home/u/.claude/.credentials.json)",
	} {
		if !hasString(s.Permissions.Deny, want) {
			t.Errorf("deny rules missing %q: %v", want, s.Permissions.Deny)
		}
	}
	// Eggify's logs stay readable (the docs tell agents to read them).
	if hasString(s.Permissions.Deny, "Read(//home/u/.claude/eggify/**)") {
		t.Error("the whole eggify dir must not be read-denied: agents read ~/.claude/eggify/logs")
	}
	js := settingsJSON(t, s)
	if _, ok := js["sandbox"]; ok {
		t.Error("unsandboxed sessions must not configure the sandbox")
	}
	if len(s.Permissions.Ask) != 0 {
		t.Errorf("unsandboxed sessions get no ask rules, got %v", s.Permissions.Ask)
	}
	for _, r := range s.Permissions.Deny {
		if strings.Contains(r, "/work/proj") {
			t.Errorf("unsandboxed sessions must not restrict the project: %s", r)
		}
	}
}

func TestBuildClaudeSessionSettings_Sandboxed(t *testing.T) {
	home := "/home/u"
	domains := []string{"registry.npmjs.org", "*.example.org"}
	js := settingsJSON(t, buildClaudeSessionSettings(home, "/work/proj", true, domains))

	sb, ok := js["sandbox"].(map[string]any)
	if !ok {
		t.Fatalf("sandbox missing: %v", js)
	}
	for key, want := range map[string]any{
		"enabled": true, "failIfUnavailable": true, "autoAllowBashIfSandboxed": true, "allowUnsandboxedCommands": false,
	} {
		if sb[key] != want {
			t.Errorf("sandbox.%s = %v, want %v", key, sb[key], want)
		}
	}
	net := sb["network"].(map[string]any)
	if net["strictAllowlist"] != true {
		t.Error("network.strictAllowlist must be true (deny, never prompt)")
	}
	gotDomains := net["allowedDomains"].([]any)
	if len(gotDomains) != 2 || gotDomains[0] != "registry.npmjs.org" {
		t.Errorf("allowedDomains = %v", gotDomains)
	}
	fs := sb["filesystem"].(map[string]any)
	denyRead := toStrings(fs["denyRead"])
	if !hasString(denyRead, "/home/u/.claude/eggify/.secret") || !hasString(denyRead, "/home/u/.claude/eggify/eggify.db") ||
		!hasString(denyRead, "/home/u/.claude/connector_encryption.key") || !hasString(denyRead, "/home/u/.codex/auth.json") {
		t.Errorf("denyRead = %v", denyRead)
	}
	if hasString(denyRead, "/home/u/.claude/eggify") {
		t.Errorf("logs must stay readable; denyRead = %v", denyRead)
	}
	denyWrite := toStrings(fs["denyWrite"])
	if !hasString(denyWrite, "/home/u/.claude/eggify") {
		t.Errorf("the eggify dir must not be writable: %v", denyWrite)
	}
	for _, want := range []string{"/work/proj/.mcp.json", "/work/proj/.git/hooks", "/work/proj/.git/config", "/home/u/.claude/settings.json"} {
		if !hasString(denyWrite, want) {
			t.Errorf("denyWrite missing %s: %v", want, denyWrite)
		}
	}
	creds := sb["credentials"].(map[string]any)["envVars"].([]any)
	var names []string
	for _, c := range creds {
		m := c.(map[string]any)
		if m["mode"] != "deny" {
			t.Errorf("env var %v must be denied", m)
		}
		names = append(names, m["name"].(string))
	}
	if !hasString(names, "ANTHROPIC_API_KEY") || !hasString(names, "EGGIFY_API_KEY") {
		t.Errorf("credential env vars = %v", names)
	}

	perms := js["permissions"].(map[string]any)
	ask := toStrings(perms["ask"])
	if !hasString(ask, "WebFetch") || !hasString(ask, "WebSearch") {
		t.Errorf("web tools must be asked in sandboxed sessions: %v", ask)
	}
	deny := toStrings(perms["deny"])
	if !hasString(deny, "Edit(//work/proj/.mcp.json)") || !hasString(deny, "Edit(//work/proj/.git/hooks/**)") {
		t.Errorf("sandboxed deny rules = %v", deny)
	}
}

func TestBuildClaudeSessionSettings_EmptyAllowlistMeansNoNetwork(t *testing.T) {
	js := settingsJSON(t, buildClaudeSessionSettings("/home/u", "/p", true, nil))
	net := js["sandbox"].(map[string]any)["network"].(map[string]any)
	if d, ok := net["allowedDomains"].([]any); !ok || len(d) != 0 {
		t.Errorf("an empty allowlist must be sent as [] (no egress), got %#v", net["allowedDomains"])
	}
}

func toStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		out = append(out, x.(string))
	}
	return out
}

func TestEggifyProtectedPaths_WorkingInsideEggifyDir(t *testing.T) {
	home := t.TempDir()
	eggifyDir := filepath.Join(home, ".claude", "eggify")
	if err := os.MkdirAll(filepath.Join(eggifyDir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}

	secretsDenied := func(p protectedPaths) {
		t.Helper()
		for _, f := range []string{".secret", "eggify.db", "eggify.db-wal", "mfa_encryption.key", "desktop-machines.json"} {
			if !hasString(p.files, filepath.Join(eggifyDir, f)) {
				t.Errorf("%s must stay denied: %v", f, p.files)
			}
		}
		if !hasString(p.files, filepath.Join(home, ".claude", "connector_encryption.key")) {
			t.Errorf("the connector key must be denied: %v", p.files)
		}
		if !hasString(p.dirs, filepath.Join(eggifyDir, "certs")) || !hasString(p.dirs, filepath.Join(eggifyDir, "secrets")) {
			t.Errorf("certs and secrets dirs must be denied: %v", p.dirs)
		}
		if hasString(p.dirs, eggifyDir) {
			t.Errorf("the whole eggify dir must not be read-denied (logs stay readable): %v", p.dirs)
		}
	}

	// Normal project: secrets denied, the rest of the eggify dir read-only.
	p := eggifyProtectedPaths(home, filepath.Join(home, "project"))
	secretsDenied(p)
	if !hasString(p.editDirs, eggifyDir) {
		t.Errorf("eggify dir must not be editable: %v", p.editDirs)
	}

	// Working on Eggify's own config: secrets still denied, the dir editable.
	p = eggifyProtectedPaths(home, filepath.Join(eggifyDir, "logs"))
	secretsDenied(p)
	if hasString(p.editDirs, eggifyDir) {
		t.Error("working inside ~/.claude/eggify must be able to edit it")
	}
}

func TestNormalizeSandboxDomains(t *testing.T) {
	got, err := NormalizeSandboxDomains([]string{" GitHub.com ", "github.com", "*.Example.org", "", "pypi.org."})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"*.example.org", "github.com", "pypi.org"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, bad := range []string{"https://github.com", "github.com/x", "*", "*.com", "10.0.0.1", "localhost", "a..b", "ex ample.com", "host:443"} {
		if _, err := NormalizeSandboxDomains([]string{bad}); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestSandboxWebToolDecision(t *testing.T) {
	allowed := []string{"docs.example.com", "*.github.io"}
	cases := []struct {
		name               string
		sandboxed          bool
		tool, url          string
		wantHandled, allow bool
	}{
		{"unsandboxed passes through", false, "WebFetch", "https://evil.test", false, false},
		{"other tools pass through", true, "Bash", "", false, false},
		{"allowlisted host", true, "WebFetch", "https://docs.example.com/a", true, true},
		{"wildcard subdomain", true, "WebFetch", "https://me.github.io/x", true, true},
		{"wildcard does not match apex", true, "WebFetch", "https://github.io/", true, false},
		{"suffix trick", true, "WebFetch", "https://docs.example.com.evil.test/", true, false},
		{"unlisted host asks", true, "WebFetch", "https://evil.test/?q=secret", true, false},
		{"non-http scheme asks", true, "WebFetch", "file:///etc/passwd", true, false},
		{"search always asks", true, "WebSearch", "", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			handled, allow := sandboxWebToolDecision(c.sandboxed, c.tool, map[string]interface{}{"url": c.url}, allowed)
			if handled != c.wantHandled || allow != c.allow {
				t.Errorf("got handled=%v allow=%v, want %v %v", handled, allow, c.wantHandled, c.allow)
			}
		})
	}
}

func TestProjectSandboxOverrides(t *testing.T) {
	dir := t.TempDir()
	if got := projectSandboxOverrides(dir); len(got) != 0 {
		t.Fatalf("no settings → no overrides, got %v", got)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Eggify's own settings.local.json (permissions only) is fine.
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.local.json"), []byte(`{"permissions":{"allow":["Bash(git:*)"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := projectSandboxOverrides(dir); len(got) != 0 {
		t.Fatalf("permissions-only settings must not count, got %v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(`{"sandbox":{"filesystem":{"allowRead":["~/.claude/eggify"]},"network":{"allowedDomains":["evil.test"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := projectSandboxOverrides(dir)
	if len(got) != 2 {
		t.Fatalf("want allowRead + allowedDomains, got %v", got)
	}

	sm := &SessionManager{}
	options := SessionOptions{WorkingDirectory: &dir, Sandboxed: boolp(true)}
	if _, err := sm.applyClaudeSessionSettings(types.NewClaudeAgentOptions(), &options); err == nil || !strings.Contains(err.Error(), "allowRead") {
		t.Errorf("sandboxed session must refuse widening project settings, got %v", err)
	}
	// Untrusted folder: its settings files are not loaded, so they cannot
	// widen anything and the session may start.
	untrusted := types.NewClaudeAgentOptions().WithSettingSources(types.SettingSource("")).WithStrictMcpConfig(true)
	opts, err := sm.applyClaudeSessionSettings(untrusted, &options)
	if err != nil {
		t.Errorf("untrusted folder must not be refused: %v", err)
	} else if opts.Settings == nil {
		t.Error("flag settings must still be passed in an untrusted folder")
	}
	// Unsandboxed sessions are unaffected by project sandbox keys.
	options.Sandboxed = boolp(false)
	if _, err := sm.applyClaudeSessionSettings(types.NewClaudeAgentOptions(), &options); err != nil {
		t.Errorf("unsandboxed session: %v", err)
	}
}

func TestApplyClaudeSessionSettings_SetsSettingsFlag(t *testing.T) {
	dir := t.TempDir()
	sm := &SessionManager{}
	for _, sandboxed := range []bool{false, true} {
		options := SessionOptions{WorkingDirectory: &dir, Sandboxed: boolp(sandboxed)}
		opts, err := sm.applyClaudeSessionSettings(types.NewClaudeAgentOptions(), &options)
		if err != nil {
			t.Fatal(err)
		}
		if opts.Settings == nil {
			t.Fatal("settings JSON must be passed with --settings")
		}
		var parsed claudeSessionSettings
		if err := json.Unmarshal([]byte(*opts.Settings), &parsed); err != nil {
			t.Fatalf("settings are not valid JSON: %v", err)
		}
		if (parsed.Sandbox != nil) != sandboxed {
			t.Errorf("sandboxed=%v but sandbox present=%v", sandboxed, parsed.Sandbox != nil)
		}
		if sandboxed && len(parsed.Sandbox.Network.AllowedDomains) != len(DefaultSandboxAllowedDomains) {
			t.Errorf("without a DB the default allowlist applies, got %v", parsed.Sandbox.Network.AllowedDomains)
		}
		if opts.Sandbox != nil {
			t.Error("the SDK's --sandbox flag is unknown to Claude Code 2.1.280; use --settings")
		}
	}
}

func TestCodexSandboxMode_SandboxedNeverFullAccess(t *testing.T) {
	yolo := SessionOptions{DangerouslySkipPermissions: boolp(true), Sandboxed: boolp(true)}
	if got := codexSandboxMode(&yolo); got != codextypes.SandboxWorkspaceWrite {
		t.Errorf("sandboxed YOLO → %s, want workspace-write", got)
	}
	allowAll := SessionOptions{PermissionMode: strp("allow-all"), Sandboxed: boolp(true)}
	if got := codexSandboxMode(&allowAll); got != codextypes.SandboxWorkspaceWrite {
		t.Errorf("sandboxed allow-all → %s, want workspace-write", got)
	}
	ro := SessionOptions{PermissionMode: strp("read-only"), Sandboxed: boolp(true)}
	if got := codexSandboxMode(&ro); got != codextypes.SandboxReadOnly {
		t.Errorf("sandboxed read-only → %s", got)
	}
	// Unsandboxed behaviour is unchanged.
	if got := codexSandboxMode(&SessionOptions{DangerouslySkipPermissions: boolp(true)}); got != codextypes.SandboxDangerFullAccess {
		t.Errorf("unsandboxed YOLO → %s, want danger-full-access", got)
	}
}

func TestCodexSandboxConfig(t *testing.T) {
	cfg := codexSandboxConfig("/home/u", "/work/proj", nil)
	if cfg["default_permissions"] != codexSandboxProfile {
		t.Errorf("default_permissions = %v", cfg["default_permissions"])
	}
	profile := cfg["permissions."+codexSandboxProfile].(map[string]any)
	if profile["extends"] != ":workspace" {
		t.Errorf("extends = %v", profile["extends"])
	}
	if profile["network"].(map[string]any)["enabled"] != false {
		t.Error("network must be off for sandboxed Codex sessions")
	}
	fs := profile["filesystem"].(map[string]any)
	for path, mode := range map[string]string{
		"/home/u/.claude/eggify":           "read",
		"/home/u/.claude/eggify/.secret":   "deny",
		"/home/u/.claude/eggify/eggify.db": "deny",
		"/home/u/.codex/auth.json":         "deny",
		"/work/proj/.mcp.json":             "read",
		"/work/proj/.git/hooks":            "read",
		"/home/u/.claude/settings.json":    "read",
	} {
		if fs[path] != mode {
			t.Errorf("filesystem[%s] = %v, want %s", path, fs[path], mode)
		}
	}
	if _, ok := cfg["sandbox_mode"]; ok {
		t.Error("sandbox_mode conflicts with default_permissions")
	}
}

func TestSandboxedOptionsPersistRoundTrip(t *testing.T) {
	in := SessionOptions{Sandboxed: boolp(true)}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"sandboxed":true`) {
		t.Errorf("options JSON = %s", data)
	}
	var out SessionOptions
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !isSandboxed(&out) {
		t.Error("sandboxed flag lost in round trip")
	}
	// Sessions stored before this feature have no flag and stay unsandboxed.
	var legacy SessionOptions
	_ = json.Unmarshal([]byte(`{"provider":"claude"}`), &legacy)
	if isSandboxed(&legacy) {
		t.Error("legacy sessions must not become sandboxed")
	}
}

func TestSandboxPreferencesAndDefaults(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	repo := database.NewRepository(database.GetInstance())
	sm.repo = repo

	prefs := sm.SandboxPreferences()
	if !prefs.DefaultEnabled || !prefs.DefaultIsImplicit || len(prefs.AllowedDomains) != len(DefaultSandboxAllowedDomains) {
		t.Fatalf("fresh install is sandboxed by default (Autopilot): %+v", prefs)
	}

	// Never chosen: Claude sessions are stored sandboxed, other runtimes keep their old behaviour.
	var opts SessionOptions
	sm.applyConfigDefaults(&opts)
	if !isSandboxed(&opts) {
		t.Fatalf("implicit default on → sandboxed, got %v", opts.Sandboxed)
	}
	codex := SessionOptions{Provider: strp("codex"), Model: strp("gpt-6-astra")}
	sm.applyConfigDefaults(&codex)
	if codex.Sandboxed == nil || *codex.Sandboxed {
		t.Fatalf("implicit default leaves Codex unsandboxed, got %v", codex.Sandboxed)
	}

	// An explicit saved false is respected: new sessions are stored with an explicit false.
	if _, err := SaveSandboxPreferences(repo, SandboxPreferences{DefaultEnabled: false, AllowedDomains: DefaultSandboxAllowedDomains}); err != nil {
		t.Fatal(err)
	}
	if got := sm.SandboxPreferences(); got.DefaultEnabled || got.DefaultIsImplicit {
		t.Fatalf("saved false: %+v", got)
	}
	opts = SessionOptions{}
	sm.applyConfigDefaults(&opts)
	if opts.Sandboxed == nil || *opts.Sandboxed {
		t.Fatalf("default off → explicit false, got %v", opts.Sandboxed)
	}

	saved, err := SaveSandboxPreferences(repo, SandboxPreferences{DefaultEnabled: true, AllowedDomains: []string{"Example.com", "example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(saved.AllowedDomains, ",") != "example.com" {
		t.Errorf("saved domains = %v", saved.AllowedDomains)
	}
	if got := sm.SandboxPreferences(); !got.DefaultEnabled || strings.Join(got.AllowedDomains, ",") != "example.com" {
		t.Errorf("reloaded prefs = %+v", got)
	}

	opts = SessionOptions{}
	sm.applyConfigDefaults(&opts)
	if !isSandboxed(&opts) {
		t.Error("default on → new sessions sandboxed")
	}
	// An explicit choice wins over the default.
	opts = SessionOptions{Sandboxed: boolp(false)}
	sm.applyConfigDefaults(&opts)
	if isSandboxed(&opts) {
		t.Error("explicit opt-out must be kept")
	}

	if _, err := SaveSandboxPreferences(repo, SandboxPreferences{AllowedDomains: []string{"https://evil.test/x"}}); err == nil {
		t.Error("invalid domains must be rejected")
	}
}

func TestChangeSessionModel_SandboxedRefusesDirectPath(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()

	s := seedSwitchSession(t, sm, "claude", "sonnet", func(sess *Session) {
		sess.Options.Sandboxed = boolp(true)
	})
	_, err := sm.ChangeSessionModel(s.ID, "ollama", "llama3", strp("http://localhost:11434/v1"))
	if !errors.Is(err, ErrSandboxUnsupported) {
		t.Fatalf("want ErrSandboxUnsupported, got %v", err)
	}
	if s.ModelName != "sonnet" {
		t.Errorf("refused switch must leave the session untouched, model=%s", s.ModelName)
	}
	// Claude → Codex stays allowed: Codex has its own sandbox profile.
	if _, err := sm.ChangeSessionModel(s.ID, "codex", "gpt-5-codex", nil); err != nil {
		t.Errorf("sandboxed switch to codex: %v", err)
	}
}

func TestSandboxOptOutAction(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	repo := database.NewRepository(database.GetInstance())
	sm.repo = repo

	// Never chosen: the default is on, so opting out needs a user.
	if got := sm.SandboxOptOutAction(&SessionOptions{Sandboxed: boolp(false)}); got != SandboxOptOutAction {
		t.Errorf("implicit default on: %q", got)
	}
	// Saved off: opting out is ordinary.
	if _, err := SaveSandboxPreferences(repo, SandboxPreferences{DefaultEnabled: false, AllowedDomains: DefaultSandboxAllowedDomains}); err != nil {
		t.Fatal(err)
	}
	if got := sm.SandboxOptOutAction(&SessionOptions{Sandboxed: boolp(false)}); got != "" {
		t.Errorf("default off: %q", got)
	}
	if _, err := SaveSandboxPreferences(repo, SandboxPreferences{DefaultEnabled: true, AllowedDomains: DefaultSandboxAllowedDomains}); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		opts *SessionOptions
		want string
	}{
		"explicit off":    {&SessionOptions{Sandboxed: boolp(false)}, SandboxOptOutAction},
		"explicit on":     {&SessionOptions{Sandboxed: boolp(true)}, ""},
		"follows default": {&SessionOptions{}, ""},
		"nil":             {nil, ""},
	} {
		if got := sm.SandboxOptOutAction(c.opts); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

// An agent holding the API key must not escape its sandbox by creating an
// unsandboxed session while sandboxing is the default.
func TestCreateUnsandboxedSessionNeedsUser(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	repo := database.NewRepository(database.GetInstance())
	w.sm.repo = repo
	if _, err := SaveSandboxPreferences(repo, SandboxPreferences{DefaultEnabled: true, AllowedDomains: DefaultSandboxAllowedDomains}); err != nil {
		t.Fatal(err)
	}

	agent := w.dial(t, "")
	_ = agent.WriteJSON(map[string]any{"type": "create_session", "session_id": uuid.NewString(), "options": map[string]any{"sandboxed": false}})
	got := w.expect(t, agent, func(m map[string]any) bool { return m["type"] == "error" })
	if !isUserRequired(got) {
		t.Fatalf("API-key create_session with sandboxed=false: got %v, want user_required", got)
	}
	if n := len(w.sm.ListSessions()); n != 1 {
		t.Fatalf("unsandboxed session created from an API-key connection: %d sessions", n)
	}

	// Following the default is fine for the API key, and the session is sandboxed.
	followID := uuid.New()
	_ = agent.WriteJSON(map[string]any{"type": "create_session", "session_id": followID.String(), "options": map[string]any{}})
	w.expect(t, agent, func(m map[string]any) bool { return m["type"] == string(MessageTypeSessionCreated) })
	s, err := w.sm.GetSession(followID)
	if err != nil || !isSandboxed(&s.Options) {
		t.Fatalf("session following the default must be sandboxed: %v %+v", err, s)
	}

	// A signed-in user may opt out.
	user := w.dial(t, "alice")
	_ = user.WriteJSON(map[string]any{"type": "create_session", "session_id": uuid.NewString(), "options": map[string]any{"sandboxed": false}})
	w.expect(t, user, func(m map[string]any) bool { return m["type"] == string(MessageTypeSessionCreated) })
}

func TestIsReservedSettingKey(t *testing.T) {
	for key, want := range map[string]bool{
		SandboxSettingDefaultKey: true, SandboxSettingDomainsKey: true, " Sandbox.anything": true,
		"theme": false, "sandboxed_ui": false,
	} {
		if got := IsReservedSettingKey(key); got != want {
			t.Errorf("IsReservedSettingKey(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestDesktopWebViewStores(t *testing.T) {
	dirs, files := desktopWebViewStores("darwin", "/Users/u")
	for _, want := range []string{
		"/Users/u/Library/HTTPStorages/com.eggify.desktop.wails.binarycookies",
		"/Users/u/Library/Cookies/com.eggify.desktop.wails.binarycookies",
		"/Users/u/Library/HTTPStorages/com.eggify.desktop.binarycookies",
		"/Users/u/Library/HTTPStorages/eggify-desktop.binarycookies",
	} {
		if !hasString(files, want) {
			t.Errorf("files missing %s: %v", want, files)
		}
	}
	for _, want := range []string{
		"/Users/u/Library/HTTPStorages/com.eggify.desktop.wails",
		"/Users/u/Library/WebKit/com.eggify.desktop.wails",
		"/Users/u/Library/Caches/com.eggify.desktop.wails",
		"/Users/u/Library/WebKit/com.eggify.desktop",
		"/Users/u/Library/WebKit/eggify-desktop",
	} {
		if !hasString(dirs, want) {
			t.Errorf("dirs missing %s: %v", want, dirs)
		}
	}
	// Never a whole shared folder: other apps' data is not Eggify's to deny.
	for _, d := range dirs {
		if filepath.Dir(filepath.Dir(d)) != "/Users/u/Library" {
			t.Errorf("unexpected dir %s", d)
		}
	}
	for _, goos := range []string{"linux", "windows"} {
		if d, f := desktopWebViewStores(goos, "/home/u"); len(d) != 0 || len(f) != 0 {
			t.Errorf("%s gets no WebKit entries, got %v %v", goos, d, f)
		}
	}
	if d, f := desktopWebViewStores("darwin", ""); len(d) != 0 || len(f) != 0 {
		t.Errorf("no home, no entries: %v %v", d, f)
	}
}

// The desktop app's WKWebView keeps the user's session_token cookie in
// ~/Library/HTTPStorages/<bundle id>.binarycookies: every list that protects
// the other secrets must cover it and the website data next to it.
func TestRedTeam_Sandbox_DesktopCookieStoreIsProtected(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("WebKit stores exist on macOS only")
	}
	const (
		home    = "/Users/u"
		cookies = "/Users/u/Library/HTTPStorages/com.eggify.desktop.wails.binarycookies"
		data    = "/Users/u/Library/WebKit/com.eggify.desktop.wails"
		caches  = "/Users/u/Library/Caches/com.eggify.desktop.wails"
	)

	// Every Claude session, sandboxed or not: the file tools are refused.
	for _, sandboxed := range []bool{false, true} {
		s := buildClaudeSessionSettings(home, "/work/proj", sandboxed, nil)
		for _, want := range []string{
			"Read(/" + cookies + ")", "Edit(/" + cookies + ")",
			"Read(/" + data + "/**)", "Edit(/" + data + "/**)",
			"Read(/" + caches + "/**)", "Edit(/" + caches + "/**)",
		} {
			if !hasString(s.Permissions.Deny, want) {
				t.Errorf("sandboxed=%v: deny rules missing %q", sandboxed, want)
			}
		}
		if sandboxed {
			for _, want := range []string{cookies, data, caches} {
				if !hasString(s.Sandbox.Filesystem.DenyRead, want) {
					t.Errorf("sandbox denyRead missing %s: %v", want, s.Sandbox.Filesystem.DenyRead)
				}
			}
		}
	}

	// Sandboxed Codex sessions: denied in the permission profile.
	cfg := codexSandboxConfig(home, "/work/proj", nil)
	fs := cfg["permissions."+codexSandboxProfile].(map[string]any)["filesystem"].(map[string]any)
	for _, p := range []string{cookies, data, caches} {
		if fs[p] != "deny" {
			t.Errorf("codex filesystem[%s] = %v, want deny", p, fs[p])
		}
	}

	// Working inside ~/.claude/eggify lifts the edit ban there, never these.
	p := eggifyProtectedPaths(home, home+"/.claude/eggify/logs")
	if !hasString(p.files, cookies) || !hasString(p.dirs, data) {
		t.Errorf("cookie store must stay denied: %v %v", p.files, p.dirs)
	}
}

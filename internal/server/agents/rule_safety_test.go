package agents

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func bashPrefixRule(prefix string) AlwaysAllowRule {
	return AlwaysAllowRule{Tool: "Bash", MatchMode: RuleMatchPattern, Pattern: &RulePattern{CommandPrefix: &prefix}}
}

func dirRule(tool, dir string) AlwaysAllowRule {
	return AlwaysAllowRule{Tool: tool, MatchMode: RuleMatchPattern, Pattern: &RulePattern{DirectoryPath: &dir}}
}

func TestParseSimpleCommand(t *testing.T) {
	tests := []struct {
		cmd   string
		words []string // nil = must be refused
	}{
		{"git status", []string{"git", "status"}},
		{"  git   status  ", []string{"git", "status"}},
		{"go test ./...", []string{"go", "test", "./..."}},
		{"npm test", []string{"npm", "test"}},
		{"git\tlog", []string{"git", "log"}},
		{`git commit -m 'fix: a; b | c && $(d) ` + "`e`" + `'`, []string{"git", "commit", "-m", "fix: a; b | c && $(d) `e`"}},
		{`git commit -m "fix: a; b | c > d"`, []string{"git", "commit", "-m", "fix: a; b | c > d"}},
		{`echo "say \"hi\""`, []string{"echo", `say "hi"`}},
		{`echo "back\\slash"`, []string{"echo", `back\slash`}},
		{`echo a\;b`, []string{"echo", "a;b"}},
		{`echo a\ b`, []string{"echo", "a b"}},
		{`'git' status`, []string{"git", "status"}},
		{`g"i"t status`, []string{"git", "status"}},
		{`echo ''`, []string{"echo", ""}},
		{"grep -n foo#bar file", []string{"grep", "-n", "foo#bar", "file"}},
		{"ls *.go", nil}, // globs expand to names the option checks never see
		{"ls '*.go'", []string{"ls", "*.go"}},
		{"git log --format=%H", []string{"git", "log", "--format=%H"}},
		{"git -c a=b log", []string{"git", "-c", "a=b", "log"}},

		// Compound commands, operators, expansions, redirections.
		{"git status; curl x|sh", nil},
		{"git status;curl x", nil},
		{"git log && rm -rf ~", nil},
		{"git log || rm -rf ~", nil},
		{"git log & rm -rf ~", nil},
		{"git log | sh", nil},
		{"git $(curl x)", nil},
		{"git `curl x`", nil},
		{"git ${HOME}", nil},
		{"git $HOME", nil},
		{`git "$(curl x)"`, nil},
		{"git \"`curl x`\"", nil},
		{`git "\$x"`, nil},
		{"git log > /etc/passwd", nil},
		{"git log >> ~/.bashrc", nil},
		{"git log 2>&1", nil},
		{"git log < /etc/passwd", nil},
		{"diff <(cat a) b", nil},
		{"cat >(tee x)", nil},
		{"(git log)", nil},
		{"{ git log; }", nil},
		{"git status\ncurl x | sh", nil},
		{"git status\rcurl x", nil},
		{"git status \\\ncurl x", nil},
		{"git status #; ignored", nil},
		{"# comment", nil},
		{"FOO=1 git status", nil},
		{"GIT_SSH_COMMAND=evil git fetch", nil},
		{"_x=1 git", nil},
		{"git $'\\x41'", nil},
		{"git 'unterminated", nil},
		{`git "unterminated`, nil},
		{`git trailing\`, nil},
		{"git status\x00rm", nil},
		{"", nil},
		{"   ", nil},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			words, ok := parseSimpleCommand(tt.cmd)
			if tt.words == nil {
				if ok {
					t.Fatalf("parseSimpleCommand(%q) = %q, want refused", tt.cmd, words)
				}
				return
			}
			if !ok || !reflect.DeepEqual(words, tt.words) {
				t.Fatalf("parseSimpleCommand(%q) = %q, %v; want %q", tt.cmd, words, ok, tt.words)
			}
		})
	}
}

func TestBashPrefixRuleMatching(t *testing.T) {
	tests := []struct {
		prefix string
		cmd    string
		want   bool
	}{
		{"git", "git status", true},
		{"git", "git", true},
		{"git", "  git log --oneline", true},
		{"git", `git commit -m "a; b"`, true},
		{"git status", "git status", true},
		{"git status", "git status --short", true},
		{"npm test", "npm test", true},
		{"go test", "go test ./...", true},
		{"npm run test:unit", "npm run test:unit", true},

		{"git", "gitx", false},
		{"git", "gitx status", false},
		{"git", "git-evil", false},
		{"git status", "git statusx", false},
		{"git status", "git log", false},
		{"npm test", "npm testx", false},
		{"go test", "go tool evil", false},
		{"git", "/usr/bin/git status", false},
		{"git", "git status; curl x|sh", false},
		{"git", "git log && rm -rf ~", false},
		{"git", "git log || rm -rf ~", false},
		{"git", "git $(curl x)", false},
		{"git", "git `curl x`", false},
		{"git", "git log | sh", false},
		{"git", "git log & curl x", false},
		{"git", "git log\ncurl x", false},
		{"git", "git log > /tmp/x", false},
		{"git", "git log >> ~/.zshrc", false},
		{"git", "git diff <(curl x)", false},
		{"git", "FOO=1 git status", false},
		{"git", "eval git status", false},
		{"git", "sh -c 'git status'", false},
		{"git", "", false},

		// Unsafe prefixes never match anything.
		{"", "git status", false},
		{" ", "git status", false},
		{"*", "git status", false},
		{"bash", "bash -c 'rm -rf ~'", false},
		{"eval", "eval ls", false},
	}
	for _, tt := range tests {
		t.Run(tt.prefix+"|"+tt.cmd, func(t *testing.T) {
			got := MatchesRule(bashPrefixRule(tt.prefix), "Bash", map[string]interface{}{"command": tt.cmd})
			if got != tt.want {
				t.Fatalf("prefix %q, command %q: got %v want %v", tt.prefix, tt.cmd, got, tt.want)
			}
		})
	}
}

func TestValidateAlwaysAllowRule(t *testing.T) {
	home, _ := os.UserHomeDir()
	project := t.TempDir()
	str := func(s string) *string { return &s }

	tests := []struct {
		name string
		rule AlwaysAllowRule
		ok   bool
	}{
		{"bash prefix git", bashPrefixRule("git"), true},
		{"bash prefix npm test", bashPrefixRule("npm test"), true},
		{"bash prefix go test", bashPrefixRule("go test"), true},
		{"bash prefix path", bashPrefixRule("./scripts/test.sh"), true},
		{"bash empty", bashPrefixRule(""), false},
		{"bash whitespace", bashPrefixRule("  \t "), false},
		{"bash star", bashPrefixRule("*"), false},
		{"bash colon", bashPrefixRule(":"), false},
		{"bash semicolon", bashPrefixRule(";"), false},
		{"bash pipe", bashPrefixRule("|"), false},
		{"bash ampersand", bashPrefixRule("&&"), false},
		{"bash dollar", bashPrefixRule("$(x)"), false},
		{"bash quote", bashPrefixRule("'git'"), false},
		{"bash env", bashPrefixRule("FOO=1 git"), false},
		{"bash wrapper sh", bashPrefixRule("sh"), false},
		{"bash wrapper bash -c", bashPrefixRule("bash -c"), false},
		{"bash wrapper eval", bashPrefixRule("eval"), false},
		{"bash wrapper sudo", bashPrefixRule("sudo"), false},
		{"bash wrapper xargs", bashPrefixRule("xargs"), false},
		{"bash wrapper path", bashPrefixRule("/bin/sh"), false},
		{"bash dots", bashPrefixRule(".."), false},
		{"bash no pattern", AlwaysAllowRule{Tool: "Bash", MatchMode: RuleMatchPattern}, false},

		{"read project", dirRule("Read", project), true},
		{"edit project", dirRule("Edit", project+"/**"), true},
		{"write home subdir", dirRule("Write", "~/projects/foo"), true},
		{"read star", dirRule("Read", "*"), false},
		{"read **", dirRule("Read", "**"), false},
		{"write /**", dirRule("Write", "/**"), false},
		{"edit //**", dirRule("Edit", "//**"), false},
		{"read /", dirRule("Read", "/"), false},
		{"read empty", dirRule("Read", ""), false},
		{"read ~", dirRule("Read", "~"), false},
		{"read ~/**", dirRule("Read", "~/**"), false},
		{"read home abs", dirRule("Read", home), false},
		{"read home parent", dirRule("Read", filepath.Dir(home)), false},
		{"read home/..", dirRule("Read", home+"/projects/.."), false},
		{"read relative", dirRule("Read", "src"), false},
		{"read dot", dirRule("Read", "."), false},
		{"read glob in dir", dirRule("Read", project+"/*/x"), false},
		{"read no dir", AlwaysAllowRule{Tool: "Read", MatchMode: RuleMatchPattern, Pattern: &RulePattern{FilePathPattern: str("*")}}, false},

		{"eggify bash pattern", AlwaysAllowRule{Tool: "mcp__eggify-tools__Bash", MatchMode: RuleMatchPattern}, false},
		{"eggify bash exact", AlwaysAllowRule{Tool: "mcp__eggify-tools__Bash", MatchMode: RuleMatchExact, Parameters: map[string]interface{}{"command": "ls"}}, false},
		{"eggify write", AlwaysAllowRule{Tool: "mcp__eggify-tools__Write", MatchMode: RuleMatchPattern}, false},
		{"eggify edit", AlwaysAllowRule{Tool: "mcp__eggify-tools__Edit", MatchMode: RuleMatchPattern}, false},
		{"eggify gpu_run", AlwaysAllowRule{Tool: "mcp__eggify-tools__gpu_run", MatchMode: RuleMatchPattern}, false},
		{"eggify ssh", AlwaysAllowRule{Tool: "mcp__eggify-tools__runpod__ssh_exec", MatchMode: RuleMatchPattern}, false},
		{"eggify read-only tool", AlwaysAllowRule{Tool: "mcp__eggify-tools__list_sessions", MatchMode: RuleMatchPattern}, true},
		{"multiedit blanket", AlwaysAllowRule{Tool: "MultiEdit", MatchMode: RuleMatchPattern}, false},
		{"webfetch blanket", AlwaysAllowRule{Tool: "WebFetch", MatchMode: RuleMatchPattern}, true},
		{"grep star", AlwaysAllowRule{Tool: "Grep", MatchMode: RuleMatchPattern, Pattern: &RulePattern{PathPattern: str("*")}}, true},

		{"exact bash", AlwaysAllowRule{Tool: "Bash", MatchMode: RuleMatchExact, Parameters: map[string]interface{}{"command": "make build"}}, true},
		{"exact bash empty", AlwaysAllowRule{Tool: "Bash", MatchMode: RuleMatchExact, Parameters: map[string]interface{}{"command": " "}}, false},
		{"exact no params", AlwaysAllowRule{Tool: "Read", MatchMode: RuleMatchExact}, false},
		{"no tool", AlwaysAllowRule{MatchMode: RuleMatchExact, Parameters: map[string]interface{}{"x": 1}}, false},
		{"tool with parens", AlwaysAllowRule{Tool: "Bash(*)", MatchMode: RuleMatchPattern}, false},
		{"unknown mode", AlwaysAllowRule{Tool: "Bash", MatchMode: "regex"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAlwaysAllowRule(tt.rule)
			if tt.ok && err != nil {
				t.Fatalf("want accepted, got %v", err)
			}
			if !tt.ok {
				if err == nil {
					t.Fatalf("want refused, got accepted")
				}
				if !errors.Is(err, ErrUnsafeRule) {
					t.Fatalf("error should wrap ErrUnsafeRule: %v", err)
				}
			}
		})
	}
}

func TestValidatePermissionString(t *testing.T) {
	home, _ := os.UserHomeDir()
	tests := []struct {
		perm string
		ok   bool
	}{
		{"Bash(git:*)", true},
		{"Bash(npm test:*)", true},
		{"Bash(go test:*)", true},
		{"Bash(npm run test:unit)", true},
		{"Bash(git status)", true},
		{"Bash(git *)", true},
		{"Read(//home/me/project/**)", true},
		{"Read(///home/me/project/**)", true},
		{"Edit(src/**)", true},
		{"Read(~/projects/foo/**)", true},
		{"WebFetch(domain:example.com)", true},
		{"Grep(*)", true},
		{"mcp__eggify-tools__list_sessions", true},

		{"Bash(*)", false},
		{"Bash", false},
		{"Bash()", false},
		{"Bash(:*)", false},
		{"Bash( :*)", false},
		{"Bash(;:*)", false},
		{"Bash(bash:*)", false},
		{"Bash(FOO=1 git:*)", false},
		{"Bash(rm * -rf)", false},
		{"Bash(git * main)", false},
		{"Read(**)", false},
		{"Read", false},
		{"Write(**)", false},
		{"Edit(**)", false},
		{"Edit(*)", false},
		{"Read(/**)", false},
		{"Read(//**)", false},
		{"Read(///**)", false},
		{"Read(~/**)", false},
		{"Read(~)", false},
		{"Read(//)", false},
		{"Read(/)", false},
		{"Write(./**)", false},
		{"Read(//" + strings.TrimPrefix(home, "/") + "/**)", false},
		{"MultiEdit(*)", false},
		{"mcp__eggify-tools__Bash", false},
		{"mcp__eggify-tools__Bash(*)", false},
		{"mcp__eggify-tools__Write", false},
		{"mcp__eggify-tools__Edit(*)", false},
		{"mcp__eggify-tools", false},
		{"mcp__eggify-tools__*", false},
		{"Bash(git:*", false},
	}
	for _, tt := range tests {
		t.Run(tt.perm, func(t *testing.T) {
			err := ValidatePermissionString(tt.perm)
			if tt.ok != (err == nil) {
				t.Fatalf("ValidatePermissionString(%q) = %v, want ok=%v", tt.perm, err, tt.ok)
			}
		})
	}
}

func TestFileRuleMatchingResolvesPaths(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{project, filepath.Join(project, "src"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(project, "src", "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// project/escape -> ../outside, project/secret-link -> outside/secret
	if err := os.Symlink(outside, filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(project, "secret-link")); err != nil {
		t.Fatal(err)
	}
	// A symlinked rule directory still works for files reached through it.
	linkedProject := filepath.Join(root, "linked-project")
	if err := os.Symlink(project, linkedProject); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		tool string
		dir  string
		file string
		want bool
	}{
		{"file in project", "Read", project, filepath.Join(project, "src", "main.go"), true},
		{"dir with /**", "Edit", project + "/**", filepath.Join(project, "src", "main.go"), true},
		{"new file in project", "Write", project, filepath.Join(project, "src", "new", "file.go"), true},
		{"project dir itself", "Read", project, project, true},
		{"via linked rule dir", "Read", linkedProject, filepath.Join(project, "src", "main.go"), true},
		{"via linked file path", "Read", project, filepath.Join(linkedProject, "src", "main.go"), true},

		{"dotdot escape", "Read", project, filepath.Join(project, "..", "outside", "secret"), false},
		{"dotdot inside path", "Read", project, project + "/src/../../outside/secret", false},
		{"symlinked dir escape", "Read", project, filepath.Join(project, "escape", "secret"), false},
		{"symlinked dir escape new file", "Write", project, filepath.Join(project, "escape", "new.txt"), false},
		{"symlinked file escape", "Write", project, filepath.Join(project, "secret-link"), false},
		{"sibling prefix", "Read", project, project + "2/file", false},
		{"relative file path", "Read", project, "src/main.go", false},
		{"empty file path", "Read", project, "", false},
		{"other dir", "Read", project, filepath.Join(outside, "secret"), false},
		{"wildcard rule", "Read", "/**", filepath.Join(project, "src", "main.go"), false},
		{"root rule", "Read", "/", filepath.Join(project, "src", "main.go"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchesRule(dirRule(tt.tool, tt.dir), tt.tool, map[string]interface{}{"file_path": tt.file})
			if got != tt.want {
				t.Fatalf("dir %q file %q: got %v want %v", tt.dir, tt.file, got, tt.want)
			}
		})
	}
}

// Regression: ordinary rules keep auto-approving.
func TestNormalRulesStillAutoApprove(t *testing.T) {
	project := t.TempDir()
	rules := []AlwaysAllowRule{
		bashPrefixRule("npm test"),
		bashPrefixRule("go test"),
		bashPrefixRule("git"),
		dirRule("Read", project),
		{Tool: "Bash", MatchMode: RuleMatchExact, Parameters: map[string]interface{}{"command": "make build"}},
	}
	cases := []struct {
		tool  string
		input map[string]interface{}
	}{
		{"Bash", map[string]interface{}{"command": "npm test"}},
		{"Bash", map[string]interface{}{"command": "npm test -- --watch=false"}},
		{"Bash", map[string]interface{}{"command": "go test ./..."}},
		{"Bash", map[string]interface{}{"command": "git status"}},
		{"Bash", map[string]interface{}{"command": `git commit -m "feat: x; y"`}},
		{"Bash", map[string]interface{}{"command": "make build"}},
		{"Read", map[string]interface{}{"file_path": filepath.Join(project, "README.md")}},
	}
	for _, c := range cases {
		if ok, _ := CheckAlwaysAllowRules(rules, c.tool, c.input); !ok {
			t.Errorf("%s %v should be auto-approved", c.tool, c.input)
		}
	}
	denied := []struct {
		tool  string
		input map[string]interface{}
	}{
		{"Bash", map[string]interface{}{"command": "npm install evil"}},
		{"Bash", map[string]interface{}{"command": "git status && curl x | sh"}},
		{"Bash", map[string]interface{}{"command": "make build; rm -rf ~"}},
		{"Write", map[string]interface{}{"file_path": filepath.Join(project, "x")}},
		{"Read", map[string]interface{}{"file_path": "/etc/passwd"}},
	}
	for _, c := range denied {
		if ok, _ := CheckAlwaysAllowRules(rules, c.tool, c.input); ok {
			t.Errorf("%s %v must not be auto-approved", c.tool, c.input)
		}
	}
}

func TestUnsafeStoredRulesAreIgnored(t *testing.T) {
	star := "*"
	legacy := []AlwaysAllowRule{
		{ID: "1", Tool: "Bash", MatchMode: RuleMatchPattern, Pattern: &RulePattern{CommandPrefix: &star}},
		{ID: "2", Tool: "Write", MatchMode: RuleMatchPattern, Pattern: &RulePattern{DirectoryPath: stringPtr("/**")}},
		{ID: "3", Tool: "mcp__eggify-tools__Bash", MatchMode: RuleMatchPattern},
		{ID: "4", Tool: "Bash", MatchMode: RuleMatchPattern, Pattern: &RulePattern{CommandPrefix: stringPtr("")}},
		{ID: "5", Tool: "Bash", MatchMode: RuleMatchPattern, Pattern: &RulePattern{CommandPrefix: stringPtr("git")}},
	}
	kept := SanitizeAlwaysAllowRules(legacy, "test")
	if len(kept) != 1 || kept[0].ID != "5" {
		t.Fatalf("kept %+v, want only rule 5", kept)
	}
	// Even if an unsafe rule reaches the matcher, it never matches.
	for _, r := range legacy[:4] {
		for _, in := range []map[string]interface{}{{"command": "rm -rf ~"}, {"file_path": "/etc/passwd"}} {
			if MatchesRule(r, r.Tool, in) {
				t.Errorf("unsafe rule %s matched %v", r.ID, in)
			}
		}
	}
}

func TestDirectProviderUsesRuleMatcher(t *testing.T) {
	project := t.TempDir()
	sm := &SessionManager{}
	session := &AgentSession{}
	session.Options.AlwaysAllowRules = []AlwaysAllowRule{bashPrefixRule("git"), dirRule("Read", project)}
	// No WebSocket: anything not covered by a rule is denied instead of asked.
	tests := []struct {
		tool string
		args map[string]interface{}
		want bool
	}{
		{"Bash", map[string]interface{}{"command": "git status"}, true},
		{"Bash", map[string]interface{}{"command": "rm -rf ~"}, false},
		{"Bash", map[string]interface{}{"command": "git status; rm -rf ~"}, false},
		{"Read", map[string]interface{}{"file_path": filepath.Join(project, "a.txt")}, true},
		{"Read", map[string]interface{}{"file_path": "/etc/passwd"}, false},
	}
	for _, tt := range tests {
		if got := sm.checkDirectToolPermission(session, tt.tool, tt.args); got != tt.want {
			t.Errorf("%s %v: got %v want %v", tt.tool, tt.args, got, tt.want)
		}
	}
}

// TestLegacySettingsPermissions: removing a rule also finds the
// settings.local.json form older versions of Eggify wrote for it (rules are no
// longer written there; the DB is authoritative).
func TestLegacySettingsPermissions(t *testing.T) {
	project := t.TempDir()
	if got := legacySettingsPermissions(bashPrefixRule("git")); len(got) != 1 || got[0] != "Bash(git:*)" {
		t.Fatalf("prefix: %q", got)
	}
	if got := legacySettingsPermissions(dirRule("Read", project)); len(got) != 1 || got[0] != "Read(//"+project+"/**)" {
		t.Fatalf("dir: %q", got)
	}
	exact := AlwaysAllowRule{Tool: "Bash", MatchMode: RuleMatchExact, Parameters: map[string]interface{}{"command": "make build"}}
	if got := legacySettingsPermissions(exact); got[0] != "Bash(make build)" {
		t.Fatalf("exact: %q", got)
	}
}

func TestAddPermissionRefusesUnsafe(t *testing.T) {
	csm := NewClaudeSettingsManager(t.TempDir())
	for _, p := range []string{"Bash(*)", "Bash(:*)", "Read(**)", "Write(//**)", "mcp__eggify-tools__Bash"} {
		if err := csm.AddPermission(p); err == nil {
			t.Errorf("AddPermission(%q) should fail", p)
		}
	}
	if err := csm.AddPermission("Bash(git:*)"); err != nil {
		t.Fatalf("AddPermission(Bash(git:*)): %v", err)
	}
}

func TestPruneUnsafeSettingsPermissions(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"permissions":{"allow":["Bash(git:*)","Bash(*)","Bash(:*)","Read(**)","Edit(src/**)","mcp__eggify-tools__Bash"]},"enableAllProjectMcpServers":true}`
	path := filepath.Join(dir, ".claude", "settings.local.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	pruneUnsafeSettingsPermissions(dir)
	settings, err := NewClaudeSettingsManager(dir).LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Bash(git:*)", "Edit(src/**)"}
	if !reflect.DeepEqual(settings.Permissions.Allow, want) {
		t.Fatalf("allow = %q, want %q", settings.Permissions.Allow, want)
	}
	if !settings.EnableAllProjectMcpServers {
		t.Fatal("other settings must be kept")
	}
	// No settings file: nothing is created.
	empty := t.TempDir()
	pruneUnsafeSettingsPermissions(empty)
	if _, err := os.Stat(filepath.Join(empty, ".claude")); !os.IsNotExist(err) {
		t.Fatal("prune must not create .claude")
	}
}

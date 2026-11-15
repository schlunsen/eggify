package agents

// Live checks of Sandboxed mode against the real CLIs on this machine.
// They are skipped unless EGGIFY_LIVE_SANDBOX=1, because each one runs a real
// (cheap) model turn. The probe only reports whether files are readable — it
// never prints their contents.
//
//	EGGIFY_LIVE_SANDBOX=1 go test ./internal/server/agents -run TestLiveSandbox -v

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	claude "github.com/schlunsen/claude-agent-sdk-go"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/codexapp"
)

const sandboxProbeScript = `#!/bin/sh
# Eggify sandbox probe: reports access, never contents.
out=./probe_out.txt
: > "$out" 2>/dev/null || out=/dev/stdout
r() { echo "$1=$2" >> "$out"; }
head -c 1 "$HOME/.claude/eggify/.secret" >/dev/null 2>&1 && r secret readable || r secret blocked
head -c 1 "$HOME/.claude/eggify/eggify.db" >/dev/null 2>&1 && r db readable || r db blocked
echo ok > ./probe_write.txt 2>/dev/null && r project_write ok || r project_write blocked
echo x >> ./.mcp.json 2>/dev/null && r mcp_write ok || r mcp_write blocked
c=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 https://registry.npmjs.org/ 2>/dev/null); r npm "${c:-000}"
c=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 https://example.com/ 2>/dev/null); r example "${c:-000}"
c=$(curl -sk -o /dev/null -w '%{http_code}' --max-time 5 https://localhost:3333/api/health 2>/dev/null); r eggify_api "${c:-000}"
[ -n "$EGGIFY_API_KEY" ] && r eggify_key visible || r eggify_key hidden
r done yes
`

func liveSandboxDir(t *testing.T) string {
	t.Helper()
	if os.Getenv("EGGIFY_LIVE_SANDBOX") == "" {
		t.Skip("set EGGIFY_LIVE_SANDBOX=1 to run a real sandboxed turn")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "probe.sh"), []byte(sandboxProbeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readProbe(t *testing.T, dir string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "probe_out.txt"))
	if err != nil {
		t.Fatalf("probe did not run: %v", err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			got[k] = v
		}
	}
	t.Logf("probe: %v", got)
	if got["done"] != "yes" {
		t.Fatalf("probe incomplete: %v", got)
	}
	return got
}

func assertSandboxedProbe(t *testing.T, got map[string]string, npmReachable bool) {
	t.Helper()
	want := map[string]string{"secret": "blocked", "db": "blocked", "project_write": "ok", "mcp_write": "blocked",
		"example": "000", "eggify_api": "000", "eggify_key": "hidden"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if npmReachable && got["npm"] != "200" {
		t.Errorf("npm = %q, want 200 (allowlisted)", got["npm"])
	}
	if !npmReachable && got["npm"] != "000" {
		t.Errorf("npm = %q, want 000 (network off)", got["npm"])
	}
}

// TestLiveSandboxClaude runs a YOLO-mode Claude SDK session (permissions
// bypassed, so only the sandbox stands in the way) with Eggify's sandbox settings.
func TestLiveSandboxClaude(t *testing.T) {
	dir := liveSandboxDir(t)
	sandboxed := true
	options := SessionOptions{WorkingDirectory: &dir, Sandboxed: &sandboxed}
	sm := &SessionManager{}

	opts := types.NewClaudeAgentOptions().
		WithModel("haiku").
		WithCWD(dir).
		WithSettingSources(types.SettingSourceLocal, types.SettingSourceProject).
		WithPermissionMode(types.PermissionModeBypassPermissions).
		WithAllowDangerouslySkipPermissions(true).
		WithDangerouslySkipPermissions(true).
		// A credential in the CLI env: sandboxed commands must not see it.
		WithEnvVar("EGGIFY_API_KEY", "probe-not-a-real-key")
	opts, err := sm.applyClaudeSessionSettings(opts, &options)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	client, err := claude.NewClient(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Query(ctx, "Run exactly this one Bash command and nothing else: sh ./probe.sh — do not retry, do not work around failures, then stop."); err != nil {
		t.Fatal(err)
	}
	for msg := range client.ReceiveResponse(ctx) {
		if _, ok := msg.(*types.ResultMessage); ok {
			break
		}
	}
	assertSandboxedProbe(t, readProbe(t, dir), true)
}

// TestLiveSandboxClaudeUntrustedFolder launches the CLI the way an untrusted
// folder is launched (no setting sources, strict MCP config) with a project
// settings file that tries to widen the sandbox. Eggify's settings travel as flag
// settings (--settings), so the sandbox and the Read deny rule must still hold
// and the project file must be ignored.
func TestLiveSandboxClaudeUntrustedFolder(t *testing.T) {
	dir := liveSandboxDir(t)
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	widening := `{"sandbox":{"enabled":false,"filesystem":{"allowRead":["~/.claude/eggify"]},"network":{"allowedDomains":["example.com"]}},"permissions":{"allow":["Read"]}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(widening), 0o644); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	settings, err := json.Marshal(buildClaudeSessionSettings(home, dir, true, []string{"registry.npmjs.org"}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	secret := filepath.Join(home, ".claude", "eggify", ".secret")
	cmd := exec.CommandContext(ctx, "claude", "-p", "--model", "haiku",
		"--setting-sources", "", "--strict-mcp-config",
		"--permission-mode", "bypassPermissions", "--dangerously-skip-permissions",
		"--settings", string(settings), "--output-format", "stream-json", "--verbose",
		"First run exactly this one Bash command: sh ./probe.sh — then call the Read tool once on "+secret+". Do not retry or work around failures, and never repeat file contents.")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "EGGIFY_API_KEY=probe-not-a-real-key")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("claude: %v", err)
	}
	// The project file tried to switch the sandbox off, re-open ~/.claude/eggify
	// and allow example.com: none of it may apply.
	assertSandboxedProbe(t, readProbe(t, dir), true)

	// The Read tool call on the secret must have been refused.
	readIDs := map[string]bool{}
	refused, sawResult := false, false
	for _, line := range strings.Split(string(out), "\n") {
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Type      string          `json:"type"`
					ID        string          `json:"id"`
					Name      string          `json:"name"`
					ToolUseID string          `json:"tool_use_id"`
					IsError   bool            `json:"is_error"`
					Content   json.RawMessage `json:"content"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		for _, c := range ev.Message.Content {
			if c.Type == "tool_use" && c.Name == "Read" {
				readIDs[c.ID] = true
			}
			if c.Type == "tool_result" && readIDs[c.ToolUseID] {
				sawResult = true
				refused = refused || c.IsError
			}
		}
	}
	if !sawResult {
		t.Skip("the model did not call Read; the deny rule was not exercised")
	}
	if !refused {
		t.Error("Read on ~/.claude/eggify/.secret was not refused")
	}
}

// TestLiveSandboxCodex runs one Codex app-server turn with the sandboxed
// permission profile and YOLO-style approval (never ask).
func TestLiveSandboxCodex(t *testing.T) {
	dir := liveSandboxDir(t)
	exe, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex CLI not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	app, err := codexapp.Start(ctx, exe, os.Environ(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := app.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "eggify-test", "version": "1.0"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := app.Notify("initialized", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	params := map[string]any{"approvalPolicy": "never", "cwd": dir, "config": codexSandboxConfig(home, dir, nil), "ephemeral": true}
	if err := app.Call(ctx, "thread/start", params, &thread); err != nil {
		t.Fatal(err)
	}
	input := []map[string]any{{"type": "text", "text": "Run exactly this one shell command and nothing else: sh ./probe.sh — do not retry, do not work around failures, then stop."}}
	if err := app.Call(ctx, "turn/start", map[string]any{"threadId": thread.Thread.ID, "input": input}, nil); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for the Codex turn")
		case ev, ok := <-app.Events():
			if !ok {
				t.Fatalf("app-server closed: %v", app.Err())
			}
			if ev.Method == "turn/completed" {
				var p struct {
					Turn struct {
						Status string          `json:"status"`
						Error  json.RawMessage `json:"error"`
					} `json:"turn"`
				}
				_ = json.Unmarshal(ev.Params, &p)
				t.Logf("turn %s %s", p.Turn.Status, string(p.Turn.Error))
				assertSandboxedProbe(t, readProbe(t, dir), false)
				return
			}
		}
	}
}

// TestLiveSandboxClaudeReadOnly runs a read-only session (review workers,
// SessionOptions.ReadOnly) with permissions bypassed: the Write tool and a
// sandboxed command must both fail to create a file in the working directory.
func TestLiveSandboxClaudeReadOnly(t *testing.T) {
	dir := liveSandboxDir(t)
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sandboxed := true
	options := SessionOptions{WorkingDirectory: &dir, Sandboxed: &sandboxed, ReadOnly: true}
	sm := &SessionManager{}

	opts := types.NewClaudeAgentOptions().
		WithModel("haiku").
		WithCWD(dir).
		WithSettingSources(types.SettingSourceLocal, types.SettingSourceProject).
		WithPermissionMode(types.PermissionModeBypassPermissions).
		WithAllowDangerouslySkipPermissions(true).
		WithDangerouslySkipPermissions(true)
	opts, err := sm.applyClaudeSessionSettings(opts, &options)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	client, err := claude.NewClient(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	prompt := "Do exactly these three steps, once each, in order; do not retry and do not work around failures, then stop. " +
		"1) Run this one Bash command: sh -c 'echo x > ./via_bash.txt; echo y >> ./keep.txt; rm -f ./probe.sh; cat ./keep.txt' " +
		"2) Use the Write tool to create the file via_write.txt in the current directory with the content hi. " +
		"3) Use the Edit tool to replace the word original with changed in keep.txt."
	if err := client.Query(ctx, prompt); err != nil {
		t.Fatal(err)
	}
	for msg := range client.ReceiveResponse(ctx) {
		if _, ok := msg.(*types.ResultMessage); ok {
			break
		}
	}

	for _, name := range []string{"via_bash.txt", "via_write.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s was created in a read-only session", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "probe.sh")); err != nil {
		t.Errorf("probe.sh was deleted in a read-only session: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "keep.txt")); string(got) != "original\n" {
		t.Errorf("keep.txt was changed in a read-only session: %q", got)
	}
}

// TestLiveSandboxAutopilot runs a sandboxed session with prompts off the way
// Eggify starts it (default permission mode, no skip flags, the permission
// callback deciding): the Write tool must work inside the project and fail for
// the home folder and ~/Library/LaunchAgents, and Bash must run without anyone
// answering.
func TestLiveSandboxAutopilot(t *testing.T) {
	dir := liveSandboxDir(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	// A trusted project that allows Write and Edit must not approve writes on its own.
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(`{"permissions":{"allow":["Write","Edit"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	homeProbe := filepath.Join(home, "eggify-autopilot-probe.txt")
	agentProbe := filepath.Join(home, "Library", "LaunchAgents", "eggify-autopilot-probe.txt")
	cleanup := func() { os.Remove(homeProbe); os.Remove(agentProbe) }
	cleanup()
	defer cleanup()

	sandboxed, yolo := true, true
	options := SessionOptions{WorkingDirectory: &dir, Sandboxed: &sandboxed, DangerouslySkipPermissions: &yolo, AllowDangerouslySkipPermissions: &yolo}
	if !autopilotSession(&options) {
		t.Fatal("options are not an Autopilot session")
	}
	scope := newAutopilotScope(home, &options, DefaultSandboxAllowedDomains)
	var asked []string
	canUseTool := func(_ context.Context, tool string, input map[string]interface{}, _ types.ToolPermissionContext) (interface{}, error) {
		d := autopilotDecide(scope, tool, input)
		asked = append(asked, tool)
		if d.Handled && d.Allow {
			return types.PermissionResultAllow{}, nil
		}
		return types.PermissionResultDeny{Message: d.Message}, nil
	}

	sm := &SessionManager{}
	opts := types.NewClaudeAgentOptions().
		WithModel("haiku").
		WithCWD(dir).
		WithSettingSources(types.SettingSourceLocal, types.SettingSourceProject).
		WithPermissionMode(types.PermissionModeDefault).
		WithCanUseTool(canUseTool)
	opts, err = sm.applyClaudeSessionSettings(opts, &options)
	if err != nil {
		t.Fatal(err)
	}
	if opts.DangerouslySkipPermissions || opts.AllowDangerouslySkipPermissions {
		t.Fatal("skip flags set on an Autopilot session")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client, err := claude.NewClient(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	prompt := "Do exactly these four steps, once each, in order; if one fails, do not retry or work around it, just go on to the next, then stop. " +
		"1) Use the Write tool to create inside.txt in the current directory with the content hi. " +
		"2) Use the Write tool to create " + homeProbe + " with the content hi. " +
		"3) Use the Write tool to create " + agentProbe + " with the content hi. " +
		"4) Run this one Bash command: sh -c 'go version > ./go_version.txt'"
	if err := client.Query(ctx, prompt); err != nil {
		t.Fatal(err)
	}
	for msg := range client.ReceiveResponse(ctx) {
		if _, ok := msg.(*types.ResultMessage); ok {
			break
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "inside.txt")); err != nil {
		t.Errorf("the Write tool could not create a file inside the project: %v (callback saw %v)", err, asked)
	}
	for _, p := range []string{homeProbe, agentProbe} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("the Write tool created %s outside the project", p)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "go_version.txt")); !strings.HasPrefix(string(got), "go version") {
		t.Errorf("go version did not run in the sandbox without an answer: %q (callback saw %v)", got, asked)
	}
}

// liveProbeProject writes a small Go + npm project with a git repository and
// the everyday-commands probe (scripts/tests/sandbox-probe.sh) into dir.
func liveProbeProject(t *testing.T, dir string) {
	t.Helper()
	probe, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "tests", "sandbox-probe.sh"))
	if err != nil {
		t.Fatalf("probe script: %v", err)
	}
	files := map[string]string{
		"sandbox-probe.sh": string(probe),
		"go.mod":           "module probe\n\ngo 1.21\n",
		"main.go":          "package main\n\nfunc main() {}\n\nfunc Add(a, b int) int { return a + b }\n",
		"main_test.go":     "package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"add\")\n\t}\n}\n",
		"package.json":     `{"name":"probe","version":"1.0.0","dependencies":{"is-odd":"3.0.1"}}`,
		"ssh-check.sh": "#!/bin/sh\n" +
			"git ls-remote git@github.com:google/uuid.git HEAD > ssh_out.txt 2>&1; echo rc=$? >> ssh_out.txt\n" +
			"for f in ~/.ssh/id_*; do case $f in *.pub) ;; *) head -c 1 \"$f\" >/dev/null 2>&1 && echo key_readable >> ssh_out.txt || echo key_hidden >> ssh_out.txt;; esac; done\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=probe", "-c", "user.email=p@example.com", "commit", "-q", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

// probeLine returns the report line of one probe label ("exit=0 built ...").
func probeLine(report, label string) string {
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, label) {
			return strings.TrimSpace(strings.TrimPrefix(line, label))
		}
	}
	return ""
}

// TestLiveSandboxProbe runs the everyday-commands probe in a temporary
// project inside a real sandboxed turn with Eggify's settings, caches and (when
// EGGIFY_LIVE_SANDBOX_SSH=1 and an ssh-agent holds a key) the ssh grant. It
// asserts that builds work and personal credentials are hidden.
//
//	EGGIFY_LIVE_SANDBOX=1 [EGGIFY_LIVE_SANDBOX_SSH=1] go test ./internal/server/agents -run TestLiveSandboxProbe -v
func TestLiveSandboxProbe(t *testing.T) {
	dir := liveSandboxDir(t)
	liveProbeProject(t, dir)
	withSSH := os.Getenv("EGGIFY_LIVE_SANDBOX_SSH") != ""
	sandboxed := true
	options := SessionOptions{WorkingDirectory: &dir, Sandboxed: &sandboxed}
	if withSSH {
		options.SandboxGrants = []string{GrantSSH}
	}
	sm := &SessionManager{}
	opts := types.NewClaudeAgentOptions().
		WithModel("haiku").
		WithCWD(dir).
		WithSettingSources(types.SettingSourceLocal, types.SettingSourceProject).
		WithPermissionMode(types.PermissionModeBypassPermissions).
		WithAllowDangerouslySkipPermissions(true).
		WithDangerouslySkipPermissions(true)
	opts, err := sm.applyClaudeSessionSettings(opts, &options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	client, err := claude.NewClient(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	prompt := "Run exactly this one Bash command and nothing else, then stop: bash ./sandbox-probe.sh > probe_report.txt 2>&1"
	if withSSH {
		prompt += " — after it finishes, also run: sh ./ssh-check.sh"
	}
	prompt += ". Do not retry and do not work around failures."
	if err := client.Query(ctx, prompt); err != nil {
		t.Fatal(err)
	}
	for msg := range client.ReceiveResponse(ctx) {
		if _, ok := msg.(*types.ResultMessage); ok {
			break
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "probe_report.txt"))
	if err != nil {
		t.Fatalf("probe did not run: %v", err)
	}
	report := string(data)
	t.Logf("probe report:\n%s", report)

	for _, label := range []string{"go build", "go test", "npm install", "uv pip install"} {
		line := probeLine(report, label)
		if !strings.HasPrefix(strings.TrimSpace(strings.TrimLeft(line, " ")), "exit=0") {
			t.Errorf("%s: %q, want exit=0", label, line)
		}
	}
	if line := probeLine(report, "uv pip install"); strings.Contains(strings.ToLower(line), "error") {
		t.Errorf("uv pip install reported an error: %q", line)
	}
	for _, label := range []string{"read ssh private key", "read ~/.config/gh", "read Eggify api key", "read ~/.aws", "read ~/.zsh_history"} {
		if line := probeLine(report, label); strings.Contains(line, "readable") && !strings.Contains(line, "not readable") {
			t.Errorf("%s: %q, want hidden", label, line)
		}
	}
	if line := probeLine(report, "write in home"); strings.Contains(line, "ok") {
		t.Errorf("write in home: %q, want a failure", line)
	}

	if withSSH {
		ssh, err := os.ReadFile(filepath.Join(dir, "ssh_out.txt"))
		if err != nil {
			t.Fatalf("ssh check did not run: %v", err)
		}
		t.Logf("ssh check (key never printed):\n%s", ssh)
		if !strings.Contains(string(ssh), "rc=0") || !strings.Contains(string(ssh), "refs/heads") && !strings.Contains(string(ssh), "HEAD") {
			t.Errorf("git ls-remote over ssh did not succeed with the ssh grant")
		}
		if strings.Contains(string(ssh), "key_readable") {
			t.Errorf("a private key was readable with the ssh grant")
		}
	}
}

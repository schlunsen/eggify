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

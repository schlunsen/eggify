package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schlunsen/eggify/internal/capability"
)

// TestRedTeam_MCP_CoreToolsOnlyForDirectSessions: eggify-tools' Bash/Read/
// Write/Edit/Glob/Grep run in its own process, outside the Claude CLI's and
// Codex's sandboxes and deny rules. A sandboxed (or YOLO) Claude session that
// called mcp__eggify-tools__Bash got an unsandboxed shell. Only the direct
// loop, which has no tools of its own, starts eggify-tools with them.
func TestRedTeam_MCP_CoreToolsOnlyForDirectSessions(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")

	t.Setenv(capability.EnvCoreTools, "")
	srv := NewMCPServer(nil)
	srv.workDir = dir
	if listed := toolNames(t, srv); listed["Bash"] || listed["Read"] || listed["Write"] {
		t.Fatalf("core tools listed without the direct-session flag: %v", listed)
	}
	out := callTool(srv, "Bash", map[string]interface{}{"command": "touch " + marker})
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("Bash ran without the direct-session flag: %s", out)
	}
	if !strings.Contains(out, "Unknown tool") {
		t.Fatalf("want Unknown tool, got %s", out)
	}

	t.Setenv(capability.EnvCoreTools, "1")
	if listed := toolNames(t, srv); !listed["Bash"] || !listed["Grep"] {
		t.Fatalf("direct session lost its core tools: %v", listed)
	}
}

func toolNames(t *testing.T, srv *MCPServer) map[string]bool {
	t.Helper()
	resp := srv.HandleRequest(MCPRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"})
	b, _ := json.Marshal(resp)
	var parsed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, tl := range parsed.Result.Tools {
		out[tl.Name] = true
	}
	return out
}

// TestRedTeam_MCP_GlobPatternIsNeverShellExpanded: a path pattern went into
// bash -c "... ls -1 %q", and bash expands $(...) inside double quotes.
func TestRedTeam_MCP_GlobPatternIsNeverShellExpanded(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "b", "x.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a/$(touch " + marker + ")", "a/`touch " + marker + "`"} {
		handleGlobTool(dir, map[string]interface{}{"pattern": p})
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a glob pattern ran a command")
	}
	res := handleGlobTool(dir, map[string]interface{}{"pattern": "**/*.go"})
	if got := res.Content[0].Text; got != filepath.Join("a", "b", "x.go") {
		t.Fatalf("**/*.go = %q", got)
	}
	res = handleGlobTool(dir, map[string]interface{}{"pattern": "a/*/x.go"})
	if got := res.Content[0].Text; got != filepath.Join("a", "b", "x.go") {
		t.Fatalf("a/*/x.go = %q", got)
	}
}

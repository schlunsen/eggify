package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/schlunsen/eggify/internal/gitexec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
)

// SessionWorkspace describes observed Git activity, not ownership of a worktree.
// It must never be used to choose which worktree session cleanup deletes.
type SessionWorkspace struct {
	WorkingDirectory string `json:"working_directory"`
	WorktreePath     string `json:"worktree_path"`
	Branch           string `json:"branch"`
}

type workspaceTool struct{ name, path string }

func sessionGitDirectory(session *AgentSession) string {
	if session.Options.Workspace != nil {
		return session.Options.Workspace.WorkingDirectory
	}
	if session.WorktreePath != "" {
		return session.WorktreePath
	}
	return codexStr(session.Options.WorkingDirectory)
}

// SessionGitDirectory returns the latest observed directory for display only.
func (sm *SessionManager) SessionGitDirectory(id uuid.UUID) (string, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	session, ok := sm.sessions[id]
	if !ok {
		return "", fmt.Errorf("session not found: %s", id)
	}
	return sessionGitDirectory(session), nil
}

var workspacePathArgs = []string{"rev-parse", "--path-format=absolute", "--show-toplevel", "--absolute-git-dir", "--git-common-dir"}

func runWorkspaceRevParse(dir string, withBranch bool) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := append([]string{}, workspacePathArgs...)
	want := 3
	if withBranch {
		args = append(args, "--abbrev-ref", "HEAD")
		want = 4
	}
	cmd := gitexec.CommandContext(ctx, args...)
	cmd.Dir = dir
	gitexec.Harden(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != want {
		return nil, fmt.Errorf("unexpected git workspace response")
	}
	return lines, nil
}

func workspaceFromLines(lines []string) *SessionWorkspace {
	workspace := &SessionWorkspace{WorkingDirectory: lines[0]}
	if filepath.Clean(lines[1]) != filepath.Clean(lines[2]) {
		workspace.WorktreePath = lines[0]
	}
	return workspace
}

// inspectWorkspacePaths resolves the checkout root and whether it is a linked
// worktree, without the branch.
func inspectWorkspacePaths(dir string) (*SessionWorkspace, error) {
	lines, err := runWorkspaceRevParse(dir, false)
	if err != nil {
		return nil, err
	}
	return workspaceFromLines(lines), nil
}

// inspectSessionWorkspace resolves the checkout root, worktree and branch with
// a single git process. A branch with no commits yet makes `--abbrev-ref HEAD`
// fail, so that case falls back to the paths alone with an empty branch, which
// is what GetGitBranch reports for it.
func inspectSessionWorkspace(dir string) (*SessionWorkspace, error) {
	if lines, err := runWorkspaceRevParse(dir, true); err == nil {
		workspace := workspaceFromLines(lines)
		workspace.Branch = lines[3]
		return workspace, nil
	}
	lines, err := runWorkspaceRevParse(dir, false)
	if err != nil {
		return nil, err
	}
	workspace := workspaceFromLines(lines)
	workspace.Branch = GetGitBranch(lines[0])
	return workspace, nil
}

func (sm *SessionManager) observeSessionWorkspace(session *AgentSession, dir string) {
	if dir == "" {
		return
	}
	workspace, err := inspectSessionWorkspace(dir)
	if err != nil {
		return
	}
	sm.mu.Lock()
	old := session.Options.Workspace
	if old != nil && *old == *workspace && session.GitBranch == workspace.Branch {
		sm.mu.Unlock()
		return
	}
	session.Options.Workspace = workspace
	session.GitBranch = workspace.Branch
	sm.mu.Unlock()
	sm.persistSessionState(session)
	sm.broadcastSessionUpdate(session)
}

// Only recognize literal shell directory prefixes; never evaluate shell input.
// A command mentioning or creating a worktree is not proof the agent used it.
var workspaceCommandPrefix = regexp.MustCompile("^\\s*(?:cd\\s+(?:--\\s+)?|git\\s+-C\\s+)(\"[^\"$`]+\"|'[^']+'|[^\\s;&|$`<>]+)(?:\\s|&&|;|$)")

// Codex may expose the shell invocation rather than only its command body.
var workspaceShellWrapper = regexp.MustCompile(`^(?:/[^ ]*/)?(?:bash|zsh|sh)\s+-(?:lc|c)\s+(.+)$`)

func workspaceShellCommand(command string) string {
	if match := workspaceShellWrapper.FindStringSubmatch(strings.TrimSpace(command)); len(match) > 1 {
		body := strings.TrimSpace(match[1])
		if len(body) >= 2 && (body[0] == '\'' || body[0] == '"') && body[len(body)-1] == body[0] && !strings.ContainsRune(body[1:len(body)-1], rune(body[0])) {
			return body[1 : len(body)-1]
		}
	}
	return command
}

func workspaceToolDirectory(input map[string]interface{}, base string) string {
	dir := base
	explicit := false
	for _, key := range []string{"cwd", "workdir", "working_directory"} {
		if v, ok := input[key].(string); ok && v != "" {
			dir = v
			explicit = true
			break
		}
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(base, dir)
	}
	command, _ := input["command"].(string)
	if command == "" {
		command, _ = input["cmd"].(string)
	}
	if match := workspaceCommandPrefix.FindStringSubmatch(workspaceShellCommand(command)); len(match) > 1 {
		explicit = true
		path := strings.Trim(match[1], "\"'")
		if filepath.IsAbs(path) {
			dir = path
		} else {
			dir = filepath.Join(dir, path)
		}
	}
	if !explicit {
		return ""
	}
	return dir
}

// isWorkspaceToolName reports whether a tool can move the session to another
// directory or branch, so its result is worth a git check.
func isWorkspaceToolName(name string) bool {
	switch name {
	case "Bash", "bash", "exec_command", "EnterWorktree":
		return true
	}
	// MCP shells such as mcp__eggify-tools__Bash.
	return strings.HasSuffix(name, "__Bash")
}

// toolResultRef is the part of a tool_result block the workspace observer needs.
type toolResultRef struct {
	id      string
	content interface{}
	isError bool
}

// userToolResults extracts tool_result blocks from a user message without a
// JSON round trip. The SDK delivers []types.ContentBlock; reconstructed and
// provider-built messages may carry plain maps instead.
func userToolResults(content interface{}) []toolResultRef {
	var results []toolResultRef
	switch blocks := content.(type) {
	case []types.ContentBlock:
		for _, block := range blocks {
			if b, ok := block.(*types.ToolResultBlock); ok {
				results = append(results, toolResultRef{id: b.ToolUseID, content: b.Content, isError: b.IsError != nil && *b.IsError})
			}
		}
	case []interface{}:
		for _, item := range blocks {
			switch b := item.(type) {
			case *types.ToolResultBlock:
				results = append(results, toolResultRef{id: b.ToolUseID, content: b.Content, isError: b.IsError != nil && *b.IsError})
			case map[string]interface{}:
				if b["type"] != "tool_result" {
					continue
				}
				id, _ := b["tool_use_id"].(string)
				isError, _ := b["is_error"].(bool)
				results = append(results, toolResultRef{id: id, content: b["content"], isError: isError})
			}
		}
	case []map[string]interface{}:
		for _, b := range blocks {
			if b["type"] != "tool_result" {
				continue
			}
			id, _ := b["tool_use_id"].(string)
			isError, _ := b["is_error"].(bool)
			results = append(results, toolResultRef{id: id, content: b["content"], isError: isError})
		}
	}
	return results
}

// observeWorkspaceMessage follows the session's workspace through tool calls.
// It reports whether the message finished a tool that may have changed the
// checked-out branch without naming a directory to inspect (a plain
// `git checkout` in Bash), in which case the caller should refresh the branch.
func (sm *SessionManager) observeWorkspaceMessage(session *AgentSession, msg types.Message) (refreshBranch bool) {
	switch m := msg.(type) {
	case *types.SystemMessage:
		if dir, ok := m.Data["cwd"].(string); ok {
			sm.observeSessionWorkspace(session, dir)
		}
	case *types.AssistantMessage:
		sm.mu.Lock()
		defer sm.mu.Unlock()
		// Shell commands without explicit cwd start in the configured directory,
		// not necessarily in the worktree used by the previous shell invocation.
		base := codexStr(session.Options.WorkingDirectory)
		for _, block := range m.Content {
			tool, ok := block.(*types.ToolUseBlock)
			if !ok || !isWorkspaceToolName(tool.Name) {
				continue
			}
			if session.workspaceTools == nil {
				session.workspaceTools = map[string]workspaceTool{}
			}
			session.workspaceTools[tool.ID] = workspaceTool{name: tool.Name, path: workspaceToolDirectory(tool.Input, base)}
		}
	case *types.UserMessage:
		// Most user messages answer Read/Edit/Grep calls: skip them unless a
		// workspace tool is actually waiting for its result.
		sm.mu.RLock()
		pending := len(session.workspaceTools)
		sm.mu.RUnlock()
		if pending == 0 {
			return false
		}
		for _, block := range userToolResults(m.Content) {
			sm.mu.Lock()
			tool, ok := session.workspaceTools[block.id]
			delete(session.workspaceTools, block.id)
			sm.mu.Unlock()
			if !ok {
				continue
			}
			if block.isError {
				// A failed command may still have switched branches before failing.
				refreshBranch = true
				continue
			}
			dir := tool.path
			if tool.name == "EnterWorktree" {
				dir = enteredWorktreePath(block.content)
			}
			if dir == "" {
				refreshBranch = true
				continue
			}
			sm.observeSessionWorkspace(session, dir)
		}
	}
	return refreshBranch
}

// clearWorkspaceTools forgets tool calls whose results will never arrive.
// Callers must hold sm.mu.
func (session *AgentSession) clearWorkspaceTools() {
	session.workspaceTools = nil
}

func enteredWorktreePath(content interface{}) string {
	switch v := content.(type) {
	case []map[string]interface{}:
		for _, part := range v {
			if p := enteredWorktreePath(part); p != "" {
				return p
			}
		}
	case map[string]interface{}:
		for _, key := range []string{"worktree_path", "worktreePath", "cwd", "path"} {
			if p, ok := v[key].(string); ok && filepath.IsAbs(p) {
				return p
			}
		}
		if text, ok := v["text"]; ok {
			return enteredWorktreePath(text)
		}
	case []interface{}:
		for _, part := range v {
			if p := enteredWorktreePath(part); p != "" {
				return p
			}
		}
	case string:
		var obj map[string]interface{}
		if json.Unmarshal([]byte(v), &obj) == nil {
			return enteredWorktreePath(obj)
		}
		for _, line := range strings.Split(v, "\n") {
			label, path, ok := strings.Cut(strings.TrimSpace(line), ":")
			if ok && strings.EqualFold(strings.TrimSpace(label), "worktree path") {
				path = strings.Trim(strings.TrimSpace(path), "`\"'")
				if filepath.IsAbs(path) {
					return path
				}
			}
		}
	}
	return ""
}

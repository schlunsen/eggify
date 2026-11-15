package mcp

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/schlunsen/eggify/internal/capability"
)

// CoreToolsEnabled reports whether this eggify-tools serves the core tools:
// only a direct session's (capability.EnvCoreTools, set by the server). Claude
// and Codex sessions start eggify-tools without it; there the tools would run
// outside those runtimes' sandboxes and deny rules.
func CoreToolsEnabled() bool { return os.Getenv(capability.EnvCoreTools) == "1" }

// sandboxToolDefinitions returns the core coding tools (Bash, Read, Write, Edit, Glob, Grep).
// These are equivalent to the tools built into Claude Code CLI but exposed via MCP
// so the direct provider path can use them for non-Claude models.
func sandboxToolDefinitions() []Tool {
	return []Tool{
		{
			Name:        "Bash",
			Description: "Execute a bash command in the sandbox environment. Use this for running shell commands, installing packages, building projects, etc.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"command": {
						Type:        "string",
						Description: "The bash command to execute",
					},
					"timeout": {
						Type:        "number",
						Description: "Optional timeout in milliseconds (default: 120000, max: 600000)",
					},
				},
				Required: []string{"command"},
			},
		},
		{
			Name:        "Read",
			Description: "Read the contents of a file. Returns the file content with line numbers.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"file_path": {
						Type:        "string",
						Description: "The path to the file to read (absolute or relative to working directory)",
					},
					"offset": {
						Type:        "number",
						Description: "Line number to start reading from (0-based)",
					},
					"limit": {
						Type:        "number",
						Description: "Maximum number of lines to read",
					},
				},
				Required: []string{"file_path"},
			},
		},
		{
			Name:        "Write",
			Description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"file_path": {
						Type:        "string",
						Description: "The path to the file to write",
					},
					"content": {
						Type:        "string",
						Description: "The content to write to the file",
					},
				},
				Required: []string{"file_path", "content"},
			},
		},
		{
			Name:        "Edit",
			Description: "Edit a file by replacing a specific string with a new string. The old_string must match exactly (including whitespace and indentation).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"file_path": {
						Type:        "string",
						Description: "The path to the file to edit",
					},
					"old_string": {
						Type:        "string",
						Description: "The exact string to find and replace",
					},
					"new_string": {
						Type:        "string",
						Description: "The string to replace it with",
					},
				},
				Required: []string{"file_path", "old_string", "new_string"},
			},
		},
		{
			Name:        "Glob",
			Description: "Find files matching a glob pattern. Returns matching file paths.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"pattern": {
						Type:        "string",
						Description: "Glob pattern to match (e.g., '**/*.go', 'src/*.ts')",
					},
					"path": {
						Type:        "string",
						Description: "Directory to search in (default: working directory)",
					},
				},
				Required: []string{"pattern"},
			},
		},
		{
			Name:        "Grep",
			Description: "Search file contents using regular expressions. Returns matching lines with file paths and line numbers.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"pattern": {
						Type:        "string",
						Description: "Regular expression pattern to search for",
					},
					"path": {
						Type:        "string",
						Description: "File or directory to search in (default: working directory)",
					},
					"glob": {
						Type:        "string",
						Description: "Glob pattern to filter files (e.g., '*.go', '*.ts')",
					},
				},
				Required: []string{"pattern"},
			},
		},
	}
}

// handleSandboxToolCall executes a core sandbox tool and returns the result.
func handleSandboxToolCall(workDir string, toolName string, args map[string]interface{}) CallToolResult {
	switch toolName {
	case "Bash":
		return handleBashTool(workDir, args)
	case "Read":
		return handleReadTool(workDir, args)
	case "Write":
		return handleWriteTool(workDir, args)
	case "Edit":
		return handleEditTool(workDir, args)
	case "Glob":
		return handleGlobTool(workDir, args)
	case "Grep":
		return handleGrepTool(workDir, args)
	default:
		return mcpErrorResult(fmt.Sprintf("Unknown sandbox tool: %s", toolName))
	}
}

func handleBashTool(workDir string, args map[string]interface{}) CallToolResult {
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return mcpErrorResult("Missing required parameter: command")
	}

	timeoutMs := 120000.0
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeoutMs = t
		if timeoutMs > 600000 {
			timeoutMs = 600000
		}
	}

	// Use context-based timeout so the process tree is cleaned up properly
	// and we don't leak goroutines waiting on CombinedOutput.
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = workDir

	output, cmdErr := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		return mcpErrorResult(fmt.Sprintf("Command timed out after %.0f ms", timeoutMs))
	}

	result := string(output)
	if len(result) > 100000 {
		result = result[:100000] + "\n... (output truncated at 100000 chars)"
	}

	if cmdErr != nil {
		if result == "" {
			result = fmt.Sprintf("Command failed: %v", cmdErr)
		}
		isErr := true
		return CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: result}},
			IsError: &isErr,
		}
	}

	return CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: result}},
	}
}

func handleReadTool(workDir string, args map[string]interface{}) CallToolResult {
	filePath, ok := args["file_path"].(string)
	if !ok || filePath == "" {
		return mcpErrorResult("Missing required parameter: file_path")
	}

	pol, err := newPathPolicy(workDir)
	if err != nil {
		return mcpErrorResult(err.Error())
	}
	filePath, err = pol.resolve(workDir, filePath, false)
	if err != nil {
		return mcpErrorResult(err.Error())
	}

	data, err := pol.readFile(filePath)
	if err != nil {
		return mcpErrorResult(fmt.Sprintf("Failed to read file: %v", err))
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	offset := 0
	if o, ok := args["offset"].(float64); ok {
		offset = int(o)
	}
	if offset < 0 {
		offset = 0
	}
	limit := len(lines)
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	if offset >= len(lines) {
		return CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: "(offset beyond end of file)"}},
		}
	}

	end := offset + limit
	if end > len(lines) {
		end = len(lines)
	}

	var result strings.Builder
	for i := offset; i < end; i++ {
		result.WriteString(fmt.Sprintf("%6d\t%s\n", i+1, lines[i]))
	}

	return CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: result.String()}},
	}
}

func handleWriteTool(workDir string, args map[string]interface{}) CallToolResult {
	filePath, ok := args["file_path"].(string)
	if !ok || filePath == "" {
		return mcpErrorResult("Missing required parameter: file_path")
	}
	content, ok := args["content"].(string)
	if !ok {
		return mcpErrorResult("Missing required parameter: content")
	}

	pol, err := newPathPolicy(workDir)
	if err != nil {
		return mcpErrorResult(err.Error())
	}
	filePath, err = pol.resolve(workDir, filePath, true)
	if err != nil {
		return mcpErrorResult(err.Error())
	}

	// Parent directories are created inside the working directory's root.
	if err := pol.writeFile(filePath, []byte(content)); err != nil {
		return mcpErrorResult(fmt.Sprintf("Failed to write file: %v", err))
	}

	return CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), filePath)}},
	}
}

func handleEditTool(workDir string, args map[string]interface{}) CallToolResult {
	filePath, ok := args["file_path"].(string)
	if !ok || filePath == "" {
		return mcpErrorResult("Missing required parameter: file_path")
	}
	oldString, ok := args["old_string"].(string)
	if !ok {
		return mcpErrorResult("Missing required parameter: old_string")
	}
	newString, ok := args["new_string"].(string)
	if !ok {
		return mcpErrorResult("Missing required parameter: new_string")
	}

	pol, err := newPathPolicy(workDir)
	if err != nil {
		return mcpErrorResult(err.Error())
	}
	filePath, err = pol.resolve(workDir, filePath, true)
	if err != nil {
		return mcpErrorResult(err.Error())
	}

	data, err := pol.readFile(filePath)
	if err != nil {
		return mcpErrorResult(fmt.Sprintf("Failed to read file: %v", err))
	}

	content := string(data)
	count := strings.Count(content, oldString)

	if count == 0 {
		return mcpErrorResult("old_string not found in file")
	}
	if count > 1 {
		return mcpErrorResult(fmt.Sprintf("old_string found %d times — must be unique. Provide more context.", count))
	}

	newContent := strings.Replace(content, oldString, newString, 1)
	if err := pol.writeFile(filePath, []byte(newContent)); err != nil {
		return mcpErrorResult(fmt.Sprintf("Failed to write file: %v", err))
	}

	return CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Successfully edited %s", filePath)}},
	}
}

func handleGlobTool(workDir string, args map[string]interface{}) CallToolResult {
	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return mcpErrorResult("Missing required parameter: pattern")
	}

	pol, err := newPathPolicy(workDir)
	if err != nil {
		return mcpErrorResult(err.Error())
	}
	searchDir := pol.root
	if p, ok := args["path"].(string); ok && p != "" {
		resolved, err := pol.resolve(workDir, p, false)
		if err != nil {
			return mcpErrorResult(err.Error())
		}
		searchDir = resolved
	}

	// Use find command for recursive glob support (** patterns).
	// Pattern is passed as a flag argument to find (not interpolated into shell),
	// so it is safe from command injection.
	if strings.Contains(pattern, "/") {
		// Path patterns (incl. **) are matched here, never by a shell: %q in
		// a bash -c string still expanded $(...) and backticks.
		return globPathPattern(searchDir, pattern, pol.noRead)
	}
	// find does not follow symbolic links (-P) and -type f skips link entries.
	cmd := exec.Command("find", "-P", searchDir, "-name", pattern, "-type", "f")

	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		return CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: "No matching files found"}},
		}
	}

	result := pol.dropProtected(strings.TrimSpace(string(output)))
	if result == "" {
		result = "No matching files found"
	}

	return CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: result}},
	}
}

func handleGrepTool(workDir string, args map[string]interface{}) CallToolResult {
	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return mcpErrorResult("Missing required parameter: pattern")
	}

	pol, err := newPathPolicy(workDir)
	if err != nil {
		return mcpErrorResult(err.Error())
	}
	searchPath := pol.root
	if p, ok := args["path"].(string); ok && p != "" {
		resolved, err := pol.resolve(workDir, p, false)
		if err != nil {
			return mcpErrorResult(err.Error())
		}
		searchPath = resolved
	}

	// The files are listed here, never by grep -r: only regular files that the
	// walk reaches without a symbolic link, none of them protected, so a link
	// planted in the project cannot take the search out of the working directory.
	include, _ := args["glob"].(string)
	files, capped := pol.regularFiles(searchPath, include)
	note := ""
	if capped {
		note = fmt.Sprintf("\n... (search stopped after %d files; narrow the path)", maxGrepFiles)
	}
	if len(files) == 0 {
		return CallToolResult{Content: []ContentBlock{{Type: "text", Text: "No matches found"}}}
	}

	var out strings.Builder
	for i := 0; i < len(files) && out.Len() <= 100000; i += 200 {
		end := i + 200
		if end > len(files) {
			end = len(files)
		}
		grepArgs := append([]string{"-Hn", "--color=never", "-e", pattern, "--"}, files[i:end]...)
		o, _ := exec.Command("grep", grepArgs...).CombinedOutput()
		out.Write(o)
	}
	result := out.String()
	if result == "" {
		return CallToolResult{Content: []ContentBlock{{Type: "text", Text: "No matches found" + note}}}
	}
	if len(result) > 100000 {
		result = result[:100000] + "\n... (output truncated)"
	}
	result += note

	return CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: result}},
	}
}

// regularFiles lists the regular files under start (start itself when it is
// one) whose base name matches include (when set), skipping symbolic links,
// protected paths and anything over a sane count.
func (p *pathPolicy) regularFiles(start, include string) (files []string, capped bool) {
	// .git is never searched; node_modules only when the search starts inside it.
	inNodeModules := strings.Contains(filepath.ToSlash(start)+"/", "/node_modules/")
	_ = filepath.WalkDir(start, func(fp string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(files) >= maxGrepFiles {
			capped = true
			return filepath.SkipAll
		}
		if p.isProtected(fp) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() && fp != start && (d.Name() == ".git" || (d.Name() == "node_modules" && !inNodeModules)) {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() {
			return nil // directories are walked; symlinks, sockets, devices skipped
		}
		if include != "" {
			if ok, _ := path.Match(include, d.Name()); !ok {
				return nil
			}
		}
		files = append(files, fp)
		return nil
	})
	return files, capped
}

const maxGrepFiles = 20000

func (p *pathPolicy) isProtected(real string) bool {
	for _, d := range p.noRead {
		if within(real, d) {
			return true
		}
	}
	return false
}

// dropProtected removes protected paths from a newline separated listing.
func (p *pathPolicy) dropProtected(listing string) string {
	var keep []string
	for _, l := range strings.Split(listing, "\n") {
		if l != "" && !p.isProtected(l) {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

// resolveFilePath resolves a potentially relative file path against the working directory.
func resolveFilePath(workDir, filePath string) string {
	if filepath.IsAbs(filePath) {
		return filepath.Clean(filePath)
	}
	return filepath.Clean(filepath.Join(workDir, filePath))
}

// validateFilePath resolves a file path and ensures it stays within the working
// directory. This prevents path traversal attacks (e.g. "../../etc/passwd").
func validateFilePath(workDir, filePath string) (string, error) {
	resolved := resolveFilePath(workDir, filePath)
	// filepath.Clean already resolves ".." components, so we just need to
	// check the resolved path is still under workDir.
	cleanWorkDir := filepath.Clean(workDir)
	if !strings.HasPrefix(resolved, cleanWorkDir+string(filepath.Separator)) && resolved != cleanWorkDir {
		return "", fmt.Errorf("path %q is outside the working directory", filePath)
	}
	return resolved, nil
}

// globPathPattern lists the files under dir whose slash-separated path
// relative to dir matches pattern; "**" matches any number of folders.
func globPathPattern(dir, pattern string, protected []string) CallToolResult {
	pat := strings.Split(strings.Trim(filepath.ToSlash(pattern), "/"), "/")
	for _, seg := range pat {
		if _, err := path.Match(seg, ""); err != nil {
			return mcpErrorResult("Invalid pattern: " + err.Error())
		}
	}
	var hits []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || len(hits) >= 5000 {
			return nil
		}
		for _, pr := range protected {
			if within(p, pr) {
				return nil
			}
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return nil
		}
		if globSegmentsMatch(pat, strings.Split(filepath.ToSlash(rel), "/")) {
			hits = append(hits, rel)
		}
		return nil
	})
	if len(hits) == 0 {
		return CallToolResult{Content: []ContentBlock{{Type: "text", Text: "No matching files found"}}}
	}
	return CallToolResult{Content: []ContentBlock{{Type: "text", Text: strings.Join(hits, "\n")}}}
}

func globSegmentsMatch(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if globSegmentsMatch(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	if ok, _ := path.Match(pat[0], segs[0]); !ok {
		return false
	}
	return globSegmentsMatch(pat[1:], segs[1:])
}

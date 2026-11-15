package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Autopilot: a sandboxed Claude CLI session with permission prompts off.
//
// The CLI's bypass mode would approve every tool call, but the sandbox only
// confines Bash: Write/Edit wrote ~/ and ~/Library/LaunchAgents in a measured
// run (docs/sandbox-grants.md). So such a session starts in the CLI's default
// mode, without the skip flags, and Eggify's permission callback answers every
// request the CLI makes, at once and without a card (autopilotDecide). File
// tools may only change files inside the session's write scope.

// autopilotSession reports whether a Claude CLI session runs in Autopilot:
// sandboxed with prompts off. Codex and direct sessions never reach the CLI
// options this decides, so callers on those paths do not ask.
func autopilotSession(opts *SessionOptions) bool {
	return isSandboxed(opts) && promptsOff(opts)
}

// promptsOff reports whether the session runs without permission prompts, by
// any of the ways the CLI start paths recognise.
func promptsOff(opts *SessionOptions) bool {
	if opts == nil {
		return false
	}
	if opts.DangerouslySkipPermissions != nil && *opts.DangerouslySkipPermissions {
		return true
	}
	if opts.PermissionMode != nil {
		m := strings.ToLower(strings.TrimSpace(*opts.PermissionMode))
		return isYOLOMode(m) || m == "allow-all"
	}
	return false
}

// autopilotScope is what a session's file tools may change.
type autopilotScope struct {
	WorkingDir     string
	WriteRoots     []string // resolved folders the file tools may write in
	ScratchRoots   []string // the write roots that are CLI scratch folders (their tasks/ are protected)
	Protected      []string // never writable, whatever the roots are (both spellings)
	ReadOnly       bool
	AllowedDomains []string
}

// autopilotDecision is the answer to one permission request.
type autopilotDecision struct {
	Handled bool // false: not decided here (AskUserQuestion, ExitPlanMode)
	Allow   bool
	Message string // for a denial, written for the agent
}

// autopilotTempRoots replaces the CLI's temp folders in tests, whose own temp
// folders live there.
var autopilotTempRoots func(uid int) []string

// claudeTempRoots are the folders the CLI gives sandboxed commands as scratch
// space (/tmp/claude-<uid>, also spelled through /private on macOS).
func claudeTempRoots(uid int) []string {
	if autopilotTempRoots != nil {
		return autopilotTempRoots(uid)
	}
	name := fmt.Sprintf("claude-%d", uid)
	return []string{filepath.Join("/tmp", name), filepath.Join("/private/tmp", name)}
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// claudeScratchRoots are the CLI's scratch folders for a working directory:
// <temp root>/<working directory with non-alphanumerics as ->, for the resolved
// and the literal spelling. The temp root itself is shared by every session and
// holds CLI-protected files (settings, bash-edit-diff, each session's tasks).
func claudeScratchRoots(uid int, dirs ...string) []string {
	var out []string
	for _, root := range claudeTempRoots(uid) {
		for _, d := range dirs {
			if d == "" {
				continue
			}
			out = appendUnique(out, filepath.Join(root, nonAlnum.ReplaceAllString(d, "-")))
		}
	}
	return out
}

// inScratchTasks reports whether path lies in a "tasks" folder below one of the roots.
func inScratchTasks(path string, roots []string) bool {
	for _, r := range roots {
		if !pathWithin(path, r) {
			continue
		}
		rel, err := filepath.Rel(r, path)
		if err != nil {
			continue
		}
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if strings.EqualFold(part, "tasks") {
				return true
			}
		}
	}
	return false
}

// newAutopilotScope builds the write scope of a session: its working
// directory, its write path grants and the CLI's scratch folder, minus what the
// sandbox and the deny rules protect.
func newAutopilotScope(home string, opts *SessionOptions, allowedDomains []string) autopilotScope {
	sc := autopilotScope{AllowedDomains: allowedDomains, ReadOnly: opts != nil && (opts.ReadOnly || readOnlySession(opts))}
	wd := sessionWorkingDir(opts)
	if wd != "" {
		sc.WorkingDir = cleanAbs(wd)
		// A working directory that is the home folder or holds it (or "/") would
		// make the whole account the write scope; the sandbox's own limits for
		// commands are unchanged, but the file tools get no folder from it.
		if !scopeTooWide(home, sc.WorkingDir) {
			sc.WriteRoots = append(sc.WriteRoots, sc.WorkingDir)
		}
	}
	if opts != nil {
		for _, gp := range opts.SandboxGrantPaths {
			if !gp.Write {
				continue
			}
			// Same check as at CLI start: a stored path that leads elsewhere now is left out.
			resolved, _, err := resolveGrantPath(home, wd, gp)
			if err != nil || resolved != filepath.Clean(gp.Path) {
				continue
			}
			sc.WriteRoots = appendUnique(sc.WriteRoots, resolved)
		}
	}
	for _, t := range claudeScratchRoots(os.Getuid(), sc.WorkingDir, wd) {
		sc.WriteRoots = appendUnique(sc.WriteRoots, t)
		sc.ScratchRoots = appendUnique(sc.ScratchRoots, t)
		if _, real, err := resolveForWrite(t, ""); err == nil {
			sc.WriteRoots = appendUnique(sc.WriteRoots, real)
			sc.ScratchRoots = appendUnique(sc.ScratchRoots, real)
		}
	}

	prot := sandboxWriteProtected(home, wd)
	if wd != "" {
		// The trusted CLI runs these later, outside the sandbox.
		prot = append(prot, filepath.Join(sc.WorkingDir, ".claude", "hooks"))
	}
	_, noWrite := ProtectedPathsForTools(home, wd)
	prot = append(prot, noWrite...)
	personal := hiddenPersonalSecrets(runtime.GOOS, home, wd, nil)
	prot = append(prot, personal.dirs...)
	prot = append(prot, personal.files...)
	for _, p := range prot {
		sc.Protected = appendUnique(sc.Protected, withResolved(p)...)
	}
	return sc
}

// scopeTooWide reports whether dir is "/", the home folder or a folder holding it.
func scopeTooWide(home, dir string) bool {
	if dir == "/" || dir == "" {
		return true
	}
	return home != "" && pathWithin(cleanAbs(home), dir)
}

// foldPath: macOS and Windows file systems are case-insensitive by default, so
// protected paths are compared without case there (a stricter test than the FS).
func foldPath(p string) string {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

func pathWithinFold(path, dir string) bool { return pathWithin(foldPath(path), foldPath(dir)) }

// resolveForWrite returns the cleaned literal path and where a write to it would
// land: the deepest existing ancestor resolved through symlinks, plus the rest
// (which does not exist, so holds no links). A link that dangles is refused:
// writing through it would create its target.
func resolveForWrite(path, workingDir string) (literal, resolved string, err error) {
	if strings.ContainsRune(path, 0) {
		return "", "", fmt.Errorf("invalid path")
	}
	if strings.HasPrefix(path, "~") {
		return "", "", fmt.Errorf("a path starting with ~ is not absolute")
	}
	if !filepath.IsAbs(path) {
		if workingDir == "" {
			return "", "", fmt.Errorf("a relative path needs a working directory")
		}
		path = filepath.Join(workingDir, path)
	}
	literal = filepath.Clean(path)
	existing, rest := literal, ""
	for {
		fi, lerr := os.Lstat(existing)
		if lerr == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				if _, serr := filepath.EvalSymlinks(existing); serr != nil {
					return literal, "", fmt.Errorf("%s is a link that leads nowhere", existing)
				}
			}
			break
		}
		if !os.IsNotExist(lerr) {
			return literal, "", lerr
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return literal, "", fmt.Errorf("no existing parent folder")
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return literal, "", err
	}
	resolved = filepath.Join(real, rest)
	return literal, resolved, nil
}

// writeToolPathKeys are the built-in file-changing tools and the input key
// that names their target.
var writeToolPathKeys = map[string]string{
	"Write":        "file_path",
	"Edit":         "file_path",
	"MultiEdit":    "file_path",
	"NotebookEdit": "notebook_path",
}

// readToolNames never change files; the deny rules for hidden paths still apply.
var readToolNames = map[string]bool{
	"Read": true, "Glob": true, "Grep": true, "LS": true, "NotebookRead": true,
}

// pathInputKeys are the inputs by which an unknown built-in tool might name a file.
var pathInputKeys = []string{"file_path", "notebook_path"}

// writePathOf returns the file a tool call would change, if it is a file tool.
// A built-in tool Eggify does not know that names a file is treated as a file
// writer too (stricter than letting it through).
func writePathOf(tool string, input map[string]interface{}) (path string, isWrite bool) {
	if key, ok := writeToolPathKeys[tool]; ok {
		p, _ := input[key].(string)
		return p, true
	}
	if readToolNames[tool] || strings.HasPrefix(tool, "mcp__") {
		return "", false
	}
	for _, key := range pathInputKeys {
		if p, ok := input[key].(string); ok && p != "" {
			return p, true
		}
	}
	return "", false
}

func autopilotOutside(scope string) string {
	if scope == "" {
		return "its working directory"
	}
	return scope
}

// checkWrite decides one file write: the target, after cleaning and resolving
// symlinks, must be inside the write scope and not protected.
func (sc autopilotScope) checkWrite(rawPath string) (bool, string) {
	if sc.ReadOnly {
		return false, "This session is read-only: it cannot change files."
	}
	deny := fmt.Sprintf("This session is sandboxed: it can only change files inside %s. Ask the user to add this folder to the session's access.", autopilotOutside(sc.WorkingDir))
	if strings.TrimSpace(rawPath) == "" {
		return false, "This session is sandboxed: the file path is missing."
	}
	literal, resolved, err := resolveForWrite(rawPath, sc.WorkingDir)
	if err != nil {
		return false, fmt.Sprintf("This session is sandboxed: cannot check %s (%v).", rawPath, err)
	}
	for _, p := range []string{literal, resolved} {
		for _, prot := range sc.Protected {
			if pathWithinFold(p, prot) {
				return false, fmt.Sprintf("This session is sandboxed: %s is protected (it could run code outside the sandbox or holds credentials), so it cannot be changed here.", rawPath)
			}
		}
	}
	if inScratchTasks(resolved, sc.ScratchRoots) || inScratchTasks(literal, sc.ScratchRoots) {
		return false, fmt.Sprintf("This session is sandboxed: %s is a CLI task folder, which it cannot change.", rawPath)
	}
	for _, root := range sc.WriteRoots {
		if pathWithin(resolved, root) {
			return true, ""
		}
	}
	// Say why when a link inside the project is what leads out.
	for _, root := range sc.WriteRoots {
		if pathWithin(literal, root) && !pathWithin(resolved, root) {
			return false, fmt.Sprintf("This session is sandboxed: %s is inside the project but leads through a symbolic link to %s, outside what it can change. Ask the user to add that folder to the session's access.", rawPath, resolved)
		}
	}
	return false, deny
}

// autopilotDecide answers one permission request of an Autopilot session. It
// is a pure function of the tool, its input and the session's scope.
//
//	file tools        allowed inside the write scope, else denied
//	Bash              allowed (the sandbox confines it); denied when it asks to leave the sandbox
//	WebFetch/Search   allowed only to allowlisted hosts (WebSearch never), else denied
//	AskUserQuestion, ExitPlanMode  not decided here: they reach the user as usual
//	everything else   allowed (read tools, MCP tools, Agent/Task, TodoWrite, ...)
func autopilotDecide(sc autopilotScope, tool string, input map[string]interface{}) autopilotDecision {
	switch tool {
	case "AskUserQuestion", "ExitPlanMode":
		return autopilotDecision{}
	case "Bash":
		if v, _ := input["dangerouslyDisableSandbox"].(bool); v {
			return autopilotDecision{Handled: true, Message: "This session is sandboxed: commands cannot run outside the sandbox."}
		}
		return autopilotDecision{Handled: true, Allow: true}
	case "WebFetch", "WebSearch":
		_, allow := sandboxWebToolDecision(true, tool, input, sc.AllowedDomains)
		if allow {
			return autopilotDecision{Handled: true, Allow: true}
		}
		return autopilotDecision{Handled: true, Message: fmt.Sprintf("This session is sandboxed: %s is only allowed to sites on the allowlist (WebSearch is not available). Ask the user to add the site in Settings → Sandbox.", tool)}
	}
	if path, isWrite := writePathOf(tool, input); isWrite {
		if ok, msg := sc.checkWrite(path); !ok {
			return autopilotDecision{Handled: true, Message: msg}
		}
	}
	return autopilotDecision{Handled: true, Allow: true}
}

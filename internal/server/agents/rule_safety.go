package agents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schlunsen/eggify/internal/logging"
)

// Always-allow rules let a session skip the permission prompt. An agent that
// has been steered by untrusted text will happily build a command that fits an
// existing rule, so a rule must never cover more than the user meant to allow:
//
//   - rules that cover everything (Bash(*), Bash(:*), Read(**), /**, ~/**, /)
//     are refused when created and ignored when loaded;
//   - Bash prefix rules match whole words and never auto-approve a command that
//     contains a shell operator, expansion or redirection (see parseSimpleCommand),
//     or an option that makes the program run other code, such as git -c or
//     find -exec (see commandRunsCodeViaOptions);
//   - file rules compare paths after resolving "..", "~" and symlinks.

// ErrUnsafeRule is wrapped by every validation error below so callers can
// tell a refused rule apart from other failures.
var ErrUnsafeRule = errors.New("unsafe always-allow rule")

func unsafeRule(format string, args ...interface{}) error {
	return fmt.Errorf("%w: %s", ErrUnsafeRule, fmt.Sprintf(format, args...))
}

// fileRuleTools are the tools whose rules are scoped by a directory.
var fileRuleTools = map[string]bool{"Read": true, "Write": true, "Edit": true}

// toolsWithoutBlanketRules can only get exact or scoped rules: allowing every
// use of them is the same as turning permissions off.
var toolsWithoutBlanketRules = map[string]bool{
	"Bash": true, "Read": true, "Write": true, "Edit": true,
	"MultiEdit": true, "NotebookEdit": true,
}

// eggifyToolsPrefix is how the Claude CLI names tools of Eggify's own MCP server.
const eggifyToolsPrefix = "mcp__eggify-tools__"

// neverAutoAllowedEggifyTools run commands or write files outside the CLI's own
// permission model, so no always-allow rule may cover them.
var neverAutoAllowedEggifyTools = map[string]bool{
	"Bash": true, "Write": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true,
	"gpu_run": true, "gpu_push": true, "runpod__ssh_exec": true,
	"deploy_app": true, "invoke_skill": true, "create_skill_from_handover": true,
}

// isNeverAutoAllowedTool reports whether toolName may not get any always-allow rule.
func isNeverAutoAllowedTool(toolName string) bool {
	if toolName == "mcp__eggify-tools" || toolName == "mcp__eggify-tools__*" {
		return true
	}
	if !strings.HasPrefix(toolName, eggifyToolsPrefix) {
		return false
	}
	return neverAutoAllowedEggifyTools[strings.TrimPrefix(toolName, eggifyToolsPrefix)]
}

// commandWrappers run whatever command follows them, so a prefix rule for one
// of them would allow every command.
var commandWrappers = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "csh": true, "tcsh": true,
	"eval": true, "exec": true, "source": true, ".": true, "command": true, "builtin": true,
	"sudo": true, "su": true, "doas": true, "env": true, "xargs": true, "nohup": true,
	"time": true, "timeout": true, "nice": true, "watch": true, "stdbuf": true, "caffeinate": true,
}

// ValidateBashPrefix checks a Bash command prefix (the "git" in Bash(git:*)).
// A prefix is one or more plain words: no quotes, operators or wildcards, no
// leading VAR=value, and not a wrapper that runs arbitrary commands.
func ValidateBashPrefix(prefix string) error {
	p := strings.TrimSpace(prefix)
	if p == "" {
		return unsafeRule("an empty Bash prefix would allow every command; pick a command such as \"git\" or \"npm test\"")
	}
	for _, r := range p {
		if !isPlainPrefixRune(r) {
			return unsafeRule("Bash prefix %q may only contain plain words (letters, digits, spaces and ._/@%%+=:,-)", prefix)
		}
	}
	words := strings.Fields(p)
	if isEnvAssignment(words[0]) {
		return unsafeRule("Bash prefix %q starts with an environment variable assignment", prefix)
	}
	if commandWrappers[filepath.Base(words[0])] {
		return unsafeRule("Bash prefix %q would allow any command (%s runs other commands)", prefix, words[0])
	}
	if strings.Trim(words[0], ".:/-") == "" {
		return unsafeRule("Bash prefix %q is not a command name", prefix)
	}
	return nil
}

func isPlainPrefixRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune(" ._/@%+=:,-", r)
}

// isEnvAssignment reports whether a raw word looks like NAME=value.
func isEnvAssignment(word string) bool {
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	for i, r := range word[:eq] {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

// ValidateDirectoryScope checks the directory of a Read/Write/Edit rule. It must
// be a concrete absolute directory (or ~/something) that is neither the
// filesystem root nor the home directory nor one of its ancestors.
func ValidateDirectoryScope(dir string) error {
	base := trimDirGlob(dir)
	if base == "" || base == "~" || base == "." {
		return unsafeRule("file rule %q would cover every file; choose a specific directory", dir)
	}
	if strings.ContainsAny(base, "*?[") {
		return unsafeRule("file rule %q must be a plain directory without wildcards", dir)
	}
	abs, err := expandRulePath(base)
	if err != nil {
		return unsafeRule("file rule %q: %v", dir, err)
	}
	if abs == string(filepath.Separator) || resolvePathForMatch(abs) == string(filepath.Separator) {
		return unsafeRule("file rule %q would cover the whole filesystem", dir)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		home = filepath.Clean(home)
		for _, h := range []string{home, resolvePathForMatch(home)} {
			for _, a := range []string{abs, resolvePathForMatch(abs)} {
				if isWithin(h, a) { // a is the home directory or one of its ancestors
					return unsafeRule("file rule %q would cover your whole home directory", dir)
				}
			}
		}
	}
	return nil
}

// trimDirGlob strips trailing "/**", "/*", "**" and slashes from a rule
// directory: "/a/b/**" -> "/a/b", "/**" -> "", "**" -> "".
func trimDirGlob(dir string) string {
	d := strings.TrimSpace(dir)
	for {
		prev := d
		d = strings.TrimSuffix(d, "**")
		d = strings.TrimSuffix(d, "*")
		d = strings.TrimRight(d, "/")
		if d == prev {
			return d
		}
	}
}

// expandRulePath turns a rule directory into a clean absolute path. Relative
// paths are refused: they would be resolved against the server's cwd.
func expandRulePath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("directory must be an absolute path")
	}
	return filepath.Clean(p), nil
}

// ValidateAlwaysAllowRule checks a rule before it is stored or used.
func ValidateAlwaysAllowRule(rule AlwaysAllowRule) error {
	tool := strings.TrimSpace(rule.Tool)
	if tool == "" {
		return unsafeRule("rule has no tool")
	}
	if tool != rule.Tool || strings.ContainsAny(tool, "()*") {
		return unsafeRule("invalid tool name %q", rule.Tool)
	}
	if isNeverAutoAllowedTool(tool) {
		return unsafeRule("%s runs commands or writes files and always needs your approval", tool)
	}

	switch rule.MatchMode {
	case RuleMatchExact:
		if len(rule.Parameters) == 0 {
			return unsafeRule("exact rule for %s has no parameters", tool)
		}
		if tool == "Bash" {
			cmd, _ := rule.Parameters["command"].(string)
			if strings.TrimSpace(cmd) == "" {
				return unsafeRule("exact Bash rule has no command")
			}
		}
		return nil

	case RuleMatchPattern:
		p := rule.Pattern
		switch {
		case tool == "Bash":
			if p == nil || p.CommandPrefix == nil {
				return unsafeRule("Bash rules need a command prefix")
			}
			return ValidateBashPrefix(*p.CommandPrefix)
		case fileRuleTools[tool]:
			if p == nil || p.DirectoryPath == nil {
				return unsafeRule("%s rules need a directory", tool)
			}
			return ValidateDirectoryScope(*p.DirectoryPath)
		case toolsWithoutBlanketRules[tool]:
			return unsafeRule("%s can only be allowed for exact calls", tool)
		}
		return nil
	}
	return unsafeRule("unknown match mode %q", rule.MatchMode)
}

// SanitizeAlwaysAllowRules drops rules that ValidateAlwaysAllowRule refuses and
// logs a warning for each, so over-broad rules stored by older versions (in
// the DB or in a handover) stop taking effect.
func SanitizeAlwaysAllowRules(rules []AlwaysAllowRule, source string) []AlwaysAllowRule {
	if len(rules) == 0 {
		return rules
	}
	kept := make([]AlwaysAllowRule, 0, len(rules))
	for _, r := range rules {
		if err := ValidateAlwaysAllowRule(r); err != nil {
			logging.Warning("⚠️ Ignoring unsafe always-allow rule from %s (tool=%s, desc=%q): %v", source, r.Tool, r.Description, err)
			continue
		}
		kept = append(kept, r)
	}
	return kept
}

// ValidatePermissionString checks a permission string in the Claude Code
// settings format (as written to .claude/settings.local.json), e.g.
// "Bash(git:*)", "Read(//home/me/project/**)".
func ValidatePermissionString(perm string) error {
	perm = strings.TrimSpace(perm)
	open := strings.IndexByte(perm, '(')
	tool, spec, hasSpec := perm, "", false
	if open >= 0 {
		if !strings.HasSuffix(perm, ")") {
			return unsafeRule("invalid permission format %q", perm)
		}
		tool, spec, hasSpec = perm[:open], perm[open+1:len(perm)-1], true
	}
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return unsafeRule("permission %q has no tool", perm)
	}
	if isNeverAutoAllowedTool(tool) {
		return unsafeRule("%s runs commands or writes files and always needs your approval", tool)
	}
	spec = strings.TrimSpace(spec)
	blanket := !hasSpec || spec == "" || spec == "*" || spec == "**"

	switch {
	case tool == "Bash":
		if blanket {
			return unsafeRule("%q would allow every Bash command; use a prefix such as Bash(git:*)", perm)
		}
		if strings.HasSuffix(spec, ":*") {
			return ValidateBashPrefix(strings.TrimSuffix(spec, ":*"))
		}
		if strings.HasSuffix(spec, " *") && !strings.Contains(strings.TrimSuffix(spec, " *"), "*") {
			return ValidateBashPrefix(strings.TrimSuffix(spec, " *"))
		}
		if strings.Contains(spec, "*") {
			return unsafeRule("%q: wildcards are only allowed at the end of a Bash prefix", perm)
		}
		return nil
	case fileRuleTools[tool]:
		if blanket {
			return unsafeRule("%q would cover every file; choose a specific directory", perm)
		}
		dir := spec
		if strings.HasPrefix(dir, "//") {
			dir = dir[1:]
		} else if !strings.HasPrefix(dir, "~") {
			// Claude Code resolves "/x" and "x" against the project; only the
			// pure wildcard forms are a problem there.
			if strings.Trim(dir, "/*.") == "" {
				return unsafeRule("%q would cover every file; choose a specific directory", perm)
			}
			return nil
		}
		return ValidateDirectoryScope(dir)
	case toolsWithoutBlanketRules[tool]:
		if blanket {
			return unsafeRule("%q would allow every %s call", perm, tool)
		}
	}
	return nil
}

// pruneUnsafeSettingsPermissions removes permissions that ValidatePermissionString
// refuses from <workingDir>/.claude/settings.local.json, where older versions of
// Eggify wrote always-allow rules and the Claude CLI reads them from. Called (via
// SessionManager.pruneLegacySettingsPermissions, inside WithOwnWrite) before the
// CLI is started so older, over-broad entries stop taking effect.
func pruneUnsafeSettingsPermissions(workingDir string) {
	if workingDir == "" {
		return
	}
	csm := NewClaudeSettingsManager(workingDir)
	if _, err := os.Stat(csm.getSettingsPath()); err != nil {
		return
	}
	settings, err := csm.LoadSettings()
	if err != nil {
		logging.Warning("Could not check %s for unsafe permissions: %v", csm.getSettingsPath(), err)
		return
	}
	kept := make([]string, 0, len(settings.Permissions.Allow))
	for _, p := range settings.Permissions.Allow {
		if err := ValidatePermissionString(p); err != nil {
			logging.Warning("⚠️ Removing unsafe permission %q from %s: %v", p, csm.getSettingsPath(), err)
			continue
		}
		kept = append(kept, p)
	}
	if len(kept) == len(settings.Permissions.Allow) {
		return
	}
	settings.Permissions.Allow = kept
	if err := csm.SaveSettings(settings); err != nil {
		logging.Warning("Could not remove unsafe permissions from %s: %v", csm.getSettingsPath(), err)
	}
}

// parseSimpleCommand splits a shell command into words (quotes removed) if it
// is a single simple command: no ; & | && || newlines, no $ or backtick
// expansions (even in double quotes), no redirections or process
// substitution, no subshells or comments, no leading VAR=value, and no
// unquoted globs or word-initial ~ / = (both expand).
// Anything else returns ok=false, which means an always-allow prefix rule
// never approves it and the user is asked instead.
func parseSimpleCommand(cmd string) (words []string, ok bool) {
	var (
		cur     strings.Builder
		inWord  bool
		rawWord strings.Builder // the word as written, for the VAR= check
	)
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			if len(words) == 1 && isEnvAssignment(rawWord.String()) {
				ok = false
			}
		}
		cur.Reset()
		rawWord.Reset()
		inWord = false
	}
	ok = true
	runes := []rune(cmd)
	for i := 0; i < len(runes) && ok; i++ {
		r := runes[i]
		if r < 0x20 && r != '\t' || r == 0x7f {
			return nil, false // newlines, NUL and other control characters
		}
		switch r {
		case ' ', '\t':
			flush()
		case ';', '&', '|', '<', '>', '(', ')', '$', '`', '{', '}':
			return nil, false
		case '#':
			if !inWord {
				return nil, false
			}
			cur.WriteRune(r)
			rawWord.WriteRune(r)
		case '\\':
			if i+1 >= len(runes) {
				return nil, false
			}
			i++
			if runes[i] < 0x20 || runes[i] == 0x7f {
				return nil, false
			}
			cur.WriteRune(runes[i])
			rawWord.WriteRune('\\')
			rawWord.WriteRune(runes[i])
			inWord = true
		case '\'':
			inWord = true
			rawWord.WriteRune(r)
			j := i + 1
			for ; j < len(runes) && runes[j] != '\''; j++ {
				if runes[j] < 0x20 && runes[j] != '\t' || runes[j] == 0x7f {
					return nil, false
				}
				cur.WriteRune(runes[j])
			}
			if j >= len(runes) {
				return nil, false // unterminated quote
			}
			i = j
		case '"':
			inWord = true
			rawWord.WriteRune(r)
			j := i + 1
			for ; j < len(runes) && runes[j] != '"'; j++ {
				c := runes[j]
				if c < 0x20 && c != '\t' || c == 0x7f || c == '$' || c == '`' {
					return nil, false
				}
				if c == '\\' && j+1 < len(runes) {
					switch runes[j+1] {
					case '"', '\\':
						j++
						c = runes[j]
					case '$', '`':
						return nil, false
					}
				}
				cur.WriteRune(c)
			}
			if j >= len(runes) {
				return nil, false // unterminated quote
			}
			i = j
		case '*', '?', '[':
			// An unquoted glob expands to file names the checks never see
			// (a file named "--checkpoint-action=exec=sh x" in the repo).
			return nil, false
		case '~', '=':
			if !inWord {
				return nil, false // tilde / zsh =cmd expansion at a word start
			}
			cur.WriteRune(r)
			rawWord.WriteRune(r)
		default:
			cur.WriteRune(r)
			rawWord.WriteRune(r)
			inWord = true
		}
	}
	if !ok {
		return nil, false
	}
	flush()
	if !ok || len(words) == 0 {
		return nil, false
	}
	return words, true
}

// bashCommandMatchesPrefix reports whether command is a simple command whose
// leading words are exactly the words of prefix.
func bashCommandMatchesPrefix(prefix, command string) bool {
	if ValidateBashPrefix(prefix) != nil {
		return false
	}
	want := strings.Fields(prefix)
	got, ok := parseSimpleCommand(command)
	if !ok || len(got) < len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	// The rule allows the program, not code its options make it run.
	if reason := commandRunsCodeViaOptions(got); reason != "" {
		logging.Warning("⚠️ Bash prefix rule %q does not cover %q: %s", prefix, command, reason)
		return false
	}
	return true
}

// resolvePathForMatch cleans an absolute path and resolves symlinks in the
// longest prefix of it that exists, so "dir/link/../x" and "dir/link-to-etc/x"
// are compared by where they really point.
func resolvePathForMatch(p string) string {
	p = filepath.Clean(p)
	rest := ""
	cur := p
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return resolved
			}
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		if rest == "" {
			rest = filepath.Base(cur)
		} else {
			rest = filepath.Join(filepath.Base(cur), rest)
		}
		cur = parent
	}
}

// isWithin reports whether path is dir or inside it (both clean and absolute).
func isWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// filePathInDirectory reports whether filePath lies inside the rule directory
// after "~", "..", and symlinks are resolved. Relative file paths never match.
func filePathInDirectory(filePath, ruleDir string) bool {
	if ValidateDirectoryScope(ruleDir) != nil {
		return false
	}
	dir, err := expandRulePath(trimDirGlob(ruleDir))
	if err != nil {
		return false
	}
	if filePath == "" || strings.ContainsRune(filePath, 0) {
		return false
	}
	fp, err := expandRulePath(filePath)
	if err != nil {
		return false
	}
	return isWithin(resolvePathForMatch(fp), resolvePathForMatch(dir))
}

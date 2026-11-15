// Package gitexec runs git for the server itself, in folders an agent can
// write to.
//
// The server's own git (status, diff, ls-files, worktree, ...) runs outside
// any session sandbox, so repository config must not make it run commands.
// The sandbox only protects <wd>/.git/config and .git/hooks; git also reads
// config through .git/commondir, a .git pointer file, linked worktrees and
// submodules, and .gitattributes can name a filter. The settings below are
// passed as command-line config (GIT_CONFIG_COUNT), which wins over every
// config file, and also reach child gits (submodules).
package gitexec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// EmptyTree is git's empty tree; as the attribute source it keeps the work
// tree's .gitattributes (filters, textconv drivers) from being read.
const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// hardened is config that would otherwise let a repository run commands.
var hardened = [][2]string{
	{"core.fsmonitor", "false"},          // runs on status/diff/ls-files
	{"core.attributesFile", "/dev/null"}, // a global attributes file can name filters too
	{"core.hooksPath", "/dev/null"},      // worktree add: post-checkout
	{"core.pager", "cat"},
	{"core.sshCommand", "false"},
	{"diff.ignoreSubmodules", "all"}, // a submodule's own config is agent-writable
	{"protocol.allow", "never"},      // no fetches from local inspection
	{"protocol.https.allow", "never"},
	{"uploadpack.packObjectsHook", ""},
	// Signature checks run the configured verifier: with log.showSignature a
	// commit carrying a gpgsig header makes log/show run gpg.program, whatever
	// the format. Off, and every verifier pinned to a program that does nothing
	// (git runs these directly, no shell), for %G placeholders and the like.
	{"log.showSignature", "false"},
	{"gpg.program", "false"},
	{"gpg.openpgp.program", "false"}, // the same setting under its newer name
	{"gpg.ssh.program", "false"},
	{"gpg.x509.program", "false"},
}

// Env returns base (os.Environ() when nil) with the hardening applied.
func Env(base []string) []string {
	if base == nil {
		base = os.Environ()
	}
	out := make([]string, 0, len(base)+8+2*len(hardened))
	for _, kv := range base {
		// Our own count must win, and an inherited external diff is not ours.
		if strings.HasPrefix(kv, "GIT_CONFIG_") || strings.HasPrefix(kv, "GIT_EXTERNAL_DIFF=") {
			continue
		}
		out = append(out, kv)
	}
	out = append(out,
		fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(hardened)),
		"GIT_PAGER=cat",
		"GIT_ATTR_SOURCE="+EmptyTree,
		"GIT_TERMINAL_PROMPT=0",
	)
	for i, kv := range hardened {
		out = append(out, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, kv[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, kv[1]))
	}
	return out
}

// Args adds --no-ext-diff --no-textconv to the commands that can run an
// external diff or textconv driver (diff, log, show): no config value turns
// diff.external off, only the flag does. log and show also get
// --no-show-signature, a second layer over log.showSignature=false.
func Args(args []string) []string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-C" || a == "-c" {
			i++ // takes a value
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		switch a {
		case "diff", "log", "show":
			out := append([]string(nil), args[:i+1]...)
			out = append(out, "--no-ext-diff", "--no-textconv")
			if a != "diff" {
				out = append(out, "--no-show-signature")
			}
			return append(out, args[i+1:]...)
		}
		return args
	}
	return args
}

// Command is exec.Command("git", args...) with the hardened environment.
// Callers that set more variables append to cmd.Env, and call Harden once
// cmd.Dir is set (Command does it itself for "-C dir").
func Command(args ...string) *exec.Cmd {
	cmd := exec.Command("git", Args(args)...)
	cmd.Env = Env(nil)
	if dirArg(args) != "" {
		Harden(cmd)
	}
	return cmd
}

// CommandContext is exec.CommandContext(ctx, "git", args...) with the
// hardened environment; see Command.
func CommandContext(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", Args(args)...)
	cmd.Env = Env(nil)
	if dirArg(args) != "" {
		Harden(cmd)
	}
	return cmd
}

// Harden turns off the filter drivers the repository at cmd's folder (or its
// -C dir) defines. A filter named in $GIT_DIR/info/attributes runs on status
// and diff whatever the attribute source is, and no config key turns all
// filters off, so each one is blanked by name. Reading config runs nothing.
// Call it after setting cmd.Dir and before running cmd; it is idempotent.
func Harden(cmd *exec.Cmd) {
	dir := dirArg(cmd.Args[1:])
	if dir == "" {
		dir = cmd.Dir
	}
	names := filterNames(dir)
	if len(names) == 0 {
		return
	}
	count := len(hardened)
	env := cmd.Env[:0:0]
	for _, kv := range cmd.Env {
		if !strings.HasPrefix(kv, "GIT_CONFIG_COUNT=") {
			env = append(env, kv)
		}
	}
	for _, n := range names {
		for _, kv := range [][2]string{{"clean", ""}, {"smudge", ""}, {"process", ""}, {"required", "false"}} {
			env = append(env,
				fmt.Sprintf("GIT_CONFIG_KEY_%d=filter.%s.%s", count, n, kv[0]),
				fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", count, kv[1]))
			count++
		}
	}
	cmd.Env = append(env, fmt.Sprintf("GIT_CONFIG_COUNT=%d", count))
}

// filterNames lists the filter drivers git's config defines for dir.
func filterNames(dir string) []string {
	args := []string{"config", "--name-only", "--get-regexp", `^filter\.`}
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", args...)
	cmd.Env = Env(nil)
	out, err := cmd.Output()
	if err != nil {
		return nil // exit 1: no filters
	}
	seen := map[string]bool{}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "filter.")
		if !ok {
			continue
		}
		i := strings.LastIndex(rest, ".")
		if i <= 0 {
			continue
		}
		if n := rest[:i]; !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	return names
}

// dirArg is the value of a leading "-C dir" among git's global options.
func dirArg(args []string) string {
	dir := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" && i+1 < len(args):
			dir = args[i+1] // later -C values are relative to earlier ones; callers pass one
			i++
		case a == "-c" && i+1 < len(args):
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return dir
		}
	}
	return dir
}

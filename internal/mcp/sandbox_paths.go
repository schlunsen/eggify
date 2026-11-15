package mcp

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/schlunsen/eggify/internal/server/agents"
	"golang.org/x/text/unicode/norm"
)

// Path policy of eggify-tools' own file tools (Read, Write, Edit, Glob, Grep).
// They run in the MCP process, outside the Claude CLI's and Codex's sandboxes,
// so they enforce the rules themselves:
//   - the path is resolved through symlinks first (the longest existing prefix
//     for a file that does not exist yet) and the real location must lie inside
//     the working directory's real location;
//   - the real location must not be one of Eggify's protected paths or another
//     tool's credential folder (agents.ProtectedPathsForTools), and writes not
//     one of the write-protected folders either;
//   - files are opened through an os.Root of the working directory with
//     O_NOFOLLOW for the last component, so a link swapped in after the check
//     is refused, and nothing is ever written through a symlink.
//
// The direct loop's Bash tool is not sandboxed (direct sessions cannot be), so
// this is a guard for the file tools, not a boundary for the session.

type pathPolicy struct {
	root    string // true path of the working directory
	noRead  []string
	noWrite []string
}

// foldPath is the comparison form of a path: on volumes that ignore case and
// Unicode normalisation (macOS, Windows) ".SSH", ".ssh" and a decomposed or
// compatibility spelling are one name, so protected paths are compared folded.
func foldPath(p string) string {
	if !foldPaths {
		return p
	}
	return strings.ToLower(norm.NFC.String(p))
}

// truePath is the real, true-case path of an existing path (through an open
// descriptor), or p itself where the platform cannot tell.
func truePath(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return p
	}
	defer f.Close()
	if t := fdPath(f); t != "" {
		return t
	}
	return p
}

func newPathPolicy(workDir string) (*pathPolicy, error) {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("working directory is not available: %w", err)
	}
	real = truePath(real)
	home, _ := os.UserHomeDir()
	nr, nw := agents.ProtectedPathsForTools(home, real)
	return &pathPolicy{root: real, noRead: withTrue(nr), noWrite: withTrue(nw)}, nil
}

// withTrue adds the true-case spelling of every protected path that exists.
func withTrue(paths []string) []string {
	out := append([]string(nil), paths...)
	for _, p := range paths {
		if t := truePath(p); t != p {
			out = append(out, t)
		}
	}
	return out
}

func within(path, dir string) bool {
	path, dir = foldPath(path), foldPath(dir)
	if path == dir {
		return true
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// check runs the whole policy on a real path: inside the working directory,
// not protected, and for writes not write-protected.
func (p *pathPolicy) check(real string, write bool) error {
	if !within(real, p.root) {
		return fmt.Errorf("path %q resolves outside the working directory", real)
	}
	for _, d := range p.noRead {
		if within(real, d) {
			return fmt.Errorf("path %q is protected", real)
		}
	}
	if write {
		for _, d := range p.noWrite {
			if within(real, d) {
				return fmt.Errorf("path %q is protected from writing", real)
			}
		}
	}
	return nil
}

// realPath resolves symlinks in path; for a path that does not exist yet it
// resolves the longest existing prefix and appends the rest. A dangling
// symlink is refused: writing to it would create its target.
func realPath(path string) (string, error) {
	var rest []string
	cur := path
	for {
		fi, err := os.Lstat(cur)
		if err == nil {
			r, rerr := filepath.EvalSymlinks(cur)
			if rerr != nil {
				if fi.Mode()&fs.ModeSymlink != 0 {
					return "", fmt.Errorf("%q is a dangling or looping symbolic link", cur)
				}
				return "", rerr
			}
			for i := len(rest) - 1; i >= 0; i-- {
				r = filepath.Join(r, rest[i])
			}
			return r, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}

// trueTarget is where a path really is: its longest existing prefix replaced
// by that prefix's true-case path (taken from an open descriptor), plus the
// part that does not exist yet. Typing ".SSH" for ".ssh" gives ".ssh".
func trueTarget(real string) string {
	var rest []string
	cur := real
	for {
		if _, err := os.Lstat(cur); err == nil {
			t := truePath(cur)
			for i := len(rest) - 1; i >= 0; i-- {
				t = filepath.Join(t, rest[i])
			}
			return t
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return real
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}

// resolve returns the real path of filePath (relative to the working
// directory) after the policy checks, on the path as typed and on its true
// spelling.
func (p *pathPolicy) resolve(workDir, filePath string, write bool) (string, error) {
	lex := resolveFilePath(workDir, filePath)
	if _, err := validateFilePath(workDir, filePath); err != nil {
		return "", err
	}
	real, err := realPath(lex)
	if err != nil {
		return "", fmt.Errorf("cannot resolve %q: %v", filePath, err)
	}
	// The root is held in its true spelling; a working directory typed in
	// another case must not make every path look outside it.
	if !within(real, p.root) {
		return "", fmt.Errorf("path %q resolves outside the working directory", filePath)
	}
	if err := p.check(real, write); err != nil {
		return "", fmt.Errorf("path %q is protected", filePath)
	}
	if err := p.check(trueTarget(real), write); err != nil {
		return "", fmt.Errorf("path %q is protected", filePath)
	}
	return real, nil
}

func (p *pathPolicy) open() (*os.Root, error) {
	return os.OpenRoot(p.root)
}

// relTo is the path relative to the working directory root, taken from the
// true spelling so a differently typed case still opens the same file.
func (p *pathPolicy) relTo(real string) (string, error) {
	rel, err := filepath.Rel(p.root, trueTarget(real))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path resolves outside the working directory")
	}
	return rel, nil
}

// readFile reads a policy-checked real path through the root, refusing a
// symbolic link in the last component, and re-runs the policy on the file's
// true path before returning any data (closing the check-then-open race and
// case or normalisation spellings the first check could not see).
func (p *pathPolicy) readFile(real string) ([]byte, error) {
	root, err := p.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	rel, err := p.relTo(real)
	if err != nil {
		return nil, err
	}
	f, err := root.OpenFile(rel, os.O_RDONLY|noFollowFlag, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if t := fdPath(f); t != "" {
		if err := p.check(t, false); err != nil {
			return nil, fmt.Errorf("path is protected")
		}
	}
	return io.ReadAll(f)
}

// writeFile writes a policy-checked real path through the root: it never
// follows a symlink in the last component and keeps the mode of an existing
// file. Nothing is created or truncated before the true location is checked:
// the nearest existing folder is checked by its true path first, and the
// file is opened without O_TRUNC, checked by its descriptor, then truncated.
func (p *pathPolicy) writeFile(real string, data []byte) error {
	if err := p.check(trueTarget(real), true); err != nil {
		return fmt.Errorf("path is protected from writing")
	}
	root, err := p.open()
	if err != nil {
		return err
	}
	defer root.Close()
	rel, err := p.relTo(real)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(rel); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|noFollowFlag, 0o644)
	if err != nil {
		return err
	}
	if t := fdPath(f); t != "" {
		if err := p.check(t, true); err != nil {
			f.Close()
			return fmt.Errorf("path is protected from writing")
		}
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

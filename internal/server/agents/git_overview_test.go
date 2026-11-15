package agents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newOverviewRepo builds a repository with a default branch, a feature branch
// checked out in a linked worktree, a merged branch, and uncommitted work
// (staged, unstaged, untracked text, binary and a symlink) in the main checkout.
func newOverviewRepo(t *testing.T) (root, wtPath string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root = t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Ada", "GIT_AUTHOR_EMAIL=a@x", "GIT_COMMITTER_NAME=Ada", "GIT_COMMITTER_EMAIL=a@x", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run(root, "init", "-q", "-b", "main")
	write("README.md", "hello\nworld\n")
	write("old name.txt", "rename me\n")
	run(root, "add", ".")
	run(root, "commit", "-q", "-m", "first")

	run(root, "branch", "merged-one")
	run(root, "checkout", "-q", "-b", "feature/x")
	write("feature.go", "package x\n")
	write("README.md", "hello\nthere\nworld\n")
	run(root, "mv", "old name.txt", "new name.txt")
	run(root, "add", "-A")
	run(root, "commit", "-q", "-m", "feature work")
	run(root, "checkout", "-q", "main")

	wtPath = filepath.Join(root, ".worktrees", "x")
	run(root, "worktree", "add", "-q", wtPath, "feature/x")

	write("README.md", "hello\nworld\nagain\n") // unstaged
	write("staged.txt", "s\n")
	run(root, "add", "staged.txt")
	write("notes.txt", "a\nb") // untracked, no trailing newline
	if err := os.WriteFile(filepath.Join(root, "pic.png"), []byte("\x89PNG\r\n\x1a\n\x00\x00binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(secret, []byte("TOP SECRET"), 0o600)
	if err := os.Symlink(secret, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	return root, wtPath
}

func TestGetRepoOverview(t *testing.T) {
	root, wtPath := newOverviewRepo(t)
	ov, err := GetRepoOverview(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if ov.DefaultBranch != "main" || ov.CurrentBranch != "main" {
		t.Fatalf("default=%q current=%q", ov.DefaultBranch, ov.CurrentBranch)
	}
	branches := map[string]GitOverviewBranch{}
	for _, b := range ov.Branches {
		branches[b.Name] = b
	}
	f, ok := branches["feature/x"]
	if !ok || f.BaseAhead != 1 || f.BaseBehind != 0 || f.Merged || f.Worktree != wtPath || f.Subject != "feature work" {
		t.Fatalf("feature/x = %+v", f)
	}
	if !branches["merged-one"].Merged || !branches["main"].IsDefault || !branches["main"].Current {
		t.Fatalf("branches = %+v", ov.Branches)
	}
	if len(ov.Worktrees) != 2 {
		t.Fatalf("worktrees = %+v", ov.Worktrees)
	}
	main, linked := ov.Worktrees[0], ov.Worktrees[1]
	if !main.IsMain || main.Dirty == nil || main.Dirty.Staged != 1 || main.Dirty.Unstaged != 1 || main.Dirty.Untracked != 4 /* + the .worktrees folder */ {
		t.Fatalf("main worktree = %+v dirty=%+v", main, main.Dirty)
	}
	if linked.Path != wtPath || linked.Branch != "feature/x" || linked.BaseAhead != 1 || linked.Dirty.Total() != 0 || linked.LastCommit == nil {
		t.Fatalf("linked worktree = %+v", linked)
	}
	if len(ov.RecentCommits) != 1 || ov.RecentCommits[0].Subject != "first" {
		t.Fatalf("recent = %+v", ov.RecentCommits)
	}
}

func TestGetUncommittedChanges(t *testing.T) {
	root, _ := newOverviewRepo(t)
	ch, err := GetUncommittedChanges(context.Background(), root, "")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]GitChangeFile{}
	for _, f := range ch.Files {
		files[f.Path] = f
	}
	if r := files["README.md"]; r.Status != "modified" || !r.Unstaged || r.Staged || r.Additions != 1 || !strings.Contains(r.Diff, "+again") {
		t.Fatalf("README = %+v", r)
	}
	if s := files["staged.txt"]; s.Status != "added" || !s.Staged || s.Untracked {
		t.Fatalf("staged = %+v", s)
	}
	if n := files["notes.txt"]; !n.Untracked || n.Additions != 2 || !strings.Contains(n.Diff, "\\ No newline at end of file") {
		t.Fatalf("notes = %+v", n)
	}
	if p := files["pic.png"]; !p.Binary || p.Diff != "" {
		t.Fatalf("binary untracked file must not be sent as text: %+v", p)
	}
	l := files["link"]
	if strings.Contains(l.Diff, "TOP SECRET") || !strings.Contains(l.Diff, "new file mode 120000") {
		t.Fatalf("symlink must show its target, not the content: %+v", l)
	}
	for p := range files {
		if strings.HasPrefix(p, ".worktrees") {
			t.Fatalf("a nested checkout is not a changed file: %q", p)
		}
	}
	if ch.Stats.FilesChanged != len(ch.Files) {
		t.Fatalf("stats = %+v", ch.Stats)
	}

	one, err := GetUncommittedChanges(context.Background(), root, "README.md")
	if err != nil || len(one.Files) != 1 || one.Files[0].Path != "README.md" {
		t.Fatalf("single file = %+v, %v", one, err)
	}
}

func TestGetBranchChanges(t *testing.T) {
	root, _ := newOverviewRepo(t)
	ch, err := GetBranchChanges(context.Background(), root, "main", "feature/x", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Commits) != 1 || ch.Commits[0].Subject != "feature work" {
		t.Fatalf("commits = %+v", ch.Commits)
	}
	files := map[string]GitChangeFile{}
	for _, f := range ch.Files {
		files[f.Path] = f
	}
	if r := files["new name.txt"]; r.Status != "renamed" || r.OldPath != "old name.txt" {
		t.Fatalf("rename = %+v (all %+v)", r, ch.Files)
	}
	if f := files["feature.go"]; f.Status != "added" || f.Additions != 1 || !strings.Contains(f.Diff, "+package x") {
		t.Fatalf("feature.go = %+v", f)
	}
	if r := files["README.md"]; r.Additions != 1 || !strings.HasPrefix(r.Diff, "diff --git a/README.md") {
		t.Fatalf("README = %+v", r)
	}
	if _, err := GetBranchChanges(context.Background(), root, "main", "--output=/tmp/x", ""); err == nil {
		t.Fatal("option-like ref accepted")
	}
}

func TestSanitizeRemoteURL(t *testing.T) {
	for _, tc := range []struct{ in, clean, web string }{
		{"https://user:tok@github.com/o/r.git", "https://github.com/o/r.git", "https://github.com/o/r"},
		{"git@github.com:o/r.git", "github.com:o/r", "https://github.com/o/r"},
		{"ssh://git@gitlab.com/g/r.git", "ssh://gitlab.com/g/r.git", "https://gitlab.com/g/r"},
	} {
		clean, web := SanitizeRemoteURL(tc.in)
		if clean != tc.clean || web != tc.web {
			t.Errorf("%s → %q %q", tc.in, clean, web)
		}
	}
}

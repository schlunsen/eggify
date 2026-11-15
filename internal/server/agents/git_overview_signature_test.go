package agents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// plantSignedHead puts a commit with a gpgsig header on top of repo's HEAD and
// makes the repository's config check signatures with script on every log.
func plantSignedHead(t *testing.T, repo, script, subject string) {
	t.Helper()
	git := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v in %s: %v", args, repo, err)
		}
		return strings.TrimSpace(string(out))
	}
	obj := "tree " + git("", "rev-parse", "HEAD^{tree}") + "\nparent " + git("", "rev-parse", "HEAD") + "\n" +
		"author Ada <a@x> 1700000000 +0000\ncommitter Ada <a@x> 1700000000 +0000\n" +
		"gpgsig -----BEGIN PGP SIGNATURE-----\n \n x\n -----END PGP SIGNATURE-----\n\n" + subject + "\n"
	signed := git(obj, "hash-object", "-t", "commit", "-w", "--stdin")
	git("", "update-ref", "HEAD", signed)
	git("", "config", "log.showSignature", "true")
	git("", "config", "gpg.program", script)
}

// TestRedTeam_RepoOverviewNeverRunsSignatureProgram: an untrusted repository
// (or an agent that can write its config) sets log.showSignature and
// gpg.program and adds a commit with a gpgsig header; the overview's git log
// would then run that program outside any sandbox. The second half is the
// variant a sandboxed agent can build without touching .git/config: a forged
// worktree entry that points at a nested repository with its own config.
func TestRedTeam_RepoOverviewNeverRunsSignatureProgram(t *testing.T) {
	root, _ := newOverviewRepo(t)
	outside := t.TempDir()
	marker := filepath.Join(outside, "ran")
	script := filepath.Join(outside, "verify.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ran := func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}

	plantSignedHead(t, root, script, "signed on main")

	ov, err := GetRepoOverview(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if ran() {
		t.Fatal("the overview ran the repository's gpg.program")
	}
	if len(ov.RecentCommits) != 2 || ov.RecentCommits[0].Subject != "signed on main" || ov.RecentCommits[1].Subject != "first" {
		t.Fatalf("recent = %+v", ov.RecentCommits)
	}
	if lc := ov.Worktrees[0].LastCommit; lc == nil || lc.Subject != "signed on main" {
		t.Fatalf("main worktree's last commit = %+v", lc)
	}

	// Forged worktree: root/.git/worktrees/evil names a nested repository.
	nested := filepath.Join(root, "nested")
	for _, args := range [][]string{
		{"init", "-q", "-b", "inner", nested},
		{"-C", nested, "-c", "user.name=Ada", "-c", "user.email=a@x", "commit", "-q", "--allow-empty", "-m", "inner first"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	plantSignedHead(t, nested, script, "signed in nested")
	// The main repository no longer asks for checks; only the nested one does.
	for _, key := range []string{"log.showSignature", "gpg.program"} {
		cmd := exec.Command("git", "-C", root, "config", "--unset", key)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("unset %s: %v\n%s", key, err, out)
		}
	}
	entry := filepath.Join(root, ".git", "worktrees", "evil")
	if err := os.MkdirAll(entry, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"gitdir":    filepath.Join(nested, ".git") + "\n",
		"HEAD":      "ref: refs/heads/inner\n",
		"commondir": "../..\n",
	} {
		if err := os.WriteFile(filepath.Join(entry, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ov, err = GetRepoOverview(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if ran() {
		t.Fatal("the overview ran the gpg.program of a nested repository listed as a worktree")
	}
	var forged *GitOverviewWorktree
	for i := range ov.Worktrees {
		if ov.Worktrees[i].Path == nested {
			forged = &ov.Worktrees[i]
		}
	}
	if forged == nil {
		t.Fatalf("git did not list the forged worktree, so this half tested nothing: %+v", ov.Worktrees)
	}
	if forged.LastCommit == nil || forged.LastCommit.Subject != "signed in nested" {
		t.Fatalf("forged worktree's last commit = %+v (git log did not read the nested repository)", forged.LastCommit)
	}

	// Sanity: plain git in the nested repository does run it.
	plain := exec.Command("git", "-C", nested, "log", "-n1", "--format=%H", "HEAD")
	plain.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	_ = plain.Run()
	if !ran() {
		t.Fatal("the planted gpg.program did not run even without the hardening; the test proves nothing")
	}
}

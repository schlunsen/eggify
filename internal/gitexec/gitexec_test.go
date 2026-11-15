package gitexec

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestRedTeam_ServerGitIgnoresRepoCommands: a sandboxed agent can't write
// .git/config, but it can redirect git to a config it wrote (.git/commondir,
// a .git pointer file) and name filters in .gitattributes. The server's own
// status/diff/ls-files must run none of it, since they run unsandboxed.
func TestRedTeam_ServerGitIgnoresRepoCommands(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "add", "f")
	run(t, repo, "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-qm", "i")
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(root, "ran")
	touch := "sh -c 'touch " + marker + "; cat'"
	cfg := "[core]\n\tfsmonitor = \"touch " + marker + "; false\"\n" +
		"[diff]\n\texternal = \"" + touch + "\"\n" +
		"[diff \"t\"]\n\ttextconv = \"" + touch + "\"\n" +
		"[filter \"x\"]\n\tclean = \"" + touch + "\"\n\tprocess = \"" + touch + "\"\n"
	f, err := os.OpenFile(filepath.Join(repo, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(cfg); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("f filter=x diff=t\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"--no-optional-locks", "status", "--porcelain", "-b"},
		{"diff"},
		{"diff", "HEAD", "--", "f"},
		{"ls-files", "-m"},
	} {
		cmd := Command(args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("git %v ran a command from the repository's config", args)
		}
	}

	// A filter named in $GIT_DIR/info/attributes is read whatever the
	// attribute source is; Harden blanks it by name.
	if err := os.Remove(filepath.Join(repo, ".gitattributes")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "info", "attributes"), []byte("* filter=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"diff"}, {"status", "--porcelain"}} {
		cmd := Command(args...)
		cmd.Dir = repo
		Harden(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("git %v ran the filter named in .git/info/attributes", args)
		}
	}
	cmd := Command("-C", repo, "diff") // -C: hardened by Command itself
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git -C diff: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git -C diff ran the filter")
	}
	plainInfo := exec.Command("git", "diff")
	plainInfo.Dir = repo
	plainInfo.Env = Env(nil) // the hardening before this fix, without Harden
	_ = plainInfo.Run()
	if _, err := os.Stat(marker); err != nil {
		t.Log("note: this git didn't run the info/attributes filter even without Harden")
	}
	_ = os.Remove(marker)

	// Sanity: without the hardening the same repo does run it.
	plain := exec.Command("git", "--no-optional-locks", "status")
	plain.Dir = repo
	_ = plain.Run()
	if _, err := os.Stat(marker); err != nil {
		t.Skip("this git runs none of the planted commands; nothing to compare")
	}
}

func TestArgsDisableExternalDiff(t *testing.T) {
	got := Args([]string{"--no-optional-locks", "-c", "core.quotePath=false", "-C", "/x", "diff", "--name-status", "--", "f"})
	want := []string{"--no-optional-locks", "-c", "core.quotePath=false", "-C", "/x", "diff", "--no-ext-diff", "--no-textconv", "--name-status", "--", "f"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v", got)
		}
	}
	if st := Args([]string{"status", "--porcelain"}); len(st) != 2 {
		t.Fatalf("status changed: %v", st)
	}
	for _, sub := range []string{"log", "show"} {
		got := strings.Join(Args([]string{"-C", "/x", sub, "-n1", "HEAD"}), " ")
		if want := "-C /x " + sub + " --no-ext-diff --no-textconv --no-show-signature -n1 HEAD"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

// TestRedTeam_ServerGitNeverRunsSignatureProgram: a repository whose config
// sets log.showSignature and gpg.program, with a commit that carries a gpgsig
// header, makes plain `git log` run that program. The server's log/show must
// not, with any signature format, and whether the flag or only the hardened
// environment is in effect.
func TestRedTeam_ServerGitNeverRunsSignatureProgram(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "add", "f")
	run(t, repo, "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-qm", "first")

	marker := filepath.Join(root, "ran")
	script := filepath.Join(root, "verify.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// One signed commit per signature format, each on top of the last.
	sigs := map[string]string{
		"openpgp": "-----BEGIN PGP SIGNATURE-----\n \n x\n -----END PGP SIGNATURE-----",
		"ssh":     "-----BEGIN SSH SIGNATURE-----\n x\n -----END SSH SIGNATURE-----",
		"x509":    "-----BEGIN SIGNED MESSAGE-----\n x\n -----END SIGNED MESSAGE-----",
	}
	gitOut := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+repo)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	tree := gitOut("", "rev-parse", "HEAD^{tree}")
	head := gitOut("", "rev-parse", "HEAD")
	for _, format := range []string{"openpgp", "ssh", "x509"} {
		obj := "tree " + tree + "\nparent " + head + "\n" +
			"author a <a@b> 1700000000 +0000\ncommitter a <a@b> 1700000000 +0000\n" +
			"gpgsig " + sigs[format] + "\n\nsigned " + format + "\n"
		head = gitOut(obj, "hash-object", "-t", "commit", "-w", "--stdin")
	}
	run(t, repo, "update-ref", "HEAD", head)

	cfg := "[log]\n\tshowSignature = true\n" +
		"[gpg]\n\tprogram = " + script + "\n" +
		"[gpg \"openpgp\"]\n\tprogram = " + script + "\n" +
		"[gpg \"ssh\"]\n\tprogram = " + script + "\n\tallowedSignersFile = " + script + "\n" + // ssh checks need one
		"[gpg \"x509\"]\n\tprogram = " + script + "\n"
	f, err := os.OpenFile(filepath.Join(repo, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(cfg); err != nil {
		t.Fatal(err)
	}
	f.Close()

	ran := func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}
	check := func(what string, cmd *exec.Cmd, want string) {
		t.Helper()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v\n%s", what, err, stderr.String())
		}
		if ran() {
			t.Fatalf("%s ran the repository's signature program", what)
		}
		if want != "" && strings.TrimSpace(string(out)) != want {
			t.Fatalf("%s printed %q, want %q", what, out, want)
		}
	}

	subjects := "signed x509\nsigned ssh\nsigned openpgp\nfirst"
	c := Command("log", "--format=%s", "HEAD")
	c.Dir = repo
	check("log", c, subjects)
	check("log -C", Command("-C", repo, "log", "-n1", "--format=%H", "HEAD"), head)
	c = Command("show", "-s", "--format=%s", "HEAD")
	c.Dir = repo
	check("show", c, "signed x509")

	// The config alone must hold: no --no-show-signature here, and the
	// placeholders and options that ask for a check outright.
	for _, args := range [][]string{
		{"log", "--format=%s", "HEAD"},
		{"log", "--show-signature", "--format=%s", "HEAD"},
		{"log", "--format=%G?%GS%GK", "HEAD"},
		{"for-each-ref", "--format=%(refname)", "refs/heads"},
	} {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = Env(nil)
		check("git "+strings.Join(args, " ")+" (environment only)", c, "")
	}
	for _, format := range []string{"openpgp", "ssh", "x509"} {
		c := exec.Command("git", "-c", "gpg.format="+format, "verify-commit", "HEAD")
		c.Dir = repo
		c.Env = Env(nil)
		_ = c.Run() // fails: nothing verifies
		if ran() {
			t.Fatalf("verify-commit (%s) ran the repository's signature program", format)
		}
	}

	// Sanity: without the hardening the same repository does run it.
	plain := exec.Command("git", "log", "-n1", "--format=%H", "HEAD")
	plain.Dir = repo
	plain.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+repo)
	_ = plain.Run()
	if !ran() {
		t.Fatal("the planted signature program did not run even without the hardening; the test proves nothing")
	}
}

package handlers

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/schlunsen/eggify/internal/database"
)

func gitTestRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Ada", "GIT_AUTHOR_EMAIL=a@x", "GIT_COMMITTER_NAME=Ada", "GIT_COMMITTER_EMAIL=a@x", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "first")
	run("checkout", "-q", "-b", "feature")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644)
	run("commit", "-q", "-am", "second")
	run("checkout", "-q", "main")
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hi\n"), 0o644)
}

// projectGitApp wires the project Git routes as routes.RegisterProject does.
func projectGitApp(t *testing.T) (do func(method, path string) (int, map[string]any), repo *database.Repository, home string) {
	t.Helper()
	a, r, _, h := newSetupApp(t)
	ph := NewProjectHandler(r, nil, "", false)
	api := a.Group("/api")
	api.Get("/projects/:id/git/overview", ph.HandleProjectGitOverview)
	api.Get("/projects/:id/git/changes", ph.HandleProjectGitChanges)
	return func(method, path string) (int, map[string]any) { return doJSON(t, a, method, path, "") }, r, h
}

func TestProjectGitOverviewAndChanges(t *testing.T) {
	do, repo, home := projectGitApp(t)
	dir := filepath.Join(home, "projects", "site")
	gitTestRepo(t, dir)
	if err := repo.CreateProject(&database.Project{ID: "p1", Name: "site", Path: dir, IsActive: true}); err != nil {
		t.Fatal(err)
	}

	status, body := do("GET", "/api/projects/p1/git/overview")
	if status != 200 || body["is_repo"] != true {
		t.Fatalf("overview = %d %v", status, body)
	}
	ov := body["overview"].(map[string]any)
	if ov["default_branch"] != "main" || len(ov["branches"].([]any)) != 2 || len(ov["worktrees"].([]any)) != 1 {
		t.Fatalf("overview body = %v", ov)
	}

	status, body = do("GET", "/api/projects/p1/git/changes")
	if status != 200 || len(body["files"].([]any)) != 1 {
		t.Fatalf("uncommitted = %d %v", status, body)
	}
	status, body = do("GET", "/api/projects/p1/git/changes?scope=branch&ref=feature")
	if status != 200 || len(body["commits"].([]any)) != 1 || body["base"] != "main" {
		t.Fatalf("branch = %d %v", status, body)
	}

	// Not a repository: a plain answer, not an error page.
	plain := filepath.Join(home, "projects", "plain")
	os.MkdirAll(plain, 0o755)
	repo.CreateProject(&database.Project{ID: "p2", Name: "plain", Path: plain, IsActive: true})
	if status, body := do("GET", "/api/projects/p2/git/overview"); status != 200 || body["is_repo"] != false {
		t.Fatalf("non-repo = %d %v", status, body)
	}
}

// The worktree parameter only selects checkouts git lists for the project;
// option-like refs and forbidden project folders are refused.
func TestRedTeam_REST_ProjectGitRefusesForeignFoldersAndOptions(t *testing.T) {
	do, repo, home := projectGitApp(t)
	dir := filepath.Join(home, "projects", "site")
	gitTestRepo(t, dir)
	repo.CreateProject(&database.Project{ID: "p1", Name: "site", Path: dir, IsActive: true})

	other := filepath.Join(home, "projects", "other")
	gitTestRepo(t, other)
	for _, wt := range []string{other, "/", filepath.Join(dir, ".."), home} {
		status, body := do("GET", "/api/projects/p1/git/changes?worktree="+url.QueryEscape(wt))
		if status != 404 {
			t.Errorf("worktree=%s → %d %v, want 404", wt, status, body)
		}
	}
	for _, q := range []string{"ref=--output=/tmp/x", "ref=feature&base=--exec=sh", "ref=main..feature", "scope=nope"} {
		if status, _ := do("GET", "/api/projects/p1/git/changes?scope=branch&"+q); status != 400 {
			t.Errorf("%s → %d, want 400", q, status)
		}
	}

	secrets := filepath.Join(home, ".claude", "eggify")
	gitTestRepo(t, secrets)
	repo.CreateProject(&database.Project{ID: "legacy", Name: "legacy", Path: secrets, IsActive: true})
	for _, p := range []string{"/api/projects/legacy/git/overview", "/api/projects/legacy/git/changes"} {
		if status, body := do("GET", p); status != 403 {
			t.Errorf("%s → %d %v, want 403", p, status, body)
		}
	}
}

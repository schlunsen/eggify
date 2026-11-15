package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// newSetupApp wires the project routes the way routes.RegisterProject does,
// with a test principal: "user" (signed in) or "apikey".
func newSetupApp(t *testing.T) (*fiber.App, *database.Repository, *string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		database.ResetInstance()
	})
	repo := database.NewRepository(db)
	who := "user"
	prev := principal.Enforced()
	principal.SetEnforced(true)
	t.Cleanup(func() { principal.SetEnforced(prev) })

	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		if who == "user" {
			principal.Set(c, principal.Principal{Kind: principal.User, Username: "alice"})
		} else {
			principal.Set(c, principal.Principal{Kind: principal.APIKey})
		}
		return c.Next()
	})
	h := NewProjectHandler(repo, nil, "", false)
	api := app.Group("/api")
	setup := principal.RequireUser(ProjectSetupAction)
	api.Get("/projects/overview", h.HandleProjectsOverview)
	api.Get("/projects/setup/defaults", setup, h.HandleSetupDefaults)
	api.Get("/projects/setup/inspect", setup, h.HandleSetupInspect)
	api.Get("/projects/setup/complete", setup, h.HandleSetupComplete)
	api.Post("/projects/setup", setup, h.HandleSetupProject)
	api.Get("/projects/setup/jobs/:jobId", setup, h.HandleSetupJob)
	api.Delete("/projects/setup/jobs/:jobId", setup, h.HandleCancelSetupJob)
	api.Put("/projects/:id", h.HandleUpdateProject)
	return app, repo, &who, home
}

func TestProjectSetupRequiresSignedInUser(t *testing.T) {
	app, _, who, home := newSetupApp(t)
	*who = "apikey"
	cases := [][3]string{
		{"GET", "/api/projects/setup/defaults", ""},
		{"GET", "/api/projects/setup/inspect?path=" + url.QueryEscape(home), ""},
		{"GET", "/api/projects/setup/complete?prefix=~/", ""},
		{"POST", "/api/projects/setup", `{"mode":"empty","parent":"~/projects","folder":"x"}`},
		{"POST", "/api/projects/setup", `{"mode":"clone","parent":"~/projects","source":"owner/repo"}`},
		{"GET", "/api/projects/setup/jobs/abc", ""},
		{"DELETE", "/api/projects/setup/jobs/abc", ""},
	}
	for _, tc := range cases {
		status, body := doJSON(t, app, tc[0], tc[1], tc[2])
		if status != 403 || body["code"] != principal.ErrorCode {
			t.Errorf("%s %s as API key: got %d %v, want 403 user_required", tc[0], tc[1], status, body)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "projects", "x")); !os.IsNotExist(err) {
		t.Fatalf("refused request still created a folder")
	}
}

func TestNewFolderTargetPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "projects", "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, "escape")); err != nil {
		t.Fatal(err)
	}
	ok := []struct{ parent, name string }{
		{"~/projects", "new-app"},
		{"~/projects", "My App 2"},
		{"~/code/nested/deeper", "app"}, // parents are created
		{"~/.claude/projects", "legacy"},
		{home + "/projects", "abs"},
	}
	for _, c := range ok {
		if _, err := newFolderTarget(c.parent, c.name); err != nil {
			t.Errorf("newFolderTarget(%q, %q) = %v, want ok", c.parent, c.name, err)
		}
	}
	bad := []struct{ parent, name, why string }{
		{"~/projects", "../evil", "traversal in name"},
		{"~/projects", "a/b", "separator in name"},
		{"~/projects", "..", "dot-dot name"},
		{"~/projects", ".hidden", "hidden name"},
		{"~/projects", "-rf", "leading dash"},
		{"~/projects", "", "empty name"},
		{"~/projects/../..", "x", "traversal in parent"},
		{"projects", "x", "relative parent"},
		{"/etc", "x", "outside home"},
		{outside, "x", "outside home (tmp)"},
		{"~/escape", "x", "symlink out of home"},
		{"~", "", "home itself"},
		{"~/.ssh", "x", "hidden dir"},
		{"~/.claude/eggify", "x", "eggify data dir"},
		{"~/projects", "taken", "already exists"},
	}
	for _, c := range bad {
		if got, err := newFolderTarget(c.parent, c.name); err == nil {
			t.Errorf("%s: newFolderTarget(%q, %q) = %q, want error", c.why, c.parent, c.name, got)
		}
	}
}

func TestParseCloneSource(t *testing.T) {
	good := map[string][2]string{
		"schlunsen/eggify":                         {"https://github.com/schlunsen/eggify.git", "eggify"},
		"octocat/Hello-World.git":                  {"https://github.com/octocat/Hello-World.git", "Hello-World"},
		"https://github.com/octocat/Hello-World":   {"https://github.com/octocat/Hello-World", "Hello-World"},
		"https://gitlab.com/group/sub/project.git": {"https://gitlab.com/group/sub/project.git", "project"},
		"git@github.com:octocat/Spoon-Knife.git":   {"https://github.com/octocat/Spoon-Knife.git", "Spoon-Knife"},
		"github.com/octocat/linguist/":             {"https://github.com/octocat/linguist", "linguist"},
	}
	for in, want := range good {
		got, err := parseCloneSource(in)
		if err != nil {
			t.Errorf("parseCloneSource(%q) error %v", in, err)
			continue
		}
		if got.URL != want[0] || got.Name != want[1] {
			t.Errorf("parseCloneSource(%q) = %+v, want %v", in, got, want)
		}
	}
	bad := []string{
		"", "http://github.com/a/b", "file:///etc/passwd", "/Users/me/repo", "ext::sh -c touch% /tmp/x",
		"ssh://git@github.com/a/b", "https://localhost/a/b", "https://127.0.0.1/a/b", "https://10.0.0.5/a/b",
		"https://[::1]/a/b", "https://token@github.com/a/b", "https://github.com/onlyowner", "https://github.com/a/..",
	}
	for _, in := range bad {
		if got, err := parseCloneSource(in); err == nil {
			t.Errorf("parseCloneSource(%q) = %+v, want error", in, got)
		}
	}
}

func TestProjectSetupExistingFolder(t *testing.T) {
	app, repo, _, home := newSetupApp(t)
	dir := filepath.Join(home, "work", "site")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	status, body := doJSON(t, app, "POST", "/api/projects/setup", `{"mode":"existing","path":"~/work/site","name":"Site","color":"#112233"}`)
	if status != 201 {
		t.Fatalf("create existing: %d %v", status, body)
	}
	p := body["project"].(map[string]interface{})
	if p["path"] != dir || p["name"] != "Site" || p["color"] != "#112233" {
		t.Fatalf("unexpected project %v", p)
	}
	// Same folder again → 409 with the existing project.
	status, body = doJSON(t, app, "POST", "/api/projects/setup", fmt.Sprintf(`{"mode":"existing","path":%q}`, dir))
	if status != 409 || body["existing_project"] == nil {
		t.Fatalf("duplicate: %d %v", status, body)
	}
	// Inspect reports the existing project.
	status, body = doJSON(t, app, "GET", "/api/projects/setup/inspect?path="+url.QueryEscape("~/work/site"), "")
	if status != 200 || body["existing_project"] == nil || body["exists"] != true || body["display_path"] != "~/work/site" {
		t.Fatalf("inspect: %d %v", status, body)
	}
	// Missing folder and a file are refused.
	if status, _ := doJSON(t, app, "POST", "/api/projects/setup", `{"mode":"existing","path":"~/nope"}`); status != 400 {
		t.Fatalf("missing folder: %d", status)
	}
	if status, _ := doJSON(t, app, "POST", "/api/projects/setup", `{"mode":"existing","path":"work/site"}`); status != 400 {
		t.Fatalf("relative path: %d", status)
	}
	all, _ := repo.GetAllProjects()
	if len(all) != 1 {
		t.Fatalf("want 1 project, got %d", len(all))
	}
}

func TestProjectSetupEmptyProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	app, _, _, home := newSetupApp(t)
	status, body := doJSON(t, app, "POST", "/api/projects/setup", `{"mode":"empty","parent":"~/projects","folder":"fresh","name":"Fresh","git_init":true}`)
	if status != 201 {
		t.Fatalf("create empty: %d %v", status, body)
	}
	target := filepath.Join(home, "projects", "fresh")
	if fi, err := os.Stat(filepath.Join(target, ".git")); err != nil || !fi.IsDir() {
		t.Fatalf("git init did not run: %v", err)
	}
	// Exists now → refused, and nothing is touched.
	if status, body := doJSON(t, app, "POST", "/api/projects/setup", `{"mode":"empty","parent":"~/projects","folder":"fresh"}`); status != 400 || !strings.Contains(body["error"].(string), "already exists") {
		t.Fatalf("existing folder: %d %v", status, body)
	}
	for _, body := range []string{
		`{"mode":"empty","parent":"~/projects","folder":"../../escape"}`,
		`{"mode":"empty","parent":"/tmp","folder":"x"}`,
		`{"mode":"empty","parent":"~/.ssh","folder":"x"}`,
		`{"mode":"bogus"}`,
	} {
		if status, _ := doJSON(t, app, "POST", "/api/projects/setup", body); status != 400 {
			t.Errorf("%s: got %d, want 400", body, status)
		}
	}
	// Inspect a creatable and a forbidden target.
	_, ins := doJSON(t, app, "GET", "/api/projects/setup/inspect?path="+url.QueryEscape("~/projects/new-one"), "")
	if ins["can_create"] != true || ins["exists"] != false {
		t.Fatalf("inspect creatable: %v", ins)
	}
	_, ins = doJSON(t, app, "GET", "/api/projects/setup/inspect?path="+url.QueryEscape("~/.config/x"), "")
	if ins["can_create"] != false || ins["create_error"] == nil {
		t.Fatalf("inspect hidden: %v", ins)
	}
}

func waitJob(t *testing.T, app *fiber.App, id string) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, body := doJSON(t, app, "GET", "/api/projects/setup/jobs/"+id, "")
		job := body["job"].(map[string]interface{})
		if job["status"] != "running" {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("clone job did not finish")
	return nil
}

func TestProjectSetupClone(t *testing.T) {
	app, repo, _, home := newSetupApp(t)
	prev := cloneRunner
	t.Cleanup(func() { cloneRunner = prev })

	var gotURL, gotTarget string
	cloneRunner = func(ctx context.Context, src, target string, depth int, progress func(string)) error {
		gotURL, gotTarget = src, target
		progress("Cloning into 'x'...")
		progress("Receiving objects:  50% (5/10)")
		if err := os.MkdirAll(filepath.Join(target, ".git"), 0o755); err != nil {
			return err
		}
		progress("Resolving deltas: 100% (3/3), done.")
		return nil
	}
	status, body := doJSON(t, app, "POST", "/api/projects/setup", `{"mode":"clone","source":"octocat/Hello-World","parent":"~/projects"}`)
	if status != 202 {
		t.Fatalf("clone: %d %v", status, body)
	}
	job := waitJob(t, app, body["job"].(map[string]interface{})["id"].(string))
	if job["status"] != "done" || job["project"] == nil || job["percent"].(float64) != 100 {
		t.Fatalf("job: %v", job)
	}
	if gotURL != "https://github.com/octocat/Hello-World.git" || gotTarget != filepath.Join(home, "projects", "Hello-World") {
		t.Fatalf("clone args %q %q", gotURL, gotTarget)
	}
	if p, err := repo.GetProjectByPath(gotTarget); err != nil || p.Name != "Hello-World" {
		t.Fatalf("project not saved: %v %v", p, err)
	}

	// A failing clone removes the folder it started and reports git's message.
	cloneRunner = func(ctx context.Context, src, target string, depth int, progress func(string)) error {
		_ = os.MkdirAll(target, 0o755)
		return errors.New("fatal: repository 'https://github.com/a/missing.git/' not found")
	}
	_, body = doJSON(t, app, "POST", "/api/projects/setup", `{"mode":"clone","source":"a/missing","parent":"~/projects"}`)
	job = waitJob(t, app, body["job"].(map[string]interface{})["id"].(string))
	if job["status"] != "error" || !strings.Contains(job["error"].(string), "not found") {
		t.Fatalf("failed job: %v", job)
	}
	if _, err := os.Stat(filepath.Join(home, "projects", "missing")); !os.IsNotExist(err) {
		t.Fatal("failed clone left its folder behind")
	}

	// Bad sources and destinations are refused before anything runs.
	for _, b := range []string{
		`{"mode":"clone","source":"file:///etc","parent":"~/projects"}`,
		`{"mode":"clone","source":"https://127.0.0.1/a/b","parent":"~/projects"}`,
		`{"mode":"clone","source":"octocat/Hello-World","parent":"~/projects"}`, // exists now
		`{"mode":"clone","source":"a/b","parent":"~/projects","folder":"../x"}`,
		`{"mode":"clone","source":"a/b","parent":"/var/tmp"}`,
	} {
		if status, _ := doJSON(t, app, "POST", "/api/projects/setup", b); status != 400 {
			t.Errorf("%s: got %d, want 400", b, status)
		}
	}
}

func TestProjectSetupComplete(t *testing.T) {
	app, _, _, home := newSetupApp(t)
	for _, d := range []string{"projects/alpha", "projects/Apple", "projects/beta", "projects/.secret"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(home, "projects", "afile"), []byte("x"), 0o644)
	_, body := doJSON(t, app, "GET", "/api/projects/setup/complete?prefix="+url.QueryEscape("~/projects/a"), "")
	sugg := body["suggestions"].([]interface{})
	var got []string
	for _, s := range sugg {
		got = append(got, s.(map[string]interface{})["display"].(string))
	}
	if strings.Join(got, ",") != "~/projects/alpha,~/projects/Apple" {
		t.Fatalf("suggestions %v", got)
	}
	_, body = doJSON(t, app, "GET", "/api/projects/setup/complete?prefix="+url.QueryEscape("~/projects/"), "")
	if n := len(body["suggestions"].([]interface{})); n != 3 {
		t.Fatalf("listing ~/projects/: want 3 visible dirs, got %d", n)
	}
}

func TestProjectsOverviewAndPartialUpdate(t *testing.T) {
	app, repo, _, home := newSetupApp(t)
	dir := filepath.Join(home, "p1")
	_ = os.MkdirAll(dir, 0o755)
	p := &database.Project{ID: "p1", Name: "One", Path: dir, SystemPrompt: "keep me", DefaultSkillIDs: `["a"]`, IsActive: true}
	if err := repo.CreateProject(p); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateProject(&database.Project{ID: "p2", Name: "Gone", Path: filepath.Join(home, "gone"), IsActive: true}); err != nil {
		t.Fatal(err)
	}
	db := database.GetInstance().GetDB()
	for i, st := range []string{"processing", "idle", "ended"} {
		if _, err := db.Exec(`INSERT INTO agent_sessions (id, status, project_id) VALUES (?, ?, 'p1')`, fmt.Sprintf("s%d", i), st); err != nil {
			t.Fatal(err)
		}
	}
	status, body := doJSON(t, app, "GET", "/api/projects/overview?git=0", "")
	if status != 200 {
		t.Fatalf("overview %d %v", status, body)
	}
	rows := map[string]map[string]interface{}{}
	for _, r := range body["projects"].([]interface{}) {
		m := r.(map[string]interface{})
		rows[m["id"].(string)] = m
	}
	if rows["p1"]["session_count"].(float64) != 3 || rows["p1"]["running_count"].(float64) != 1 || rows["p1"]["display_path"] != "~/p1" {
		t.Fatalf("p1 row %v", rows["p1"])
	}
	if rows["p2"]["missing"] != true || rows["p1"]["missing"] != false {
		t.Fatalf("missing flags %v %v", rows["p1"]["missing"], rows["p2"]["missing"])
	}

	// Rename only: the rest of the project is kept.
	status, body = doJSON(t, app, "PUT", "/api/projects/p1", `{"name":"Renamed"}`)
	if status != 200 {
		t.Fatalf("rename %d %v", status, body)
	}
	got, _ := repo.GetProject("p1")
	if got.Name != "Renamed" || got.SystemPrompt != "keep me" || got.DefaultSkillIDs != `["a"]` || got.Path != dir || !got.IsActive {
		t.Fatalf("partial update lost fields: %+v", got)
	}
	if status, _ := doJSON(t, app, "PUT", "/api/projects/p1", `{"name":"   "}`); status != 400 {
		t.Fatalf("blank name accepted: %d", status)
	}
}

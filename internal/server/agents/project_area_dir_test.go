package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
)

func TestAreaWorkingDirectory(t *testing.T) {
	base := resolvePathForMatch(t.TempDir())
	project := filepath.Join(base, "site")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(project, "src", "api"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(project, "link")); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		rel  string
		want string
		ok   bool
	}{
		{"", project, true},
		{".", project, true},
		{"src", filepath.Join(project, "src"), true},
		{"src/api", filepath.Join(project, "src", "api"), true},
		{"not-there-yet", filepath.Join(project, "not-there-yet"), true},
		{"..", project, false},
		{"../outside", project, false},
		{"../../..", project, false},
		{"src/../../outside", project, false},
		{`src\..\..\outside`, project, false},
		{"/etc", project, false},
		{outside, project, false},
		{"~/.ssh", project, false},
		{"link", project, false},
		{"link/deeper", project, false},
	} {
		got, ok := areaWorkingDirectory(project, tc.rel)
		if got != tc.want || ok != tc.ok {
			t.Errorf("areaWorkingDirectory(%q) = %q, %v; want %q, %v", tc.rel, got, ok, tc.want, tc.ok)
		}
	}
}

// An area row stored before relative_path was validated (or edited in the
// database) holds "../../..": a session started in that area, and the same
// session restored later, must stay in the project folder.
func TestRedTeam_ProjectArea_StoredEscapingPathStaysInProject(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()

	base := resolvePathForMatch(t.TempDir())
	project := filepath.Join(base, "home", "projects", "site")
	if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := database.NewRepository(database.GetInstance())
	if err := repo.CreateProject(&database.Project{ID: "p1", Name: "site", Path: project, IsActive: true}); err != nil {
		t.Fatal(err)
	}
	areas := map[string]string{
		"escape":   "../../..",
		"absolute": base,
		"fine":     "src",
	}
	for id, rel := range areas {
		if err := repo.CreateProjectArea(&database.ProjectArea{ID: id, ProjectID: "p1", Name: id, RelativePath: rel, ContextPrompt: "area notes"}); err != nil {
			t.Fatal(err)
		}
	}

	for id, want := range map[string]string{
		"escape":   project,
		"absolute": project,
		"fine":     filepath.Join(project, "src"),
	} {
		areaID, projectID := id, "p1"
		sessionID := uuid.New()
		s, err := sm.CreateSession(sessionID, SessionOptions{ProjectID: &projectID, ProjectAreaID: &areaID})
		if err != nil {
			t.Fatalf("%s: CreateSession: %v", id, err)
		}
		if got := codexStr(s.Options.WorkingDirectory); got != want {
			t.Errorf("%s: new session works in %q, want %q", id, got, want)
		}

		// Restore it from the database, as after a restart.
		sm.mu.Lock()
		delete(sm.sessions, sessionID)
		sm.mu.Unlock()
		s, err = sm.CreateSession(sessionID, SessionOptions{})
		if err != nil {
			t.Fatalf("%s: restoring: %v", id, err)
		}
		if got := codexStr(s.Options.WorkingDirectory); got != want {
			t.Errorf("%s: restored session works in %q, want %q", id, got, want)
		}
	}
}

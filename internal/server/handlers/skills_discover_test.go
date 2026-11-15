package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schlunsen/eggify/internal/database"
)

func writeSkillFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// POST /api/skills/discover reports counts, the files it could not parse and
// the files it skipped (with a reason), and creates only pending skills.
func TestDiscoverSkillsResponseShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app, repo, who := rtReviewApp(t)
	personal := filepath.Join(home, ".claude", "skills")

	// A symlinked skill with list-form allowed-tools (gstack style).
	writeSkillFile(t, filepath.Join(personal, "gstack", "careful"),
		"---\nname: careful\ndescription: d\nallowed-tools:\n  - Bash\n  - Read\n---\nbody\n")
	if err := os.Symlink(filepath.Join("gstack", "careful"), filepath.Join(personal, "careful")); err != nil {
		t.Fatal(err)
	}
	// A nested synced skill.
	writeSkillFile(t, filepath.Join(personal, "synced", "uuid-1", "morning"), "---\nname: morning\ndescription: d\n---\nbody\n")
	// A broken file.
	writeSkillFile(t, filepath.Join(personal, "broken"), "---\nname: broken\ndescription: [x\n---\nbody\n")
	// A project skill whose name an agent proposal already holds.
	if err := repo.Skill.CreateAgentSkill(&database.Skill{Name: "helper", Scope: "project", Body: "pending", UserInvocable: true}, "s1"); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	writeSkillFile(t, filepath.Join(project, ".claude", "skills", "helper"), "---\nname: helper\ndescription: d\n---\nbody\n")

	*who = asAPIKey // discovery stays callable with the API key
	code, out := doJSON(t, app, "POST", "/api/skills/discover?project_dir="+project, "")
	if code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	num := func(k string) int {
		f, ok := out[k].(float64)
		if !ok {
			t.Fatalf("%s missing in %v", k, out)
		}
		return int(f)
	}
	if num("discovered") != 3 || num("pending") != 2 || num("unchanged") != 0 || num("skipped") != 1 {
		t.Fatalf("counts = %v", out)
	}
	errs, _ := out["errors"].([]interface{})
	if len(errs) != 1 {
		t.Fatalf("errors = %v", out["errors"])
	}
	e := errs[0].(map[string]interface{})
	if e["path"] != filepath.Join(personal, "broken", "SKILL.md") || e["message"] == "" {
		t.Errorf("error = %v", e)
	}
	skipped, _ := out["skipped_items"].([]interface{})
	if len(skipped) != 1 {
		t.Fatalf("skipped_items = %v", out["skipped_items"])
	}
	sk := skipped[0].(map[string]interface{})
	if sk["name"] != "helper" || !strings.Contains(sk["reason"].(string), "agent") {
		t.Errorf("skipped = %v", sk)
	}

	for _, name := range []string{"careful", "morning"} {
		s, err := repo.Skill.GetSkillByName(name)
		if err != nil || s == nil {
			t.Fatalf("%s: %v %v", name, s, err)
		}
		if s.Status != database.ReviewStatusPending || !database.IsFileOrigin(s.CreatedBy) {
			t.Errorf("%s: status=%s created_by=%s", name, s.Status, s.CreatedBy)
		}
	}
	if s, _ := repo.Skill.GetSkillByName("careful"); s.AllowedTools != "Bash, Read" {
		t.Errorf("allowed_tools = %q", s.AllowedTools)
	}
	if s, _ := repo.Skill.GetSkillByName("helper"); s.Body != "pending" {
		t.Errorf("the agent's pending skill was overwritten: %q", s.Body)
	}

	// Nothing to report: lists are [] (not null).
	t.Setenv("HOME", t.TempDir())
	_, out = doJSON(t, app, "POST", "/api/skills/discover", "")
	if _, ok := out["errors"].([]interface{}); !ok {
		t.Errorf("errors should be a list: %v", out)
	}
	if _, ok := out["skipped_items"].([]interface{}); !ok {
		t.Errorf("skipped_items should be a list: %v", out)
	}
}

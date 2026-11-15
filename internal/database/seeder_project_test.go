package database

import (
	"path/filepath"
	"testing"
)

func TestSeedDefaultProjectOnlyWhenAppFolderExists(t *testing.T) {
	ResetInstance()
	db, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); ResetInstance() })
	prev := sandboxAppDir
	t.Cleanup(func() { sandboxAppDir = prev })

	sandboxAppDir = filepath.Join(t.TempDir(), "missing")
	if err := db.SeedDefaultProject(); err != nil {
		t.Fatal(err)
	}
	if all, _ := NewProjectRepository(db).GetAllProjects(); len(all) != 0 {
		t.Fatalf("seeded a project for a missing /app: %v", all)
	}

	sandboxAppDir = t.TempDir()
	if err := db.SeedDefaultProject(); err != nil {
		t.Fatal(err)
	}
	all, _ := NewProjectRepository(db).GetAllProjects()
	if len(all) != 1 || all[0].Path != sandboxAppDir {
		t.Fatalf("want the sandbox project, got %v", all)
	}
}

package handlers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveProjectPath(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "app")
	sibling := filepath.Join(base, "app-secrets")
	for _, d := range []string{filepath.Join(root, "src"), sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sibling, "key"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sibling, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "src"), filepath.Join(root, "inside")); err != nil {
		t.Fatal(err)
	}

	ok := []string{"", ".", "src", "src/new-file.txt", "src/a/b/new.txt", "inside/x.go", "/src"}
	for _, p := range ok {
		if _, err := resolveProjectPath(root, p); err != nil {
			t.Errorf("resolveProjectPath(%q) rejected: %v", p, err)
		}
	}

	bad := []string{
		"..",
		"../app-secrets/key", // sibling sharing the root as a string prefix
		"src/../../app-secrets/key",
		"escape/key", // symlink out of the project
		"escape/new.txt",
	}
	for _, p := range bad {
		if got, err := resolveProjectPath(root, p); err == nil {
			t.Errorf("resolveProjectPath(%q) = %q, want error", p, got)
		}
	}
}

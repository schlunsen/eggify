package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestFindSymbolInFile(t *testing.T) {
	src := "package x\n\n// Helper does things\nfunc Helper() {}\n\ntype Server struct{}\n\nfunc (s *Server) Helper2(a int) {}\nvar Helper = 1\n"
	got := findSymbolInFile([]byte(src), "Helper", symbolPatterns["go"], 20)
	if len(got) != 2 {
		t.Fatalf("want 2 matches, got %+v", got)
	}
	if got[0].Line != 4 || got[0].Column != 6 || got[0].Kind != "function" || got[0].Context != "func Helper() {}" {
		t.Errorf("unexpected func match: %+v", got[0])
	}
	if got[1].Line != 9 || got[1].Column != 5 || got[1].Kind != "variable" || got[1].Context != "var Helper = 1" {
		t.Errorf("unexpected var match: %+v", got[1])
	}

	method := findSymbolInFile([]byte(src), "Helper2", symbolPatterns["go"], 20)
	if len(method) != 1 || method[0].Line != 8 || method[0].Column != 18 {
		t.Errorf("unexpected method match: %+v", method)
	}

	// The last line without a trailing newline still yields its context.
	tail := findSymbolInFile([]byte("package x\ntype Last int"), "Last", symbolPatterns["go"], 20)
	if len(tail) != 1 || tail[0].Line != 2 || tail[0].Context != "type Last int" {
		t.Errorf("unexpected tail match: %+v", tail)
	}

	if m := findSymbolInFile([]byte(src), "Missing", symbolPatterns["go"], 20); m != nil {
		t.Errorf("symbol absent from the file must not match: %+v", m)
	}
	if m := findSymbolInFile([]byte(src), "Helper", symbolPatterns["go"], 1); len(m) != 1 {
		t.Errorf("max must cap the matches: %+v", m)
	}
	if m := findSymbolInFile([]byte("func Helper() {}\x00\x00"), "Helper", symbolPatterns["go"], 20); m != nil {
		t.Errorf("binary content must be skipped: %+v", m)
	}
}

func TestReadSymbolCandidateSkipsLargeFiles(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.go")
	big := filepath.Join(dir, "big.go")
	if err := os.WriteFile(small, []byte("package x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(big, []byte(strings.Repeat("a", maxSymbolFileSize+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readSymbolCandidate(small); !ok {
		t.Error("small file should be read")
	}
	if _, ok := readSymbolCandidate(big); ok {
		t.Error("file over the size cap should be skipped")
	}
	if _, ok := readSymbolCandidate(dir); ok {
		t.Error("directories should be skipped")
	}
}

func TestProjectFileListWalksAndCaches(t *testing.T) {
	dir := t.TempDir() // not a git repository: exercises the walk
	for _, rel := range []string{"a.go", "sub/b.go", "node_modules/pkg/c.js", ".hidden/d.go"} {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files := append([]string(nil), projectFileList(dir)...)
	sort.Strings(files)
	want := []string{"a.go", filepath.Join("sub", "b.go")}
	if fmt.Sprint(files) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", files, want)
	}

	// A new file is not seen until the cache entry expires.
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := projectFileList(dir); len(got) != 2 {
		t.Errorf("expected the cached list, got %v", got)
	}
	projectFilesMu.Lock()
	entry := projectFilesCache[dir]
	entry.at = entry.at.Add(-2 * projectFilesTTL)
	projectFilesCache[dir] = entry
	projectFilesMu.Unlock()
	if got := projectFileList(dir); len(got) != 3 {
		t.Errorf("expected a fresh list after expiry, got %v", got)
	}
}

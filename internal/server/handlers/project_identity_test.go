package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIdentityFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindProjectIconPrefersAppIconThenFavicon(t *testing.T) {
	root := t.TempDir()
	if got := findProjectIcon(root); got != "" {
		t.Fatalf("empty folder: got %q", got)
	}
	writeIdentityFile(t, root, "web/public/favicon.ico", "ico")
	writeIdentityFile(t, root, "web/public/favicon.svg", "<svg/>")
	if got := findProjectIcon(root); !strings.HasSuffix(got, "web/public/favicon.svg") {
		t.Fatalf("want favicon.svg before favicon.ico, got %q", got)
	}
	set := "ios/App/Assets.xcassets/AppIcon.appiconset/"
	writeIdentityFile(t, root, set+"small.png", "png")
	writeIdentityFile(t, root, set+"large.png", "a bigger png file")
	if got := findProjectIcon(root); !strings.HasSuffix(got, "large.png") {
		t.Fatalf("want the largest app icon, got %q", got)
	}
}

func TestFindProjectIconRefusesEscapesAndBigFiles(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeIdentityFile(t, outside, "secret.png", "png")
	if err := os.Symlink(filepath.Join(outside, "secret.png"), filepath.Join(root, "favicon.png")); err != nil {
		t.Fatal(err)
	}
	writeIdentityFile(t, root, "public/logo.png", strings.Repeat("x", projectIconMaxBytes+1))
	writeIdentityFile(t, root, "public/logo.svg.txt", "<svg/>")
	if got := findProjectIcon(root); got != "" {
		t.Fatalf("want no icon, got %q", got)
	}
}

func TestFindProjectSummary(t *testing.T) {
	root := t.TempDir()
	writeIdentityFile(t, root, "README.md", "# Title\n\n> ⚠️ Not affiliated with anyone.\n\n[![CI](https://x/badge.svg)](https://x)\n\n```sh\nmake\n```\n\nA **fast** tool for [syncing](https://x) music\nlibraries across devices.\n\nMore text.\n")
	if got := findProjectSummary(root); got != "A fast tool for syncing music libraries across devices." {
		t.Fatalf("readme summary: %q", got)
	}
	writeIdentityFile(t, root, "package.json", `{"description": "From package.json"}`)
	if got := findProjectSummary(root); got != "From package.json" {
		t.Fatalf("package.json summary: %q", got)
	}
	if got := clampRunes(strings.Repeat("word ", 60), 40); len([]rune(got)) > 41 || !strings.HasSuffix(got, "…") {
		t.Fatalf("clamp: %q", got)
	}
}

package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShowMedia(t *testing.T) {
	dir := t.TempDir()
	s := &MCPServer{workDir: dir}
	clip := filepath.Join(dir, "out", "clip.webm")
	if err := os.MkdirAll(filepath.Dir(clip), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clip, []byte("\x1a\x45\xdf\xa3\x01\x00\x00\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.mp4"), []byte("just text"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := s.toolShowMedia(map[string]interface{}{"path": "out/clip.webm", "title": "Clip"})
	if isErr(res) || !strings.Contains(res.Content[0].Text, clip) || !strings.Contains(res.Content[0].Text, "can't play") {
		t.Errorf("relative webm: %+v", res)
	}
	for _, bad := range []string{"", "notes.txt", "missing.mp4", "notes.mp4"} {
		if res := s.toolShowMedia(map[string]interface{}{"path": bad}); !isErr(res) {
			t.Errorf("%q should be refused: %+v", bad, res)
		}
	}
}

func isErr(r CallToolResult) bool { return r.IsError != nil && *r.IsError }

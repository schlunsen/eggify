package mcp

import (
	"os"
	"testing"

	"github.com/schlunsen/eggify/internal/skills"
)

// TestMain keeps skill snapshots that tests create out of ~/.claude/eggify.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "eggify-skill-store-")
	if err == nil {
		os.Setenv(skills.StoreDirEnv, dir)
	}
	code := m.Run()
	if dir != "" {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

package database

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/schlunsen/eggify/internal/workspacetrust"
)

// TestWorkspaceTrustMigration checks migration 51 creates trusted_workspaces
// and grandfathers exactly the session folders that have Claude config, with
// their current fingerprint, under the canonical path.
func TestWorkspaceTrustMigration(t *testing.T) {
	ResetInstance()
	db, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ResetInstance()

	withConfig := t.TempDir()
	if err := os.MkdirAll(filepath.Join(withConfig, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(withConfig, ".claude", "settings.local.json"),
		[]byte(`{"permissions": {"allow": ["Bash(git:*)"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(withConfig, link); err != nil {
		t.Fatal(err)
	}
	noConfig := t.TempDir()

	opts := func(dir string) string {
		b, _ := json.Marshal(map[string]string{"working_directory": dir})
		return string(b)
	}
	for id, o := range map[string]string{
		"s1": opts(withConfig),
		"s2": opts(link), // same folder via a symlink: one row
		"s3": opts(noConfig),
		"s4": opts(filepath.Join(noConfig, "missing")),
		"s5": `not json`,
	} {
		if _, err := db.db.Exec(`INSERT INTO agent_sessions (id, options) VALUES (?, ?)`, id, o); err != nil {
			t.Fatal(err)
		}
	}

	mustExec(t, db, `DROP TABLE trusted_workspaces`)
	mustExec(t, db, `DELETE FROM schema_migrations WHERE version = 51`)
	if err := runMigrations(db.db); err != nil {
		t.Fatal(err)
	}

	if n := count(t, db, "trusted_workspaces"); n != 1 {
		t.Fatalf("trusted_workspaces has %d rows, want 1", n)
	}
	canonical, _ := workspacetrust.Canonical(withConfig)
	inv, err := workspacetrust.Scan(withConfig, workspacetrust.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var path, fp, by string
	if err := db.db.QueryRow(`SELECT path, fingerprint, trusted_by FROM trusted_workspaces`).Scan(&path, &fp, &by); err != nil {
		t.Fatal(err)
	}
	if path != canonical || fp != inv.Fingerprint || by != workspacetrust.TrustedByGrandfathered {
		t.Fatalf("row = (%s, %s, %s), want (%s, %s, grandfathered)", path, fp, by, canonical, inv.Fingerprint)
	}

	// The grandfathered folder is trusted; once its config changes it is not.
	c := workspacetrust.NewChecker(db.db, workspacetrust.Options{})
	if d := c.Decide(withConfig); d.Status != workspacetrust.StatusTrusted {
		t.Fatalf("grandfathered folder: %s", d.Status)
	}
}

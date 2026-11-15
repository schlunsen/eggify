package database

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// claudeModels returns the Claude provider's model list from a database.
func claudeModels(t *testing.T, db *Database) []string {
	t.Helper()

	var modelsJSON string
	if err := db.db.QueryRow(
		`SELECT models FROM providers WHERE provider_id = 'claude'`,
	).Scan(&modelsJSON); err != nil {
		t.Fatalf("Failed to query Claude provider models: %v", err)
	}

	var models []string
	if err := json.Unmarshal([]byte(modelsJSON), &models); err != nil {
		t.Fatalf("Failed to unmarshal Claude models %q: %v", modelsJSON, err)
	}
	return models
}

// TestClaudeCurrentModelsAvailable verifies that the current Claude lineup is
// selectable after the database is brought up to date.
//
// The model list reaches a database by two independent routes — the seed file
// for a fresh install, and a migration for an existing one — and they are easy
// to update out of step. This asserts the end state a user actually sees,
// whichever route produced it.
func TestClaudeCurrentModelsAvailable(t *testing.T) {
	ResetInstance()

	tempDir, err := os.MkdirTemp("", "cct_claude_models_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	db, err := Initialize(tempDir)
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	models := claudeModels(t, db)

	present := make(map[string]bool, len(models))
	for _, m := range models {
		present[m] = true
	}

	// The current lineup per the Claude platform docs.
	for _, want := range []string{
		"claude-fable-5-2",
		"claude-fable-5-5",
		"claude-fable-5-1",
		"claude-mythos-5-2",
		"claude-mythos-5-1",
		"claude-opus-5-5",
		"claude-sonnet-5-5",
		"claude-haiku-5-5",
		"claude-opus-5",
		"claude-sonnet-5",
		"claude-haiku-4-5-20251001",
	} {
		if !present[want] {
			t.Errorf("Claude model %q is not selectable; got %v", want, models)
		}
	}

	// Duplicates would render twice in the model picker.
	seen := make(map[string]bool, len(models))
	for _, m := range models {
		if seen[m] {
			t.Errorf("Claude model %q listed more than once: %v", m, models)
		}
		seen[m] = true
	}
}

// TestMigrationAddsClaudeSonnet55 checks an existing install: the stored list
// gains claude-sonnet-5-5 after claude-opus-5-5, keeps the user's own entries,
// and a second run changes nothing.
func TestMigrationAddsClaudeSonnet55(t *testing.T) {
	ResetInstance()

	tempDir, err := os.MkdirTemp("", "cct_sonnet55_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	db, err := Initialize(tempDir)
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	old := `["sonnet","claude-opus-5-5","claude-fable-5-1","claude-sonnet-5","my-custom-model"]`
	if _, err := db.db.Exec(`UPDATE providers SET models = ? WHERE provider_id = 'claude'`, old); err != nil {
		t.Fatalf("Failed to set old model list: %v", err)
	}

	for run := 0; run < 2; run++ {
		tx, err := db.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateAddClaudeSonnet55(tx); err != nil {
			tx.Rollback()
			t.Fatalf("migration run %d: %v", run, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	got := claudeModels(t, db)
	want := []string{"sonnet", "claude-opus-5-5", "claude-sonnet-5-5", "claude-fable-5-1", "claude-sonnet-5", "my-custom-model"}
	if len(got) != len(want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("models = %v, want %v", got, want)
		}
	}
}

// TestMigrationAddsClaudeHaiku55 checks an existing install: the stored list
// gains claude-haiku-5-5 after claude-sonnet-5-5, keeps the user's own
// entries, and a second run changes nothing.
func TestMigrationAddsClaudeHaiku55(t *testing.T) {
	ResetInstance()

	tempDir, err := os.MkdirTemp("", "cct_haiku55_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	db, err := Initialize(tempDir)
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	old := `["sonnet","claude-opus-5-5","claude-sonnet-5-5","claude-fable-5-1","my-custom-model"]`
	if _, err := db.db.Exec(`UPDATE providers SET models = ? WHERE provider_id = 'claude'`, old); err != nil {
		t.Fatalf("Failed to set old model list: %v", err)
	}

	for run := 0; run < 2; run++ {
		tx, err := db.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateAddClaudeHaiku55(tx); err != nil {
			tx.Rollback()
			t.Fatalf("migration run %d: %v", run, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	got := claudeModels(t, db)
	want := []string{"sonnet", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-1", "my-custom-model"}
	if len(got) != len(want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("models = %v, want %v", got, want)
		}
	}
}

// TestMigrationAddsClaudeFable52 checks an existing install: the stored list
// gains claude-fable-5-2 ahead of the older Fable models, keeps the user's own
// entries, and a second run changes nothing. A list without Fable models gets
// it before Opus 5.5.
func TestMigrationAddsClaudeFable52(t *testing.T) {
	cases := []struct {
		old  string
		want []string
	}{
		{`["sonnet","fable","claude-fable-5-5","claude-opus-5-5","claude-fable-5-1","my-custom-model"]`,
			[]string{"sonnet", "fable", "claude-fable-5-2", "claude-fable-5-5", "claude-opus-5-5", "claude-fable-5-1", "my-custom-model"}},
		{`["sonnet","claude-opus-5-5","claude-sonnet-5-5"]`,
			[]string{"sonnet", "claude-fable-5-2", "claude-opus-5-5", "claude-sonnet-5-5"}},
	}
	for _, tc := range cases {
		ResetInstance()
		tempDir, err := os.MkdirTemp("", "cct_fable52_*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		db, err := Initialize(tempDir)
		if err != nil {
			os.RemoveAll(tempDir)
			t.Fatalf("Failed to initialize database: %v", err)
		}
		if _, err := db.db.Exec(`UPDATE providers SET models = ? WHERE provider_id = 'claude'`, tc.old); err != nil {
			t.Fatalf("Failed to set old model list: %v", err)
		}
		for run := 0; run < 2; run++ {
			tx, err := db.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err := migrateAddClaudeFable52(tx); err != nil {
				tx.Rollback()
				t.Fatalf("migration run %d: %v", run, err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
		got := claudeModels(t, db)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("from %s: models = %v, want %v", tc.old, got, tc.want)
		}
		db.Close()
		os.RemoveAll(tempDir)
	}
}

// TestMigrationAddsClaudeMythos checks an existing install: the stored list
// gains claude-mythos-5-2 and claude-mythos-5-1 ahead of Opus 5, keeps the
// user's own entries, and a second run changes nothing. A list without Opus 5
// gets them at the end, and one that already has a Mythos model keeps it once.
func TestMigrationAddsClaudeMythos(t *testing.T) {
	cases := []struct {
		old  string
		want []string
	}{
		{`["sonnet","claude-fable-5-1","claude-fable-5","claude-opus-5","claude-sonnet-5","my-custom-model"]`,
			[]string{"sonnet", "claude-fable-5-1", "claude-fable-5", "claude-mythos-5-2", "claude-mythos-5-1", "claude-opus-5", "claude-sonnet-5", "my-custom-model"}},
		{`["sonnet","claude-opus-5-5"]`,
			[]string{"sonnet", "claude-opus-5-5", "claude-mythos-5-2", "claude-mythos-5-1"}},
		{`["claude-mythos-5-1","claude-opus-5"]`,
			[]string{"claude-mythos-5-1", "claude-mythos-5-2", "claude-opus-5"}},
	}
	for _, tc := range cases {
		ResetInstance()
		tempDir, err := os.MkdirTemp("", "cct_mythos_*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		db, err := Initialize(tempDir)
		if err != nil {
			os.RemoveAll(tempDir)
			t.Fatalf("Failed to initialize database: %v", err)
		}
		if _, err := db.db.Exec(`UPDATE providers SET models = ? WHERE provider_id = 'claude'`, tc.old); err != nil {
			t.Fatalf("Failed to set old model list: %v", err)
		}
		for run := 0; run < 2; run++ {
			tx, err := db.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err := migrateAddClaudeMythos(tx); err != nil {
				tx.Rollback()
				t.Fatalf("migration run %d: %v", run, err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
		got := claudeModels(t, db)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("from %s: models = %v, want %v", tc.old, got, tc.want)
		}
		db.Close()
		os.RemoveAll(tempDir)
	}
}

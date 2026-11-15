package database

import "testing"

// TestSubagentModelMigration checks migration 62 adds background_agents.model
// and model_source, leaves older rows NULL and runs twice.
func TestSubagentModelMigration(t *testing.T) {
	db, _ := newReviewTestDB(t)
	for _, col := range []string{"model", "model_source"} {
		if !columnExists(t, db, "background_agents", col) {
			t.Fatalf("background_agents.%s missing", col)
		}
	}
	var applied int
	db.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 62`).Scan(&applied)
	if applied != 1 {
		t.Error("migration 62 not recorded")
	}

	// A row from before migration 62 keeps NULL.
	mustExec(t, db, "ALTER TABLE background_agents DROP COLUMN model")
	mustExec(t, db, "ALTER TABLE background_agents DROP COLUMN model_source")
	mustExec(t, db, `INSERT INTO agent_sessions (id) VALUES ('s1')`)
	mustExec(t, db, `INSERT INTO background_agents (agent_id, parent_session_id, subagent_type, description, status)
		VALUES ('a1', 's1', 'Explore', 'd', 'completed')`)
	for i := 0; i < 2; i++ { // idempotent
		tx, err := db.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateBackgroundAgentModel(tx); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	var model, source *string
	if err := db.db.QueryRow(`SELECT model, model_source FROM background_agents WHERE agent_id = 'a1'`).Scan(&model, &source); err != nil {
		t.Fatal(err)
	}
	if model != nil || source != nil {
		t.Fatalf("old row got model %v / %v, want NULL", model, source)
	}
}

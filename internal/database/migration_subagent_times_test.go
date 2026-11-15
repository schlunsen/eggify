package database

import "testing"

// TestSubagentOutputTimesMigration checks migration 61 adds
// background_agents.recent_output_at, leaves older rows NULL and runs twice.
func TestSubagentOutputTimesMigration(t *testing.T) {
	db, _ := newReviewTestDB(t)
	if !columnExists(t, db, "background_agents", "recent_output_at") {
		t.Fatal("background_agents.recent_output_at missing")
	}
	var applied int
	db.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 61`).Scan(&applied)
	if applied != 1 {
		t.Error("migration 61 not recorded")
	}

	// A row from before migration 61 keeps NULL times.
	mustExec(t, db, "ALTER TABLE background_agents DROP COLUMN recent_output_at")
	mustExec(t, db, `INSERT INTO agent_sessions (id) VALUES ('s1')`)
	mustExec(t, db, `INSERT INTO background_agents (agent_id, parent_session_id, subagent_type, description, status, recent_output)
		VALUES ('a1', 's1', 'Explore', 'd', 'completed', '["TOOL:Read: x"]')`)
	for i := 0; i < 2; i++ { // idempotent
		tx, err := db.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateBackgroundAgentOutputTimes(tx); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	var times *string
	if err := db.db.QueryRow(`SELECT recent_output_at FROM background_agents WHERE agent_id = 'a1'`).Scan(&times); err != nil {
		t.Fatal(err)
	}
	if times != nil {
		t.Fatalf("old row got times %q, want NULL", *times)
	}
}

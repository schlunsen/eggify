package database

import (
	"context"
	"testing"
)

// TestForeignKeysOnEveryConnection checks that the per-connection settings
// come from the DSN, so every pooled connection enforces foreign keys.
func TestForeignKeysOnEveryConnection(t *testing.T) {
	ResetInstance()
	db, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ResetInstance()

	ctx := context.Background()

	// Hold several connections at once so the pool must open new ones.
	for i := 0; i < 3; i++ {
		c, err := db.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()

		var fk, busy int
		var journal, tempStore string
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA temp_store").Scan(&tempStore); err != nil {
			t.Fatal(err)
		}
		if fk != 1 {
			t.Errorf("connection %d: foreign_keys = %d, want 1", i, fk)
		}
		if journal != "wal" {
			t.Errorf("connection %d: journal_mode = %q, want wal", i, journal)
		}
		if busy != 5000 {
			t.Errorf("connection %d: busy_timeout = %d, want 5000", i, busy)
		}
		if tempStore != "2" { // 2 = MEMORY
			t.Errorf("connection %d: temp_store = %q, want 2", i, tempStore)
		}
	}
}

// TestSessionDeleteCascades checks ON DELETE CASCADE fires regardless of
// which pooled connection runs the delete.
func TestSessionDeleteCascades(t *testing.T) {
	ResetInstance()
	db, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ResetInstance()

	ctx := context.Background()
	held, err := db.db.Conn(ctx) // keep the first connection busy
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	mustExec(t, db, `INSERT INTO agent_sessions (id) VALUES ('s1')`)
	mustExec(t, db, `INSERT INTO agent_messages (id, session_id, sequence, role, content) VALUES ('m1', 's1', 1, 'user', 'hi')`)
	mustExec(t, db, `INSERT INTO background_agents (agent_id, parent_session_id) VALUES ('a1', 's1')`)
	mustExec(t, db, `DELETE FROM agent_sessions WHERE id = 's1'`)

	for _, table := range []string{"agent_messages", "background_agents"} {
		if n := count(t, db, table); n != 0 {
			t.Errorf("%s has %d rows after session delete, want 0", table, n)
		}
	}
}

// TestRemoveSessionOrphansMigration creates rows whose session is gone and
// checks migration 47 removes them, keeps live rows and drops the redundant
// agent_messages indexes.
func TestRemoveSessionOrphansMigration(t *testing.T) {
	ResetInstance()
	db, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ResetInstance()

	ctx := context.Background()
	conn, err := db.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Orphans could only be written with foreign keys off.
	stmts := []string{
		`PRAGMA foreign_keys = OFF`,
		`INSERT INTO agent_sessions (id) VALUES ('live')`,
		`INSERT INTO agent_messages (id, session_id, sequence, role, content) VALUES ('m-live', 'live', 1, 'user', 'x')`,
		`INSERT INTO agent_messages (id, session_id, sequence, role, content) VALUES ('m-gone', 'gone', 1, 'user', 'x')`,
		`INSERT INTO agent_model_switches (message_id, session_id, history_mode) VALUES ('m-gone', 'gone', 'none')`,
		`INSERT INTO background_agents (agent_id, parent_session_id) VALUES ('a-live', 'live')`,
		`INSERT INTO background_agents (agent_id, parent_session_id) VALUES ('a-gone', 'gone')`,
		`INSERT INTO session_connectors (session_id, connector_slug) VALUES ('gone', 'github')`,
		`INSERT INTO agent_handovers (handover_token, source_session_id, handover_data, expires_at) VALUES ('t', 'gone', '{}', CURRENT_TIMESTAMP)`,
		`INSERT INTO terminal_sessions (id, agent_session_id, shell, working_directory) VALUES ('t-gone', 'gone', 'zsh', '/')`,
		`INSERT INTO terminal_history (terminal_session_id, command) VALUES ('t-gone', 'ls')`,
		`INSERT INTO session_digests (session_id) VALUES ('gone')`,
		`INSERT INTO session_digests (session_id) VALUES ('live')`,
		`INSERT INTO session_tags (session_id) VALUES ('gone')`,
		`CREATE INDEX idx_agent_messages_session ON agent_messages(session_id, sequence ASC)`,
		`CREATE INDEX idx_agent_messages_timestamp ON agent_messages(timestamp DESC)`,
		`PRAGMA foreign_keys = ON`,
	}
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	conn.Close()

	mustExec(t, db, `DELETE FROM schema_migrations WHERE version = 47`)
	orphanRowsDeleted = 0
	if err := runMigrations(db.db); err != nil {
		t.Fatal(err)
	}

	want := map[string]int{
		"agent_messages":       1,
		"agent_model_switches": 0,
		"background_agents":    1,
		"session_connectors":   0,
		"agent_handovers":      0,
		"terminal_sessions":    0,
		"terminal_history":     0,
		"session_digests":      1,
		"session_tags":         0,
	}
	for table, n := range want {
		if got := count(t, db, table); got != n {
			t.Errorf("%s: %d rows, want %d", table, got, n)
		}
	}
	if orphanRowsDeleted != 9 {
		t.Errorf("orphanRowsDeleted = %d, want 9", orphanRowsDeleted)
	}

	for _, idx := range []string{"idx_agent_messages_session", "idx_agent_messages_timestamp"} {
		var n int
		if err := db.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("index %s still exists", idx)
		}
	}

	// The vacuum that follows a large cleanup must work on a live pool.
	vacuumDatabase(db.db, db.path)
	if n := count(t, db, "agent_messages"); n != 1 {
		t.Errorf("agent_messages after vacuum: %d rows, want 1", n)
	}
}

func mustExec(t *testing.T, db *Database, query string) {
	t.Helper()
	if _, err := db.db.Exec(query); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func count(t *testing.T, db *Database, table string) int {
	t.Helper()
	var n int
	if err := db.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestHookExecutionUnknownSession checks that a hook execution from a session
// eggify doesn't manage is still recorded (with no session) now that foreign keys
// are enforced on every connection.
func TestHookExecutionUnknownSession(t *testing.T) {
	ResetInstance()
	db, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ResetInstance()

	sid := "claude-code-session-not-in-eggify"
	repo := NewHookRepository(db)
	exec := &HookExecution{SessionID: &sid, EventName: "PreToolUse", HookType: "command"}
	if err := repo.RecordExecution(exec); err != nil {
		t.Fatalf("RecordExecution with unknown session: %v", err)
	}
	var got *string
	if err := db.db.QueryRow("SELECT session_id FROM hook_executions WHERE id = ?", exec.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("session_id = %q, want NULL", *got)
	}
}

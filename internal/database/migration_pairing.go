package database

import (
	"database/sql"
	"fmt"
)

// migratePairing (54) adds the pairing_codes table and device metadata on
// user_sessions.
//
// pairing_codes keeps only HashToken(code): a copy of eggify.db never yields a
// redeemable code. user_sessions gains a public session_id (used to list and
// revoke a session without exposing its token hash), a device name/model, how
// the session was created ("password", "oauth", "pairing", ...) and when it
// was last used.
func migratePairing(tx *sql.Tx) error {
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS pairing_codes (
		id                    TEXT PRIMARY KEY,
		code_hash             TEXT NOT NULL UNIQUE,
		username              TEXT NOT NULL,
		created_by_session    TEXT NOT NULL DEFAULT '',
		device_name_hint      TEXT NOT NULL DEFAULT '',
		base_url              TEXT NOT NULL DEFAULT '',
		created_at            INTEGER NOT NULL,
		expires_at            INTEGER NOT NULL,
		used_at               INTEGER,
		revoked_at            INTEGER,
		redeemed_session_id   TEXT NOT NULL DEFAULT '',
		redeemed_device_name  TEXT NOT NULL DEFAULT '',
		FOREIGN KEY (username) REFERENCES users(username) ON DELETE CASCADE
	)`); err != nil {
		return fmt.Errorf("failed to create pairing_codes: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_pairing_codes_username ON pairing_codes(username, created_at DESC)`); err != nil {
		return fmt.Errorf("failed to index pairing_codes: %w", err)
	}

	columns := []struct{ name, def string }{
		{"session_id", "TEXT NOT NULL DEFAULT ''"},
		{"device_name", "TEXT NOT NULL DEFAULT ''"},
		{"device_model", "TEXT NOT NULL DEFAULT ''"},
		{"created_via", "TEXT NOT NULL DEFAULT 'password'"},
		{"last_used_at", "TIMESTAMP"},
	}
	for _, col := range columns {
		var exists bool
		if err := tx.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info('user_sessions') WHERE name = ?`, col.name).Scan(&exists); err != nil {
			return fmt.Errorf("failed to check user_sessions.%s: %w", col.name, err)
		}
		if exists {
			continue
		}
		if _, err := tx.Exec(fmt.Sprintf(`ALTER TABLE user_sessions ADD COLUMN %s %s`, col.name, col.def)); err != nil {
			return fmt.Errorf("failed to add user_sessions.%s: %w", col.name, err)
		}
	}
	// Existing sessions get a public id.
	if _, err := tx.Exec(`UPDATE user_sessions SET session_id = lower(hex(randomblob(16))) WHERE session_id = ''`); err != nil {
		return fmt.Errorf("failed to assign session ids: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_user_sessions_session_id ON user_sessions(session_id)`); err != nil {
		return fmt.Errorf("failed to index user_sessions.session_id: %w", err)
	}
	return nil
}

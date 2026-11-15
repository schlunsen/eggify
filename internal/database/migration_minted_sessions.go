package database

import (
	"database/sql"
	"fmt"
)

// migrateMintedSessions (65) adds user_sessions.minted_by_session: for a
// session created by redeeming a pairing code, the public session id of the
// session that made the code. Signing out or revoking that session then
// revokes the sessions it minted (before, a signed-out Mac left its paired
// phone signed in). Existing rows start empty (minter unknown).
func migrateMintedSessions(tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info('user_sessions') WHERE name = 'minted_by_session'`).Scan(&exists); err != nil {
		return fmt.Errorf("failed to check user_sessions.minted_by_session: %w", err)
	}
	if !exists {
		if _, err := tx.Exec(`ALTER TABLE user_sessions ADD COLUMN minted_by_session TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("failed to add user_sessions.minted_by_session: %w", err)
		}
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_user_sessions_minted_by ON user_sessions(minted_by_session)`); err != nil {
		return fmt.Errorf("failed to index user_sessions.minted_by_session: %w", err)
	}
	return nil
}

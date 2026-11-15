package database

import (
	"database/sql"
	"fmt"
)

// migrateMFAThrottle (66) adds mfa_throttle: the per-account MFA attempt
// count, lock and last accepted TOTP time step, so a restart neither clears a
// lock nor lets a code that was just accepted be used again.
func migrateMFAThrottle(tx *sql.Tx) error {
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS mfa_throttle (
		username     TEXT PRIMARY KEY,
		attempts     INTEGER NOT NULL DEFAULT 0,
		locked_until INTEGER NOT NULL DEFAULT 0,
		last_attempt INTEGER NOT NULL DEFAULT 0,
		last_step    INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		return fmt.Errorf("failed to create mfa_throttle: %w", err)
	}
	return nil
}

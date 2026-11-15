package database

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
)

// HashToken returns the at-rest form of a bearer token: the hex SHA-256 of it.
//
// Bearer tokens that grant access (user session tokens, MFA login tokens,
// handover tokens) are random and high-entropy, so a fast unsalted hash is
// enough: the database holds only hashes, a copy of eggify.db (readable by any
// process running as the user, including agents) no longer yields usable
// tokens, and lookups stay a simple equality match on the hash.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// hashedTokenColumns lists the plaintext bearer-token columns that migration
// 49 rewrites to HashToken(value).
var hashedTokenColumns = []struct{ table, column string }{
	{"user_sessions", "token"},
	{"mfa_temporary_tokens", "token"},
	{"agent_handovers", "handover_token"},
}

// migrateHashBearerTokens replaces every stored bearer token with its hash, in
// place, so existing sessions keep working (the client still holds the
// plaintext token and the server now looks it up by hash).
//
// It is not idempotent (hashing twice would lock everyone out); the migration
// runner applies it exactly once.
func migrateHashBearerTokens(tx *sql.Tx) error {
	for _, col := range hashedTokenColumns {
		rows, err := tx.Query(fmt.Sprintf(`SELECT %s FROM %s`, col.column, col.table))
		if err != nil {
			return fmt.Errorf("failed to read %s.%s: %w", col.table, col.column, err)
		}
		var tokens []string
		for rows.Next() {
			var tok sql.NullString
			if err := rows.Scan(&tok); err != nil {
				rows.Close()
				return fmt.Errorf("failed to scan %s.%s: %w", col.table, col.column, err)
			}
			if tok.Valid && tok.String != "" {
				tokens = append(tokens, tok.String)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		update := fmt.Sprintf(`UPDATE %s SET %s = ? WHERE %s = ?`, col.table, col.column, col.column)
		for _, tok := range tokens {
			if _, err := tx.Exec(update, HashToken(tok), tok); err != nil {
				return fmt.Errorf("failed to hash %s.%s: %w", col.table, col.column, err)
			}
		}
	}
	return nil
}

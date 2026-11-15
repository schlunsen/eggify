// Package database provides data access methods for MFA (Multi-Factor Authentication) management.
// This repository handles all operations related to MFA configuration, audit logging,
// and temporary token management.
package database

import (
	"database/sql"
	"time"
)

// MFARepository provides data access methods for MFA operations
type MFARepository struct {
	db *Database
}

// NewMFARepository creates a new MFA repository instance
func NewMFARepository(db *Database) *MFARepository {
	return &MFARepository{db: db}
}

// ============================================
// MFA Configuration Operations
// ============================================

// SaveMFAConfig saves or updates MFA configuration for a user
func (r *MFARepository) SaveMFAConfig(config *MFAConfig) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	query := `
		INSERT INTO user_mfa_config (
			username, is_enabled, totp_secret, totp_secret_iv, backup_codes,
			mfa_enabled_at, last_verified_at, last_verification_method, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET
			is_enabled = excluded.is_enabled,
			totp_secret = excluded.totp_secret,
			totp_secret_iv = excluded.totp_secret_iv,
			backup_codes = excluded.backup_codes,
			mfa_enabled_at = excluded.mfa_enabled_at,
			last_verified_at = excluded.last_verified_at,
			last_verification_method = excluded.last_verification_method,
			updated_at = excluded.updated_at
	`

	_, err := r.db.db.Exec(
		query,
		config.Username,
		config.IsEnabled,
		config.TOTPSecret,
		config.TOTPSecretIV,
		config.BackupCodes,
		config.MFAEnabledAt,
		config.LastVerifiedAt,
		config.LastVerificationMethod,
		config.CreatedAt,
		time.Now(),
	)
	return err
}

// GetMFAConfig retrieves MFA configuration for a user
func (r *MFARepository) GetMFAConfig(username string) (*MFAConfig, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()

	query := `
		SELECT id, username, is_enabled, totp_secret, totp_secret_iv, backup_codes,
		       mfa_enabled_at, last_verified_at, last_verification_method, created_at, updated_at
		FROM user_mfa_config WHERE username = ?
	`

	config := &MFAConfig{}
	err := r.db.db.QueryRow(query, username).Scan(
		&config.ID,
		&config.Username,
		&config.IsEnabled,
		&config.TOTPSecret,
		&config.TOTPSecretIV,
		&config.BackupCodes,
		&config.MFAEnabledAt,
		&config.LastVerifiedAt,
		&config.LastVerificationMethod,
		&config.CreatedAt,
		&config.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil // User has no MFA config yet
	}
	if err != nil {
		return nil, err
	}

	return config, nil
}

// DisableMFAForUser disables MFA for a user and clears secrets
func (r *MFARepository) DisableMFAForUser(username string) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	query := `
		UPDATE user_mfa_config SET
			is_enabled = 0,
			totp_secret = NULL,
			totp_secret_iv = NULL,
			backup_codes = NULL,
			mfa_enabled_at = NULL,
			updated_at = ?
		WHERE username = ?
	`

	_, err := r.db.db.Exec(query, time.Now(), username)
	return err
}

// GetMFAStats returns statistics about MFA usage
func (r *MFARepository) GetMFAStats() (map[string]interface{}, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()

	stats := make(map[string]interface{})

	// Get count of users with MFA enabled
	var mfaCount int
	err := r.db.db.QueryRow(`SELECT COUNT(*) FROM user_mfa_config WHERE is_enabled = 1`).Scan(&mfaCount)
	if err != nil {
		return nil, err
	}
	stats["mfa_enabled_users"] = mfaCount

	// Get success/failure stats
	var successCount, failureCount int
	err = r.db.db.QueryRow(`SELECT COUNT(*) FROM mfa_audit_log WHERE success = 1`).Scan(&successCount)
	if err != nil {
		return nil, err
	}
	stats["successful_verifications"] = successCount

	err = r.db.db.QueryRow(`SELECT COUNT(*) FROM mfa_audit_log WHERE success = 0`).Scan(&failureCount)
	if err != nil {
		return nil, err
	}
	stats["failed_verifications"] = failureCount

	// Get backup code usage
	var backupCodeUsage int
	err = r.db.db.QueryRow(`SELECT COUNT(*) FROM mfa_audit_log WHERE method = 'backup_code'`).Scan(&backupCodeUsage)
	if err != nil {
		return nil, err
	}
	stats["backup_code_usages"] = backupCodeUsage

	return stats, nil
}

// ============================================
// MFA Audit Log Operations
// ============================================

// LogMFAAudit logs an MFA audit event
func (r *MFARepository) LogMFAAudit(log *MFAAuditLog) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	query := `
		INSERT INTO mfa_audit_log (username, action, method, success, ip_address, user_agent, reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := r.db.db.Exec(
		query,
		log.Username,
		log.Action,
		log.Method,
		log.Success,
		log.IPAddress,
		log.UserAgent,
		log.Reason,
		time.Now(),
	)
	return err
}

// GetMFAAuditLog retrieves MFA audit logs for a user
func (r *MFARepository) GetMFAAuditLog(username string, limit int, offset int) ([]*MFAAuditLog, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()

	query := `
		SELECT id, username, action, method, success, ip_address, user_agent, reason, created_at
		FROM mfa_audit_log WHERE username = ?
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`

	rows, err := r.db.db.Query(query, username, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []*MFAAuditLog
	for rows.Next() {
		log := &MFAAuditLog{}
		err := rows.Scan(
			&log.ID,
			&log.Username,
			&log.Action,
			&log.Method,
			&log.Success,
			&log.IPAddress,
			&log.UserAgent,
			&log.Reason,
			&log.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}

	return logs, rows.Err()
}

// ============================================
// MFA Temporary Token Operations
// ============================================

// SaveMFATemporaryToken saves a temporary token for the MFA flow
func (r *MFARepository) SaveMFATemporaryToken(token *MFATemporaryToken) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	query := `
		INSERT INTO mfa_temporary_tokens (token, username, token_type, totp_secret, verified, attempts, max_attempts, created_at, expires_at, ip_address, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := r.db.db.Exec(
		query,
		HashToken(token.Token), // SECURITY: stored hashed (migration 49)
		token.Username,
		token.TokenType,
		token.TOTPSecret,
		token.Verified,
		token.Attempts,
		token.MaxAttempts,
		token.CreatedAt,
		token.ExpiresAt,
		token.IPAddress,
		token.UserAgent,
	)
	return err
}

// GetMFATemporaryToken retrieves a temporary token
func (r *MFARepository) GetMFATemporaryToken(token string) (*MFATemporaryToken, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()

	query := `
		SELECT id, token, username, token_type, totp_secret, verified, attempts, max_attempts, created_at, expires_at, ip_address, user_agent
		FROM mfa_temporary_tokens WHERE token = ?
	`

	t := &MFATemporaryToken{}
	err := r.db.db.QueryRow(query, HashToken(token)).Scan(
		&t.ID,
		&t.Token,
		&t.Username,
		&t.TokenType,
		&t.TOTPSecret,
		&t.Verified,
		&t.Attempts,
		&t.MaxAttempts,
		&t.CreatedAt,
		&t.ExpiresAt,
		&t.IPAddress,
		&t.UserAgent,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// The row holds the hash; hand callers back the token they presented.
	t.Token = token

	return t, nil
}

// UpdateMFATemporaryToken updates a temporary token's state
func (r *MFARepository) UpdateMFATemporaryToken(token *MFATemporaryToken) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	query := `
		UPDATE mfa_temporary_tokens SET
			verified = ?,
			attempts = ?
		WHERE token = ?
	`

	_, err := r.db.db.Exec(query, token.Verified, token.Attempts, HashToken(token.Token))
	return err
}

// DeleteMFATemporaryToken deletes a specific temporary token by its token string.
// SECURITY: Used to invalidate tokens immediately after successful MFA verification
// to prevent replay attacks within the token's expiration window.
func (r *MFARepository) DeleteMFATemporaryToken(token string) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	query := `DELETE FROM mfa_temporary_tokens WHERE token = ?`
	_, err := r.db.db.Exec(query, HashToken(token))
	return err
}

// DeleteExpiredMFATemporaryTokens deletes all expired temporary tokens
func (r *MFARepository) DeleteExpiredMFATemporaryTokens() error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	query := `DELETE FROM mfa_temporary_tokens WHERE expires_at < ?`
	_, err := r.db.db.Exec(query, time.Now())
	return err
}

// ClaimMFATemporaryTokenAttempt atomically counts one verification attempt
// on a temporary token. It returns false when the token is gone or has no
// attempts left. SECURITY: the check and the increment are one statement, so
// parallel guesses on one token cannot all be evaluated (each needs its own
// claimed attempt before its code is checked).
func (r *MFARepository) ClaimMFATemporaryTokenAttempt(token string) (bool, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	res, err := r.db.db.Exec(`
		UPDATE mfa_temporary_tokens SET attempts = attempts + 1
		WHERE token = ? AND attempts < max_attempts`, HashToken(token))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// ConsumeMFATemporaryToken deletes a temporary token and reports whether this
// call was the one that deleted it, so two parallel correct codes cannot
// both turn one login into two sessions.
func (r *MFARepository) ConsumeMFATemporaryToken(token string) (bool, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	res, err := r.db.db.Exec(`DELETE FROM mfa_temporary_tokens WHERE token = ?`, HashToken(token))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// SwapMFABackupCodes replaces a user's backup codes only if they still equal
// oldCodes (compare and swap). It returns false when another request changed
// them first, e.g. used the same backup code in parallel.
func (r *MFARepository) SwapMFABackupCodes(username, oldCodes, newCodes string) (bool, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	res, err := r.db.db.Exec(`
		UPDATE user_mfa_config SET backup_codes = ?, updated_at = ?
		WHERE username = ? AND is_enabled = 1 AND backup_codes = ?`,
		newCodes, time.Now(), username, oldCodes)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// RecordMFAVerification stores when and how a user last passed MFA, without
// rewriting the rest of the config (a full SaveMFAConfig from a stale read
// could bring back a backup code another request just used).
func (r *MFARepository) RecordMFAVerification(username, method string) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	now := time.Now()
	_, err := r.db.db.Exec(`
		UPDATE user_mfa_config SET last_verified_at = ?, last_verification_method = ?, updated_at = ?
		WHERE username = ?`, now, method, now, username)
	return err
}

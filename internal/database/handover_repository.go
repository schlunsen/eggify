package database

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// HandoverRepository provides data access methods for agent handovers
type HandoverRepository struct {
	db *Database
}

// NewHandoverRepository creates a new handover repository instance
func NewHandoverRepository(db *Database) *HandoverRepository {
	return &HandoverRepository{db: db}
}

// CreateHandover creates a new agent handover
func (r *HandoverRepository) CreateHandover(handover *AgentHandover) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	query := `
		INSERT INTO agent_handovers (
			handover_token, source_session_id, target_session_id, handover_data,
			handover_note, consumed_by_session, expires_at, consumed_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`

	_, err := r.db.db.Exec(
		query,
		HashToken(handover.HandoverToken), // SECURITY: stored hashed (migration 49)
		handover.SourceSessionID,
		handover.TargetSessionID,
		handover.HandoverData,
		handover.HandoverNote,
		handover.ConsumedBySession,
		handover.ExpiresAt,
		handover.ConsumedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create handover: %w", err)
	}

	return nil
}

// GetHandoverByToken retrieves a handover by token
func (r *HandoverRepository) GetHandoverByToken(token string) (*AgentHandover, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()

	query := `
		SELECT id, handover_token, source_session_id, target_session_id, handover_data,
		       handover_note, consumed_by_session, expires_at, consumed_at, created_at
		FROM agent_handovers
		WHERE handover_token = ?
	`

	handover := &AgentHandover{}
	err := r.db.db.QueryRow(query, HashToken(token)).Scan(
		&handover.ID,
		&handover.HandoverToken,
		&handover.SourceSessionID,
		&handover.TargetSessionID,
		&handover.HandoverData,
		&handover.HandoverNote,
		&handover.ConsumedBySession,
		&handover.ExpiresAt,
		&handover.ConsumedAt,
		&handover.CreatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("handover not found: %s", token)
		}
		return nil, fmt.Errorf("failed to get handover: %w", err)
	}
	// The row holds the hash; hand callers back the token they presented.
	handover.HandoverToken = token

	return handover, nil
}

// ErrHandoverConsumed is returned by ClaimHandover when the handover was
// already consumed (or does not exist).
var ErrHandoverConsumed = errors.New("handover already consumed")

// ConsumeHandover marks a handover as consumed
func (r *HandoverRepository) ConsumeHandover(token, consumedBySessionID string) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	query := `
		UPDATE agent_handovers
		SET consumed_by_session = ?, consumed_at = CURRENT_TIMESTAMP
		WHERE handover_token = ?
	`

	result, err := r.db.db.Exec(query, consumedBySessionID, HashToken(token))
	if err != nil {
		return fmt.Errorf("failed to consume handover: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check affected rows: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("handover not found: %s", token)
	}

	return nil
}

// ClaimHandover atomically marks an unconsumed handover consumed, so two
// accepts of one token cannot both succeed. ConsumeHandover records the
// session that used it once that session exists. When handoverData is
// non-empty it replaces the stored package (used to drop permission settings
// the acceptor did not keep, so nothing can pick them up from the row later).
func (r *HandoverRepository) ClaimHandover(token, handoverData string) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	query := `
		UPDATE agent_handovers
		SET consumed_at = CURRENT_TIMESTAMP,
		    handover_data = CASE WHEN ? != '' THEN ? ELSE handover_data END
		WHERE handover_token = ? AND consumed_at IS NULL
	`
	result, err := r.db.db.Exec(query, handoverData, handoverData, HashToken(token))
	if err != nil {
		return fmt.Errorf("failed to consume handover: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check affected rows: %w", err)
	}
	if rows == 0 {
		return ErrHandoverConsumed
	}
	return nil
}

// CleanupExpiredHandovers removes expired handovers
func (r *HandoverRepository) CleanupExpiredHandovers() (int64, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	query := `
		DELETE FROM agent_handovers
		WHERE expires_at < CURRENT_TIMESTAMP AND consumed_at IS NULL
	`

	result, err := r.db.db.Exec(query)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired handovers: %w", err)
	}

	return result.RowsAffected()
}

// GetHandoversBySourceSession retrieves all handovers created by a source session.
// HandoverToken holds the stored hash (migration 49), not a usable token.
func (r *HandoverRepository) GetHandoversBySourceSession(sourceSessionID string) ([]*AgentHandover, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()

	query := `
		SELECT id, handover_token, source_session_id, target_session_id, handover_data,
		       handover_note, consumed_by_session, expires_at, consumed_at, created_at
		FROM agent_handovers
		WHERE source_session_id = ?
		ORDER BY created_at DESC
	`

	rows, err := r.db.db.Query(query, sourceSessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get handovers by source session: %w", err)
	}
	defer rows.Close()

	var handovers []*AgentHandover
	for rows.Next() {
		handover := &AgentHandover{}
		err := rows.Scan(
			&handover.ID,
			&handover.HandoverToken,
			&handover.SourceSessionID,
			&handover.TargetSessionID,
			&handover.HandoverData,
			&handover.HandoverNote,
			&handover.ConsumedBySession,
			&handover.ExpiresAt,
			&handover.ConsumedAt,
			&handover.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan handover: %w", err)
		}
		handovers = append(handovers, handover)
	}

	return handovers, nil
}

// IsHandoverExpired checks if a handover has expired
func (r *HandoverRepository) IsHandoverExpired(token string) (bool, error) {
	handover, err := r.GetHandoverByToken(token)
	if err != nil {
		return false, err
	}

	return time.Now().After(handover.ExpiresAt), nil
}

// IsHandoverConsumed checks if a handover has already been consumed
func (r *HandoverRepository) IsHandoverConsumed(token string) (bool, error) {
	handover, err := r.GetHandoverByToken(token)
	if err != nil {
		return false, err
	}

	return handover.ConsumedBySession != nil && *handover.ConsumedBySession != "", nil
}

package database

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"time"
)

// PairingCode is a one-time code that signs a device (the iOS app) in as the
// user who created it. Only HashToken(code) is stored.
type PairingCode struct {
	ID                 string
	CodeHash           string
	Username           string
	CreatedBySession   string // public session id of the creating session
	DeviceNameHint     string
	BaseURL            string
	CreatedAt          time.Time
	ExpiresAt          time.Time
	UsedAt             *time.Time
	RevokedAt          *time.Time
	RedeemedSessionID  string
	RedeemedDeviceName string
}

// Status is pending, redeemed, expired or revoked (at now).
func (p *PairingCode) Status(now time.Time) string {
	switch {
	case p.UsedAt != nil:
		return "redeemed"
	case p.RevokedAt != nil:
		return "revoked"
	case !now.Before(p.ExpiresAt):
		return "expired"
	default:
		return "pending"
	}
}

// ErrPairingCodeInvalid means the code does not exist, expired, was revoked
// or was already used. Callers must not tell these apart to the client.
var ErrPairingCodeInvalid = errors.New("pairing code invalid")

// PairingRepository stores pairing codes.
type PairingRepository struct {
	db *Database
}

// NewPairingRepository creates a pairing repository.
func NewPairingRepository(db *Database) *PairingRepository {
	return &PairingRepository{db: db}
}

// RevokeUnusedBySession revokes the unused pairing codes made by the session
// with this public id (the session was signed out or revoked).
func (r *PairingRepository) RevokeUnusedBySession(sessionID string) error {
	if sessionID == "" {
		return nil
	}
	_, err := r.db.db.Exec(`UPDATE pairing_codes SET revoked_at = ?
		WHERE created_by_session = ? AND used_at IS NULL AND revoked_at IS NULL`, time.Now().Unix(), sessionID)
	return err
}

func unixPtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0)
	return &t
}

const pairingColumns = `id, code_hash, username, created_by_session, device_name_hint, base_url,
	created_at, expires_at, used_at, revoked_at, redeemed_session_id, redeemed_device_name`

func scanPairing(scan func(dest ...any) error) (*PairingCode, error) {
	p := &PairingCode{}
	var created, expires int64
	var used, revoked sql.NullInt64
	if err := scan(&p.ID, &p.CodeHash, &p.Username, &p.CreatedBySession, &p.DeviceNameHint, &p.BaseURL,
		&created, &expires, &used, &revoked, &p.RedeemedSessionID, &p.RedeemedDeviceName); err != nil {
		return nil, err
	}
	p.CreatedAt = time.Unix(created, 0)
	p.ExpiresAt = time.Unix(expires, 0)
	p.UsedAt = unixPtr(used)
	p.RevokedAt = unixPtr(revoked)
	return p, nil
}

// Create stores a new pairing code and, in the same transaction, revokes
// every earlier unused code of the same user: only the newest code works.
func (r *PairingRepository) Create(p *PairingCode) error {
	tx, err := r.db.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().Unix()
	if _, err := tx.Exec(`UPDATE pairing_codes SET revoked_at = ?
		WHERE username = ? AND used_at IS NULL AND revoked_at IS NULL`, now, p.Username); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO pairing_codes (id, code_hash, username, created_by_session, device_name_hint,
		base_url, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.CodeHash, p.Username, p.CreatedBySession, p.DeviceNameHint, p.BaseURL,
		p.CreatedAt.Unix(), p.ExpiresAt.Unix()); err != nil {
		return err
	}
	// Old rows are useless after a day; keep the table small.
	if _, err := tx.Exec(`DELETE FROM pairing_codes WHERE expires_at < ?`, now-24*3600); err != nil {
		return err
	}
	return tx.Commit()
}

// Get returns a pairing code by its public id, or nil.
func (r *PairingRepository) Get(id string) (*PairingCode, error) {
	p, err := scanPairing(r.db.db.QueryRow(`SELECT `+pairingColumns+` FROM pairing_codes WHERE id = ?`, id).Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// Claim atomically marks the unexpired, unrevoked, unused code with this hash
// as used and returns it. A second claim of the same code (a replay, or two
// racing requests) gets ErrPairingCodeInvalid.
func (r *PairingRepository) Claim(codeHash, deviceName string, now time.Time) (*PairingCode, error) {
	res, err := r.db.db.Exec(`UPDATE pairing_codes SET used_at = ?, redeemed_device_name = ?
		WHERE code_hash = ? AND used_at IS NULL AND revoked_at IS NULL AND expires_at > ?`,
		now.Unix(), deviceName, codeHash, now.Unix())
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, ErrPairingCodeInvalid
	}
	p, err := scanPairing(r.db.db.QueryRow(`SELECT `+pairingColumns+` FROM pairing_codes WHERE code_hash = ?`, codeHash).Scan)
	if err != nil {
		return nil, err
	}
	// The lookup is an equality match on a SHA-256 of a 256-bit random code,
	// so timing reveals nothing about the code; compare again in constant
	// time anyway.
	if subtle.ConstantTimeCompare([]byte(p.CodeHash), []byte(codeHash)) != 1 {
		return nil, ErrPairingCodeInvalid
	}
	return p, nil
}

// SetRedeemedSession records the session a redeemed code created.
func (r *PairingRepository) SetRedeemedSession(id, sessionID string) error {
	_, err := r.db.db.Exec(`UPDATE pairing_codes SET redeemed_session_id = ? WHERE id = ?`, sessionID, id)
	return err
}

// Revoke cancels an unused code.
func (r *PairingRepository) Revoke(id string) error {
	_, err := r.db.db.Exec(`UPDATE pairing_codes SET revoked_at = ? WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL`,
		time.Now().Unix(), id)
	return err
}

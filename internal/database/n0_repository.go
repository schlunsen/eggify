package database

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// N0 connection statuses.
const (
	N0StatusActive      = "active"
	N0StatusNeedsReauth = "needs_reauth"
	N0StatusRevoked     = "revoked"
)

// N0OAuthClient is one dynamic client registration against an n0 host.
// RedirectKey is "loopback" for the 127.0.0.1 callback (n0 accepts any
// loopback port, so one registration serves every port) or the exact https
// redirect URI for a public callback.
type N0OAuthClient struct {
	ID                    int64
	Host                  string
	RedirectKey           string
	RedirectURI           string
	ClientID              string
	Issuer                string
	Resource              string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RevocationEndpoint    string
	RegistrationEndpoint  string
	ScopesSupported       string // space separated
	CreatedAt             time.Time
}

// N0Connection is one authorized n0 connection (bound by n0 to one app).
// AccessToken and RefreshToken hold ciphertext (connector key); this package
// never sees plaintext tokens.
type N0Connection struct {
	ID                 int64
	Host               string
	Issuer             string
	Resource           string
	ClientID           string
	RedirectURI        string
	TokenEndpoint      string
	RevocationEndpoint string
	AccessToken        string
	RefreshToken       string
	AccessExpiresAt    time.Time
	Scopes             string // space separated
	WorkspaceName      string
	AppSubdomain       string
	AppName            string
	Status             string
	StatusMessage      string
	LastUsedAt         time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// N0Repository persists n0 OAuth clients, connections and session bindings.
type N0Repository struct {
	db *Database
}

// NewN0Repository creates an n0 repository.
func NewN0Repository(db *Database) *N0Repository {
	return &N0Repository{db: db}
}

// ErrN0ConnectionNotFound is returned when a connection id does not exist.
var ErrN0ConnectionNotFound = errors.New("n0 connection not found")

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func timeOrZero(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(v, 0)
}

// --- OAuth clients ---

// GetN0OAuthClient returns the registration for (host, redirectKey), or nil.
func (r *N0Repository) GetN0OAuthClient(host, redirectKey string) (*N0OAuthClient, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()
	c := &N0OAuthClient{}
	var created int64
	err := r.db.db.QueryRow(`SELECT id, host, redirect_key, redirect_uri, client_id, issuer, resource,
		authorization_endpoint, token_endpoint, revocation_endpoint, registration_endpoint, scopes_supported, created_at
		FROM n0_oauth_clients WHERE host = ? AND redirect_key = ?`, host, redirectKey).Scan(
		&c.ID, &c.Host, &c.RedirectKey, &c.RedirectURI, &c.ClientID, &c.Issuer, &c.Resource,
		&c.AuthorizationEndpoint, &c.TokenEndpoint, &c.RevocationEndpoint, &c.RegistrationEndpoint, &c.ScopesSupported, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get n0 oauth client: %w", err)
	}
	c.CreatedAt = timeOrZero(created)
	return c, nil
}

// SaveN0OAuthClient inserts or replaces the registration for (host, redirect_key).
func (r *N0Repository) SaveN0OAuthClient(c *N0OAuthClient) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	res, err := r.db.db.Exec(`INSERT INTO n0_oauth_clients (host, redirect_key, redirect_uri, client_id, issuer, resource,
			authorization_endpoint, token_endpoint, revocation_endpoint, registration_endpoint, scopes_supported, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(host, redirect_key) DO UPDATE SET
			redirect_uri = excluded.redirect_uri, client_id = excluded.client_id, issuer = excluded.issuer,
			resource = excluded.resource, authorization_endpoint = excluded.authorization_endpoint,
			token_endpoint = excluded.token_endpoint, revocation_endpoint = excluded.revocation_endpoint,
			registration_endpoint = excluded.registration_endpoint, scopes_supported = excluded.scopes_supported,
			created_at = excluded.created_at`,
		c.Host, c.RedirectKey, c.RedirectURI, c.ClientID, c.Issuer, c.Resource,
		c.AuthorizationEndpoint, c.TokenEndpoint, c.RevocationEndpoint, c.RegistrationEndpoint, c.ScopesSupported, c.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("save n0 oauth client: %w", err)
	}
	if id, err := res.LastInsertId(); err == nil && id > 0 {
		c.ID = id
	}
	return nil
}

// DeleteN0OAuthClient forgets a registration (e.g. n0 garbage-collected it).
func (r *N0Repository) DeleteN0OAuthClient(host, redirectKey string) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	_, err := r.db.db.Exec(`DELETE FROM n0_oauth_clients WHERE host = ? AND redirect_key = ?`, host, redirectKey)
	return err
}

// CountN0ConnectionsForClient counts connections that use a client_id on a host.
func (r *N0Repository) CountN0ConnectionsForClient(host, clientID string) (int, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()
	var n int
	err := r.db.db.QueryRow(`SELECT COUNT(*) FROM n0_connections WHERE host = ? AND client_id = ?`, host, clientID).Scan(&n)
	return n, err
}

// --- Connections ---

const n0ConnectionColumns = `id, host, issuer, resource, client_id, redirect_uri, token_endpoint, revocation_endpoint,
	access_token, refresh_token, access_expires_at, scopes, workspace_name, app_subdomain, app_name,
	status, status_message, last_used_at, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanN0Connection(row rowScanner) (*N0Connection, error) {
	c := &N0Connection{}
	var expires, lastUsed, created, updated int64
	err := row.Scan(&c.ID, &c.Host, &c.Issuer, &c.Resource, &c.ClientID, &c.RedirectURI, &c.TokenEndpoint, &c.RevocationEndpoint,
		&c.AccessToken, &c.RefreshToken, &expires, &c.Scopes, &c.WorkspaceName, &c.AppSubdomain, &c.AppName,
		&c.Status, &c.StatusMessage, &lastUsed, &created, &updated)
	if err != nil {
		return nil, err
	}
	c.AccessExpiresAt = timeOrZero(expires)
	c.LastUsedAt = timeOrZero(lastUsed)
	c.CreatedAt = timeOrZero(created)
	c.UpdatedAt = timeOrZero(updated)
	return c, nil
}

// CreateN0Connection inserts a new connection and sets c.ID.
func (r *N0Repository) CreateN0Connection(c *N0Connection) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	now := time.Now()
	c.CreatedAt, c.UpdatedAt = now, now
	if c.Status == "" {
		c.Status = N0StatusActive
	}
	res, err := r.db.db.Exec(`INSERT INTO n0_connections (host, issuer, resource, client_id, redirect_uri, token_endpoint,
			revocation_endpoint, access_token, refresh_token, access_expires_at, scopes, workspace_name, app_subdomain,
			app_name, status, status_message, last_used_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Host, c.Issuer, c.Resource, c.ClientID, c.RedirectURI, c.TokenEndpoint, c.RevocationEndpoint,
		c.AccessToken, c.RefreshToken, unixOrZero(c.AccessExpiresAt), c.Scopes, c.WorkspaceName, c.AppSubdomain,
		c.AppName, c.Status, c.StatusMessage, unixOrZero(c.LastUsedAt), now.Unix(), now.Unix())
	if err != nil {
		return fmt.Errorf("create n0 connection: %w", err)
	}
	c.ID, _ = res.LastInsertId()
	return nil
}

// GetN0Connection loads one connection.
func (r *N0Repository) GetN0Connection(id int64) (*N0Connection, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()
	c, err := scanN0Connection(r.db.db.QueryRow(`SELECT `+n0ConnectionColumns+` FROM n0_connections WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, ErrN0ConnectionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get n0 connection: %w", err)
	}
	return c, nil
}

// ListN0Connections returns all connections, most recently used first.
func (r *N0Repository) ListN0Connections() ([]*N0Connection, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()
	rows, err := r.db.db.Query(`SELECT ` + n0ConnectionColumns + ` FROM n0_connections ORDER BY last_used_at DESC, created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list n0 connections: %w", err)
	}
	defer rows.Close()
	var out []*N0Connection
	for rows.Next() {
		c, err := scanN0Connection(rows)
		if err != nil {
			return nil, fmt.Errorf("scan n0 connection: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountN0Connections counts stored OAuth connections regardless of status.
func (r *N0Repository) CountN0Connections() (int, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()
	var n int
	err := r.db.db.QueryRow(`SELECT COUNT(*) FROM n0_connections`).Scan(&n)
	return n, err
}

// UpdateN0Tokens atomically stores a rotated token pair (ciphertext) and
// marks the connection active. It also releases a refresh lease held by owner.
func (r *N0Repository) UpdateN0Tokens(id int64, accessEnc, refreshEnc string, expiresAt time.Time, scopes, owner string) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	res, err := r.db.db.Exec(`UPDATE n0_connections SET access_token = ?, refresh_token = ?, access_expires_at = ?,
			scopes = CASE WHEN ? = '' THEN scopes ELSE ? END,
			status = 'active', status_message = '',
			refresh_lease_owner = CASE WHEN refresh_lease_owner = ? THEN '' ELSE refresh_lease_owner END,
			refresh_lease_until = CASE WHEN refresh_lease_owner = ? THEN 0 ELSE refresh_lease_until END,
			updated_at = ?
		WHERE id = ?`,
		accessEnc, refreshEnc, unixOrZero(expiresAt), scopes, scopes, owner, owner, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("update n0 tokens: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrN0ConnectionNotFound
	}
	return nil
}

// ReplaceN0ConnectionAuth re-authorizes an existing connection in place
// (Reconnect): new client/tokens/scopes, status back to active.
func (r *N0Repository) ReplaceN0ConnectionAuth(c *N0Connection) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	res, err := r.db.db.Exec(`UPDATE n0_connections SET host = ?, issuer = ?, resource = ?, client_id = ?, redirect_uri = ?,
			token_endpoint = ?, revocation_endpoint = ?, access_token = ?, refresh_token = ?, access_expires_at = ?,
			scopes = ?, status = 'active', status_message = '', refresh_lease_owner = '', refresh_lease_until = 0,
			updated_at = ?
		WHERE id = ?`,
		c.Host, c.Issuer, c.Resource, c.ClientID, c.RedirectURI, c.TokenEndpoint, c.RevocationEndpoint,
		c.AccessToken, c.RefreshToken, unixOrZero(c.AccessExpiresAt), c.Scopes, time.Now().Unix(), c.ID)
	if err != nil {
		return fmt.Errorf("replace n0 connection auth: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrN0ConnectionNotFound
	}
	return nil
}

// SetN0ConnectionStatus changes status (e.g. needs_reauth) and clears the tokens
// when the connection can no longer be used. Releases any refresh lease.
func (r *N0Repository) SetN0ConnectionStatus(id int64, status, message string, clearTokens bool) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	q := `UPDATE n0_connections SET status = ?, status_message = ?, refresh_lease_owner = '', refresh_lease_until = 0, updated_at = ?`
	if clearTokens {
		q += `, access_token = '', refresh_token = '', access_expires_at = 0`
	}
	_, err := r.db.db.Exec(q+` WHERE id = ?`, status, message, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("set n0 connection status: %w", err)
	}
	return nil
}

// UpdateN0ConnectionInfo stores what n0_connection_info reported.
func (r *N0Repository) UpdateN0ConnectionInfo(id int64, workspace, subdomain, appName string) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	_, err := r.db.db.Exec(`UPDATE n0_connections SET
			workspace_name = CASE WHEN ? = '' THEN workspace_name ELSE ? END,
			app_subdomain = CASE WHEN ? = '' THEN app_subdomain ELSE ? END,
			app_name = CASE WHEN ? = '' THEN app_name ELSE ? END,
			updated_at = ?
		WHERE id = ?`, workspace, workspace, subdomain, subdomain, appName, appName, time.Now().Unix(), id)
	return err
}

// TouchN0Connection records that a connection was used.
func (r *N0Repository) TouchN0Connection(id int64) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	_, err := r.db.db.Exec(`UPDATE n0_connections SET last_used_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// DeleteN0Connection removes a connection and its session bindings.
func (r *N0Repository) DeleteN0Connection(id int64) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	tx, err := r.db.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM n0_session_bindings WHERE connection_id = ?`, id); err != nil {
		tx.Rollback()
		return fmt.Errorf("delete n0 bindings: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM n0_connections WHERE id = ?`, id); err != nil {
		tx.Rollback()
		return fmt.Errorf("delete n0 connection: %w", err)
	}
	return tx.Commit()
}

// AcquireN0RefreshLease tries to take the cross-process refresh lease for a
// connection. It succeeds when no unexpired lease is held by someone else.
// Together with the in-process per-connection mutex this guarantees a
// rotating refresh token is only ever sent once, even with several Eggify
// processes sharing the database.
func (r *N0Repository) AcquireN0RefreshLease(id int64, owner string, ttl time.Duration) (bool, error) {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	now := time.Now()
	res, err := r.db.db.Exec(`UPDATE n0_connections SET refresh_lease_owner = ?, refresh_lease_until = ?
		WHERE id = ? AND (refresh_lease_until < ? OR refresh_lease_owner = '' OR refresh_lease_owner = ?)`,
		owner, now.Add(ttl).Unix(), id, now.Unix(), owner)
	if err != nil {
		return false, fmt.Errorf("acquire n0 refresh lease: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ReleaseN0RefreshLease drops a lease held by owner.
func (r *N0Repository) ReleaseN0RefreshLease(id int64, owner string) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	_, err := r.db.db.Exec(`UPDATE n0_connections SET refresh_lease_owner = '', refresh_lease_until = 0
		WHERE id = ? AND refresh_lease_owner = ?`, id, owner)
	return err
}

// --- Session bindings ---

// BindN0Session binds a session to a connection (replacing any previous binding).
func (r *N0Repository) BindN0Session(sessionID string, connectionID int64) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	_, err := r.db.db.Exec(`INSERT INTO n0_session_bindings (session_id, connection_id, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET connection_id = excluded.connection_id, updated_at = excluded.updated_at`,
		sessionID, connectionID, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("bind n0 session: %w", err)
	}
	return nil
}

// UnbindN0Session removes a session's binding.
func (r *N0Repository) UnbindN0Session(sessionID string) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	_, err := r.db.db.Exec(`DELETE FROM n0_session_bindings WHERE session_id = ?`, sessionID)
	return err
}

// GetN0SessionBinding returns the bound connection id, or 0.
func (r *N0Repository) GetN0SessionBinding(sessionID string) (int64, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()
	var id int64
	err := r.db.db.QueryRow(`SELECT connection_id FROM n0_session_bindings WHERE session_id = ?`, sessionID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// ListN0SessionBindings returns session_id → connection_id for all bindings.
func (r *N0Repository) ListN0SessionBindings() (map[string]int64, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()
	rows, err := r.db.db.Query(`SELECT session_id, connection_id FROM n0_session_bindings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var s string
		var id int64
		if err := rows.Scan(&s, &id); err != nil {
			return nil, err
		}
		out[s] = id
	}
	return out, rows.Err()
}

// SessionHasConnectorEnabled reports whether a connector slug is enabled for a session.
func (r *N0Repository) SessionHasConnectorEnabled(sessionID, slug string) (bool, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()
	var n int
	err := r.db.db.QueryRow(`SELECT COUNT(*) FROM session_connectors WHERE session_id = ? AND connector_slug = ?`, sessionID, slug).Scan(&n)
	return n > 0, err
}

// SessionsWithConnectorEnabled lists the session ids that have a connector slug enabled.
func (r *N0Repository) SessionsWithConnectorEnabled(slug string) ([]string, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()
	rows, err := r.db.db.Query(`SELECT session_id FROM session_connectors WHERE connector_slug = ?`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// EnsureN0ConnectorMarker makes sure a user_connections row exists for n0 so
// the existing session-connector plumbing (enable/disable, auto-enable) sees
// n0 as connected. The row carries no API key; a pre-existing legacy row is
// left untouched.
func (r *N0Repository) EnsureN0ConnectorMarker() error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	_, err := r.db.db.Exec(`INSERT INTO user_connections (connector_slug, api_key, extra_config, status, status_message, created_at, updated_at)
		SELECT 'n0', NULL, '{"mode":"oauth"}', 'active', 'Connected with OAuth', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		WHERE NOT EXISTS (SELECT 1 FROM user_connections WHERE connector_slug = 'n0')`)
	if err != nil {
		return fmt.Errorf("ensure n0 connector marker: %w", err)
	}
	return nil
}

// RemoveN0ConnectorMarkerIfUnused deletes the keyless n0 marker row once no
// OAuth connections remain. A legacy API-key row is never removed here.
func (r *N0Repository) RemoveN0ConnectorMarkerIfUnused() error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	_, err := r.db.db.Exec(`DELETE FROM user_connections WHERE connector_slug = 'n0'
		AND (api_key IS NULL OR api_key = '')
		AND NOT EXISTS (SELECT 1 FROM n0_connections)`)
	return err
}

// ClearLegacyN0Key removes a legacy n0 API key but keeps the row as the
// OAuth marker.
func (r *N0Repository) ClearLegacyN0Key() error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	_, err := r.db.db.Exec(`UPDATE user_connections SET api_key = NULL, extra_config = '{"mode":"oauth"}',
		status = 'active', status_message = 'Connected with OAuth', updated_at = CURRENT_TIMESTAMP
		WHERE connector_slug = 'n0'`)
	return err
}

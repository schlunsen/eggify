package n0

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schlunsen/eggify/internal/connectors"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/n0/oauth"
)

// ConnectorSlug is the connector catalog slug for n0.
const ConnectorSlug = "n0"

var (
	// ErrNotEnabled means the session does not have the n0 connector enabled.
	ErrNotEnabled = errors.New("n0 is not enabled for this session — enable the n0 connector for this session in Eggify")
	// ErrNoConnection means n0 is enabled but no connection exists.
	ErrNoConnection = errors.New("no n0 connection — connect n0 in Eggify → Connectors")
	// ErrNeedsReauth means the connection must be re-authorized by the user.
	ErrNeedsReauth = errors.New(ReauthMessage)
)

// Manager owns every n0 connection of this Eggify server: OAuth flows, token
// storage and single-flight refresh, and MCP calls to n0.
type Manager struct {
	repo  *database.N0Repository
	sqlDB *sql.DB
	key   []byte

	// HTTP is used for every request to n0.
	HTTP *http.Client

	// OnChange is called (in a new goroutine) when connections are created or
	// deleted, so running sessions can pick up a changed tool list.
	OnChange func()

	guard atomic.Pointer[ToolGuard] // approval / spending-cap check for high-impact tools

	owner       string
	now         func() time.Time
	refreshSkew time.Duration
	leaseTTL    time.Duration
	leaseWait   time.Duration
	toolsTTL    time.Duration
	clientTTL   time.Duration

	mu          sync.Mutex
	conns       map[int64]*connState
	discoveries map[string]cachedDiscovery
	flows       map[string]*flow
}

type connState struct {
	// refreshMu makes refresh single-flight per connection within this
	// process; the DB lease covers other processes.
	refreshMu sync.Mutex
	// unsaved holds a rotated token pair we failed to persist. It is written
	// before anything else happens on this connection.
	unsaved *tokenPair

	transportMu sync.Mutex
	transport   *Transport

	toolsMu sync.Mutex
	tools   []json.RawMessage
	toolsAt time.Time

	touchMu   sync.Mutex
	lastTouch time.Time
}

type tokenPair struct {
	access, refresh, scopes string
	expires                 time.Time
}

type cachedDiscovery struct {
	d  *oauth.Discovery
	at time.Time
}

// NewManager creates an n0 manager. key is the connector encryption key.
func NewManager(db *database.Database, key []byte) *Manager {
	owner := make([]byte, 12)
	_, _ = rand.Read(owner)
	return &Manager{
		repo:        database.NewN0Repository(db),
		sqlDB:       db.GetDB(),
		key:         key,
		HTTP:        &http.Client{Timeout: 2 * time.Minute},
		owner:       hex.EncodeToString(owner),
		now:         time.Now,
		refreshSkew: 2 * time.Minute,
		leaseTTL:    30 * time.Second,
		leaseWait:   45 * time.Second,
		toolsTTL:    60 * time.Second,
		clientTTL:   20 * time.Hour,
		conns:       map[int64]*connState{},
		discoveries: map[string]cachedDiscovery{},
		flows:       map[string]*flow{},
	}
}

// Repo exposes the repository (for handlers that list/bind).
func (m *Manager) Repo() *database.N0Repository { return m.repo }

func (m *Manager) state(id int64) *connState {
	m.mu.Lock()
	defer m.mu.Unlock()
	cs := m.conns[id]
	if cs == nil {
		cs = &connState{}
		m.conns[id] = cs
	}
	return cs
}

// forget drops cached MCP session and tool list for a connection.
func (m *Manager) forget(id int64) {
	m.mu.Lock()
	cs := m.conns[id]
	m.mu.Unlock()
	if cs == nil {
		return
	}
	cs.transportMu.Lock()
	cs.transport = nil
	cs.transportMu.Unlock()
	cs.toolsMu.Lock()
	cs.tools = nil
	cs.toolsMu.Unlock()
}

func (m *Manager) notifyChange() {
	if m.OnChange != nil {
		go m.OnChange()
	}
}

func (m *Manager) encrypt(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	return connectors.Encrypt(v, m.key)
}

func (m *Manager) decrypt(v string) string {
	if v == "" {
		return ""
	}
	out, err := connectors.Decrypt(v, m.key)
	if err != nil || out == v {
		// Decrypt returns its input when the value is not ciphertext; tokens
		// are always stored encrypted, so treat that as "no token".
		return ""
	}
	return out
}

// --- tokens ---

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// accessToken returns a usable access token for a connection, refreshing it
// when it expires within refreshSkew or when stale (a token n0 just rejected)
// is still the current one. Refresh is single-flight: concurrent callers wait
// on the per-connection mutex and then see the rotated token.
func (m *Manager) accessToken(ctx context.Context, id int64, stale string) (string, error) {
	cs := m.state(id)
	cs.refreshMu.Lock()
	defer cs.refreshMu.Unlock()

	if cs.unsaved != nil {
		if err := m.persistTokens(id, *cs.unsaved); err != nil {
			return "", fmt.Errorf("n0: could not save the rotated token: %w", err)
		}
		cs.unsaved = nil
	}

	deadline := m.now().Add(m.leaseWait)
	for {
		row, err := m.repo.GetN0Connection(id)
		if err != nil {
			return "", err
		}
		if row.Status != database.N0StatusActive {
			return "", ErrNeedsReauth
		}
		if tok, ok := m.usable(row, stale); ok {
			return tok, nil
		}
		got, err := m.repo.AcquireN0RefreshLease(id, m.owner, m.leaseTTL)
		if err != nil {
			return "", err
		}
		if !got {
			// Another Eggify process is refreshing; wait for its result.
			if m.now().After(deadline) {
				return "", errors.New("n0: timed out waiting for another Eggify process to refresh the token")
			}
			if err := sleepCtx(ctx, 150*time.Millisecond); err != nil {
				return "", err
			}
			continue
		}
		// Re-read under the lease: someone may have finished a refresh
		// between our read and the lease.
		row, err = m.repo.GetN0Connection(id)
		if err != nil {
			_ = m.repo.ReleaseN0RefreshLease(id, m.owner)
			return "", err
		}
		if row.Status != database.N0StatusActive {
			_ = m.repo.ReleaseN0RefreshLease(id, m.owner)
			return "", ErrNeedsReauth
		}
		if tok, ok := m.usable(row, stale); ok {
			_ = m.repo.ReleaseN0RefreshLease(id, m.owner)
			return tok, nil
		}
		return m.refreshLocked(ctx, cs, row)
	}
}

func (m *Manager) usable(row *database.N0Connection, stale string) (string, bool) {
	access := m.decrypt(row.AccessToken)
	if access == "" || row.AccessExpiresAt.Sub(m.now()) <= m.refreshSkew {
		return "", false
	}
	if stale != "" && access == stale {
		return "", false
	}
	return access, true
}

// refreshLocked runs with cs.refreshMu and the DB lease held.
func (m *Manager) refreshLocked(ctx context.Context, cs *connState, row *database.N0Connection) (string, error) {
	rt := m.decrypt(row.RefreshToken)
	if rt == "" {
		m.markNeedsReauth(row.ID)
		return "", ErrNeedsReauth
	}
	tok, err := oauth.Refresh(ctx, m.HTTP, row.TokenEndpoint, row.ClientID, rt, row.Resource)
	if err != nil {
		if oauth.IsReauthError(err) {
			m.markNeedsReauth(row.ID)
			return "", ErrNeedsReauth
		}
		_ = m.repo.ReleaseN0RefreshLease(row.ID, m.owner)
		return "", fmt.Errorf("n0: token refresh failed: %w", err)
	}
	pair := tokenPair{access: tok.AccessToken, refresh: tok.RefreshToken, scopes: tok.Scope, expires: tok.ExpiresAt(m.now())}
	if pair.refresh == "" {
		pair.refresh = rt // server did not rotate
	}
	// Persist the rotated pair BEFORE using it: the old refresh token is now
	// spent and replaying it would revoke the connection.
	var perr error
	for attempt := 0; attempt < 3; attempt++ {
		if perr = m.persistTokens(row.ID, pair); perr == nil {
			return pair.access, nil
		}
		_ = sleepCtx(context.Background(), time.Duration(attempt+1)*100*time.Millisecond)
	}
	cs.unsaved = &pair
	return "", fmt.Errorf("n0: could not save the rotated token: %w", perr)
}

func (m *Manager) persistTokens(id int64, p tokenPair) error {
	encA, err := m.encrypt(p.access)
	if err != nil {
		return err
	}
	encR, err := m.encrypt(p.refresh)
	if err != nil {
		return err
	}
	return m.repo.UpdateN0Tokens(id, encA, encR, p.expires, p.scopes, m.owner)
}

func (m *Manager) markNeedsReauth(id int64) {
	_ = m.repo.SetN0ConnectionStatus(id, database.N0StatusNeedsReauth, ReauthMessage, true)
	m.forget(id)
}

// --- MCP calls ---

func (m *Manager) transport(id int64, host string) *Transport {
	cs := m.state(id)
	cs.transportMu.Lock()
	defer cs.transportMu.Unlock()
	endpoint := MCPURL(host)
	if cs.transport == nil || cs.transport.Endpoint != endpoint {
		cs.transport = NewTransport(endpoint, m.HTTP)
	}
	return cs.transport
}

// call performs one MCP request on a connection with a fresh token: a
// proactive refresh when the token is about to expire, and one reactive
// refresh + retry on a 401. A 401 right after a refresh means the
// connection is gone (revoked in n0) → needs_reauth.
func (m *Manager) call(ctx context.Context, row *database.N0Connection, method string, params interface{}) (json.RawMessage, error) {
	if row.Status != database.N0StatusActive {
		return nil, ErrNeedsReauth
	}
	t := m.transport(row.ID, row.Host)
	token, err := m.accessToken(ctx, row.ID, "")
	if err != nil {
		return nil, err
	}
	res, err := t.Call(ctx, token, method, params)
	var ue *UnauthorizedError
	if errors.As(err, &ue) {
		token2, rerr := m.accessToken(ctx, row.ID, token)
		if rerr != nil {
			return nil, rerr
		}
		res, err = t.Call(ctx, token2, method, params)
		if errors.As(err, &ue) {
			m.markNeedsReauth(row.ID)
			return nil, ErrNeedsReauth
		}
	}
	if err == nil {
		m.touch(row.ID)
	}
	return res, err
}

func (m *Manager) touch(id int64) {
	cs := m.state(id)
	cs.touchMu.Lock()
	defer cs.touchMu.Unlock()
	if m.now().Sub(cs.lastTouch) < 30*time.Second {
		return
	}
	cs.lastTouch = m.now()
	_ = m.repo.TouchN0Connection(id)
}

// ResolveSession returns the n0 connection a session uses. When n0 is
// enabled but the session has no binding yet, the most recently used active
// connection is bound.
func (m *Manager) ResolveSession(sessionID string) (*database.N0Connection, error) {
	enabled, err := m.repo.SessionHasConnectorEnabled(sessionID, ConnectorSlug)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrNotEnabled
	}
	if id, err := m.repo.GetN0SessionBinding(sessionID); err == nil && id != 0 {
		row, err := m.repo.GetN0Connection(id)
		if err == nil {
			return row, nil
		}
		if !errors.Is(err, database.ErrN0ConnectionNotFound) {
			return nil, err
		}
		_ = m.repo.UnbindN0Session(sessionID)
	}
	row, err := m.DefaultConnection()
	if err != nil {
		return nil, err
	}
	_ = m.repo.BindN0Session(sessionID, row.ID)
	return row, nil
}

// DefaultConnection is the most recently used active connection (falling
// back to any connection so the user gets a "reconnect" message).
func (m *Manager) DefaultConnection() (*database.N0Connection, error) {
	conns, err := m.repo.ListN0Connections()
	if err != nil {
		return nil, err
	}
	for _, c := range conns {
		if c.Status == database.N0StatusActive {
			return c, nil
		}
	}
	if len(conns) > 0 {
		return conns[0], nil
	}
	return nil, ErrNoConnection
}

// HasConnections reports whether any OAuth connection exists.
func (m *Manager) HasConnections() bool {
	n, err := m.repo.CountN0Connections()
	return err == nil && n > 0
}

// ListTools returns n0's tools (raw MCP tool objects, names n0_*) for a
// session. The list is cached briefly per connection.
func (m *Manager) ListTools(ctx context.Context, sessionID string) ([]json.RawMessage, *database.N0Connection, error) {
	row, err := m.ResolveSession(sessionID)
	if err != nil {
		return nil, nil, err
	}
	tools, err := m.connectionTools(ctx, row)
	return tools, row, err
}

func (m *Manager) connectionTools(ctx context.Context, row *database.N0Connection) ([]json.RawMessage, error) {
	cs := m.state(row.ID)
	cs.toolsMu.Lock()
	if cs.tools != nil && m.now().Sub(cs.toolsAt) < m.toolsTTL {
		tools := cs.tools
		cs.toolsMu.Unlock()
		return tools, nil
	}
	cs.toolsMu.Unlock()

	var all []json.RawMessage
	var cursor string
	for page := 0; page < 20; page++ {
		var params interface{}
		if cursor != "" {
			params = map[string]string{"cursor": cursor}
		}
		raw, err := m.call(ctx, row, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var res struct {
			Tools      []json.RawMessage `json:"tools"`
			NextCursor string            `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("invalid n0 tools/list result: %w", err)
		}
		for _, t := range res.Tools {
			var head struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(t, &head) == nil && IsProxiedTool(head.Name) {
				all = append(all, t)
			}
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if all == nil {
		all = []json.RawMessage{}
	}
	cs.toolsMu.Lock()
	cs.tools, cs.toolsAt = all, m.now()
	cs.toolsMu.Unlock()
	return all, nil
}

// CallTool invokes an n0 tool for a session and returns the raw MCP
// CallToolResult. Tool errors come back as result.isError, not as err.
func (m *Manager) CallTool(ctx context.Context, sessionID, name string, args json.RawMessage) (json.RawMessage, error) {
	if !IsProxiedTool(name) {
		return nil, fmt.Errorf("not an n0 tool: %s", name)
	}
	row, err := m.ResolveSession(sessionID)
	if err != nil {
		return nil, err
	}
	params := map[string]interface{}{"name": name}
	if len(args) > 0 && string(args) != "null" {
		params["arguments"] = args
	} else {
		params["arguments"] = map[string]interface{}{}
	}
	res, err := m.call(ctx, row, "tools/call", params)
	if err != nil {
		return nil, err
	}
	switch name {
	case "n0_connection_info":
		m.storeConnectionInfo(row.ID, res)
	case "n0_repo_create":
		// A new-app connection just got its app: refresh what we show in the UI.
		go m.RefreshConnectionInfo(context.Background(), row.ID)
		m.forget(row.ID) // n0_repo_create disappears from the tool list
	}
	return res, nil
}

// RefreshConnectionInfo calls n0_connection_info and stores workspace/app.
func (m *Manager) RefreshConnectionInfo(ctx context.Context, id int64) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	row, err := m.repo.GetN0Connection(id)
	if err != nil {
		return
	}
	res, err := m.call(ctx, row, "tools/call", map[string]interface{}{"name": "n0_connection_info", "arguments": map[string]interface{}{}})
	if err != nil {
		return
	}
	m.storeConnectionInfo(id, res)
}

func (m *Manager) storeConnectionInfo(id int64, result json.RawMessage) {
	info := ParseConnectionInfo(result)
	if info.Workspace == "" && info.AppSubdomain == "" && info.AppName == "" {
		return
	}
	_ = m.repo.UpdateN0ConnectionInfo(id, info.Workspace, info.AppSubdomain, info.AppName)
}

// --- disconnect ---

// Disconnect revokes the connection's tokens at n0 (best effort) and
// deletes it locally. It reports whether n0 confirmed the revocation.
func (m *Manager) Disconnect(ctx context.Context, id int64) (bool, error) {
	cs := m.state(id)
	cs.refreshMu.Lock()
	defer cs.refreshMu.Unlock()
	row, err := m.repo.GetN0Connection(id)
	if err != nil {
		return false, err
	}
	revoked := false
	if row.RevocationEndpoint != "" {
		rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		// Revoking the refresh token ends the whole grant at n0.
		if rt := m.decrypt(row.RefreshToken); rt != "" {
			if oauth.Revoke(rctx, m.HTTP, row.RevocationEndpoint, row.ClientID, rt, "refresh_token") == nil {
				revoked = true
			}
		}
		if at := m.decrypt(row.AccessToken); at != "" {
			if oauth.Revoke(rctx, m.HTTP, row.RevocationEndpoint, row.ClientID, at, "access_token") == nil {
				revoked = true
			}
		}
		cancel()
	}
	if err := m.repo.DeleteN0Connection(id); err != nil {
		return revoked, err
	}
	_ = m.repo.RemoveN0ConnectorMarkerIfUnused()
	m.forget(id)
	m.mu.Lock()
	delete(m.conns, id)
	m.mu.Unlock()
	m.notifyChange()
	return revoked, nil
}

// --- connection info parsing ---

// ConnectionInfo is what Eggify shows about a connection.
type ConnectionInfo struct {
	Workspace    string
	AppSubdomain string
	AppName      string
}

// ParseConnectionInfo extracts workspace/app from an n0_connection_info
// result. It accepts structuredContent or JSON text content, and falls back
// to "Key: value" lines.
func ParseConnectionInfo(result json.RawMessage) ConnectionInfo {
	var res struct {
		StructuredContent map[string]interface{} `json:"structuredContent"`
		Content           []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	var info ConnectionInfo
	if json.Unmarshal(result, &res) != nil || res.IsError {
		return info
	}
	docs := []map[string]interface{}{}
	if res.StructuredContent != nil {
		docs = append(docs, res.StructuredContent)
	}
	var texts []string
	for _, c := range res.Content {
		if c.Type != "text" {
			continue
		}
		var doc map[string]interface{}
		if json.Unmarshal([]byte(c.Text), &doc) == nil {
			docs = append(docs, doc)
		} else {
			texts = append(texts, c.Text)
		}
	}
	for _, d := range docs {
		mergeInfo(&info, infoFromMap(d))
	}
	for _, t := range texts {
		mergeInfo(&info, infoFromText(t))
	}
	return info
}

func mergeInfo(dst *ConnectionInfo, src ConnectionInfo) {
	if dst.Workspace == "" {
		dst.Workspace = src.Workspace
	}
	if dst.AppSubdomain == "" {
		dst.AppSubdomain = src.AppSubdomain
	}
	if dst.AppName == "" {
		dst.AppName = src.AppName
	}
}

func str(v interface{}) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func infoFromMap(d map[string]interface{}) ConnectionInfo {
	var info ConnectionInfo
	if c, ok := d["connection"].(map[string]interface{}); ok {
		mergeInfo(&info, infoFromMap(c))
	}
	switch w := d["workspace"].(type) {
	case string:
		info.Workspace = strings.TrimSpace(w)
	case map[string]interface{}:
		info.Workspace = firstNonEmpty(str(w["name"]), str(w["slug"]), str(w["id"]))
	}
	if info.Workspace == "" {
		info.Workspace = firstNonEmpty(str(d["workspace_name"]), str(d["workspaceName"]), str(d["workspace_slug"]))
	}
	switch a := d["app"].(type) {
	case string:
		info.AppSubdomain = strings.TrimSpace(a)
	case map[string]interface{}:
		info.AppSubdomain = firstNonEmpty(str(a["subdomain"]), str(a["slug"]))
		info.AppName = firstNonEmpty(str(a["name"]), str(a["display_name"]))
	}
	if info.AppSubdomain == "" {
		info.AppSubdomain = firstNonEmpty(str(d["app_subdomain"]), str(d["appSubdomain"]), str(d["subdomain"]))
	}
	if info.AppName == "" {
		info.AppName = firstNonEmpty(str(d["app_name"]), str(d["appName"]))
	}
	return info
}

func infoFromText(t string) ConnectionInfo {
	var info ConnectionInfo
	for _, line := range strings.Split(t, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*• "))
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.Trim(strings.TrimSpace(k), "*_`"))
		v = strings.Trim(strings.TrimSpace(v), "*_`\"")
		switch k {
		case "workspace", "workspace name":
			info.Workspace = firstNonEmpty(info.Workspace, v)
		case "app subdomain", "subdomain":
			info.AppSubdomain = firstNonEmpty(info.AppSubdomain, v)
		case "app", "app name":
			info.AppName = firstNonEmpty(info.AppName, v)
		}
	}
	return info
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

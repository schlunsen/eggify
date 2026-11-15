// Package n0test provides an in-process fake of n0's MCP + OAuth surface
// (as specified by n0 PR #825) for tests.
package n0test

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Fake is a fake n0 host.
type Fake struct {
	Server *httptest.Server
	URL    string

	// AccessTTL is the access token lifetime handed out (default 1h).
	AccessTTL time.Duration
	// ExtraRedirectPrefixes mimics OAUTH_DCR_EXTRA_REDIRECT_PREFIXES.
	ExtraRedirectPrefixes []string
	// RejectAllAccessTokens makes the MCP endpoint answer 401 invalid_token
	// even for freshly issued tokens (a connection revoked mid-flight).
	RejectAllAccessTokens atomic.Bool
	// RefreshDelay slows refreshes down to widen race windows.
	RefreshDelay time.Duration

	Registrations   atomic.Int64
	RefreshRequests atomic.Int64
	Revocations     atomic.Int64
	ReplayDetected  atomic.Int64
	ToolCalls       atomic.Int64

	mu            sync.Mutex
	clients       map[string][]string // client_id → redirect_uris
	codes         map[string]*authCode
	grants        map[string]*grant // grant id → grant
	access        map[string]*tokenInfo
	refresh       map[string]*tokenInfo
	usedRefresh   map[string]string // spent refresh token → grant id
	mcpSessions   map[string]bool
	issuedSecrets []string // every token ever issued (for leak checks)
}

type authCode struct {
	clientID, redirectURI, challenge, resource, grantID string
	used                                                bool
}

type grant struct {
	id       string
	clientID string
	revoked  bool
	app      string
}

type tokenInfo struct {
	grantID string
	expires time.Time
}

// New starts a fake n0 host on 127.0.0.1.
func New() *Fake {
	f := &Fake{
		AccessTTL:   time.Hour,
		clients:     map[string][]string{},
		codes:       map[string]*authCode{},
		grants:      map[string]*grant{},
		access:      map[string]*tokenInfo{},
		refresh:     map[string]*tokenInfo{},
		usedRefresh: map[string]string{},
		mcpSessions: map[string]bool{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource/api/v1/mcp", f.prm)
	mux.HandleFunc("/.well-known/oauth-authorization-server", f.asMeta)
	mux.HandleFunc("/api/v1/oauth/register", f.register)
	mux.HandleFunc("/api/v1/oauth/authorize", f.authorize)
	mux.HandleFunc("/api/v1/oauth/token", f.token)
	mux.HandleFunc("/api/v1/oauth/revoke", f.revoke)
	mux.HandleFunc("/api/v1/mcp", f.mcp)
	// Test/demo hook: simulate the user disconnecting everything in
	// n0 → Security → Connected tools (and access tokens going stale).
	mux.HandleFunc("/__fake/revoke_all", func(w http.ResponseWriter, r *http.Request) {
		f.RevokeAll()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/workspaces/", func(w http.ResponseWriter, r *http.Request) {
		// Connection tokens never work on REST routes.
		w.WriteHeader(http.StatusUnauthorized)
	})
	f.Server = httptest.NewServer(mux)
	f.URL = f.Server.URL
	return f
}

// Close stops the server.
func (f *Fake) Close() { f.Server.Close() }

// MCPURL is the resource / MCP endpoint.
func (f *Fake) MCPURL() string { return f.URL + "/api/v1/mcp" }

func randToken(prefix string) string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthErr(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func (f *Fake) prm(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{
		"resource":              f.MCPURL(),
		"authorization_servers": []string{f.URL},
		"scopes_supported":      []string{"app:read", "app:code", "app:deploy"},
	})
}

func (f *Fake) asMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{
		"issuer":                                f.URL,
		"authorization_endpoint":                f.URL + "/api/v1/oauth/authorize",
		"token_endpoint":                        f.URL + "/api/v1/oauth/token",
		"registration_endpoint":                 f.URL + "/api/v1/oauth/register",
		"revocation_endpoint":                   f.URL + "/api/v1/oauth/revoke",
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_post"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"response_types_supported":              []string{"code"},
		"scopes_supported":                      []string{"app:read", "app:code", "app:deploy"},
	})
}

func isLoopbackRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return false
	}
	h := u.Hostname()
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func (f *Fake) redirectAllowed(raw string) bool {
	if isLoopbackRedirect(raw) {
		return true
	}
	for _, p := range f.ExtraRedirectPrefixes {
		if strings.HasPrefix(raw, p) {
			return true
		}
	}
	return false
}

// sameLoopbackIgnoringPort compares redirect URIs, allowing the loopback port to differ.
func sameRedirect(registered, got string) bool {
	if registered == got {
		return true
	}
	a, err1 := url.Parse(registered)
	b, err2 := url.Parse(got)
	if err1 != nil || err2 != nil {
		return false
	}
	return isLoopbackRedirect(registered) && isLoopbackRedirect(got) &&
		a.Hostname() == b.Hostname() && a.Path == b.Path
}

func (f *Fake) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var req struct {
		ClientName              string   `json:"client_name"`
		RedirectURIs            []string `json:"redirect_uris"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		oauthErr(w, 400, "invalid_client_metadata", "bad json")
		return
	}
	if len(req.RedirectURIs) == 0 {
		oauthErr(w, 400, "invalid_redirect_uri", "redirect_uris is required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !f.redirectAllowed(u) {
			oauthErr(w, 400, "invalid_redirect_uri", "redirect_uri "+u+" is not allowed")
			return
		}
	}
	if req.TokenEndpointAuthMethod != "none" {
		oauthErr(w, 400, "invalid_client_metadata", "only public clients")
		return
	}
	f.Registrations.Add(1)
	id := randToken("n0c_")
	f.mu.Lock()
	f.clients[id] = req.RedirectURIs
	f.mu.Unlock()
	writeJSON(w, 201, map[string]interface{}{"client_id": id, "client_name": req.ClientName, "redirect_uris": req.RedirectURIs, "token_endpoint_auth_method": "none"})
}

// authorize requires a real user in n0; tests use Approve instead.
func (f *Fake) authorize(w http.ResponseWriter, r *http.Request) {
	loc, err := f.Approve(r.URL.String(), "demo-app")
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, loc, http.StatusFound)
}

// Approve simulates the user approving the consent screen for app (the
// subdomain of the chosen app, "" = a new app) and returns the redirect
// URL the browser would follow (with code and state).
func (f *Fake) Approve(authorizeURL, app string) (string, error) {
	u, err := url.Parse(authorizeURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	clientID := q.Get("client_id")
	redirect := q.Get("redirect_uri")
	f.mu.Lock()
	defer f.mu.Unlock()
	regs, ok := f.clients[clientID]
	if !ok {
		return "", fmt.Errorf("unknown client_id")
	}
	match := false
	for _, r := range regs {
		if sameRedirect(r, redirect) {
			match = true
		}
	}
	if !match {
		return "", fmt.Errorf("redirect_uri mismatch")
	}
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		return "", fmt.Errorf("PKCE S256 is required")
	}
	if q.Get("resource") != f.MCPURL() {
		return "", fmt.Errorf("resource must be %s", f.MCPURL())
	}
	if q.Get("state") == "" {
		return "", fmt.Errorf("state is required")
	}
	g := &grant{id: randToken("g_"), clientID: clientID, app: app}
	f.grants[g.id] = g
	code := randToken("code_")
	f.codes[code] = &authCode{clientID: clientID, redirectURI: redirect, challenge: q.Get("code_challenge"), resource: q.Get("resource"), grantID: g.id}
	back, _ := url.Parse(redirect)
	bq := back.Query()
	bq.Set("code", code)
	bq.Set("state", q.Get("state"))
	back.RawQuery = bq.Encode()
	return back.String(), nil
}

// Deny returns the redirect for a user declining consent.
func (f *Fake) Deny(authorizeURL string) string {
	u, _ := url.Parse(authorizeURL)
	back, _ := url.Parse(u.Query().Get("redirect_uri"))
	bq := back.Query()
	bq.Set("error", "access_denied")
	bq.Set("state", u.Query().Get("state"))
	back.RawQuery = bq.Encode()
	return back.String()
}

func (f *Fake) issue(grantID string) map[string]interface{} {
	at := randToken("pp_oat_")
	rt := randToken("pp_ort_")
	f.access[at] = &tokenInfo{grantID: grantID, expires: time.Now().Add(f.AccessTTL)}
	f.refresh[rt] = &tokenInfo{grantID: grantID, expires: time.Now().Add(7 * 24 * time.Hour)}
	f.issuedSecrets = append(f.issuedSecrets, at, rt)
	return map[string]interface{}{
		"access_token":  at,
		"token_type":    "Bearer",
		"expires_in":    int(f.AccessTTL.Seconds()),
		"refresh_token": rt,
		"scope":         "app:read app:code app:deploy",
	}
}

func (f *Fake) revokeGrantLocked(grantID string) {
	if g := f.grants[grantID]; g != nil {
		g.revoked = true
	}
	for t, ti := range f.access {
		if ti.grantID == grantID {
			delete(f.access, t)
		}
	}
	for t, ti := range f.refresh {
		if ti.grantID == grantID {
			delete(f.refresh, t)
		}
	}
}

func (f *Fake) token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		oauthErr(w, 400, "invalid_request", "form-encoded POST required")
		return
	}
	_ = r.ParseForm()
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		f.mu.Lock()
		defer f.mu.Unlock()
		c := f.codes[r.PostForm.Get("code")]
		if c == nil || c.used {
			oauthErr(w, 400, "invalid_grant", "unknown or used code")
			return
		}
		c.used = true
		if _, ok := f.clients[r.PostForm.Get("client_id")]; !ok {
			oauthErr(w, 401, "invalid_client", "unknown client")
			return
		}
		if c.clientID != r.PostForm.Get("client_id") || c.redirectURI != r.PostForm.Get("redirect_uri") {
			oauthErr(w, 400, "invalid_grant", "client or redirect_uri mismatch")
			return
		}
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge {
			oauthErr(w, 400, "invalid_grant", "PKCE verification failed")
			return
		}
		if r.PostForm.Get("resource") != "" && r.PostForm.Get("resource") != c.resource {
			oauthErr(w, 400, "invalid_target", "resource mismatch")
			return
		}
		writeJSON(w, 200, f.issue(c.grantID))
	case "refresh_token":
		f.RefreshRequests.Add(1)
		if f.RefreshDelay > 0 {
			time.Sleep(f.RefreshDelay)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		rt := r.PostForm.Get("refresh_token")
		if gid, spent := f.usedRefresh[rt]; spent {
			// Replay of a rotated refresh token: treat as theft.
			f.ReplayDetected.Add(1)
			f.revokeGrantLocked(gid)
			oauthErr(w, 400, "invalid_grant", "refresh token reuse detected; connection revoked")
			return
		}
		ti := f.refresh[rt]
		if ti == nil || time.Now().After(ti.expires) || f.grants[ti.grantID] == nil || f.grants[ti.grantID].revoked {
			oauthErr(w, 400, "invalid_grant", "refresh token is invalid, expired or revoked")
			return
		}
		if f.grants[ti.grantID].clientID != r.PostForm.Get("client_id") {
			oauthErr(w, 401, "invalid_client", "client mismatch")
			return
		}
		delete(f.refresh, rt)
		f.usedRefresh[rt] = ti.grantID
		writeJSON(w, 200, f.issue(ti.grantID))
	default:
		oauthErr(w, 400, "unsupported_grant_type", "")
	}
}

func (f *Fake) revoke(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	tok := r.PostForm.Get("token")
	f.mu.Lock()
	defer f.mu.Unlock()
	if ti := f.refresh[tok]; ti != nil {
		f.revokeGrantLocked(ti.grantID)
	} else if ti := f.access[tok]; ti != nil {
		delete(f.access, tok)
	}
	f.Revocations.Add(1)
	w.WriteHeader(200)
}

// RevokeAll simulates the user disconnecting every connection in
// n0 → Settings → Security → Connected tools.
func (f *Fake) RevokeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id := range f.grants {
		f.revokeGrantLocked(id)
	}
}

// ExpireAccessTokens invalidates all current access tokens (refresh tokens stay valid).
func (f *Fake) ExpireAccessTokens() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for t := range f.access {
		delete(f.access, t)
	}
}

// ActiveGrants counts grants that are not revoked.
func (f *Fake) ActiveGrants() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, g := range f.grants {
		if !g.revoked {
			n++
		}
	}
	return n
}

// Secrets returns every token the fake ever issued.
func (f *Fake) Secrets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.issuedSecrets...)
}

func (f *Fake) unauthorized(w http.ResponseWriter, sentToken bool) {
	v := fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/api/v1/mcp"`, f.URL)
	if sentToken {
		v += `, error="invalid_token"`
	}
	w.Header().Set("WWW-Authenticate", v)
	writeJSON(w, 401, map[string]string{"error": "unauthorized"})
}

var protocolVersions = map[string]bool{"2025-11-25": true, "2025-06-18": true, "2025-03-26": true}

// Tools lists the fake's tools (like a new-app connection).
var Tools = []map[string]interface{}{
	{"name": "n0_connection_info", "description": "Which workspace/app this connection is bound to.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	{"name": "n0_app_guide", "description": "How to build an n0 app.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	{"name": "n0_repo_write_files", "description": "Write many files in one commit.", "inputSchema": map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"message": map[string]interface{}{"type": "string"},
			"files": map[string]interface{}{"type": "array", "items": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}, "content": map[string]interface{}{"type": "string"}},
				"required":   []string{"path", "content"},
			}},
		},
		"required": []string{"message", "files"},
	}},
	{"name": "n0_app_status", "description": "Deployment status.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	// A workspace connector tool (listed by n0 when the connection was granted
	// workspace:connectors).
	{"name": "runpod__list_pods", "description": "[RunPod] List pods.", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
}

func textResult(text string, isError bool) map[string]interface{} {
	return map[string]interface{}{"content": []map[string]string{{"type": "text", "text": text}}, "isError": isError}
}

func (f *Fake) mcp(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		f.unauthorized(w, false)
		return
	}
	tok := strings.TrimPrefix(auth, "Bearer ")
	f.mu.Lock()
	ti := f.access[tok]
	var g *grant
	if ti != nil {
		g = f.grants[ti.grantID]
	}
	f.mu.Unlock()
	if ti == nil || time.Now().After(ti.expires) || g == nil || g.revoked || f.RejectAllAccessTokens.Load() {
		f.unauthorized(w, true)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"jsonrpc": "2.0", "error": map[string]interface{}{"code": -32700, "message": "parse error"}})
		return
	}
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted) // notification
		return
	}
	reply := func(result interface{}) {
		writeJSON(w, 200, map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := "2025-06-18"
		if protocolVersions[p.ProtocolVersion] {
			v = p.ProtocolVersion
		}
		sid := randToken("mcps_")
		f.mu.Lock()
		f.mcpSessions[sid] = true
		f.mu.Unlock()
		w.Header().Set("Mcp-Session-Id", sid)
		reply(map[string]interface{}{"protocolVersion": v, "capabilities": map[string]interface{}{"tools": map[string]interface{}{}}, "serverInfo": map[string]string{"name": "n0", "version": "test"}})
	case "ping":
		reply(map[string]interface{}{})
	case "tools/list":
		reply(map[string]interface{}{"tools": Tools})
	case "tools/call":
		f.ToolCalls.Add(1)
		var p struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		switch p.Name {
		case "n0_connection_info":
			// Same shape as n0 (mcp_app_tools.py): JSON text, no app name,
			// app is null for a "new app" connection.
			var app interface{}
			if g.app != "" {
				app = map[string]string{"id": "app_1", "subdomain": g.app, "url": "https://" + g.app + ".example.test", "status": "running"}
			}
			info, _ := json.MarshalIndent(map[string]interface{}{
				"workspace":          map[string]string{"id": "ws_1", "name": "Acme"},
				"app":                app,
				"may_create_new_app": g.app == "",
				"repository":         nil,
				"scopes":             []string{"app:read", "app:code", "app:deploy"},
				"manifest":           "n0-app.json",
				"next_step":          "n0_app_status",
			}, "", "  ")
			reply(textResult(string(info), false))
		case "n0_app_guide":
			reply(textResult("Create n0-app.json, a Dockerfile and a CI workflow.", false))
		case "n0_repo_write_files":
			files, _ := p.Arguments["files"].([]interface{})
			reply(textResult(fmt.Sprintf("committed %d files: %v", len(files), p.Arguments["message"]), false))
		case "n0_app_status":
			reply(textResult("app "+g.app+" is running", false))
		case "runpod__list_pods":
			reply(textResult(`[{"id": "pod1"}]`, false))
		default:
			reply(textResult("unknown tool "+p.Name, true))
		}
	default:
		writeJSON(w, 200, map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "error": map[string]interface{}{"code": -32601, "message": "method not found"}})
	}
}

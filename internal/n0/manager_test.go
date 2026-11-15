package n0

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schlunsen/eggify/internal/connectors"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/n0/n0test"
)

type env struct {
	t    *testing.T
	db   *database.Database
	m    *Manager
	ls   *LoopbackServer
	fake *n0test.Fake
}

func newEnv(t *testing.T) *env {
	t.Helper()
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	m := NewManager(db, key)
	ls, err := StartLoopback(m, 0)
	if err != nil {
		t.Fatalf("loopback: %v", err)
	}
	fake := n0test.New()
	t.Cleanup(func() {
		ls.Close()
		fake.Close()
		database.ResetInstance()
	})
	return &env{t: t, db: db, m: m, ls: ls, fake: fake}
}

// newSession creates an agent session row with n0 enabled.
func (e *env) newSession(id string, enableN0 bool) {
	e.t.Helper()
	if _, err := e.db.GetDB().Exec(`INSERT INTO agent_sessions (id, status) VALUES (?, 'idle')`, id); err != nil {
		e.t.Fatalf("insert session: %v", err)
	}
	if enableN0 {
		if err := database.NewConnectorRepository(e.db).EnableSessionConnector(id, ConnectorSlug); err != nil {
			e.t.Fatalf("enable n0: %v", err)
		}
	}
}

// connect runs the whole browser flow against the fake and returns the connection id.
func (e *env) connect(app string, reconnectID int64) int64 {
	e.t.Helper()
	res, err := e.m.StartAuthorization(context.Background(), StartRequest{
		Host: e.fake.URL, RedirectURI: e.ls.CallbackURL(), RedirectKey: RedirectKeyLoopback,
		ReconnectID: reconnectID, ReturnTo: "https://localhost:3333/connectors",
	})
	if err != nil {
		e.t.Fatalf("start: %v", err)
	}
	if st := e.m.FlowStatus(res.State); st.Status != "pending" {
		e.t.Fatalf("flow status = %q, want pending", st.Status)
	}
	loc, err := e.fake.Approve(res.AuthorizeURL, app)
	if err != nil {
		e.t.Fatalf("approve: %v", err)
	}
	resp, err := http.Get(loc)
	if err != nil {
		e.t.Fatalf("callback: %v", err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	st := e.m.FlowStatus(res.State)
	if resp.StatusCode != 200 || st.Status != "connected" {
		e.t.Fatalf("callback status %d, flow %+v, page %s", resp.StatusCode, st, page)
	}
	e.assertNoSecrets("callback page", string(page))
	return st.ConnectionID
}

func (e *env) proxy(sessionID, token string, body string, hdr map[string]string) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.ls.ProxyURL(), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderSessionID, sessionID)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("proxy: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	e.assertNoSecrets("proxy response", string(b))
	return resp.StatusCode, string(b)
}

func (e *env) assertNoSecrets(where, s string) {
	e.t.Helper()
	for _, sec := range e.fake.Secrets() {
		if strings.Contains(s, sec) {
			e.t.Fatalf("%s leaks an n0 token", where)
		}
	}
	if strings.Contains(s, "pp_oat_") || strings.Contains(s, "pp_ort_") {
		e.t.Fatalf("%s contains an n0 token prefix", where)
	}
}

func toolResultText(t *testing.T, body string) (string, bool) {
	t.Helper()
	var rr struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct{ Message string } `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &rr); err != nil {
		t.Fatalf("bad JSON-RPC: %v: %s", err, body)
	}
	if rr.Error != nil {
		t.Fatalf("JSON-RPC error: %s", rr.Error.Message)
	}
	if len(rr.Result.Content) == 0 {
		return "", rr.Result.IsError
	}
	return rr.Result.Content[0].Text, rr.Result.IsError
}

func TestConnectListAndCallThroughProxy(t *testing.T) {
	e := newEnv(t)
	sid := "11111111-1111-1111-1111-111111111111"
	e.newSession(sid, true)
	id := e.connect("demo-app", 0)

	row, err := e.m.Repo().GetN0Connection(id)
	if err != nil {
		t.Fatal(err)
	}
	// Tokens are stored encrypted, never in plaintext.
	for _, sec := range e.fake.Secrets() {
		if row.AccessToken == sec || row.RefreshToken == sec || strings.Contains(row.AccessToken+row.RefreshToken, sec) {
			t.Fatal("token stored in plaintext")
		}
	}
	if !strings.HasPrefix(row.AccessToken, connectors.EncryptionPrefix) || !strings.HasPrefix(row.RefreshToken, connectors.EncryptionPrefix) {
		t.Fatal("tokens not encrypted with the connector key")
	}
	// n0_connection_info was called right after connecting.
	if row.WorkspaceName != "Acme" || row.AppSubdomain != "demo-app" {
		t.Fatalf("connection info not stored: %+v", row)
	}
	if row.Scopes != "app:read app:code app:deploy" || row.Status != database.N0StatusActive {
		t.Fatalf("unexpected row: scopes=%q status=%q", row.Scopes, row.Status)
	}
	// The keyless marker makes n0 look connected to the connector plumbing.
	uc, _ := database.NewConnectorRepository(e.db).GetConnectionBySlug("n0")
	if uc == nil || (uc.APIKey != nil && *uc.APIKey != "") {
		t.Fatalf("expected keyless n0 marker row, got %+v", uc)
	}

	tok := ProxyToken(sid)
	code, body := e.proxy(sid, tok, `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`, nil)
	if code != 200 {
		t.Fatalf("tools/list: %d %s", code, body)
	}
	var list struct {
		ID     int `json:"id"`
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	_ = json.Unmarshal([]byte(body), &list)
	if list.ID != 7 || len(list.Result.Tools) != len(n0test.Tools) {
		t.Fatalf("tools/list result: %s", body)
	}
	for _, tl := range list.Result.Tools {
		if tl.Name == "n0_repo_write_files" && !strings.Contains(string(tl.InputSchema), `"items"`) {
			t.Fatalf("nested schema lost: %s", tl.InputSchema)
		}
	}

	// n0_repo_write_files writes externally: the user approves it.
	e.m.SetToolGuard(func(ctx context.Context, sessionID, tool string, args json.RawMessage) (bool, string) {
		return true, ""
	})
	code, body = e.proxy(sid, tok, `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"n0_repo_write_files","arguments":{"message":"init","files":[{"path":"a","content":"x"},{"path":"b","content":"y"}]}}}`, nil)
	text, isErr := toolResultText(t, body)
	if code != 200 || isErr || text != "committed 2 files: init" {
		t.Fatalf("tools/call: %d %q %v", code, text, isErr)
	}
	if b, _ := e.m.Repo().GetN0SessionBinding(sid); b != id {
		t.Fatalf("session not bound to connection: %d", b)
	}
}

func TestProxyRejectsForeignCallersAndSessions(t *testing.T) {
	e := newEnv(t)
	a := "22222222-2222-2222-2222-222222222222"
	b := "33333333-3333-3333-3333-333333333333"
	e.newSession(a, true)
	e.newSession(b, false)
	e.connect("demo-app", 0)
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	if code, _ := e.proxy(a, "", list, nil); code != 401 {
		t.Fatalf("missing capability: %d", code)
	}
	if code, _ := e.proxy(a, ProxyToken(b), list, nil); code != 401 {
		t.Fatalf("another session's capability accepted: %d", code)
	}
	if code, _ := e.proxy(b, ProxyToken(a), list, nil); code != 401 {
		t.Fatalf("session id swap accepted: %d", code)
	}
	if code, _ := e.proxy(a, ProxyToken(a), list, map[string]string{"X-Forwarded-For": "203.0.113.9"}); code != 403 {
		t.Fatalf("forwarded request accepted: %d", code)
	}
	// Non-loopback peer.
	req := httptest.NewRequest(http.MethodPost, ProxyPath, strings.NewReader(list))
	req.RemoteAddr = "192.0.2.10:5555"
	req.Header.Set(HeaderSessionID, a)
	req.Header.Set("Authorization", "Bearer "+ProxyToken(a))
	rec := httptest.NewRecorder()
	e.m.ServeProxy(rec, req)
	if rec.Code != 403 {
		t.Fatalf("non-loopback caller: %d", rec.Code)
	}
	// Only tools/list and tools/call are proxied.
	_, body := e.proxy(a, ProxyToken(a), `{"jsonrpc":"2.0","id":2,"method":"initialize"}`, nil)
	if !strings.Contains(body, "method not allowed") {
		t.Fatalf("initialize should not be proxied: %s", body)
	}
	// Session b has n0 disabled: no tools, and calls explain why.
	_, body = e.proxy(b, ProxyToken(b), list, nil)
	if !strings.Contains(body, `"tools":[]`) {
		t.Fatalf("disabled session got tools: %s", body)
	}
	_, body = e.proxy(b, ProxyToken(b), `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"n0_app_status"}}`, nil)
	if text, isErr := toolResultText(t, body); !isErr || !strings.Contains(text, "not enabled") {
		t.Fatalf("disabled session call: %q", text)
	}
	// Only n0_* tools are forwarded.
	_, body = e.proxy(a, ProxyToken(a), `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"Bash","arguments":{"command":"id"}}}`, nil)
	if _, isErr := toolResultText(t, body); !isErr {
		t.Fatalf("non-n0 tool forwarded: %s", body)
	}
}

func TestSessionUsesOnlyItsBoundConnection(t *testing.T) {
	e := newEnv(t)
	s1 := "44444444-4444-4444-4444-444444444444"
	s2 := "55555555-5555-5555-5555-555555555555"
	e.newSession(s1, true)
	e.newSession(s2, true)
	c1 := e.connect("app-one", 0)
	c2 := e.connect("app-two", 0)
	if err := e.m.Repo().BindN0Session(s1, c1); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Repo().BindN0Session(s2, c2); err != nil {
		t.Fatal(err)
	}
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"n0_app_status"}}`
	_, b1 := e.proxy(s1, ProxyToken(s1), call, nil)
	_, b2 := e.proxy(s2, ProxyToken(s2), call, nil)
	if t1, _ := toolResultText(t, b1); t1 != "app app-one is running" {
		t.Fatalf("s1 used wrong app: %q", t1)
	}
	if t2, _ := toolResultText(t, b2); t2 != "app app-two is running" {
		t.Fatalf("s2 used wrong app: %q", t2)
	}
}

func TestConcurrentCallsRefreshExactlyOnce(t *testing.T) {
	e := newEnv(t)
	sid := "66666666-6666-6666-6666-666666666666"
	e.newSession(sid, true)
	// Connect with a token that is already inside the 2-minute refresh window.
	e.fake.AccessTTL = 60 * time.Second
	id := e.connect("demo-app", 0)
	e.fake.AccessTTL = time.Hour
	e.fake.RefreshDelay = 50 * time.Millisecond
	before := e.fake.RefreshRequests.Load()

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := e.m.CallTool(context.Background(), sid, "n0_app_status", nil)
			if err != nil {
				errs <- err
				return
			}
			if strings.Contains(string(res), `"isError":true`) {
				errs <- errors.New(string(res))
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("call failed: %v", err)
	}
	if got := e.fake.RefreshRequests.Load() - before; got != 1 {
		t.Fatalf("refresh requests = %d, want exactly 1", got)
	}
	if e.fake.ReplayDetected.Load() != 0 || e.fake.ActiveGrants() != 1 {
		t.Fatalf("connection was revoked (replays=%d, grants=%d)", e.fake.ReplayDetected.Load(), e.fake.ActiveGrants())
	}
	row, _ := e.m.Repo().GetN0Connection(id)
	if row.Status != database.N0StatusActive || time.Until(row.AccessExpiresAt) < 30*time.Minute {
		t.Fatalf("rotated token not persisted: status=%s expires=%v", row.Status, row.AccessExpiresAt)
	}
}

func TestConcurrentRefreshAcrossManagersSharingTheDB(t *testing.T) {
	e := newEnv(t)
	sid := "67676767-6767-6767-6767-676767676767"
	e.newSession(sid, true)
	e.fake.AccessTTL = 60 * time.Second
	e.connect("demo-app", 0)
	e.fake.AccessTTL = time.Hour
	e.fake.RefreshDelay = 100 * time.Millisecond
	before := e.fake.RefreshRequests.Load()

	// A second manager (think: a second Eggify process) on the same database.
	other := NewManager(e.db, e.m.key)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		for _, m := range []*Manager{e.m, other} {
			wg.Add(1)
			go func(m *Manager) {
				defer wg.Done()
				if _, err := m.CallTool(context.Background(), sid, "n0_app_status", nil); err != nil {
					t.Errorf("call: %v", err)
				}
			}(m)
		}
	}
	wg.Wait()
	if got := e.fake.RefreshRequests.Load() - before; got != 1 {
		t.Fatalf("refresh requests = %d, want 1", got)
	}
	if e.fake.ReplayDetected.Load() != 0 {
		t.Fatal("refresh token was replayed")
	}
}

func TestInvalidGrantMarksNeedsReauthOnce(t *testing.T) {
	e := newEnv(t)
	sid := "77777777-7777-7777-7777-777777777777"
	e.newSession(sid, true)
	e.fake.AccessTTL = 60 * time.Second // next call must refresh
	id := e.connect("demo-app", 0)
	e.fake.RevokeAll() // user disconnects Eggify in n0 → Connected tools
	before := e.fake.RefreshRequests.Load()

	_, err := e.m.CallTool(context.Background(), sid, "n0_app_status", nil)
	if !errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("err = %v, want ErrNeedsReauth", err)
	}
	row, _ := e.m.Repo().GetN0Connection(id)
	if row.Status != database.N0StatusNeedsReauth || row.StatusMessage != ReauthMessage {
		t.Fatalf("status = %q / %q", row.Status, row.StatusMessage)
	}
	if row.AccessToken != "" || row.RefreshToken != "" {
		t.Fatal("dead tokens were kept")
	}
	// Further calls fail fast with the same message and never retry.
	for i := 0; i < 5; i++ {
		_, body := e.proxy(sid, ProxyToken(sid), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"n0_app_status"}}`, nil)
		text, isErr := toolResultText(t, body)
		if !isErr || text != ReauthMessage {
			t.Fatalf("call %d: %q", i, text)
		}
	}
	if got := e.fake.RefreshRequests.Load() - before; got != 1 {
		t.Fatalf("refresh requests = %d, want 1 (no retry loop)", got)
	}
	// tools/list still offers n0_connection_info so the agent can learn why.
	_, body := e.proxy(sid, ProxyToken(sid), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)
	if !strings.Contains(body, "n0_connection_info") {
		t.Fatalf("placeholder tool missing: %s", body)
	}

	// Reconnect in place restores the same connection id.
	e.fake.AccessTTL = time.Hour
	if got := e.connect("demo-app", id); got != id {
		t.Fatalf("reconnect created %d, want %d", got, id)
	}
	if _, err := e.m.CallTool(context.Background(), sid, "n0_app_status", nil); err != nil {
		t.Fatalf("after reconnect: %v", err)
	}
}

func TestReactiveRefreshOn401(t *testing.T) {
	e := newEnv(t)
	sid := "88888888-8888-8888-8888-888888888888"
	e.newSession(sid, true)
	e.connect("demo-app", 0)
	e.fake.ExpireAccessTokens() // n0 drops the access token early
	before := e.fake.RefreshRequests.Load()
	res, err := e.m.CallTool(context.Background(), sid, "n0_app_status", nil)
	if err != nil || !strings.Contains(string(res), "is running") {
		t.Fatalf("call after 401: %v %s", err, res)
	}
	if got := e.fake.RefreshRequests.Load() - before; got != 1 {
		t.Fatalf("refresh requests = %d, want 1", got)
	}
}

func TestUnauthorizedAfterFreshRefreshNeedsReauth(t *testing.T) {
	e := newEnv(t)
	sid := "99999999-9999-9999-9999-999999999999"
	e.newSession(sid, true)
	id := e.connect("demo-app", 0)
	e.fake.RejectAllAccessTokens.Store(true)
	before := e.fake.RefreshRequests.Load()
	if _, err := e.m.CallTool(context.Background(), sid, "n0_app_status", nil); !errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.m.CallTool(context.Background(), sid, "n0_app_status", nil); !errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("second err = %v", err)
	}
	if got := e.fake.RefreshRequests.Load() - before; got != 1 {
		t.Fatalf("refresh requests = %d, want 1", got)
	}
	row, _ := e.m.Repo().GetN0Connection(id)
	if row.Status != database.N0StatusNeedsReauth {
		t.Fatalf("status = %s", row.Status)
	}
}

func TestRegistrationReusedAndLoopbackRuleEnforced(t *testing.T) {
	e := newEnv(t)
	e.connect("a", 0)
	e.connect("b", 0)
	if n := e.fake.Registrations.Load(); n != 1 {
		t.Fatalf("registrations = %d, want 1 (one per host)", n)
	}
	// A different loopback port reuses the same registration.
	res, err := e.m.StartAuthorization(context.Background(), StartRequest{
		Host: e.fake.URL, RedirectURI: "http://127.0.0.1:1/api/connectors/n0/oauth/callback", RedirectKey: RedirectKeyLoopback,
	})
	if err != nil || e.fake.Registrations.Load() != 1 {
		t.Fatalf("port change re-registered: %v", err)
	}
	if !strings.Contains(res.AuthorizeURL, "code_challenge_method=S256") || !strings.Contains(res.AuthorizeURL, "resource=") {
		t.Fatalf("authorize URL lacks PKCE/resource: %s", res.AuthorizeURL)
	}

	// A public https callback that n0 does not allowlist.
	_, err = e.m.StartAuthorization(context.Background(), StartRequest{
		Host: e.fake.URL, RedirectURI: "https://eggify.example.com/api/connectors/n0/oauth/callback", RedirectKey: "https://eggify.example.com/api/connectors/n0/oauth/callback",
	})
	var se *StartError
	if !errors.As(err, &se) || se.Code != "invalid_redirect_uri" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(se.Message, "is not allowed") || !strings.Contains(se.Hint, "OAUTH_DCR_EXTRA_REDIRECT_PREFIXES") ||
		!strings.Contains(se.Hint, "https://eggify.example.com/api/connectors/n0/oauth/callback") {
		t.Fatalf("error not surfaced verbatim with hint: %+v", se)
	}

	// Once the admin allowlists the prefix, it works.
	e.fake.ExtraRedirectPrefixes = []string{"https://eggify.example.com/"}
	if _, err := e.m.StartAuthorization(context.Background(), StartRequest{
		Host: e.fake.URL, RedirectURI: "https://eggify.example.com/api/connectors/n0/oauth/callback", RedirectKey: "https://eggify.example.com/api/connectors/n0/oauth/callback",
	}); err != nil {
		t.Fatalf("allowlisted: %v", err)
	}
}

func TestCallbackRejectsReplayedStateAndDeniedConsent(t *testing.T) {
	e := newEnv(t)
	res, err := e.m.StartAuthorization(context.Background(), StartRequest{Host: e.fake.URL, RedirectURI: e.ls.CallbackURL(), RedirectKey: RedirectKeyLoopback})
	if err != nil {
		t.Fatal(err)
	}
	resp, _ := http.Get(e.fake.Deny(res.AuthorizeURL))
	resp.Body.Close()
	if st := e.m.FlowStatus(res.State); st.Status != "error" || st.Error != "access_denied" {
		t.Fatalf("denied: %+v", st)
	}
	// The state is single-use.
	st := e.m.CompleteAuthorization(context.Background(), res.State, "x", "", "")
	if st.Error != "invalid_state" {
		t.Fatalf("replayed state: %+v", st)
	}
	if n, _ := e.m.Repo().CountN0Connections(); n != 0 {
		t.Fatal("connection created without consent")
	}
}

func TestDisconnectRevokesAtN0AndDeletes(t *testing.T) {
	e := newEnv(t)
	sid := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	e.newSession(sid, true)
	id := e.connect("demo-app", 0)
	changed := make(chan struct{}, 1)
	e.m.OnChange = func() { changed <- struct{}{} }
	revoked, err := e.m.Disconnect(context.Background(), id)
	if err != nil || !revoked {
		t.Fatalf("disconnect: %v revoked=%v", err, revoked)
	}
	if e.fake.ActiveGrants() != 0 {
		t.Fatal("grant still active at n0")
	}
	if _, err := e.m.Repo().GetN0Connection(id); !errors.Is(err, database.ErrN0ConnectionNotFound) {
		t.Fatal("connection not deleted")
	}
	if b, _ := e.m.Repo().GetN0SessionBinding(sid); b != 0 {
		t.Fatal("binding not deleted")
	}
	if uc, _ := database.NewConnectorRepository(e.db).GetConnectionBySlug("n0"); uc != nil {
		t.Fatal("marker row left behind")
	}
	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("OnChange not called")
	}
}

func TestTransportToleratesSSEAndSessionExpiry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)
		if len(req.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "s1")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":{"protocolVersion":"2025-06-18"}}`))
		default:
			calls++
			if calls == 1 && r.Header.Get("Mcp-Session-Id") == "s1" {
				w.WriteHeader(404) // session expired server-side
				return
			}
			if r.Header.Get("MCP-Protocol-Version") != "2025-06-18" {
				t.Errorf("protocol version header = %q", r.Header.Get("MCP-Protocol-Version"))
			}
			w.Header().Set("Content-Type", "text/event-stream")
			var buf bytes.Buffer
			buf.WriteString("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
			buf.WriteString("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":" + string(req.ID) + ",\"result\":{\"ok\":true}}\n\n")
			w.Write(buf.Bytes())
		}
	}))
	defer srv.Close()
	tr := NewTransport(srv.URL, srv.Client())
	res, err := tr.Call(context.Background(), "tok", "tools/list", nil)
	if err != nil || string(res) != `{"ok":true}` {
		t.Fatalf("res=%s err=%v", res, err)
	}
}

func TestParseConnectionInfoVariants(t *testing.T) {
	cases := map[string]ConnectionInfo{
		`{"content":[{"type":"text","text":"{\"workspace\":{\"name\":\"W\"},\"app\":{\"subdomain\":\"s\",\"name\":\"N\"}}"}]}`: {"W", "s", "N"},
		`{"structuredContent":{"workspace_name":"W2","app_subdomain":"s2"},"content":[]}`:                                      {"W2", "s2", ""},
		`{"content":[{"type":"text","text":"Workspace: W3\nApp subdomain: s3\nApp name: N3"}]}`:                                {"W3", "s3", "N3"},
		`{"content":[{"type":"text","text":"boom"}],"isError":true}`:                                                           {},
	}
	for in, want := range cases {
		if got := ParseConnectionInfo(json.RawMessage(in)); got != want {
			t.Errorf("ParseConnectionInfo(%s) = %+v, want %+v", in, got, want)
		}
	}
}

func TestNormalizeHost(t *testing.T) {
	ok := map[string]string{
		"":                        DefaultHost,
		"nzero.pro":               "https://nzero.pro",
		"https://n0.example.com/": "https://n0.example.com",
		"http://127.0.0.1:8080":   "http://127.0.0.1:8080",
	}
	for in, want := range ok {
		if got, err := NormalizeHost(in); err != nil || got != want {
			t.Errorf("NormalizeHost(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"http://nzero.pro", "https://user@nzero.pro", "https://nzero.pro/?x=1"} {
		if _, err := NormalizeHost(bad); err == nil {
			t.Errorf("NormalizeHost(%q) accepted", bad)
		}
	}
}

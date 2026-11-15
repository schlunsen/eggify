package remoteaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeNgrokAPI is an httptest stand-in for api.ngrok.com's tunnel sessions.
type fakeNgrokAPI struct {
	t        *testing.T
	mu       sync.Mutex
	key      string
	sessions []TunnelSession
	pageSize int
	stopped  []string
	status   int // if set, every request fails with it
	errBody  string
	requests int
}

func newFakeNgrokAPI(t *testing.T, key string, sessions ...TunnelSession) (*fakeNgrokAPI, *httptest.Server) {
	f := &fakeNgrokAPI{t: t, key: key, sessions: sessions, pageSize: 2}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeNgrokAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if r.Header.Get("Ngrok-Version") != "2" {
		http.Error(w, "missing version", 400)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.key {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"error_code":"ERR_NGROK_201","status_code":401,"msg":"The API key is invalid."}`)
		return
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, f.errBody)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/tunnel_sessions":
		// Newest first, paged by before_id, like ngrok.
		start := 0
		if b := r.URL.Query().Get("before_id"); b != "" {
			for i, s := range f.sessions {
				if s.ID == b {
					start = i + 1
				}
			}
		}
		end := start + f.pageSize
		if end > len(f.sessions) {
			end = len(f.sessions)
		}
		page := map[string]any{"tunnel_sessions": f.sessions[start:end], "uri": "https://api.ngrok.com/tunnel_sessions"}
		if end < len(f.sessions) {
			// Absolute, on ngrok's host: the client must reuse only path+query.
			page["next_page_uri"] = "https://api.ngrok.com/tunnel_sessions?before_id=" + f.sessions[end-1].ID + "&limit=100"
		} else {
			page["next_page_uri"] = nil
		}
		_ = json.NewEncoder(w).Encode(page)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/tunnel_sessions/") && strings.HasSuffix(r.URL.Path, "/stop"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/tunnel_sessions/"), "/stop")
		for i, s := range f.sessions {
			if s.ID == id {
				f.sessions = append(f.sessions[:i], f.sessions[i+1:]...)
				f.stopped = append(f.stopped, id)
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `{"error_code":"ERR_NGROK_404","status_code":404,"msg":"not found"}`)
	default:
		http.NotFound(w, r)
	}
}

func sessionsN(n int) []TunnelSession {
	out := make([]TunnelSession, n)
	for i := range out {
		out[i] = TunnelSession{ID: fmt.Sprintf("ts_%02d", i), AgentVersion: "3.20.0", OS: "linux", Region: "eu",
			IP: "203.0.113.9", StartedAt: time.Date(2026, 10, 1, 12, 0, 60-i, 0, time.UTC).Format(time.RFC3339)}
	}
	return out
}

func TestNgrokAPIListPaginates(t *testing.T) {
	f, srv := newFakeNgrokAPI(t, "key_1234567890", sessionsN(5)...)
	api := &NgrokAPI{BaseURL: srv.URL}
	got, err := api.ListTunnelSessions(context.Background(), "key_1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || got[0].ID != "ts_00" || got[4].ID != "ts_04" || got[0].AgentVersion != "3.20.0" || got[0].IP != "203.0.113.9" {
		t.Fatalf("got %+v", got)
	}
	if f.requests != 3 {
		t.Fatalf("want 3 page requests, got %d", f.requests)
	}
}

func TestNgrokAPIStop(t *testing.T) {
	f, srv := newFakeNgrokAPI(t, "key_1234567890", sessionsN(2)...)
	api := &NgrokAPI{BaseURL: srv.URL}
	if err := api.StopTunnelSession(context.Background(), "key_1234567890", "ts_01"); err != nil {
		t.Fatal(err)
	}
	if len(f.stopped) != 1 || f.stopped[0] != "ts_01" {
		t.Fatalf("stopped %v", f.stopped)
	}
	var ae *APIError
	if err := api.StopTunnelSession(context.Background(), "key_1234567890", "ts_01"); !errors.As(err, &ae) || ae.Code != APIErrNotFound {
		t.Fatalf("second stop: %v", err)
	}
	if err := api.StopTunnelSession(context.Background(), "key_1234567890", "../users"); !errors.As(err, &ae) {
		t.Fatalf("path traversal id accepted: %v", err)
	}
}

func TestNgrokAPIErrors(t *testing.T) {
	f, srv := newFakeNgrokAPI(t, "key_1234567890", sessionsN(1)...)
	api := &NgrokAPI{BaseURL: srv.URL}
	var ae *APIError
	_, err := api.ListTunnelSessions(context.Background(), "wrong_key_123")
	if !errors.As(err, &ae) || ae.Code != APIErrInvalidKey || !strings.Contains(ae.Msg, "api-keys") || strings.Contains(ae.Msg, "wrong_key") {
		t.Fatalf("invalid key: %v", err)
	}
	for status, code := range map[int]string{403: APIErrForbidden, 429: APIErrRateLimited, 500: APIErrNgrok} {
		f.mu.Lock()
		f.status, f.errBody = status, `{"error_code":"ERR_NGROK_X","msg":"nope"}`
		f.mu.Unlock()
		_, err := api.ListTunnelSessions(context.Background(), "key_1234567890")
		if !errors.As(err, &ae) || ae.Code != code || ae.Status != status {
			t.Fatalf("status %d: %v", status, err)
		}
	}
	if _, err := api.ListTunnelSessions(context.Background(), ""); !errors.As(err, &ae) || ae.Code != APIErrInvalidKey {
		t.Fatalf("empty key: %v", err)
	}
}

func TestNgrokAPITimeoutAndRedirects(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := (&NgrokAPI{BaseURL: slow.URL}).ListTunnelSessions(ctx, "key_1234567890"); err == nil || !strings.Contains(err.Error(), "in time") {
		t.Fatalf("timeout: %v", err)
	}

	var leaked bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked = true
		}
	}))
	defer evil.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/tunnel_sessions", http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	_, err := (&NgrokAPI{BaseURL: redir.URL}).ListTunnelSessions(context.Background(), "key_1234567890")
	if err == nil || leaked {
		t.Fatalf("redirect followed with the key (err=%v leaked=%v)", err, leaked)
	}
}

func TestAPIKeyStoredEncrypted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote_access.json")
	key := make([]byte, 32)
	key[0] = 7
	store, _ := NewStore(path, key)
	m := NewManager(Config{Store: store})
	if err := m.SetAPIKey("  short "); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("short key accepted: %v", err)
	}
	if err := m.SetAPIKey("  2abcSECRETapikey_9f3e  "); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "SECRETapikey") {
		t.Fatal("API key stored in plaintext")
	}
	v := m.View()
	if !v.APIKeySet || v.APIKeyHint != "…9f3e" {
		t.Fatalf("view %+v", v)
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "SECRET") {
		t.Fatalf("view leaks the key: %s", b)
	}
	// Round trip through a new manager (same key).
	store2, _ := NewStore(path, key)
	m2 := NewManager(Config{Store: store2})
	if m2.Settings().APIKey != "2abcSECRETapikey_9f3e" {
		t.Fatalf("reloaded key = %q", m2.Settings().APIKey)
	}
	if err := m2.DeleteAPIKey(); err != nil || m2.View().APIKeySet {
		t.Fatalf("delete: %v %+v", err, m2.View())
	}
}

func TestAgentSessionsMarksThisEggify(t *testing.T) {
	p := &countingProvider{}
	m := newTestManager(t, p, httpServe, "tok_1234567890")
	_ = m.Start()
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
	p.mu.Lock()
	md := p.lastOC.Metadata
	p.mu.Unlock()
	_, inst, _, _ := parseAgentMetadata(md)

	sessions := []TunnelSession{
		{ID: "ts_other", Metadata: "", StartedAt: "2026-10-01T10:00:00Z"},
		{ID: "ts_stale", Metadata: AgentMetadata("mac", inst, "oldnonce"), StartedAt: "2026-10-01T11:00:00Z"},
		{ID: "ts_live", Metadata: md, StartedAt: "2026-10-01T09:00:00Z"},
		{ID: "ts_server", Metadata: AgentMetadata("server", "otherinstall", "n"), StartedAt: "2026-10-01T12:00:00Z"},
	}
	f, srv := newFakeNgrokAPI(t, "key_1234567890", sessions...)
	f.pageSize = 100
	m.cfg.API = &NgrokAPI{BaseURL: srv.URL}

	if _, err := m.AgentSessions(context.Background()); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("without key: %v", err)
	}
	if err := m.SetAPIKey("key_1234567890"); err != nil {
		t.Fatal(err)
	}
	got, err := m.AgentSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0].ID != "ts_live" || !got[0].Current || !got[0].ThisInstall {
		t.Fatalf("live session not first/current: %+v", got)
	}
	byID := map[string]AgentSession{}
	for _, a := range got {
		byID[a.ID] = a
	}
	if s := byID["ts_stale"]; !s.ThisInstall || s.Current || !s.Eggify {
		t.Fatalf("stale: %+v", s)
	}
	if s := byID["ts_server"]; s.ThisInstall || !s.Eggify || s.EggifyHost != "server" {
		t.Fatalf("server: %+v", s)
	}
	if s := byID["ts_other"]; s.Eggify {
		t.Fatalf("other: %+v", s)
	}
	if err := m.StopAgentSession(context.Background(), "ts_live"); !errors.Is(err, ErrOwnSession) {
		t.Fatalf("stopping own live session: %v", err)
	}
	if err := m.StopAgentSession(context.Background(), "ts_stale"); err != nil {
		t.Fatal(err)
	}
	if len(f.stopped) != 1 || f.stopped[0] != "ts_stale" {
		t.Fatalf("stopped %v", f.stopped)
	}
	_ = m.Stop()
}

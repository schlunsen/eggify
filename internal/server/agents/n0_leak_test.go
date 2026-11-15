package agents

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/connectors"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/n0"
	"github.com/schlunsen/eggify/internal/n0/n0test"
)

type n0LeakEnv struct {
	db   *database.Database
	key  []byte
	m    *n0.Manager
	ls   *n0.LoopbackServer
	fake *n0test.Fake
	sm   *SessionManager
	sid  uuid.UUID
}

func newN0LeakEnv(t *testing.T) *n0LeakEnv {
	t.Helper()
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(255 - i)
	}
	m := n0.NewManager(db, key)
	ls, err := n0.StartLoopback(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	fake := n0test.New()
	t.Cleanup(func() { ls.Close(); fake.Close(); database.ResetInstance() })

	sm := &SessionManager{config: &Config{Model: "sonnet"}, sessions: map[uuid.UUID]*AgentSession{}, db: db.GetDB()}
	sm.SetConnectorEncryptionKey(key)
	sm.SetN0(m, ls.ProxyURL())

	sid := uuid.New()
	if _, err := db.GetDB().Exec(`INSERT INTO agent_sessions (id, status) VALUES (?, 'idle')`, sid.String()); err != nil {
		t.Fatal(err)
	}
	e := &n0LeakEnv{db: db, key: key, m: m, ls: ls, fake: fake, sm: sm, sid: sid}

	// A pre-existing legacy n0 API key (the env-var connector path).
	legacy, _ := connectors.Encrypt("legacy-n0-secret-key", key)
	extra := `{"domain":"nzero.pro"}`
	if err := database.NewConnectorRepository(db).SaveConnection(&database.UserConnection{ConnectorSlug: "n0", APIKey: &legacy, ExtraConfig: &extra, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := database.NewConnectorRepository(db).EnableSessionConnector(sid.String(), "n0"); err != nil {
		t.Fatal(err)
	}

	// Connect over OAuth through the real loopback callback.
	res, err := m.StartAuthorization(context.Background(), n0.StartRequest{Host: fake.URL, RedirectURI: ls.CallbackURL(), RedirectKey: n0.RedirectKeyLoopback})
	if err != nil {
		t.Fatal(err)
	}
	loc, err := fake.Approve(res.AuthorizeURL, "demo-app")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(loc)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if st := m.FlowStatus(res.State); st.Status != "connected" {
		t.Fatalf("connect: %+v", st)
	}
	return e
}

func (e *n0LeakEnv) assertClean(t *testing.T, where, s string) {
	t.Helper()
	secrets := append(e.fake.Secrets(), "legacy-n0-secret-key")
	for _, sec := range secrets {
		if strings.Contains(s, sec) {
			t.Fatalf("%s contains a credential (%s…)", where, sec[:8])
		}
	}
	for _, marker := range []string{"pp_oat_", "pp_ort_", "N0_API_KEY", "NZERO_API_KEY"} {
		if strings.Contains(s, marker) {
			t.Fatalf("%s contains %q", where, marker)
		}
	}
}

// TestN0TokenNeverReachesAgentRuntimes checks every place an agent can see:
// Claude CLI env + --mcp-config JSON, Codex MCP config overrides, the direct
// provider's eggify-tools env, the project's .mcp.json, and the logs.
func TestN0TokenNeverReachesAgentRuntimes(t *testing.T) {
	logDir := t.TempDir()
	if logging.GetLogger() == nil {
		if _, err := logging.Initialize(logDir, true); err != nil {
			t.Fatal(err)
		}
	}
	e := newN0LeakEnv(t)

	// Exercise the token paths so they would show up in logs if logged:
	// tool calls, a forced refresh, needs_reauth.
	for _, name := range []string{"n0_connection_info", "n0_app_status"} {
		if _, err := e.m.CallTool(context.Background(), e.sid.String(), name, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	e.fake.ExpireAccessTokens()
	if _, err := e.m.CallTool(context.Background(), e.sid.String(), "n0_app_status", nil); err != nil {
		t.Fatalf("after refresh: %v", err)
	}

	// --- Claude SDK runtime ---
	prompt := "code"
	opts := e.sm.buildSDKOptions(SessionOptions{SystemPrompt: &prompt}, e.sid)
	opts = e.sm.injectConnectorCredentials(e.sid, opts)
	envJSON, _ := json.Marshal(opts.Env)
	e.assertClean(t, "Claude CLI env", string(envJSON))
	mcpJSON, _ := json.Marshal(opts.McpServers) // becomes --mcp-config (visible in ps)
	e.assertClean(t, "Claude --mcp-config", string(mcpJSON))
	if !strings.Contains(string(mcpJSON), n0.EnvProxyToken) || !strings.Contains(string(mcpJSON), e.ls.ProxyURL()) {
		t.Fatalf("eggify-tools is not wired to the n0 proxy: %s", mcpJSON)
	}
	if sp, _ := opts.SystemPrompt.(types.SystemPromptPreset); sp.Append == nil || !strings.Contains(*sp.Append, n0.SessionHint) {
		t.Fatalf("n0 hint missing from system prompt: %+v", opts.SystemPrompt)
	}

	// --- Codex app-server runtime ---
	cs := &AgentSession{}
	cs.ID = e.sid
	codexCfg := e.sm.codexEggifyToolsConfig(cs)
	exe, _ := os.Executable()
	if got := codexCfg["mcp_servers"].(map[string]any)["eggify-tools"].(map[string]any)["command"]; got != exe {
		t.Fatalf("codex eggify-tools command = %v, want the running binary %s", got, exe)
	}
	codexJSON, _ := json.Marshal(codexCfg)
	e.assertClean(t, "Codex mcp_servers config", string(codexJSON))
	if !strings.Contains(string(codexJSON), n0.EnvProxyURL) {
		t.Fatalf("codex eggify-tools not wired: %s", codexJSON)
	}
	if !strings.Contains(e.sm.withN0Hint(e.sid, codexPreamble(cs)), n0.SessionHint) {
		t.Fatal("codex preamble lacks the n0 hint")
	}

	// --- Direct provider runtime (eggify-tools spawned by mcpclient) ---
	directEnv, _ := json.Marshal(e.sm.eggifyToolsEnv(e.sid))
	e.assertClean(t, "direct-provider eggify-tools env", string(directEnv))
	if !strings.Contains(e.sm.withN0Hint(e.sid, "tools"), n0.SessionHint) {
		t.Fatal("direct system prompt lacks the n0 hint")
	}

	// The proxy capability is session-scoped and is not an n0 credential.
	cap := e.sm.eggifyToolsEnv(e.sid)[n0.EnvProxyToken]
	if cap == "" || !n0.VerifyProxyToken(e.sid.String(), cap) || n0.VerifyProxyToken(uuid.NewString(), cap) {
		t.Fatal("proxy capability is not bound to the session")
	}

	// --- project .mcp.json ---
	proj := t.TempDir()
	ensureMCPConfigInDir(proj)
	if b, err := os.ReadFile(filepath.Join(proj, ".mcp.json")); err == nil {
		e.assertClean(t, ".mcp.json", string(b))
		if strings.Contains(string(b), n0.EnvProxyToken) {
			t.Fatal(".mcp.json must not carry session capabilities")
		}
	}

	// needs_reauth path (also logged by callers)
	e.fake.RevokeAll()
	e.fake.ExpireAccessTokens()
	if _, err := e.m.CallTool(context.Background(), e.sid.String(), "n0_app_status", nil); err == nil {
		t.Fatal("expected needs_reauth")
	}

	// --- logs ---
	if lg := logging.GetLogger(); lg != nil {
		for _, p := range []string{lg.GetLogFilePath(), lg.GetErrorLogFilePath(), lg.GetStderrFilePath()} {
			if b, err := os.ReadFile(p); err == nil {
				e.assertClean(t, "log "+filepath.Base(p), string(b))
			}
		}
	}
}

// TestLegacyN0KeyStillInjectedWithoutOAuth keeps the legacy mode working for
// existing users who have not connected over OAuth.
func TestLegacyN0KeyStillInjectedWithoutOAuth(t *testing.T) {
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.ResetInstance()
	key := make([]byte, 32)
	sm := &SessionManager{config: &Config{Model: "sonnet"}, sessions: map[uuid.UUID]*AgentSession{}, db: db.GetDB()}
	sm.SetConnectorEncryptionKey(key)
	sm.SetN0(n0.NewManager(db, key), "http://127.0.0.1:1/internal/n0/mcp")
	sid := uuid.New()
	db.GetDB().Exec(`INSERT INTO agent_sessions (id, status) VALUES (?, 'idle')`, sid.String())
	legacy, _ := connectors.Encrypt("legacy-key", key)
	database.NewConnectorRepository(db).SaveConnection(&database.UserConnection{ConnectorSlug: "n0", APIKey: &legacy, Status: "active"})
	database.NewConnectorRepository(db).EnableSessionConnector(sid.String(), "n0")

	opts := sm.injectConnectorCredentials(sid, sm.buildSDKOptions(SessionOptions{}, sid))
	if opts.Env["N0_API_KEY"] != "legacy-key" {
		t.Fatalf("legacy key not injected: %v", opts.Env)
	}
	// No OAuth connection → no n0 hint.
	if strings.Contains(sm.withN0Hint(sid, "x"), n0.SessionHint) {
		t.Fatal("hint added without an OAuth connection")
	}
}

func TestReusableClaudeClientHonoursToolsStale(t *testing.T) {
	sm := &SessionManager{sessions: map[uuid.UUID]*AgentSession{}}
	s := &AgentSession{}
	s.ID = uuid.New()
	sm.sessions[s.ID] = s
	if sm.reusableClaudeClient(s) != nil {
		t.Fatal("expected nil client")
	}
	sm.MarkSessionToolsStale(s.ID)
	if !s.claudeToolsStale.Load() || !s.directToolsStale.Load() {
		t.Fatal("stale flags not set")
	}
}

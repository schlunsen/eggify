package agents

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/connectors"
	"github.com/schlunsen/eggify/internal/credproxy"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
)

// fakeProvider records the credential and path of the last request.
type fakeProvider struct {
	*httptest.Server
	mu               sync.Mutex
	path, xKey, auth string
}

func newFakeProvider(t *testing.T) *fakeProvider {
	f := &fakeProvider{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.path, f.xKey, f.auth = r.URL.Path, r.Header.Get("X-Api-Key"), r.Header.Get("Authorization")
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeProvider) last() (string, string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.path, f.xKey, f.auth
}

// Secrets that must never show up anywhere an agent can look.
const (
	secretDeepSeek  = "ds-secret-0123456789"
	secretGLM       = "glm-secret-0123456789"
	secretCodex     = "sk-codex-secret-0123456789"
	secretConfig    = "sk-ant-config-secret-0123456789"
	secretEnvToken  = "env-auth-token-secret-0123456789"
	secretConnector = "sk-ant-connector-secret-0123456789"
)

var allSecrets = []string{secretDeepSeek, secretGLM, secretCodex, secretConfig, secretEnvToken, secretConnector}

func assertNoSecrets(t *testing.T, where, s string) {
	t.Helper()
	for _, sec := range allSecrets {
		if strings.Contains(s, sec) {
			t.Fatalf("%s contains a provider secret (%s…)", where, sec[:10])
		}
	}
}

func setProviderRow(t *testing.T, sm *SessionManager, id, key, customURL string) {
	t.Helper()
	_, err := sm.db.Exec(`INSERT INTO providers (provider_id, name, api_key, custom_url) VALUES (?, ?, ?, ?)
		ON CONFLICT(provider_id) DO UPDATE SET api_key = excluded.api_key, custom_url = excluded.custom_url`, id, id, key, customURL)
	if err != nil {
		t.Fatal(err)
	}
}

type proxyTestEnv struct {
	sm    *SessionManager
	proxy *credproxy.Server
}

func newProxyTestEnv(t *testing.T) *proxyTestEnv {
	t.Helper()
	// The Eggify server's own environment may hold keys; agents must not inherit them.
	t.Setenv("ANTHROPIC_AUTH_TOKEN", secretEnvToken)
	t.Setenv("CLAUDE_API_KEY", secretConfig)
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("CODEX_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	sm, cleanup := newModelSwitchTestManager(t)
	t.Cleanup(cleanup)
	sm.config.APIKey = secretConfig
	p, err := credproxy.Start(sm.ResolveProviderUpstream, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	sm.SetProviderProxy(p)
	return &proxyTestEnv{sm: sm, proxy: p}
}

func call(t *testing.T, url string, hdr map[string]string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"model":"m","stream":false}`))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// TestClaudeSessionEnvHasCapabilityNotKey covers every Claude CLI options
// builder, the connector env, --mcp-config, the proxy round trip and a
// mid-session provider switch.
func TestClaudeSessionEnvHasCapabilityNotKey(t *testing.T) {
	if logging.GetLogger() == nil {
		if _, err := logging.Initialize(t.TempDir(), true); err != nil {
			t.Fatal(err)
		}
	}
	e := newProxyTestEnv(t)
	ds, glm := newFakeProvider(t), newFakeProvider(t)
	setProviderRow(t, e.sm, "deepseek", secretDeepSeek, ds.URL+"/anthropic")
	setProviderRow(t, e.sm, "glm", secretGLM, glm.URL+"/api/anthropic")
	s := seedSwitchSession(t, e.sm, "deepseek", "deepseek-v4-pro", nil)

	// An "anthropic" connector would set ANTHROPIC_API_KEY; it must not
	// replace the capability.
	key := make([]byte, 32)
	e.sm.SetConnectorEncryptionKey(key)
	enc, _ := connectors.Encrypt(secretConnector, key)
	repo := database.NewConnectorRepository(database.GetInstance())
	if err := repo.SaveConnection(&database.UserConnection{ConnectorSlug: "anthropic", APIKey: &enc, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnableSessionConnector(s.ID.String(), "anthropic"); err != nil {
		t.Fatal(err)
	}

	builders := map[string]*types.ClaudeAgentOptions{
		"applyClaudeProviderAuth": e.sm.injectConnectorCredentials(s.ID,
			e.sm.applyClaudeProviderAuth(s.ID, &s.Session, types.NewClaudeAgentOptions())),
		"buildSDKOptions": e.sm.injectConnectorCredentials(s.ID, e.sm.buildSDKOptions(s.Options, s.ID)),
	}
	wantBase := e.proxy.BaseURL(credproxy.KindAnthropic)
	var capability string
	for name, opts := range builders {
		envJSON, _ := json.Marshal(opts.Env)
		assertNoSecrets(t, name+" env", string(envJSON))
		mcpJSON, _ := json.Marshal(opts.McpServers)
		assertNoSecrets(t, name+" --mcp-config", string(mcpJSON))
		if opts.BaseURL == nil || *opts.BaseURL != wantBase {
			t.Fatalf("%s: base URL %v, want the proxy %s", name, opts.BaseURL, wantBase)
		}
		capability = opts.Env["ANTHROPIC_API_KEY"]
		if sid, ok := credproxy.VerifyToken(capability, credproxy.KindAnthropic); !ok || sid != s.ID.String() {
			t.Fatalf("%s: ANTHROPIC_API_KEY is not this session's capability: %q", name, capability)
		}
		for _, v := range []string{"ANTHROPIC_AUTH_TOKEN", "CLAUDE_API_KEY"} {
			if got, ok := opts.Env[v]; !ok || got != "" {
				t.Fatalf("%s: inherited %s not blanked (%q, %v)", name, v, got, ok)
			}
		}
		if opts.Env["_CLAUDE_CODE_ASSUME_FIRST_PARTY_BASE_URL"] != "" {
			t.Fatalf("%s: a third-party provider must not be marked first-party", name)
		}
	}

	// The capability reaches DeepSeek with DeepSeek's real key.
	if code := call(t, wantBase+"/v1/messages?beta=true", map[string]string{"X-Api-Key": capability}); code != 200 {
		t.Fatalf("proxy status %d", code)
	}
	if path, xKey, auth := ds.last(); path != "/anthropic/v1/messages" || xKey != secretDeepSeek || auth != "" {
		t.Fatalf("deepseek saw path=%q x-api-key=%q auth=%q", path, xKey, auth)
	}

	// Switch provider mid-session: the same capability now reaches GLM.
	if _, err := e.sm.ChangeSessionModel(s.ID, "glm", "glm-5.3", nil); err != nil {
		t.Fatal(err)
	}
	if code := call(t, wantBase+"/v1/messages", map[string]string{"X-Api-Key": capability}); code != 200 {
		t.Fatalf("proxy status after switch %d", code)
	}
	if path, xKey, _ := glm.last(); path != "/api/anthropic/v1/messages" || xKey != secretGLM {
		t.Fatalf("glm saw path=%q x-api-key=%q", path, xKey)
	}

	// Switching to Codex makes the Anthropic capability useless.
	setProviderRow(t, e.sm, "codex", secretCodex, "")
	if _, err := e.sm.ChangeSessionModel(s.ID, "codex", "gpt-6-astra", nil); err != nil {
		t.Fatal(err)
	}
	if code := call(t, wantBase+"/v1/messages", map[string]string{"X-Api-Key": capability}); code != http.StatusUnauthorized {
		t.Fatalf("anthropic capability on a codex session: %d, want 401", code)
	}

	// --- project .mcp.json and logs ---
	proj := t.TempDir()
	ensureMCPConfigInDir(proj)
	if b, err := os.ReadFile(filepath.Join(proj, ".mcp.json")); err == nil {
		assertNoSecrets(t, ".mcp.json", string(b))
	}
	if lg := logging.GetLogger(); lg != nil {
		for _, p := range []string{lg.GetLogFilePath(), lg.GetErrorLogFilePath(), lg.GetStderrFilePath()} {
			if b, err := os.ReadFile(p); err == nil {
				assertNoSecrets(t, "log "+filepath.Base(p), string(b))
			}
		}
	}
}

func TestClaudeFirstPartyProxyKeepsFirstPartyFeatures(t *testing.T) {
	e := newProxyTestEnv(t)
	s := seedSwitchSession(t, e.sm, "claude", "sonnet", nil)
	opts := e.sm.applyClaudeProviderAuth(s.ID, &s.Session, types.NewClaudeAgentOptions())
	if opts.Env["_CLAUDE_CODE_ASSUME_FIRST_PARTY_BASE_URL"] != "1" {
		t.Fatalf("api.anthropic.com behind the proxy must stay first-party: %v", opts.Env)
	}
	up, err := e.sm.ResolveProviderUpstream(t.Context(), s.ID.String(), credproxy.KindAnthropic)
	if err != nil || up.BaseURL != "https://api.anthropic.com" || up.APIKey != secretConfig || up.Auth != credproxy.AuthXAPIKey {
		t.Fatalf("upstream %+v err %v", up, err)
	}
}

func TestOpenRouterUsesBearer(t *testing.T) {
	e := newProxyTestEnv(t)
	setProviderRow(t, e.sm, "custom", "or-key", "https://openrouter.ai/api")
	s := seedSwitchSession(t, e.sm, "custom", "anthropic/claude-sonnet", nil)
	up, err := e.sm.ResolveProviderUpstream(t.Context(), s.ID.String(), credproxy.KindAnthropic)
	if err != nil || up.Auth != credproxy.AuthBearer || up.APIKey != "or-key" {
		t.Fatalf("upstream %+v err %v", up, err)
	}
}

// Users who log the Claude CLI in with their subscription have no key in Eggify:
// the environment is left alone.
func TestSubscriptionLoginLeavesEnvUntouched(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	p, err := credproxy.Start(sm.ResolveProviderUpstream, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	sm.SetProviderProxy(p)
	s := seedSwitchSession(t, sm, "claude", "sonnet", nil)
	opts := sm.applyClaudeProviderAuth(s.ID, &s.Session, types.NewClaudeAgentOptions())
	if _, ok := opts.Env["ANTHROPIC_API_KEY"]; ok || opts.BaseURL != nil {
		t.Fatalf("subscription session got env %v base %v", opts.Env, opts.BaseURL)
	}
}

// Without a running proxy (listener failed / EGGIFY_PROVIDER_PROXY=off) the old
// behaviour is kept so sessions keep working.
func TestNoProxyFallsBackToKeyInEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	setProviderRow(t, sm, "deepseek", "ds-key", "https://api.deepseek.com/anthropic")
	s := seedSwitchSession(t, sm, "deepseek", "deepseek-v4-pro", nil)
	opts := sm.applyClaudeProviderAuth(s.ID, &s.Session, types.NewClaudeAgentOptions())
	if opts.Env["ANTHROPIC_API_KEY"] != "ds-key" || opts.BaseURL == nil || *opts.BaseURL != "https://api.deepseek.com/anthropic" {
		t.Fatalf("fallback env %v base %v", opts.Env, opts.BaseURL)
	}
}

func TestCodexSessionEnvHasCapabilityNotKey(t *testing.T) {
	e := newProxyTestEnv(t)
	t.Setenv("OPENAI_API_KEY", secretCodex+"-env")
	oai := newFakeProvider(t)
	setProviderRow(t, e.sm, "codex", secretCodex, oai.URL+"/v1")
	s := seedSwitchSession(t, e.sm, "codex", "gpt-6-astra", nil)

	key, base := e.sm.resolveCodexConfig(s)
	env, effBase := e.sm.codexProviderEnv(s.ID, key, base)
	joined := strings.Join(env, "\n")
	assertNoSecrets(t, "codex env", joined)
	if effBase != e.proxy.BaseURL(credproxy.KindOpenAI) {
		t.Fatalf("codex base URL %q, want the proxy", effBase)
	}
	var capability string
	for _, kv := range env {
		if strings.HasPrefix(kv, "EGGIFY_CODEX_API_KEY=") {
			capability = strings.TrimPrefix(kv, "EGGIFY_CODEX_API_KEY=")
		}
	}
	if _, ok := credproxy.VerifyToken(capability, credproxy.KindOpenAI); !ok {
		t.Fatalf("EGGIFY_CODEX_API_KEY is not a capability: %q", capability)
	}
	for _, v := range []string{"CODEX_API_KEY=", "OPENAI_API_KEY="} {
		if !strings.Contains(joined+"\n", v+"\n") {
			t.Fatalf("inherited %s not blanked: %v", v, env)
		}
	}
	if code := call(t, effBase+"/responses", map[string]string{"Authorization": "Bearer " + capability}); code != 200 {
		t.Fatalf("proxy status %d", code)
	}
	if path, xKey, auth := oai.last(); path != "/v1/responses" || auth != "Bearer "+secretCodex || xKey != "" {
		t.Fatalf("openai saw path=%q auth=%q x-api-key=%q", path, auth, xKey)
	}
	// An Anthropic-kind capability does not work on the Codex route.
	if code := call(t, effBase+"/responses", map[string]string{"Authorization": "Bearer " + credproxy.Token(s.ID.String(), credproxy.KindAnthropic)}); code != http.StatusUnauthorized {
		t.Fatalf("wrong-kind capability: %d", code)
	}

	// ChatGPT login (no key): nothing to proxy.
	if env, b := e.sm.codexProviderEnv(s.ID, "", ""); env != nil || b != "" {
		t.Fatalf("codex login session got env %v base %q", env, b)
	}
}

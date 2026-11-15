package agents

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/credproxy"
	"github.com/schlunsen/eggify/internal/logging"
)

// Model-provider credentials never enter an agent's environment while the
// loopback provider proxy runs (internal/credproxy). The Claude CLI gets
//
//	ANTHROPIC_BASE_URL = http://127.0.0.1:<port>/anthropic
//	ANTHROPIC_API_KEY  = eggify-cap1.<session>.<hmac>   (a session capability)
//
// and the Codex app-server gets the same for its eggify_openai model provider.
// The proxy looks up the session's CURRENT provider, base URL and key on every
// request (so model switches apply), swaps the auth header and streams the
// request/response through.
//
// When no key is configured (Claude subscription login, `codex login`) there
// is nothing to proxy and the environment is left as before.

type providerProxyState struct {
	server atomic.Pointer[credproxy.Server]
}

// SetProviderProxy wires the loopback provider proxy (nil disables it).
func (sm *SessionManager) SetProviderProxy(p *credproxy.Server) { sm.credProxy.server.Store(p) }

// ProviderProxy returns the provider proxy, or nil.
func (sm *SessionManager) ProviderProxy() *credproxy.Server { return sm.credProxy.server.Load() }

// Environment variables that hold the model credential Eggify picked for a
// runtime. When the proxy is in use they are blanked in the agent env, so a
// key the Eggify server inherited from its own environment does not leak either.
var (
	claudeCredentialEnv = []string{"ANTHROPIC_AUTH_TOKEN", "CLAUDE_API_KEY"}
	codexCredentialEnv  = []string{"CODEX_API_KEY", "OPENAI_API_KEY"}
)

// claudeCredentials is what a Claude CLI session authenticates with.
type claudeCredentials struct {
	provider string
	apiKey   string
	baseURL  string // configured base URL ("" = CLI default / inherited env)
	bearer   bool   // key must be sent as Authorization: Bearer
	fromEnv  bool   // key is the server's inherited ANTHROPIC_AUTH_TOKEN
	// customBase: the session points at a base URL that is not the
	// provider's own; apiKey is the session's own key or a placeholder.
	customBase bool
}

// inferClaudeProviderID returns the session's provider, inferring it from the
// model name when it was not set explicitly.
func inferClaudeProviderID(s *Session) string {
	if s.Options.Provider != nil && *s.Options.Provider != "" {
		return *s.Options.Provider
	}
	switch {
	case strings.HasPrefix(s.ModelName, "glm-"):
		return "glm"
	case strings.HasPrefix(s.ModelName, "claude-"):
		return "claude"
	case strings.Contains(s.ModelName, "gpt-"):
		return "openai"
	case strings.Contains(s.ModelName, "deepseek"):
		return "deepseek"
	}
	return ""
}

// providerAPIKey reads a provider's configured API key from the DB.
func (sm *SessionManager) providerAPIKey(providerID string) string {
	if sm.db == nil || providerID == "" {
		return ""
	}
	var key string
	if err := sm.db.QueryRow("SELECT COALESCE(api_key, '') FROM providers WHERE provider_id = ? LIMIT 1", providerID).Scan(&key); err != nil {
		return ""
	}
	return key
}

// providerBaseURL reads a provider's base URL (custom_url wins) from the DB.
func (sm *SessionManager) providerBaseURL(providerID string) string {
	if sm.db == nil || providerID == "" {
		return ""
	}
	var u string
	if err := sm.db.QueryRow("SELECT COALESCE(NULLIF(custom_url, ''), NULLIF(base_url, ''), '') FROM providers WHERE provider_id = ? LIMIT 1", providerID).Scan(&u); err != nil {
		return ""
	}
	return u
}

// resolveClaudeCredentials resolves the key and base URL of a Claude CLI
// session. Key: session-specific > providers table > server config/env.
// Base URL: providers table > session-specific > known defaults.
func (sm *SessionManager) resolveClaudeCredentials(s *Session) claudeCredentials {
	c := claudeCredentials{provider: inferClaudeProviderID(s)}
	if sm.config != nil {
		c.apiKey = sm.config.APIKey
	}
	if k := sm.providerAPIKey(c.provider); k != "" {
		c.apiKey = k
	}
	if s.Options.APIKey != nil && *s.Options.APIKey != "" {
		c.apiKey = *s.Options.APIKey
	}
	if c.apiKey == "" {
		// A gateway token the server inherited: the CLI would send it as
		// Authorization: Bearer.
		if tok := os.Getenv("ANTHROPIC_AUTH_TOKEN"); tok != "" {
			c.apiKey, c.bearer, c.fromEnv = tok, true, true
		}
	}

	c.baseURL = sm.providerBaseURL(c.provider)
	if c.baseURL == "" && s.Options.BaseURL != nil && *s.Options.BaseURL != "" {
		c.baseURL = *s.Options.BaseURL
		// SECURITY: a session base URL that is not the provider's own host
		// gets only a key supplied with the session (provider_key_scope.go).
		defaults := claudeDefaultBase(c.provider)
		if env := os.Getenv("ANTHROPIC_BASE_URL"); env != "" && (c.provider == "" || c.provider == "claude") {
			defaults = append(defaults, env)
		}
		if !sm.storedKeyMayReach(c.provider, c.baseURL, defaults...) {
			c.apiKey = sessionKeyForCustomBase(s, c.provider, c.baseURL)
			c.bearer, c.fromEnv, c.customBase = false, false, true
		}
	}
	if c.baseURL == "" {
		switch c.provider {
		case "glm":
			c.baseURL = "https://api.z.ai/api/anthropic"
		case "deepseek":
			c.baseURL = "https://api.deepseek.com"
		case "openai":
			c.baseURL = "https://api.openai.com/v1"
		}
	}
	if host := hostOf(c.upstreamBase()); host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai") {
		c.bearer = true // OpenRouter's Anthropic endpoint takes a Bearer token
	}
	return c
}

// upstreamBase is where the CLI would have sent requests without the proxy.
func (c claudeCredentials) upstreamBase() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	if env := os.Getenv("ANTHROPIC_BASE_URL"); env != "" {
		return env
	}
	return "https://api.anthropic.com"
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// applyClaudeProviderAuth sets the model credential env for a Claude CLI
// session: a proxy capability when the provider proxy runs, otherwise (proxy
// unavailable) the real key as before.
func (sm *SessionManager) applyClaudeProviderAuth(sessionID uuid.UUID, s *Session, opts *types.ClaudeAgentOptions) *types.ClaudeAgentOptions {
	c := sm.resolveClaudeCredentials(s)
	if c.apiKey == "" {
		// Claude subscription login (or no credentials at all): nothing to proxy.
		if c.baseURL != "" {
			opts = opts.WithBaseURL(c.baseURL)
		}
		logging.Debug("No API key for session %s (provider=%s) - the Claude CLI uses its own login", sessionID, c.provider)
		return opts
	}

	p := sm.ProviderProxy()
	if p == nil || sessionID == uuid.Nil || p.BaseURL(credproxy.KindAnthropic) == "" {
		logging.Warning("Provider proxy unavailable - session %s gets its API key in the agent environment", sessionID)
		if !c.fromEnv {
			opts = opts.WithEnvVar("ANTHROPIC_API_KEY", c.apiKey)
		}
		if c.customBase {
			opts = blankClaudeLoginEnv(opts)
		}
		if c.baseURL != "" {
			opts = opts.WithBaseURL(c.baseURL)
		}
		return opts
	}

	opts = opts.WithBaseURL(p.BaseURL(credproxy.KindAnthropic))
	opts = opts.WithEnvVar("ANTHROPIC_API_KEY", credproxy.Token(sessionID.String(), credproxy.KindAnthropic))
	for _, v := range claudeCredentialEnv {
		opts = opts.WithEnvVar(v, "")
	}
	if c.customBase {
		opts = blankClaudeLoginEnv(opts)
	}
	// The CLI treats any non-Anthropic ANTHROPIC_BASE_URL as a third-party
	// gateway and turns off first-party features (tool search, betas). When
	// the proxy fronts api.anthropic.com, tell it the endpoint is first-party.
	if hostOf(c.upstreamBase()) == "api.anthropic.com" {
		opts = opts.WithEnvVar("_CLAUDE_CODE_ASSUME_FIRST_PARTY_BASE_URL", "1")
	} else {
		opts = opts.WithEnvVar("_CLAUDE_CODE_ASSUME_FIRST_PARTY_BASE_URL", "")
	}
	logging.Debug("Session %s (provider=%s) reaches %s through the provider proxy", sessionID, c.provider, hostOf(c.upstreamBase()))
	return opts
}

// blankClaudeLoginEnv clears the credentials the Claude CLI could fall back
// to (a gateway token, an OAuth token) for a session on a custom base URL.
func blankClaudeLoginEnv(opts *types.ClaudeAgentOptions) *types.ClaudeAgentOptions {
	for _, v := range []string{"ANTHROPIC_AUTH_TOKEN", "CLAUDE_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
		opts = opts.WithEnvVar(v, "")
	}
	return opts
}

// usesProviderProxy reports whether opts were pointed at the provider proxy.
func (sm *SessionManager) usesProviderProxy(opts *types.ClaudeAgentOptions) bool {
	p := sm.ProviderProxy()
	return p != nil && opts != nil && opts.BaseURL != nil && *opts.BaseURL == p.BaseURL(credproxy.KindAnthropic)
}

// proxyManagedEnv are env vars a connector must not overwrite while the
// session talks to its model through the proxy.
var proxyManagedEnv = map[string]bool{
	"ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true, "ANTHROPIC_BASE_URL": true,
	"CLAUDE_API_KEY": true, "_CLAUDE_CODE_ASSUME_FIRST_PARTY_BASE_URL": true,
}

// codexProviderEnv returns the env additions and the base URL for a Codex
// app-server run that authenticates with an API key.
func (sm *SessionManager) codexProviderEnv(sessionID uuid.UUID, key, baseURL string) (env []string, effectiveBaseURL string) {
	if key == "" {
		return nil, baseURL
	}
	p := sm.ProviderProxy()
	if p == nil || sessionID == uuid.Nil || p.BaseURL(credproxy.KindOpenAI) == "" {
		logging.Warning("Provider proxy unavailable - Codex session %s gets its API key in the agent environment", sessionID)
		return []string{"EGGIFY_CODEX_API_KEY=" + key}, baseURL
	}
	env = []string{"EGGIFY_CODEX_API_KEY=" + credproxy.Token(sessionID.String(), credproxy.KindOpenAI)}
	for _, v := range codexCredentialEnv {
		env = append(env, v+"=")
	}
	return env, p.BaseURL(credproxy.KindOpenAI)
}

// ResolveProviderUpstream is the provider proxy's resolver: it maps a session
// to its CURRENT provider base URL and key on every request.
func (sm *SessionManager) ResolveProviderUpstream(_ context.Context, sessionID string, kind credproxy.Kind) (credproxy.Upstream, error) {
	id, err := uuid.Parse(sessionID)
	if err != nil {
		return credproxy.Upstream{}, credproxy.ErrNoUpstream
	}
	sm.mu.RLock()
	live, ok := sm.sessions[id]
	var snap Session
	if ok {
		snap = live.Session
	}
	sm.mu.RUnlock()
	if !ok {
		return credproxy.Upstream{}, credproxy.ErrNoUpstream
	}
	codex := isCodexProvider(codexStr(snap.Options.Provider), snap.ModelName)

	switch kind {
	case credproxy.KindAnthropic:
		if codex {
			return credproxy.Upstream{}, credproxy.ErrNoUpstream
		}
		c := sm.resolveClaudeCredentials(&snap)
		if c.apiKey == "" {
			return credproxy.Upstream{}, credproxy.ErrNoUpstream
		}
		up := credproxy.Upstream{BaseURL: c.upstreamBase(), APIKey: c.apiKey, Auth: credproxy.AuthXAPIKey}
		if c.bearer {
			up.Auth = credproxy.AuthBearer
		}
		return up, nil
	case credproxy.KindOpenAI:
		if !codex {
			return credproxy.Upstream{}, credproxy.ErrNoUpstream
		}
		key, base := sm.resolveCodexConfig(&AgentSession{Session: snap})
		if key == "" {
			return credproxy.Upstream{}, credproxy.ErrNoUpstream
		}
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		return credproxy.Upstream{BaseURL: base, APIKey: key, Auth: credproxy.AuthBearer}, nil
	}
	return credproxy.Upstream{}, credproxy.ErrNoUpstream
}

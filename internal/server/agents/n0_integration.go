package agents

import (
	"context"
	"os"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	claude "github.com/schlunsen/claude-agent-sdk-go"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	codextypes "github.com/schlunsen/codex-sdk-go/types"
	"github.com/schlunsen/eggify/internal/capability"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/n0"
)

// n0 integration: every runtime (Claude SDK, Codex app-server, direct
// provider) loads the eggify-tools stdio MCP server. When the Eggify server runs
// the n0 loopback proxy, each session's eggify-tools process gets that
// session's id and proxy capability in its environment and exposes n0's
// tools by forwarding them to the Eggify server, which attaches the n0 token.
// The n0 token itself never enters an agent's env, CLI args or config.

// n0State is the SessionManager's n0 wiring (all fields optional).
type n0State struct {
	manager  atomic.Pointer[n0.Manager]
	proxyURL atomic.Value // string
}

// SetN0 wires the n0 manager and the loopback proxy URL.
func (sm *SessionManager) SetN0(m *n0.Manager, proxyURL string) {
	sm.n0.manager.Store(m)
	sm.n0.proxyURL.Store(proxyURL)
	if m != nil {
		m.OnChange = sm.MarkN0SessionsToolsStale
		// High-impact n0 / connector tools need the user's approval.
		m.SetToolGuard(sm.n0ToolGuard)
	}
}

// N0Manager returns the n0 manager, or nil.
func (sm *SessionManager) N0Manager() *n0.Manager { return sm.n0.manager.Load() }

func (sm *SessionManager) n0ProxyURL() string {
	v, _ := sm.n0.proxyURL.Load().(string)
	return v
}

// eggifyToolsEnv is the environment of a session's eggify-tools process. It holds
// the session id and session-scoped capabilities (n0 proxy, /internal/apps) —
// never n0 tokens or the API key.
func (sm *SessionManager) eggifyToolsEnv(sessionID uuid.UUID) map[string]string {
	if sessionID == uuid.Nil {
		return nil
	}
	sid := sessionID.String()
	env := map[string]string{
		capability.EnvSessionID: sid,
		capability.EnvAppsToken: capability.Token(capability.Apps, sid),
	}
	for k, v := range n0.ProxyEnv(sid, sm.n0ProxyURL()) {
		env[k] = v
	}
	return env
}

func eggifyBinaryPath() string {
	p, _ := os.Executable()
	if p == "" {
		p = "eggify" // fallback to PATH lookup
	}
	return p
}

// eggifyToolsMCPConfig is the --mcp-config value for Claude CLI sessions.
func (sm *SessionManager) eggifyToolsMCPConfig(sessionID uuid.UUID) map[string]interface{} {
	return map[string]interface{}{
		"mcpServers": map[string]types.McpStdioServerConfig{
			"eggify-tools": {
				Command: eggifyBinaryPath(),
				Args:    []string{"mcp-server"},
				Env:     sm.eggifyToolsEnv(sessionID),
			},
		},
	}
}

// n0Enabled reports whether a session has n0 enabled with an OAuth connection.
func (sm *SessionManager) n0Enabled(sessionID uuid.UUID) bool {
	m := sm.N0Manager()
	if m == nil || sm.n0ProxyURL() == "" || !m.HasConnections() {
		return false
	}
	ok, err := m.Repo().SessionHasConnectorEnabled(sessionID.String(), n0.ConnectorSlug)
	return err == nil && ok
}

// withN0Hint appends the n0 guidance to a system prompt when the session has
// n0 enabled. n0's own guide is served by the n0_app_guide tool, not pasted.
func (sm *SessionManager) withN0Hint(sessionID uuid.UUID, prompt string) string {
	if !sm.n0Enabled(sessionID) || strings.Contains(prompt, n0.SessionHint) {
		return prompt
	}
	if strings.TrimSpace(prompt) == "" {
		return n0.SessionHint
	}
	return prompt + "\n\n" + n0.SessionHint
}

// skipLegacyN0Env reports whether the legacy N0_API_KEY env injection must be
// skipped: once OAuth connections exist, n0 is reached only through the
// eggify-tools proxy.
func (sm *SessionManager) skipLegacyN0Env(slug string) bool {
	if slug != n0.ConnectorSlug {
		return false
	}
	m := sm.N0Manager()
	if m != nil {
		return m.HasConnections()
	}
	if db := database.GetInstance(); db != nil {
		n, err := database.NewN0Repository(db).CountN0Connections()
		return err == nil && n > 0
	}
	return false
}

// MarkSessionToolsStale makes the session respawn its runtime (Claude CLI /
// direct MCP client) at the next prompt so a changed tool list is picked up.
// A running turn is never interrupted. Codex spawns per turn and needs nothing.
func (sm *SessionManager) MarkSessionToolsStale(sessionID uuid.UUID) {
	sm.mu.RLock()
	session, ok := sm.sessions[sessionID]
	sm.mu.RUnlock()
	if !ok {
		return
	}
	session.claudeToolsStale.Store(true)
	session.directToolsStale.Store(true)
	logging.Info("🔌 n0 tools changed for session %s — runtime will reload tools on the next prompt", sessionID)
}

// MarkN0SessionsToolsStale marks every session with n0 enabled.
func (sm *SessionManager) MarkN0SessionsToolsStale() {
	m := sm.N0Manager()
	if m == nil {
		return
	}
	ids, err := m.Repo().SessionsWithConnectorEnabled(n0.ConnectorSlug)
	if err != nil {
		return
	}
	for _, s := range ids {
		if id, err := uuid.Parse(s); err == nil {
			sm.MarkSessionToolsStale(id)
		}
	}
}

// bindN0AtCreate binds a new session to the requested n0 connection.
func (sm *SessionManager) bindN0AtCreate(sessionID string, connectionID *int64) {
	m := sm.N0Manager()
	if m == nil || connectionID == nil || *connectionID == 0 {
		return
	}
	if _, err := m.Repo().GetN0Connection(*connectionID); err != nil {
		logging.Warning("n0: session %s requested unknown connection %d", sessionID, *connectionID)
		return
	}
	if err := database.NewConnectorRepository(database.GetInstance()).EnableSessionConnector(sessionID, n0.ConnectorSlug); err != nil {
		logging.Warning("n0: enabling n0 for session %s: %v", sessionID, err)
		return
	}
	if err := m.Repo().BindN0Session(sessionID, *connectionID); err != nil {
		logging.Warning("n0: binding session %s: %v", sessionID, err)
	}
}

// reusableClaudeClient returns the session's cached Claude client, or nil
// when there is none or the tool set changed since it was spawned (then the
// stale client is closed and the caller spawns a fresh one with --resume).
func (sm *SessionManager) reusableClaudeClient(session *AgentSession) *claude.Client {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.client != nil && session.claudeToolsStale.CompareAndSwap(true, false) {
		logging.Info("🔌 Reloading Claude CLI for session %s to pick up changed tools", session.ID)
		_ = sm.closeClaudeClient(session, context.Background())
	}
	return session.client
}

// codexEggifyToolsConfig is the mcp_servers config for a Codex thread. Like the
// Claude and direct paths it runs eggify-tools from the running server's own
// binary: a `eggify` found on PATH may be an older install that lacks tools
// (such as the n0 proxy) this server expects.
func (sm *SessionManager) codexEggifyToolsConfig(session *AgentSession) codextypes.ConfigObject {
	return codexMCPConfig(eggifyBinaryPath(), sm.eggifyToolsEnv(session.ID))
}

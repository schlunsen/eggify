package agents

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/workspacetrust"
)

// Workspace trust: a session only loads the Claude config that lives in its
// working directory (.claude/settings.json, .claude/settings.local.json and
// .mcp.json: hooks, allow rules, env, apiKeyHelper, MCP servers) when the user
// trusted that folder with exactly that config. Otherwise the CLI runs with
// `--setting-sources ""` (no project/local settings) and `--strict-mcp-config`
// (only Eggify's own --mcp-config servers), and the session shows a banner asking
// the user to review and trust the config.
//
// The check runs before every prompt (three stat calls while nothing changed,
// see workspacetrust.Checker). When the decision differs from the one the
// cached CLI process was started with, the process is closed and the next one
// is started with the new decision. That is how trusting takes effect on the
// next turn, and how a config file the agent edited falls back to untrusted:
// its fingerprint no longer matches.
//
// Codex sessions: Codex has its own project trust (projects."<path>".trust_level
// in ~/.codex/config.toml) and only loads a project's .codex/config.toml for
// trusted projects. Eggify never marks projects trusted for Codex, so nothing is
// gated here. The direct-provider loop loads no project config at all.

type trustState struct {
	once    sync.Once
	checker *workspacetrust.Checker
}

// trustChecker returns the session manager's workspace trust checker.
func (sm *SessionManager) trustChecker() *workspacetrust.Checker {
	sm.trust.once.Do(func() {
		opts := workspacetrust.Options{EggifyBinaries: append([]string{eggifyBinaryPath(), findEggifyBinaryPath()}, workspacetrust.DefaultEggifyBinaries()...)}
		sm.trust.checker = workspacetrust.NewChecker(sm.db, opts)
	})
	return sm.trust.checker
}

// TrustChecker exposes the checker to the REST handlers.
func (sm *SessionManager) TrustChecker() *workspacetrust.Checker { return sm.trustChecker() }

func sessionWorkDir(o *SessionOptions) string {
	if o != nil && o.WorkingDirectory != nil {
		return *o.WorkingDirectory
	}
	return ""
}

// WorkspaceTrustDecision returns how a session's working directory may be used.
// A session without a working directory runs in the server's cwd, which has
// no project config Eggify would gate, so it is treated as having none.
func (sm *SessionManager) WorkspaceTrustDecision(session *AgentSession) workspacetrust.Decision {
	return sm.workspaceTrustForOptions(&session.Options)
}

func (sm *SessionManager) workspaceTrustForOptions(o *SessionOptions) workspacetrust.Decision {
	dir := sessionWorkDir(o)
	if dir == "" {
		return workspacetrust.Decision{Status: workspacetrust.StatusNoConfig}
	}
	return sm.trustChecker().Decide(dir)
}

// applyWorkspaceTrust sets the CLI flags for a trust decision. Every code path
// that builds options for claude.NewClient must call it.
func applyWorkspaceTrust(opts *types.ClaudeAgentOptions, d workspacetrust.Decision) *types.ClaudeAgentOptions {
	if d.LoadProjectConfig() {
		// Project instructions (CLAUDE.md), slash commands and the folder's
		// settings, as before workspace trust existed.
		return opts.WithSettingSources(types.SettingSourceLocal, types.SettingSourceProject)
	}
	// One empty source renders as `--setting-sources ""`: no settings files at
	// all. Leaving SettingSources empty would omit the flag, and the CLI would
	// then load every source.
	return opts.WithSettingSources(types.SettingSource("")).WithStrictMcpConfig(true)
}

// prepareWorkspaceTrust runs before each prompt on the Claude SDK path. It
// returns the decision to build options with, and closes the cached CLI
// process when that process was started under a different decision.
func (sm *SessionManager) prepareWorkspaceTrust(session *AgentSession) workspacetrust.Decision {
	d := sm.WorkspaceTrustDecision(session)
	key := d.Key()

	session.mu.Lock()
	if session.client != nil && session.trustKey != key {
		logging.Info("🛡️ Session %s: workspace trust changed (%q → %q); restarting the Claude CLI", session.ID, session.trustKey, key)
		_ = sm.closeClaudeClient(session, context.Background())
	}
	session.trustKey = key
	notify := !d.LoadProjectConfig() && session.trustNoticeKey != key
	if notify {
		session.trustNoticeKey = key
	}
	session.mu.Unlock()

	if notify {
		sm.emitSessionNotice(session, workspaceTrustNotice(d))
	}
	return d
}

// recordClientTrust notes the decision a newly started client runs under.
func recordClientTrust(session *AgentSession, d workspacetrust.Decision) {
	session.mu.Lock()
	session.trustKey = d.Key()
	session.mu.Unlock()
}

func workspaceTrustNotice(d workspacetrust.Decision) string {
	what := "its own Claude config"
	if d.Inventory != nil && d.Inventory.Summary() != "" {
		what = fmt.Sprintf("its own Claude config (%s)", d.Inventory.Summary())
	}
	if d.Status == workspacetrust.StatusChanged {
		return fmt.Sprintf("🛡️ This folder's Claude config changed since it was trusted, so Eggify did not load it. The folder has %s. Review it in the banner above the chat to trust it again.", what)
	}
	return fmt.Sprintf("🛡️ This folder has %s that Eggify did not load because the folder is not trusted. Review it in the banner above the chat to trust it.", what)
}

// matchSessionAlwaysAllow checks the session's always-allow rules (kept in
// the session options in the DB, the only place Eggify stores them).
func (sm *SessionManager) matchSessionAlwaysAllow(sessionID uuid.UUID, toolName string, input map[string]interface{}) (bool, string) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	session, ok := sm.sessions[sessionID]
	if !ok || len(session.Options.AlwaysAllowRules) == 0 {
		return false, ""
	}
	return CheckAlwaysAllowRules(session.Options.AlwaysAllowRules, toolName, input)
}

// pruneLegacySettingsPermissions removes over-broad allow entries older
// versions of Eggify wrote to the folder's settings.local.json. It is Eggify's own
// edit, so it runs inside WithOwnWrite: a folder trusted with its current
// config stays trusted, an untrusted one stays untrusted.
func (sm *SessionManager) pruneLegacySettingsPermissions(session *AgentSession) {
	dir := sessionWorkDir(&session.Options)
	if dir == "" {
		return
	}
	_ = sm.trustChecker().WithOwnWrite(dir, func() error {
		pruneUnsafeSettingsPermissions(dir)
		return nil
	})
}

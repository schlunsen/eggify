package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/skills"
)

// Native skills: Eggify is the gatekeeper (discover → pending → a user
// approves), the runtime runs the approved skills.
//
//   - Claude sessions get a plugin directory built from the snapshots of the
//     approved skills that apply to the session (skills.ApplicableSkills),
//     passed with --plugin-dir on every path that starts a Claude CLI
//     (applySessionSkills, next to applyWorkspaceTrust). Slash commands go to
//     the CLI as typed; nothing is pasted into the system prompt.
//   - The set is content-addressed (skillsKey). When it changes (a skill was
//     approved, edited, sent back to review), the cached CLI is closed before
//     the next prompt, never while a turn is running, so the next one starts
//     with the new plugin directory (prepareSessionSkills).
//   - Codex and direct (OpenAI-compatible) sessions cannot load plugins: they
//     get an "Available skills" index in their instructions and load a skill
//     with the invoke_skill tool, which returns its body and snapshot folder.

// skillStore returns where snapshots and plugin directories live.
func (sm *SessionManager) skillStore() *skills.Store {
	if sm.skillsStore != nil {
		return sm.skillsStore
	}
	return skills.DefaultStore()
}

// SetSkillStore points the manager at another snapshot store (tests).
func (sm *SessionManager) SetSkillStore(st *skills.Store) { sm.skillsStore = st }

// sessionSkills returns the approved skills that apply to a session.
func (sm *SessionManager) sessionSkills(o *SessionOptions) []*database.Skill {
	if sm.repo == nil || o == nil {
		return nil
	}
	all, err := sm.repo.Skill.GetAllSkills("")
	if err != nil {
		logging.Warning("Skills: cannot list skills: %v", err)
		return nil
	}
	// Approved file skills synced before folders were fingerprinted are
	// fingerprinted now (or sent back to review) before a session uses them.
	if skills.UpgradeLegacyFileSkills(sm.repo.Skill, sm.skillStore(), all) {
		if all, err = sm.repo.Skill.GetAllSkills(""); err != nil {
			return nil
		}
	}
	return skills.ApplicableSkills(all, sessionWorkDir(o), o.EnabledSkillIDs)
}

// sessionSkillPlugin builds (or reuses) the plugin directory for the
// session's skills. Both are "" when no approved skill applies.
func (sm *SessionManager) sessionSkillPlugin(o *SessionOptions) (dir, key string) {
	list := sm.sessionSkills(o)
	if len(list) == 0 {
		return "", ""
	}
	dir, key, err := sm.skillStore().EnsurePlugin(list)
	if err != nil {
		logging.Warning("Skills: cannot build the skills plugin: %v", err)
		return "", ""
	}
	return dir, key
}

// applySessionSkills adds the session's skills plugin to the CLI options and
// returns the key of the set it holds. Every code path that builds options
// for claude.NewClient must call it (next to applyWorkspaceTrust) and record
// the key with recordClientSkills once the client is stored.
func (sm *SessionManager) applySessionSkills(opts *types.ClaudeAgentOptions, o *SessionOptions) (*types.ClaudeAgentOptions, string) {
	dir, key := sm.sessionSkillPlugin(o)
	if dir != "" {
		opts = opts.WithLocalPlugin(dir)
	}
	return opts, key
}

// recordClientSkills notes the skill set a newly started client runs with.
func recordClientSkills(session *AgentSession, key string) {
	session.mu.Lock()
	session.skillsKey = key
	session.mu.Unlock()
}

// prepareSessionSkills runs before each prompt on the Claude SDK path. When
// the applicable skills changed since the cached CLI was started, it closes
// that CLI so the next one gets the new plugin directory, unless a turn is
// still running: then the change waits for the next prompt after it.
func (sm *SessionManager) prepareSessionSkills(session *AgentSession) {
	_, key := sm.sessionSkillPlugin(&session.Options)
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.client == nil || session.skillsKey == key {
		return
	}
	if session.claudeTurnOpen.Load() {
		logging.Info("🧩 Session %s: approved skills changed; the Claude CLI restarts after the running turn", session.ID)
		return
	}
	logging.Info("🧩 Session %s: approved skills changed (%q → %q); restarting the Claude CLI", session.ID, session.skillsKey, key)
	_ = sm.closeClaudeClient(session, context.Background())
}

// claudeSystemPrompt keeps Claude Code's own system prompt and appends
// Eggify's additions (project/area context, memories, n0 hint). A plain string
// would be passed as `--system-prompt <text>` and replace the CLI's prompt
// (tool use, skills, CLAUDE.md handling) wholesale; "code" used to be sent
// that way as a placeholder.
func claudeSystemPrompt(additions string) types.SystemPromptPreset {
	p := types.SystemPromptPreset{Type: "preset", Preset: "claude_code"}
	if s := strings.TrimSpace(additions); s != "" {
		p.Append = &s
	}
	return p
}

// customSystemPrompt returns the session's own system prompt text, without
// the historical "code" placeholder.
func customSystemPrompt(o *SessionOptions) string {
	if o == nil || o.SystemPrompt == nil {
		return ""
	}
	p := strings.TrimSpace(*o.SystemPrompt)
	if p == "code" {
		return ""
	}
	return p
}

// builtinSlashCommands are Claude Code commands that are not skills; a prompt
// starting with one never triggers skill discovery.
var builtinSlashCommands = map[string]bool{
	"context": true, "compact": true, "clear": true, "cost": true, "help": true,
	"init": true, "model": true, "review": true, "memory": true, "status": true,
	"agents": true, "mcp": true, "config": true, "permissions": true, "todos": true,
}

// noticeSlashSkill handles a prompt that starts with /<name> on a path with
// native or indexed skills. An unknown name triggers discovery (which only
// ever creates pending skills); a skill that is not approved gets a notice in
// the chat instead of silently doing nothing. The prompt itself is never
// rewritten.
func (sm *SessionManager) noticeSlashSkill(session *AgentSession, prompt string) {
	if sm.repo == nil {
		return
	}
	trimmed := strings.TrimSpace(prompt)
	if !strings.HasPrefix(trimmed, "/") {
		return
	}
	name := strings.TrimPrefix(strings.Fields(trimmed)[0], "/")
	name = strings.TrimPrefix(name, skills.PluginName+":")
	if name == "" || builtinSlashCommands[name] || strings.ContainsAny(name, `/\`) {
		return
	}
	skill, err := sm.repo.Skill.GetSkillByName(name)
	if err == nil && skill == nil {
		sm.autoDiscoverSkills(sessionWorkDir(&session.Options))
		skill, err = sm.repo.Skill.GetSkillByName(name)
	}
	if err != nil || skill == nil {
		return
	}
	switch skill.Status {
	case database.ReviewStatusPending:
		sm.emitSessionNotice(session, fmt.Sprintf("⏳ The skill /%s waits for your review in Skills → Review. It is not loaded until you approve it.", name))
	case database.ReviewStatusRejected:
		sm.emitSessionNotice(session, fmt.Sprintf("🚫 The skill /%s was rejected in review, so it is not loaded.", name))
	}
}

// skillIndexPrompt is the "Available skills" block for Codex and direct
// sessions ("" when no approved skill applies).
func (sm *SessionManager) skillIndexPrompt(o *SessionOptions) string {
	return skills.SkillIndex(sm.sessionSkills(o))
}

// joinNonEmpty joins the non-empty parts with blank lines.
func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n\n")
}

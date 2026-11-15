package agents

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/skills"
	"github.com/schlunsen/eggify/internal/workspacetrust"
	"github.com/stretchr/testify/require"
)

// newNativeSkillsManager returns a manager with a repository, a temp HOME and
// a temp snapshot store.
func newNativeSkillsManager(t *testing.T) (*SessionManager, *database.Repository, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sm, cleanup := newModelSwitchTestManager(t)
	t.Cleanup(cleanup)
	repo := database.NewRepository(database.GetInstance())
	sm.repo = repo
	sm.skillInjector = NewSkillInjector(repo)
	sm.SetSkillStore(&skills.Store{Root: t.TempDir()})
	return sm, repo, home
}

// pluginSkillFiles returns the files under skills/ of the session's plugin
// directory, by path relative to skills/ ("name/SKILL.md").
func pluginSkillFiles(t *testing.T, sm *SessionManager, o *SessionOptions) map[string]string {
	t.Helper()
	dir, _ := sm.sessionSkillPlugin(o)
	out := map[string]string{}
	if dir == "" {
		return out
	}
	root := filepath.Join(dir, "skills")
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		data, _ := os.ReadFile(p)
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	return out
}

func approvedUISkill(t *testing.T, repo *database.Repository, name, desc, body string) *database.Skill {
	t.Helper()
	s := &database.Skill{Name: name, Scope: "personal", Description: desc, Body: body, UserInvocable: true}
	require.NoError(t, repo.Skill.SaveSkill(s))
	got, err := repo.Skill.GetSkillByName(name)
	require.NoError(t, err)
	return got
}

// The plugin directory has the layout Claude Code expects and only approved
// skills; unchanged sets reuse the same directory.
func TestSessionSkillPluginLayout(t *testing.T) {
	sm, repo, _ := newNativeSkillsManager(t)
	approvedUISkill(t, repo, "notes", "Take notes", "Write notes.")
	require.NoError(t, repo.Skill.CreateAgentSkill(&database.Skill{Name: "sneaky", Scope: "personal", Body: "PENDING", UserInvocable: true}, "s1"))
	rejected := &database.Skill{Name: "nope", Scope: "personal", Body: "REJECTED", UserInvocable: true}
	require.NoError(t, repo.Skill.CreateAgentSkill(rejected, "s1"))
	_, err := repo.Skill.ReviewSkill(rejected.ID, database.SkillReview{Status: database.ReviewStatusRejected, ReviewedBy: "user:alice"})
	require.NoError(t, err)

	opts := &SessionOptions{}
	dir, key := sm.sessionSkillPlugin(opts)
	require.NotEmpty(t, dir)
	require.NotEmpty(t, key)
	var manifest map[string]interface{}
	data, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.Equal(t, skills.PluginName, manifest["name"])

	files := pluginSkillFiles(t, sm, opts)
	require.Len(t, files, 1)
	require.Contains(t, files["notes/SKILL.md"], "description: Take notes")
	for _, body := range files {
		require.NotContains(t, body, "PENDING")
		require.NotContains(t, body, "REJECTED")
	}

	dir2, key2 := sm.sessionSkillPlugin(opts)
	require.Equal(t, dir, dir2)
	require.Equal(t, key, key2)

	// Approving another skill changes the set.
	approvedUISkill(t, repo, "todo", "Track todos", "Keep a list.")
	_, key3 := sm.sessionSkillPlugin(opts)
	require.NotEqual(t, key, key3)
}

// --plugin-dir reaches the CLI on trusted, untrusted and sandboxed sessions,
// and the deny rules / sandbox never block reading the plugin directory.
func TestClaudeOptionsGetPluginDir(t *testing.T) {
	sm, repo, home := newNativeSkillsManager(t)
	// The store under ~/.claude/eggify, as in production.
	sm.SetSkillStore(&skills.Store{Root: filepath.Join(home, ".claude", "eggify")})
	approvedUISkill(t, repo, "notes", "Take notes", "Write notes.")
	work := t.TempDir()
	yes := true

	for _, tc := range []struct {
		name      string
		status    workspacetrust.Status
		sandboxed bool
	}{
		{"trusted", workspacetrust.StatusTrusted, false},
		{"untrusted", workspacetrust.StatusUntrusted, false},
		{"sandboxed", workspacetrust.StatusUntrusted, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &SessionOptions{WorkingDirectory: &work}
			if tc.sandboxed {
				o.Sandboxed = &yes
			}
			opts := applyWorkspaceTrust(types.NewClaudeAgentOptions(), workspacetrust.Decision{Status: tc.status})
			opts, key := sm.applySessionSkills(opts, o)
			opts, err := sm.applyClaudeSessionSettings(opts, o)
			require.NoError(t, err)
			require.NotEmpty(t, key)
			require.Len(t, opts.Plugins, 1)
			plugin := opts.Plugins[0]
			require.Equal(t, "local", plugin.Type)
			require.FileExists(t, filepath.Join(plugin.Path, "skills", "notes", "SKILL.md"))

			var settings claudeSessionSettings
			require.NoError(t, json.Unmarshal([]byte(*opts.Settings), &settings))
			for _, rule := range settings.Permissions.Deny {
				if strings.HasPrefix(rule, "Read(") {
					p := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(rule, "Read("), "/"), ")")
					p = strings.TrimSuffix(p, "/**")
					require.False(t, pathWithin(plugin.Path, p), "Read deny rule %s covers the plugin dir", rule)
				}
			}
			if tc.sandboxed {
				require.NotNil(t, settings.Sandbox)
				for _, p := range settings.Sandbox.Filesystem.DenyRead {
					require.False(t, pathWithin(plugin.Path, p), "sandbox denyRead %s covers the plugin dir", p)
				}
			}
		})
	}
}

// A changed skill set closes the cached CLI before the next prompt, but never
// while a turn is running.
func TestSkillsKeyChangeRestartsClientBetweenTurns(t *testing.T) {
	sm, repo, _ := newNativeSkillsManager(t)
	s := seedSwitchSession(t, sm, "claude", "sonnet", nil)
	_, key := sm.sessionSkillPlugin(&s.Options)
	s.client = fakeClient(t)
	recordClientSkills(s, key)

	// Unchanged: the client is kept.
	sm.prepareSessionSkills(s)
	require.NotNil(t, s.client)

	approvedUISkill(t, repo, "notes", "Take notes", "Write notes.")

	// Changed while a turn runs: kept until the turn ends.
	s.claudeTurnOpen.Store(true)
	sm.prepareSessionSkills(s)
	require.NotNil(t, s.client, "client closed in the middle of a turn")

	// Between turns: closed so the next prompt starts a CLI with the new set.
	sm.completeTurn(s)
	require.False(t, s.claudeTurnOpen.Load())
	sm.prepareSessionSkills(s)
	require.Nil(t, s.client)
}

// Claude Code's own system prompt is kept; Eggify's text is appended, and the
// old "code" placeholder is never sent.
func TestClaudeSystemPromptKeepsDefault(t *testing.T) {
	p := claudeSystemPrompt("")
	require.Equal(t, "claude_code", p.Preset)
	require.Nil(t, p.Append)
	code := "code"
	require.Equal(t, "", customSystemPrompt(&SessionOptions{SystemPrompt: &code}))
	p = claudeSystemPrompt("Project context")
	require.NotNil(t, p.Append)
	require.Equal(t, "Project context", *p.Append)
}

// A /name for a skill that waits for review tells the user so in the chat.
func TestSlashSkillPendingNotice(t *testing.T) {
	sm, repo, _ := newNativeSkillsManager(t)
	require.NoError(t, repo.Skill.CreateAgentSkill(&database.Skill{Name: "helper", Scope: "personal", Body: "x", UserInvocable: true}, "s1"))
	s := seedSwitchSession(t, sm, "claude", "sonnet", nil)
	before := s.MessageCount
	sm.noticeSlashSkill(s, "/helper do it")
	require.Equal(t, before+1, s.MessageCount, "no notice recorded")
	sm.noticeSlashSkill(s, "/compact")
	require.Equal(t, before+1, s.MessageCount)
}

// Direct sessions keep list_skills/invoke_skill, and invoke_skill is sent
// with its schema (not deferred) when approved skills apply.
func TestDirectSessionsGetSkillTools(t *testing.T) {
	all := append(bigToolSet(), tool("invoke_skill", "Load a skill"), tool("list_skills", "List skills"))
	kept := names(filterDirectProviderTools(all))
	require.Contains(t, kept, "invoke_skill")
	require.Contains(t, kept, "list_skills")

	ts := newDirectToolSet(all, nil)
	require.True(t, ts.deferred)
	require.NotContains(t, names(ts.offered()), "invoke_skill")
	ts.markCore("invoke_skill")
	require.Contains(t, names(ts.offered()), "invoke_skill")
	require.NotContains(t, names(ts.deferredTools()), "invoke_skill")
}

// Codex and direct sessions get an index of the approved skills (never the
// pending ones), with names and one-line descriptions only.
func TestSkillIndexForNonClaudeRuntimes(t *testing.T) {
	sm, repo, _ := newNativeSkillsManager(t)
	approvedUISkill(t, repo, "notes", "Take notes\nwhen asked", "SECRET BODY")
	require.NoError(t, repo.Skill.CreateAgentSkill(&database.Skill{Name: "sneaky", Scope: "personal", Body: "x", UserInvocable: true}, "s1"))
	idx := sm.skillIndexPrompt(&SessionOptions{})
	require.Contains(t, idx, "- notes: Take notes when asked")
	require.Contains(t, idx, "invoke_skill")
	require.NotContains(t, idx, "SECRET BODY")
	require.NotContains(t, idx, "sneaky")
	require.Equal(t, "a\n\nb", joinNonEmpty("a", "", " ", "b"))
}

package agents

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/principal"
	"github.com/stretchr/testify/require"
)

func TestCodexNetworkProfilePreservesFilesystem(t *testing.T) {
	for _, mode := range []string{"default", "read-only", "yolo", "allow-all"} {
		t.Run(mode, func(t *testing.T) {
			opts := SessionOptions{Sandboxed: boolp(true), PermissionMode: &mode, DangerouslySkipPermissions: boolp(true)}
			off := codexSandboxConfig("/home/u", "/work/project", &opts)
			opts.CodexNetworkAccess = true
			on := codexSandboxConfig("/home/u", "/work/project", &opts)
			offProfile := off["permissions."+codexSandboxProfile].(map[string]any)
			onProfile := on["permissions."+codexSandboxProfile].(map[string]any)
			require.Equal(t, false, offProfile["network"].(map[string]any)["enabled"])
			require.Equal(t, map[string]any{"enabled": true}, onProfile["network"])
			require.Equal(t, offProfile["filesystem"], onProfile["filesystem"])
			require.Equal(t, offProfile["extends"], onProfile["extends"])
			if mode == "read-only" {
				require.Equal(t, ":read-only", onProfile["extends"])
			} else {
				require.Equal(t, ":workspace", onProfile["extends"])
			}
			fs := onProfile["filesystem"].(map[string]any)
			require.Equal(t, "deny", fs["/home/u/.claude/eggify/.secret"])
			require.Equal(t, "deny", fs["/home/u/.codex/auth.json"])
			require.Equal(t, "read", fs["/work/project/.git/config"])
			require.Equal(t, "read", fs["/work/project/.mcp.json"])
			// Network consent changes exactly one field, never filesystem policy.
			onProfile["network"] = offProfile["network"]
			require.Equal(t, off, on)
		})
	}
	// A forged network flag without the filesystem sandbox never enables it.
	cfg := codexSandboxConfig("/home/u", "/work/project", &SessionOptions{CodexNetworkAccess: true})
	require.Equal(t, false, cfg["permissions."+codexSandboxProfile].(map[string]any)["network"].(map[string]any)["enabled"])
	// Leaf keys only: the user's own shell_environment_policy is merged, not replaced.
	require.NotContains(t, cfg, "shell_environment_policy")
	require.Equal(t, false, cfg["shell_environment_policy.ignore_default_excludes"])
	for _, name := range sandboxDeniedEnvVars {
		require.Equal(t, "", cfg["shell_environment_policy.set."+name])
	}
}

func TestCodexNetworkDefaultsAndPersistence(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	sm.repo = database.NewRepository(database.GetInstance())
	_, err := SaveSandboxPreferences(sm.repo, SandboxPreferences{DefaultEnabled: true, AllowedDomains: []string{"registry.npmjs.org"}})
	require.NoError(t, err)
	for _, raw := range []string{`{}`, `{"sandboxed":true}`, `{"codex_network_access":false}`} {
		var opts SessionOptions
		require.NoError(t, json.Unmarshal([]byte(raw), &opts))
		sm.applyConfigDefaults(&opts)
		require.False(t, opts.CodexNetworkAccess)
	}
	opts := SessionOptions{Provider: strp("codex"), Model: strp("gpt-6-astra"), Sandboxed: boolp(true), CodexNetworkAccess: true}
	// Internal callers cannot mint grants by deserializing untrusted options.
	_, err = sm.CreateSession(uuid.New(), opts)
	require.ErrorContains(t, err, "requires authenticated session creation")
	// Without a folder the workspace boundary would be the server's own cwd.
	opts.codexNetworkAuthorized = true
	_, err = sm.CreateSession(uuid.New(), opts)
	require.ErrorContains(t, err, "requires a working directory")
	opts.WorkingDirectory = strp(t.TempDir())
	created, err := sm.CreateSession(uuid.New(), opts)
	require.NoError(t, err)
	require.True(t, created.Options.CodexNetworkAccess)
	// The authorization is used up at creation: copying the live session's
	// options into another CreateSession grants nothing.
	live, err := sm.GetSession(created.ID)
	require.NoError(t, err)
	require.False(t, live.Options.codexNetworkAuthorized)
	_, err = sm.CreateSession(uuid.New(), live.Options)
	require.ErrorContains(t, err, "requires authenticated session creation")
	opts.codexNetworkAuthorized = true
	meta, err := sm.Storage.GetSession(created.ID)
	require.NoError(t, err)
	var stored SessionOptions
	require.NoError(t, json.Unmarshal([]byte(meta.OptionsJSON), &stored))
	require.True(t, stored.CodexNetworkAccess)
	require.False(t, stored.codexNetworkAuthorized, "authorization must never be serialized")
	// Reopen requests and generic updates cannot change a session's grant.
	reopened, err := sm.CreateSession(created.ID, SessionOptions{})
	require.NoError(t, err)
	require.True(t, reopened.Options.CodexNetworkAccess)
	// A client echoing the stored flag on reopen is not refused.
	reopened, err = sm.CreateSession(created.ID, SessionOptions{CodexNetworkAccess: true})
	require.NoError(t, err)
	require.True(t, reopened.Options.CodexNetworkAccess)
	updated, err := sm.UpdateSessionOptions(created.ID, func(o *SessionOptions) { o.CodexNetworkAccess = false })
	require.NoError(t, err)
	require.True(t, updated.Options.CodexNetworkAccess)
	// Exercise a database restore rather than the in-memory reopen path.
	sm.mu.Lock()
	delete(sm.sessions, created.ID)
	sm.mu.Unlock()
	restored, err := sm.CreateSession(created.ID, SessionOptions{})
	require.NoError(t, err)
	require.True(t, restored.Options.CodexNetworkAccess)
	legacy := seedSwitchSession(t, sm, "codex", "gpt-6-astra", nil)
	reopened, err = sm.CreateSession(legacy.ID, opts)
	require.NoError(t, err)
	require.False(t, reopened.Options.CodexNetworkAccess, "reopening must not widen an existing session")
	updated, err = sm.UpdateSessionOptions(legacy.ID, func(o *SessionOptions) { o.CodexNetworkAccess = true })
	require.NoError(t, err)
	require.False(t, updated.Options.CodexNetworkAccess)
	stripHandoverPermissions(&opts, HandoverOptions{})
	require.False(t, opts.CodexNetworkAccess)
	require.False(t, opts.codexNetworkAuthorized)
}

func TestCodexNetworkRequiresSandboxedCodex(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	for _, opts := range []SessionOptions{
		{Provider: strp("codex"), Sandboxed: boolp(false)},
		{Provider: strp("claude"), Sandboxed: boolp(true)},
	} {
		opts.CodexNetworkAccess, opts.codexNetworkAuthorized = true, true
		_, err := sm.CreateSession(uuid.New(), opts)
		require.ErrorContains(t, err, "requires a sandboxed Codex session")
	}
}

func TestCodexNetworkWSRequiresUserEvenWithAuthDisabled(t *testing.T) {
	for _, enforced := range []bool{true, false} {
		t.Run(map[bool]string{true: "auth-on", false: "auth-off"}[enforced], func(t *testing.T) {
			withEnforced(t, enforced)
			w := newWSHarness(t)
			conn := w.dial(t, "")
			id := uuid.New()
			require.NoError(t, conn.WriteJSON(map[string]any{
				"type": "create_session", "session_id": id,
				"options": map[string]any{"provider": "codex", "sandboxed": true, "codex_network_access": true, "codexNetworkAuthorized": true},
			}))
			got := w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "error" })
			require.True(t, isUserRequired(got), "%v", got)
			_, err := w.sm.GetSession(id)
			require.Error(t, err)
		})
	}
}

func TestCodexNetworkWSSignedInAuthorization(t *testing.T) {
	withEnforced(t, true)
	principal.SetAdminResolver(func(username string) bool { return username == "alice" })
	t.Cleanup(func() { principal.SetAdminResolver(nil) })
	w := newWSHarness(t)
	wd := t.TempDir()
	for _, user := range []string{"bob", "alice"} {
		conn := w.dial(t, user)
		id := uuid.New()
		require.NoError(t, conn.WriteJSON(map[string]any{
			"type": "create_session", "session_id": id,
			"options": map[string]any{"provider": "codex", "sandboxed": true, "codex_network_access": true, "working_directory": wd},
		}))
		got := w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "error" || m["type"] == "session_created" })
		if user == "bob" {
			require.Equal(t, principal.AdminErrorCode, got["code"])
			continue
		}
		require.Equal(t, "session_created", got["type"], "%v", got)
		session, err := w.sm.GetSession(id)
		require.NoError(t, err)
		require.True(t, session.Options.CodexNetworkAccess)
		require.True(t, isSandboxed(&session.Options))
	}
}

// Reopening an existing session never needs an admin, even when the client
// echoes codex_network_access, and never grants it.
func TestCodexNetworkWSReopenByNonAdminOwner(t *testing.T) {
	withEnforced(t, true)
	principal.SetAdminResolver(func(username string) bool { return username == "alice" })
	t.Cleanup(func() { principal.SetAdminResolver(nil) })
	w := newWSHarness(t)
	conn := w.dial(t, "bob")
	id := uuid.New()
	opts := map[string]any{"provider": "codex", "sandboxed": true, "working_directory": t.TempDir()}
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "create_session", "session_id": id, "options": opts}))
	got := w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "error" || m["type"] == "session_created" })
	require.Equal(t, "session_created", got["type"], "%v", got)

	opts["codex_network_access"] = true
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "create_session", "session_id": id, "options": opts}))
	got = w.expect(t, conn, func(m map[string]any) bool { return m["type"] == "error" || m["type"] == "session_created" })
	require.Equal(t, "session_created", got["type"], "%v", got)
	session, err := w.sm.GetSession(id)
	require.NoError(t, err)
	require.False(t, session.Options.CodexNetworkAccess)
}

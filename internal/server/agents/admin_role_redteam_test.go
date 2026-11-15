package agents

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/credproxy"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// withAdmins makes the named users admins (principal.SetAdminResolver), as
// the server does with several accounts.
func withAdmins(t *testing.T, admins ...string) {
	t.Helper()
	principal.SetAdminResolver(func(u string) bool {
		for _, a := range admins {
			if u == a {
				return true
			}
		}
		return false
	})
	t.Cleanup(func() { principal.SetAdminResolver(nil) })
}

func isAdminRequired(m map[string]any) bool {
	return m["type"] == "error" && m["code"] == principal.AdminErrorCode
}

// Attack: a second, non-admin account on the same Eggify uses the WebSocket
// privileged actions, which act as the server's OS user.
func TestRedTeam_WS_NonAdminGetsAdminRequired(t *testing.T) {
	withEnforced(t, true)
	withAdmins(t, "alice")
	w := newWSHarness(t)
	bob := w.dial(t, "bob")
	sid := w.session.ID.String()
	ch := w.addPending("perm-1")

	for _, msg := range []map[string]any{
		{"type": "toggle_yolo_mode", "session_id": sid, "enabled": true},
		{"type": "add_always_allow_rule", "session_id": sid, "rule": map[string]any{"tool": "Bash", "pattern": map[string]any{"command_prefix": "rm"}}},
		{"type": "remove_always_allow_rule", "session_id": sid, "rule_id": "r1"},
		{"type": "accept_handover", "handover_token": "0123456789abcdef0123"},
		{"type": "create_session", "session_id": uuid.NewString(), "options": map[string]any{"dangerously_skip_permissions": true}},
		{"type": "create_session", "session_id": uuid.NewString(), "options": map[string]any{"permission_mode": "bypassPermissions"}},
		{"type": "create_session", "session_id": uuid.NewString(), "options": map[string]any{"base_url": "https://evil.example"}},
		{"type": "change_session_model", "session_id": sid, "provider": "claude", "model": "opus", "base_url": "https://evil.example"},
		// The session has no owner: it belongs to the admin.
		{"type": "permission_response", "session_id": sid, "permission_id": "perm-1", "approved": true},
	} {
		if err := bob.WriteJSON(msg); err != nil {
			t.Fatal(err)
		}
		got := w.expect(t, bob, func(m map[string]any) bool { return m["type"] == "error" })
		if !isAdminRequired(got) {
			t.Errorf("%v from a non-admin user: got %v, want an admin_required error", msg["type"], got)
		}
	}
	select {
	case r := <-ch:
		t.Fatalf("a non-admin answered a permission request in the admin's session: %+v", r)
	default:
	}
	if w.session.Options.DangerouslySkipPermissions != nil && *w.session.Options.DangerouslySkipPermissions {
		t.Fatal("YOLO mode was enabled by a non-admin")
	}
	if n := len(w.sm.ListSessions()); n != 1 {
		t.Fatalf("non-admin privileged create_session created sessions: %d sessions", n)
	}

	// Control: the admin is allowed.
	alice := w.dial(t, "alice")
	_ = alice.WriteJSON(map[string]any{"type": "permission_response", "session_id": sid, "permission_id": "perm-1", "approved": true})
	w.expect(t, alice, func(m map[string]any) bool {
		if isAdminRequired(m) || isUserRequired(m) {
			t.Fatalf("admin refused: %v", m)
		}
		return m["type"] == string(MessageTypePermissionAcknowledged)
	})
	if r := <-ch; !r.Approved {
		t.Fatal("admin approval not delivered")
	}
}

// Ordinary work stays open to non-admins: answering a permission request in
// a session they own.
func TestNonAdminAnswersPermissionInOwnSession(t *testing.T) {
	withEnforced(t, true)
	withAdmins(t, "alice")
	w := newWSHarness(t)
	if _, err := w.sm.db.Exec(`INSERT INTO users (username) VALUES ('bob')`); err != nil {
		t.Fatal(err)
	}
	owner := "bob"
	if err := w.sm.SetSessionOwner(w.session.ID, &owner); err != nil {
		t.Fatal(err)
	}
	bob := w.dial(t, "bob")
	ch := w.addPending("perm-own")
	_ = bob.WriteJSON(map[string]any{"type": "permission_response", "session_id": w.session.ID.String(), "permission_id": "perm-own", "approved": true})
	w.expect(t, bob, func(m map[string]any) bool {
		if isAdminRequired(m) || isUserRequired(m) {
			t.Fatalf("non-admin refused in own session: %v", m)
		}
		return m["type"] == string(MessageTypePermissionAcknowledged)
	})
	select {
	case r := <-ch:
		if !r.Approved {
			t.Fatal("approval delivered as denial")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval not delivered")
	}
}

// Attack: point a session at an attacker's base URL so the provider proxy
// attaches the stored key on the way there. The stored key (providers table,
// server config, inherited env) must never reach a host that is not the
// provider's own; only a key supplied with the session may.
func TestRedTeam_SessionBaseURLNeverGetsStoredKey(t *testing.T) {
	e := newProxyTestEnv(t)
	evil := newFakeProvider(t)
	setProviderRow(t, e.sm, "claude", secretConfig, "")
	setProviderRow(t, e.sm, "codex", secretCodex, "")

	// Claude CLI path, provider claude (no URL of its own in the DB).
	evilURL := evil.URL + "/anthropic"
	s := seedSwitchSession(t, e.sm, "claude", "sonnet", func(s *Session) { s.Options.BaseURL = &evilURL })
	up, err := e.sm.ResolveProviderUpstream(t.Context(), s.ID.String(), credproxy.KindAnthropic)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecrets(t, "upstream key for a custom base URL", up.APIKey)
	if up.APIKey != noStoredKeyPlaceholder || up.BaseURL != evilURL {
		t.Fatalf("custom base upstream = %+v", up)
	}
	opts := e.sm.applyClaudeProviderAuth(s.ID, &s.Session, types.NewClaudeAgentOptions())
	if opts.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "" || opts.Env["ANTHROPIC_AUTH_TOKEN"] != "" {
		t.Fatalf("login tokens not blanked for a custom base: %v", opts.Env)
	}
	// Round trip through the proxy: the evil host sees no secret.
	capability := opts.Env["ANTHROPIC_API_KEY"]
	if code := call(t, e.proxy.BaseURL(credproxy.KindAnthropic)+"/v1/messages", map[string]string{"X-Api-Key": capability}); code != 200 {
		t.Fatalf("proxy status %d", code)
	}
	_, xKey, auth := evil.last()
	assertNoSecrets(t, "evil host x-api-key", xKey)
	assertNoSecrets(t, "evil host authorization", auth)

	// A key supplied with the session is the one used.
	own := "session-own-key"
	s2 := seedSwitchSession(t, e.sm, "claude", "sonnet", func(s *Session) { s.Options.BaseURL = &evilURL; s.Options.APIKey = &own })
	if up, _ := e.sm.ResolveProviderUpstream(t.Context(), s2.ID.String(), credproxy.KindAnthropic); up.APIKey != own {
		t.Fatalf("session key not used: %+v", up)
	}

	// The provider's own host keeps its stored key.
	anth := "https://api.anthropic.com"
	s3 := seedSwitchSession(t, e.sm, "claude", "sonnet", func(s *Session) { s.Options.BaseURL = &anth })
	if up, _ := e.sm.ResolveProviderUpstream(t.Context(), s3.ID.String(), credproxy.KindAnthropic); up.APIKey != secretConfig {
		t.Fatalf("own host lost its key: %+v", up)
	}

	// Codex: a custom base gets no stored or env key.
	t.Setenv("OPENAI_API_KEY", secretCodex+"-env")
	evilOAI := evil.URL + "/v1"
	c := seedSwitchSession(t, e.sm, "codex", "gpt-6-astra", func(s *Session) { s.Options.BaseURL = &evilOAI })
	key, base := e.sm.resolveCodexConfig(c)
	assertNoSecrets(t, "codex key for a custom base URL", key)
	if strings.Contains(key, secretCodex) || key != noStoredKeyPlaceholder || base != evilOAI {
		t.Fatalf("codex custom base: key=%q base=%q", key, base)
	}

	// Direct (OpenAI-compatible) path: no stored, config or env key.
	setProviderRow(t, e.sm, "deepseek", secretDeepSeek, "https://api.deepseek.com/anthropic")
	d := seedSwitchSession(t, e.sm, "deepseek", "deepseek-chat", func(s *Session) { s.Options.BaseURL = &evilOAI })
	dkey, dbase, err := e.sm.resolveDirectProviderConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecrets(t, "direct key for a custom base URL", dkey)
	if dkey != "" || !strings.HasPrefix(dbase, evil.URL) {
		t.Fatalf("direct custom base: key=%q base=%q", dkey, dbase)
	}
	// ...but its own host keeps the key.
	dsOwn := "https://api.deepseek.com/v1"
	d2 := seedSwitchSession(t, e.sm, "deepseek", "deepseek-chat", func(s *Session) { s.Options.BaseURL = &dsOwn })
	if k, _, _ := e.sm.resolveDirectProviderConfig(d2); k != secretDeepSeek {
		t.Fatalf("direct own host lost its key: %q", k)
	}
}

// Session base URLs must be http(s), https unless loopback; a model switch
// to another host drops the session's own key.
func TestSessionBaseURLValidation(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	s := seedSwitchSession(t, sm, "custom", "m1", nil)
	for _, bad := range []string{"file:///etc/passwd", "http://evil.example/v1", "gopher://x", "https://user:pw@x.example"} {
		b := bad
		if _, err := sm.ChangeSessionModel(s.ID, "custom", "m2", &b); err == nil {
			t.Errorf("base URL %q accepted", bad)
		}
	}
	key := "sk-session"
	s.Options.APIKey = &key
	local := "http://localhost:11434/v1#frag"
	if _, err := sm.ChangeSessionModel(s.ID, "custom", "m2", &local); err != nil {
		t.Fatalf("loopback http refused: %v", err)
	}
	got, _ := sm.GetSession(s.ID)
	if got.Options.BaseURL == nil || *got.Options.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("base URL not normalised: %v", got.Options.BaseURL)
	}
	if got.Options.APIKey != nil {
		t.Fatal("session key kept for a new host")
	}
}

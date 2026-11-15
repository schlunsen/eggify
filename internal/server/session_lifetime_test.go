package server

import (
	"os"
	"testing"
	"time"

	"github.com/schlunsen/eggify/internal/database"
)

// auth.session_lifetime_hours sets how long a new session lasts, and also ends
// sessions issued earlier under a longer lifetime.
func TestSessionLifetime_FollowsConfig(t *testing.T) {
	us := NewUserStore(t.TempDir())
	us.SetDatabase(NewTestDB(t))
	if err := us.Initialize(); err != nil {
		t.Fatal(err)
	}
	if err := us.CreateUser("alice", "password123", true); err != nil {
		t.Fatal(err)
	}
	if got := us.SessionLifetime(); got != SessionDuration {
		t.Fatalf("default lifetime %v, want %v", got, SessionDuration)
	}

	us.SetSessionLifetime(24 * time.Hour)
	token, err := us.Authenticate("alice", "password123")
	if err != nil {
		t.Fatal(err)
	}
	key := database.HashToken(token)
	us.mu.RLock()
	sess := us.sessions[key]
	us.mu.RUnlock()
	if d := time.Until(sess.ExpiresAt); d > 24*time.Hour || d < 23*time.Hour {
		t.Fatalf("new session expires in %v, want ~24h", d)
	}
	if _, err := us.ValidateSession(token); err != nil {
		t.Fatalf("fresh session refused: %v", err)
	}

	// A session issued 2 days ago with the old 7-day lifetime.
	us.mu.Lock()
	sess.CreatedAt = time.Now().Add(-48 * time.Hour)
	sess.ExpiresAt = sess.CreatedAt.Add(SessionDuration)
	us.mu.Unlock()
	if _, err := us.ValidateSession(token); err == nil {
		t.Fatal("session older than the configured lifetime was accepted")
	}

	us.SetSessionLifetime(0)
	if got := us.SessionLifetime(); got != SessionDuration {
		t.Fatalf("lifetime after reset %v, want %v", got, SessionDuration)
	}
}

// Every existing config file holds the session_timeout_hours: 24 written as
// its default; it must not shorten logins. New configs say 7 days.
func TestSessionLifetime_OldTimeoutKeyIgnored(t *testing.T) {
	cm := NewConfigManager(t.TempDir())
	if got := cm.getDefaultConfig().Auth.SessionLifetimeHours; got != 168 {
		t.Fatalf("default session_lifetime_hours = %d, want 168", got)
	}
	if err := os.MkdirAll(cm.configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cm.configFile, []byte(`{"auth":{"enabled":true,"user_auth_enabled":true,"session_timeout_hours":24}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := cm.LoadOrCreateConfig()
	if err != nil {
		t.Fatal(err)
	}
	us := NewUserStore(t.TempDir())
	us.SetSessionLifetime(time.Duration(config.Auth.SessionLifetimeHours) * time.Hour)
	if got := us.SessionLifetime(); got != SessionDuration {
		t.Fatalf("lifetime with only the old key = %v, want %v", got, SessionDuration)
	}
}

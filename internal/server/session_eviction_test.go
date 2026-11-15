package server

import (
	"testing"
	"time"

	"github.com/schlunsen/eggify/internal/database"
)

// A burst of logins (a sign-in loop, a few browsers) must not sign out the
// devices in use: the iPhone and the other Macs' pairings. Once the cap is
// reached, the least recently used session goes, not the oldest.
func TestSessionEviction_KeepsDevicesInUse(t *testing.T) {
	us := NewUserStore(t.TempDir())
	us.SetDatabase(NewTestDB(t))
	if err := us.Initialize(); err != nil {
		t.Fatal(err)
	}
	if err := us.CreateUser("alice", "password123", true); err != nil {
		t.Fatal(err)
	}

	// The oldest session is the paired iPhone, used a minute ago.
	phone, err := us.createUserSession("alice", SessionMeta{CreatedVia: "pairing"})
	if err != nil {
		t.Fatal(err)
	}
	us.mu.Lock()
	ps := us.sessions[database.HashToken(phone)]
	ps.CreatedAt = time.Now().Add(-72 * time.Hour)
	used := time.Now().Add(-time.Minute)
	ps.LastUsedAt = &used
	us.mu.Unlock()

	// Ten more logins: far more than the old cap of 5.
	var logins []string
	for i := 0; i < 10; i++ {
		tok, err := us.createUserSession("alice", SessionMeta{CreatedVia: "password"})
		if err != nil {
			t.Fatal(err)
		}
		logins = append(logins, tok)
	}
	if _, err := us.ValidateSession(phone); err != nil {
		t.Fatalf("ten logins signed out the paired iPhone: %v", err)
	}
	for i, tok := range logins {
		if _, err := us.ValidateSession(tok); err != nil {
			t.Fatalf("login %d was signed out: %v", i, err)
		}
	}

	// At the cap, the least recently used session goes first.
	for len(logins)+1 < maxSessionsPerUser {
		tok, err := us.createUserSession("alice", SessionMeta{CreatedVia: "password"})
		if err != nil {
			t.Fatal(err)
		}
		logins = append(logins, tok)
	}
	stale := logins[3]
	us.mu.Lock()
	for _, tok := range logins {
		s := us.sessions[database.HashToken(tok)]
		at := time.Now()
		if tok == stale {
			at = time.Now().Add(-48 * time.Hour)
			s.CreatedAt = at
		}
		s.LastUsedAt = &at
	}
	us.mu.Unlock()
	if _, err := us.createUserSession("alice", SessionMeta{CreatedVia: "password"}); err != nil {
		t.Fatal(err)
	}
	if _, err := us.ValidateSession(stale); err == nil {
		t.Fatal("the least recently used session survived the cap")
	}
	if _, err := us.ValidateSession(phone); err != nil {
		t.Fatalf("the cap signed out the recently used iPhone instead: %v", err)
	}
}

// Half-finished MFA logins are evicted before any real session.
func TestSessionEviction_PendingMFAFirst(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	sessions := []*Session{
		{TokenHash: "used-long-ago", CreatedAt: old.Add(-time.Hour)},
		{TokenHash: "pending", CreatedAt: now, MFARequired: true},
		{TokenHash: "recent", CreatedAt: old, LastUsedAt: &now},
	}
	evictionOrder(sessions)
	got := []string{sessions[0].TokenHash, sessions[1].TokenHash, sessions[2].TokenHash}
	want := []string{"pending", "used-long-ago", "recent"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("eviction order %v, want %v", got, want)
		}
	}
}

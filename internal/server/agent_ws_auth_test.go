package server

import (
	"testing"
	"time"

	"github.com/schlunsen/eggify/internal/database"
)

// A session token whose MFA step is not done must not sign a /agent/ws
// connection in (it would be a signed-in user for privileged actions).
func TestRedTeam_AgentWS_MFAPendingTokenRefused(t *testing.T) {
	us := NewInMemoryUserStore()
	us.users["alice"] = &User{Username: "alice"}
	PutTestSession(us, "sess-done", &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour)})
	PutTestSession(us, "sess-pending", &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour), MFARequired: true})

	validate := agentWSSessionValidator(us)
	if name, err := validate("sess-pending"); err == nil || name != "" {
		t.Fatalf("MFA-pending token accepted on /agent/ws as %q", name)
	}
	if name, err := validate("sess-done"); err != nil || name != "alice" {
		t.Fatalf("complete session refused: %q, %v", name, err)
	}

	// Re-checks of a live connection use the same rules.
	if us.sessionHashSignedIn(database.HashToken("sess-pending")) {
		t.Fatal("MFA-pending session counted as signed in")
	}
	if !us.sessionHashSignedIn(database.HashToken("sess-done")) {
		t.Fatal("signed-in session not recognised")
	}
	us.mu.Lock()
	delete(us.sessions, database.HashToken("sess-done"))
	us.mu.Unlock()
	if us.sessionHashSignedIn(database.HashToken("sess-done")) {
		t.Fatal("revoked session still counted as signed in")
	}
}

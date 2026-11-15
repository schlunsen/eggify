package server

import (
	"testing"
	"time"
)

// Attack: a second, non-admin account opens the terminal WebSocket (a shell
// as the server's OS user). Only an admin, or the only account, may.
func TestRedTeam_TerminalWebSocketNeedsAdmin(t *testing.T) {
	us := NewInMemoryUserStore()
	us.users["alice"] = &User{Username: "alice", IsAdmin: true}
	us.users["bob"] = &User{Username: "bob"}
	PutTestSession(us, "alice-tok", &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour)})
	PutTestSession(us, "bob-tok", &Session{Username: "bob", ExpiresAt: time.Now().Add(time.Hour)})
	s := &Server{userStore: us, sessionAuthMiddleware: NewSessionAuthMiddleware(us, true, false)}

	if s.validateWebSocketToken("bob-tok") {
		t.Fatal("a non-admin user opened the terminal")
	}
	if !s.validateWebSocketToken("alice-tok") {
		t.Fatal("the admin was refused the terminal")
	}

	// A single account is the owner of the machine, admin flag or not.
	delete(us.users, "alice")
	if !s.validateWebSocketToken("bob-tok") {
		t.Fatal("the only account was refused the terminal")
	}
}

func TestMayAdminister(t *testing.T) {
	us := NewInMemoryUserStore()
	if us.MayAdminister("nobody") {
		t.Fatal("unknown user")
	}
	us.users["solo"] = &User{Username: "solo"}
	if !us.MayAdminister("solo") {
		t.Fatal("the only user must count as admin")
	}
	us.users["admin"] = &User{Username: "admin", IsAdmin: true}
	if us.MayAdminister("solo") || !us.MayAdminister("admin") || us.MayAdminister("") {
		t.Fatal("with two accounts only the admin may administer")
	}
}

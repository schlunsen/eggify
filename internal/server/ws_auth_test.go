package server

import (
	"testing"
	"time"
)

func TestValidateWebSocketToken_UserAuth(t *testing.T) {
	us := NewInMemoryUserStore()
	us.apiKey = "the-api-key"
	us.users["alice"] = &User{Username: "alice"}
	PutTestSession(us, "good", &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour)})
	PutTestSession(us, "expired", &Session{Username: "alice", ExpiresAt: time.Now().Add(-time.Hour)})
	PutTestSession(us, "mfa-pending", &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour), MFARequired: true})
	PutTestSession(us, "mfa-done", &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour), MFARequired: true, MFAVerified: true})

	s := &Server{userStore: us, sessionAuthMiddleware: NewSessionAuthMiddleware(us, true, false)}

	cases := map[string]bool{
		"":            false,
		"x":           false, // any non-empty string used to be accepted
		"good":        true,
		"expired":     false,
		"mfa-pending": false,
		"mfa-done":    true,
		// The terminal is a shell: with user auth the API key (which agents
		// can read) is not enough, only a signed-in user's session.
		"the-api-key": false,
		"the-api-ke":  false,
	}
	for token, want := range cases {
		if got := s.validateWebSocketToken(token); got != want {
			t.Errorf("validateWebSocketToken(%q) = %v, want %v", token, got, want)
		}
	}
}

func TestValidateWebSocketToken_APIKeyOnly(t *testing.T) {
	s := &Server{authMiddleware: NewAuthMiddleware("k3y", true)}
	if s.validateWebSocketToken("") || s.validateWebSocketToken("nope") {
		t.Fatal("invalid API key accepted")
	}
	if !s.validateWebSocketToken("k3y") {
		t.Fatal("valid API key rejected")
	}
}

func TestValidateWebSocketToken_AuthDisabled(t *testing.T) {
	s := &Server{}
	if !s.validateWebSocketToken("") {
		t.Fatal("with auth fully disabled the socket should be open")
	}
}

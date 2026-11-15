package websocket

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestRedTeam_AnalyticsHub_SessionEventsReachOnlyUsersWhoMaySeeThem replays
// the leak: every signed-in user used to receive every session's
// agent_tool_use (full parameters) and command_recorded events.
func TestRedTeam_AnalyticsHub_SessionEventsReachOnlyUsersWhoMaySeeThem(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Shutdown()
	var enforced atomic.Bool
	enforced.Store(true)
	hub.SetAccessPolicy(nil, AccessPolicy{
		Enforced: func() bool { return enforced.Load() },
		IsAdmin:  func(u string) bool { return u == "admin" },
		CanSee:   func(u, sid string) bool { return u == "alice" && sid == "alice-session" },
	})
	mk := func(name string) *client {
		cl := newClient(nil)
		cl.username = name
		hub.register <- cl
		return cl
	}
	admin, alice, bob, apiKey := mk("admin"), mk("alice"), mk("bob"), mk("")

	got := func(cl *client) bool {
		select {
		case <-cl.send:
			return true
		case <-time.After(150 * time.Millisecond):
			return false
		}
	}

	hub.BroadcastSessionData("agent_tool_use", "alice-session", map[string]string{"command": "secret"})
	if !got(admin) || !got(alice) {
		t.Fatal("the owner and the admin must receive their session's event")
	}
	if got(bob) {
		t.Fatal("another user received a session event they may not see")
	}
	if got(apiKey) {
		t.Fatal("an API-key-only socket received a session event while user auth is enforced")
	}

	// A conversation id that is no agent session, and events without a
	// session, are for admins only.
	hub.BroadcastSessionData("command_recorded", "some-hook-conversation", map[string]string{"x": "y"})
	hub.BroadcastData("setting_updated", map[string]string{"key": "k"})
	for i := 0; i < 2; i++ {
		if !got(admin) {
			t.Fatal("admin must receive unscoped events")
		}
	}
	for _, cl := range []*client{alice, bob, apiKey} {
		if got(cl) {
			t.Fatal("a non-admin received an event that belongs to no visible session")
		}
	}

	// With user auth off nothing is scoped (the API key may do everything).
	enforced.Store(false)
	hub.BroadcastData("setting_updated", map[string]string{"key": "k"})
	for _, cl := range []*client{admin, alice, bob, apiKey} {
		if !got(cl) {
			t.Fatal("with user auth off every client keeps receiving events")
		}
	}
}

// TestRedTeam_AnalyticsHub_WatchdogClosesDeadSessionSocket: a socket whose
// session expired (or lost its MFA step) used to live on.
func TestRedTeam_AnalyticsHub_WatchdogClosesDeadSessionSocket(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Shutdown()
	hub.recheckEvery = 10 * time.Millisecond
	var valid atomic.Bool
	valid.Store(true)
	hub.SetSessionValidator(func(string) bool { return valid.Load() })
	cl := newClient(nil)
	cl.token = "tok"
	go hub.watchSession(cl)
	time.Sleep(40 * time.Millisecond)
	select {
	case <-cl.quit:
		t.Fatal("closed while the session was valid")
	default:
	}
	valid.Store(false)
	select {
	case <-cl.quit:
	case <-time.After(time.Second):
		t.Fatal("socket of an invalid session was not closed")
	}
}

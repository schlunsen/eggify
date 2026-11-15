package agents

import "testing"

// TestRedTeam_ReopenedSessionKeepsItsOwner: reopening a session that was
// only in the DB used to drop its owner, and the next write stored NULL,
// which the ownership checks treat as a shared session: any other signed-in
// user could then read and prompt it (YOLO included).
func TestRedTeam_ReopenedSessionKeepsItsOwner(t *testing.T) {
	withEnforced(t, true)
	w := newWSHarness(t)
	yes := true
	bobs := seedOwned(t, w, "bob", func(s *Session) {
		s.Options.DangerouslySkipPermissions = &yes
		s.Options.AllowDangerouslySkipPermissions = &yes
	})
	// Ended or evicted: only in the DB.
	w.sm.mu.Lock()
	delete(w.sm.sessions, bobs.ID)
	w.sm.mu.Unlock()

	bob := w.dial(t, "bob")
	_ = bob.WriteJSON(map[string]any{"type": "create_session", "session_id": bobs.ID.String(), "options": map[string]any{}})
	w.expect(t, bob, func(m map[string]any) bool { return m["type"] == string(MessageTypeSessionCreated) })

	live, err := w.sm.GetSession(bobs.ID)
	if err != nil {
		t.Fatal(err)
	}
	if live.OwnerUserID == nil || *live.OwnerUserID != "bob" {
		t.Fatalf("reopened session lost its owner: %v", live.OwnerUserID)
	}
	w.sm.persistSessionState(live)

	// A copy without an owner (older restore path) must not wipe it either.
	stale := live.Session
	stale.OwnerUserID = nil
	if err := w.sm.updateSessionInDB(&stale); err != nil {
		t.Fatal(err)
	}
	meta, err := w.sm.Storage.GetSession(bobs.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.OwnerUserID == nil || *meta.OwnerUserID != "bob" {
		t.Fatalf("stored owner after writes: %v", meta.OwnerUserID)
	}
	if err := w.h.validateSessionOwnership(bobs.ID, strPtr("alice")); err == nil {
		t.Fatal("alice may act on bob's reopened session")
	}
}

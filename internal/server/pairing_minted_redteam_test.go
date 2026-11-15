package server

import (
	"testing"
	"time"

	"github.com/schlunsen/eggify/internal/database"
)

// TestRedTeam_PairedSessionIsRevokedWithTheSessionThatMintedIt: a phone paired
// from a Mac stayed signed in after the Mac's session was signed out, so a
// stolen/revoked session could leave a durable foothold behind.
func TestRedTeam_PairedSessionIsRevokedWithTheSessionThatMintedIt(t *testing.T) {
	db := NewTestDB(t)
	us := NewUserStore(t.TempDir())
	us.SetDatabase(db)
	if err := us.Initialize(); err != nil {
		t.Fatal(err)
	}
	if err := us.CreateUser("alice", "password123", true); err != nil {
		t.Fatal(err)
	}
	var closed []string
	us.onMintedRevoked = func(hash string) { closed = append(closed, hash) }

	macTok, err := us.createUserSession("alice", SessionMeta{CreatedVia: "password"})
	if err != nil {
		t.Fatal(err)
	}
	otherTok, err := us.createUserSession("alice", SessionMeta{CreatedVia: "password"})
	if err != nil {
		t.Fatal(err)
	}
	macID := us.sessionIDForToken(macTok)
	phoneTok, err := us.createUserSession("alice", SessionMeta{CreatedVia: "pairing", MintedBy: macID})
	if err != nil {
		t.Fatal(err)
	}
	// The phone paired a second device in turn.
	tabletTok, err := us.createUserSession("alice", SessionMeta{CreatedVia: "pairing", MintedBy: us.sessionIDForToken(phoneTok)})
	if err != nil {
		t.Fatal(err)
	}

	// Signing out the paired phone itself leaves the Mac alone (unchanged).
	phone2, _ := us.createUserSession("alice", SessionMeta{CreatedVia: "pairing", MintedBy: macID})
	if err := us.RevokeSession(phone2); err != nil {
		t.Fatal(err)
	}
	if us.userForSessionToken(macTok) == nil {
		t.Fatal("signing out a paired session signed out the one that paired it")
	}

	// Signing out the Mac revokes what it paired, transitively, and closes their sockets.
	if err := us.RevokeSession(macTok); err != nil {
		t.Fatal(err)
	}
	for name, tok := range map[string]string{"phone": phoneTok, "tablet": tabletTok} {
		if us.userForSessionToken(tok) != nil {
			t.Errorf("%s session survived the revocation of the session that paired it", name)
		}
		var n int
		if err := db.GetDB().QueryRow(`SELECT COUNT(*) FROM user_sessions WHERE token = ?`, database.HashToken(tok)).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s session is still stored (n=%d, err=%v)", name, n, err)
		}
	}
	if len(closed) != 2 {
		t.Errorf("closed sockets of %d revoked sessions, want 2", len(closed))
	}
	if us.userForSessionToken(otherTok) == nil {
		t.Error("an unrelated session was revoked")
	}

	// The link survives a restart (it is stored, not only in memory).
	macTok2, _ := us.createUserSession("alice", SessionMeta{CreatedVia: "password"})
	phoneTok2, _ := us.createUserSession("alice", SessionMeta{CreatedVia: "pairing", MintedBy: us.sessionIDForToken(macTok2)})
	restarted := NewUserStore(t.TempDir())
	restarted.SetDatabase(db)
	if err := restarted.Initialize(); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RevokeSession(macTok2); err != nil {
		t.Fatal(err)
	}
	if restarted.userForSessionToken(phoneTok2) != nil {
		t.Fatal("after a restart, the paired session outlived the session that paired it")
	}
}

// TestRedTeam_PairingCodeMadeBeforeRevocationIsDeadAfterIt: a code made by a
// session and redeemed only after that session was signed out used to mint a
// fresh session.
func TestRedTeam_PairingCodeMadeBeforeRevocationIsDeadAfterIt(t *testing.T) {
	db := NewTestDB(t)
	us := NewUserStore(t.TempDir())
	us.SetDatabase(db)
	if err := us.Initialize(); err != nil {
		t.Fatal(err)
	}
	if err := us.CreateUser("alice", "password123", true); err != nil {
		t.Fatal(err)
	}
	macTok, err := us.createUserSession("alice", SessionMeta{CreatedVia: "password"})
	if err != nil {
		t.Fatal(err)
	}
	macID := us.sessionIDForToken(macTok)
	if !us.sessionAlive(macID) {
		t.Fatal("a live session is not alive")
	}

	repo := database.NewPairingRepository(db)
	now := time.Now()
	code := &database.PairingCode{ID: "p1", CodeHash: database.HashToken("the-code"), Username: "alice",
		CreatedBySession: macID, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	if err := repo.Create(code); err != nil {
		t.Fatal(err)
	}

	if err := us.RevokeSession(macTok); err != nil {
		t.Fatal(err)
	}
	if us.sessionAlive(macID) {
		t.Fatal("the minter is still alive after it was revoked")
	}
	// The unused code was revoked with it: claiming fails.
	if _, err := repo.Claim(database.HashToken("the-code"), "iPhone", time.Now()); err == nil {
		t.Fatal("a pairing code made before the revocation could still be claimed")
	}
}

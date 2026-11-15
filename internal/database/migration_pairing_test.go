package database

import (
	"sync"
	"testing"
	"time"
)

func TestPairingMigrationAndClaimOnce(t *testing.T) {
	db, _ := newReviewTestDB(t)
	for _, col := range []string{"session_id", "device_name", "device_model", "created_via", "last_used_at"} {
		if !columnExists(t, db, "user_sessions", col) {
			t.Errorf("user_sessions.%s missing", col)
		}
	}
	if !columnExists(t, db, "pairing_codes", "code_hash") {
		t.Fatal("pairing_codes missing")
	}
	mustExec(t, db, `INSERT INTO users (username, password_hash) VALUES ('alice', 'x')`)

	repo := NewPairingRepository(db)
	now := time.Now()
	p := &PairingCode{ID: "p1", CodeHash: HashToken("code"), Username: "alice", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := repo.Create(p); err != nil {
		t.Fatal(err)
	}

	// Racing claims: exactly one wins.
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := repo.Claim(HashToken("code"), "phone", time.Now()); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d concurrent claims succeeded, want 1", wins)
	}
	got, _ := repo.Get("p1")
	if got == nil || got.Status(time.Now()) != "redeemed" || got.RedeemedDeviceName != "phone" {
		t.Fatalf("after claim: %+v", got)
	}
}

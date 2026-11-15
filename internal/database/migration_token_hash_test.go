package database

import (
	"strings"
	"testing"
	"time"
)

func TestHashToken(t *testing.T) {
	h := HashToken("abc")
	if len(h) != 64 || strings.Contains(h, "abc") {
		t.Fatalf("HashToken = %q", h)
	}
	if HashToken("abc") != h || HashToken("abd") == h {
		t.Fatal("HashToken must be deterministic and distinguish tokens")
	}
}

// TestHashBearerTokensMigration simulates an install from before migration 49:
// plaintext tokens in user_sessions, mfa_temporary_tokens and agent_handovers
// are hashed in place, and lookups by the plaintext token keep working.
func TestHashBearerTokensMigration(t *testing.T) {
	ResetInstance()
	db, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ResetInstance()

	future := time.Now().Add(time.Hour)
	mustExec(t, db, `INSERT INTO users (username, password_hash, created_at, updated_at) VALUES ('alice', 'x', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO agent_sessions (id) VALUES ('s1')`)
	if _, err := db.db.Exec(`INSERT INTO user_sessions (token, username, expires_at, created_at) VALUES (?, 'alice', ?, CURRENT_TIMESTAMP)`, "plain-session", future); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO mfa_temporary_tokens (token, username, totp_secret, expires_at, ip_address, user_agent) VALUES (?, 'alice', '', ?, '', '')`, "plain-mfa", future); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO agent_handovers (handover_token, source_session_id, handover_data, handover_note, expires_at) VALUES (?, 's1', '{}', '', ?)`, "plain-handover", future); err != nil {
		t.Fatal(err)
	}

	mustExec(t, db, `DELETE FROM schema_migrations WHERE version = 49`)
	if err := runMigrations(db.db); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ table, column, plain string }{
		{"user_sessions", "token", "plain-session"},
		{"mfa_temporary_tokens", "token", "plain-mfa"},
		{"agent_handovers", "handover_token", "plain-handover"},
	} {
		var stored string
		if err := db.db.QueryRow(`SELECT ` + c.column + ` FROM ` + c.table).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != HashToken(c.plain) {
			t.Errorf("%s.%s = %q, want the hash of the token", c.table, c.column, stored)
		}
	}

	repo := NewRepository(db)

	// The user stays logged in: the server looks the session up by hash.
	sessions, err := repo.ListValidSessions()
	if err != nil || len(sessions) != 1 || sessions[0].Token != HashToken("plain-session") {
		t.Fatalf("sessions after migration = %+v, %v", sessions, err)
	}

	mfa, err := repo.GetMFATemporaryToken("plain-mfa")
	if err != nil || mfa == nil || mfa.Username != "alice" || mfa.Token != "plain-mfa" {
		t.Fatalf("MFA token lookup after migration = %+v, %v", mfa, err)
	}
	if got, _ := repo.GetMFATemporaryToken(HashToken("plain-mfa")); got != nil {
		t.Fatal("the stored hash must not work as a token")
	}

	h, err := repo.GetHandoverByToken("plain-handover")
	if err != nil || h.SourceSessionID != "s1" || h.HandoverToken != "plain-handover" {
		t.Fatalf("handover lookup after migration = %+v, %v", h, err)
	}
	if _, err := repo.GetHandoverByToken(HashToken("plain-handover")); err == nil {
		t.Fatal("the stored hash must not work as a handover token")
	}
	if err := repo.ConsumeHandover("plain-handover", "s1"); err != nil {
		t.Fatalf("consume by plaintext token: %v", err)
	}
}

// New MFA and handover tokens are written hashed and found by the plaintext.
func TestBearerTokensStoredHashed(t *testing.T) {
	ResetInstance()
	db, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ResetInstance()
	mustExec(t, db, `INSERT INTO users (username, password_hash, created_at, updated_at) VALUES ('bob', 'x', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO agent_sessions (id) VALUES ('s2')`)
	repo := NewRepository(db)

	tok := &MFATemporaryToken{Token: "mfa-new", Username: "bob", TokenType: "login", MaxAttempts: 5, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}
	if err := repo.SaveMFATemporaryToken(tok); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetMFATemporaryToken("mfa-new")
	if err != nil || got == nil {
		t.Fatalf("lookup: %+v %v", got, err)
	}
	got.Attempts = 2
	if err := repo.UpdateMFATemporaryToken(got); err != nil {
		t.Fatal(err)
	}
	if again, _ := repo.GetMFATemporaryToken("mfa-new"); again == nil || again.Attempts != 2 {
		t.Fatalf("update by plaintext token lost: %+v", again)
	}
	if err := repo.DeleteMFATemporaryToken("mfa-new"); err != nil {
		t.Fatal(err)
	}
	if gone, _ := repo.GetMFATemporaryToken("mfa-new"); gone != nil {
		t.Fatal("delete by plaintext token did not delete")
	}

	if err := repo.CreateHandover(&AgentHandover{HandoverToken: "ho-new", SourceSessionID: "s2", HandoverData: "{}", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	var n int
	db.db.QueryRow(`SELECT COUNT(*) FROM agent_handovers WHERE handover_token = 'ho-new'`).Scan(&n)
	if n != 0 {
		t.Fatal("handover token stored in plaintext")
	}
	if _, err := repo.GetHandoverByToken("ho-new"); err != nil {
		t.Fatal(err)
	}
}

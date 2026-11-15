package database

import (
	"testing"
	"time"
)

func tableExists(t *testing.T, db *Database, name string) bool {
	t.Helper()
	var n int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// TestN0OAuthMigrationFreshDB verifies a fresh database has the n0 OAuth
// tables and the n0 catalog entry uses OAuth.
func TestN0OAuthMigrationFreshDB(t *testing.T) {
	ResetInstance()
	db, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ResetInstance()

	for _, tbl := range []string{"n0_oauth_clients", "n0_connections", "n0_session_bindings"} {
		if !tableExists(t, db, tbl) {
			t.Errorf("table %s missing", tbl)
		}
	}
	var authType, desc string
	if err := db.db.QueryRow(`SELECT auth_type, description FROM connector_definitions WHERE slug='n0'`).Scan(&authType, &desc); err != nil {
		t.Fatal(err)
	}
	if authType != "oauth2" || desc != n0OAuthDescription {
		t.Errorf("n0 catalog = %q / %q", authType, desc)
	}
	var applied int
	db.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 45`).Scan(&applied)
	if applied != 1 {
		t.Error("migration 45 not recorded")
	}
}

// TestN0OAuthMigrationIdempotent re-runs the migration on a migrated DB.
func TestN0OAuthMigrationIdempotent(t *testing.T) {
	ResetInstance()
	db, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ResetInstance()
	repo := NewN0Repository(db)
	c := &N0Connection{Host: "https://nzero.pro", Issuer: "i", Resource: "r", ClientID: "c", RedirectURI: "u", TokenEndpoint: "t"}
	if err := repo.CreateN0Connection(c); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		tx, err := db.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateN0OAuth(tx); err != nil {
			tx.Rollback()
			t.Fatalf("re-run %d: %v", i, err)
		}
		tx.Commit()
	}
	if n, _ := repo.CountN0Connections(); n != 1 {
		t.Fatalf("re-running the migration lost data: %d connections", n)
	}
}

// TestN0OAuthMigrationUpgradePath simulates an install from before the
// migration that has a legacy n0 API-key connection.
func TestN0OAuthMigrationUpgradePath(t *testing.T) {
	ResetInstance()
	db, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ResetInstance()
	for _, stmt := range []string{
		`DROP TABLE n0_session_bindings`, `DROP TABLE n0_connections`, `DROP TABLE n0_oauth_clients`,
		`DELETE FROM schema_migrations WHERE version = 45`,
		`UPDATE connector_definitions SET auth_type = 'api_key', description = 'old' WHERE slug = 'n0'`,
		`INSERT INTO user_connections (connector_slug, api_key, extra_config, status) VALUES ('n0', 'enc:legacy', '{"domain":"nzero.pro"}', 'active')`,
	} {
		if _, err := db.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := runMigrations(db.db); err != nil {
		t.Fatal(err)
	}
	if !tableExists(t, db, "n0_connections") {
		t.Fatal("tables not created on upgrade")
	}
	var authType, key string
	db.db.QueryRow(`SELECT auth_type FROM connector_definitions WHERE slug='n0'`).Scan(&authType)
	db.db.QueryRow(`SELECT api_key FROM user_connections WHERE connector_slug='n0'`).Scan(&key)
	if authType != "oauth2" || key != "enc:legacy" {
		t.Fatalf("auth_type=%q legacy key=%q", authType, key)
	}
}

func TestN0RepositoryLeaseAndBindings(t *testing.T) {
	ResetInstance()
	db, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ResetInstance()
	repo := NewN0Repository(db)
	c := &N0Connection{Host: "h", Issuer: "i", Resource: "r", ClientID: "c", RedirectURI: "u", TokenEndpoint: "t"}
	if err := repo.CreateN0Connection(c); err != nil {
		t.Fatal(err)
	}
	ok, _ := repo.AcquireN0RefreshLease(c.ID, "a", time.Minute)
	if !ok {
		t.Fatal("first lease refused")
	}
	if ok, _ := repo.AcquireN0RefreshLease(c.ID, "b", time.Minute); ok {
		t.Fatal("second owner got a held lease")
	}
	// Storing the rotated tokens releases the lease.
	if err := repo.UpdateN0Tokens(c.ID, "enc:a", "enc:r", time.Now().Add(time.Hour), "app:read", "a"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := repo.AcquireN0RefreshLease(c.ID, "b", time.Minute); !ok {
		t.Fatal("lease not released after token update")
	}
	// Expired leases can be taken over.
	repo.ReleaseN0RefreshLease(c.ID, "b")
	repo.AcquireN0RefreshLease(c.ID, "x", -time.Second)
	if ok, _ := repo.AcquireN0RefreshLease(c.ID, "y", time.Minute); !ok {
		t.Fatal("expired lease not reclaimable")
	}

	db.db.Exec(`INSERT INTO agent_sessions (id, status) VALUES ('s1', 'idle')`)
	if err := repo.BindN0Session("s1", c.ID); err != nil {
		t.Fatal(err)
	}
	if id, _ := repo.GetN0SessionBinding("s1"); id != c.ID {
		t.Fatal("binding not stored")
	}
	if err := repo.DeleteN0Connection(c.ID); err != nil {
		t.Fatal(err)
	}
	if id, _ := repo.GetN0SessionBinding("s1"); id != 0 {
		t.Fatal("binding survived connection delete")
	}

	// Marker row lifecycle.
	repo.CreateN0Connection(&N0Connection{Host: "h", Issuer: "i", Resource: "r", ClientID: "c", RedirectURI: "u", TokenEndpoint: "t"})
	if err := repo.EnsureN0ConnectorMarker(); err != nil {
		t.Fatal(err)
	}
	var n int
	db.db.QueryRow(`SELECT COUNT(*) FROM user_connections WHERE connector_slug='n0' AND api_key IS NULL`).Scan(&n)
	if n != 1 {
		t.Fatal("marker not created")
	}
	db.db.Exec(`DELETE FROM n0_connections`)
	repo.RemoveN0ConnectorMarkerIfUnused()
	db.db.QueryRow(`SELECT COUNT(*) FROM user_connections WHERE connector_slug='n0'`).Scan(&n)
	if n != 0 {
		t.Fatal("marker not removed")
	}
}

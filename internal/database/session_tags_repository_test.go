package database

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeSessionTags(t *testing.T) {
	long := strings.Repeat("x", 50)
	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, []string{}},
		{"trim and collapse", []string{"  bug   fix ", "api"}, []string{"bug fix", "api"}},
		{"blanks and case duplicates", []string{"", "  ", "Auth", "auth", "AUTH "}, []string{"Auth"}},
		{"commas split like the web", []string{"a, b ,c", "B"}, []string{"a", "b", "c"}},
		{"long tags cut", []string{long}, []string{strings.Repeat("x", MaxSessionTagLength)}},
		{"at most 8", []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}, []string{"1", "2", "3", "4", "5", "6", "7", "8"}},
		{"runes, not bytes", []string{strings.Repeat("æ", 45)}, []string{strings.Repeat("æ", 40)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeSessionTags(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSessionTagsRoundTripAndDeleteWithSession(t *testing.T) {
	db := NewTestDB(t)
	repo := NewRepository(db)
	mustExec(t, db, `INSERT INTO agent_sessions (id) VALUES ('s1')`)
	mustExec(t, db, `INSERT INTO agent_sessions (id) VALUES ('s2')`)

	got, err := repo.SetSessionTags("s1", []string{" Auth ", "auth", "api"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"Auth", "api"}) {
		t.Fatalf("stored %q", got)
	}
	if _, err := repo.SetSessionTags("s2", []string{"docs"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetSessionTags("missing", []string{"x"}); err == nil {
		t.Fatal("tags for an unknown session were stored")
	}

	tags, err := repo.GetSessionTags([]string{"s1", "s2", "other"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"s1": {"Auth", "api"}, "s2": {"docs"}}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("got %v, want %v", tags, want)
	}

	// An empty list removes the row.
	if got, err := repo.SetSessionTags("s2", []string{" ", ""}); err != nil || len(got) != 0 {
		t.Fatalf("clear: %q %v", got, err)
	}
	if n := count(t, db, "session_tags"); n != 1 {
		t.Fatalf("session_tags has %d rows after clearing, want 1", n)
	}

	// Tags go with their session.
	mustExec(t, db, `DELETE FROM agent_sessions WHERE id = 's1'`)
	if n := count(t, db, "session_tags"); n != 0 {
		t.Fatalf("session_tags has %d rows after the session was deleted", n)
	}
	all, err := repo.GetAllSessionTags()
	if err != nil || len(all) != 0 {
		t.Fatalf("all tags: %v %v", all, err)
	}
}

// Migration 47 lists session_tags as a session child table, but on an older
// database it runs before migration 59 creates it.
func TestOrphanMigrationRunsBeforeSessionTagsExist(t *testing.T) {
	db := NewTestDB(t)
	mustExec(t, db, `DROP TABLE session_tags`)
	mustExec(t, db, `DELETE FROM schema_migrations WHERE version IN (47, 59)`)
	if err := runMigrations(db.db); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, "session_tags"); n != 0 {
		t.Fatalf("session_tags: %d rows", n)
	}
}

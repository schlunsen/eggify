package agents

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
)

// createSessionFromWire decodes a create_session message the way the
// WebSocket handler does and creates the session.
func createSessionFromWire(t *testing.T, sm *SessionManager, raw string) uuid.UUID {
	t.Helper()
	var msg CreateSessionMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("decode create_session: %v", err)
	}
	msg.SessionID = uuid.New()
	if _, err := sm.CreateSession(msg.SessionID, msg.Options); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return msg.SessionID
}

func sessionConnectorSlugs(t *testing.T, id uuid.UUID) []string {
	t.Helper()
	slugs, err := database.NewConnectorRepository(database.GetInstance()).GetSessionConnectorSlugs(id.String())
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(slugs)
	return slugs
}

// An explicit empty "connectors" list (Create Session with nothing selected)
// enables no connectors; a missing field keeps the old "all active" default.
func TestCreateSession_ExplicitEmptyConnectorsVsMissing(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()

	repo := database.NewConnectorRepository(database.GetInstance())
	for _, slug := range []string{"anthropic", "openai"} {
		key := "enc-" + slug
		if err := repo.SaveConnection(&database.UserConnection{ConnectorSlug: slug, APIKey: &key, Status: "active"}); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"missing field enables all active", `{"type":"create_session","options":{}}`, []string{"anthropic", "openai"}},
		{"null enables all active", `{"type":"create_session","options":{"connectors":null}}`, []string{"anthropic", "openai"}},
		{"explicit empty enables none", `{"type":"create_session","options":{"connectors":[]}}`, nil},
		{"explicit list enables only those", `{"type":"create_session","options":{"connectors":["openai","n0"]}}`, []string{"openai"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := createSessionFromWire(t, sm, tc.raw)
			got := sessionConnectorSlugs(t, id)
			if len(got) != len(tc.want) {
				t.Fatalf("connectors = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("connectors = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// The explicit empty list survives the options JSON round trip (DB, handover
// packages, auto-handoff), so a later copy of the session doesn't fall back
// to enabling every connector.
func TestSessionOptions_EmptyConnectorsSurviveJSON(t *testing.T) {
	for name, in := range map[string]SessionOptions{
		"empty":   {Connectors: []string{}},
		"missing": {},
	} {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var out SessionOptions
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		if (in.Connectors == nil) != (out.Connectors == nil) {
			t.Fatalf("%s: connectors nil-ness lost in %s", name, b)
		}
	}
}

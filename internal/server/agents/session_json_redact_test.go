package agents

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRedTeam_SessionJSONNeverCarriesTheAPIKey: GET /api/agent/sessions,
// session_updated and session_created serialise Session; a site-generator
// session holds the stored provider key in its options.
func TestRedTeam_SessionJSONNeverCarriesTheAPIKey(t *testing.T) {
	key := "sk-live-secret-provider-key"
	base := "https://user:pass@api.example.com/v1"
	s := Session{Options: SessionOptions{APIKey: &key, BaseURL: &base}}

	for name, v := range map[string]any{
		"session":         s,
		"agent session":   &AgentSession{Session: s},
		"list":            []Session{s},
		"session_created": SessionCreatedMessage{Session: s},
		"map":             map[string]any{"session": s},
	} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(string(b), key) || strings.Contains(string(b), "pass@") {
			t.Errorf("%s leaks credentials: %s", name, b)
		}
		if !strings.Contains(string(b), "api.example.com") {
			t.Errorf("%s lost the base URL: %s", name, b)
		}
	}

	// Storage marshals the options themselves and must keep the key.
	b, _ := json.Marshal(s.Options)
	if !strings.Contains(string(b), key) {
		t.Fatalf("stored options lost the key: %s", b)
	}

	// Stored metadata (GET /api/projects/:id/sessions) carries the raw options.
	raw, _ := json.Marshal(s.Options)
	meta := &SessionMetadata{OptionsJSON: string(raw)}
	b, err := json.Marshal([]*SessionMetadata{meta})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), key) || strings.Contains(string(b), "pass@") {
		t.Fatalf("session metadata leaks credentials: %s", b)
	}
}

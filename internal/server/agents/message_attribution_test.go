package agents

import (
	"testing"

	"github.com/google/uuid"
)

func TestMessageAttributionAcrossSwitchesAndPagination(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	session := seedSwitchSession(t, sm, "codex", "gpt-6-astra", nil)
	seedMessages(t, sm, session, [2]string{"assistant", "Codex reply"})
	if _, err := sm.ChangeSessionModel(session.ID, "claude", "opus", nil); err != nil {
		t.Fatal(err)
	}
	if err := sm.saveMessageToDB(session.ID, 3, "assistant", "Claude reply", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	session.MessageCount = 3
	if _, err := sm.ChangeSessionModel(session.ID, "codex", "gpt-6-astra", nil); err != nil {
		t.Fatal(err)
	}
	if err := sm.saveMessageToDB(session.ID, 5, "assistant", "Back to Codex", "", nil, nil); err != nil {
		t.Fatal(err)
	}

	// SDK replay/content enrichment must not overwrite the original model.
	if err := sm.saveMessageToDB(session.ID, 3, "assistant", "Claude reply with richer content", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T) {
		t.Helper()
		// Re-open the storage facade, as on reload; no in-memory session state.
		storage, err := NewSQLiteSessionStorage(sm.db)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			sequence        int
			provider, model string
		}{{1, "codex", "gpt-6-astra"}, {3, "claude", "opus"}, {5, "codex", "gpt-6-astra"}} {
			messages, _, err := storage.GetLatestMessages(session.ID, 1, tc.sequence+1)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 || messages[0].Sequence != tc.sequence {
				t.Fatalf("unexpected page: %+v", messages)
			}
			msg := messages[0]
			if msg.Provider != tc.provider || msg.ModelName != tc.model {
				t.Fatalf("sequence %d: got %s/%s, want %s/%s", tc.sequence, msg.Provider, msg.ModelName, tc.provider, tc.model)
			}
			api := msg.ToAPIResponse()
			if api["model_name"] != tc.model || api["provider"] != tc.provider {
				t.Fatalf("API lost attribution: %+v", api)
			}
			// Clients page backwards with before_sequence, so the row must carry it.
			if api["sequence"] != tc.sequence {
				t.Fatalf("API row has sequence %v, want %d", api["sequence"], tc.sequence)
			}
			oldPage, _, err := storage.GetMessages(session.ID, 1, tc.sequence-1)
			if err != nil {
				t.Fatal(err)
			}
			if len(oldPage) != 1 || oldPage[0].ModelName != tc.model {
				t.Fatalf("offset page lost attribution: %+v", oldPage)
			}
		}
	}
	t.Run("persisted snapshots", check)
	// Simulate messages written by old Eggify releases, without attribution columns.
	if _, err := sm.db.Exec(`UPDATE agent_messages SET provider = NULL, model_name = NULL WHERE session_id = ?`, session.ID.String()); err != nil {
		t.Fatal(err)
	}
	t.Run("legacy switch notes", check)
}

func TestMessageAttributionSnapshotOverridesLegacyTimeline(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	session := seedSwitchSession(t, sm, "claude", "opus", nil)
	if err := sm.Storage.SaveMessage(&MessageRecord{ID: uuid.New(), SessionID: session.ID, Sequence: 1, Role: "assistant", Content: "reply", Provider: "codex", ModelName: "gpt-6-astra"}); err != nil {
		t.Fatal(err)
	}
	messages, _, err := sm.Storage.GetLatestMessages(session.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ModelName != "gpt-6-astra" || messages[0].Provider != "codex" {
		t.Fatalf("snapshot replaced by session model: %+v", messages)
	}
}

func TestParseModelSwitchNote(t *testing.T) {
	before, after, ok := parseModelSwitchNote("🔀 Switched model: codex/gpt-6-astra → openrouter/anthropic/claude-opus")
	if !ok || before.model != "gpt-6-astra" || after.provider != "openrouter" || after.model != "anthropic/claude-opus" {
		t.Fatalf("incorrect parse: %+v %+v %v", before, after, ok)
	}
	for _, content := range []string{"ordinary text", "🔀 Switched model: missing arrow", "🔀 Switched model: codex/ → claude/opus"} {
		if _, _, ok := parseModelSwitchNote(content); ok {
			t.Fatalf("accepted malformed note: %q", content)
		}
	}
}

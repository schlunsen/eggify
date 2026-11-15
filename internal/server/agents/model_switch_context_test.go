package agents

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestModelSwitchContextSnapshot(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	s := seedSwitchSession(t, sm, "codex", "gpt-6-astra", nil)
	seedMessages(t, sm, s, [2]string{"assistant", "Original answer"})
	change, err := sm.ChangeSessionModel(s.ID, "claude", "opus", nil)
	if err != nil {
		t.Fatal(err)
	}
	get := func(want string) *ModelSwitchContext {
		t.Helper()
		result, err := sm.GetModelSwitchContext(s.ID, change.MessageID)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != want {
			t.Fatalf("got status %s, want %s", result.Status, want)
		}
		return result
	}
	// A preview is recorded at switch time so the handoff is inspectable
	// before the next message is sent.
	if got := get("preview"); got.Context == "" {
		t.Fatal("no preview recorded at switch time")
	}
	if change.Sequence != 2 {
		t.Fatalf("wrong notice sequence %d", change.Sequence)
	}
	prefix := sm.historyReplayPrefix(s, 3)
	sm.captureModelSwitchContext(s.ID, 3, prefix, "text")
	if got := get("sent"); got.Context != prefix || got.Format != "text" {
		t.Fatalf("snapshot differs from transmitted context: %+v", got)
	}
	// Neither a later turn nor switching again may replace the snapshot.
	sm.captureModelSwitchContext(s.ID, 4, "later context", "text")
	if _, err := sm.ChangeSessionModel(s.ID, "codex", "gpt-6-astra", nil); err != nil {
		t.Fatal(err)
	}
	if got := get("sent"); got.Context != prefix {
		t.Fatal("snapshot mutated")
	}
	if _, err := sm.GetModelSwitchContext(uuid.New(), change.MessageID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-session read allowed: %v", err)
	}
}

func TestModelSwitchContextStates(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	for _, tc := range []struct {
		name, want, provider, model string
		messages                    bool
		native                      bool
	}{
		{"empty", "empty", "codex", "gpt-6-astra", false, false},
		{"native resume", "resumed", "claude", "opus", true, true},
		{"direct provider", "preview", "custom", "qwen", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := seedSwitchSession(t, sm, "claude", "sonnet", func(s *Session) {
				if tc.native {
					s.ClaudeSessionID = "native-thread"
				}
			})
			if tc.messages {
				seedMessages(t, sm, s, [2]string{"user", "hello"})
			}
			change, err := sm.ChangeSessionModel(s.ID, tc.provider, tc.model, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := sm.GetModelSwitchContext(s.ID, change.MessageID)
			if err != nil || got.Status != tc.want {
				t.Fatalf("got %+v, %v", got, err)
			}
			if tc.provider == "custom" {
				sm.captureModelSwitchContext(s.ID, 0, `[{"role":"user","content":"hello"}]`, "json")
				got, err = sm.GetModelSwitchContext(s.ID, change.MessageID)
				if err != nil || got.Status != "sent" || got.Format != "json" {
					t.Fatalf("got %+v, %v", got, err)
				}
			}
		})
	}
	s := seedSwitchSession(t, sm, "codex", "gpt-6-astra", nil)
	seedMessages(t, sm, s, [2]string{"assistant", "Hello"})
	first, err := sm.ChangeSessionModel(s.ID, "claude", "opus", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.ChangeSessionModel(s.ID, "codex", "gpt-6-astra", nil); err != nil {
		t.Fatal(err)
	}
	got, err := sm.GetModelSwitchContext(s.ID, first.MessageID)
	if err != nil || got.Status != "superseded" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := sm.db.Exec(`DELETE FROM agent_model_switches WHERE message_id = ?`, first.MessageID.String()); err != nil {
		t.Fatal(err)
	}
	got, err = sm.GetModelSwitchContext(s.ID, first.MessageID)
	if err != nil || got.Status != "unavailable" {
		t.Fatalf("legacy notice: %+v, %v", got, err)
	}
}

func TestModelSwitchPersistenceFailureIsAtomic(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	s := seedSwitchSession(t, sm, "codex", "gpt-6-astra", nil)
	if _, err := sm.db.Exec(`CREATE TRIGGER reject_switch BEFORE INSERT ON agent_model_switches BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.ChangeSessionModel(s.ID, "claude", "opus", nil); err == nil {
		t.Fatal("expected persistence failure")
	}
	if s.ModelName != "gpt-6-astra" || s.MessageCount != 0 {
		t.Fatal("live session changed after failed switch")
	}
	stored, err := sm.Storage.GetSession(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ModelName != "gpt-6-astra" {
		t.Fatal("persisted session changed after failed switch")
	}
	count, err := sm.Storage.GetMessageCount(s.ID)
	if err != nil || count != 0 {
		t.Fatalf("false switch notice persisted: count=%d err=%v", count, err)
	}
}

// The provisional preview must give way to the payload actually transmitted,
// and only that payload is then immutable.
func TestModelSwitchContextPreviewReplacedBySentPayload(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	s := seedSwitchSession(t, sm, "codex", "gpt-6-astra", nil)
	seedMessages(t, sm, s, [2]string{"assistant", "Original answer"})
	change, err := sm.ChangeSessionModel(s.ID, "claude", "opus", nil)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := sm.GetModelSwitchContext(s.ID, change.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Status != "preview" || preview.Context == "" {
		t.Fatalf("expected a preview at switch time, got %+v", preview)
	}

	sent := sm.historyReplayPrefix(s, 3)
	sm.captureModelSwitchContext(s.ID, 3, sent, "text")
	got, err := sm.GetModelSwitchContext(s.ID, change.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "sent" || got.Context != sent {
		t.Fatalf("preview was not replaced by the transmitted payload: %+v", got)
	}

	sm.captureModelSwitchContext(s.ID, 4, "a later turn", "text")
	if after, err := sm.GetModelSwitchContext(s.ID, change.MessageID); err != nil || after.Context != sent {
		t.Fatalf("transmitted payload mutated: %+v, %v", after, err)
	}
}

// A same-provider switch resumes the provider's own transcript, so nothing is
// "sent" - but the conversation the new model picks up must still be viewable.
func TestModelSwitchContextResumedCarriesTranscript(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	s := seedSwitchSession(t, sm, "claude", "opus", func(s *Session) {
		s.ClaudeSessionID = "native-thread"
	})
	seedMessages(t, sm, s, [2]string{"assistant", "Earlier answer"})
	change, err := sm.ChangeSessionModel(s.ID, "claude", "haiku", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := sm.GetModelSwitchContext(s.ID, change.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "resumed" {
		t.Fatalf("a same-provider switch must stay resumed, got %q", got.Status)
	}
	if !strings.Contains(got.Context, "Earlier answer") {
		t.Fatalf("resumed handoff hid the conversation: %+v", got)
	}
	// A later turn must not relabel the resumed conversation as a sent payload.
	sm.captureModelSwitchContext(s.ID, 0, "a later turn", "text")
	after, err := sm.GetModelSwitchContext(s.ID, change.MessageID)
	if err != nil || after.Status != "resumed" || strings.Contains(after.Context, "a later turn") {
		t.Fatalf("resumed switch overwritten by a later turn: %+v, %v", after, err)
	}
}

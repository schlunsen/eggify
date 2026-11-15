package agents

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/llmclient"
)

func TestTruncateMiddle(t *testing.T) {
	if got := truncateMiddle("short", 100); got != "short" {
		t.Fatalf("short text changed: %q", got)
	}
	long := strings.Repeat("a", 500) + "ERROR at the end"
	got := truncateMiddle(long, 200)
	if utf8.RuneCountInString(got) > 200 {
		t.Fatalf("truncated to %d runes, want <= 200", utf8.RuneCountInString(got))
	}
	if !strings.HasPrefix(got, "aaa") || !strings.HasSuffix(got, "ERROR at the end") || !strings.Contains(got, "characters omitted") {
		t.Fatalf("must keep head and tail with a marker: %q", got)
	}
	multi := strings.Repeat("æøå", 300)
	if out := truncateMiddle(multi, 120); !utf8.ValidString(out) {
		t.Fatal("must not split multi-byte runes")
	}
}

func TestTrimOldToolResults_KeepsNewest(t *testing.T) {
	big := strings.Repeat("x", 5000)
	var msgs []llmclient.ChatMessage
	for i := 0; i < 5; i++ {
		msgs = append(msgs, llmclient.ChatMessage{Role: "assistant"}, llmclient.ChatMessage{Role: "tool", Content: big})
	}
	trimOldToolResults(msgs, 2, 1000)
	for i, m := range msgs {
		if m.Role != "tool" {
			continue
		}
		n := utf8.RuneCountInString(m.Content.(string))
		recent := i >= len(msgs)-4 // last two tool messages
		if recent && n != 5000 {
			t.Errorf("recent result %d trimmed to %d", i, n)
		}
		if !recent && n > 1000 {
			t.Errorf("old result %d kept %d runes", i, n)
		}
	}
}

// The bug this fixes: results were never stored, so the next prompt replayed
// "(tool result not available from history)" for every earlier tool call.
func TestDirectToolResult_StoredAndReplayed(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	s := seedSwitchSession(t, sm, "litellm", "qwen3.8", nil)
	seedMessages(t, sm, s, [2]string{"user", "list the files"})

	call := &types.AssistantMessage{Type: "assistant", Content: []types.ContentBlock{
		&types.TextBlock{Type: "text", Text: "Looking."},
		&types.ToolUseBlock{Type: "tool_use", ID: "call_1", Name: "Bash", Input: map[string]interface{}{"command": "ls"}},
		&types.ToolUseBlock{Type: "tool_use", ID: "call_2", Name: "Bash", Input: map[string]interface{}{"command": "pwd"}},
	}}
	s.MessageCount++
	if _, err := sm.persistSDKMessage(s.ID, s.MessageCount, call); err != nil {
		t.Fatal(err)
	}

	huge := strings.Repeat("y", directStoredResultMaxChars*2)
	if err := sm.saveDirectToolResult(s.ID, "call_1", "go.mod\nmain.go", false); err != nil {
		t.Fatal(err)
	}
	if err := sm.saveDirectToolResult(s.ID, "call_2", huge, true); err != nil {
		t.Fatal(err)
	}
	if err := sm.saveDirectToolResult(s.ID, "call_missing", "x", false); err == nil {
		t.Fatal("an unknown call id must be reported")
	}

	var raw string
	if err := sm.db.QueryRow(`SELECT tool_uses FROM agent_messages WHERE session_id = ? AND role = 'assistant'`, s.ID.String()).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var stored []map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatal(err)
	}
	if stored[0]["result"] != "go.mod\nmain.go" || stored[0]["is_error"] != nil {
		t.Fatalf("call_1 stored wrong: %v", stored[0])
	}
	if r, _ := stored[1]["result"].(string); utf8.RuneCountInString(r) > directStoredResultMaxChars || stored[1]["is_error"] != true {
		t.Fatalf("call_2 must be capped and flagged as error (len %d, %v)", len(r), stored[1]["is_error"])
	}

	history, err := sm.buildDirectConversationHistory(s.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var tools []llmclient.ChatMessage
	for _, m := range history {
		if m.Role == "tool" {
			tools = append(tools, m)
		}
	}
	if len(tools) != 2 || tools[0].ToolCallID != "call_1" || tools[0].Content != "go.mod\nmain.go" {
		t.Fatalf("history must replay stored results, got %+v", tools)
	}
	for _, m := range tools {
		if strings.Contains(fmt.Sprint(m.Content), "not available from history") {
			t.Fatal("stored results must replace the placeholder")
		}
	}
}

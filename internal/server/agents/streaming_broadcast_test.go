package agents

import (
	"sync"
	"testing"

	"github.com/schlunsen/claude-agent-sdk-go/types"
)

type countingAnalyticsHub struct {
	mu     sync.Mutex
	events []string
}

func (c *countingAnalyticsHub) BroadcastData(event string, _ interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
}

// A message is converted once and its analytics events fire once per tool
// use, however many connections it is then written to.
func TestBuildAgentMessage_SideEffectsOncePerMessage(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	t.Cleanup(cleanup)
	session := seedSwitchSession(t, sm, "claude", "opus", nil)
	hub := &countingAnalyticsHub{}
	h := &AgentHandler{SessionManager: sm, analyticsHub: hub}

	msg := &types.AssistantMessage{
		Type: "assistant",
		Content: []types.ContentBlock{
			&types.TextBlock{Type: "text", Text: "hello"},
			&types.ThinkingBlock{Type: "thinking", Thinking: "secret reasoning"},
			&types.ToolUseBlock{Type: "tool_use", ID: "t1", Name: "Read", Input: map[string]interface{}{"file_path": "/x"}},
			&types.ToolUseBlock{Type: "tool_use", ID: "t2", Name: "Bash", Input: map[string]interface{}{"command": "ls"}},
		},
	}

	out := h.buildAgentMessage(session.ID, msg)
	if out == nil {
		t.Fatal("assistant message was skipped")
	}
	if out.attributeFromConn {
		t.Error("assistant message must not take per-connection attribution")
	}
	if out.response.ID == "" || out.response.Role != "assistant" {
		t.Fatalf("unexpected response: id=%q role=%q", out.response.ID, out.response.Role)
	}
	if got := len(out.toolUseEvents); got != 2 {
		t.Fatalf("toolUseEvents = %d, want 2", got)
	}
	content := out.response.Content.(map[string]interface{})
	if text := content["text"].([]string); len(text) != 1 || text[0] != "hello" {
		t.Errorf("text = %v", text)
	}
	if tools := content["tools"].([]map[string]interface{}); len(tools) != 2 {
		t.Errorf("tools = %v", tools)
	}

	h.emitAgentMessageSideEffects(session.ID, out)
	if got := len(hub.events); got != 2 {
		t.Fatalf("analytics events = %d, want 2 (one per tool use)", got)
	}
}

func TestBuildAgentMessage_UserAttribution(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	t.Cleanup(cleanup)
	session := seedSwitchSession(t, sm, "claude", "opus", nil)
	h := &AgentHandler{SessionManager: sm}
	msg := &types.UserMessage{Type: "user", Content: "hi"}

	// With an explicit sender the attribution is fixed for every connection.
	out := h.buildAgentMessage(session.ID, msg, "user-uuid", "alice")
	if out == nil || out.attributeFromConn {
		t.Fatalf("explicit sender: out=%+v", out)
	}
	if out.response.UserID == nil || *out.response.UserID != "user-uuid" ||
		out.response.Username == nil || *out.response.Username != "alice" {
		t.Errorf("attribution = %v/%v", out.response.UserID, out.response.Username)
	}

	// Without one it is taken from the receiving connection at write time.
	out = h.buildAgentMessage(session.ID, msg)
	if out == nil || !out.attributeFromConn || out.response.UserID != nil {
		t.Fatalf("no sender: out=%+v", out)
	}
}

func TestUserMessageHasImage(t *testing.T) {
	withImage := &types.UserMessage{Type: "user", Content: []types.ContentBlock{
		&types.ToolResultBlock{Type: "tool_result", ToolUseID: "t", Content: []interface{}{
			map[string]interface{}{"type": "image"},
		}},
	}}
	if !userMessageHasImage(withImage) {
		t.Error("image tool result not detected")
	}
	if userMessageHasImage(&types.UserMessage{Type: "user", Content: "text"}) {
		t.Error("plain text detected as image")
	}
}

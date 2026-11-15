package agents

import (
	"context"
	"testing"

	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/stretchr/testify/require"
)

// Shapes from Claude Code 2.1.311's stream-json schema.
func TestChatSystemText(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{`{"type":"system","subtype":"thinking_tokens","estimated_tokens":120,"estimated_tokens_delta":40}`, ""},
		{`{"type":"system","subtype":"init","cwd":"/tmp","model":"claude-fable-5-2"}`, ""},
		{`{"type":"system","subtype":"status","status":"compacting"}`, ""},
		{`{"type":"system","subtype":"session_state_changed","state":"idle"}`, ""},
		{`{"type":"system","subtype":"post_turn_summary","status_category":"done"}`, ""},
		{`{"type":"system","subtype":"hook_started","hook_name":"x"}`, ""},
		{`{"type":"system","subtype":"informational","content":"debug detail","level":"info"}`, ""},
		{`{"type":"system","subtype":"local_command_output","content":"Total cost: $0.12"}`, "Total cost: $0.12"},
		{`{"type":"system","subtype":"informational","content":"Stop hook blocked","level":"warning"}`, "Stop hook blocked"},
		{`{"type":"system","subtype":"notification","key":"k","text":"Plugin updated","priority":"low"}`, "Plugin updated"},
		{`{"type":"system","subtype":"model_fallback","trigger":"overloaded","original_model":"a","fallback_model":"b","content":""}`, "🔀 Switched to fallback model b"},
		{`{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"auto","pre_tokens":180000,"post_tokens":12000}}`, "🗜️ Context compacted (180.0k → 12.0k tokens)"},
		{`{"type":"system","subtype":"memory_saved","written_paths":["/a"]}`, "🧠 Saved 1 memory"},
	} {
		msg, ok := mustMessage(t, tc.raw).(*types.SystemMessage)
		require.True(t, ok, tc.raw)
		got, show := chatSystemText(msg)
		require.Equal(t, tc.want, got, tc.raw)
		require.Equal(t, tc.want != "", show, tc.raw)
	}
}

// Host-status frames neither reach the chat nor wake an idle session; the
// ones meant for a person are stored as a notice.
func TestReadTurnKeepsSystemFramesOutOfTheChat(t *testing.T) {
	x := newLimitHarness(t)
	s := x.session(t, SessionStatusIdle, x.clock())
	ch := make(chan types.Message, 4)
	ch <- mustMessage(t, `{"type":"system","subtype":"thinking_tokens","estimated_tokens":120,"estimated_tokens_delta":40}`)
	ch <- mustMessage(t, `{"type":"system","subtype":"post_turn_summary","status_category":"done"}`)
	ch <- mustMessage(t, `{"type":"system","subtype":"session_state_changed","state":"idle"}`)
	ch <- mustMessage(t, `{"type":"system","subtype":"local_command_output","content":"Total cost: $0.12"}`)
	close(ch)
	x.sm.readTurn(context.Background(), s, ch)

	require.Equal(t, SessionStatusIdle, s.Status)
	msgs, _, err := x.sm.GetMessages(s.ID, 10, 0)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, "system", msgs[0].Role)
	require.Equal(t, "Total cost: $0.12", msgs[0].Content)
}

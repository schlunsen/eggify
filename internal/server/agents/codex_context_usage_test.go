package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCodexContextUsageRestoredSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	thread := uuid.NewString()
	dir := filepath.Join(home, "sessions", "2026", "09", "20")
	require.NoError(t, os.MkdirAll(dir, 0700))
	path := filepath.Join(dir, "rollout-2026-09-20T10-00-00-"+thread+".jsonl")
	data := `{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":90000},"model_context_window":258400}}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":999999},"last_token_usage":{"total_tokens":12345,"cached_input_tokens":8000},"model_context_window":258400}}}
{"type":"event_msg","payload":{"type":"token_count","info":null}}
{"type":`
	require.NoError(t, os.WriteFile(path, []byte(data), 0600))
	sm := &SessionManager{}
	session := &AgentSession{Session: Session{ModelName: "gpt-6-astra", Options: SessionOptions{CodexThreadID: &thread}}}
	usage := sm.getCodexContextUsage(session)
	require.Equal(t, 12345, usage.TotalTokens)
	require.Equal(t, 258400, usage.MaxTokens)
	require.InDelta(t, 4.7775, usage.Percentage, 0.001)
	// A new thread must not inherit a previous thread's in-memory measurement.
	session.codexContextUsage = usage
	session.codexContextThread = thread
	other := uuid.NewString()
	session.Options.CodexThreadID = &other
	unknown := sm.getCodexContextUsage(session)
	require.Zero(t, unknown.TotalTokens)
	require.Zero(t, unknown.MaxTokens)
}

func TestCodexContextUsageMissingWindow(t *testing.T) {
	require.Nil(t, codexContextReading("gpt-6-astra", 12345, 0))
	require.Nil(t, codexContextReading("gpt-6-astra", -1, 258400))
}

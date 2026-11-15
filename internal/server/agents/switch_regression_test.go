package agents

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/llmclient"
	"github.com/stretchr/testify/require"
)

func TestDirectSwitchPreviewMatchesRecentHistory(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	s := seedSwitchSession(t, sm, "claude", "opus", nil)
	for i := 1; i <= 205; i++ {
		require.NoError(t, sm.saveMessageToDB(s.ID, i, "user", fmt.Sprintf("message-%d", i), "", nil, nil))
	}
	s.MessageCount = 205
	change, err := sm.ChangeSessionModel(s.ID, "custom", "qwen", nil)
	require.NoError(t, err)
	preview, err := sm.GetModelSwitchContext(s.ID, change.MessageID)
	require.NoError(t, err)
	var expected []llmclient.ChatMessage
	require.Equal(t, 5, preview.OmittedRecords)
	require.NoError(t, json.Unmarshal([]byte(preview.Context), &expected))
	require.Len(t, expected, 200)
	require.Equal(t, "message-6", expected[0].Content)
	require.Equal(t, "message-205", expected[199].Content)
	require.NoError(t, sm.saveMessageToDB(s.ID, change.Sequence+1, "user", "current prompt", "", nil, nil))
	actual, err := sm.buildDirectConversationHistory(s.ID, change.Sequence+1)
	require.NoError(t, err)
	require.Equal(t, expected, actual, "current prompt must not enter history before being appended")
}

func TestRecoveryPreservesOriginalAttribution(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	s := seedSwitchSession(t, sm, "codex", "gpt-6-astra", nil)
	seedMessages(t, sm, s, [2]string{"assistant", "old reply"})
	_, err := sm.ChangeSessionModel(s.ID, "claude", "opus", nil)
	require.NoError(t, err)
	var recovered *recoveredAssistantMessage
	sm.SetBroadcastMessageCallback(func(_ uuid.UUID, msg types.Message) {
		if m, ok := msg.(*recoveredAssistantMessage); ok {
			recovered = m
		}
	})
	require.NoError(t, sm.resendAllMessagesFromDB(s.ID))
	require.NotNil(t, recovered)
	require.Equal(t, "codex", recovered.Provider)
	require.Equal(t, "gpt-6-astra", recovered.Model)
	require.Equal(t, "gpt-6-astra", recovered.Message.(*types.AssistantMessage).Model)
}

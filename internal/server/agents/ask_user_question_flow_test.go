package agents

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/stretchr/testify/require"
)

var askInput = map[string]interface{}{
	"questions": []interface{}{map[string]interface{}{
		"question": "How close should the character be?",
		"header":   "Character",
		"options": []interface{}{
			map[string]interface{}{"label": "Original hero", "description": "safe"},
			map[string]interface{}{"label": "Batman, named", "description": "risky"},
		},
	}},
}

// newQuestionHarness wires AskUserQuestion the way NewAgentHandler does, for a
// session with no browser connected.
func newQuestionHarness(t *testing.T) (*AgentHandler, *AgentSession, types.CanUseToolFunc) {
	t.Helper()
	sm, cleanup := newModelSwitchTestManager(t)
	t.Cleanup(cleanup)
	session := seedSwitchSession(t, sm, "claude", "opus", nil)
	session.pendingQuestions = map[string]chan UserQuestionAnswerResponse{}
	session.pendingQuestionData = map[string]*UserQuestionMessage{}
	h := &AgentHandler{SessionManager: sm, sessionBuffers: map[uuid.UUID]*MessageBuffer{}}
	sm.SetAskUserQuestionHandler(h.askUserQuestions)
	return h, session, sm.createPermissionCallback(session)
}

type callbackResult struct {
	result interface{}
	err    error
}

func askAsync(ctx context.Context, callback types.CanUseToolFunc) chan callbackResult {
	done := make(chan callbackResult, 1)
	go func() {
		result, err := callback(ctx, "AskUserQuestion", askInput, types.ToolPermissionContext{})
		done <- callbackResult{result, err}
	}()
	return done
}

// waitForQuestion returns the id of the question the session is waiting on.
func waitForQuestion(t *testing.T, session *AgentSession) string {
	t.Helper()
	var id string
	require.Eventually(t, func() bool {
		pending := session.pendingQuestionMessages()
		if len(pending) == 0 {
			return false
		}
		id = pending[0].QuestionID
		return true
	}, 5*time.Second, 5*time.Millisecond)
	return id
}

// resolvedReasons lists the user_question_resolved reasons buffered for the
// session (no browser was connected to receive them).
func resolvedReasons(h *AgentHandler, sessionID uuid.UUID) map[string]string {
	reasons := map[string]string{}
	for _, m := range h.getOrCreateBuffer(sessionID).DrainAll() {
		if raw, ok := m.RawMsg.(map[string]interface{}); ok && raw["type"] == string(MessageTypeUserQuestionResolved) {
			reasons[raw["question_id"].(string)] = raw["reason"].(string)
		}
	}
	return reasons
}

func TestAskUserQuestionWaitsWithoutABrowserAndAcrossDisconnects(t *testing.T) {
	h, session, callback := newQuestionHarness(t)
	require.False(t, session.IsWebSocketConnected())

	done := askAsync(context.Background(), callback)
	id := waitForQuestion(t, session)

	// A tab closing used to deny every pending permission, this question included,
	// so the agent carried on without an answer.
	session.CleanupPendingPermissions()
	select {
	case r := <-done:
		t.Fatalf("question resolved without an answer: %+v", r.result)
	case <-time.After(100 * time.Millisecond):
	}

	session.questionMu.Lock()
	session.pendingQuestions[id] <- UserQuestionAnswerResponse{Answers: []string{"Original hero"}}
	session.questionMu.Unlock()

	r := <-done
	require.NoError(t, r.err)
	allow, ok := r.result.(types.PermissionResultAllow)
	require.True(t, ok, "expected approval, got %#v", r.result)
	require.Equal(t, map[string]interface{}{"How close should the character be?": "Original hero"}, (*allow.UpdatedInput)["answers"])
	require.Empty(t, session.pendingQuestionMessages())
	require.Equal(t, map[string]string{id: questionAnswered}, resolvedReasons(h, session.ID))
}

func TestDismissedQuestionTellsTheAgentAndClearsEveryClient(t *testing.T) {
	h, session, callback := newQuestionHarness(t)
	done := askAsync(context.Background(), callback)
	id := waitForQuestion(t, session)

	require.NoError(t, h.dismissUserQuestion(session.ID, id, nil))

	r := <-done
	deny, ok := r.result.(types.PermissionResultDeny)
	require.True(t, ok, "expected denial, got %#v", r.result)
	require.Equal(t, askUserQuestionDismissedMessage, deny.Message)
	require.Empty(t, session.pendingQuestionMessages())
	require.Equal(t, map[string]string{id: questionDismissed}, resolvedReasons(h, session.ID))
}

func TestInterruptedTurnCancelsTheQuestion(t *testing.T) {
	h, session, callback := newQuestionHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := askAsync(ctx, callback)
	id := waitForQuestion(t, session)
	cancel()

	r := <-done
	_, ok := r.result.(types.PermissionResultDeny)
	require.True(t, ok)
	require.Equal(t, map[string]string{id: questionCancelled}, resolvedReasons(h, session.ID))
}

// Dismissing a question nothing waits on any more (a stale modal) just clears
// it from every client.
func TestDismissingAStaleQuestionClearsIt(t *testing.T) {
	h, session, _ := newQuestionHarness(t)
	require.NoError(t, h.dismissUserQuestion(session.ID, "gone", nil))
	require.Equal(t, map[string]string{"gone": questionExpired}, resolvedReasons(h, session.ID))

	unknown := uuid.New()
	require.NoError(t, h.dismissUserQuestion(unknown, "other", nil))
	require.Equal(t, map[string]string{"other": questionExpired}, resolvedReasons(h, unknown))
}

package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/toolguard"
	"github.com/stretchr/testify/require"
)

func guardAsync(ctx context.Context, sm *SessionManager, id uuid.UUID, tool string, args map[string]interface{}, l *toolguard.Limiter) chan ToolGuardResult {
	done := make(chan ToolGuardResult, 1)
	go func() { done <- sm.guardTool(ctx, id, tool, args, l) }()
	return done
}

func answerQuestion(t *testing.T, session *AgentSession, id string, answer UserQuestionAnswerResponse) {
	t.Helper()
	session.questionMu.Lock()
	ch := session.pendingQuestions[id]
	session.questionMu.Unlock()
	require.NotNil(t, ch, "question %s is not waiting", id)
	ch <- answer
}

func requireStillWaiting(t *testing.T, done chan ToolGuardResult) {
	t.Helper()
	select {
	case r := <-done:
		t.Fatalf("guard returned without an answer: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestGuardToolAsksTheUserAndRunsOnApproval(t *testing.T) {
	h, session, _ := newQuestionHarness(t)
	sm := h.SessionManager
	// Yolo / bypass / always-allow do not skip the approval.
	yes := true
	session.Options.DangerouslySkipPermissions = &yes
	session.Options.AlwaysAllowRules = []AlwaysAllowRule{{Tool: "*"}}

	l := toolguard.NewLimiter(toolguard.Caps{MaxGPUPodsPerSession: 1})
	done := guardAsync(context.Background(), sm, session.ID, "gpu_launch", map[string]interface{}{"tier": "beast", "template_id": "t1"}, l)
	id := waitForQuestion(t, session)

	q := session.pendingQuestionMessages()[0]
	require.Equal(t, toolguard.Header, q.Header)
	require.Contains(t, q.Question, "gpu_launch")
	require.Contains(t, q.Question, "tier: beast")
	require.Contains(t, q.Question, "$3.49/hr")
	require.Len(t, q.Options, 2)

	// No timeout: it keeps waiting, even across a closed tab.
	session.CleanupPendingPermissions()
	requireStillWaiting(t, done)

	answerQuestion(t, session, id, UserQuestionAnswerResponse{Answers: []string{toolguard.AnswerApprove}})
	r := <-done
	require.True(t, r.Allowed, r.Message)
	require.Equal(t, 1, l.Active(session.ID.String()))
	require.Equal(t, map[string]string{id: questionAnswered}, resolvedReasons(h, session.ID))
}

func TestGuardToolDenyDismissAndOtherAnswers(t *testing.T) {
	for name, answer := range map[string]UserQuestionAnswerResponse{
		"deny":    {Answers: []string{toolguard.AnswerDeny}},
		"dismiss": {Dismissed: true},
		"other":   {Answers: []string{"sure, whatever"}},
	} {
		t.Run(name, func(t *testing.T) {
			h, session, _ := newQuestionHarness(t)
			l := toolguard.NewLimiter(toolguard.Caps{MaxGPUPodsPerSession: 1})
			done := guardAsync(context.Background(), h.SessionManager, session.ID, "runpod__create_pod", map[string]interface{}{"gpuTypeIds": []string{"H100"}}, l)
			id := waitForQuestion(t, session)
			answerQuestion(t, session, id, answer)
			r := <-done
			require.False(t, r.Allowed)
			require.Contains(t, r.Message, "did not approve runpod__create_pod")
			require.Equal(t, 0, l.Active(session.ID.String()), "a declined launch must give its slot back")
		})
	}
}

func TestGuardToolInterruptCancelsTheWait(t *testing.T) {
	h, session, _ := newQuestionHarness(t)
	l := toolguard.NewLimiter(toolguard.Caps{})

	// The request goes away (eggify-tools killed / call aborted).
	ctx, cancel := context.WithCancel(context.Background())
	done := guardAsync(ctx, h.SessionManager, session.ID, "deploy_app", nil, l)
	id := waitForQuestion(t, session)
	cancel()
	r := <-done
	require.False(t, r.Allowed)
	require.Equal(t, map[string]string{id: questionCancelled}, resolvedReasons(h, session.ID))

	// The turn is interrupted (the session context is cancelled).
	done = guardAsync(context.Background(), h.SessionManager, session.ID, "deploy_app", nil, l)
	waitForQuestion(t, session)
	session.cancel()
	r = <-done
	require.False(t, r.Allowed)
}

func TestGuardToolPassThroughAndCaps(t *testing.T) {
	h, session, _ := newQuestionHarness(t)
	sm := h.SessionManager
	sid := session.ID.String()
	l := toolguard.NewLimiter(toolguard.Caps{MaxGPUPodsPerSession: 1, MaxGPULaunchesPerDay: 5})

	// Read-only tools never ask.
	for _, tool := range []string{"gpu_status", "runpod__list_pods", "n0_repo_read_file", "Bash"} {
		require.True(t, sm.guardTool(context.Background(), session.ID, tool, nil, l).Allowed, tool)
	}
	require.Empty(t, session.pendingQuestionMessages())

	// Session cap: a second launch is refused without asking.
	require.NoError(t, l.Reserve(sid))
	r := sm.guardTool(context.Background(), session.ID, "gpu_launch", nil, l)
	require.False(t, r.Allowed)
	require.Contains(t, r.Message, toolguard.EnvMaxGPUPodsPerSession)
	require.Empty(t, session.pendingQuestionMessages())

	// gpu_stop frees the slot without a question.
	require.True(t, sm.guardTool(context.Background(), session.ID, "gpu_stop", nil, l).Allowed)
	require.Equal(t, 0, l.Active(sid))

	// Unknown session: refused.
	r = sm.guardTool(context.Background(), uuid.New(), "deploy_app", nil, l)
	require.False(t, r.Allowed)
	require.Contains(t, r.Message, "not active")
}

func TestN0ToolGuardAdapter(t *testing.T) {
	h, session, _ := newQuestionHarness(t)
	sm := h.SessionManager
	ok, _ := sm.n0ToolGuard(context.Background(), session.ID.String(), "runpod__list_pods", nil)
	require.True(t, ok)
	ok, msg := sm.n0ToolGuard(context.Background(), "not-a-uuid", "runpod__create_pod", nil)
	require.False(t, ok)
	require.NotEmpty(t, msg)

	type res struct {
		ok  bool
		msg string
	}
	done := make(chan res, 1)
	go func() {
		ok, msg := sm.n0ToolGuard(context.Background(), session.ID.String(), "ws__acme__relay__send_message", json.RawMessage(`{"to":"+4512345678","text":"hi"}`))
		done <- res{ok, msg}
	}()
	id := waitForQuestion(t, session)
	q := session.pendingQuestionMessages()[0].Question
	require.True(t, strings.Contains(q, "to: +4512345678") && strings.Contains(q, "sends a message"), q)
	answerQuestion(t, session, id, UserQuestionAnswerResponse{Answers: []string{toolguard.AnswerDeny}})
	got := <-done
	require.False(t, got.ok)
	require.Contains(t, got.msg, "did not approve")
}

// An agent that reads the API key can connect to /agent/ws, but it must not be
// able to answer or dismiss its own approval card.
func TestApprovalCardOnlyAnsweredByASignedInUser(t *testing.T) {
	withEnforced(t, true)
	h, session, _ := newQuestionHarness(t)
	l := toolguard.NewLimiter(toolguard.Caps{})
	done := guardAsync(context.Background(), h.SessionManager, session.ID, "deploy_app", map[string]interface{}{"name": "x"}, l)
	id := waitForQuestion(t, session)
	require.True(t, session.pendingQuestionMessages()[0].RequiresUser, "approval cards are marked server-side")

	// API-key connection (no user): answer rejected, the call stays blocked.
	delivered := false
	err := h.answerUserQuestion(session.ID, id, []string{toolguard.AnswerApprove}, nil, func() { delivered = true })
	require.ErrorIs(t, err, ErrConfirmationNeedsUser)
	require.False(t, delivered, "no ack for a rejected answer")
	empty := ""
	require.ErrorIs(t, h.answerUserQuestion(session.ID, id, []string{toolguard.AnswerApprove}, &empty, nil), ErrConfirmationNeedsUser)
	// Dismissing via the API key does nothing either.
	require.ErrorIs(t, h.dismissUserQuestion(session.ID, id, nil), ErrConfirmationNeedsUser)
	requireStillWaiting(t, done)
	require.Len(t, session.pendingQuestionMessages(), 1)

	// A signed-in user's answer lets the call through.
	user := "alice"
	require.NoError(t, h.answerUserQuestion(session.ID, id, []string{toolguard.AnswerApprove}, &user, nil))
	r := <-done
	require.True(t, r.Allowed, r.Message)
}

func TestApprovalCardUserCanDismiss(t *testing.T) {
	withEnforced(t, true)
	h, session, _ := newQuestionHarness(t)
	done := guardAsync(context.Background(), h.SessionManager, session.ID, "gpu_push", nil, toolguard.NewLimiter(toolguard.Caps{}))
	id := waitForQuestion(t, session)
	user := "alice"
	require.NoError(t, h.dismissUserQuestion(session.ID, id, &user))
	require.False(t, (<-done).Allowed)
}

func TestApprovalCardWithUserAuthDisabled(t *testing.T) {
	withEnforced(t, false)
	h, session, _ := newQuestionHarness(t)
	done := guardAsync(context.Background(), h.SessionManager, session.ID, "deploy_app", nil, toolguard.NewLimiter(toolguard.Caps{}))
	id := waitForQuestion(t, session)
	// No users exist: today's behaviour (with a warning in the log).
	require.NoError(t, h.answerUserQuestion(session.ID, id, []string{toolguard.AnswerApprove}, nil, nil))
	require.True(t, (<-done).Allowed)
}

// The agent's own AskUserQuestion calls are not confirmations: any
// authenticated connection may answer them, as before.
func TestOrdinaryQuestionsStayUnprivileged(t *testing.T) {
	withEnforced(t, true)
	h, session, callback := newQuestionHarness(t)
	done := askAsync(context.Background(), callback)
	id := waitForQuestion(t, session)
	require.False(t, session.pendingQuestionMessages()[0].RequiresUser)
	require.NoError(t, h.dismissUserQuestion(session.ID, id, nil))
	<-done
}

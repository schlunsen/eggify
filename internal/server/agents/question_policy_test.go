package agents

import (
	"context"
	"testing"
	"time"

	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/stretchr/testify/require"
)

// recommendedInput is an AskUserQuestion whose second option is recommended.
var recommendedInput = map[string]interface{}{
	"questions": []interface{}{map[string]interface{}{
		"question": "Which time zone should Home use?",
		"header":   "Time zone",
		"options": []interface{}{
			map[string]interface{}{"label": "Profile zone"},
			map[string]interface{}{"label": "Device unless set (Recommended)"},
		},
	}},
}

func askWith(ctx context.Context, callback types.CanUseToolFunc, input map[string]interface{}) chan callbackResult {
	done := make(chan callbackResult, 1)
	go func() {
		result, err := callback(ctx, "AskUserQuestion", input, types.ToolPermissionContext{})
		done <- callbackResult{result, err}
	}()
	return done
}

func requireQuestionWaiting(t *testing.T, done chan callbackResult) {
	t.Helper()
	select {
	case r := <-done:
		t.Fatalf("question resolved without an answer: %#v", r.result)
	case <-time.After(150 * time.Millisecond):
	}
}

func requireAnswer(t *testing.T, done chan callbackResult) map[string]interface{} {
	t.Helper()
	select {
	case r := <-done:
		require.NoError(t, r.err)
		allow, ok := r.result.(types.PermissionResultAllow)
		require.True(t, ok, "expected an answer, got %#v", r.result)
		return (*allow.UpdatedInput)["answers"].(map[string]interface{})
	case <-time.After(3 * time.Second):
		t.Fatal("question was not answered")
	}
	return nil
}

func fastQuestionTimeouts(t *testing.T) {
	t.Helper()
	old := questionTimeoutUnit
	questionTimeoutUnit = 10 * time.Millisecond
	t.Cleanup(func() { questionTimeoutUnit = old })
}

func setSessionPolicy(t *testing.T, sm *SessionManager, session *AgentSession, p *QuestionPolicy) {
	t.Helper()
	_, _, err := sm.SetSessionQuestionPolicy(session.ID, p)
	require.NoError(t, err)
}

func TestRecommendedOptions(t *testing.T) {
	opts := func(labels ...string) []QuestionOption {
		out := make([]QuestionOption, len(labels))
		for i, l := range labels {
			out[i] = QuestionOption{Label: l}
		}
		return out
	}
	require.Equal(t, []int{1}, recommendedOptions(opts("A", "B (Recommended)", "C (recommended)"), false))
	require.Equal(t, []int{1, 2}, recommendedOptions(opts("A", "B (Recommended)", "C ( recommended ) "), true))
	require.Equal(t, []int{0}, recommendedOptions(opts("A", "B"), false), "no mark: the first option")
	require.Equal(t, []int{0}, recommendedOptions(opts("Recommended setup", "B"), true), "only a trailing (Recommended) counts")
	require.Equal(t, []int{}, recommendedOptions(nil, false))
}

func TestNormalizeQuestionPolicy(t *testing.T) {
	p, err := NormalizeQuestionPolicy(QuestionPolicy{Mode: "wait", TimeoutSeconds: 99})
	require.NoError(t, err)
	require.Equal(t, QuestionPolicy{Mode: QuestionPolicyWait}, p)
	_, err = NormalizeQuestionPolicy(QuestionPolicy{Mode: "auto_after", TimeoutSeconds: 5})
	require.Error(t, err)
	_, err = NormalizeQuestionPolicy(QuestionPolicy{Mode: "yolo"})
	require.Error(t, err)
	require.Equal(t, "", QuestionPolicyAutoAction(&SessionOptions{QuestionPolicy: &QuestionPolicy{Mode: "wait"}}))
	require.Equal(t, QuestionPolicyAction, QuestionPolicyAutoAction(&SessionOptions{QuestionPolicy: &QuestionPolicy{Mode: "auto"}}))
	require.Equal(t, QuestionPolicyAction, QuestionPolicyAutoAction(&SessionOptions{QuestionPolicy: &QuestionPolicy{Mode: "bogus"}}), "invalid is privileged")
}

// Only a policy that answers sooner than the user's global one needs a user:
// an agent may make a session wait longer, never shorter.
func TestQuestionPolicyPrivilegeIsRelativeToTheGlobalPolicy(t *testing.T) {
	h, _, _ := newQuestionHarness(t) // global: default, auto after 120 s
	after := func(s int) *SessionOptions {
		return &SessionOptions{QuestionPolicy: &QuestionPolicy{Mode: QuestionPolicyAutoAfter, TimeoutSeconds: s}}
	}
	require.Equal(t, "", QuestionPolicyAutoAction(after(600)), "slower than the global policy")
	require.Equal(t, "", QuestionPolicyAutoAction(after(120)))
	require.Equal(t, QuestionPolicyAction, QuestionPolicyAutoAction(after(30)))

	_, err := SaveQuestionPolicy(h.SessionManager.GetRepo(), QuestionPolicy{Mode: QuestionPolicyWait})
	require.NoError(t, err)
	require.Equal(t, QuestionPolicyAction, QuestionPolicyAutoAction(after(600)), "any auto-answer beats waiting")
	require.Equal(t, "", QuestionPolicyAutoAction(&SessionOptions{QuestionPolicy: &QuestionPolicy{Mode: QuestionPolicyWait}}))
}

func TestGlobalQuestionPolicyDefaultsToAutoAfterTwoMinutes(t *testing.T) {
	h, _, _ := newQuestionHarness(t)
	p, isDefault := LoadQuestionPolicy(h.SessionManager.GetRepo())
	require.True(t, isDefault)
	require.Equal(t, QuestionPolicy{Mode: QuestionPolicyAutoAfter, TimeoutSeconds: 120}, p)

	saved, err := SaveQuestionPolicy(h.SessionManager.GetRepo(), QuestionPolicy{Mode: "wait"})
	require.NoError(t, err)
	p, isDefault = LoadQuestionPolicy(h.SessionManager.GetRepo())
	require.False(t, isDefault)
	require.Equal(t, saved, p)
	require.True(t, IsReservedSettingKey(QuestionPolicySettingKey), "only the privileged endpoint writes it")
}

func TestAutoPolicyPicksTheRecommendedOptionAndTellsTheAgent(t *testing.T) {
	h, session, callback := newQuestionHarness(t)
	setSessionPolicy(t, h.SessionManager, session, &QuestionPolicy{Mode: QuestionPolicyAuto})

	answers := requireAnswer(t, askWith(context.Background(), callback, recommendedInput))
	answer := answers["Which time zone should Home use?"].(string)
	require.Contains(t, answer, "Device unless set (Recommended)")
	require.Contains(t, answer, "auto-picked", "the agent must know nobody chose it")
	require.Empty(t, session.pendingQuestionMessages())
	reasons := resolvedReasons(h, session.ID)
	require.Len(t, reasons, 1)
	for _, r := range reasons {
		require.Equal(t, questionAutoAnswered, r)
	}
}

func TestAutoAfterWaitsThenAnswers(t *testing.T) {
	fastQuestionTimeouts(t)
	h, session, callback := newQuestionHarness(t)
	setSessionPolicy(t, h.SessionManager, session, &QuestionPolicy{Mode: QuestionPolicyAutoAfter, TimeoutSeconds: 30}) // 300 ms

	done := askWith(context.Background(), callback, recommendedInput)
	waitForQuestion(t, session)
	q := session.pendingQuestionMessages()[0]
	require.NotNil(t, q.AutoAnswerAt, "clients get the deadline")
	require.Equal(t, []int{1}, q.Recommended)
	requireQuestionWaiting(t, done)

	answers := requireAnswer(t, done)
	require.Contains(t, answers["Which time zone should Home use?"], "Device unless set (Recommended)")
	require.Contains(t, answers["Which time zone should Home use?"], "within 30 s")
}

func TestAnswerBeforeTheTimerWins(t *testing.T) {
	fastQuestionTimeouts(t)
	h, session, callback := newQuestionHarness(t)
	setSessionPolicy(t, h.SessionManager, session, &QuestionPolicy{Mode: QuestionPolicyAutoAfter, TimeoutSeconds: 30})

	done := askWith(context.Background(), callback, recommendedInput)
	id := waitForQuestion(t, session)
	answerQuestion(t, session, id, UserQuestionAnswerResponse{Answers: []string{"Profile zone"}})
	answers := requireAnswer(t, done)
	require.Equal(t, "Profile zone", answers["Which time zone should Home use?"])
}

func TestWaitPolicyWaitsUntilSwitchedToAuto(t *testing.T) {
	fastQuestionTimeouts(t)
	h, session, callback := newQuestionHarness(t)
	setSessionPolicy(t, h.SessionManager, session, &QuestionPolicy{Mode: QuestionPolicyWait})

	done := askWith(context.Background(), callback, recommendedInput)
	waitForQuestion(t, session)
	require.Nil(t, session.pendingQuestionMessages()[0].AutoAnswerAt)
	requireQuestionWaiting(t, done)

	// Changing the policy applies to the question already waiting.
	setSessionPolicy(t, h.SessionManager, session, &QuestionPolicy{Mode: QuestionPolicyAuto})
	answers := requireAnswer(t, done)
	require.Contains(t, answers["Which time zone should Home use?"], "Device unless set (Recommended)")
}

func TestGlobalPolicyChangeRetimesSessionsWithoutTheirOwn(t *testing.T) {
	fastQuestionTimeouts(t)
	h, session, callback := newQuestionHarness(t)
	sm := h.SessionManager
	_, err := SaveQuestionPolicy(sm.GetRepo(), QuestionPolicy{Mode: QuestionPolicyWait})
	require.NoError(t, err)

	done := askWith(context.Background(), callback, recommendedInput)
	waitForQuestion(t, session)
	requireQuestionWaiting(t, done)

	saved, err := SaveQuestionPolicy(sm.GetRepo(), QuestionPolicy{Mode: QuestionPolicyAuto})
	require.NoError(t, err)
	sm.QuestionPolicyDefaultChanged(saved)
	requireAnswer(t, done)
}

func TestKeepWaitingStopsTheTimer(t *testing.T) {
	fastQuestionTimeouts(t)
	h, session, callback := newQuestionHarness(t)
	setSessionPolicy(t, h.SessionManager, session, &QuestionPolicy{Mode: QuestionPolicyAutoAfter, TimeoutSeconds: 20}) // 200 ms

	done := askWith(context.Background(), callback, recommendedInput)
	id := waitForQuestion(t, session)
	require.True(t, h.SessionManager.HoldQuestion(session.ID, id))
	require.Eventually(t, func() bool {
		q := session.pendingQuestionMessages()
		return len(q) == 1 && q[0].Held && q[0].AutoAnswerAt == nil
	}, time.Second, 5*time.Millisecond)

	select {
	case r := <-done:
		t.Fatalf("held question was answered: %#v", r.result)
	case <-time.After(400 * time.Millisecond):
	}
	// Even "auto now" leaves a held question to the user.
	setSessionPolicy(t, h.SessionManager, session, &QuestionPolicy{Mode: QuestionPolicyAuto})
	requireQuestionWaiting(t, done)

	answerQuestion(t, session, id, UserQuestionAnswerResponse{Answers: []string{"Profile zone"}})
	require.Equal(t, "Profile zone", requireAnswer(t, done)["Which time zone should Home use?"])
}

// Tool approvals and loop proposals use the same question machinery; an
// auto-answering policy must never approve them.
func TestConfirmationsNeverFollowTheQuestionPolicy(t *testing.T) {
	fastQuestionTimeouts(t)
	h, session, callback := newQuestionHarness(t)
	setSessionPolicy(t, h.SessionManager, session, &QuestionPolicy{Mode: QuestionPolicyAuto})

	done := askWith(withUserConfirmation(context.Background()), callback, recommendedInput)
	waitForQuestion(t, session)
	q := session.pendingQuestionMessages()[0]
	require.True(t, q.RequiresUser)
	require.Nil(t, q.AutoAnswerAt)
	require.Empty(t, q.Recommended)
	requireQuestionWaiting(t, done)
}

func TestSessionQuestionPolicyIsListedAndClearable(t *testing.T) {
	h, session, _ := newQuestionHarness(t)
	sm := h.SessionManager
	override, effective, err := sm.SetSessionQuestionPolicy(session.ID, &QuestionPolicy{Mode: QuestionPolicyWait})
	require.NoError(t, err)
	require.Equal(t, &QuestionPolicy{Mode: QuestionPolicyWait}, override)
	require.Equal(t, QuestionPolicyWait, effective.Mode)

	listed := []Session{session.Session}
	sm.AddQuestionPolicies(listed)
	require.Equal(t, QuestionPolicyWait, listed[0].QuestionPolicy.Mode)

	override, effective, err = sm.SetSessionQuestionPolicy(session.ID, nil)
	require.NoError(t, err)
	require.Nil(t, override)
	require.Equal(t, DefaultQuestionPolicy, effective)
}

// An agent that accepts a handover must not inherit an auto-answering policy
// (like YOLO, it is kept only when a signed-in user asks to keep permissions).
func TestHandoverDropsAutoAnsweringPolicy(t *testing.T) {
	newQuestionHarness(t) // a database with the default global policy (auto after 2 min)
	slow := SessionOptions{QuestionPolicy: &QuestionPolicy{Mode: QuestionPolicyAutoAfter, TimeoutSeconds: 1800}}
	require.False(t, hasHandoverPermissions(slow))
	stripHandoverPermissions(&slow, HandoverOptions{})
	require.NotNil(t, slow.QuestionPolicy, "dropping it would make the session answer sooner")

	opts := SessionOptions{QuestionPolicy: &QuestionPolicy{Mode: QuestionPolicyAuto}}
	require.True(t, hasHandoverPermissions(opts))
	stripHandoverPermissions(&opts, HandoverOptions{})
	require.Nil(t, opts.QuestionPolicy)

	wait := SessionOptions{QuestionPolicy: &QuestionPolicy{Mode: QuestionPolicyWait}}
	require.False(t, hasHandoverPermissions(wait))
	stripHandoverPermissions(&wait, HandoverOptions{})
	require.NotNil(t, wait.QuestionPolicy, "waiting is harmless and kept")
}

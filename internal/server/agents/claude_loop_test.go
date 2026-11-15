package agents

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	claude "github.com/schlunsen/claude-agent-sdk-go"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/stretchr/testify/require"
)

func feedLoop(t *testing.T, sm *SessionManager, session *AgentSession, raw string) {
	t.Helper()
	msg, err := types.UnmarshalMessage([]byte(raw))
	require.NoError(t, err)
	sm.observeScheduledLoop(session, msg)
}

// wakeupResult is the CLI's answer to a ScheduleWakeup call.
func wakeupResult(id string, isError bool) string {
	return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":%t,"content":"ok"}]}}`, id, isError)
}

const wakeupCall = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_W","name":"ScheduleWakeup","input":{"delaySeconds":%s,"reason":"waiting for CI\nsecond line","prompt":"/loop go on"}}]}}`

func TestClaudeLoopIsTrackedUntilItEnds(t *testing.T) {
	sm := &SessionManager{sessions: map[uuid.UUID]*AgentSession{}}
	session := &AgentSession{Session: Session{ID: uuid.New()}}
	sm.sessions[session.ID] = session
	var seen []*ScheduledLoop
	sm.SetScheduledLoopCallback(func(id uuid.UUID, loop *ScheduledLoop) {
		require.Equal(t, session.ID, id)
		seen = append(seen, loop)
	})

	feedLoop(t, sm, session, sprintf(wakeupCall, "1800"))
	require.Nil(t, sm.ScheduledLoopFor(session.ID), "not scheduled before the call succeeds")
	feedLoop(t, sm, session, wakeupResult("toolu_W", false))
	loop := sm.ScheduledLoopFor(session.ID)
	require.NotNil(t, loop)
	require.Equal(t, 1800, loop.DelaySeconds)
	require.Equal(t, "waiting for CI", loop.Reason)
	require.WithinDuration(t, time.Now().Add(30*time.Minute), loop.NextAt, 5*time.Second)
	require.Len(t, seen, 1)

	// The turn that scheduled it ends before the wakeup is due: still pending.
	feedLoop(t, sm, session, `{"type":"result","subtype":"success","is_error":false,"session_id":"x"}`)
	require.NotNil(t, sm.ScheduledLoopFor(session.ID))

	// Delays are clamped like the CLI does.
	feedLoop(t, sm, session, sprintf(wakeupCall, "5"))
	feedLoop(t, sm, session, wakeupResult("toolu_W", false))
	require.Equal(t, minWakeupDelaySeconds, sm.ScheduledLoopFor(session.ID).DelaySeconds)
	feedLoop(t, sm, session, `{"type":"result","subtype":"success","is_error":false,"session_id":"x"}`)
	require.NotNil(t, sm.ScheduledLoopFor(session.ID), "the scheduling turn keeps it")

	// A turn that started before the wakeup was due (a user's) leaves it pending,
	// even if it ends after the due time.
	feedLoop(t, sm, session, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"user turn"}]}}`)
	session.loopTurnStartedAt.Store(time.Now().Add(-time.Minute).UnixNano()) // it began a minute ago
	session.scheduledLoop.Load().NextAt = time.Now().Add(-time.Second)       // and the wakeup fell due since
	feedLoop(t, sm, session, `{"type":"result","subtype":"success","is_error":false,"session_id":"x"}`)
	require.NotNil(t, sm.ScheduledLoopFor(session.ID))

	// The wakeup's own run that ends without a new one ends the loop.
	feedLoop(t, sm, session, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"iteration"}]}}`)
	feedLoop(t, sm, session, `{"type":"result","subtype":"success","is_error":false,"session_id":"x"}`)
	require.Nil(t, sm.ScheduledLoopFor(session.ID))
	require.Nil(t, seen[len(seen)-1])

	// stop: true ends it at once.
	feedLoop(t, sm, session, sprintf(wakeupCall, "600"))
	feedLoop(t, sm, session, wakeupResult("toolu_W", false))
	feedLoop(t, sm, session, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_S","name":"ScheduleWakeup","input":{"stop":true}}]}}`)
	require.NotNil(t, sm.ScheduledLoopFor(session.ID), "stopped only once the call succeeds")
	feedLoop(t, sm, session, wakeupResult("toolu_S", false))
	require.Nil(t, sm.ScheduledLoopFor(session.ID))

	// A denied call schedules nothing.
	feedLoop(t, sm, session, sprintf(wakeupCall, "600"))
	feedLoop(t, sm, session, wakeupResult("toolu_W", true))
	require.Nil(t, sm.ScheduledLoopFor(session.ID))
}

// The wakeup lives in the session's Claude CLI; closing it ends the loop.
func TestClosingClaudeCLIEndsItsLoop(t *testing.T) {
	sm := &SessionManager{sessions: map[uuid.UUID]*AgentSession{}}
	session := &AgentSession{Session: Session{ID: uuid.New()}}
	sm.sessions[session.ID] = session
	feedLoop(t, sm, session, sprintf(wakeupCall, "900"))
	feedLoop(t, sm, session, wakeupResult("toolu_W", false))
	require.NotNil(t, sm.ScheduledLoopFor(session.ID))

	session.client = &claude.Client{} // never connected: Close is a no-op
	require.NoError(t, sm.closeClaudeClient(session, context.Background()))
	require.Nil(t, sm.ScheduledLoopFor(session.ID))
}

func sprintf(format string, args ...interface{}) string { return fmt.Sprintf(format, args...) }

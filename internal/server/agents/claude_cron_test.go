package agents

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	claude "github.com/schlunsen/claude-agent-sdk-go"
	"github.com/stretchr/testify/require"
)

// The shapes below are from a real Claude Code 2.1.288 session (stream-json).
const (
	cronCreateCall   = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_C","name":"CronCreate","input":{"cron":"*/30 * * * *","prompt":"check TestFlight review status for build 2.20\nreport back","recurring":true}}]}}`
	cronCreateResult = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_C","type":"tool_result","content":"Scheduled recurring job 8841f567 (Every 30 minutes). Session-only (not written to disk, dies when Claude exits). Auto-expires after 7 days. Use CronDelete to cancel sooner."}]},"tool_use_result":{"id":"8841f567","humanSchedule":"Every 30 minutes","recurring":true,"durable":false}}`
	cronDeleteCall   = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_D","name":"CronDelete","input":{"id":"8841f567"}}]}}`
	cronDeleteResult = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_D","type":"tool_result","content":"Cancelled job 8841f567."}]},"tool_use_result":{"id":"8841f567"}}`
	turnResult       = `{"type":"result","subtype":"success","is_error":false,"session_id":"x"}`
)

func newCronSession(t *testing.T) (*SessionManager, *AgentSession, *int) {
	sm := &SessionManager{sessions: map[uuid.UUID]*AgentSession{}}
	session := &AgentSession{Session: Session{ID: uuid.New()}}
	sm.sessions[session.ID] = session
	notified := 0
	sm.SetScheduledLoopCallback(func(id uuid.UUID, _ *ScheduledLoop) {
		require.Equal(t, session.ID, id)
		notified++
	})
	return sm, session, &notified
}

func feedCron(t *testing.T, sm *SessionManager, session *AgentSession, raw string) {
	t.Helper()
	feedCronAt(t, sm, session, raw, session.loopTurnStartedAt.Load())
}

func feedCronAt(t *testing.T, sm *SessionManager, session *AgentSession, raw string, turnStarted int64) {
	t.Helper()
	msg := mustMessage(t, raw)
	sm.observeCronJobs(session, msg, turnStarted)
	sm.observeScheduledLoop(session, msg)
}

func TestCronJobIsTrackedUntilDeleted(t *testing.T) {
	sm, session, notified := newCronSession(t)

	feedCron(t, sm, session, cronCreateCall)
	loop, jobs := sm.ScheduledFor(session.ID)
	require.Nil(t, loop, "not scheduled before the call succeeds")
	require.Empty(t, jobs)

	feedCron(t, sm, session, cronCreateResult)
	loop, jobs = sm.ScheduledFor(session.ID)
	require.Len(t, jobs, 1)
	job := jobs[0]
	require.Equal(t, "8841f567", job.ID)
	require.Equal(t, "*/30 * * * *", job.Cron)
	require.Equal(t, "Every 30 minutes", job.Schedule)
	require.True(t, job.Recurring)
	require.Contains(t, job.Prompt, "check TestFlight")
	require.WithinDuration(t, time.Now().Add(cronJobLifetime), job.ExpiresAt, 5*time.Second)
	require.True(t, job.NextAt.After(time.Now()))
	require.LessOrEqual(t, time.Until(job.NextAt), 30*time.Minute)
	require.Zero(t, job.NextAt.Minute()%30)
	require.Equal(t, 1, *notified)

	// The session's next run (what the sidebar shows) is the job's.
	require.NotNil(t, loop)
	require.Equal(t, "cron", loop.Kind)
	require.Equal(t, job.NextAt, loop.NextAt)
	require.Equal(t, "Every 30 minutes · check TestFlight review status for build 2.20", loop.Reason)

	// The turn that created it ends: still there.
	feedCron(t, sm, session, turnResult)
	_, jobs = sm.ScheduledFor(session.ID)
	require.Len(t, jobs, 1)

	// CronDelete counts once it succeeds.
	feedCron(t, sm, session, cronDeleteCall)
	_, jobs = sm.ScheduledFor(session.ID)
	require.Len(t, jobs, 1)
	feedCron(t, sm, session, cronDeleteResult)
	loop, jobs = sm.ScheduledFor(session.ID)
	require.Empty(t, jobs)
	require.Nil(t, loop)
	require.Equal(t, 2, *notified)
}

// The agent swaps a job for another in one turn (seen live: every 30 min →
// every hour at :07).
func TestCronJobReplacedInOneTurn(t *testing.T) {
	sm, session, _ := newCronSession(t)
	feedCron(t, sm, session, cronCreateCall)
	feedCron(t, sm, session, cronCreateResult)
	feedCron(t, sm, session, cronDeleteCall)
	feedCron(t, sm, session, cronDeleteResult)
	feedCron(t, sm, session, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_C2","name":"CronCreate","input":{"cron":"7 * * * *","prompt":"check again","recurring":true}}]}}`)
	// No tool_use_result: the id comes from the text.
	feedCron(t, sm, session, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_C2","type":"tool_result","content":"Scheduled recurring job df1126f4 (Every hour at :07). Session-only (not written to disk, dies when Claude exits)."}]}}`)
	_, jobs := sm.ScheduledFor(session.ID)
	require.Len(t, jobs, 1)
	require.Equal(t, "df1126f4", jobs[0].ID)
	require.Equal(t, "Every hour at :07", jobs[0].Schedule)
	require.Equal(t, 7, jobs[0].NextAt.Minute())
}

func TestDeniedCronCallChangesNothing(t *testing.T) {
	sm, session, notified := newCronSession(t)
	feedCron(t, sm, session, cronCreateCall)
	feedCron(t, sm, session, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_C","type":"tool_result","is_error":true,"content":"denied"}]}}`)
	_, jobs := sm.ScheduledFor(session.ID)
	require.Empty(t, jobs)
	require.Zero(t, *notified)
}

// A recurring job's run is remembered; its next slot follows. A one-shot job
// is gone after its run.
func TestCronJobRuns(t *testing.T) {
	sm, session, _ := newCronSession(t)
	feedCron(t, sm, session, cronCreateCall)
	feedCron(t, sm, session, cronCreateResult)
	feedCron(t, sm, session, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_O","name":"CronCreate","input":{"cron":"0 9 * * *","prompt":"once","recurring":false}}]}}`)
	feedCron(t, sm, session, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_O","type":"tool_result","content":"Scheduled one-shot job abcd1234 (At 9:00 AM)."}]},"tool_use_result":{"id":"abcd1234","humanSchedule":"At 9:00 AM","recurring":false}}`)
	feedCron(t, sm, session, turnResult)
	_, jobs := sm.ScheduledFor(session.ID)
	require.Len(t, jobs, 2)

	// Pretend both were created two days ago, then a run starts now.
	session.cronMu.Lock()
	for _, job := range session.cronJobs {
		job.CreatedAt = time.Now().Add(-48 * time.Hour)
		if job.Recurring {
			job.ExpiresAt = job.CreatedAt.Add(cronJobLifetime)
		}
	}
	session.cronMu.Unlock()
	started := time.Now().UnixNano()
	feedCronAt(t, sm, session, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"checking"}]}}`, started)
	feedCronAt(t, sm, session, turnResult, started)

	_, jobs = sm.ScheduledFor(session.ID)
	require.Len(t, jobs, 1, "the one-shot job ran")
	require.Equal(t, "8841f567", jobs[0].ID)
	require.False(t, jobs[0].LastRunAt.IsZero())
	require.True(t, jobs[0].NextAt.After(time.Now()))

	// A slot that passed without a run yet (the CLI runs late) stays next.
	session.cronMu.Lock()
	session.cronJobs[0].LastRunAt = time.Now().Add(-40 * time.Minute)
	session.cronMu.Unlock()
	_, jobs = sm.ScheduledFor(session.ID)
	require.True(t, jobs[0].NextAt.Before(time.Now()))

	// Expired jobs are not listed.
	session.cronMu.Lock()
	session.cronJobs[0].ExpiresAt = time.Now().Add(-time.Minute)
	session.cronMu.Unlock()
	loop, jobs := sm.ScheduledFor(session.ID)
	require.Empty(t, jobs)
	require.Nil(t, loop)
}

// A wakeup due sooner than every job wins the session's next run.
func TestWakeupAndCronSoonestWins(t *testing.T) {
	sm, session, _ := newCronSession(t)
	feedCron(t, sm, session, cronCreateCall)
	feedCron(t, sm, session, cronCreateResult)
	feedCron(t, sm, session, sprintf(wakeupCall, "60"))
	feedCron(t, sm, session, wakeupResult("toolu_W", false))
	loop, jobs := sm.ScheduledFor(session.ID)
	require.Len(t, jobs, 1)
	if time.Until(jobs[0].NextAt) > 2*time.Minute {
		require.Equal(t, "wakeup", loop.Kind)
	}
}

// Jobs live in the session's Claude CLI; closing it forgets them.
func TestClosingClaudeCLIForgetsCronJobs(t *testing.T) {
	sm, session, notified := newCronSession(t)
	feedCron(t, sm, session, cronCreateCall)
	feedCron(t, sm, session, cronCreateResult)
	session.client = &claude.Client{} // never connected: Close is a no-op
	require.NoError(t, sm.closeClaudeClient(session, context.Background()))
	loop, jobs := sm.ScheduledFor(session.ID)
	require.Empty(t, jobs)
	require.Nil(t, loop)
	require.Equal(t, 2, *notified)
}

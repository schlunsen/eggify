package agents

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/logging"
)

// Claude Code's own loops.
//
// Claude Code's /loop skill keeps a session going by calling its ScheduleWakeup
// tool: the CLI runs the given prompt again after delaySeconds. Eggify does not
// run these loops (they are not Loop-mode sessions), but it can see them: the
// call is an ordinary tool_use in the parent's stream. The wakeup lives inside
// the session's Claude CLI process, so it is kept in memory only and forgotten
// when that CLI closes (an Eggify restart closes every CLI).

// ScheduledLoop is a pending Claude Code wakeup for a session.
type ScheduledLoop struct {
	NextAt       time.Time `json:"next_at"`
	DelaySeconds int       `json:"delay_seconds"`
	Reason       string    `json:"reason,omitempty"`
	ScheduledAt  time.Time `json:"scheduled_at"`
	// Kind is "wakeup" (/loop's ScheduleWakeup) or "cron" (the soonest
	// CronCreate job, see claude_cron.go).
	Kind string `json:"kind,omitempty"`
}

// The CLI clamps ScheduleWakeup delays to this range.
const (
	minWakeupDelaySeconds = 60
	maxWakeupDelaySeconds = 3600
)

// pendingWakeup is a ScheduleWakeup call waiting for its result: a denied or
// failed call schedules nothing.
type pendingWakeup struct {
	toolID string
	loop   *ScheduledLoop // nil for stop: true
}

// observeScheduledLoop follows ScheduleWakeup calls and turn ends in a
// session's own (parent) stream.
func (sm *SessionManager) observeScheduledLoop(session *AgentSession, msg types.Message) {
	if _, isResult := msg.(*types.ResultMessage); !isResult {
		session.loopTurnStartedAt.CompareAndSwap(0, time.Now().UnixNano())
	}
	switch m := msg.(type) {
	case *types.AssistantMessage:
		for _, block := range m.Content {
			tool, ok := block.(*types.ToolUseBlock)
			if !ok || tool.Name != "ScheduleWakeup" {
				continue
			}
			if stop, _ := tool.Input["stop"].(bool); stop {
				session.pendingWakeup.Store(&pendingWakeup{toolID: tool.ID})
				continue
			}
			delay, _ := tool.Input["delaySeconds"].(float64)
			seconds := int(delay)
			if seconds < minWakeupDelaySeconds {
				seconds = minWakeupDelaySeconds
			} else if seconds > maxWakeupDelaySeconds {
				seconds = maxWakeupDelaySeconds
			}
			reason, _ := tool.Input["reason"].(string)
			now := time.Now()
			session.pendingWakeup.Store(&pendingWakeup{toolID: tool.ID, loop: &ScheduledLoop{
				NextAt:       now.Add(time.Duration(seconds) * time.Second),
				DelaySeconds: seconds,
				Reason:       firstLine(reason, 160),
				ScheduledAt:  now,
				Kind:         "wakeup",
			}})
		}
	case *types.UserMessage:
		blocks, _ := m.Content.([]types.ContentBlock)
		for _, block := range blocks {
			result, ok := block.(*types.ToolResultBlock)
			if !ok {
				continue
			}
			pending := session.pendingWakeup.Load()
			if pending == nil || pending.toolID != result.ToolUseID || !session.pendingWakeup.CompareAndSwap(pending, nil) {
				continue
			}
			if result.IsError != nil && *result.IsError {
				continue // denied or rejected: nothing was scheduled (or stopped)
			}
			sm.setScheduledLoop(session, pending.loop)
		}
	case *types.ResultMessage:
		// A turn that started once the wakeup was due (the wakeup's own run) and
		// ends without scheduling the next one was the loop's last iteration. A
		// turn that started earlier (a user's, or the one that scheduled it)
		// leaves the wakeup pending: the CLI runs it after that turn.
		started := session.loopTurnStartedAt.Swap(0)
		if loop := session.scheduledLoop.Load(); loop != nil && started != 0 && !time.Unix(0, started).Before(loop.NextAt) {
			sm.setScheduledLoop(session, nil)
		}
	}
}

// setScheduledLoop records (or, with nil, clears) a session's pending wakeup
// and tells the frontend when it changed.
func (sm *SessionManager) setScheduledLoop(session *AgentSession, loop *ScheduledLoop) {
	previous := session.scheduledLoop.Swap(loop)
	if previous == nil && loop == nil {
		return
	}
	if loop != nil {
		logging.Info("🔁 Session %s: Claude Code loop wakes up in %ds", session.ID, loop.DelaySeconds)
	} else {
		logging.Info("🔁 Session %s: Claude Code loop ended", session.ID)
	}
	sm.notifyScheduledLoop(session)
}

// notifyScheduledLoop tells the frontend that a session's wakeup or cron jobs
// changed; the receiver reads the current state itself.
func (sm *SessionManager) notifyScheduledLoop(session *AgentSession) {
	sm.subagentCallbackMu.RLock()
	onChange := sm.scheduledLoopCallback
	sm.subagentCallbackMu.RUnlock()
	if onChange != nil {
		onChange(session.ID, session.scheduledLoop.Load())
	}
}

// SetScheduledLoopCallback registers the receiver told when a session's
// Claude Code loop is scheduled or ends.
func (sm *SessionManager) SetScheduledLoopCallback(callback func(sessionID uuid.UUID, loop *ScheduledLoop)) {
	sm.subagentCallbackMu.Lock()
	defer sm.subagentCallbackMu.Unlock()
	sm.scheduledLoopCallback = callback
}

// ScheduledLoopFor returns what runs next in a live session's CLI: its
// pending /loop wakeup or its soonest cron job, whichever comes first.
func (sm *SessionManager) ScheduledLoopFor(sessionID uuid.UUID) *ScheduledLoop {
	loop, _ := sm.ScheduledFor(sessionID)
	return loop
}

// ScheduledFor returns a live session's next scheduled run (as
// ScheduledLoopFor) and its cron jobs, soonest first.
func (sm *SessionManager) ScheduledFor(sessionID uuid.UUID) (*ScheduledLoop, []ScheduledJob) {
	sm.mu.RLock()
	session := sm.sessions[sessionID]
	sm.mu.RUnlock()
	if session == nil {
		return nil, nil
	}
	now := time.Now()
	loop := session.scheduledLoop.Load()
	jobs := session.scheduledJobs(now)
	for _, job := range jobs {
		if job.NextAt.IsZero() || (loop != nil && !job.NextAt.Before(loop.NextAt)) {
			continue
		}
		reason := job.Schedule
		if prompt := firstLine(job.Prompt, 120); prompt != "" {
			if reason != "" {
				reason += " · "
			}
			reason += prompt
		}
		loop = &ScheduledLoop{
			NextAt:       job.NextAt,
			DelaySeconds: int(job.NextAt.Sub(job.lastBoundary()).Seconds()),
			Reason:       reason,
			ScheduledAt:  job.CreatedAt,
			Kind:         "cron",
		}
		break // jobs are sorted: the first with a time is the soonest
	}
	return loop, jobs
}

// loopBroadcastMu serializes loop broadcasts (see broadcastScheduledLoop).
var loopBroadcastMu sync.Mutex

// broadcastScheduledLoop sends a session's loop as a partial session_updated,
// which web and iOS merge into the session they hold. It runs in the
// background, since callers may hold session locks and a slow client must not
// stall them. Each send reads the loop current at that moment, one at a time,
// so the last send always carries the latest state.
func (h *AgentHandler) broadcastScheduledLoop(sessionID uuid.UUID, _ *ScheduledLoop) {
	go func() {
		loopBroadcastMu.Lock()
		defer loopBroadcastMu.Unlock()
		var loop *ScheduledLoop
		var jobs []ScheduledJob
		if h.SessionManager != nil {
			loop, jobs = h.SessionManager.ScheduledFor(sessionID)
		}
		if jobs == nil {
			jobs = []ScheduledJob{} // an empty list clears the client's copy
		}
		h.broadcastToAllConnections(sessionID, map[string]interface{}{
			"type":       string(MessageTypeSessionUpdated),
			"session_id": sessionID.String(),
			"session":    map[string]interface{}{"scheduled_loop": loop, "scheduled_jobs": jobs},
		})
	}()
}

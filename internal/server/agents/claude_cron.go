package agents

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/logging"
)

// Claude Code's cron jobs.
//
// Besides /loop's ScheduleWakeup (claude_loop.go) an agent can schedule a
// prompt with CronCreate {cron, prompt, recurring}: the CLI runs the prompt as
// a new turn whenever the 5-field cron expression (local time) matches, until
// CronDelete {id}, until it expires (recurring jobs after 7 days) or, for a
// one-shot job, after its single run. Like wakeups, jobs live in the session's
// Claude CLI and die with it (durable ones are written to the project, but
// Eggify only follows the running CLI), so they are kept in memory only.

// ScheduledJob is a Claude Code cron job in a session's CLI.
type ScheduledJob struct {
	ID        string    `json:"id"`
	Cron      string    `json:"cron"`
	Schedule  string    `json:"schedule,omitempty"` // The CLI's own wording: "Every 30 minutes"
	Prompt    string    `json:"prompt,omitempty"`
	Recurring bool      `json:"recurring"`
	Durable   bool      `json:"durable,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"` // Recurring jobs only
	NextAt    time.Time `json:"next_at"`              // Filled in when listed
	LastRunAt time.Time `json:"last_run_at,omitempty"`
}

// The CLI expires recurring jobs after this long.
const cronJobLifetime = 7 * 24 * time.Hour

// A slot that passed this long ago without a run counts as missed.
const cronMissedAfter = time.Hour

// Prompts are shown, not replayed: keep them short.
const maxCronPromptChars = 2000

// pendingCronCall is a CronCreate/CronDelete call waiting for its result.
type pendingCronCall struct {
	job      *ScheduledJob // CronCreate
	deleteID string        // CronDelete
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// "Scheduled recurring job 8841f567 (Every 30 minutes). Session-only …"
var cronCreatedText = regexp.MustCompile(`(?i)scheduled (?:recurring |one-shot |one-time )?job ([0-9a-z-]+)(?: \(([^)]*)\))?`)

// observeCronJobs follows CronCreate/CronDelete calls and the turns the jobs
// start in a session's own (parent) stream. turnStartedAt is when the turn
// that this message belongs to began (0 if unknown).
func (sm *SessionManager) observeCronJobs(session *AgentSession, msg types.Message, turnStartedAt int64) {
	changed := false
	session.cronMu.Lock()
	switch m := msg.(type) {
	case *types.AssistantMessage:
		for _, block := range m.Content {
			tool, ok := block.(*types.ToolUseBlock)
			if !ok {
				continue
			}
			switch tool.Name {
			case "CronCreate":
				expr, _ := tool.Input["cron"].(string)
				prompt, _ := tool.Input["prompt"].(string)
				recurring, _ := tool.Input["recurring"].(bool)
				durable, _ := tool.Input["durable"].(bool)
				if _, hasRecurring := tool.Input["recurring"]; !hasRecurring {
					recurring = true // the CLI's default
				}
				session.pendingCron()[tool.ID] = pendingCronCall{job: &ScheduledJob{
					Cron:      strings.TrimSpace(expr),
					Prompt:    truncateRunes(strings.TrimSpace(prompt), maxCronPromptChars),
					Recurring: recurring,
					Durable:   durable,
				}}
			case "CronDelete":
				id, _ := tool.Input["id"].(string)
				session.pendingCron()[tool.ID] = pendingCronCall{deleteID: strings.TrimSpace(id)}
			}
		}
	case *types.UserMessage:
		blocks, _ := m.Content.([]types.ContentBlock)
		for _, block := range blocks {
			result, ok := block.(*types.ToolResultBlock)
			if !ok {
				continue
			}
			call, ok := session.cronPending[result.ToolUseID]
			if !ok {
				continue
			}
			delete(session.cronPending, result.ToolUseID)
			if result.IsError != nil && *result.IsError {
				continue // denied or rejected: nothing changed
			}
			if call.deleteID != "" {
				changed = session.removeCronJob(call.deleteID) || changed
				continue
			}
			if job := bindCronJob(call.job, m.ToolUseResult, toolResultText(result.Content)); job != nil {
				session.cronJobs = append(session.cronJobs, job)
				changed = true
				logging.Info("⏰ Session %s: Claude Code cron job %s (%s)", session.ID, job.ID, job.Cron)
			}
		}
	case *types.ResultMessage:
		// A turn that started once a job was due was (probably) that job's run:
		// remember it, and a one-shot job is done.
		if turnStartedAt != 0 && len(session.cronJobs) > 0 {
			started := time.Unix(0, turnStartedAt)
			kept := session.cronJobs[:0]
			for _, job := range session.cronJobs {
				if due := job.dueAfter(job.lastBoundary()); !due.IsZero() && !started.Before(due) {
					job.LastRunAt = started
					changed = true
					if !job.Recurring {
						continue
					}
				}
				kept = append(kept, job)
			}
			session.cronJobs = kept
		}
	}
	session.cronMu.Unlock()
	if changed {
		sm.notifyScheduledLoop(session)
	}
}

// bindCronJob completes a CronCreate call from its result: the job id comes
// from tool_use_result ({"id","humanSchedule","recurring","durable"}), else
// from the result text. Without an id nothing is tracked.
func bindCronJob(job *ScheduledJob, meta map[string]interface{}, text string) *ScheduledJob {
	if job == nil {
		return nil
	}
	if id, _ := meta["id"].(string); id != "" {
		job.ID = id
	}
	if human, _ := meta["humanSchedule"].(string); human != "" {
		job.Schedule = human
	}
	if recurring, ok := meta["recurring"].(bool); ok {
		job.Recurring = recurring
	}
	if durable, ok := meta["durable"].(bool); ok {
		job.Durable = durable
	}
	if job.ID == "" {
		if match := cronCreatedText.FindStringSubmatch(text); match != nil {
			job.ID = match[1]
			if job.Schedule == "" {
				job.Schedule = match[2]
			}
		}
	}
	if job.ID == "" {
		return nil
	}
	job.CreatedAt = time.Now()
	if job.Recurring {
		job.ExpiresAt = job.CreatedAt.Add(cronJobLifetime)
	}
	return job
}

// lastBoundary is when the job last ran, or was created.
func (j *ScheduledJob) lastBoundary() time.Time {
	if j.LastRunAt.After(j.CreatedAt) {
		return j.LastRunAt
	}
	return j.CreatedAt
}

// dueAfter is the job's first run after t, or zero when its expression can't
// be read (the job is still listed, without a time).
func (j *ScheduledJob) dueAfter(t time.Time) time.Time {
	schedule, err := cronParser.Parse(j.Cron)
	if err != nil {
		return time.Time{}
	}
	return schedule.Next(t)
}

// live reports whether the job can still run at now.
func (j *ScheduledJob) live(now time.Time) bool {
	return j.ExpiresAt.IsZero() || now.Before(j.ExpiresAt)
}

// removeCronJob drops a job by id. The caller holds cronMu.
func (s *AgentSession) removeCronJob(id string) bool {
	for i, job := range s.cronJobs {
		if job.ID == id {
			s.cronJobs = append(s.cronJobs[:i], s.cronJobs[i+1:]...)
			return true
		}
	}
	return false
}

// pendingCron returns the session's pending-call map, creating it. The caller
// holds cronMu.
func (s *AgentSession) pendingCron() map[string]pendingCronCall {
	if s.cronPending == nil {
		s.cronPending = map[string]pendingCronCall{}
	}
	return s.cronPending
}

// clearCronJobs forgets every job (the CLI holding them closed). The caller
// tells the frontend.
func (s *AgentSession) clearCronJobs() bool {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	had := len(s.cronJobs) > 0
	s.cronJobs = nil
	s.cronPending = nil
	return had
}

// scheduledJobs lists the session's live jobs with their next run, soonest first.
func (s *AgentSession) scheduledJobs(now time.Time) []ScheduledJob {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	if len(s.cronJobs) == 0 {
		return nil
	}
	jobs := make([]ScheduledJob, 0, len(s.cronJobs))
	for _, job := range s.cronJobs {
		if !job.live(now) {
			continue
		}
		listed := *job
		if job.Recurring {
			// The CLI starts a run up to several minutes after its slot (it
			// spreads jobs out), so a slot that passed without a run is still
			// the next one, unless it was missed long ago.
			listed.NextAt = job.dueAfter(job.lastBoundary())
			if now.Sub(listed.NextAt) > cronMissedAfter {
				listed.NextAt = job.dueAfter(now)
			}
			if !listed.ExpiresAt.IsZero() && listed.NextAt.After(listed.ExpiresAt) {
				continue
			}
		} else {
			// A one-shot job keeps its single due time, even once it passed,
			// until its run ends.
			listed.NextAt = job.dueAfter(job.CreatedAt)
		}
		jobs = append(jobs, listed)
	}
	sort.SliceStable(jobs, func(a, b int) bool {
		if jobs[a].NextAt.IsZero() != jobs[b].NextAt.IsZero() {
			return !jobs[a].NextAt.IsZero()
		}
		return jobs[a].NextAt.Before(jobs[b].NextAt)
	})
	return jobs
}

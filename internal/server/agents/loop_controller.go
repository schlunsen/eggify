package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/logging"
)

// Loop mode tuning constants.
const (
	// DefaultLoopMaxIterations is the fallback cap on loop iterations when a
	// session's LoopConfig doesn't specify MaxIterations.
	DefaultLoopMaxIterations = 10

	// DefaultLoopTimeoutMinutes is the fallback wall-clock budget for a loop when
	// a session's LoopConfig doesn't specify TimeoutMinutes.
	DefaultLoopTimeoutMinutes = 30

	// loopVerifyTimeout bounds how long a single verify command may run.
	loopVerifyTimeout = 3 * time.Minute

	// loopInterPromptDelay gives the SDK a moment to settle between iterations.
	loopInterPromptDelay = 500 * time.Millisecond

	// loopVerifyOutputLimit caps how much verify output we feed back to the agent.
	loopVerifyOutputLimit = 4000

	// loopNoProgressLimit stops a loop whose verify command fails with the same
	// output this many iterations in a row: the agent is not making progress,
	// and more iterations only burn time and tokens.
	loopNoProgressLimit = 3
)

// isLoopMode reports whether a session is configured to run in autonomous loop mode.
func (sm *SessionManager) isLoopMode(session *AgentSession) bool {
	return session.Options.Mode != nil &&
		*session.Options.Mode == SessionModeLoop &&
		session.Options.Loop != nil
}

// resetLoopState clears loop runtime state so the next completed turn starts a
// fresh loop. Called when the user sends a new (non-silent) prompt in loop mode.
//
// This is the only place loopStopped is cleared: an explicit user prompt is the
// one unambiguous signal that a new loop is wanted.
func (sm *SessionManager) resetLoopState(session *AgentSession) {
	session.loopMu.Lock()
	session.loopActive = false
	session.loopStopped = false
	session.loopIteration = 0
	// The timeout budget starts with the prompt that kicks the loop off, not
	// when its first turn ends — otherwise that first turn is unbounded.
	session.loopStartedAt = time.Now()
	session.loopGen++
	gen := session.loopGen
	session.loopFailSig = ""
	session.loopFailSeen = 0
	session.loopMu.Unlock()

	if session.Options.Loop != nil {
		_, timeoutMin := loopGuards(session.Options.Loop)
		sm.armLoopWatchdog(session, gen, time.Duration(timeoutMin)*time.Minute)
	}
}

// loopWatchdogAfter is a seam for tests.
var loopWatchdogAfter = time.AfterFunc

// armLoopWatchdog enforces the loop's wall-clock budget even while a turn is
// running. The per-turn check in onLoopTurnComplete only fires between turns,
// so a single long (or hung) turn could otherwise run far past the timeout.
// When the budget is spent the loop is stopped and a turn in progress is
// interrupted.
func (sm *SessionManager) armLoopWatchdog(session *AgentSession, gen uint64, budget time.Duration) {
	loopWatchdogAfter(budget, func() {
		session.loopMu.Lock()
		if session.loopGen != gen || session.loopStopped {
			session.loopMu.Unlock()
			return
		}
		session.loopActive = false
		session.loopStopped = true
		iteration := session.loopIteration
		session.loopMu.Unlock()

		sm.mu.RLock()
		processing := session.Status == SessionStatusProcessing
		sm.mu.RUnlock()

		cfg := session.Options.Loop
		maxIter := DefaultLoopMaxIterations
		if cfg != nil {
			maxIter, _ = loopGuards(cfg)
		}
		reason := fmt.Sprintf("timeout after %d min", int(budget.Minutes()))
		logging.Info("🔁 Session %s: LOOP STOPPED by watchdog — %s (turn running: %v)", session.ID, reason, processing)
		f := false
		sm.broadcastLoop(session, MessageTypeLoopFailed, iteration, maxIter, cfg, &f, "", reason)
		if processing {
			if err := sm.InterruptSession(session.ID); err != nil {
				logging.Warning("🔁 Session %s: watchdog could not interrupt the running turn: %v", session.ID, err)
			}
		}
	})
}

// loopAfterTurn is called by every execution path (Claude SDK, direct
// provider, Codex) when a turn ends. A turn that ended in an error stops the
// loop — re-prompting into the same broken provider/API key would only fail
// again until the guards trip. Otherwise the normal verify-and-retry runs.
func (sm *SessionManager) loopAfterTurn(session *AgentSession, turnErr error) {
	if !sm.isLoopMode(session) {
		return
	}
	if turnErr == nil {
		sm.mu.RLock()
		if session.ErrorMessage != nil && *session.ErrorMessage != "" {
			turnErr = errors.New(*session.ErrorMessage)
		}
		sm.mu.RUnlock()
	}
	if turnErr == nil {
		sm.onLoopTurnComplete(session)
		return
	}

	session.loopMu.Lock()
	if session.loopStopped {
		// Already stopped (user stop, interrupt, watchdog): nothing to report.
		session.loopMu.Unlock()
		return
	}
	session.loopActive = false
	session.loopStopped = true
	iteration := session.loopIteration
	session.loopMu.Unlock()

	maxIter, _ := loopGuards(session.Options.Loop)
	f := false
	reason := "the agent hit an error: " + truncateLoopOutput(turnErr.Error(), 300)
	logging.Info("🔁 Session %s: LOOP STOPPED — %s", session.ID, reason)
	sm.broadcastLoop(session, MessageTypeLoopFailed, iteration, maxIter, session.Options.Loop, &f, "", reason)
}

// stopLoop puts the loop in a terminal state. Every caller (user stop, verify
// passed, guard hit, send failure) means "this loop is finished", so we latch
// loopStopped as well as clearing loopActive.
//
// Clearing loopActive alone is NOT enough: onLoopTurnComplete treats
// !loopActive as "no loop running, start one", so a turn that completes after a
// stop would restart the loop and reset both guards. The latch is what makes a
// stop stick until the user sends a real prompt.
func (sm *SessionManager) stopLoop(session *AgentSession) {
	session.loopMu.Lock()
	session.loopActive = false
	session.loopStopped = true
	session.loopMu.Unlock()
}

// isLoopActive reports whether the loop is currently active. Used by the verify
// goroutine to bail out if the user stopped/interrupted the loop while a
// (potentially multi-minute) verify command was running.
func (sm *SessionManager) isLoopActive(session *AgentSession) bool {
	session.loopMu.Lock()
	defer session.loopMu.Unlock()
	return session.loopActive
}

// StopLoop stops a running loop on demand (e.g. user clicked "Stop loop") and
// notifies clients. Safe to call even if no loop is active.
func (sm *SessionManager) StopLoop(sessionID uuid.UUID) error {
	session, err := sm.GetSession(sessionID)
	if err != nil {
		return err
	}

	session.loopMu.Lock()
	wasActive := session.loopActive
	iteration := session.loopIteration
	session.loopActive = false
	// Latch even when no loop was active: the user may have clicked "Stop loop"
	// in the gap between a turn completing and the next iteration arming, and
	// their intent is "do not loop again" either way.
	session.loopStopped = true
	session.loopMu.Unlock()

	if !wasActive {
		return nil
	}

	maxIter := DefaultLoopMaxIterations
	if session.Options.Loop != nil {
		maxIter, _ = loopGuards(session.Options.Loop)
	}
	logging.Info("🔁 Session %s: loop STOPPED by user at iteration %d", sessionID, iteration)
	sm.broadcastLoop(session, MessageTypeLoopStopped, iteration, maxIter, session.Options.Loop, nil, "", "stopped by user")
	return nil
}

// loopGuards resolves the effective max-iteration and timeout guards for a loop.
func loopGuards(cfg *LoopConfig) (maxIter, timeoutMin int) {
	maxIter = DefaultLoopMaxIterations
	if cfg.MaxIterations != nil && *cfg.MaxIterations > 0 {
		maxIter = *cfg.MaxIterations
	}
	timeoutMin = DefaultLoopTimeoutMinutes
	if cfg.TimeoutMinutes != nil && *cfg.TimeoutMinutes > 0 {
		timeoutMin = *cfg.TimeoutMinutes
	}
	return maxIter, timeoutMin
}

// onLoopTurnComplete is the heart of loop mode. It is invoked from
// completeTurn after every completed turn. It starts the
// loop on the first turn, then for each subsequent turn runs the verification
// check and decides whether to stop (verify passed / guard hit) or to silently
// re-prompt the agent for another iteration.
//
// The loop is driven by chained silent prompts rather than an internal for-loop:
// each SendPromptSilent produces a new turn whose completion calls this method
// again. This keeps everything on the existing per-turn pipeline.
func (sm *SessionManager) onLoopTurnComplete(session *AgentSession) {
	if !sm.isLoopMode(session) {
		return
	}
	cfg := session.Options.Loop
	if cfg == nil {
		return
	}

	session.loopMu.Lock()
	// A stopped loop must not be revived by a turn that was already in flight
	// when the stop landed. Without this check, !loopActive reads as "start a
	// new loop", which resets loopIteration and loopStartedAt and so defeats
	// both the max-iterations and timeout guards — an unstoppable loop.
	if session.loopStopped {
		session.loopMu.Unlock()
		logging.Info("🔁 Session %s: loop is stopped, ignoring turn completion", session.ID)
		return
	}
	starting := !session.loopActive
	if starting {
		session.loopActive = true
		// Normally set when the kickoff prompt was sent (resetLoopState).
		if session.loopStartedAt.IsZero() {
			session.loopStartedAt = time.Now()
		}
		session.loopIteration = 0
	}
	session.loopIteration++
	iteration := session.loopIteration
	startedAt := session.loopStartedAt
	session.loopMu.Unlock()

	maxIter, timeoutMin := loopGuards(cfg)

	if starting {
		logging.Info("🔁 Session %s: LOOP STARTED (goal=%q, verify=%q, max=%d, timeout=%dm)",
			session.ID, cfg.Goal, cfg.VerifyCommand, maxIter, timeoutMin)
		sm.broadcastLoop(session, MessageTypeLoopStarted, iteration-1, maxIter, cfg, nil, "", "loop started")
	}

	// Run verification + the continue/stop decision in a goroutine so we don't
	// block the receiver goroutine from exiting its defer.
	go func() {
		hasVerify := strings.TrimSpace(cfg.VerifyCommand) != ""

		// The agent can end the loop itself: BLOCKED hands control back to the
		// human; DONE finishes a loop that has no verify command (with one,
		// the verify command stays the gate — "tests pass", not "I think so").
		signal, detail := parseLoopPromise(sm.lastAssistantText(session.ID))
		if signal == "blocked" {
			sm.stopLoop(session)
			f := false
			reason := "the agent is blocked"
			if detail != "" {
				reason += ": " + truncateLoopOutput(detail, 300)
			}
			logging.Info("🔁 Session %s: LOOP STOPPED — %s", session.ID, reason)
			sm.broadcastLoop(session, MessageTypeLoopStopped, iteration, maxIter, cfg, &f, "", reason)
			return
		}
		if signal == "done" && !hasVerify {
			sm.stopLoop(session)
			p := true
			logging.Info("🔁 Session %s: LOOP COMPLETED at iteration %d (agent reported done)", session.ID, iteration)
			sm.broadcastLoop(session, MessageTypeLoopCompleted, iteration, maxIter, cfg, &p, "", "the agent reported the goal is done")
			return
		}

		sm.broadcastLoop(session, MessageTypeLoopVerifying, iteration, maxIter, cfg, nil, "", "verifying")

		passed, output := sm.runLoopVerify(session, cfg)
		output = truncateLoopOutput(output, loopVerifyOutputLimit)

		// Success: verification passed.
		if passed {
			sm.stopLoop(session)
			p := true
			logging.Info("🔁 Session %s: LOOP COMPLETED at iteration %d (verification passed)", session.ID, iteration)
			sm.broadcastLoop(session, MessageTypeLoopCompleted, iteration, maxIter, cfg, &p, output, "verification passed")
			return
		}

		// Guard: timeout budget exceeded.
		if time.Since(startedAt) > time.Duration(timeoutMin)*time.Minute {
			sm.stopLoop(session)
			f := false
			reason := fmt.Sprintf("timeout after %d min", timeoutMin)
			logging.Info("🔁 Session %s: LOOP STOPPED — %s", session.ID, reason)
			sm.broadcastLoop(session, MessageTypeLoopFailed, iteration, maxIter, cfg, &f, output, reason)
			return
		}

		// Guard: max iterations reached.
		if iteration >= maxIter {
			sm.stopLoop(session)
			msgType := MessageTypeLoopFailed
			reason := fmt.Sprintf("reached max iterations (%d) without passing verification", maxIter)
			verdict := false
			if !hasVerify {
				// No verify command: the loop was just "run N times", which is a
				// successful completion, not a failure.
				msgType = MessageTypeLoopCompleted
				reason = fmt.Sprintf("completed %d iterations", maxIter)
				verdict = true
			}
			logging.Info("🔁 Session %s: LOOP ENDED — %s", session.ID, reason)
			sm.broadcastLoop(session, msgType, iteration, maxIter, cfg, &verdict, output, reason)
			return
		}

		// Guard: no progress. The same failure again and again means the agent
		// is stuck; stop instead of burning the remaining iterations.
		repeats := 0
		if hasVerify {
			sig := loopFailureSignature(output)
			session.loopMu.Lock()
			if sig != "" && sig == session.loopFailSig {
				session.loopFailSeen++
			} else {
				session.loopFailSig = sig
				session.loopFailSeen = 1
			}
			seen := session.loopFailSeen
			session.loopMu.Unlock()
			repeats = seen
			if seen >= loopNoProgressLimit {
				sm.stopLoop(session)
				f := false
				reason := fmt.Sprintf("no progress: the check failed the same way %d iterations in a row", seen)
				logging.Info("🔁 Session %s: LOOP STOPPED — %s", session.ID, reason)
				sm.broadcastLoop(session, MessageTypeLoopFailed, iteration, maxIter, cfg, &f, output, reason)
				return
			}
		}

		// Bail out if the loop was stopped/interrupted while verify was running.
		// runLoopVerify can block for minutes, during which the user may have
		// clicked "Stop loop" or interrupted the session. Without this re-check we
		// would re-prompt a session the user explicitly took back control of (and,
		// because the silent retry never resets loop state, the next turn would
		// restart the loop from iteration 0).
		if !sm.isLoopActive(session) {
			logging.Info("🔁 Session %s: loop stopped during verification, not re-prompting", session.ID)
			return
		}

		// Otherwise: re-prompt the agent for another iteration.
		f := false
		sm.broadcastLoop(session, MessageTypeLoopIteration, iteration, maxIter, cfg, &f, output, "verification failed, retrying")
		time.Sleep(loopInterPromptDelay)

		retryPrompt := buildLoopRetryPrompt(cfg, iteration+1, maxIter, output, repeats, signal == "done")
		logging.Info("🔁 Session %s: loop iteration %d failed verification, re-prompting (next: %d/%d)",
			session.ID, iteration, iteration+1, maxIter)
		if err := sm.SendPromptSilent(session.ID, retryPrompt); err != nil {
			sm.stopLoop(session)
			f2 := false
			logging.Error("🔁 Session %s: failed to send loop retry prompt: %v", session.ID, err)
			sm.broadcastLoop(session, MessageTypeLoopFailed, iteration, maxIter, cfg, &f2, "", "failed to send retry prompt: "+err.Error())
		}
	}()
}

// runLoopVerify executes the configured verification command in the session's
// working directory. Returns (passed, combinedOutput). When no command is
// configured it returns (false, "") so the loop falls back to MaxIterations.
func (sm *SessionManager) runLoopVerify(session *AgentSession, cfg *LoopConfig) (bool, string) {
	command := strings.TrimSpace(cfg.VerifyCommand)
	if command == "" {
		return false, ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), loopVerifyTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	if session.Options.WorkingDirectory != nil && *session.Options.WorkingDirectory != "" {
		cmd.Dir = *session.Options.WorkingDirectory
	}
	// Non-interactive: no prompts, no pagers, no colour codes in the output
	// that is fed back to the agent.
	cmd.Env = append(os.Environ(), "CI=1", "NO_COLOR=1", "TERM=dumb", "GIT_PAGER=cat", "PAGER=cat")
	cmd.Stdin = nil
	configureVerifyProcess(cmd)
	cmd.WaitDelay = 5 * time.Second

	out, err := cmd.CombinedOutput()
	output := string(out)

	if ctx.Err() == context.DeadlineExceeded {
		return false, output + fmt.Sprintf("\n... (verify command timed out after %s)", loopVerifyTimeout)
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			output += fmt.Sprintf("\n(exit code %d)", exitErr.ExitCode())
		} else {
			output += "\n(" + err.Error() + ")"
		}
		return false, output
	}
	return true, output
}

// loopPromise matches the agent's completion signal: <promise>DONE</promise>
// or <promise>BLOCKED: why</promise>.
var loopPromise = regexp.MustCompile(`(?is)<promise>\s*(DONE|BLOCKED)\b[:\s-]*(.*?)\s*</promise>`)

// parseLoopPromise returns "done", "blocked" or "" and any detail text.
func parseLoopPromise(text string) (signal, detail string) {
	m := loopPromise.FindAllStringSubmatch(text, -1)
	if len(m) == 0 {
		return "", ""
	}
	last := m[len(m)-1] // the agent's final word counts
	return strings.ToLower(last[1]), strings.TrimSpace(last[2])
}

// lastAssistantText returns the latest assistant text of a session (the end
// of the turn that just completed).
func (sm *SessionManager) lastAssistantText(sessionID uuid.UUID) string {
	if sm.db == nil {
		return ""
	}
	var content string
	err := sm.db.QueryRow(`SELECT content FROM agent_messages
		WHERE session_id = ? AND role = 'assistant' AND content != ''
		ORDER BY sequence DESC LIMIT 1`, sessionID.String()).Scan(&content)
	if err != nil {
		return ""
	}
	return content
}

// buildLoopRetryPrompt constructs the silent continuation prompt fed back to the
// agent when verification fails. It restates the goal and includes the failing
// verify output so the agent addresses the root cause.
func buildLoopRetryPrompt(cfg *LoopConfig, nextIteration, maxIter int, verifyOutput string, sameFailureCount int, claimedDone bool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("SYSTEM: LOOP MODE — autonomous iteration %d of %d.\n\n", nextIteration, maxIter))
	b.WriteString("Start by reading `.eggify/loop-progress.md` (your notes from earlier iterations) and end by updating it: what you tried, what you learned, what is left.\n\n")

	if strings.TrimSpace(cfg.Goal) != "" {
		b.WriteString("## Goal\n")
		b.WriteString(cfg.Goal)
		b.WriteString("\n\n")
	}

	if strings.TrimSpace(cfg.VerifyCommand) != "" {
		b.WriteString("## Verification still failing\n")
		b.WriteString(fmt.Sprintf("The check `%s` did not pass. Its output was:\n\n", cfg.VerifyCommand))
		b.WriteString("```\n")
		b.WriteString(strings.TrimSpace(verifyOutput))
		b.WriteString("\n```\n\n")
		if claimedDone {
			b.WriteString("You reported the goal as done, but the check still fails — it decides, not the claim.\n\n")
		}
		if sameFailureCount >= 2 {
			b.WriteString(fmt.Sprintf("This is the same failure as the previous iteration (%d in a row): the last approach did not work. Step back, re-read the error from the top, and try a different approach instead of repeating the same fix. Another identical failure stops the loop.\n\n", sameFailureCount))
		}
		b.WriteString("Address the root cause of the failure and continue. Do NOT suppress the error, skip or weaken tests, or edit the check itself. The check re-runs automatically after your turn.\n\n")
		b.WriteString("If you cannot make progress without the user (missing credentials, an unclear requirement, a decision only they can make), end your reply with `<promise>BLOCKED: <reason></promise>` and the loop will stop.")
	} else {
		b.WriteString("Continue working toward the goal. When it is fully complete, end your reply with `<promise>DONE</promise>`. If you cannot continue without the user, end with `<promise>BLOCKED: <reason></promise>`.")
	}

	return b.String()
}

// broadcastLoop sends a LoopMessage of the given type to the session's clients.
func (sm *SessionManager) broadcastLoop(session *AgentSession, msgType MessageType, iteration, maxIter int, cfg *LoopConfig, passed *bool, output, reason string) {
	msg := LoopMessage{
		BaseMessage:   BaseMessage{Type: msgType},
		SessionID:     session.ID,
		Iteration:     iteration,
		MaxIterations: maxIter,
		Reason:        reason,
		VerifyOutput:  output,
		VerifyPassed:  passed,
	}
	if cfg != nil {
		msg.Goal = cfg.Goal
		msg.VerifyCommand = cfg.VerifyCommand
	}
	session.loopMu.Lock()
	last := msg
	session.lastLoopEvent = &last
	session.loopMu.Unlock()
	sm.broadcastToSessionConns(session.ID, msg)

	if isTerminalLoopEvent(msgType) {
		// A loop started by another agent session reports back to it, and
		// messages from it that waited for the next iteration are delivered now.
		go func() {
			sm.reportLoopToParent(session, msg)
			sm.drainInbox(session)
		}()
	}
}

// truncateLoopOutput trims verify output to about max bytes. It keeps the
// head (compilers and type checkers report the first error there) and the
// tail (test runners summarise at the end), and never cuts a UTF-8 character
// in half — the output goes into a prompt and a JSON message.
func truncateLoopOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	headLen := max / 4
	tailLen := max - headLen
	head := s[:headLen]
	for len(head) > 0 && !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	tail := s[len(s)-tailLen:]
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	omitted := len(s) - len(head) - len(tail)
	return fmt.Sprintf("%s\n... (%d bytes omitted) ...\n%s", head, omitted, tail)
}

var loopVolatile = regexp.MustCompile(`0x[0-9a-fA-F]+|[0-9]+(\.[0-9]+)?(ms|s|m|µs)?`)

// loopFailureSignature normalises verify output so that two failures that
// differ only in timings, counters or addresses compare equal.
func loopFailureSignature(output string) string {
	s := loopVolatile.ReplaceAllString(output, "#")
	return strings.Join(strings.Fields(s), " ")
}

// LastLoopEvent returns a copy of the session's latest loop event (nil when
// it has not looped since the server started).
func (s *AgentSession) LastLoopEvent() *LoopMessage {
	s.loopMu.Lock()
	defer s.loopMu.Unlock()
	if s.lastLoopEvent == nil {
		return nil
	}
	ev := *s.lastLoopEvent
	return &ev
}

// AddLoopStates fills in each live loop session's latest loop event for a
// listing. Verify output is cut short; the full text is in the event stream.
func (sm *SessionManager) AddLoopStates(sessions []Session) {
	if sm == nil {
		return
	}
	for i := range sessions {
		sm.mu.RLock()
		live := sm.sessions[sessions[i].ID]
		sm.mu.RUnlock()
		if live == nil {
			continue
		}
		if ev := live.LastLoopEvent(); ev != nil {
			ev.VerifyOutput = truncateLoopOutput(ev.VerifyOutput, 2000)
			sessions[i].LoopState = ev
		}
	}
}

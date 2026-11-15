package agents

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// newLoopTestSession builds a minimal loop-mode session. It deliberately avoids
// the SDK/websocket machinery: every assertion here is about the loop state
// machine, which is guarded solely by session.loopMu.
func newLoopTestSession(t *testing.T, cfg *LoopConfig) *AgentSession {
	t.Helper()

	mode := SessionModeLoop
	return &AgentSession{
		Session: Session{
			ID: uuid.New(),
			Options: SessionOptions{
				Mode: &mode,
				Loop: cfg,
			},
		},
	}
}

func loopState(session *AgentSession) (active, stopped bool, iteration int) {
	session.loopMu.Lock()
	defer session.loopMu.Unlock()
	return session.loopActive, session.loopStopped, session.loopIteration
}

// TestOnLoopTurnComplete_StoppedLoopDoesNotRestart is the regression test for
// the "unstoppable loop" bug.
//
// onLoopTurnComplete reads !loopActive as "no loop running, start a fresh one".
// Because stopping a loop clears loopActive, a turn that was already in flight
// when the stop landed used to complete and be misread as the start of a brand
// new loop — resetting loopIteration to 0 and loopStartedAt to now, which
// defeated both the max-iterations and timeout guards. The loop could then
// never terminate by any means.
func TestOnLoopTurnComplete_StoppedLoopDoesNotRestart(t *testing.T) {
	sm := &SessionManager{}
	session := newLoopTestSession(t, &LoopConfig{
		Goal:          "make the check pass",
		VerifyCommand: "false", // always fails, so a live loop would re-prompt
	})

	// Simulate a loop that ran a few iterations and was then stopped by the user.
	session.loopMu.Lock()
	session.loopActive = true
	session.loopIteration = 7
	session.loopStartedAt = time.Now().Add(-5 * time.Minute)
	session.loopMu.Unlock()

	sm.stopLoop(session)

	// The turn that was in flight when the stop landed now completes.
	sm.onLoopTurnComplete(session)

	active, stopped, iteration := loopState(session)
	if active {
		t.Error("loop restarted after being stopped: loopActive is true")
	}
	if !stopped {
		t.Error("loopStopped latch was cleared by a turn completion")
	}
	// A restart would have reset the counter to 0 and then incremented to 1.
	// Preserving 7 proves the guards were not rearmed.
	if iteration != 7 {
		t.Errorf("loop iteration counter was reset: got %d, want 7 (a reset defeats the max-iterations guard)", iteration)
	}
}

// TestOnLoopTurnComplete_RepeatedStopsStayStopped covers the observed symptom:
// the user pressing stop/interrupt repeatedly, each producing a "Stopped." turn.
// Every one of those completions must be inert.
func TestOnLoopTurnComplete_RepeatedStopsStayStopped(t *testing.T) {
	sm := &SessionManager{}
	session := newLoopTestSession(t, &LoopConfig{VerifyCommand: "false"})

	session.loopMu.Lock()
	session.loopActive = true
	session.loopIteration = 2
	session.loopStartedAt = time.Now()
	session.loopMu.Unlock()

	sm.stopLoop(session)

	for i := 0; i < 5; i++ {
		sm.onLoopTurnComplete(session)
		if active, _, _ := loopState(session); active {
			t.Fatalf("loop became active again on turn completion %d", i+1)
		}
	}

	if _, _, iteration := loopState(session); iteration != 2 {
		t.Errorf("iteration drifted across repeated stopped turns: got %d, want 2", iteration)
	}
}

// TestInterruptLatchesLoopStopped documents the invariant that the interrupt
// path in InterruptSession relies on. The interrupt itself produces a final
// "Stopped." turn, so clearing loopActive alone would let that turn restart the
// loop. InterruptSession sets both flags inline (it already holds sm.mu), so
// this test asserts the resulting state is what onLoopTurnComplete needs.
func TestInterruptLatchesLoopStopped(t *testing.T) {
	sm := &SessionManager{}
	session := newLoopTestSession(t, &LoopConfig{VerifyCommand: "false"})

	session.loopMu.Lock()
	session.loopActive = true
	session.loopIteration = 3
	session.loopMu.Unlock()

	// Mirror what InterruptSession does to the loop flags.
	session.loopMu.Lock()
	session.loopActive = false
	session.loopStopped = true
	session.loopMu.Unlock()

	sm.onLoopTurnComplete(session)

	if active, stopped, _ := loopState(session); active || !stopped {
		t.Errorf("interrupted loop was revived: active=%v stopped=%v, want active=false stopped=true", active, stopped)
	}
}

// TestResetLoopStateClearsStopLatch verifies the escape hatch: an explicit user
// prompt is the one signal that legitimately starts a new loop. If reset failed
// to clear the latch, loop mode would be permanently dead after the first stop.
func TestResetLoopStateClearsStopLatch(t *testing.T) {
	sm := &SessionManager{}
	session := newLoopTestSession(t, &LoopConfig{VerifyCommand: "false"})

	sm.stopLoop(session)
	if _, stopped, _ := loopState(session); !stopped {
		t.Fatal("stopLoop did not latch loopStopped")
	}

	sm.resetLoopState(session)

	active, stopped, iteration := loopState(session)
	if stopped {
		t.Error("resetLoopState left loopStopped latched; loop mode would be permanently disabled")
	}
	if active {
		t.Error("resetLoopState should leave the loop inactive until a turn completes")
	}
	if iteration != 0 {
		t.Errorf("resetLoopState should zero the iteration counter, got %d", iteration)
	}
}

// TestStopLoopIsIdempotentAndConcurrencySafe runs the stop path against
// concurrent turn completions, which is exactly the real-world shape of the bug:
// the user clicks stop while the receive goroutine is finishing a turn.
func TestStopLoopIsIdempotentAndConcurrencySafe(t *testing.T) {
	sm := &SessionManager{}
	session := newLoopTestSession(t, &LoopConfig{VerifyCommand: "false"})

	session.loopMu.Lock()
	session.loopActive = true
	session.loopIteration = 1
	session.loopStartedAt = time.Now()
	session.loopMu.Unlock()

	var wg sync.WaitGroup
	// One goroutine stops repeatedly, others complete turns. Whatever the
	// interleaving, once a stop has been observed the loop must never come back.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			sm.stopLoop(session)
		}
	}()

	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				sm.onLoopTurnComplete(session)
			}
		}()
	}
	wg.Wait()

	// The stopper always wins: stopLoop latches, and the latch is only cleared
	// by resetLoopState, which nothing here calls.
	if active, stopped, _ := loopState(session); active || !stopped {
		t.Errorf("loop survived concurrent stops: active=%v stopped=%v", active, stopped)
	}
}

// TestLoopGuardsDefaults pins the fallback guards. These are the last line of
// defence against a runaway loop, so a silent change to either constant should
// break a test.
func TestLoopGuardsDefaults(t *testing.T) {
	maxIter, timeoutMin := loopGuards(&LoopConfig{})
	if maxIter != DefaultLoopMaxIterations {
		t.Errorf("max iterations: got %d, want %d", maxIter, DefaultLoopMaxIterations)
	}
	if timeoutMin != DefaultLoopTimeoutMinutes {
		t.Errorf("timeout minutes: got %d, want %d", timeoutMin, DefaultLoopTimeoutMinutes)
	}

	zero := 0
	negative := -1
	maxIter, timeoutMin = loopGuards(&LoopConfig{MaxIterations: &zero, TimeoutMinutes: &negative})
	if maxIter != DefaultLoopMaxIterations {
		t.Errorf("zero max iterations must fall back to the default, got %d", maxIter)
	}
	if timeoutMin != DefaultLoopTimeoutMinutes {
		t.Errorf("negative timeout must fall back to the default, got %d", timeoutMin)
	}

	twentyFive := 25
	five := 5
	maxIter, timeoutMin = loopGuards(&LoopConfig{MaxIterations: &twentyFive, TimeoutMinutes: &five})
	if maxIter != 25 || timeoutMin != 5 {
		t.Errorf("explicit guards not honoured: got max=%d timeout=%d, want 25/5", maxIter, timeoutMin)
	}
}

func TestTruncateLoopOutputKeepsHeadTailAndUTF8(t *testing.T) {
	// A long output whose first error is at the top and summary at the bottom,
	// with multi-byte characters around the cut points.
	s := "FIRST ERROR: ✗ main.go:1\n" + strings.Repeat("ålpha βeta γ ", 800) + "\nSUMMARY: 3 failed ✗"
	got := truncateLoopOutput(s, 400)
	if len(got) > 460 {
		t.Fatalf("truncated output too long: %d", len(got))
	}
	if !strings.HasPrefix(got, "FIRST ERROR") {
		t.Errorf("head (first error) was dropped: %q", got[:40])
	}
	if !strings.HasSuffix(got, "SUMMARY: 3 failed ✗") {
		t.Errorf("tail (summary) was dropped")
	}
	if !utf8.ValidString(got) {
		t.Error("truncation split a UTF-8 character")
	}
	if short := truncateLoopOutput("ok", 400); short != "ok" {
		t.Errorf("short output changed: %q", short)
	}
}

func TestLoopFailureSignatureIgnoresTimings(t *testing.T) {
	a := loopFailureSignature("FAIL TestX (0.12s)\n  expected 1, got 2\nok  pkg  1.234s")
	b := loopFailureSignature("FAIL TestX (0.98s)\n  expected 1, got 2\nok  pkg  0.5s")
	if a != b {
		t.Errorf("same failure with different timings should match:\n%q\n%q", a, b)
	}
	c := loopFailureSignature("FAIL TestY\n  expected true")
	if a == c {
		t.Error("different failures should not match")
	}
}

func TestLoopAfterTurnErrorStopsLoop(t *testing.T) {
	sm := &SessionManager{}
	session := newLoopTestSession(t, &LoopConfig{Goal: "g", VerifyCommand: "false"})
	session.loopMu.Lock()
	session.loopActive = true
	session.loopIteration = 2
	session.loopMu.Unlock()

	sm.loopAfterTurn(session, errors.New("invalid API key"))

	active, stopped, iteration := loopState(session)
	if active || !stopped {
		t.Errorf("an errored turn must stop the loop (active=%v stopped=%v)", active, stopped)
	}
	if iteration != 2 {
		t.Errorf("iteration changed: %d", iteration)
	}
}

func TestLoopAfterTurnErrorMessageOnSessionStopsLoop(t *testing.T) {
	sm := &SessionManager{}
	session := newLoopTestSession(t, &LoopConfig{Goal: "g"})
	msg := "provider returned 500"
	session.ErrorMessage = &msg
	session.loopMu.Lock()
	session.loopActive = true
	session.loopMu.Unlock()

	sm.loopAfterTurn(session, nil)

	if _, stopped, _ := loopState(session); !stopped {
		t.Error("a turn that left an error on the session must stop the loop")
	}
}

func TestLoopWatchdogStopsLoopAndIgnoresStaleGenerations(t *testing.T) {
	var fire []func()
	orig := loopWatchdogAfter
	loopWatchdogAfter = func(_ time.Duration, f func()) *time.Timer {
		fire = append(fire, f)
		return nil
	}
	defer func() { loopWatchdogAfter = orig }()

	sm := &SessionManager{}
	session := newLoopTestSession(t, &LoopConfig{Goal: "g"})

	sm.resetLoopState(session) // loop 1 armed
	sm.resetLoopState(session) // user sent another prompt: loop 2 armed
	if len(fire) != 2 {
		t.Fatalf("expected 2 watchdogs, got %d", len(fire))
	}
	// Loop 1's watchdog fires late: it must not touch loop 2.
	fire[0]()
	if _, stopped, _ := loopState(session); stopped {
		t.Fatal("a stale watchdog stopped the current loop")
	}
	// Loop 2's budget runs out.
	session.loopMu.Lock()
	session.loopActive = true
	session.loopMu.Unlock()
	fire[1]()
	if active, stopped, _ := loopState(session); active || !stopped {
		t.Errorf("watchdog must stop the loop (active=%v stopped=%v)", active, stopped)
	}
}

func TestResetLoopStateStartsTheClock(t *testing.T) {
	orig := loopWatchdogAfter
	loopWatchdogAfter = func(time.Duration, func()) *time.Timer { return nil }
	defer func() { loopWatchdogAfter = orig }()

	sm := &SessionManager{}
	session := newLoopTestSession(t, &LoopConfig{Goal: "g"})
	before := time.Now()
	sm.resetLoopState(session)
	session.loopMu.Lock()
	started := session.loopStartedAt
	session.loopMu.Unlock()
	if started.Before(before) {
		t.Error("the timeout clock must start when the loop is kicked off")
	}
}

func TestOnLoopTurnCompleteStopsWhenNoProgress(t *testing.T) {
	sm := &SessionManager{}
	session := newLoopTestSession(t, &LoopConfig{
		Goal:          "g",
		VerifyCommand: "echo 'FAIL TestX: expected 1 got 2'; exit 1",
	})
	// Two earlier iterations already failed exactly like this one will.
	session.loopMu.Lock()
	session.loopActive = true
	session.loopIteration = 2
	session.loopStartedAt = time.Now()
	// runLoopVerify appends the exit code to the output
	session.loopFailSig = loopFailureSignature("FAIL TestX: expected 1 got 2\n\n(exit code 1)")
	session.loopFailSeen = 2
	session.loopMu.Unlock()

	sm.onLoopTurnComplete(session)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, stopped, _ := loopState(session); stopped {
			session.loopMu.Lock()
			seen := session.loopFailSeen
			session.loopMu.Unlock()
			if seen < loopNoProgressLimit {
				t.Errorf("stopped, but not by the no-progress guard (seen=%d)", seen)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("loop kept going although the same failure repeated")
}

func TestParseLoopPromise(t *testing.T) {
	cases := []struct{ in, signal, detail string }{
		{"All tests pass now.\n<promise>DONE</promise>", "done", ""},
		{"<promise> BLOCKED: need the staging API key </promise>", "blocked", "need the staging API key"},
		{"I'll keep going", "", ""},
		// The last promise wins (an early mention is not the final word)
		{"Earlier I said <promise>BLOCKED: x</promise> but fixed it. <promise>DONE</promise>", "done", ""},
	}
	for _, c := range cases {
		s, d := parseLoopPromise(c.in)
		if s != c.signal || d != c.detail {
			t.Errorf("parseLoopPromise(%q) = (%q, %q), want (%q, %q)", c.in, s, d, c.signal, c.detail)
		}
	}
}

func TestBuildLoopRetryPromptEscalates(t *testing.T) {
	cfg := &LoopConfig{Goal: "green tests", VerifyCommand: "go test ./..."}
	first := buildLoopRetryPrompt(cfg, 2, 10, "FAIL", 1, false)
	if strings.Contains(first, "same failure") {
		t.Error("first failure should not escalate")
	}
	again := buildLoopRetryPrompt(cfg, 3, 10, "FAIL", 2, true)
	for _, want := range []string{"same failure", "different approach", "check still fails", "loop-progress.md", "BLOCKED"} {
		if !strings.Contains(again, want) {
			t.Errorf("retry prompt missing %q", want)
		}
	}
	noVerify := buildLoopRetryPrompt(&LoopConfig{Goal: "g"}, 2, 5, "", 0, false)
	if !strings.Contains(noVerify, "<promise>DONE</promise>") {
		t.Error("a loop without a verify command must tell the agent how to finish")
	}
}

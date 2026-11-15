package agents

// Turn handling for the direct (OpenAI-compatible) provider path only.
//
// The Claude CLI accepts messages mid-turn; the direct loop used to start a
// second loop instead. Two loops then shared one session: each new prompt
// replaced the response channel, the UI stream stopped at the first loop's
// result, and the other loop's messages were dropped after a 5 s wait each
// (the chat looked stuck while the model kept running tools).
//
// Now a session runs one direct turn at a time. A prompt sent during a turn is
// stored as usual and handed to the running loop, which adds it as a user
// message before its next model call. A prompt that arrives as the turn ends
// starts a follow-up turn in the same goroutine.

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/llmclient"
	"github.com/schlunsen/eggify/internal/logging"
)

// directClearMarker is stored (role system) by /clear; history starts after it.
const directClearMarker = "🧹 Context cleared"

// directTurnRunning reports whether a direct turn is in progress.
func (s *AgentSession) directTurnRunning() bool {
	s.directTurnMu.Lock()
	defer s.directTurnMu.Unlock()
	return s.directTurnActive
}

// joinOrStartDirectTurn hands prompt to the running turn and returns true, or
// marks a new turn as running and returns false (the caller starts it).
func (s *AgentSession) joinOrStartDirectTurn(prompt string) bool {
	s.directTurnMu.Lock()
	defer s.directTurnMu.Unlock()
	if s.directTurnActive {
		s.directQueued = append(s.directQueued, prompt)
		return true
	}
	s.directTurnActive = true
	s.directQueued = nil
	return false
}

// takeDirectPrompts returns (and clears) prompts sent during the turn.
func (s *AgentSession) takeDirectPrompts() []string {
	s.directTurnMu.Lock()
	defer s.directTurnMu.Unlock()
	q := s.directQueued
	s.directQueued = nil
	return q
}

// finishDirectTurn ends the turn unless prompts are still waiting; those are
// returned and the turn stays running. failed drops waiting prompts (they are
// already stored, so the next prompt's history includes them).
func (s *AgentSession) finishDirectTurn(failed bool) []string {
	s.directTurnMu.Lock()
	defer s.directTurnMu.Unlock()
	q := s.directQueued
	s.directQueued = nil
	if failed || len(q) == 0 {
		s.directTurnActive = false
		return nil
	}
	return q
}

// isDirectClear reports whether prompt is the /clear command.
func isDirectClear(prompt string) bool {
	return strings.TrimSpace(prompt) == "/clear"
}

// clearDirectContext answers /clear on the direct path: the stored transcript
// stays (and stays visible), but later prompts are built only from messages
// after the marker. Loaded tools are reset too.
func (sm *SessionManager) clearDirectContext(session *AgentSession, sessionID uuid.UUID) error {
	if session.directTurnRunning() {
		sm.sendDirectTextMessage(session, "A turn is still running. Stop it first, then send /clear.")
		sm.finishDirectCommand(session)
		return nil
	}
	sm.sendDirectTextMessage(session, "Context cleared. Your next message starts a fresh conversation (earlier messages stay visible here but are no longer sent to the model).")

	sm.mu.Lock()
	session.MessageCount++
	seq := session.MessageCount
	sm.mu.Unlock()
	if err := sm.saveMessageToDB(sessionID, seq, "system", directClearMarker, "", nil, nil); err != nil {
		sm.releaseSequence(session, seq)
		logging.Error("Direct provider: failed to store /clear marker for %s: %v", sessionID, err)
		sm.finishDirectCommand(session)
		return err
	}

	session.directLoadedMu.Lock()
	session.directLoadedTools = nil
	session.directLoadedMu.Unlock()

	logging.Info("Direct provider: context cleared for session %s (marker seq %d)", sessionID, seq)
	sm.finishDirectCommand(session)
	return nil
}

// finishDirectCommand ends a prompt that was answered without a model call.
func (sm *SessionManager) finishDirectCommand(session *AgentSession) {
	sm.sendDirectResultMessage(session, time.Now(), 0, nil)
	sm.mu.Lock()
	session.Status = SessionStatusIdle
	session.UpdatedAt = time.Now()
	sm.mu.Unlock()
	sm.persistSessionState(session)
	sm.broadcastSessionUpdate(session)
}

// directHistoryStart returns the sequence of the latest /clear marker before
// beforeSequence (0 when there is none): history is read after it.
func (sm *SessionManager) directHistoryStart(sessionID uuid.UUID, beforeSequence int) int {
	if sm.db == nil {
		return 0
	}
	var start int
	err := sm.db.QueryRow(`
		SELECT COALESCE(MAX(sequence), 0) FROM agent_messages
		WHERE session_id = ? AND role = 'system' AND content = ?
		AND (? <= 0 OR sequence < ?)`,
		sessionID.String(), directClearMarker, beforeSequence, beforeSequence).Scan(&start)
	if err != nil {
		return 0
	}
	return start
}

// appendDirectPrompts adds prompts sent mid-turn as user messages.
func appendDirectPrompts(messages []llmclient.ChatMessage, prompts []string) []llmclient.ChatMessage {
	for _, p := range prompts {
		messages = append(messages, llmclient.ChatMessage{Role: "user", Content: p})
	}
	return messages
}

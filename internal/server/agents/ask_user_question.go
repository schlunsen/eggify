package agents

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/logging"
)

// askUserQuestionMultiSelectSeparator joins multi-select answers into the
// single string the Claude Code AskUserQuestion schema expects per question.
const askUserQuestionMultiSelectSeparator = ", "

// askUserQuestionDismissedMessage is what the agent is told when the user
// dismisses its question instead of answering.
const askUserQuestionDismissedMessage = "The user dismissed this question without answering; it is no longer relevant. " +
	"Do not ask it again. Continue without an answer if you can, otherwise stop and wait for the user's next message."

// Reasons a question stops waiting, sent in user_question_resolved.
const (
	questionAnswered     = "answered"
	questionDismissed    = "dismissed"
	questionCancelled    = "cancelled"     // the turn was interrupted or the session ended
	questionExpired      = "expired"       // the server no longer waits on it (e.g. after a restart)
	questionAutoAnswered = "auto_answered" // the question policy picked the recommended option
)

// handleAskUserQuestion answers an AskUserQuestion request that arrived through
// the permission forwarder. It runs on its own goroutine so the forwarder keeps
// serving other permission requests while the user thinks.
func (h *AgentHandler) handleAskUserQuestion(sessionID uuid.UUID, session *AgentSession, permReq *PermissionRequest) {
	logging.Info("📋 Handling AskUserQuestion permission - requestID=%s", permReq.RequestID)
	go func() {
		resp := h.askUserQuestions(session.ctx, session, permReq.Input)
		if !sendPermissionResponse(permReq, resp, 3*time.Second) {
			logging.Error("❌ Timeout sending AskUserQuestion response")
		}
	}()
}

// askUserQuestions drives the AskUserQuestion tool: every question in the tool
// input is shown to the user (one modal at a time), and the collected answers
// are returned via UpdatedInput.
//
// It waits as long as the user needs: until they answer or dismiss the
// question, the turn is interrupted (ctx) or the session ends. There is no
// timeout, and a closed browser tab does not end the wait: the question is
// re-sent when the user opens the session again. Without this the agent went
// on without an answer while the question stayed on screen.
//
// Claude Code validates UpdatedInput against the AskUserQuestion tool schema,
// so the original input must be preserved (notably the required `questions`
// array) and `answers` must be an object keyed by question text whose values
// are strings — multi-select answers are joined with ", ". Returning anything
// else (e.g. `{"answers": [...]}` alone) makes the CLI reject the tool call.
func (h *AgentHandler) askUserQuestions(ctx context.Context, session *AgentSession, input map[string]interface{}) PermissionResponse {
	sessionID := session.ID
	questions, err := parseAskUserQuestions(input)
	if err != nil {
		logging.Error("AskUserQuestion: %v", err)
		return PermissionResponse{Approved: false, DenyMessage: err.Error()}
	}

	requiresUser := isUserConfirmation(ctx)
	sessionName := h.SessionManager.sessionDisplayName(&session.Session)
	answers := make(map[string]interface{}, len(questions))
	for i, q := range questions {
		questionID := uuid.New().String()
		questionMsg := UserQuestionMessage{
			BaseMessage:   BaseMessage{Type: MessageTypeUserQuestion},
			SessionID:     sessionID,
			QuestionID:    questionID,
			Question:      q.Question,
			Header:        q.Header,
			Options:       q.Options,
			MultiSelect:   q.MultiSelect,
			QuestionIndex: i + 1,
			QuestionTotal: len(questions),
			Timestamp:     time.Now(),
			RequiresUser:  requiresUser,
			Recommended:   []int{},
			SessionName:   sessionName,
		}
		// An agent's own AskUserQuestion must not dress up as a confirmation card.
		if requiresUser {
			questionMsg.Kind = q.Kind
			questionMsg.Proposal = q.Proposal
		}

		// Confirmations always wait for a human; agent questions follow the
		// session's question policy (question_policy.go).
		var wait *questionWait
		if !requiresUser {
			questionMsg.Recommended = recommendedOptions(q.Options, q.MultiSelect)
			wait = newQuestionWait(questionMsg.Timestamp)
		}

		answerChan := make(chan UserQuestionAnswerResponse, 1)
		session.questionMu.Lock()
		session.pendingQuestions[questionID] = answerChan
		session.pendingQuestionData[questionID] = &questionMsg // re-sent when a client subscribes
		if wait != nil {
			if session.questionWaits == nil {
				session.questionWaits = make(map[string]*questionWait)
			}
			session.questionWaits[questionID] = wait
		}
		session.questionMu.Unlock()

		var policy QuestionPolicy
		if wait != nil {
			policy = h.SessionManager.EffectiveQuestionPolicy(session)
			h.setQuestionTimer(session, questionID, wait.deadline(policy, false), false, false)
		}

		h.broadcastToAllConnections(sessionID, questionMsg)
		logging.Info("📤 User question %d/%d sent to frontend, waiting for answer: question_id=%s", i+1, len(questions), questionID)

		var answer UserQuestionAnswerResponse
		reason, deny := questionAnswered, ""
		var t *time.Timer
		stopTimer := func() {
			if t != nil {
				t.Stop()
				t = nil
			}
		}
	wait:
		for {
			stopTimer()
			var timer <-chan time.Time
			var changed chan struct{}
			if wait != nil {
				changed = wait.changed
				session.questionMu.Lock()
				deadline := wait.deadline(policy, wait.held)
				session.questionMu.Unlock()
				if deadline != nil {
					t = time.NewTimer(time.Until(*deadline))
					timer = t.C
				}
			}
			select {
			case answer = <-answerChan:
				if answer.Dismissed {
					logging.Info("🚫 User dismissed question_id=%s", questionID)
					reason, deny = questionDismissed, askUserQuestionDismissedMessage
				} else {
					logging.Info("✅ Received user answer for question_id=%s: %v", questionID, answer.Answers)
				}
				break wait
			case <-timer:
				// An answer that arrived at the same moment wins over the auto-pick.
				select {
				case answer = <-answerChan:
					if answer.Dismissed {
						reason, deny = questionDismissed, askUserQuestionDismissedMessage
					}
					break wait
				default:
				}
				answer.Answers = optionLabels(q.Options, questionMsg.Recommended)
				reason = questionAutoAnswered
				logging.Info("🤖 Auto-answered question_id=%s (%s): %v", questionID, policy.Mode, answer.Answers)
				break wait
			case <-changed:
				// The policy changed or the user pressed "Keep waiting".
				policy = h.SessionManager.EffectiveQuestionPolicy(session)
				session.questionMu.Lock()
				held := wait.held
				deadline := wait.deadline(policy, held)
				session.questionMu.Unlock()
				h.setQuestionTimer(session, questionID, deadline, held, true)
			case <-ctx.Done():
				logging.Info("Turn cancelled while waiting for user question answer")
				reason, deny = questionCancelled, "The question was cancelled before the user answered"
				break wait
			case <-session.ctx.Done():
				logging.Info("Session context cancelled while waiting for user question answer")
				reason, deny = questionCancelled, "Session ended"
				break wait
			}
		}

		stopTimer()
		session.questionMu.Lock()
		delete(session.pendingQuestions, questionID)
		delete(session.pendingQuestionData, questionID)
		delete(session.questionWaits, questionID)
		session.questionMu.Unlock()

		if reason == questionAutoAnswered {
			h.broadcastQuestionResolved(sessionID, questionID, reason, answer.Answers...)
			label := q.Header
			if label == "" {
				label = q.Question
			}
			h.SessionManager.emitSessionNotice(session, fmt.Sprintf("🤖 Auto-answered %q: %s (%s)",
				label, formatAskUserQuestionAnswer(answer.Answers), policy.autoReason()))
			answers[q.Question] = fmt.Sprintf("%s (auto-picked: %s; the user may revisit this)",
				formatAskUserQuestionAnswer(answer.Answers), policy.autoReason())
			continue
		}
		h.broadcastQuestionResolved(sessionID, questionID, reason)

		if deny != "" {
			return PermissionResponse{Approved: false, DenyMessage: deny}
		}
		answers[q.Question] = formatAskUserQuestionAnswer(answer.Answers)
	}

	updatedInput := buildAskUserQuestionUpdatedInput(input, answers)
	logging.Info("✅ AskUserQuestion approved with user's answers")
	return PermissionResponse{Approved: true, UpdatedInput: &updatedInput}
}

// broadcastQuestionResolved tells every client of the session that a question
// stopped waiting, so no tab keeps showing it.
// An auto-answered question also carries the picked labels.
func (h *AgentHandler) broadcastQuestionResolved(sessionID uuid.UUID, questionID, reason string, answer ...string) {
	msg := map[string]interface{}{
		"type":        string(MessageTypeUserQuestionResolved),
		"session_id":  sessionID.String(),
		"question_id": questionID,
		"reason":      reason,
	}
	if reason == questionAutoAnswered {
		msg["answer"] = answer
	}
	h.broadcastToAllConnections(sessionID, msg)
}

// setQuestionTimer records a waiting question's auto-answer time in the copy
// re-sent on subscribe and, when it changed, tells the session's clients.
func (h *AgentHandler) setQuestionTimer(session *AgentSession, questionID string, deadline *time.Time, held, notify bool) {
	session.questionMu.Lock()
	// Replace, never mutate: pendingQuestionMessages hands out these pointers
	// and they are serialized after the lock is released.
	if data, ok := session.pendingQuestionData[questionID]; ok {
		updated := *data
		updated.AutoAnswerAt = deadline
		updated.Held = held
		session.pendingQuestionData[questionID] = &updated
	}
	session.questionMu.Unlock()
	if !notify {
		return
	}
	h.broadcastToAllConnections(session.ID, map[string]interface{}{
		"type":           string(MessageTypeUserQuestionTimer),
		"session_id":     session.ID.String(),
		"question_id":    questionID,
		"auto_answer_at": deadline,
		"held":           held,
	})
}

// optionLabels returns the labels of the given options.
func optionLabels(options []QuestionOption, indexes []int) []string {
	labels := make([]string, 0, len(indexes))
	for _, i := range indexes {
		if i >= 0 && i < len(options) {
			labels = append(labels, options[i].Label)
		}
	}
	return labels
}

// askUserQuestion is one entry of the AskUserQuestion `questions` array.
type askUserQuestion struct {
	Question    string
	Header      string
	MultiSelect bool
	Options     []QuestionOption
	Kind        string      // dedicated card; honoured on confirmations only
	Proposal    interface{} // details for that card
}

// parseAskUserQuestions extracts every question from the AskUserQuestion tool
// input, validating the shape the frontend modal relies on.
func parseAskUserQuestions(input map[string]interface{}) ([]askUserQuestion, error) {
	questionsRaw, ok := input["questions"].([]interface{})
	if !ok || len(questionsRaw) == 0 {
		return nil, fmt.Errorf("invalid question format: missing 'questions' array")
	}

	questions := make([]askUserQuestion, 0, len(questionsRaw))
	for i, raw := range questionsRaw {
		qMap, ok := raw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid question format: question %d is not an object", i)
		}
		q := askUserQuestion{}
		q.Question, _ = qMap["question"].(string)
		q.Header, _ = qMap["header"].(string)
		q.MultiSelect, _ = qMap["multiSelect"].(bool)
		q.Kind, _ = qMap["kind"].(string)
		q.Proposal = qMap["proposal"]
		if q.Question == "" {
			return nil, fmt.Errorf("invalid question format: question %d has no text", i)
		}

		optionsRaw, ok := qMap["options"].([]interface{})
		if !ok || len(optionsRaw) == 0 {
			return nil, fmt.Errorf("invalid question options: question %d has no options", i)
		}
		for _, optRaw := range optionsRaw {
			optMap, ok := optRaw.(map[string]interface{})
			if !ok {
				continue
			}
			label, _ := optMap["label"].(string)
			description, _ := optMap["description"].(string)
			q.Options = append(q.Options, QuestionOption{Label: label, Description: description})
		}
		if len(q.Options) == 0 {
			return nil, fmt.Errorf("invalid question options: question %d has no valid options", i)
		}
		questions = append(questions, q)
	}
	return questions, nil
}

// formatAskUserQuestionAnswer turns the selected labels into the single string
// per question that the AskUserQuestion schema expects.
func formatAskUserQuestionAnswer(selected []string) string {
	return strings.Join(selected, askUserQuestionMultiSelectSeparator)
}

// buildAskUserQuestionUpdatedInput returns a copy of the original tool input
// with the user's answers merged in, so the result still satisfies the tool's
// input schema.
func buildAskUserQuestionUpdatedInput(original map[string]interface{}, answers map[string]interface{}) map[string]interface{} {
	updated := make(map[string]interface{}, len(original)+1)
	for k, v := range original {
		updated[k] = v
	}
	updated["answers"] = answers
	return updated
}

// sendPermissionResponse delivers a response on the request's channel without
// blocking forever if nobody is listening. Returns false on timeout.
func sendPermissionResponse(permReq *PermissionRequest, resp PermissionResponse, timeout time.Duration) bool {
	select {
	case permReq.ResponseChan <- resp:
		return true
	case <-time.After(timeout):
		return false
	}
}

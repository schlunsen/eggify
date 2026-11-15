package agents

import (
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// What is waiting for the user, across sessions, for compact clients that do
// not subscribe to sessions (the desktop notch theatre). Permission requests
// only live as response channels in pendingPermissions, so a short summary of
// each is kept next to it; questions already keep their message
// (pendingQuestionData). Answers still go through the WebSocket handlers
// (permission_response, user_question_response) with their user and
// ownership checks: this is read-only.

// PendingPermissionSummary is one tool approval waiting for the user.
type PendingPermissionSummary struct {
	SessionID    uuid.UUID         `json:"session_id"`
	SessionName  string            `json:"session_name"`
	PermissionID string            `json:"permission_id"`
	ToolName     string            `json:"tool_name"`
	Details      map[string]string `json:"details"`
	AskedAt      time.Time         `json:"asked_at"`
}

// PendingQuestionSummary is one AskUserQuestion (or confirmation card) waiting.
type PendingQuestionSummary struct {
	SessionID    uuid.UUID        `json:"session_id"`
	SessionName  string           `json:"session_name"`
	QuestionID   string           `json:"question_id"`
	Question     string           `json:"question"`
	Header       string           `json:"header,omitempty"`
	Options      []QuestionOption `json:"options"`
	MultiSelect  bool             `json:"multi_select"`
	Kind         string           `json:"kind,omitempty"`
	RequiresUser bool             `json:"requires_user"`
	Recommended  []int            `json:"recommended"`
	AutoAnswerAt *time.Time       `json:"auto_answer_at"`
	AskedAt      time.Time        `json:"asked_at"`
}

// pendingDetailKeys are the inputs a card names (never file contents).
var pendingDetailKeys = []string{"command", "description", "file_path", "path", "url", "pattern", "query"}

const pendingDetailMax = 300

// summarizePermissionInput keeps the few short string inputs that say what a
// tool acts on, cut to pendingDetailMax.
func summarizePermissionInput(input map[string]interface{}) map[string]string {
	out := map[string]string{}
	for _, k := range pendingDetailKeys {
		s, ok := input[k].(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if r := []rune(s); len(r) > pendingDetailMax {
			s = string(r[:pendingDetailMax-1]) + "…"
		}
		out[k] = s
	}
	return out
}

// trackPendingPermission records the summary of a request just added to
// pendingPermissions. Call with permMu held.
func (s *AgentSession) trackPendingPermission(requestID, toolName string, input map[string]interface{}) {
	if s.pendingPermissionData == nil {
		s.pendingPermissionData = make(map[string]*PendingPermissionSummary)
	}
	s.pendingPermissionData[requestID] = &PendingPermissionSummary{
		SessionID:    s.ID,
		PermissionID: requestID,
		ToolName:     toolName,
		Details:      summarizePermissionInput(input),
		AskedAt:      time.Now(),
	}
}

// PendingRequests lists what waits for the user in the live sessions that
// visible allows (nil: all), oldest first.
func (sm *SessionManager) PendingRequests(visible func(uuid.UUID) bool) ([]PendingPermissionSummary, []PendingQuestionSummary) {
	sm.mu.RLock()
	sessions := make([]*AgentSession, 0, len(sm.sessions))
	for id, s := range sm.sessions {
		if visible == nil || visible(id) {
			sessions = append(sessions, s)
		}
	}
	sm.mu.RUnlock()

	perms := []PendingPermissionSummary{}
	questions := []PendingQuestionSummary{}
	for _, s := range sessions {
		firstPerm, firstQuestion := len(perms), len(questions)
		s.permMu.Lock()
		for id, p := range s.pendingPermissionData {
			if _, waiting := s.pendingPermissions[id]; !waiting {
				continue
			}
			perms = append(perms, *p)
		}
		s.permMu.Unlock()

		s.questionMu.Lock()
		for id, q := range s.pendingQuestionData {
			if _, waiting := s.pendingQuestions[id]; !waiting || q == nil {
				continue
			}
			questions = append(questions, PendingQuestionSummary{
				SessionID:    s.ID,
				QuestionID:   id,
				Question:     q.Question,
				Header:       q.Header,
				Options:      append([]QuestionOption{}, q.Options...),
				MultiSelect:  q.MultiSelect,
				Kind:         q.Kind,
				RequiresUser: q.RequiresUser,
				Recommended:  append([]int{}, q.Recommended...),
				AutoAnswerAt: q.AutoAnswerAt,
				AskedAt:      q.Timestamp,
			})
		}
		s.questionMu.Unlock()
		if len(perms) == firstPerm && len(questions) == firstQuestion {
			continue
		}
		// Like askUserQuestions: no manager lock (it may read the avatar from the DB).
		name := sm.sessionDisplayName(&s.Session)
		for i := firstPerm; i < len(perms); i++ {
			perms[i].SessionName = name
		}
		for i := firstQuestion; i < len(questions); i++ {
			questions[i].SessionName = name
		}
	}
	sort.Slice(perms, func(i, j int) bool { return perms[i].AskedAt.Before(perms[j].AskedAt) })
	sort.Slice(questions, func(i, j int) bool { return questions[i].AskedAt.Before(questions[j].AskedAt) })
	return perms, questions
}

package agents

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPendingRequestsListsWaitingApprovalsAndQuestions(t *testing.T) {
	name := "Builder"
	a := &AgentSession{Session: Session{ID: uuid.New(), Options: SessionOptions{AgentName: &name}}}
	b := &AgentSession{Session: Session{ID: uuid.New()}}
	for _, s := range []*AgentSession{a, b} {
		s.pendingPermissions = map[string]chan PermissionResponse{}
		s.pendingQuestions = map[string]chan UserQuestionAnswerResponse{}
		s.pendingQuestionData = map[string]*UserQuestionMessage{}
	}
	sm := &SessionManager{sessions: map[uuid.UUID]*AgentSession{a.ID: a, b.ID: b}}

	a.pendingPermissions["p1"] = make(chan PermissionResponse, 1)
	a.trackPendingPermission("p1", "Bash", map[string]interface{}{
		"command": "git push origin main", "content": "never sent", "timeout": 30,
	})
	// A summary whose request was answered is not listed.
	a.trackPendingPermission("gone", "Edit", map[string]interface{}{"file_path": "/x"})

	asked := time.Now().Add(-time.Minute)
	b.pendingQuestions["q1"] = make(chan UserQuestionAnswerResponse, 1)
	b.pendingQuestionData["q1"] = &UserQuestionMessage{
		Question: "Which one?", Options: []QuestionOption{{Label: "A (Recommended)"}, {Label: "B"}},
		Recommended: []int{0}, Timestamp: asked,
	}

	perms, questions := sm.PendingRequests(nil)
	if len(perms) != 1 || perms[0].PermissionID != "p1" || perms[0].ToolName != "Bash" || perms[0].SessionName != "Builder" {
		t.Fatalf("permissions = %+v", perms)
	}
	if got := perms[0].Details; got["command"] != "git push origin main" || len(got) != 1 {
		t.Fatalf("details = %v, want only the command", got)
	}
	if len(questions) != 1 || questions[0].QuestionID != "q1" || len(questions[0].Options) != 2 || !questions[0].AskedAt.Equal(asked) {
		t.Fatalf("questions = %+v", questions)
	}

	perms, questions = sm.PendingRequests(func(id uuid.UUID) bool { return id == b.ID })
	if len(perms) != 0 || len(questions) != 1 {
		t.Fatalf("filtered: %d permissions, %d questions", len(perms), len(questions))
	}
}

func TestSummarizePermissionInputCutsLongValues(t *testing.T) {
	got := summarizePermissionInput(map[string]interface{}{"command": strings.Repeat("x", 1000), "path": "  "})
	if n := len([]rune(got["command"])); n != pendingDetailMax {
		t.Fatalf("command has %d runes, want %d", n, pendingDetailMax)
	}
	if _, ok := got["path"]; ok {
		t.Fatal("blank path kept")
	}
}

package handlers

import (
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/agents"
)

func newResumeApp(t *testing.T, username string) (*fiber.App, *agents.AgentHandler, *database.Database, *[]uuid.UUID, *sync.Mutex) {
	t.Helper()
	db := database.NewTestDB(t)
	alice := insertSession(t, db, "alice")
	bob := insertSession(t, db, "bob")
	if _, err := db.GetDB().Exec(`UPDATE agent_sessions SET status = 'processing'`); err != nil {
		t.Fatal(err)
	}
	sm, err := agents.NewSessionManager(&agents.Config{Model: "sonnet"}, db.GetDB())
	if err != nil {
		t.Fatal(err)
	}
	ah := &agents.AgentHandler{SessionManager: sm, BackgroundAgentMgr: agents.NewBackgroundAgentManager()}
	var mu sync.Mutex
	var sent []uuid.UUID
	ah.SetResumeSender(func(id uuid.UUID, _ string) error {
		mu.Lock()
		sent = append(sent, id)
		mu.Unlock()
		return nil
	})
	// A plain quit interrupted both sessions: they are offered.
	now := time.Now()
	snap := &agents.ResumeSnapshot{V: 1, Reason: agents.ShutdownReasonQuit, WrittenAt: now, Sessions: []agents.ResumeSession{
		{ID: alice, Why: agents.ResumeWhyTurn, UpdatedAt: now.Add(-time.Minute), InterruptedAt: now},
		{ID: bob, Why: agents.ResumeWhyTurn, UpdatedAt: now, InterruptedAt: now},
	}}
	if plan := ah.ApplyResumeSnapshot(snap, agents.ResumeOptions{AutoResume: true}); len(plan.Offered) != 2 {
		t.Fatalf("offered %d, want 2", len(plan.Offered))
	}

	h := &AgentSessionHandler{agentHandler: ah, db: db}
	app := fiber.New()
	if username != "" {
		app.Use(func(c *fiber.Ctx) error { c.Locals("username", username); return c.Next() })
	}
	app.Get("/api/agent/resume-after-restart", h.HandleGetResumeAfterRestart)
	app.Post("/api/agent/resume-after-restart", h.HandleResumeAfterRestart)
	app.Delete("/api/agent/resume-after-restart", h.HandleDismissResumeAfterRestart)
	return app, ah, db, &sent, &mu
}

func TestResumeAfterRestartAPI_ListsOnlyVisibleSessions(t *testing.T) {
	app, _, _, _, _ := newResumeApp(t, "alice")
	status, body := doTagsJSON(t, app, "GET", "/api/agent/resume-after-restart", "")
	if status != 200 {
		t.Fatalf("GET: %d %v", status, body)
	}
	if list, _ := body["sessions"].([]any); len(list) != 1 {
		t.Fatalf("alice sees %v, want only her session", body["sessions"])
	}
	if body["reason"] != agents.ShutdownReasonQuit || body["working_now"] != float64(0) {
		t.Fatalf("body = %v", body)
	}
}

func TestResumeAfterRestartAPI_ContinueAndDismiss(t *testing.T) {
	app, ah, _, sent, mu := newResumeApp(t, "alice")
	status, body := doTagsJSON(t, app, "POST", "/api/agent/resume-after-restart", "")
	if status != 200 || body["resumed"] != float64(1) {
		t.Fatalf("POST: %d %v", status, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(*sent)
		mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resume prompts sent: %d, want 1", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Bob's session is still offered (to bob); alice cannot dismiss it.
	if status, body := doTagsJSON(t, app, "DELETE", "/api/agent/resume-after-restart", ""); status != 200 || body["dismissed"] != float64(0) {
		t.Fatalf("DELETE: %d %v", status, body)
	}
	if pending, _ := ah.PendingResumes(); len(pending) != 1 {
		t.Fatalf("pending = %d, want bob's 1", len(pending))
	}
}

func TestResumeAfterRestartAPI_DismissWithoutUser(t *testing.T) {
	// User auth off: no username, every offered session is the caller's.
	app, ah, _, sent, _ := newResumeApp(t, "")
	if status, body := doTagsJSON(t, app, "DELETE", "/api/agent/resume-after-restart", ""); status != 200 || body["dismissed"] != float64(2) {
		t.Fatalf("DELETE: %d %v", status, body)
	}
	if pending, _ := ah.PendingResumes(); len(pending) != 0 {
		t.Fatalf("pending = %d, want 0", len(pending))
	}
	if len(*sent) != 0 {
		t.Fatal("dismissing sends nothing")
	}
}

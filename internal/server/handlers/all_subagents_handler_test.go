package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/agents"
)

type allSubagentsFixture struct {
	app     *fiber.App
	db      *database.Database
	storage *agents.SQLiteSessionStorage
	mgr     *agents.BackgroundAgentManager
}

func newAllSubagentsApp(t *testing.T, username string) *allSubagentsFixture {
	t.Helper()
	db := database.NewTestDB(t)
	storage, err := agents.NewSQLiteSessionStorage(db.GetDB())
	if err != nil {
		t.Fatal(err)
	}
	mgr := agents.NewBackgroundAgentManager()
	h := &AgentSessionHandler{
		agentHandler: &agents.AgentHandler{SessionManager: &agents.SessionManager{Storage: storage}, BackgroundAgentMgr: mgr},
		db:           db,
	}
	app := fiber.New()
	if username != "" {
		app.Use(func(c *fiber.Ctx) error { c.Locals("username", username); return c.Next() })
	}
	app.Get("/api/agent/subagents", h.HandleListAllSubagents)
	return &allSubagentsFixture{app: app, db: db, storage: storage, mgr: mgr}
}

func (f *allSubagentsFixture) saveAgent(t *testing.T, session uuid.UUID, id string, status agents.BackgroundAgentStatus, updated time.Time, lines int) {
	t.Helper()
	agent := &agents.BackgroundAgent{
		AgentID:         id,
		ParentSessionID: session,
		SubagentType:    "general-purpose",
		Description:     "agent " + id,
		Status:          status,
		CreatedAt:       updated.Add(-time.Minute),
		UpdatedAt:       updated,
	}
	for i := 0; i < lines; i++ {
		agent.RecentOutput = append(agent.RecentOutput, fmt.Sprintf("line %d", i))
	}
	if status != agents.BackgroundAgentStatusRunning {
		done := updated
		agent.CompletedAt = &done
	}
	if err := f.storage.SaveBackgroundAgent(agent); err != nil {
		t.Fatal(err)
	}
	if status == agents.BackgroundAgentStatusRunning {
		// A running agent runs in this process; a stored running row
		// without one is stale and listed as stopped.
		live := *agent
		f.mgr.RestoreAgent(&live)
	}
}

type allSubagentsBody struct {
	Subagents []agents.BackgroundAgent `json:"subagents"`
	Total     int                      `json:"total"`
	Running   int                      `json:"running"`
	Truncated bool                     `json:"truncated"`
}

func (f *allSubagentsFixture) get(t *testing.T, query string) (int, allSubagentsBody) {
	t.Helper()
	resp, err := f.app.Test(httptest.NewRequest("GET", "/api/agent/subagents"+query, nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var body allSubagentsBody
	_ = json.Unmarshal(raw, &body)
	return resp.StatusCode, body
}

func agentIDs(list []agents.BackgroundAgent) []string {
	ids := make([]string, len(list))
	for i, a := range list {
		ids[i] = a.AgentID
	}
	return ids
}

func TestListAllSubagentsFilters(t *testing.T) {
	f := newAllSubagentsApp(t, "")
	s1 := insertSession(t, f.db, "")
	s2 := insertSession(t, f.db, "")
	now := time.Now()
	f.saveAgent(t, s1, "run-old", agents.BackgroundAgentStatusRunning, now.Add(-48*time.Hour), 0)
	f.saveAgent(t, s2, "run-new", agents.BackgroundAgentStatusRunning, now.Add(-time.Minute), 0)
	f.saveAgent(t, s1, "done-recent", agents.BackgroundAgentStatusCompleted, now.Add(-10*time.Minute), 0)
	f.saveAgent(t, s2, "failed-recent", agents.BackgroundAgentStatusFailed, now.Add(-5*time.Minute), 0)
	f.saveAgent(t, s1, "done-ancient", agents.BackgroundAgentStatusCompleted, now.Add(-72*time.Hour), 0)

	status, body := f.get(t, "")
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	// Default filter: running only, oldest first.
	if got := fmt.Sprint(agentIDs(body.Subagents)); got != "[run-old run-new]" || body.Running != 2 {
		t.Fatalf("running = %s (running %d)", got, body.Running)
	}

	// All: running first, then finished in the last 24 h, newest first.
	_, body = f.get(t, "?status=all")
	if got := fmt.Sprint(agentIDs(body.Subagents)); got != "[run-old run-new failed-recent done-recent]" {
		t.Fatalf("all = %s", got)
	}

	_, body = f.get(t, "?status=failed")
	if got := fmt.Sprint(agentIDs(body.Subagents)); got != "[failed-recent]" {
		t.Fatalf("failed = %s", got)
	}

	// An explicit since reaches further back.
	since := url.QueryEscape(now.Add(-100 * time.Hour).Format(time.RFC3339))
	_, body = f.get(t, "?status=all&since="+since)
	if len(body.Subagents) != 5 {
		t.Fatalf("since: %v", agentIDs(body.Subagents))
	}

	if status, _ := f.get(t, "?status=bogus"); status != 400 {
		t.Fatalf("bad status: %d, want 400", status)
	}
	if status, _ := f.get(t, "?since=yesterday"); status != 400 {
		t.Fatalf("bad since: %d, want 400", status)
	}
}

// A live agent in the manager wins over its stored row.
func TestListAllSubagentsPrefersLiveState(t *testing.T) {
	f := newAllSubagentsApp(t, "")
	s := insertSession(t, f.db, "")
	f.saveAgent(t, s, "a1", agents.BackgroundAgentStatusRunning, time.Now(), 0)
	f.mgr.RestoreAgent(&agents.BackgroundAgent{
		AgentID: "a1", ParentSessionID: s, Status: agents.BackgroundAgentStatusCompleted,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	_, body := f.get(t, "?status=all")
	if len(body.Subagents) != 1 || body.Subagents[0].Status != agents.BackgroundAgentStatusCompleted || body.Running != 0 {
		t.Fatalf("got %+v", body)
	}
}

func TestListAllSubagentsOnlyVisibleSessions(t *testing.T) {
	f := newAllSubagentsApp(t, "alice")
	mine := insertSession(t, f.db, "alice")
	shared := insertSession(t, f.db, "")
	theirs := insertSession(t, f.db, "bob")
	now := time.Now()
	f.saveAgent(t, mine, "mine", agents.BackgroundAgentStatusRunning, now, 0)
	f.saveAgent(t, shared, "shared", agents.BackgroundAgentStatusRunning, now, 0)
	f.saveAgent(t, theirs, "theirs", agents.BackgroundAgentStatusRunning, now, 0)

	_, body := f.get(t, "?status=all")
	got := map[string]bool{}
	for _, id := range agentIDs(body.Subagents) {
		got[id] = true
	}
	if !got["mine"] || !got["shared"] || got["theirs"] || len(got) != 2 {
		t.Fatalf("visible = %v", agentIDs(body.Subagents))
	}

	// The API key (no user) sees every session's subagents.
	f2 := newAllSubagentsApp(t, "")
	other := insertSession(t, f2.db, "bob")
	f2.saveAgent(t, other, "bobs", agents.BackgroundAgentStatusRunning, now, 0)
	if _, body := f2.get(t, ""); len(body.Subagents) != 1 {
		t.Fatalf("api key: %v", agentIDs(body.Subagents))
	}
}

func TestListAllSubagentsCaps(t *testing.T) {
	f := newAllSubagentsApp(t, "")
	s := insertSession(t, f.db, "")
	now := time.Now()
	for i := 0; i < allSubagentsLimit+5; i++ {
		lines := 0
		if i == 0 {
			lines = 120
		}
		f.saveAgent(t, s, fmt.Sprintf("a%03d", i), agents.BackgroundAgentStatusRunning, now.Add(time.Duration(i)*time.Second), lines)
	}
	_, body := f.get(t, "")
	if len(body.Subagents) != allSubagentsLimit || !body.Truncated {
		t.Fatalf("len %d truncated %v", len(body.Subagents), body.Truncated)
	}
	first := body.Subagents[0]
	if first.AgentID != "a000" || len(first.RecentOutput) != allSubagentsOutputLines || first.RecentOutput[len(first.RecentOutput)-1] != "line 119" {
		t.Fatalf("first = %s, %d lines", first.AgentID, len(first.RecentOutput))
	}
}

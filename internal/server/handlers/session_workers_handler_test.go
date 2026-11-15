package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/agents"
)

type workersFixture struct {
	t  *testing.T
	db *database.Database
	sm *agents.SessionManager
	h  *AgentSessionHandler
}

func newWorkersFixture(t *testing.T) *workersFixture {
	t.Helper()
	db := database.NewTestDB(t)
	sm, err := agents.NewSessionManager(&agents.Config{Model: "sonnet"}, db.GetDB())
	if err != nil {
		t.Fatal(err)
	}
	h := &AgentSessionHandler{agentHandler: &agents.AgentHandler{SessionManager: sm, BackgroundAgentMgr: agents.NewBackgroundAgentManager()}, db: db}
	return &workersFixture{t: t, db: db, sm: sm, h: h}
}

// worker starts a live worker session of parent, owned by owner ("" = nobody).
func (f *workersFixture) worker(parent uuid.UUID, owner string) uuid.UUID {
	f.t.Helper()
	id := uuid.New()
	if _, err := f.sm.CreateSession(id, agents.SessionOptions{
		SpawnedBySessionID: &parent,
		Worker:             &agents.WorkerConfig{Task: "Add the endpoint", Mode: agents.WorkerModeBuild},
	}); err != nil {
		f.t.Fatal(err)
	}
	if owner != "" {
		if _, err := f.db.GetDB().Exec(`INSERT OR IGNORE INTO users (username) VALUES (?)`, owner); err != nil {
			f.t.Fatal(err)
		}
		if err := f.sm.SetSessionOwner(id, &owner); err != nil {
			f.t.Fatal(err)
		}
	}
	return id
}

// get asks for a session's workers as username ("" = the API key).
func (f *workersFixture) get(username, sessionID string) (int, map[string]any) {
	f.t.Helper()
	app := fiber.New()
	if username != "" {
		app.Use(func(c *fiber.Ctx) error { c.Locals("username", username); return c.Next() })
	}
	app.Get("/api/agent/sessions/:id/workers", f.h.HandleGetSessionWorkers)
	resp, err := app.Test(httptest.NewRequest("GET", "/api/agent/sessions/"+sessionID+"/workers", nil))
	if err != nil {
		f.t.Fatal(err)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func workerIDs(t *testing.T, body map[string]any) map[string]bool {
	t.Helper()
	list, ok := body["workers"].([]any)
	if !ok {
		t.Fatalf("workers is not a list: %v", body)
	}
	ids := map[string]bool{}
	for _, item := range list {
		w, _ := item.(map[string]any)
		id, _ := w["session_id"].(string)
		// A worker that has said nothing yet still has its (empty) lists.
		if _, ok := w["lines"].([]any); !ok {
			t.Errorf("worker %s lacks a lines list: %v", id, w)
		}
		if _, ok := w["lines_at"].([]any); !ok {
			t.Errorf("worker %s lacks a lines_at list: %v", id, w)
		}
		ids[id] = true
	}
	return ids
}

func TestSessionWorkers_ListsTheSessionsOwnWorkers(t *testing.T) {
	f := newWorkersFixture(t)
	parent := insertSession(t, f.db, "alice")
	other := insertSession(t, f.db, "alice")
	mine := f.worker(parent, "alice")
	second := f.worker(parent, "alice")
	foreign := f.worker(other, "alice")

	for _, caller := range []string{"alice", ""} {
		status, body := f.get(caller, parent.String())
		if status != 200 {
			t.Fatalf("as %q: status %d: %v", caller, status, body)
		}
		got := workerIDs(t, body)
		if len(got) != 2 || !got[mine.String()] || !got[second.String()] || got[foreign.String()] {
			t.Errorf("as %q: workers %v, want the session's two", caller, got)
		}
	}
}

func TestSessionWorkers_NoWorkersIsAnEmptyList(t *testing.T) {
	f := newWorkersFixture(t)
	parent := insertSession(t, f.db, "alice")

	status, body := f.get("alice", parent.String())
	if status != 200 {
		t.Fatalf("status %d: %v", status, body)
	}
	if list, ok := body["workers"].([]any); !ok || len(list) != 0 {
		t.Errorf("workers = %v, want an empty list", body["workers"])
	}
}

func TestSessionWorkers_RefusesABadSessionID(t *testing.T) {
	f := newWorkersFixture(t)
	if status, _ := f.get("alice", "not-a-uuid"); status != 400 {
		t.Errorf("status %d, want 400", status)
	}
}

func TestSessionWorkers_NeedsASessionManager(t *testing.T) {
	app := fiber.New()
	app.Get("/api/agent/sessions/:id/workers", (&AgentSessionHandler{}).HandleGetSessionWorkers)
	resp, err := app.Test(httptest.NewRequest("GET", "/api/agent/sessions/"+uuid.NewString()+"/workers", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 503 {
		t.Errorf("status %d, want 503", resp.StatusCode)
	}
}

// A worker's output carries file contents and command output, so it follows
// the session rules: another user's session is refused, and a worker the
// caller may not see is left out of a session they may see.
func TestRedTeam_REST_SessionWorkersShowOnlyTheCallersSessions(t *testing.T) {
	f := newWorkersFixture(t)
	bobs := insertSession(t, f.db, "bob")
	f.worker(bobs, "bob")

	status, body := f.get("alice", bobs.String())
	if status != 403 {
		t.Fatalf("alice asking for bob's session: status %d: %v", status, body)
	}
	if _, leaked := body["workers"]; leaked {
		t.Errorf("refusal carries workers: %v", body)
	}

	shared := insertSession(t, f.db, "")
	alices := f.worker(shared, "alice")
	bobsWorker := f.worker(shared, "bob")

	status, body = f.get("alice", shared.String())
	if status != 200 {
		t.Fatalf("alice on a shared session: status %d: %v", status, body)
	}
	if got := workerIDs(t, body); len(got) != 1 || !got[alices.String()] || got[bobsWorker.String()] {
		t.Errorf("alice sees %v, want only her worker", got)
	}

	// The API key (no user) keeps read access to all of it.
	_, body = f.get("", shared.String())
	if got := workerIDs(t, body); len(got) != 2 {
		t.Errorf("API key sees %v, want both workers", got)
	}
}

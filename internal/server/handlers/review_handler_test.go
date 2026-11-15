package handlers

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// reviewCaller is how the test request authenticated.
type reviewCaller int

const (
	asUser reviewCaller = iota
	asAPIKey
	asNobody // user auth disabled
)

func newReviewApp(t *testing.T) (*fiber.App, *database.Repository, *reviewCaller) {
	t.Helper()
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		database.ResetInstance()
	})
	if _, err := db.GetDB().Exec(`INSERT INTO projects (id, name, path) VALUES ('p1', 'P', '/tmp/review-handler')`); err != nil {
		t.Fatal(err)
	}
	repo := database.NewRepository(db)

	who := asUser
	app := fiber.New()
	prevEnforced := principal.Enforced()
	t.Cleanup(func() { principal.SetEnforced(prevEnforced) })
	app.Use(func(c *fiber.Ctx) error {
		// API-key-only requests exist only with user auth on (the session
		// middleware marks them); asNobody is user auth disabled.
		principal.SetEnforced(who != asNobody)
		switch who {
		case asUser:
			c.Locals("username", "alice")
		case asAPIKey:
			c.Locals("authenticated_via_api_key", true)
		}
		return c.Next()
	})
	api := app.Group("/api")
	review := NewReviewHandler(repo)
	api.Get("/review/pending", review.HandleListPending)
	api.Get("/review/pending/counts", review.HandlePendingCounts)
	api.Post("/review/memories/:id/approve", principal.RequireUser(ReviewAction), review.HandleApproveMemory)
	api.Post("/review/memories/:id/reject", principal.RequireUser(ReviewAction), review.HandleRejectMemory)
	api.Post("/review/skills/:id/approve", principal.RequireUser(ReviewAction), review.HandleApproveSkill)
	api.Post("/review/skills/:id/reject", principal.RequireUser(ReviewAction), review.HandleRejectSkill)
	mem := NewMemoryHandler(repo)
	api.Post("/projects/:id/memories", mem.HandleCreateMemory)
	return app, repo, &who
}

func doJSON(t *testing.T, app *fiber.App, method, path, body string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func pendingAgentMemory(t *testing.T, repo *database.Repository) *database.Memory {
	t.Helper()
	m := &database.Memory{ProjectID: "p1", MemoryType: "note", Title: "t", Content: "c", Tags: "[]", Importance: 5,
		Source: "agent", CreatedBy: database.CreatedByAgent("sess-1"), Status: database.ReviewStatusPending}
	if err := repo.Memory.CreateMemory(m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestReviewListAndCounts(t *testing.T) {
	app, repo, _ := newReviewApp(t)
	pendingAgentMemory(t, repo)
	if err := repo.Skill.CreateAgentSkill(&database.Skill{Name: "s", Scope: "personal", Body: "b"}, "sess-2"); err != nil {
		t.Fatal(err)
	}

	code, out := doJSON(t, app, "GET", "/api/review/pending", "")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	mems := out["memories"].([]interface{})
	skills := out["skills"].([]interface{})
	if len(mems) != 1 || len(skills) != 1 {
		t.Fatalf("pending = %d memories, %d skills", len(mems), len(skills))
	}
	if mems[0].(map[string]interface{})["created_by_session_id"] != "sess-1" {
		t.Errorf("memory item lacks session: %v", mems[0])
	}

	_, counts := doJSON(t, app, "GET", "/api/review/pending/counts", "")
	if counts["total"] != float64(2) || counts["memories_by_project"].(map[string]interface{})["p1"] != float64(1) {
		t.Fatalf("counts = %v", counts)
	}
}

func TestReviewApproveWithEditsAndReject(t *testing.T) {
	app, repo, _ := newReviewApp(t)
	m := pendingAgentMemory(t, repo)

	code, out := doJSON(t, app, "POST", "/api/review/memories/"+m.ID+"/approve", `{"content":"edited","importance":9}`)
	if code != 200 {
		t.Fatalf("approve status %d: %v", code, out)
	}
	got, _ := repo.Memory.GetMemory(m.ID)
	if got.Status != "approved" || got.Content != "edited" || got.Importance != 9 || *got.ReviewedBy != "user:alice" {
		t.Fatalf("after approve: %+v", got)
	}
	// Already decided.
	if code, _ := doJSON(t, app, "POST", "/api/review/memories/"+m.ID+"/reject", ""); code != 409 {
		t.Fatalf("re-review status %d, want 409", code)
	}

	s := &database.Skill{Name: "s", Scope: "personal", Body: "b"}
	if err := repo.Skill.CreateAgentSkill(s, "sess-2"); err != nil {
		t.Fatal(err)
	}
	if code, _ := doJSON(t, app, "POST", "/api/review/skills/"+s.ID+"/reject", ""); code != 200 {
		t.Fatalf("reject skill status %d", code)
	}
	if got, _ := repo.Skill.GetSkillByID(s.ID); got.Status != "rejected" {
		t.Fatalf("skill status %s", got.Status)
	}
	if code, _ := doJSON(t, app, "POST", "/api/review/skills/nope/approve", ""); code != 404 {
		t.Fatalf("missing skill status %d", code)
	}
}

func TestReviewRefusesAPIKeyOnly(t *testing.T) {
	app, repo, who := newReviewApp(t)
	m := pendingAgentMemory(t, repo)

	*who = asAPIKey
	if code, _ := doJSON(t, app, "POST", "/api/review/memories/"+m.ID+"/approve", ""); code != 403 {
		t.Fatalf("API-key approve status %d, want 403", code)
	}
	if got, _ := repo.Memory.GetMemory(m.ID); got.Status != "pending" {
		t.Fatal("API key approved a memory")
	}

	// User auth disabled: no principal at all, keep working.
	*who = asNobody
	if code, _ := doJSON(t, app, "POST", "/api/review/memories/"+m.ID+"/approve", ""); code != 200 {
		t.Fatalf("auth-disabled approve status %d, want 200", code)
	}
	if got, _ := repo.Memory.GetMemory(m.ID); *got.ReviewedBy != "user:local" {
		t.Fatalf("reviewer = %v", *got.ReviewedBy)
	}
}

func TestCreateMemoryProvenance(t *testing.T) {
	app, repo, who := newReviewApp(t)

	body := `{"title":"t","content":"c","memory_type":"note","importance":9,"status":"approved","created_by":"user"}`
	*who = asAPIKey
	code, out := doJSON(t, app, "POST", "/api/projects/p1/memories", body)
	if code != 201 {
		t.Fatalf("status %d", code)
	}
	id := out["memory"].(map[string]interface{})["id"].(string)
	got, _ := repo.Memory.GetMemory(id)
	if got.Status != "pending" || got.CreatedBy != database.CreatedByAPIKey || got.Importance != 5 {
		t.Fatalf("API-key memory = %s/%s/%d", got.Status, got.CreatedBy, got.Importance)
	}

	*who = asUser
	_, out = doJSON(t, app, "POST", "/api/projects/p1/memories", `{"title":"t","content":"c","memory_type":"note","status":"pending"}`)
	id = out["memory"].(map[string]interface{})["id"].(string)
	got, _ = repo.Memory.GetMemory(id)
	if got.Status != "approved" || got.CreatedBy != "user" {
		t.Fatalf("user memory = %s/%s", got.Status, got.CreatedBy)
	}
}

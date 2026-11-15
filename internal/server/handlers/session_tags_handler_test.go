package handlers

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/agents"
)

func newSessionTagsApp(t *testing.T, username string) (*fiber.App, *database.Database) {
	t.Helper()
	db := database.NewTestDB(t)
	storage, err := agents.NewSQLiteSessionStorage(db.GetDB())
	if err != nil {
		t.Fatal(err)
	}
	h := &AgentSessionHandler{agentHandler: &agents.AgentHandler{SessionManager: &agents.SessionManager{Storage: storage}}}
	app := fiber.New()
	if username != "" {
		app.Use(func(c *fiber.Ctx) error { c.Locals("username", username); return c.Next() })
	}
	app.Put("/api/agent/sessions/:id/tags", h.HandleSetSessionTags)
	app.Get("/api/agent/session-tags", h.HandleListSessionTags)
	return app, db
}

func insertSession(t *testing.T, db *database.Database, owner string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var ownerArg any
	if owner != "" {
		ownerArg = owner
		if _, err := db.GetDB().Exec(`INSERT OR IGNORE INTO users (username) VALUES (?)`, owner); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.GetDB().Exec(`INSERT INTO agent_sessions (id, owner_user_id) VALUES (?, ?)`, id.String(), ownerArg); err != nil {
		t.Fatal(err)
	}
	return id
}

func doTagsJSON(t *testing.T, app *fiber.App, method, url, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestSetSessionTags(t *testing.T) {
	app, db := newSessionTagsApp(t, "alice")
	mine := insertSession(t, db, "alice")
	theirs := insertSession(t, db, "bob")

	status, body := doTagsJSON(t, app, "PUT", "/api/agent/sessions/"+mine.String()+"/tags", `{"tags":[" Auth ","auth","bug   fix",""]}`)
	if status != 200 {
		t.Fatalf("PUT: %d %v", status, body)
	}
	if got, _ := json.Marshal(body["tags"]); string(got) != `["Auth","bug fix"]` {
		t.Fatalf("tags = %s", got)
	}

	if status, _ := doTagsJSON(t, app, "PUT", "/api/agent/sessions/"+uuid.NewString()+"/tags", `{"tags":["x"]}`); status != 404 {
		t.Fatalf("unknown session: %d, want 404", status)
	}
	if status, _ := doTagsJSON(t, app, "PUT", "/api/agent/sessions/not-a-uuid/tags", `{"tags":["x"]}`); status != 400 {
		t.Fatalf("bad id: %d, want 400", status)
	}
	if status, _ := doTagsJSON(t, app, "PUT", "/api/agent/sessions/"+theirs.String()+"/tags", `{"tags":["x"]}`); status != 403 {
		t.Fatalf("other user's session: %d, want 403", status)
	}

	// An empty list clears them.
	status, body = doTagsJSON(t, app, "PUT", "/api/agent/sessions/"+mine.String()+"/tags", `{"tags":[]}`)
	if got, _ := json.Marshal(body["tags"]); status != 200 || string(got) != `[]` {
		t.Fatalf("clear: %d %s", status, got)
	}
}

func TestListSessionTagsOnlyVisibleSessions(t *testing.T) {
	app, db := newSessionTagsApp(t, "alice")
	mine := insertSession(t, db, "alice")
	shared := insertSession(t, db, "")
	theirs := insertSession(t, db, "bob")
	for id, tags := range map[uuid.UUID][]string{mine: {"a"}, shared: {"s"}, theirs: {"b"}} {
		if _, err := database.SaveSessionTags(db.GetDB(), id.String(), tags); err != nil {
			t.Fatal(err)
		}
	}

	status, body := doTagsJSON(t, app, "GET", "/api/agent/session-tags", "")
	if status != 200 {
		t.Fatalf("GET: %d %v", status, body)
	}
	got, _ := json.Marshal(body["tags"])
	want, _ := json.Marshal(map[string][]string{mine.String(): {"a"}, shared.String(): {"s"}})
	if string(got) != string(want) {
		t.Fatalf("tags = %s, want %s", got, want)
	}
}

// The API key (no signed-in user) may set and list tags: they are not a
// security setting.
func TestSessionTagsWithAPIKeyOnly(t *testing.T) {
	app, db := newSessionTagsApp(t, "")
	id := insertSession(t, db, "bob")
	if status, body := doTagsJSON(t, app, "PUT", "/api/agent/sessions/"+id.String()+"/tags", `{"tags":["x"]}`); status != 200 {
		t.Fatalf("PUT: %d %v", status, body)
	}
	_, body := doTagsJSON(t, app, "GET", "/api/agent/session-tags", "")
	if got, _ := json.Marshal(body["tags"]); string(got) != `{"`+id.String()+`":["x"]}` {
		t.Fatalf("tags = %s", got)
	}
}

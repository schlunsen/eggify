package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/agents"
)

var testMP4 = append([]byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2"), make([]byte, 2000)...)

func newSessionMediaApp(t *testing.T, username string) (*fiber.App, *database.Database, agents.SessionStorage) {
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
	app.Get("/api/agent/sessions/:id/media", h.HandleListSessionMedia)
	app.Get("/api/agent/sessions/:id/media/:mediaId", h.HandleGetSessionMedia)
	app.Get("/api/agent/sessions/:id/media/:mediaId/poster", h.HandleGetSessionMediaPoster)
	app.Post("/api/agent/sessions/:id/media/:mediaId/convert", h.HandleConvertSessionMedia)
	app.Get("/api/agent/sessions/:id/image", h.HandleGetSessionImage)
	return app, db, storage
}

// mediaSession makes a session working in a fresh folder whose agent ran an
// ffmpeg command writing out/render.mp4.
func mediaSession(t *testing.T, db *database.Database, storage agents.SessionStorage, owner string) (uuid.UUID, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := insertSession(t, db, owner)
	opts, _ := json.Marshal(map[string]string{"working_directory": dir})
	if _, err := db.GetDB().Exec(`UPDATE agent_sessions SET options = ? WHERE id = ?`, string(opts), id.String()); err != nil {
		t.Fatal(err)
	}
	toolUses, _ := json.Marshal([]map[string]any{{"id": "toolu_1", "name": "Bash", "input": map[string]any{"command": "ffmpeg -i in.png out/render.mp4"}}})
	if err := storage.SaveMessage(&agents.MessageRecord{ID: uuid.New(), SessionID: id, Sequence: 1, Role: "assistant", ToolUses: toolUses, Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return id, dir
}

func getJSON(t *testing.T, app *fiber.App, url string) (int, map[string]any) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", url, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestSessionMedia_ListsAndStreamsTheRender(t *testing.T) {
	app, db, storage := newSessionMediaApp(t, "alice")
	id, dir := mediaSession(t, db, storage, "alice")
	if err := os.MkdirAll(filepath.Join(dir, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "out", "render.mp4"), testMP4, 0o644); err != nil {
		t.Fatal(err)
	}

	status, body := getJSON(t, app, "/api/agent/sessions/"+id.String()+"/media")
	items, _ := body["items"].([]any)
	if status != 200 || len(items) != 1 {
		t.Fatalf("list: %d %v", status, body)
	}
	item := items[0].(map[string]any)
	if item["name"] != "render.mp4" || item["kind"] != "video" || item["tool_use_id"] != "toolu_1" || item["ios_plays"] != true {
		t.Errorf("item: %v", item)
	}
	if _, leaked := item["RealPath"]; leaked {
		t.Error("the real path must not be sent")
	}

	req := httptest.NewRequest("GET", item["url"].(string), nil)
	req.Header.Set("Range", "bytes=4-7")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || string(raw) != "ftyp" || resp.Header.Get("Content-Type") != "video/mp4" {
		t.Errorf("range: %d %q %s", resp.StatusCode, raw, resp.Header.Get("Content-Type"))
	}

	if status, _ := getJSON(t, app, "/api/agent/sessions/"+id.String()+"/media/0123456789abcdef0123"); status != 404 {
		t.Errorf("unknown media id: %d", status)
	}
}

func TestRedTeam_SessionMedia_OtherUsersSessionIsRefused(t *testing.T) {
	app, db, storage := newSessionMediaApp(t, "alice")
	id, dir := mediaSession(t, db, storage, "bob")
	_ = os.MkdirAll(filepath.Join(dir, "out"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "out", "render.mp4"), testMP4, 0o644)
	if status, body := getJSON(t, app, "/api/agent/sessions/"+id.String()+"/media"); status == 200 {
		t.Errorf("alice listed bob's media: %v", body)
	}
}

func TestSessionMedia_PosterAndConvertWithFFmpeg(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	sessionMediaToolsMu.Lock()
	saved := sessionMediaTools
	sessionMediaTools = nil
	sessionMediaToolsMu.Unlock()
	t.Cleanup(func() { sessionMediaToolsMu.Lock(); sessionMediaTools = saved; sessionMediaToolsMu.Unlock() })

	app, db, storage := newSessionMediaApp(t, "alice")
	h := &AgentSessionHandler{}
	tools := h.mediaTools()
	tools.Dir = t.TempDir()

	id, dir := mediaSession(t, db, storage, "alice")
	_ = os.MkdirAll(filepath.Join(dir, "out"), 0o755)
	// A real two-second WebM clip, so a conversion has something to do.
	webm := filepath.Join(dir, "out", "render.webm")
	if out, err := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=10:duration=2",
		"-c:v", "libvpx", "-b:v", "200k", webm).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg can't make a WebM here: %v %s", err, out)
	}
	toolUses, _ := json.Marshal([]map[string]any{{"id": "toolu_2", "name": "mcp__eggify-tools__show_media", "input": map[string]any{"path": webm, "title": "Clip"}}})
	if err := storage.SaveMessage(&agents.MessageRecord{ID: uuid.New(), SessionID: id, Sequence: 2, Role: "assistant", ToolUses: toolUses, Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// The cache keys on the message count; wait out the reuse window instead of reaching into it.
	sessionMediaCacheMu.Lock()
	delete(sessionMediaCache, id)
	sessionMediaCacheMu.Unlock()

	_, body := getJSON(t, app, "/api/agent/sessions/"+id.String()+"/media")
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list: %v", body)
	}
	item := items[0].(map[string]any)
	if item["ios_plays"] != false || item["title"] != "Clip" || item["shown"] != true {
		t.Errorf("webm item: %v", item)
	}

	resp, err := app.Test(httptest.NewRequest("GET", item["poster_url"].(string), nil), 30000)
	if err != nil {
		t.Fatal(err)
	}
	poster, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(poster) < 3 || poster[0] != 0xFF || poster[1] != 0xD8 {
		t.Fatalf("poster: %d %d bytes", resp.StatusCode, len(poster))
	}

	convertURL := "/api/agent/sessions/" + id.String() + "/media/" + item["id"].(string) + "/convert"
	if resp, err := app.Test(httptest.NewRequest("POST", convertURL, nil)); err != nil || resp.StatusCode != 200 {
		t.Fatalf("convert: %v %v", resp, err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		_, body = getJSON(t, app, "/api/agent/sessions/"+id.String()+"/media")
		item = body["items"].([]any)[0].(map[string]any)
		if item["compatible"] == "ready" {
			break
		}
		if item["compatible"] == "failed" || time.Now().After(deadline) {
			t.Fatalf("conversion: %v", item)
		}
		time.Sleep(300 * time.Millisecond)
	}
	resp, err = app.Test(httptest.NewRequest("GET", item["compatible_url"].(string), nil))
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 8)
	_, _ = io.ReadFull(resp.Body, head)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(head[4:8]) != "ftyp" || resp.Header.Get("Content-Type") != "video/mp4" {
		t.Errorf("converted copy: %d %q %s", resp.StatusCode, head, resp.Header.Get("Content-Type"))
	}
}

// The image an agent shows in its reply (![alt](output/imagegen/x.png)).
func TestSessionImage_ServesTheReplysImage(t *testing.T) {
	app, db, storage := newSessionMediaApp(t, "alice")
	id, dir := mediaSession(t, db, storage, "alice")
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	if err := os.MkdirAll(filepath.Join(dir, "output", "imagegen"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "output", "imagegen", "pop art.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	base := "/api/agent/sessions/" + id.String() + "/image?path="
	resp, err := app.Test(httptest.NewRequest("GET", base+url.QueryEscape("output/imagegen/pop art.png"), nil))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(raw, png) || resp.Header.Get("Content-Type") != "image/png" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("%d %q %v", resp.StatusCode, raw, resp.Header)
	}
	req := httptest.NewRequest("GET", base+url.QueryEscape("output/imagegen/pop art.png"), nil)
	req.Header.Set("If-None-Match", resp.Header.Get("ETag"))
	if resp2, _ := app.Test(req); resp2.StatusCode != 304 {
		t.Errorf("revalidation: %d", resp2.StatusCode)
	}
	if status, _ := getJSON(t, app, base+url.QueryEscape("output/missing.png")); status != 404 {
		t.Errorf("missing image: %d", status)
	}
	if status, _ := getJSON(t, app, base); status != 400 {
		t.Errorf("no path: %d", status)
	}
}

func TestRedTeam_SessionImage_RefusesOtherSessionsAndFolders(t *testing.T) {
	app, db, storage := newSessionMediaApp(t, "alice")
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	bobID, bobDir := mediaSession(t, db, storage, "bob")
	_ = os.WriteFile(filepath.Join(bobDir, "secret.png"), png, 0o644)
	if status, _ := getJSON(t, app, "/api/agent/sessions/"+bobID.String()+"/image?path=secret.png"); status == 200 {
		t.Error("alice read bob's session image")
	}
	// Alice's session can't reach bob's folder by absolute path or ../
	aliceID, aliceDir := mediaSession(t, db, storage, "alice")
	rel, _ := filepath.Rel(aliceDir, filepath.Join(bobDir, "secret.png"))
	for _, ref := range []string{filepath.Join(bobDir, "secret.png"), rel} {
		if status, _ := getJSON(t, app, "/api/agent/sessions/"+aliceID.String()+"/image?path="+url.QueryEscape(ref)); status != 403 {
			t.Errorf("%s: %d, want 403", ref, status)
		}
	}
	// Not an image, whatever the name says.
	_ = os.WriteFile(filepath.Join(aliceDir, "db.png"), []byte("SQLite format 3\x00"), 0o644)
	if status, _ := getJSON(t, app, "/api/agent/sessions/"+aliceID.String()+"/image?path=db.png"); status != 404 {
		t.Errorf("fake png: %d", status)
	}
}

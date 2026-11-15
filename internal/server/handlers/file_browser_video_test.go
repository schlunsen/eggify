package handlers

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
)

func newVideoTestApp(t *testing.T) (*fiber.App, string) {
	t.Helper()
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.ResetInstance)
	repo := database.NewRepository(db)

	root := t.TempDir()
	promo := filepath.Join(root, "public", "promo")
	if err := os.MkdirAll(promo, 0o755); err != nil {
		t.Fatal(err)
	}
	video := make([]byte, 4096)
	for i := range video {
		video[i] = byte(i)
	}
	files := map[string][]byte{
		"clip.mp4":    video,
		"clip.vtt":    []byte("WEBVTT\n"),
		"clip.da.vtt": []byte("WEBVTT\n"),
		"other.vtt":   []byte("WEBVTT\n"),
		"page.html":   []byte("<script>alert(1)</script>"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(promo, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	project := &database.Project{ID: "p1", Name: "p", Path: root}
	if err := repo.CreateProject(project); err != nil {
		t.Fatal(err)
	}

	h := NewFileBrowserHandler(repo)
	app := fiber.New()
	app.Get("/projects/:id/files/read", h.HandleReadFile)
	app.Get("/projects/:id/files/raw", h.HandleRawFile)
	return app, project.ID
}

func TestReadFileReturnsVideoMetadata(t *testing.T) {
	app, pid := newVideoTestApp(t)
	resp, err := app.Test(httptest.NewRequest("GET", "/projects/"+pid+"/files/read?path="+url.QueryEscape("public/promo/clip.mp4"), nil))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Content   string          `json:"content"`
		Video     bool            `json:"video"`
		Binary    bool            `json:"binary"`
		MimeType  string          `json:"mimeType"`
		Size      int64           `json:"size"`
		Subtitles []SubtitleTrack `json:"subtitles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Video || body.Binary || body.MimeType != "video/mp4" || body.Size != 4096 {
		t.Fatalf("unexpected metadata: %+v", body)
	}
	if body.Content != "" {
		t.Fatal("video bytes must not be inlined in the read response")
	}
	got := map[string]string{}
	for _, s := range body.Subtitles {
		got[s.Path] = s.Lang
	}
	if len(got) != 2 || got["public/promo/clip.vtt"] != "" || got["public/promo/clip.da.vtt"] != "da" {
		t.Fatalf("subtitles = %+v, want clip.vtt and clip.da.vtt only", body.Subtitles)
	}
}

func TestRawFileStreamsWithRanges(t *testing.T) {
	app, pid := newVideoTestApp(t)
	req := httptest.NewRequest("GET", "/projects/"+pid+"/files/raw?path="+url.QueryEscape("public/promo/clip.mp4"), nil)
	req.Header.Set("Range", "bytes=100-199")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "inline" {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	b, _ := io.ReadAll(resp.Body)
	if len(b) != 100 || b[0] != 100 {
		t.Fatalf("got %d bytes starting with %d", len(b), b[0])
	}
}

func TestRawFileOnlyInlinesMedia(t *testing.T) {
	app, pid := newVideoTestApp(t)
	resp, err := app.Test(httptest.NewRequest("GET", "/projects/"+pid+"/files/raw?path="+url.QueryEscape("public/promo/page.html"), nil))
	if err != nil {
		t.Fatal(err)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename="page.html"` {
		t.Fatalf("html must be served as an attachment, got Content-Disposition %q", cd)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}

	resp, err = app.Test(httptest.NewRequest("GET", "/projects/"+pid+"/files/raw?path="+url.QueryEscape("../../etc/passwd"), nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("traversal status = %d, want 400", resp.StatusCode)
	}
}

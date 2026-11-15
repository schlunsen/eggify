package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// newModelCacheTestApp builds a handler on a temp ~/.claude with the routes of
// routes.RegisterModelCache, plus a marker file next to the cache directory
// (where eggify.db lives in a real install).
func newModelCacheTestApp(t *testing.T) (app *fiber.App, h *ModelCacheHandler, marker string) {
	t.Helper()
	claudeDir := t.TempDir()
	h = NewModelCacheHandler(claudeDir)
	if err := os.MkdirAll(h.cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker = filepath.Join(claudeDir, "eggify", "eggify.db")
	if err := os.WriteFile(marker, []byte("MARKER-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}

	app = fiber.New()
	cache := app.Group("/api/model-cache")
	cache.Get("/:version", h.HandleCheckCache)
	cache.Get("/:version/:filename", h.HandleGetOrFetchFile)
	cache.Delete("/:version", h.HandleDeleteCache)
	return app, h, marker
}

// rawModelCacheRequest sends rawPath exactly as written: URL.Opaque keeps
// net/http from cleaning ".." out of it, as a raw socket client would.
func rawModelCacheRequest(t *testing.T, app *fiber.App, method, rawPath string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, "/", nil)
	req.URL.Opaque = rawPath
	req.RequestURI = "" // else the request line is written from it, not from the URL
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawPath, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestModelCache_ServesListsAndDeletesANormalVersion(t *testing.T) {
	app, h, marker := newModelCacheTestApp(t)
	const version = "parakeet-tdt-0.6b-v3"
	dir := filepath.Join(h.cacheDir, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"encoder-model.onnx": "ONNX-BYTES", "vocab.txt": "a\nb\n"}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	status, body := rawModelCacheRequest(t, app, http.MethodGet, "/api/model-cache/"+version)
	if status != http.StatusOK || !strings.Contains(body, "encoder-model.onnx") || !strings.Contains(body, "vocab.txt") {
		t.Fatalf("list: status %d, body %s", status, body)
	}

	for name, content := range files {
		status, body := rawModelCacheRequest(t, app, http.MethodGet, "/api/model-cache/"+version+"/"+name)
		if status != http.StatusOK || body != content {
			t.Fatalf("get %s: status %d, body %q", name, status, body)
		}
	}

	status, body = rawModelCacheRequest(t, app, http.MethodDelete, "/api/model-cache/"+version)
	if status != http.StatusOK {
		t.Fatalf("delete: status %d, body %s", status, body)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("version folder still there after delete (err %v)", err)
	}
	if _, err := os.Stat(h.cacheDir); err != nil {
		t.Fatalf("cache dir must survive deleting a version: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker must survive deleting a version: %v", err)
	}
}

func TestModelCache_NameRules(t *testing.T) {
	good := []string{"parakeet-tdt-0.6b-v3", "encoder-model.onnx", "vocab.txt", "nemo128.onnx", "a", "decoder_joint-model.int8.onnx"}
	for _, name := range good {
		if !validModelName(name) {
			t.Errorf("%q should be accepted", name)
		}
	}
	bad := []string{"", ".", "..", "...", ".secret", ".env", "a..b", "-rf", "_x", "a/b", `a\b`, "a%2e", "%2e%2e", "a b", "a\x00b", strings.Repeat("a", 256)}
	for _, name := range bad {
		if validModelName(name) {
			t.Errorf("%q should be refused", name)
		}
	}

	h := NewModelCacheHandler(t.TempDir())
	if _, ok := h.versionDir(".."); ok {
		t.Error("versionDir accepted ..")
	}
	if _, ok := h.filePath("..", "eggify.db"); ok {
		t.Error("filePath accepted a .. version")
	}
	if _, ok := h.filePath("v1", ".."); ok {
		t.Error("filePath accepted a .. filename")
	}
	if p, ok := h.filePath("v1", "vocab.txt"); !ok || p != filepath.Join(h.cacheDir, "v1", "vocab.txt") {
		t.Errorf("filePath(v1, vocab.txt) = %q, %v", p, ok)
	}
}

// Attack: Fiber routes on the raw path, so a version of ".." used to list
// ~/.claude/eggify, serve its files (eggify.db, .secret) and, with DELETE,
// remove the whole folder. All of it needed only the API key.
func TestRedTeam_ModelCache_DotDotNeverLeavesTheCacheDir(t *testing.T) {
	app, h, marker := newModelCacheTestApp(t)
	// A cached model, so "the cache dir itself" has something to lose too.
	kept := filepath.Join(h.cacheDir, "parakeet-tdt-0.6b-v3", "vocab.txt")
	if err := os.MkdirAll(filepath.Dir(kept), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, []byte("vocab"), 0o644); err != nil {
		t.Fatal(err)
	}

	attacks := []struct{ method, path string }{
		{http.MethodGet, "/api/model-cache/.."},
		{http.MethodGet, "/api/model-cache/."},
		{http.MethodGet, "/api/model-cache/%2e%2e"},
		{http.MethodGet, "/api/model-cache/../eggify.db"},
		{http.MethodGet, "/api/model-cache/%2e%2e/eggify.db"},
		{http.MethodGet, "/api/model-cache/..%2feggify.db"},
		{http.MethodGet, "/api/model-cache/parakeet-tdt-0.6b-v3/.."},
		{http.MethodGet, "/api/model-cache/parakeet-tdt-0.6b-v3/..%2f..%2feggify.db"},
		{http.MethodDelete, "/api/model-cache/.."},
		{http.MethodDelete, "/api/model-cache/."},
		{http.MethodDelete, "/api/model-cache/%2e%2e"},
	}
	for _, a := range attacks {
		status, body := rawModelCacheRequest(t, app, a.method, a.path)
		if status != http.StatusBadRequest {
			t.Errorf("%s %s: status %d, want 400 (body %s)", a.method, a.path, status, body)
		}
		if strings.Contains(body, "MARKER-SECRET") {
			t.Errorf("%s %s served the marker file", a.method, a.path)
		}
		if strings.Contains(body, "eggify.db") || strings.Contains(body, `"files"`) {
			t.Errorf("%s %s listed a folder: %s", a.method, a.path, body)
		}
		if strings.Contains(body, "deleted") {
			t.Errorf("%s %s reported a delete: %s", a.method, a.path, body)
		}
	}

	if got, err := os.ReadFile(marker); err != nil || string(got) != "MARKER-SECRET" {
		t.Fatalf("marker outside the cache dir was touched: %q, %v", got, err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("cached model was deleted: %v", err)
	}
}

package routes

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestTTSModelsServeOnlyModelFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"F5_Preprocess.onnx": "onnx-bytes",
		"config.json":        `{"a":1}`,
		"evil.html":          "<script>alert(1)</script>",
		"evil.svg":           `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`,
		"evil.js":            "alert(1)",
		"evil.onnx.html":     "<script>alert(1)</script>",
		"noext":              "<script>alert(1)</script>",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app := fiber.New()
	registerTTSModels(app.Group("/api").Group("/tts"), dir)

	get := func(p string) (int, map[string]string, string) {
		resp, err := app.Test(httptest.NewRequest("GET", p, nil))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, map[string]string{
			"ct":   resp.Header.Get("Content-Type"),
			"nosn": resp.Header.Get("X-Content-Type-Options"),
			"disp": resp.Header.Get("Content-Disposition"),
		}, string(b)
	}

	code, h, body := get("/api/tts/models/f5-tts/F5_Preprocess.onnx")
	if code != 200 || body != "onnx-bytes" || h["ct"] != "application/octet-stream" || h["nosn"] != "nosniff" || h["disp"] != "attachment" {
		t.Fatalf("onnx: %d %v %q", code, h, body)
	}
	code, h, _ = get("/api/tts/models/f5-tts/config.json")
	if code != 200 || h["ct"] != "application/json" || h["nosn"] != "nosniff" {
		t.Fatalf("json: %d %v", code, h)
	}
	for _, p := range []string{"evil.html", "evil.svg", "evil.js", "evil.onnx.html", "noext", "EVIL.HTML", "", "../tts.go"} {
		code, _, body := get("/api/tts/models/f5-tts/" + p)
		if code == 200 || body == files[p] && files[p] != "" {
			t.Errorf("%q served: %d %q", p, code, body)
		}
	}
}

func TestTTSModelsAbsentFolder(t *testing.T) {
	app := fiber.New()
	registerTTSModels(app.Group("/api").Group("/tts"), filepath.Join(t.TempDir(), "missing"))
	resp, err := app.Test(httptest.NewRequest("GET", "/api/tts/models/f5-tts/x.onnx", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

package handlers

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func ttsApp(h *TTSHandler) *fiber.App {
	app := fiber.New()
	app.Post("/api/tts/generate", h.HandleTTS)
	app.Get("/api/tts/status", h.HandleTTSStatus)
	return app
}

func postTTS(t *testing.T, app *fiber.App, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/tts/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestTTSAutoNeverFallsBackToMacOS(t *testing.T) {
	app := ttsApp(&TTSHandler{})
	for _, body := range []string{
		`{"text":"hello there world"}`,
		`{"text":"hello there world","backend":"auto","voice":"samantha"}`,
		`{"text":"hello there world","backend":"auto","voice":"kitt"}`,
	} {
		code, out := postTTS(t, app, body)
		if code != 503 {
			t.Fatalf("body %s: want 503, got %d (%s)", body, code, out)
		}
		if strings.Contains(out, `"backend":"macos"`) {
			t.Fatalf("auto selected macOS: %s", out)
		}
	}
}

func TestTTSSherpaUnavailableReturnsError(t *testing.T) {
	app := ttsApp(&TTSHandler{})
	code, out := postTTS(t, app, `{"text":"hello there world","backend":"sherpa"}`)
	if code != 503 {
		t.Fatalf("want 503, got %d (%s)", code, out)
	}
	if strings.Contains(out, `"backend":"macos"`) {
		t.Fatalf("sherpa failure fell back to macOS: %s", out)
	}
}

func TestTTSStatusDoesNotRecommendMacOS(t *testing.T) {
	app := ttsApp(&TTSHandler{})
	resp, err := app.Test(httptest.NewRequest("GET", "/api/tts/status", nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	if data["recommended"] != "none" {
		t.Fatalf("recommended = %v, want none", data["recommended"])
	}
}

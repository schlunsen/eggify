package handlers

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
)

// Attack: keep the stored provider key but point the provider at the
// attacker's server ({"api_key":"","custom_url":"https://evil"}), so the
// provider proxy attaches the real key on the way there.
func TestRedTeam_ProviderURLChangeWithoutKeyRefused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.ResetInstance()
	repo := database.NewRepository(db)
	h := NewProviderHandler(repo)
	app := fiber.New()
	app.Post("/providers", h.HandleSaveProvider)
	post := func(body string) (int, string) {
		req := httptest.NewRequest("POST", "/providers", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	const secret = "sk-ant-STORED-0123456789"
	if code, body := post(`{"provider_id":"claude","api_key":"` + secret + `"}`); code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	if code, body := post(`{"provider_id":"claude","api_key":"","custom_url":"https://evil.example"}`); code != 400 || !strings.Contains(body, "API key again") {
		t.Fatalf("URL change without key = %d %s, want 400", code, body)
	}
	p, _ := repo.GetProvider("claude")
	if p.CustomURL != nil && *p.CustomURL != "" {
		t.Fatalf("custom URL stored: %q", *p.CustomURL)
	}
	if p.APIKey == nil || *p.APIKey != secret {
		t.Fatal("stored key changed")
	}

	// Bad URLs are refused outright.
	for _, bad := range []string{"file:///etc/passwd", "http://evil.example", "https://u:p@evil.example"} {
		if code, _ := post(`{"provider_id":"claude","api_key":"new-key-123456","custom_url":"` + bad + `"}`); code != 400 {
			t.Errorf("custom_url %q accepted (%d)", bad, code)
		}
	}
	// Injection through the model name.
	if code, _ := post(`{"provider_id":"claude","api_key":"","model_name":"x\"; touch /tmp/pwn; \""}`); code != 400 {
		t.Errorf("unsafe model name accepted (%d)", code)
	}

	// Control: a new URL with a new key, and saving again unchanged, work.
	if code, body := post(`{"provider_id":"claude","api_key":"new-key-123456","custom_url":"https://gw.example/anthropic"}`); code != 200 {
		t.Fatalf("URL change with key: %d %s", code, body)
	}
	if code, body := post(`{"provider_id":"claude","api_key":"","custom_url":"https://gw.example/anthropic","model_name":"claude-opus-5-5[1m]"}`); code != 200 {
		t.Fatalf("unchanged URL without key: %d %s", code, body)
	}
	// Loopback http is fine (local LLM servers).
	if code, body := post(`{"provider_id":"custom","api_key":"k-123456","custom_url":"http://localhost:4000"}`); code != 200 {
		t.Fatalf("loopback custom URL: %d %s", code, body)
	}
}

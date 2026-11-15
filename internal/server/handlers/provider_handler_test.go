package handlers

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
)

// TestProviderAPIKeyNeverReturned checks that the stored provider key never
// appears in GET /providers or in the save response, and that saving with an
// empty key keeps the stored one.
func TestProviderAPIKeyNeverReturned(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // GenerateEnvScript writes ~/.claude/provider-env.sh

	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.ResetInstance()
	repo := database.NewRepository(db)

	const secret = "sk-test-SUPERSECRET-1234567890"
	h := NewProviderHandler(repo)
	app := fiber.New()
	app.Get("/providers", h.HandleGetProviders)
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

	code, body := post(`{"provider_id":"deepseek","api_key":"` + secret + `","model_name":"deepseek-chat"}`)
	if code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	if strings.Contains(body, "SUPERSECRET") {
		t.Fatalf("save response leaks key: %s", body)
	}

	resp, err := app.Test(httptest.NewRequest("GET", "/providers", nil))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(b), "SUPERSECRET") {
		t.Fatalf("GET /providers leaks key: %s", b)
	}
	if !strings.Contains(string(b), `"has_api_key":true`) {
		t.Fatalf("GET /providers should report has_api_key: %s", b)
	}

	// Re-save without a key (e.g. changing the model): stored key is kept.
	code, body = post(`{"provider_id":"deepseek","model_name":"deepseek-reasoner"}`)
	if code != 200 {
		t.Fatalf("re-save without key: %d %s", code, body)
	}
	p, err := repo.GetProvider("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if p.APIKey == nil || *p.APIKey != secret {
		t.Fatalf("stored key changed: %v", p.APIKey)
	}
}

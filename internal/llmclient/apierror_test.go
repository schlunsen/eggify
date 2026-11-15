package llmclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Upstream error bodies are not echoed wholesale into chat errors.
func TestAPIErrorIsShortAndSanitised(t *testing.T) {
	big := strings.Repeat("A", 3000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key sk-secret-key-123 \u001b[31m` + big + `"}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "sk-secret-key-123", "m")
	_, err := c.ChatCompletion(context.Background(), ChatCompletionRequest{})
	if err == nil {
		t.Fatal("no error")
	}
	msg := err.Error()
	if len(msg) > 300 || strings.Contains(msg, "sk-secret-key-123") || strings.Contains(msg, "\x1b") {
		t.Fatalf("error not capped/sanitised (%d bytes): %q", len(msg), msg)
	}
	if !strings.Contains(msg, "bad key [redacted]") {
		t.Fatalf("message lost: %q", msg)
	}
	if _, err := c.ChatCompletionStream(context.Background(), ChatCompletionRequest{}); err == nil || len(err.Error()) > 300 {
		t.Fatalf("stream error not capped: %v", err)
	}
}

func TestValidateBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.example.com/v1#frag": "https://api.example.com/v1",
		"http://localhost:11434/v1":       "http://localhost:11434/v1",
		"http://127.0.0.1:4000":           "http://127.0.0.1:4000",
		"http://[::1]:4000/v1":            "http://[::1]:4000/v1",
		" HTTPS://x.example ":             "https://x.example",
		"https://192.168.1.5:4000/v1":     "https://192.168.1.5:4000/v1",
	} {
		got, err := ValidateBaseURL(in)
		if err != nil || got != want {
			t.Errorf("ValidateBaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "file:///etc/passwd", "http://evil.example", "http://192.168.1.5:4000", "ftp://x", "https://u:p@x.example", "https:///nohost", "javascript:alert(1)"} {
		if got, err := ValidateBaseURL(bad); err == nil {
			t.Errorf("ValidateBaseURL(%q) = %q, want error", bad, got)
		}
	}
	if Origin("https://X.example/a") != Origin("https://x.example:443/b") || Origin("https://x.example") == Origin("http://x.example") {
		t.Fatal("Origin comparison")
	}
}

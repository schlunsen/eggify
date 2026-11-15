package routes

import "testing"

func TestIsOriginAllowed(t *testing.T) {
	allowed := buildAllowedOriginSet([]string{"http://localhost:3000", "https://app.example.org"})
	cases := []struct {
		origin, host string
		want         bool
	}{
		// Same origin, ports on both sides.
		{"http://localhost:3333", "localhost:3333", true},
		{"https://example.com", "example.com", true},
		// Default ports are ignored (Starscream sends "Host: host:443" through ngrok).
		{"wss://example.ngrok.io", "example.ngrok.io:443", true},
		{"https://example.ngrok.io", "example.ngrok.io:443", true},
		{"https://example.ngrok.io:443", "example.ngrok.io", true},
		{"http://example.com", "example.com:80", true},
		{"HTTPS://Example.com", "example.com", true},
		{"http://[::1]:3333", "[::1]:3333", true},
		// Configured allowed origins.
		{"http://localhost:3000", "localhost:3333", true},
		{"https://app.example.org", "eggify.local", true},
		// Cross-origin.
		{"https://evil.example", "example.ngrok.io:443", false},
		{"https://evil.example", "example.ngrok.io", false},
		{"http://localhost:4000", "localhost:3333", false},
		{"https://example.com:8443", "example.com", false},
		{"https://example.com", "example.com:8443", false},
		{"https://example.ngrok.io.evil.example", "example.ngrok.io:443", false},
		{"null", "example.com", false},
	}
	for _, c := range cases {
		if got := isOriginAllowed(c.origin, c.host, allowed); got != c.want {
			t.Errorf("isOriginAllowed(%q, %q) = %v, want %v", c.origin, c.host, got, c.want)
		}
	}
	if !isOriginAllowed("https://anything", "x", map[string]bool{"*": true}) {
		t.Error("wildcard should allow everything")
	}
}

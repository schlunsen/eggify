package credproxy

// Prompt-injection red-team suite, attack 9 (proxy side). See CLAUDE.md →
// "Prompt-injection protections"; the catalogue is listed in
// internal/server/agents/redteam_test.go. The agent holds only a session
// capability and tries to turn it (or a guess) into the provider key, or to
// reach something other than the provider API through the proxy.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func rtProxy(t *testing.T) (*Server, func() captured) {
	t.Helper()
	up, got := upstream(t)
	return NewHandler(staticResolver(map[string]Upstream{"anthropic:s1": {BaseURL: up.URL, APIKey: "sk-real-key"}}), nil), got
}

func rtServe(h *Server, method, target, remote string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(`{}`))
	req.RemoteAddr = remote
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The agent presents a forged / foreign / wrong-kind capability or the
// provider-key format it hopes the proxy passes through.
func TestRedTeam_Proxy_BadCapabilityRejected(t *testing.T) {
	h, got := rtProxy(t)
	good := Token("s1", KindAnthropic)
	for name, hdr := range map[string]map[string]string{
		"none":           {},
		"guessed key":    {"X-Api-Key": "sk-ant-api03-guess"},
		"forged hmac":    {"X-Api-Key": good[:len(good)-2] + "zz"},
		"other session":  {"X-Api-Key": Token("s2", KindAnthropic)},
		"other api kind": {"X-Api-Key": Token("s1", KindOpenAI)},
		"bearer garbage": {"Authorization": "Bearer " + TokenPrefix + "s1.AAAA"},
	} {
		rec := rtServe(h, http.MethodPost, "/anthropic/v1/messages", "127.0.0.1:4000", hdr)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "sk-real-key") {
			t.Errorf("%s: the error leaks the key", name)
		}
	}
	if got().path != "" {
		t.Fatal("a rejected request reached the provider")
	}
}

// Another machine (or a request relayed through a local reverse proxy)
// replays a capability it captured.
func TestRedTeam_Proxy_NonLoopbackOrForwardedRejected(t *testing.T) {
	h, got := rtProxy(t)
	tok := map[string]string{"X-Api-Key": Token("s1", KindAnthropic)}
	for name, c := range map[string]struct {
		remote string
		hdr    map[string]string
	}{
		"LAN peer":         {"192.168.1.20:5555", nil},
		"public peer":      {"203.0.113.7:5555", nil},
		"X-Forwarded-For":  {"127.0.0.1:5555", map[string]string{"X-Forwarded-For": "203.0.113.7"}},
		"Forwarded":        {"127.0.0.1:5555", map[string]string{"Forwarded": "for=203.0.113.7"}},
		"X-Real-IP":        {"127.0.0.1:5555", map[string]string{"X-Real-Ip": "203.0.113.7"}},
		"X-Forwarded-Host": {"127.0.0.1:5555", map[string]string{"X-Forwarded-Host": "evil.example"}},
	} {
		hdr := map[string]string{}
		for k, v := range tok {
			hdr[k] = v
		}
		for k, v := range c.hdr {
			hdr[k] = v
		}
		if rec := rtServe(h, http.MethodPost, "/anthropic/v1/messages", c.remote, hdr); rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", name, rec.Code)
		}
	}
	if got().path != "" {
		t.Fatal("a non-loopback request reached the provider")
	}
}

// With a valid capability the agent walks out of the provider API prefix to
// reach other paths on the provider (or Eggify's own loopback endpoints).
func TestRedTeam_Proxy_PathEscapesRejected(t *testing.T) {
	h, got := rtProxy(t)
	tok := map[string]string{"X-Api-Key": Token("s1", KindAnthropic)}
	for _, target := range []string{
		"/anthropic/../internal/n0/mcp",
		"/anthropic/v1/../../admin",
		"/anthropic/v1/%2e%2e/%2e%2e/admin",
		"/anthropic/v1/%2E%2E/admin",
		"/anthropic//v1/messages",
		"//anthropic/v1/messages",
		"/anthropic/v1//evil.example/x",
		"/anthropic/v1/a%2Fb",
		"/anthropic/v1/a%5cb",
		"/anthropic/v1/..%2fadmin",
		"/anthropic/api/oauth/token",
		"/anthropic",
		"/internal/eggify/tool-guard",
	} {
		rec := rtServe(h, http.MethodPost, target, "127.0.0.1:4000", tok)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", target, rec.Code)
		}
	}
	if p := got().path; p != "" {
		t.Fatalf("an escaped path reached the provider: %s", p)
	}
	// Control: the API path itself goes through with the real key swapped in.
	rec := rtServe(h, http.MethodPost, "/anthropic/v1/messages", "127.0.0.1:4000", tok)
	b, _ := io.ReadAll(rec.Body)
	if rec.Code != 200 || got().xAPIKey != "sk-real-key" {
		t.Fatalf("control request: %d %s, upstream key %q", rec.Code, b, got().xAPIKey)
	}
}

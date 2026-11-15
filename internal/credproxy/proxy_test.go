package credproxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type captured struct {
	path, query, xAPIKey, auth, version, beta, forwarded, cookie, body string
}

// upstream records what reached the "provider".
func upstream(t *testing.T) (*httptest.Server, func() captured) {
	t.Helper()
	var mu sync.Mutex
	var last captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		last = captured{
			path: r.URL.Path, query: r.URL.RawQuery,
			xAPIKey: r.Header.Get("X-Api-Key"), auth: r.Header.Get("Authorization"),
			version: r.Header.Get("Anthropic-Version"), beta: r.Header.Get("Anthropic-Beta"),
			forwarded: r.Header.Get("X-Forwarded-For"), cookie: r.Header.Get("Cookie"), body: string(b),
		}
		mu.Unlock()
		w.Header().Set("Request-Id", "req_123")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() captured { mu.Lock(); defer mu.Unlock(); return last }
}

func startProxy(t *testing.T, r Resolver) *Server {
	t.Helper()
	p, err := Start(r, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func staticResolver(m map[string]Upstream) Resolver {
	return func(_ context.Context, sid string, kind Kind) (Upstream, error) {
		up, ok := m[string(kind)+":"+sid]
		if !ok {
			return Upstream{}, ErrNoUpstream
		}
		return up, nil
	}
}

func post(t *testing.T, url string, hdr map[string]string, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestTokenBoundToSessionAndKind(t *testing.T) {
	tok := Token("s1", KindAnthropic)
	if sid, ok := VerifyToken(tok, KindAnthropic); !ok || sid != "s1" {
		t.Fatalf("valid token rejected: %v %q", ok, sid)
	}
	if _, ok := VerifyToken(tok, KindOpenAI); ok {
		t.Fatal("anthropic capability accepted for openai")
	}
	forged := strings.Replace(tok, "s1.", "s2.", 1)
	if _, ok := VerifyToken(forged, KindAnthropic); ok {
		t.Fatal("token re-labelled for another session accepted")
	}
	for _, bad := range []string{"", "sk-ant-123", TokenPrefix, TokenPrefix + "s1.", TokenPrefix + ".abc"} {
		if _, ok := VerifyToken(bad, KindAnthropic); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestProxyRejectsBadOrMissingCapability(t *testing.T) {
	up, got := upstream(t)
	p := startProxy(t, staticResolver(map[string]Upstream{"anthropic:s1": {BaseURL: up.URL, APIKey: "sk-real"}}))
	url := p.BaseURL(KindAnthropic) + "/v1/messages"
	for name, hdr := range map[string]map[string]string{
		"missing":        {},
		"wrong":          {"X-Api-Key": "sk-ant-guess"},
		"other kind":     {"X-Api-Key": Token("s1", KindOpenAI)},
		"unknown sess":   {"X-Api-Key": Token("nope", KindAnthropic)},
		"bearer garbage": {"Authorization": "Bearer xyz"},
	} {
		resp := post(t, url, hdr, `{}`)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(b), "sk-real") {
			t.Errorf("%s: error body leaks the key", name)
		}
	}
	if got().path != "" {
		t.Fatal("a rejected request reached the upstream")
	}
}

func TestProxyRejectsNonLoopbackAndForwarded(t *testing.T) {
	up, _ := upstream(t)
	h := NewHandler(staticResolver(map[string]Upstream{"anthropic:s1": {BaseURL: up.URL, APIKey: "sk-real"}}), nil)
	tok := Token("s1", KindAnthropic)

	req := httptest.NewRequest(http.MethodPost, "/anthropic/v1/messages", strings.NewReader(`{}`))
	req.RemoteAddr = "192.168.1.20:5555"
	req.Header.Set("X-Api-Key", tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote peer: %d, want 403", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/anthropic/v1/messages", strings.NewReader(`{}`))
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("X-Api-Key", tok)
	req.Header.Set("X-Forwarded-For", "8.8.8.8")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forwarded: %d, want 403", rec.Code)
	}
}

func TestProxyRejectsOtherPaths(t *testing.T) {
	up, got := upstream(t)
	p := startProxy(t, staticResolver(map[string]Upstream{"anthropic:s1": {BaseURL: up.URL, APIKey: "sk-real"}}))
	tok := map[string]string{"X-Api-Key": Token("s1", KindAnthropic)}
	base := strings.TrimSuffix(p.BaseURL(KindAnthropic), "/anthropic")
	for _, path := range []string{
		"/", "/anthropic", "/anthropic/", "/anthropic/api/oauth/token", "/internal/n0/mcp",
		"/anthropic/v1/../../admin", "/anthropic/v1/%2e%2e/admin", "/anthropic/v1/a%2Fb", "/openai/v1/",
	} {
		req, _ := http.NewRequest(http.MethodPost, base+path, strings.NewReader(`{}`))
		req.URL.RawPath = path // keep the dot segments / escapes as sent
		for k, v := range tok {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, resp.StatusCode)
		}
	}
	if got().path != "" {
		t.Fatal("a disallowed path reached the upstream")
	}
}

func TestProxyHeaderSwapPerProvider(t *testing.T) {
	anth, gotAnth := upstream(t)
	compat, gotCompat := upstream(t)
	oai, gotOAI := upstream(t)
	p := startProxy(t, staticResolver(map[string]Upstream{
		"anthropic:claude":     {BaseURL: anth.URL, APIKey: "sk-ant-real"},
		"anthropic:deepseek":   {BaseURL: compat.URL + "/anthropic", APIKey: "ds-real"},
		"anthropic:openrouter": {BaseURL: compat.URL + "/api", APIKey: "or-real", Auth: AuthBearer},
		"openai:codex":         {BaseURL: oai.URL + "/v1", APIKey: "sk-oai-real", Auth: AuthBearer},
	}))

	hdr := map[string]string{
		"X-Api-Key":         Token("claude", KindAnthropic),
		"Anthropic-Version": "2023-06-01",
		"Anthropic-Beta":    "interleaved-thinking-2025-05-14",
		"Cookie":            "session_token=abc",
	}
	resp := post(t, p.BaseURL(KindAnthropic)+"/v1/messages?beta=true", hdr, `{"model":"x"}`)
	if resp.StatusCode != 200 || resp.Header.Get("Request-Id") != "req_123" {
		t.Fatalf("status %d / headers %v", resp.StatusCode, resp.Header)
	}
	c := gotAnth()
	if c.path != "/v1/messages" || c.query != "beta=true" || c.xAPIKey != "sk-ant-real" || c.auth != "" ||
		c.version != "2023-06-01" || c.beta != "interleaved-thinking-2025-05-14" || c.body != `{"model":"x"}` ||
		c.cookie != "" || c.forwarded != "" {
		t.Fatalf("anthropic upstream saw %+v", c)
	}

	post(t, p.BaseURL(KindAnthropic)+"/v1/messages", map[string]string{"X-Api-Key": Token("deepseek", KindAnthropic)}, `{}`)
	if c := gotCompat(); c.path != "/anthropic/v1/messages" || c.xAPIKey != "ds-real" || c.auth != "" {
		t.Fatalf("deepseek upstream saw %+v", c)
	}

	post(t, p.BaseURL(KindAnthropic)+"/v1/messages", map[string]string{"Authorization": "Bearer " + Token("openrouter", KindAnthropic)}, `{}`)
	if c := gotCompat(); c.path != "/api/v1/messages" || c.auth != "Bearer or-real" || c.xAPIKey != "" {
		t.Fatalf("openrouter upstream saw %+v", c)
	}

	post(t, p.BaseURL(KindOpenAI)+"/responses", map[string]string{"Authorization": "Bearer " + Token("codex", KindOpenAI)}, `{}`)
	if c := gotOAI(); c.path != "/v1/responses" || c.auth != "Bearer sk-oai-real" || c.xAPIKey != "" {
		t.Fatalf("openai upstream saw %+v", c)
	}
}

// SSE must be streamed: each event has to reach the client before the
// upstream sends the next one.
func TestProxyStreamsSSEIncrementally(t *testing.T) {
	release := make(chan struct{})
	sse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "event: content_block_delta\ndata: {\"i\":%d}\n\n", i)
			fl.Flush()
			select {
			case <-release:
			case <-time.After(5 * time.Second):
				return
			}
		}
	}))
	defer sse.Close()
	p := startProxy(t, staticResolver(map[string]Upstream{"anthropic:s1": {BaseURL: sse.URL, APIKey: "k"}}))
	resp := post(t, p.BaseURL(KindAnthropic)+"/v1/messages", map[string]string{"X-Api-Key": Token("s1", KindAnthropic)}, `{"stream":true}`)
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	rd := bufio.NewReader(resp.Body)
	for i := 0; i < 3; i++ {
		lineCh := make(chan string, 1)
		go func() {
			var sb strings.Builder
			for {
				l, err := rd.ReadString('\n')
				sb.WriteString(l)
				if err != nil || l == "\n" {
					break
				}
			}
			lineCh <- sb.String()
		}()
		select {
		case ev := <-lineCh:
			if !strings.Contains(ev, fmt.Sprintf(`{"i":%d}`, i)) {
				t.Fatalf("event %d: %q", i, ev)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("event %d was buffered (not received before the upstream continued)", i)
		}
		release <- struct{}{}
	}
}

// The resolver is consulted on every request, so a provider switch applies
// without restarting anything.
func TestProxyFollowsProviderChange(t *testing.T) {
	a, gotA := upstream(t)
	b, gotB := upstream(t)
	var mu sync.Mutex
	cur := Upstream{BaseURL: a.URL, APIKey: "key-a"}
	p := startProxy(t, func(_ context.Context, sid string, kind Kind) (Upstream, error) {
		mu.Lock()
		defer mu.Unlock()
		return cur, nil
	})
	hdr := map[string]string{"X-Api-Key": Token("s1", KindAnthropic)}
	post(t, p.BaseURL(KindAnthropic)+"/v1/messages", hdr, `{}`)
	mu.Lock()
	cur = Upstream{BaseURL: b.URL + "/anthropic", APIKey: "key-b"}
	mu.Unlock()
	post(t, p.BaseURL(KindAnthropic)+"/v1/messages", hdr, `{}`)
	if gotA().xAPIKey != "key-a" || gotB().xAPIKey != "key-b" || gotB().path != "/anthropic/v1/messages" {
		t.Fatalf("a=%+v b=%+v", gotA(), gotB())
	}
}

func TestProxyLargeBody(t *testing.T) {
	up, got := upstream(t)
	p := startProxy(t, staticResolver(map[string]Upstream{"anthropic:s1": {BaseURL: up.URL, APIKey: "k"}}))
	big := strings.Repeat("a", 20<<20)
	resp := post(t, p.BaseURL(KindAnthropic)+"/v1/messages", map[string]string{"X-Api-Key": Token("s1", KindAnthropic)}, big)
	if resp.StatusCode != 200 || len(got().body) != len(big) {
		t.Fatalf("status %d, upstream got %d bytes", resp.StatusCode, len(got().body))
	}
}

func TestProxyUpstreamErrorDoesNotLeakKey(t *testing.T) {
	p := startProxy(t, staticResolver(map[string]Upstream{"anthropic:s1": {BaseURL: "http://127.0.0.1:1", APIKey: "sk-secret-123"}}))
	resp := post(t, p.BaseURL(KindAnthropic)+"/v1/messages", map[string]string{"X-Api-Key": Token("s1", KindAnthropic)}, `{}`)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadGateway || strings.Contains(string(b), "sk-secret") {
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
}

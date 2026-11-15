package credproxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLiveClaudeCLIThroughProxy runs the real Claude CLI against the proxy
// and a fake Anthropic upstream. Opt-in: EGGIFY_LIVE_CLI=1.
func TestLiveClaudeCLIThroughProxy(t *testing.T) {
	if os.Getenv("EGGIFY_LIVE_CLI") == "" {
		t.Skip("set EGGIFY_LIVE_CLI=1 to run against the installed claude CLI")
	}
	var mu sync.Mutex
	var seen []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, fmt.Sprintf("%s %s key=%q auth=%q stream=%v", r.Method, r.URL.Path, r.Header.Get("X-Api-Key"), r.Header.Get("Authorization"), strings.Contains(string(body), `"stream":true`)))
		mu.Unlock()
		if !strings.HasSuffix(r.URL.Path, "/v1/messages") {
			w.WriteHeader(404)
			return
		}
		if !strings.Contains(string(body), `"stream":true`) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"PONG"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, ev := range []string{
			`message_start`, `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
			`content_block_start`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`content_block_delta`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"PONG"}}`,
			`content_block_stop`, `{"type":"content_block_stop","index":0}`,
			`message_delta`, `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
			`message_stop`, `{"type":"message_stop"}`,
		} {
			if strings.HasPrefix(ev, "{") {
				fmt.Fprintf(w, "data: %s\n\n", ev)
				fl.Flush()
			} else {
				fmt.Fprintf(w, "event: %s\n", ev)
			}
		}
	}))
	defer up.Close()

	p, err := Start(func(_ context.Context, sid string, kind Kind) (Upstream, error) {
		return Upstream{BaseURL: up.URL, APIKey: "sk-real-upstream-key"}, nil
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", "say PONG", "--model", "sonnet", "--max-turns", "1")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		"ANTHROPIC_BASE_URL="+p.BaseURL(KindAnthropic),
		"ANTHROPIC_API_KEY="+Token("live-session", KindAnthropic),
		"ANTHROPIC_AUTH_TOKEN=",
		"_CLAUDE_CODE_ASSUME_FIRST_PARTY_BASE_URL="+os.Getenv("EGGIFY_LIVE_FIRST_PARTY"),
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
	)
	out, err := cmd.CombinedOutput()
	mu.Lock()
	defer mu.Unlock()
	t.Logf("cli output: %s (err %v)", out, err)
	for _, s := range seen {
		t.Logf("upstream saw: %s", s)
	}
	if !strings.Contains(string(out), "PONG") {
		t.Fatal("CLI did not get the proxied answer")
	}
	for _, s := range seen {
		if !strings.Contains(s, `key="sk-real-upstream-key"`) {
			t.Fatalf("upstream got a request without the real key: %s", s)
		}
	}
}

package n0

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// The proxy, not eggify-tools, enforces approval: an agent can call it directly.
func TestProxyEnforcesToolGuard(t *testing.T) {
	e := newEnv(t)
	sid := "44444444-4444-4444-4444-444444444444"
	e.newSession(sid, true)
	e.connect("demo-app", 0)
	tok := ProxyToken(sid)
	write := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"n0_repo_write_files","arguments":{"message":"m","files":[{"path":"a","content":"x"}]}}}`
	read := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"n0_app_status"}}`

	// No guard installed: high-impact calls fail closed, reads pass.
	_, body := e.proxy(sid, tok, write, nil)
	if text, isErr := toolResultText(t, body); !isErr || !strings.Contains(text, "approval") {
		t.Fatalf("write without a guard: %q %v", text, isErr)
	}
	_, body = e.proxy(sid, tok, read, nil)
	if _, isErr := toolResultText(t, body); isErr {
		t.Fatalf("read-only call blocked: %s", body)
	}

	var mu sync.Mutex
	var seen []string
	e.m.SetToolGuard(func(ctx context.Context, sessionID, tool string, args json.RawMessage) (bool, string) {
		mu.Lock()
		seen = append(seen, sessionID+" "+tool+" "+string(args))
		mu.Unlock()
		return false, "The user did not approve " + tool
	})
	_, body = e.proxy(sid, tok, write, nil)
	if text, isErr := toolResultText(t, body); !isErr || text != "The user did not approve n0_repo_write_files" {
		t.Fatalf("denied write: %q %v", text, isErr)
	}
	_, body = e.proxy(sid, tok, read, nil)
	if _, isErr := toolResultText(t, body); isErr {
		t.Fatalf("read-only call blocked: %s", body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || !strings.HasPrefix(seen[0], sid+" n0_repo_write_files ") || !strings.Contains(seen[0], `"message":"m"`) {
		t.Fatalf("guard calls: %v", seen)
	}
}

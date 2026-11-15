package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/n0"
	"github.com/schlunsen/eggify/internal/n0/n0test"
)

// TestEggifyToolsExposesAndProxiesN0Tools runs the whole chain in-process:
// connect over OAuth → eggify-tools tools/list includes n0's tools (schemas
// intact) → tools/call is proxied through the Eggify server → result.
func TestEggifyToolsExposesAndProxiesN0Tools(t *testing.T) {
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.ResetInstance()
	m := n0.NewManager(db, make([]byte, 32))
	ls, err := n0.StartLoopback(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ls.Close()
	fake := n0test.New()
	defer fake.Close()

	sid := uuid.NewString()
	db.GetDB().Exec(`INSERT INTO agent_sessions (id, status) VALUES (?, 'idle')`, sid)
	database.NewConnectorRepository(db).EnableSessionConnector(sid, "n0")

	res, err := m.StartAuthorization(context.Background(), n0.StartRequest{Host: fake.URL, RedirectURI: ls.CallbackURL(), RedirectKey: n0.RedirectKeyLoopback})
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := fake.Approve(res.AuthorizeURL, "demo-app")
	resp, err := http.Get(loc)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// The Eggify server hands eggify-tools only this environment.
	for k, v := range n0.ProxyEnv(sid, ls.ProxyURL()) {
		t.Setenv(k, v)
	}
	srv := NewMCPServer(nil)

	listResp := srv.HandleRequest(MCPRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"})
	raw, _ := json.Marshal(listResp)
	for _, sec := range fake.Secrets() {
		if strings.Contains(string(raw), sec) {
			t.Fatal("tools/list leaks a token")
		}
	}
	var list struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	names := map[string]json.RawMessage{}
	for _, tl := range list.Result.Tools {
		names[tl.Name] = tl.InputSchema
	}
	for _, want := range []string{"get_current_session", "store_memory", "n0_connection_info", "n0_app_guide", "n0_repo_write_files", "n0_app_status", "runpod__list_pods"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("tool %s missing from eggify-tools tools/list", want)
		}
	}
	if !strings.Contains(string(names["n0_repo_write_files"]), `"items"`) {
		t.Fatalf("n0 schema not passed through verbatim: %s", names["n0_repo_write_files"])
	}

	// Writing externally needs the user's approval, enforced by the Eggify
	// server's proxy (here: the user approves).
	m.SetToolGuard(func(ctx context.Context, sessionID, tool string, args json.RawMessage) (bool, string) {
		return sessionID == sid && tool == "n0_repo_write_files", "unexpected"
	})
	call := srv.HandleRequest(MCPRequest{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: map[string]interface{}{
		"name": "n0_repo_write_files",
		"arguments": map[string]interface{}{
			"message": "hello",
			"files":   []interface{}{map[string]interface{}{"path": "n0-app.json", "content": "{}"}},
		},
	}})
	out, _ := json.Marshal(call)
	if !strings.Contains(string(out), "committed 1 files: hello") || strings.Contains(string(out), `"isError":true`) {
		t.Fatalf("proxied call result: %s", out)
	}
	// Workspace connector tools (<slug>__<tool>) are forwarded like n0_* tools.
	conn := srv.HandleRequest(MCPRequest{JSONRPC: "2.0", ID: 4, Method: "tools/call", Params: map[string]interface{}{"name": "runpod__list_pods"}})
	out, _ = json.Marshal(conn)
	if !strings.Contains(string(out), "pod1") || strings.Contains(string(out), `"isError":true`) {
		t.Fatalf("proxied connector call result: %s", out)
	}
	if fake.ToolCalls.Load() < 3 { // n0_connection_info at connect + the two calls
		t.Fatalf("n0 saw %d tool calls", fake.ToolCalls.Load())
	}

	// An eggify-tools process without the proxy env has no n0 tools.
	for _, k := range []string{n0.EnvSessionID, n0.EnvProxyURL, n0.EnvProxyToken} {
		t.Setenv(k, "")
	}
	plain := NewMCPServer(nil)
	if plain.n0 != nil {
		t.Fatal("proxy enabled without env")
	}
	r := plain.HandleRequest(MCPRequest{JSONRPC: "2.0", ID: 3, Method: "tools/call", Params: map[string]interface{}{"name": "n0_app_status"}})
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), "not available") {
		t.Fatalf("expected unavailable error: %s", b)
	}
}

func TestN0ProxyRefusesNonLoopbackURL(t *testing.T) {
	t.Setenv(n0.EnvSessionID, "s")
	t.Setenv(n0.EnvProxyToken, "t")
	for _, u := range []string{"http://example.com/internal/n0/mcp", "https://127.0.0.1:3333/internal/n0/mcp", "http://10.0.0.1:1/x"} {
		t.Setenv(n0.EnvProxyURL, u)
		if newN0ProxyFromEnv() != nil {
			t.Errorf("proxy accepted %s", u)
		}
	}
	t.Setenv(n0.EnvProxyURL, "http://127.0.0.1:4242/internal/n0/mcp")
	if newN0ProxyFromEnv() == nil {
		t.Error("loopback proxy refused")
	}
}

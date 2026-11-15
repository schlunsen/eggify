package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/schlunsen/eggify/internal/n0"
)

// fakeControl is a stand-in for the Eggify server's loopback control endpoints.
type fakeControl struct {
	srv     *httptest.Server
	mu      sync.Mutex
	calls   []string // path + body
	allowed bool
}

func newFakeControl(t *testing.T, sid string) *fakeControl {
	t.Helper()
	f := &fakeControl{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(n0.HeaderSessionID) != sid || r.Header.Get("Authorization") != "Bearer "+n0.ProxyToken(sid) {
			w.WriteHeader(401)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, r.URL.Path+" "+string(body))
		allowed := f.allowed
		f.mu.Unlock()
		switch r.URL.Path {
		case n0.ControlToolGuardPath:
			if allowed {
				w.Write([]byte(`{"allowed":true}`))
			} else {
				w.Write([]byte(`{"allowed":false,"message":"The user did not approve it"}`))
			}
		case n0.ControlHandoverCreatePath:
			w.Write([]byte(`{"handover_token":"tok","source_session_id":"` + sid + `","created_at":"2026-10-02T10:00:00Z","expires_at":"2026-10-02T11:00:00Z","context":{}}`))
		case n0.ControlHandoverAcceptPath:
			w.Write([]byte(`{"session_id":"new","handover_applied":true,"session":{"status":"idle"},"pending_prompt":"go","note":"created idle"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeControl) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func callTool(srv *MCPServer, name string, args map[string]interface{}) string {
	r := srv.HandleRequest(MCPRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: map[string]interface{}{"name": name, "arguments": args}})
	b, _ := json.Marshal(r)
	return string(b)
}

func setControlEnv(t *testing.T, sid, url string) {
	t.Setenv(n0.EnvSessionID, sid)
	t.Setenv(n0.EnvProxyToken, n0.ProxyToken(sid))
	t.Setenv(n0.EnvControlURL, url)
	t.Setenv(n0.EnvProxyURL, "")
}

func TestHighImpactToolsFailClosedOutsideAnEggifySession(t *testing.T) {
	setControlEnv(t, "", "")
	srv := NewMCPServer(nil)
	if srv.control != nil {
		t.Fatal("control client without env")
	}
	for _, tool := range []string{"gpu_launch", "gpu_push", "deploy_app", "undeploy_app", "gpu_delete_volume"} {
		out := callTool(srv, tool, map[string]interface{}{"tier": "pro"})
		if !strings.Contains(out, `"isError":true`) || !strings.Contains(out, "approval") {
			t.Errorf("%s ran without approval: %s", tool, out)
		}
	}
	// Handovers need the server too.
	if out := callTool(srv, "create_handover", nil); !strings.Contains(out, "only available inside an Eggify agent session") {
		t.Errorf("create_handover: %s", out)
	}
	if out := callTool(srv, "accept_handover", map[string]interface{}{"handover_token": "x"}); !strings.Contains(out, "only available inside an Eggify agent session") {
		t.Errorf("accept_handover: %s", out)
	}
}

func TestHighImpactToolsAskTheEggifyServer(t *testing.T) {
	sid := "55555555-5555-5555-5555-555555555555"
	fc := newFakeControl(t, sid)
	setControlEnv(t, sid, fc.srv.URL)
	srv := NewMCPServer(nil)
	if srv.control == nil {
		t.Fatal("control client not configured")
	}

	// Denied: the tool does not run and the agent gets the server's message.
	out := callTool(srv, "gpu_launch", map[string]interface{}{"tier": "beast"})
	if !strings.Contains(out, "The user did not approve it") || !strings.Contains(out, `"isError":true`) {
		t.Fatalf("denied launch: %s", out)
	}
	calls := fc.recorded()
	if len(calls) != 1 || !strings.HasPrefix(calls[0], n0.ControlToolGuardPath+" ") || !strings.Contains(calls[0], `"tier":"beast"`) {
		t.Fatalf("guard request: %v", calls)
	}

	// Approved: the tool runs (and here fails for lack of a GPU control plane).
	fc.mu.Lock()
	fc.allowed = true
	fc.mu.Unlock()
	t.Setenv("EGGIFY_CONTROL_PLANE_URL", "")
	out = callTool(srv, "gpu_launch", map[string]interface{}{"tier": "starter"})
	if !strings.Contains(out, "GPU sidecar not available") {
		t.Fatalf("approved launch did not run the tool: %s", out)
	}

	// Read-only tools never ask.
	before := len(fc.recorded())
	callTool(srv, "gpu_status", nil)
	callTool(srv, "list_deployed_apps", nil)
	if len(fc.recorded()) != before {
		t.Fatalf("read-only tools asked the server: %v", fc.recorded()[before:])
	}
}

func TestHandoverToolsGoThroughTheEggifyServer(t *testing.T) {
	sid := "66666666-6666-6666-6666-666666666666"
	fc := newFakeControl(t, sid)
	setControlEnv(t, sid, fc.srv.URL)
	srv := NewMCPServer(nil) // no session manager: nothing may touch the DB directly

	out := callTool(srv, "create_handover", map[string]interface{}{"session_id": "77777777-7777-7777-7777-777777777777"})
	if !strings.Contains(out, "only export your own session") {
		t.Fatalf("foreign session export: %s", out)
	}
	if len(fc.recorded()) != 0 {
		t.Fatal("foreign export reached the server")
	}

	out = callTool(srv, "create_handover", map[string]interface{}{"handover_note": "n", "preserve_yolo_mode": true})
	if !strings.Contains(out, `\"handover_token\": \"tok\"`) || !strings.Contains(out, `\"permissions_carried\": false`) {
		t.Fatalf("create_handover: %s", out)
	}
	if c := fc.recorded(); len(c) != 1 || strings.Contains(c[0], "preserve") || !strings.Contains(c[0], `"handover_note":"n"`) {
		t.Fatalf("create request: %v", c)
	}

	out = callTool(srv, "accept_handover", map[string]interface{}{"handover_token": "tok", "auto_start": true, "initial_prompt": "go"})
	if !strings.Contains(out, `\"started\": false`) || !strings.Contains(out, `\"pending_prompt\": \"go\"`) {
		t.Fatalf("accept_handover: %s", out)
	}
}

func TestControlClientRefusesNonLoopbackURL(t *testing.T) {
	for _, u := range []string{"http://example.com", "https://127.0.0.1:3333", "http://10.0.0.1:1"} {
		setControlEnv(t, "s", u)
		if newControlClientFromEnv() != nil {
			t.Errorf("control client accepted %s", u)
		}
	}
	setControlEnv(t, "s", "http://127.0.0.1:4242")
	if newControlClientFromEnv() == nil {
		t.Error("loopback control URL refused")
	}
}

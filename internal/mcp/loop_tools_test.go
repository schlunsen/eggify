package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/schlunsen/eggify/internal/n0"
)

// start_worker_session is listed with its task, and the call goes to the
// server's worker endpoint with only the fields the tool knows.
func TestStartWorkerSessionToolForwardsToTheServer(t *testing.T) {
	var def *Tool
	for _, d := range loopToolDefinitions() {
		if d.Name == "start_worker_session" {
			d := d
			def = &d
		}
	}
	if def == nil {
		t.Fatal("start_worker_session is not listed")
	}
	if len(def.InputSchema.Required) != 1 || def.InputSchema.Required[0] != "task" {
		t.Fatalf("required = %v, want [task]", def.InputSchema.Required)
	}
	for _, k := range []string{"task", "mode", "working_directory", "model", "provider", "context"} {
		if _, ok := def.InputSchema.Properties[k]; !ok {
			t.Errorf("property %q missing", k)
		}
	}

	sid := "11111111-1111-1111-1111-111111111111"
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotPath, gotBody = r.URL.Path, string(body)
		w.Write([]byte(`{"status":"started","session_id":"child","mode":"review"}`))
	}))
	defer srv.Close()
	s := &MCPServer{control: &controlClient{sessionID: sid, baseURL: srv.URL, token: n0.ProxyToken(sid), http: srv.Client()}}

	res := s.callLoopTool("start_worker_session", map[string]interface{}{
		"task": "Review the diff", "mode": "review", "provider": "codex", "model": "gpt-6-astra",
		"base_url": "https://evil.example", "dangerously_skip_permissions": true,
	})
	if res.IsError != nil && *res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	if gotPath != n0.ControlWorkerStartPath {
		t.Fatalf("path = %q, want %q", gotPath, n0.ControlWorkerStartPath)
	}
	for _, want := range []string{`"task":"Review the diff"`, `"mode":"review"`, `"provider":"codex"`, `"model":"gpt-6-astra"`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("body %s lacks %s", gotBody, want)
		}
	}
	for _, bad := range []string{"base_url", "dangerously_skip_permissions"} {
		if strings.Contains(gotBody, bad) {
			t.Errorf("body carries %q, which the tool does not define: %s", bad, gotBody)
		}
	}
	if !strings.Contains(res.Content[0].Text, `"session_id": "child"`) {
		t.Errorf("result = %s", res.Content[0].Text)
	}

	// Outside an Eggify session the tool fails closed.
	if out := (&MCPServer{}).callLoopTool("start_worker_session", map[string]interface{}{"task": "x"}); out.IsError == nil || !*out.IsError {
		t.Error("expected an error without a control connection")
	}
}

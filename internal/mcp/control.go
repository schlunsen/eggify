package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/schlunsen/eggify/internal/n0"
	"github.com/schlunsen/eggify/internal/toolguard"
)

// controlClient calls the Eggify server's loopback session control endpoints
// (n0.ControlPath*). eggify-tools uses it for everything the server must decide
// for this session — tool approvals and handovers — instead of acting on
// eggify.db itself. It authenticates with the session capability from its env.
type controlClient struct {
	sessionID string
	baseURL   string
	token     string
	http      *http.Client
}

// newControlClientFromEnv returns nil unless the Eggify server configured this
// eggify-tools process for a session.
func newControlClientFromEnv() *controlClient {
	sid := os.Getenv(n0.EnvSessionID)
	base := os.Getenv(n0.EnvControlURL)
	token := os.Getenv(n0.EnvProxyToken)
	if sid == "" || base == "" || token == "" {
		return nil
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || !n0.IsLoopbackAddr(u.Hostname()) {
		// Loopback only: the capability is never sent off the machine.
		return nil
	}
	return &controlClient{
		sessionID: sid,
		baseURL:   strings.TrimRight(base, "/"),
		token:     token,
		// No timeout: an approval waits for the user as long as it takes. The
		// server ends the wait when the turn is interrupted or the session ends.
		http: &http.Client{},
	}
}

// controlError is an error reply from the Eggify server.
type controlError struct {
	status int
	msg    string
}

func (e *controlError) Error() string { return e.msg }

func (c *controlClient) post(ctx context.Context, path string, in, out interface{}) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set(n0.HeaderSessionID, c.sessionID)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Eggify server unreachable: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("Eggify server returned HTTP %d", resp.StatusCode)
		}
		return &controlError{status: resp.StatusCode, msg: e.Error}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("invalid response from the Eggify server")
	}
	return nil
}

// guardTool asks the Eggify server whether a high-impact eggify-tools tool may run
// (the server asks the user). It returns a tool error result when it must not.
// Tools that don't need the server pass straight through.
func (s *MCPServer) guardTool(name string, args map[string]interface{}) (CallToolResult, bool) {
	d := toolguard.Classify(name)
	if !d.NeedsServer() {
		return CallToolResult{}, true
	}
	if s.control == nil {
		if !d.Confirm {
			return CallToolResult{}, true // only cap bookkeeping, nothing to approve
		}
		return mcpErrorResult(name + " needs the user's approval in Eggify, but this eggify-tools process is not connected to an Eggify session, so it cannot ask. Ask the user to run it from an Eggify session."), false
	}
	var verdict struct {
		Allowed bool   `json:"allowed"`
		Message string `json:"message"`
	}
	if args == nil {
		args = map[string]interface{}{}
	}
	err := s.control.post(context.Background(), n0.ControlToolGuardPath, map[string]interface{}{"tool": name, "arguments": args}, &verdict)
	if err != nil {
		if !d.Confirm {
			return CallToolResult{}, true
		}
		return mcpErrorResult(name + " was not run: could not get the user's approval (" + err.Error() + ")"), false
	}
	if !verdict.Allowed {
		return mcpErrorResult(verdict.Message), false
	}
	return CallToolResult{}, true
}

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
	"time"

	"github.com/schlunsen/eggify/internal/n0"
)

// n0Proxy forwards n0's tools (n0_* and workspace connector tools) to the Eggify server's loopback n0 proxy. The
// eggify-tools process never holds an n0 token: it holds only the session-scoped
// proxy capability that the Eggify server passed in its environment.
type n0Proxy struct {
	sessionID string
	endpoint  string
	token     string
	http      *http.Client
}

// newN0ProxyFromEnv returns nil unless the Eggify server configured the proxy
// for this eggify-tools process.
func newN0ProxyFromEnv() *n0Proxy {
	sid := os.Getenv(n0.EnvSessionID)
	endpoint := os.Getenv(n0.EnvProxyURL)
	token := os.Getenv(n0.EnvProxyToken)
	if sid == "" || endpoint == "" || token == "" {
		return nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || !n0.IsLoopbackAddr(u.Hostname()) {
		// The proxy is loopback-only by design; refuse anything else so the
		// capability is never sent off the machine.
		return nil
	}
	return &n0Proxy{
		sessionID: sid,
		endpoint:  endpoint,
		token:     token,
		// n0 deploys and CI waits can take a while.
		http: &http.Client{Timeout: 10 * time.Minute},
	}
}

func (p *n0Proxy) rpc(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	payload, err := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set(n0.HeaderSessionID, p.sessionID)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Eggify server unreachable: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Eggify n0 proxy returned HTTP %d", resp.StatusCode)
	}
	var rr struct {
		Result json.RawMessage `json:"result"`
		Error  *MCPError       `json:"error"`
	}
	if err := json.Unmarshal(body, &rr); err != nil {
		return nil, fmt.Errorf("invalid response from Eggify n0 proxy")
	}
	if rr.Error != nil {
		return nil, fmt.Errorf("%s", rr.Error.Message)
	}
	return rr.Result, nil
}

// listTools returns n0's tool definitions (raw JSON, schema untouched).
func (p *n0Proxy) listTools() []json.RawMessage {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := p.rpc(ctx, "tools/list", map[string]interface{}{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "eggify-tools: n0 tools unavailable: %v\n", err)
		return nil
	}
	var out struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(res, &out) != nil {
		return nil
	}
	tools := make([]json.RawMessage, 0, len(out.Tools))
	for _, t := range out.Tools {
		var head struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(t, &head) == nil && n0.IsProxiedTool(head.Name) {
			tools = append(tools, t)
		}
	}
	return tools
}

// callTool forwards a tools/call and returns the raw CallToolResult.
func (p *n0Proxy) callTool(name string, args map[string]interface{}) json.RawMessage {
	if args == nil {
		args = map[string]interface{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res, err := p.rpc(ctx, "tools/call", map[string]interface{}{"name": name, "arguments": args})
	if err != nil {
		return n0.ToolErrorResult("n0 tool call failed: " + err.Error())
	}
	return res
}

// isN0Tool reports whether a tool name should be forwarded to n0: its n0_*
// tools and the workspace connector tools (<slug>__<tool>) it lists when the
// connection was granted workspace:connectors.
func isN0Tool(name string) bool { return n0.IsProxiedTool(name) }

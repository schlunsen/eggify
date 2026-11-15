package n0

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/schlunsen/eggify/internal/n0/oauth"
	"github.com/schlunsen/eggify/internal/version"
)

// Protocol versions n0 accepts; the first is what Eggify asks for.
var supportedProtocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

const maxResponseBody = 64 << 20

// UnauthorizedError is a 401 from the MCP endpoint.
type UnauthorizedError struct {
	// ErrorCode is the error auth-param ("invalid_token" when a token was sent
	// but is bad, expired or revoked).
	ErrorCode string
}

func (e *UnauthorizedError) Error() string {
	if e.ErrorCode != "" {
		return "n0 rejected the access token (" + e.ErrorCode + ")"
	}
	return "n0 requires authorization"
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("n0 MCP error %d: %s", e.Code, e.Message) }

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *RPCError       `json:"error"`
}

// Transport is a Streamable HTTP MCP client for one n0 connection. It POSTs
// JSON-RPC, accepts application/json (and a single-message
// text/event-stream response, for forward compatibility), tracks the
// Mcp-Session-Id and performs initialize once.
//
// The access token is passed per call and never stored here.
type Transport struct {
	Endpoint string
	HTTP     *http.Client

	mu              sync.Mutex
	initialized     bool
	sessionID       string
	protocolVersion string
	nextID          atomic.Int64
}

// NewTransport creates a transport for an MCP endpoint URL.
func NewTransport(endpoint string, hc *http.Client) *Transport {
	return &Transport{Endpoint: endpoint, HTTP: hc}
}

// Reset forgets the MCP session (next call re-initializes).
func (t *Transport) Reset() {
	t.mu.Lock()
	t.initialized = false
	t.sessionID = ""
	t.protocolVersion = ""
	t.mu.Unlock()
}

// errSessionGone signals a 404 for our Mcp-Session-Id.
var errSessionGone = errors.New("n0 MCP session expired")

// Call performs one JSON-RPC request, initializing the session first when needed.
func (t *Transport) Call(ctx context.Context, token, method string, params interface{}) (json.RawMessage, error) {
	if err := t.ensureInitialized(ctx, token); err != nil {
		return nil, err
	}
	res, err := t.request(ctx, token, method, params)
	if errors.Is(err, errSessionGone) {
		t.Reset()
		if err := t.ensureInitialized(ctx, token); err != nil {
			return nil, err
		}
		res, err = t.request(ctx, token, method, params)
	}
	return res, err
}

func (t *Transport) ensureInitialized(ctx context.Context, token string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.initialized {
		return nil
	}
	t.sessionID = ""
	t.protocolVersion = ""
	params := map[string]interface{}{
		"protocolVersion": supportedProtocolVersions[0],
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "eggify", "version": version.Version},
	}
	raw, sid, err := t.do(ctx, token, "", "initialize", params, false)
	if err != nil {
		return err
	}
	var res struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(raw, &res)
	ok := false
	for _, v := range supportedProtocolVersions {
		if v == res.ProtocolVersion {
			ok = true
		}
	}
	if !ok {
		return fmt.Errorf("n0 MCP server negotiated unsupported protocol version %q", res.ProtocolVersion)
	}
	t.sessionID = sid
	t.protocolVersion = res.ProtocolVersion
	if _, _, err := t.doLocked(ctx, token, "notifications/initialized", nil, true); err != nil {
		return err
	}
	t.initialized = true
	return nil
}

func (t *Transport) request(ctx context.Context, token, method string, params interface{}) (json.RawMessage, error) {
	t.mu.Lock()
	sid, pv := t.sessionID, t.protocolVersion
	t.mu.Unlock()
	raw, _, err := t.doWith(ctx, token, sid, pv, method, params, false)
	return raw, err
}

// doLocked sends with the current (locked) session state.
func (t *Transport) doLocked(ctx context.Context, token, method string, params interface{}, notify bool) (json.RawMessage, string, error) {
	return t.doWith(ctx, token, t.sessionID, t.protocolVersion, method, params, notify)
}

func (t *Transport) do(ctx context.Context, token, sid, method string, params interface{}, notify bool) (json.RawMessage, string, error) {
	return t.doWith(ctx, token, sid, "", method, params, notify)
}

func (t *Transport) doWith(ctx context.Context, token, sid, protocolVersion, method string, params interface{}, notify bool) (json.RawMessage, string, error) {
	msg := map[string]interface{}{"jsonrpc": "2.0", "method": method}
	var id int64
	if !notify {
		id = t.nextID.Add(1)
		msg["id"] = id
	}
	if params != nil {
		msg["params"] = params
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	if protocolVersion != "" {
		req.Header.Set("MCP-Protocol-Version", protocolVersion)
	}
	resp, err := t.HTTP.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("n0 MCP request failed: %v", unwrapURLError(err))
	}
	defer resp.Body.Close()
	newSID := resp.Header.Get("Mcp-Session-Id")

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, "", &UnauthorizedError{ErrorCode: oauth.ParseAuthParam(resp.Header.Get("WWW-Authenticate"), "error")}
	case resp.StatusCode == http.StatusNotFound && sid != "":
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, "", errSessionGone
	case notify && (resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent):
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, newSID, nil
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", fmt.Errorf("n0 MCP endpoint returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, "", fmt.Errorf("reading n0 MCP response: %w", err)
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	var rr *rpcResponse
	if mediaType == "text/event-stream" {
		rr, err = parseSSEResponse(body, id)
	} else {
		rr = &rpcResponse{}
		err = json.Unmarshal(body, rr)
	}
	if err != nil {
		return nil, "", fmt.Errorf("invalid n0 MCP response: %w", err)
	}
	if rr.Error != nil {
		return nil, newSID, rr.Error
	}
	return rr.Result, newSID, nil
}

// parseSSEResponse returns the JSON-RPC response with the given id from an
// SSE body (servers may answer a POST with a short event stream).
func parseSSEResponse(body []byte, id int64) (*rpcResponse, error) {
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), maxResponseBody)
	var data strings.Builder
	flush := func() (*rpcResponse, bool) {
		defer data.Reset()
		if data.Len() == 0 {
			return nil, false
		}
		var rr rpcResponse
		if json.Unmarshal([]byte(data.String()), &rr) != nil {
			return nil, false
		}
		if string(rr.ID) == fmt.Sprint(id) && (rr.Result != nil || rr.Error != nil) {
			return &rr, true
		}
		return nil, false
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if rr, ok := flush(); ok {
				return rr, nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if rr, ok := flush(); ok {
		return rr, nil
	}
	return nil, errors.New("no JSON-RPC response in event stream")
}

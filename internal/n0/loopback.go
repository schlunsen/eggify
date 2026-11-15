package n0

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/schlunsen/eggify/internal/toolguard"
)

// ProxyPath is the internal endpoint eggify-tools forwards n0 tool calls to.
const ProxyPath = "/internal/n0/mcp"

// Header carrying the session id on proxy requests.
const HeaderSessionID = "X-Eggify-Session-Id"

const maxProxyBody = 32 << 20

// LoopbackServer is a plain-HTTP listener bound to 127.0.0.1 that serves
//   - POST /internal/n0/mcp — the eggify-tools → Eggify proxy (capabilities, never credentials)
//   - GET  /api/connectors/n0/oauth/callback — the loopback OAuth redirect target
//
// It is separate from the main (usually TLS, possibly tunnelled) server so
// the proxy is never reachable from outside the machine and n0 can use its
// http://127.0.0.1 loopback redirect rule.
type LoopbackServer struct {
	m        *Manager
	ln       net.Listener
	srv      *http.Server
	mux      *http.ServeMux
	port     int
	baseURL  string
	shutdown chan struct{}
}

// StartLoopback starts the loopback listener on 127.0.0.1 (port 0 = any).
func StartLoopback(m *Manager, port int) (*LoopbackServer, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("n0 loopback listener: %w", err)
	}
	ls := &LoopbackServer{m: m, ln: ln, port: ln.Addr().(*net.TCPAddr).Port, shutdown: make(chan struct{})}
	ls.baseURL = fmt.Sprintf("http://127.0.0.1:%d", ls.port)
	mux := http.NewServeMux()
	mux.HandleFunc(ProxyPath, ls.m.ServeProxy)
	mux.HandleFunc(CallbackPath, ls.m.ServeCallback)
	ls.mux = mux
	ls.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = ls.srv.Serve(ln) }()
	return ls, nil
}

// Handle mounts another loopback-only endpoint (e.g. Eggify's session control
// endpoints for eggify-tools). Handlers must do their own capability checks.
func (ls *LoopbackServer) Handle(pattern string, h http.Handler) { ls.mux.Handle(pattern, h) }

// BaseURL is http://127.0.0.1:<port>.
func (ls *LoopbackServer) BaseURL() string { return ls.baseURL }

// ProxyURL is the full proxy endpoint URL.
func (ls *LoopbackServer) ProxyURL() string { return ls.baseURL + ProxyPath }

// CallbackURL is the loopback OAuth redirect URI.
func (ls *LoopbackServer) CallbackURL() string { return ls.baseURL + CallbackPath }

// Close stops the listener.
func (ls *LoopbackServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return ls.srv.Shutdown(ctx)
}

// IsLoopbackRequest reports whether a request came straight from this machine
// (loopback peer and no proxy forwarding headers).
func IsLoopbackRequest(remoteAddr string, header http.Header) bool {
	for _, h := range []string{"X-Forwarded-For", "Forwarded", "X-Real-Ip", "X-Forwarded-Host"} {
		if header.Get(h) != "" {
			return false
		}
	}
	return IsLoopbackAddr(remoteAddr)
}

// IsLoopbackAddr reports whether "ip:port" (or a bare ip) is a loopback address.
func IsLoopbackAddr(remoteAddr string) bool {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

type proxyRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// placeholderTool is listed when n0 is enabled but its real tool list can't
// be fetched (e.g. the connection needs to be reconnected), so the agent can
// discover the reason instead of silently missing the tools.
var placeholderTool = json.RawMessage(`{"name":"n0_connection_info","description":"Show which n0 workspace and app this session is connected to. Call this first; if the n0 connection needs attention it explains what the user must do.","inputSchema":{"type":"object","properties":{}}}`)

// ToolErrorResult builds an MCP CallToolResult with isError set.
func ToolErrorResult(msg string) json.RawMessage {
	b, _ := json.Marshal(map[string]interface{}{
		"content": []map[string]string{{"type": "text", "text": msg}},
		"isError": true,
	})
	return b
}

// ServeProxy handles POST /internal/n0/mcp from a session's eggify-tools
// process. It accepts only loopback callers holding that session's proxy
// capability, supports tools/list and tools/call only, and uses only the
// connection bound to that session. It forwards tool calls — no route here
// ever returns a token.
func (m *Manager) ServeProxy(w http.ResponseWriter, r *http.Request) {
	if !IsLoopbackRequest(r.RemoteAddr, r.Header) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "the n0 proxy only accepts loopback callers"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	sessionID := r.Header.Get(HeaderSessionID)
	auth := r.Header.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	if !strings.HasPrefix(auth, "Bearer ") || !VerifyProxyToken(sessionID, token) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid session proxy credentials"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxProxyBody+1))
	if err != nil || len(body) > maxProxyBody {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request too large"})
		return
	}
	var req proxyRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"jsonrpc": "2.0", "id": nil,
			"error": map[string]interface{}{"code": -32700, "message": "parse error"}})
		return
	}
	id := req.ID
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	reply := func(result json.RawMessage) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": result})
	}
	ctx := r.Context()

	switch req.Method {
	case "tools/list":
		tools, _, err := m.ListTools(ctx, sessionID)
		if err != nil {
			if errors.Is(err, ErrNotEnabled) {
				tools = []json.RawMessage{}
			} else {
				tools = []json.RawMessage{placeholderTool}
			}
		}
		b, _ := json.Marshal(map[string]interface{}{"tools": tools})
		reply(b)
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			writeJSON(w, http.StatusOK, map[string]interface{}{"jsonrpc": "2.0", "id": id,
				"error": map[string]interface{}{"code": -32602, "message": "invalid params"}})
			return
		}
		// High-impact tools (spending, sending, signing, deploying, writing
		// externally) need the user's approval, whatever the session's
		// permission mode. This is enforced here, not in eggify-tools, because
		// an agent can call this proxy directly.
		if d := toolguard.Classify(p.Name); d.NeedsServer() {
			guard := m.toolGuard()
			if guard == nil {
				if d.Confirm {
					reply(ToolErrorResult(p.Name + " needs the user's approval, but this Eggify server cannot ask for it right now."))
					return
				}
			} else if ok, msg := guard(ctx, sessionID, p.Name, p.Arguments); !ok {
				reply(ToolErrorResult(msg))
				return
			}
		}
		res, err := m.CallTool(ctx, sessionID, p.Name, p.Arguments)
		if err != nil {
			reply(ToolErrorResult(proxyErrorText(err)))
			return
		}
		reply(res)
	default:
		writeJSON(w, http.StatusOK, map[string]interface{}{"jsonrpc": "2.0", "id": id,
			"error": map[string]interface{}{"code": -32601, "message": "method not allowed on the n0 proxy"}})
	}
}

func proxyErrorText(err error) string {
	switch {
	case errors.Is(err, ErrNeedsReauth):
		return ReauthMessage
	case errors.Is(err, ErrNotEnabled), errors.Is(err, ErrNoConnection):
		return err.Error()
	}
	return "n0 request failed: " + err.Error()
}

// ServeCallback handles the OAuth redirect from n0 and renders a small page
// that closes itself or sends the browser back to Eggify's Connectors page.
func (m *Manager) ServeCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	st := m.CompleteAuthorization(r.Context(), q.Get("state"), q.Get("code"), q.Get("error"), q.Get("error_description"))
	RenderCallbackPage(w, st)
}

var callbackTmpl = template.Must(template.New("cb").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{if .OK}}n0 connected{{else}}n0 connection failed{{end}} · Eggify</title>
<style>
body{font-family:system-ui,-apple-system,sans-serif;background:#0f172a;color:#e2e8f0;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0}
.card{background:#1e293b;border-radius:12px;padding:32px 36px;max-width:460px;text-align:center;box-shadow:0 10px 30px rgba(0,0,0,.35)}
h1{font-size:20px;margin:0 0 12px}.ok{color:#34d399}.err{color:#f87171}p{line-height:1.5;color:#cbd5e1}
a{color:#93c5fd}code{background:#0f172a;padding:2px 6px;border-radius:4px;word-break:break-all}
</style></head><body><div class="card">
{{if .OK}}<h1 class="ok">n0 connected</h1><p>Eggify can now use this n0 app.{{if .Desktop}} Switching back to the Eggify app… you can close this tab.{{else}} You can close this tab.{{end}}</p>
{{else}}<h1 class="err">n0 connection failed</h1><p>{{.Message}}</p>{{if .Hint}}<p>{{.Hint}}</p>{{end}}{{end}}
{{if .Back}}<p><a id="back" href="{{.Back}}">{{if .Desktop}}Open Eggify{{else}}Back to Eggify → Connectors{{end}}</a></p>{{end}}
</div>
<script>
(function(){
  var back = {{.BackJS}};
  try { if (window.opener && !window.opener.closed) { window.close(); } } catch (e) {}
  var desktop = {{.Desktop}};
  if (back) { setTimeout(function(){ if (!window.closed) { window.location.replace(back); } }, desktop ? 300 : 1500); }
})();
</script></body></html>`))

// RenderCallbackPage writes the OAuth result page.
func RenderCallbackPage(w http.ResponseWriter, st FlowStatus) {
	data := struct {
		OK            bool
		Message, Hint string
		// Back is either same-origin http(s) or exactly eggify://connectors
		// (checked when the flow started), so it is trusted as a URL here;
		// html/template would otherwise blank the eggify: scheme.
		Back    template.URL
		BackJS  string
		Desktop bool // Back is the desktop app's eggify:// deep link
	}{OK: st.Status == "connected", Hint: st.Hint}
	data.Message = st.ErrorDescription
	if data.Message == "" {
		data.Message = st.Error
	}
	if st.ReturnTo != "" {
		if u, err := url.Parse(st.ReturnTo); err == nil && (u.Scheme == "https" || u.Scheme == "http" || u.Scheme == "eggify") {
			q := u.Query()
			if data.OK {
				q.Set("n0_connected", fmt.Sprint(st.ConnectionID))
			} else {
				q.Set("n0_error", st.Error)
				if st.ErrorDescription != "" {
					q.Set("n0_error_description", st.ErrorDescription)
				}
			}
			u.RawQuery = q.Encode()
			data.Back = template.URL(u.String())
		}
	}
	data.BackJS = string(data.Back)
	data.Desktop = strings.HasPrefix(data.BackJS, "eggify://")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	if !data.OK {
		w.WriteHeader(http.StatusBadRequest)
	}
	_ = callbackTmpl.Execute(w, data)
}

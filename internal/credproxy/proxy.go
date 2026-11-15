package credproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/n0"
)

// AuthStyle is how the upstream provider expects its API key.
type AuthStyle int

const (
	// AuthXAPIKey sends the key as "x-api-key" (Anthropic and most
	// Anthropic-compatible endpoints; what Eggify always sent before the proxy).
	AuthXAPIKey AuthStyle = iota
	// AuthBearer sends "Authorization: Bearer <key>" (OpenAI/Codex,
	// OpenRouter, ANTHROPIC_AUTH_TOKEN-style gateways).
	AuthBearer
)

// Upstream is where a session's requests go and with which key.
type Upstream struct {
	BaseURL string
	APIKey  string
	Auth    AuthStyle
}

// ErrNoUpstream is returned by a Resolver when the session is unknown, ended,
// not on this API kind, or has no key to proxy.
var ErrNoUpstream = errors.New("no provider credentials for this session")

// Resolver returns the CURRENT upstream for a session. It is called on every
// request, so a mid-session provider/model switch takes effect immediately.
type Resolver func(ctx context.Context, sessionID string, kind Kind) (Upstream, error)

// Route prefixes. The Claude CLI appends /v1/messages (etc.) to
// ANTHROPIC_BASE_URL; Codex appends /responses (etc.) to the provider base_url.
const (
	anthropicPrefix = "/anthropic"
	openAIPrefix    = "/openai/v1"
)

// maxBody caps a single request body (base64 images/PDFs can be large).
const maxBody = 256 << 20

// Server is the loopback-only reverse proxy.
type Server struct {
	resolve   Resolver
	transport http.RoundTripper
	ln        net.Listener
	srv       *http.Server
	base      string
}

// NewHandler builds the proxy without a listener (used by Start and tests).
func NewHandler(resolve Resolver, transport http.RoundTripper) *Server {
	if transport == nil {
		transport = defaultTransport()
	}
	return &Server{resolve: resolve, transport: transport}
}

func defaultTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 15 * time.Second,
		// No ResponseHeaderTimeout/overall timeout: long generations (and
		// non-streaming requests with big outputs) can take many minutes. The
		// request is cancelled when the agent disconnects.
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// Start listens on 127.0.0.1 (port 0 = any free port) and serves the proxy.
func Start(resolve Resolver, port int) (*Server, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("provider proxy listener: %w", err)
	}
	s := NewHandler(resolve, nil)
	s.ln = ln
	s.base = fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
	s.srv = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       5 * time.Minute,
	}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// SetBase sets the base URL (tests that serve the handler themselves).
func (s *Server) SetBase(base string) { s.base = strings.TrimRight(base, "/") }

// BaseURL is the URL a runtime should use as its API base for kind:
// ANTHROPIC_BASE_URL for the Claude CLI, the provider base_url for Codex.
func (s *Server) BaseURL(kind Kind) string {
	if s == nil || s.base == "" {
		return ""
	}
	switch kind {
	case KindOpenAI:
		return s.base + openAIPrefix
	default:
		return s.base + anthropicPrefix
	}
}

// Close stops the listener.
func (s *Server) Close() error {
	if s == nil || s.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

// Headers that carry credentials or forwarding info and are never forwarded
// as received.
var strippedRequestHeaders = []string{
	"X-Api-Key", "Authorization", "Proxy-Authorization", "Cookie",
	"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded", "X-Real-Ip",
}

func writeError(w http.ResponseWriter, kind Kind, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	var body interface{}
	if kind == KindOpenAI {
		body = map[string]interface{}{"error": map[string]string{"type": errType, "message": msg}}
	} else {
		body = map[string]interface{}{"type": "error", "error": map[string]string{"type": errType, "message": msg}}
	}
	_ = json.NewEncoder(w).Encode(body)
}

// route splits a request path into the API kind and the path to append to the
// upstream base URL. Only the provider API paths are allowed.
func route(escapedPath string) (Kind, string, bool) {
	lower := strings.ToLower(escapedPath)
	// Reject encoded separators/dots and dot segments outright: nothing in the
	// provider APIs needs them and they are the usual way out of a prefix.
	if strings.Contains(lower, "%2f") || strings.Contains(lower, "%2e") || strings.Contains(lower, "%5c") ||
		strings.Contains(escapedPath, "\\") || strings.Contains(escapedPath, "//") {
		return "", "", false
	}
	for _, seg := range strings.Split(escapedPath, "/") {
		if seg == "." || seg == ".." {
			return "", "", false
		}
	}
	switch {
	case strings.HasPrefix(escapedPath, anthropicPrefix+"/v1/"):
		return KindAnthropic, strings.TrimPrefix(escapedPath, anthropicPrefix), true
	case strings.HasPrefix(escapedPath, openAIPrefix+"/"):
		rest := strings.TrimPrefix(escapedPath, openAIPrefix)
		if rest == "/" {
			return "", "", false
		}
		return KindOpenAI, rest, true
	}
	return "", "", false
}

func credential(r *http.Request) string {
	if v := r.Header.Get("X-Api-Key"); v != "" {
		return v
	}
	if auth := r.Header.Get("Authorization"); len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

// ServeHTTP proxies one request.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !n0.IsLoopbackRequest(r.RemoteAddr, r.Header) {
		writeError(w, KindAnthropic, http.StatusForbidden, "permission_error", "the Eggify provider proxy only accepts loopback callers")
		return
	}
	kind, rest, ok := route(r.URL.EscapedPath())
	if !ok {
		writeError(w, KindAnthropic, http.StatusNotFound, "not_found_error", "path not allowed on the Eggify provider proxy")
		return
	}
	sessionID, ok := VerifyToken(credential(r), kind)
	if !ok {
		writeError(w, kind, http.StatusUnauthorized, "authentication_error", "invalid Eggify session capability")
		return
	}
	up, err := s.resolve(r.Context(), sessionID, kind)
	if err != nil || up.APIKey == "" || up.BaseURL == "" {
		writeError(w, kind, http.StatusUnauthorized, "authentication_error",
			"Eggify has no API key for this session's provider - configure it in Settings > Providers")
		return
	}
	target, err := url.Parse(strings.TrimRight(up.BaseURL, "/"))
	if err != nil || (target.Scheme != "https" && target.Scheme != "http") || target.Host == "" {
		writeError(w, kind, http.StatusBadGateway, "api_error", "the session's provider base URL is invalid")
		return
	}
	if r.ContentLength > maxBody {
		writeError(w, kind, http.StatusRequestEntityTooLarge, "request_too_large", "request too large")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			out := pr.Out
			out.URL.Scheme = target.Scheme
			out.URL.Host = target.Host
			out.URL.User = nil
			out.URL.RawPath = target.EscapedPath() + rest
			if p, err := url.PathUnescape(out.URL.RawPath); err == nil {
				out.URL.Path = p
			} else {
				out.URL.Path = target.Path + rest
				out.URL.RawPath = ""
			}
			out.URL.RawQuery = pr.In.URL.RawQuery
			out.Host = target.Host
			for _, h := range strippedRequestHeaders {
				out.Header.Del(h)
			}
			switch up.Auth {
			case AuthBearer:
				out.Header.Set("Authorization", "Bearer "+up.APIKey)
			default:
				out.Header.Set("X-Api-Key", up.APIKey)
			}
		},
		Transport: s.transport,
		// Flush every write: SSE streams must reach the agent as they arrive.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, context.Canceled) {
				return
			}
			// err names the upstream host at most, never the key.
			logging.Warning("provider proxy: upstream %s request failed: %v", target.Host, err)
			writeError(w, kind, http.StatusBadGateway, "api_error", "the Eggify provider proxy could not reach the provider")
		},
	}
	rp.ServeHTTP(w, r)
}

package remoteaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultNgrokAPIURL is the ngrok REST API.
const DefaultNgrokAPIURL = "https://api.ngrok.com"

// NgrokAPI is a minimal client for the ngrok REST API's tunnel sessions
// (https://ngrok.com/docs/api/resources/tunnel-sessions/): one tunnel
// session is one connected ngrok agent, and free accounts may only have a
// few at a time (ERR_NGROK_108).
type NgrokAPI struct {
	BaseURL string       // default DefaultNgrokAPIURL
	HTTP    *http.Client // default: 15 s timeout
}

// TunnelSession is a connected ngrok agent (ngrok API "TunnelSession").
type TunnelSession struct {
	ID           string `json:"id"`
	AgentVersion string `json:"agent_version,omitempty"`
	IP           string `json:"ip,omitempty"`
	Metadata     string `json:"metadata,omitempty"`
	OS           string `json:"os,omitempty"`
	Region       string `json:"region,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
	Transport    string `json:"transport,omitempty"`
}

type tunnelSessionList struct {
	TunnelSessions []TunnelSession `json:"tunnel_sessions"`
	NextPageURI    *string         `json:"next_page_uri"`
}

// APIError is an error answer from the ngrok API, with a message fit for the
// UI. Code is a short machine-readable reason.
type APIError struct {
	Status int
	Code   string // invalid_key | forbidden | rate_limited | not_found | ngrok_error
	Msg    string
}

func (e *APIError) Error() string { return e.Msg }

// API error codes.
const (
	APIErrInvalidKey  = "invalid_key"
	APIErrForbidden   = "forbidden"
	APIErrRateLimited = "rate_limited"
	APIErrNotFound    = "not_found"
	APIErrNgrok       = "ngrok_error"
)

// maxSessionPages bounds pagination (100 sessions per page).
const maxSessionPages = 20

var tunnelSessionIDRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,128}$`)

// ValidTunnelSessionID reports whether id looks like an ngrok resource id
// (e.g. ts_2abc…), so it can go into a URL path.
func ValidTunnelSessionID(id string) bool { return tunnelSessionIDRe.MatchString(id) }

func (a *NgrokAPI) base() (*url.URL, error) {
	raw := DefaultNgrokAPIURL
	if a != nil && a.BaseURL != "" {
		raw = a.BaseURL
	}
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("invalid ngrok API URL %q", raw)
	}
	return u, nil
}

func (a *NgrokAPI) client() *http.Client {
	if a != nil && a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{
		Timeout: 15 * time.Second,
		// Never follow a redirect with the API key.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// ListTunnelSessions returns all online agent sessions of the account.
func (a *NgrokAPI) ListTunnelSessions(ctx context.Context, apiKey string) ([]TunnelSession, error) {
	base, err := a.base()
	if err != nil {
		return nil, err
	}
	next := *base
	next.Path = base.Path + "/tunnel_sessions"
	next.RawQuery = url.Values{"limit": {"100"}}.Encode()
	var out []TunnelSession
	seen := map[string]bool{}
	for page := 0; page < maxSessionPages; page++ {
		var list tunnelSessionList
		if err := a.do(ctx, apiKey, http.MethodGet, next.String(), nil, &list); err != nil {
			return nil, err
		}
		for _, s := range list.TunnelSessions {
			if s.ID == "" || seen[s.ID] {
				continue
			}
			seen[s.ID] = true
			out = append(out, s)
		}
		if list.NextPageURI == nil || *list.NextPageURI == "" || len(list.TunnelSessions) == 0 {
			return out, nil
		}
		// next_page_uri is absolute; only its path and query are used, on our
		// own base URL, so the API key never goes to another host.
		nu, err := url.Parse(*list.NextPageURI)
		if err != nil {
			return out, nil
		}
		next = *base
		next.Path = nu.Path
		next.RawQuery = nu.RawQuery
		if !strings.HasSuffix(next.Path, "/tunnel_sessions") {
			return out, nil
		}
	}
	return out, nil
}

// StopTunnelSession asks the agent behind session id to stop.
func (a *NgrokAPI) StopTunnelSession(ctx context.Context, apiKey, id string) error {
	if !ValidTunnelSessionID(id) {
		return &APIError{Status: http.StatusBadRequest, Code: APIErrNotFound, Msg: "invalid agent session id"}
	}
	base, err := a.base()
	if err != nil {
		return err
	}
	u := *base
	u.Path = base.Path + "/tunnel_sessions/" + id + "/stop"
	return a.do(ctx, apiKey, http.MethodPost, u.String(), strings.NewReader("{}"), nil)
}

func (a *NgrokAPI) do(ctx context.Context, apiKey, method, rawURL string, body io.Reader, out any) error {
	if apiKey == "" {
		return &APIError{Status: http.StatusUnauthorized, Code: APIErrInvalidKey, Msg: "add an ngrok API key first"}
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Ngrok-Version", "2")
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client().Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errors.New("the ngrok API did not answer in time; try again")
		}
		return fmt.Errorf("could not reach the ngrok API: %s", firstLine(stripKey(err.Error(), apiKey)))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil || len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("unexpected answer from the ngrok API")
		}
		return nil
	}
	return apiError(resp.StatusCode, data)
}

// apiError maps an ngrok API error answer to a clear message.
func apiError(status int, body []byte) *APIError {
	var e struct {
		ErrorCode string `json:"error_code"`
		Msg       string `json:"msg"`
	}
	_ = json.Unmarshal(body, &e)
	msg := strings.TrimSpace(firstLine(e.Msg))
	if len(msg) > 300 {
		msg = msg[:300]
	}
	suffix := ""
	if e.ErrorCode != "" {
		suffix = " (" + e.ErrorCode + ")"
	}
	switch status {
	case http.StatusUnauthorized:
		return &APIError{Status: status, Code: APIErrInvalidKey,
			Msg: "ngrok rejected the API key" + suffix + ". Create a new one at dashboard.ngrok.com/api-keys (an API key, not the authtoken)"}
	case http.StatusForbidden:
		return &APIError{Status: status, Code: APIErrForbidden,
			Msg: "this ngrok API key may not manage agent sessions" + suffix + ". Use a key created by an account admin"}
	case http.StatusTooManyRequests:
		return &APIError{Status: status, Code: APIErrRateLimited,
			Msg: "the ngrok API is rate limiting requests; wait a minute and try again"}
	case http.StatusNotFound:
		return &APIError{Status: status, Code: APIErrNotFound,
			Msg: "that agent session is gone (it may have stopped already)"}
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return &APIError{Status: status, Code: APIErrNgrok, Msg: "ngrok API error: " + msg + suffix}
}

func stripKey(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "…")
}

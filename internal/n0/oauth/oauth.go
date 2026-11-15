// Package oauth is a small OAuth 2.1 client for MCP authorization as n0
// implements it: protected resource metadata (RFC 9728) → authorization
// server metadata (RFC 8414) → dynamic client registration (RFC 7591) →
// authorization code + PKCE S256 with a resource indicator (RFC 8707) →
// refresh with rotation → revocation (RFC 7009).
//
// Nothing in this package logs, and no error it returns contains a token.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxBody = 1 << 20

// Error is an RFC 6749 §5.2 style error from the authorization server (or a
// local validation failure dressed the same way).
type Error struct {
	Status      int
	Code        string
	Description string
}

func (e *Error) Error() string {
	msg := e.Code
	if e.Description != "" {
		msg += ": " + e.Description
	}
	if e.Status != 0 {
		msg = fmt.Sprintf("%s (HTTP %d)", msg, e.Status)
	}
	return msg
}

// ErrorCode returns the OAuth error code of err, or "".
func ErrorCode(err error) string {
	var oe *Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

// IsReauthError reports whether err means the grant/client is gone and the
// user has to authorize again (retrying cannot help).
func IsReauthError(err error) bool {
	switch ErrorCode(err) {
	case "invalid_grant", "invalid_client", "unauthorized_client":
		return true
	}
	return false
}

// ProtectedResourceMetadata is RFC 9728 metadata.
type ProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`
}

// ServerMetadata is RFC 8414 authorization server metadata.
type ServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	RevocationEndpoint                string   `json:"revocation_endpoint,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
}

// Discovery is everything needed to authorize against an MCP endpoint.
type Discovery struct {
	MCPURL   string
	Resource ProtectedResourceMetadata
	Server   ServerMetadata
}

// Scopes returns the scopes to request: those the resource advertises,
// falling back to the authorization server's list.
func (d *Discovery) Scopes() []string {
	if len(d.Resource.ScopesSupported) > 0 {
		return d.Resource.ScopesSupported
	}
	return d.Server.ScopesSupported
}

// isLoopbackHost reports whether a URL host is 127.0.0.1 / ::1 / localhost.
func isLoopbackHost(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

// CheckEndpoint requires https, except for loopback (used by local n0 dev
// servers and tests).
func CheckEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid URL %q", raw)
	}
	if u.Scheme == "https" || (u.Scheme == "http" && isLoopbackHost(u.Host)) {
		return nil
	}
	return fmt.Errorf("refusing non-https endpoint %q", raw)
}

func getJSON(ctx context.Context, hc *http.Client, rawURL string, out interface{}) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("GET %s: HTTP %d", rawURL, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("GET %s: invalid JSON: %w", rawURL, err)
	}
	return resp.StatusCode, nil
}

// ParseResourceMetadataURL extracts resource_metadata from a
// "WWW-Authenticate: Bearer ..." header value.
func ParseResourceMetadataURL(header string) string {
	return ParseAuthParam(header, "resource_metadata")
}

// ParseAuthParam returns one auth-param from a WWW-Authenticate value.
func ParseAuthParam(header, name string) string {
	h := header
	if i := strings.IndexByte(h, ' '); i >= 0 && !strings.Contains(h[:i], "=") {
		h = h[i+1:] // drop the scheme
	}
	for len(h) > 0 {
		h = strings.TrimLeft(h, " ,")
		eq := strings.IndexByte(h, '=')
		if eq < 0 {
			return ""
		}
		key := strings.TrimSpace(h[:eq])
		h = h[eq+1:]
		var val string
		if strings.HasPrefix(h, `"`) {
			end := 1
			for end < len(h) && h[end] != '"' {
				if h[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(h) {
				val, h = strings.ReplaceAll(h[1:], `\"`, `"`), ""
			} else {
				val, h = strings.ReplaceAll(h[1:end], `\"`, `"`), h[end+1:]
			}
		} else {
			end := strings.IndexByte(h, ',')
			if end < 0 {
				end = len(h)
			}
			val, h = strings.TrimSpace(h[:end]), h[end:]
		}
		if strings.EqualFold(key, name) {
			return val
		}
	}
	return ""
}

// wellKnownURL inserts a well-known suffix between origin and path
// (RFC 8414 §3.1 / RFC 9728 §3.1).
func wellKnownURL(base, suffix string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	path := strings.TrimSuffix(u.EscapedPath(), "/")
	return u.Scheme + "://" + u.Host + "/.well-known/" + suffix + path, nil
}

// normalizeResource compares resource URLs the way n0 does: scheme and host
// case-insensitively, path without a trailing slash, query ignored.
func normalizeResource(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + strings.TrimSuffix(u.Path, "/")
}

// Discover resolves the metadata chain for an MCP endpoint. It probes the
// endpoint without a token to read the resource_metadata hint from the 401,
// and falls back to the RFC 9728 well-known location. Endpoints are never
// hard-coded.
func Discover(ctx context.Context, hc *http.Client, mcpURL string) (*Discovery, error) {
	if err := CheckEndpoint(mcpURL); err != nil {
		return nil, err
	}
	prmURL := probeResourceMetadata(ctx, hc, mcpURL)
	if prmURL == "" {
		var err error
		if prmURL, err = wellKnownURL(mcpURL, "oauth-protected-resource"); err != nil {
			return nil, err
		}
	}
	if err := CheckEndpoint(prmURL); err != nil {
		return nil, err
	}
	d := &Discovery{MCPURL: mcpURL}
	if _, err := getJSON(ctx, hc, prmURL, &d.Resource); err != nil {
		return nil, fmt.Errorf("protected resource metadata: %w", err)
	}
	if normalizeResource(d.Resource.Resource) != normalizeResource(mcpURL) {
		return nil, fmt.Errorf("protected resource metadata is for %q, not %q", d.Resource.Resource, mcpURL)
	}
	if len(d.Resource.AuthorizationServers) == 0 {
		return nil, errors.New("protected resource metadata lists no authorization server")
	}
	issuer := d.Resource.AuthorizationServers[0]
	if err := CheckEndpoint(issuer); err != nil {
		return nil, err
	}
	if err := fetchServerMetadata(ctx, hc, issuer, &d.Server); err != nil {
		return nil, err
	}
	if strings.TrimSuffix(d.Server.Issuer, "/") != strings.TrimSuffix(issuer, "/") {
		return nil, fmt.Errorf("authorization server metadata issuer %q does not match %q", d.Server.Issuer, issuer)
	}
	s256 := false
	for _, m := range d.Server.CodeChallengeMethodsSupported {
		if m == "S256" {
			s256 = true
		}
	}
	if !s256 {
		return nil, errors.New("authorization server does not support PKCE S256")
	}
	for _, ep := range []string{d.Server.AuthorizationEndpoint, d.Server.TokenEndpoint} {
		if err := CheckEndpoint(ep); err != nil {
			return nil, err
		}
	}
	for _, ep := range []string{d.Server.RegistrationEndpoint, d.Server.RevocationEndpoint} {
		if ep != "" {
			if err := CheckEndpoint(ep); err != nil {
				return nil, err
			}
		}
	}
	return d, nil
}

func probeResourceMetadata(ctx context.Context, hc *http.Client, mcpURL string) string {
	body := `{"jsonrpc":"2.0","id":0,"method":"ping"}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, strings.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := hc.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode != http.StatusUnauthorized {
		return ""
	}
	return ParseResourceMetadataURL(resp.Header.Get("WWW-Authenticate"))
}

func fetchServerMetadata(ctx context.Context, hc *http.Client, issuer string, out *ServerMetadata) error {
	var candidates []string
	if u, err := wellKnownURL(issuer, "oauth-authorization-server"); err == nil {
		candidates = append(candidates, u)
	}
	candidates = append(candidates, strings.TrimSuffix(issuer, "/")+"/.well-known/oauth-authorization-server")
	if u, err := wellKnownURL(issuer, "openid-configuration"); err == nil {
		candidates = append(candidates, u)
	}
	var lastErr error
	seen := map[string]bool{}
	for _, c := range candidates {
		if seen[c] {
			continue
		}
		seen[c] = true
		*out = ServerMetadata{}
		if _, err := getJSON(ctx, hc, c, out); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("authorization server metadata: %w", lastErr)
}

// RegistrationRequest is an RFC 7591 client metadata document.
type RegistrationRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	Scope                   string   `json:"scope,omitempty"`
}

// RegistrationResponse is the subset of the RFC 7591 response Eggify needs.
type RegistrationResponse struct {
	ClientID     string   `json:"client_id"`
	RedirectURIs []string `json:"redirect_uris,omitempty"`
}

// NewPublicClientRegistration builds the registration Eggify sends: a public
// client (no secret) using authorization_code + refresh_token.
func NewPublicClientRegistration(clientName, redirectURI, scope string) RegistrationRequest {
	return RegistrationRequest{
		ClientName:              clientName,
		RedirectURIs:            []string{redirectURI},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
		Scope:                   scope,
	}
}

// Register performs dynamic client registration.
func Register(ctx context.Context, hc *http.Client, endpoint string, reg RegistrationRequest) (*RegistrationResponse, error) {
	if endpoint == "" {
		return nil, &Error{Code: "registration_not_supported", Description: "the n0 authorization server does not offer dynamic client registration"}
	}
	payload, _ := json.Marshal(reg)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("client registration: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, parseError(resp.StatusCode, body)
	}
	var out RegistrationResponse
	if err := json.Unmarshal(body, &out); err != nil || out.ClientID == "" {
		return nil, &Error{Status: resp.StatusCode, Code: "invalid_client_metadata", Description: "registration response has no client_id"}
	}
	return &out, nil
}

func parseError(status int, body []byte) error {
	var e struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return &Error{Status: status, Code: e.Error, Description: e.ErrorDescription}
	}
	code := "server_error"
	if status == http.StatusUnauthorized {
		code = "invalid_client"
	} else if status == http.StatusTooManyRequests {
		code = "rate_limited"
	}
	return &Error{Status: status, Code: code, Description: http.StatusText(status)}
}

// PKCE is an RFC 7636 verifier/challenge pair (S256).
type PKCE struct {
	Verifier  string
	Challenge string
}

// NewPKCE creates a fresh verifier (43 chars) and its S256 challenge.
func NewPKCE() (PKCE, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return PKCE{}, err
	}
	v := base64.RawURLEncoding.EncodeToString(b)
	return PKCE{Verifier: v, Challenge: S256Challenge(v)}, nil
}

// S256Challenge computes BASE64URL(SHA256(verifier)).
func S256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// NewState returns an unguessable state value.
func NewState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// AuthorizeParams are the authorization request parameters.
type AuthorizeParams struct {
	ClientID      string
	RedirectURI   string
	State         string
	CodeChallenge string
	Resource      string
	Scopes        []string
}

// AuthorizeURL builds the authorization request URL.
func AuthorizeURL(endpoint string, p AuthorizeParams) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("state", p.State)
	q.Set("code_challenge", p.CodeChallenge)
	q.Set("code_challenge_method", "S256")
	if p.Resource != "" {
		q.Set("resource", p.Resource)
	}
	if len(p.Scopes) > 0 {
		q.Set("scope", strings.Join(p.Scopes, " "))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Token is a token endpoint response.
type Token struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// ExpiresAt converts expires_in to an absolute time (default 1h).
func (t *Token) ExpiresAt(now time.Time) time.Time {
	secs := t.ExpiresIn
	if secs <= 0 {
		secs = 3600
	}
	return now.Add(time.Duration(secs) * time.Second)
}

func postForm(ctx context.Context, hc *http.Client, endpoint string, form url.Values) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		// Never wrap the url.Error: it is safe (no secrets in the URL) but keep
		// the message short.
		return 0, nil, fmt.Errorf("token endpoint request failed: %v", unwrapURLError(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return resp.StatusCode, body, nil
}

func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func tokenRequest(ctx context.Context, hc *http.Client, endpoint string, form url.Values) (*Token, error) {
	status, body, err := postForm(ctx, hc, endpoint, form)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, parseError(status, body)
	}
	var t Token
	if err := json.Unmarshal(body, &t); err != nil || t.AccessToken == "" {
		return nil, &Error{Status: status, Code: "invalid_token_response", Description: "token response has no access_token"}
	}
	return &t, nil
}

// ExchangeCode redeems an authorization code.
func ExchangeCode(ctx context.Context, hc *http.Client, tokenEndpoint, clientID, code, redirectURI, verifier, resource string) (*Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	if resource != "" {
		form.Set("resource", resource)
	}
	return tokenRequest(ctx, hc, tokenEndpoint, form)
}

// Refresh redeems a refresh token. n0 rotates refresh tokens: the returned
// token replaces the old one, and sending the old one again revokes the
// whole connection, so callers must persist the result before anything else.
func Refresh(ctx context.Context, hc *http.Client, tokenEndpoint, clientID, refreshToken, resource string) (*Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}
	if resource != "" {
		form.Set("resource", resource)
	}
	return tokenRequest(ctx, hc, tokenEndpoint, form)
}

// Revoke revokes a token (RFC 7009). A 200 means revoked or already invalid.
func Revoke(ctx context.Context, hc *http.Client, endpoint, clientID, token, hint string) error {
	if endpoint == "" {
		return errors.New("no revocation endpoint")
	}
	form := url.Values{"token": {token}, "client_id": {clientID}}
	if hint != "" {
		form.Set("token_type_hint", hint)
	}
	status, body, err := postForm(ctx, hc, endpoint, form)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return parseError(status, body)
	}
	return nil
}

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schlunsen/eggify/internal/remoteaccess"
)

type fakeRemoteAccess struct {
	status        remoteaccess.Status
	view          remoteaccess.SettingsView
	guardErr      error
	startErr      error
	updErr        error
	lastUpd       remoteaccess.Update
	started       int
	stopped       int
	deleted       int
	apiKey        string
	agents        []remoteaccess.AgentSession
	agentErr      error
	stopErr       error
	stoppedAgents []string
}

func (f *fakeRemoteAccess) Status() remoteaccess.Status     { return f.status }
func (f *fakeRemoteAccess) View() remoteaccess.SettingsView { return f.view }
func (f *fakeRemoteAccess) CheckGuard() error               { return f.guardErr }
func (f *fakeRemoteAccess) Stop() error                     { f.stopped++; return nil }
func (f *fakeRemoteAccess) DeleteToken() error              { f.deleted++; f.view.TokenSet = false; return nil }
func (f *fakeRemoteAccess) Start() error                    { f.started++; return f.startErr }
func (f *fakeRemoteAccess) UpdateSettings(u remoteaccess.Update) (bool, error) {
	f.lastUpd = u
	return false, f.updErr
}

func (f *fakeRemoteAccess) SetAPIKey(k string) error {
	n, err := remoteaccess.NormalizeAPIKey(k)
	if err != nil {
		return err
	}
	f.apiKey = n
	f.view.APIKeySet, f.view.APIKeyHint = true, remoteaccess.TokenHint(n)
	return nil
}
func (f *fakeRemoteAccess) DeleteAPIKey() error {
	f.apiKey = ""
	f.view.APIKeySet, f.view.APIKeyHint = false, ""
	return nil
}
func (f *fakeRemoteAccess) AgentSessions(context.Context) ([]remoteaccess.AgentSession, error) {
	return f.agents, f.agentErr
}
func (f *fakeRemoteAccess) StopAgentSession(_ context.Context, id string) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	f.stoppedAgents = append(f.stoppedAgents, id)
	return nil
}

func tunnelApp(h *TunnelHandler) *fiber.App {
	app := fiber.New()
	app.Get("/api/tunnel/status", h.HandleGetStatus)
	app.Put("/api/tunnel/settings", h.HandleUpdateSettings)
	app.Post("/api/tunnel/start", h.HandleStart)
	app.Post("/api/tunnel/stop", h.HandleStop)
	app.Delete("/api/tunnel/token", h.HandleDeleteToken)
	app.Put("/api/tunnel/api-key", h.HandleSetAPIKey)
	app.Delete("/api/tunnel/api-key", h.HandleDeleteAPIKey)
	app.Get("/api/tunnel/sessions", h.HandleListAgentSessions)
	app.Post("/api/tunnel/sessions/:id/stop", h.HandleStopAgentSession)
	return app
}

func call(t *testing.T, app *fiber.App, method, path, body string) (int, map[string]any, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out, string(b)
}

func TestTunnelHandler_Status_Disabled(t *testing.T) {
	app := tunnelApp(NewTunnelHandler(nil))
	code, body, _ := call(t, app, http.MethodGet, "/api/tunnel/status", "")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "disabled", body["status"])
	assert.Equal(t, "ngrok", body["provider"])

	code, _, _ = call(t, app, http.MethodPost, "/api/tunnel/start", "")
	assert.Equal(t, http.StatusServiceUnavailable, code)
}

func TestTunnelHandler_TypedNilManagerIsDisabled(t *testing.T) {
	var m *remoteaccess.Manager
	app := tunnelApp(NewTunnelHandler(m))
	_, body, _ := call(t, app, http.MethodGet, "/api/tunnel/status", "")
	assert.Equal(t, "disabled", body["status"])
}

func TestTunnelHandler_Status_ConnectedKeepsLegacyShape(t *testing.T) {
	f := &fakeRemoteAccess{
		status: remoteaccess.Status{Status: "connected", PublicURL: "https://x.ngrok.app", Domain: "x.ngrok.app", Provider: "ngrok", Running: true, Region: "eu"},
		view:   remoteaccess.SettingsView{TokenSet: true, TokenHint: "…abcd", TokenSource: "settings"},
	}
	_, body, raw := call(t, tunnelApp(NewTunnelHandler(f)), http.MethodGet, "/api/tunnel/status", "")
	assert.Equal(t, "connected", body["status"])
	assert.Equal(t, "https://x.ngrok.app", body["public_url"])
	assert.Equal(t, "x.ngrok.app", body["domain"])
	assert.Equal(t, "eu", body["region"])
	assert.Equal(t, true, body["can_start"])
	settings := body["settings"].(map[string]any)
	assert.Equal(t, "…abcd", settings["token_hint"])
	assert.NotContains(t, raw, "authtoken")
}

func TestTunnelHandler_StatusExplainsWhyItCantStart(t *testing.T) {
	f := &fakeRemoteAccess{status: remoteaccess.Status{Status: "disconnected"},
		guardErr: &remoteaccess.GuardError{Code: "user_auth_disabled", Msg: "needs user auth"}}
	_, body, _ := call(t, tunnelApp(NewTunnelHandler(f)), http.MethodGet, "/api/tunnel/status", "")
	assert.Equal(t, false, body["can_start"])
	assert.Equal(t, "user_auth_disabled", body["blocked"].(map[string]any)["code"])

	f.guardErr = nil
	_, body, _ = call(t, tunnelApp(NewTunnelHandler(f)), http.MethodGet, "/api/tunnel/status", "")
	assert.Equal(t, "no_token", body["blocked"].(map[string]any)["code"])
}

func TestTunnelHandler_StartErrors(t *testing.T) {
	f := &fakeRemoteAccess{startErr: &remoteaccess.GuardError{Code: "user_auth_disabled", Msg: "Remote access needs user authentication."}}
	app := tunnelApp(NewTunnelHandler(f))
	code, body, _ := call(t, app, http.MethodPost, "/api/tunnel/start", "")
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "user_auth_disabled", body["code"])

	f.startErr = remoteaccess.ErrNoToken
	code, body, _ = call(t, app, http.MethodPost, "/api/tunnel/start", "")
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "no_token", body["code"])

	f.startErr = nil
	code, _, _ = call(t, app, http.MethodPost, "/api/tunnel/start", "")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, 3, f.started)
}

func TestTunnelHandler_UpdateSettings(t *testing.T) {
	f := &fakeRemoteAccess{}
	app := tunnelApp(NewTunnelHandler(f))
	code, body, raw := call(t, app, http.MethodPut, "/api/tunnel/settings", `{"authtoken":"tok_1234567890","domain":"me.ngrok.app","autostart":true}`)
	assert.Equal(t, http.StatusOK, code, raw)
	assert.Equal(t, true, body["ok"])
	require.NotNil(t, f.lastUpd.AuthToken)
	assert.Equal(t, "tok_1234567890", *f.lastUpd.AuthToken)
	assert.Equal(t, "me.ngrok.app", *f.lastUpd.Domain)
	assert.True(t, *f.lastUpd.Autostart)
	assert.Nil(t, f.lastUpd.Protection)
	assert.NotContains(t, raw, "tok_1234567890", "the token must never be echoed back")

	f.updErr = remoteaccess.ErrInvalidDomain
	code, body, _ = call(t, app, http.MethodPut, "/api/tunnel/settings", `{"domain":"bad domain"}`)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "invalid", body["code"])

	f.updErr = errors.New("OAuth protection needs at least one allowed email or domain")
	code, _, _ = call(t, app, http.MethodPut, "/api/tunnel/settings", `{"protection":{"mode":"oauth","oauth_provider":"google"}}`)
	assert.Equal(t, http.StatusBadRequest, code)

	code, _, _ = call(t, app, http.MethodPut, "/api/tunnel/settings", `not json`)
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestTunnelHandler_StopAndDeleteToken(t *testing.T) {
	f := &fakeRemoteAccess{view: remoteaccess.SettingsView{TokenSet: true}}
	app := tunnelApp(NewTunnelHandler(f))
	code, _, _ := call(t, app, http.MethodPost, "/api/tunnel/stop", "")
	assert.Equal(t, http.StatusOK, code)
	code, body, _ := call(t, app, http.MethodDelete, "/api/tunnel/token", "")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, 1, f.stopped)
	assert.Equal(t, 1, f.deleted)
	assert.Equal(t, false, body["settings"].(map[string]any)["token_set"])
}

func TestTunnelHandler_APIKeyNeverEchoed(t *testing.T) {
	f := &fakeRemoteAccess{}
	app := tunnelApp(NewTunnelHandler(f))
	code, body, raw := call(t, app, http.MethodPut, "/api/tunnel/api-key", `{"api_key":"2abcSECRETkey_77x9"}`)
	assert.Equal(t, http.StatusOK, code, raw)
	assert.NotContains(t, raw, "SECRET")
	settings := body["settings"].(map[string]any)
	assert.Equal(t, true, settings["api_key_set"])
	assert.Equal(t, "…77x9", settings["api_key_hint"])

	code, body, _ = call(t, app, http.MethodPut, "/api/tunnel/api-key", `{"api_key":"x"}`)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "invalid", body["code"])

	code, body, _ = call(t, app, http.MethodDelete, "/api/tunnel/api-key", "")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, false, body["settings"].(map[string]any)["api_key_set"])
}

func TestTunnelHandler_AgentSessions(t *testing.T) {
	f := &fakeRemoteAccess{agents: []remoteaccess.AgentSession{
		{TunnelSession: remoteaccess.TunnelSession{ID: "ts_1", OS: "darwin", IP: "203.0.113.1"}, Current: true, ThisInstall: true, Eggify: true},
		{TunnelSession: remoteaccess.TunnelSession{ID: "ts_2", OS: "linux"}},
	}}
	app := tunnelApp(NewTunnelHandler(f))
	code, body, raw := call(t, app, http.MethodGet, "/api/tunnel/sessions", "")
	assert.Equal(t, http.StatusOK, code, raw)
	list := body["sessions"].([]any)
	require.Len(t, list, 2)
	assert.Equal(t, true, list[0].(map[string]any)["current"])

	code, _, _ = call(t, app, http.MethodPost, "/api/tunnel/sessions/ts_2/stop", "")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, []string{"ts_2"}, f.stoppedAgents)

	code, _, _ = call(t, app, http.MethodPost, "/api/tunnel/sessions/ts%2F..%2Fx/stop", "")
	assert.Equal(t, http.StatusBadRequest, code)

	f.agents = nil
	_, body, _ = call(t, app, http.MethodGet, "/api/tunnel/sessions", "")
	assert.Equal(t, []any{}, body["sessions"], "an empty list is [] not null")
}

func TestTunnelHandler_AgentSessionErrors(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{remoteaccess.ErrNoAPIKey, 400, "no_api_key"},
		{remoteaccess.ErrOwnSession, 409, "own_session"},
		{&remoteaccess.APIError{Code: remoteaccess.APIErrInvalidKey, Msg: "bad key"}, 400, "invalid_key"},
		{&remoteaccess.APIError{Code: remoteaccess.APIErrForbidden, Msg: "no"}, 400, "forbidden"},
		{&remoteaccess.APIError{Code: remoteaccess.APIErrRateLimited, Msg: "slow"}, 429, "rate_limited"},
		{&remoteaccess.APIError{Code: remoteaccess.APIErrNotFound, Msg: "gone"}, 404, "not_found"},
		{errors.New("dial tcp: no route"), 502, "ngrok_unreachable"},
	}
	for _, tc := range cases {
		f := &fakeRemoteAccess{agentErr: tc.err, stopErr: tc.err}
		app := tunnelApp(NewTunnelHandler(f))
		code, body, _ := call(t, app, http.MethodGet, "/api/tunnel/sessions", "")
		assert.Equal(t, tc.status, code, tc.code)
		assert.Equal(t, tc.code, body["code"])
		code, body, _ = call(t, app, http.MethodPost, "/api/tunnel/sessions/ts_9/stop", "")
		assert.Equal(t, tc.status, code, tc.code)
		assert.Equal(t, tc.code, body["code"])
	}
}

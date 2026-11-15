package handlers

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/gofiber/fiber/v2"

	"github.com/schlunsen/eggify/internal/remoteaccess"
)

// RemoteAccessAction names the privileged remote access actions in
// "requires a signed-in user" refusals.
const RemoteAccessAction = "Changing remote access"

// RemoteAccessController is the remote access manager as seen by the API
// (*remoteaccess.Manager).
type RemoteAccessController interface {
	Status() remoteaccess.Status
	View() remoteaccess.SettingsView
	CheckGuard() error
	UpdateSettings(remoteaccess.Update) (bool, error)
	Start() error
	Stop() error
	DeleteToken() error
	SetAPIKey(string) error
	DeleteAPIKey() error
	AgentSessions(context.Context) ([]remoteaccess.AgentSession, error)
	StopAgentSession(context.Context, string) error
}

// TunnelStatus is GET /api/tunnel/status. The top-level fields keep the old
// shape (status, public_url, domain, provider, error) for existing clients
// (navbar indicator, iOS app).
type TunnelStatus struct {
	remoteaccess.Status
	Settings *remoteaccess.SettingsView `json:"settings,omitempty"`
	// CanStart is false when the start guard refuses (see Blocked).
	CanStart bool          `json:"can_start"`
	Blocked  *BlockedState `json:"blocked,omitempty"`
}

// BlockedState explains why remote access can't be started.
type BlockedState struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// TunnelHandler serves the remote access (ngrok tunnel) API.
type TunnelHandler struct {
	ctl RemoteAccessController
}

// NewTunnelHandler creates the handler; ctl may be nil (remote access
// unavailable: status "disabled", mutations 503).
func NewTunnelHandler(ctl RemoteAccessController) *TunnelHandler {
	h := &TunnelHandler{}
	// Avoid a typed-nil interface.
	if m, ok := ctl.(*remoteaccess.Manager); ok && m == nil {
		return h
	}
	h.ctl = ctl
	return h
}

func (h *TunnelHandler) status() TunnelStatus {
	if h.ctl == nil {
		return TunnelStatus{Status: remoteaccess.Status{Status: "disabled", Provider: "ngrok"}}
	}
	view := h.ctl.View()
	out := TunnelStatus{Status: h.ctl.Status(), Settings: &view, CanStart: true}
	if err := h.ctl.CheckGuard(); err != nil {
		out.CanStart = false
		out.Blocked = blocked(err)
	} else if view.Mode == remoteaccess.ModeRelay && view.RelayAddr == "" && view.DefaultRelayAddr == "" {
		out.CanStart = false
		out.Blocked = &BlockedState{Code: "no_relay_addr", Message: remoteaccess.ErrNoRelayAddr.Error()}
	} else if !view.TokenSet && view.Mode != remoteaccess.ModeRelay {
		out.CanStart = false
		out.Blocked = &BlockedState{Code: "no_token", Message: remoteaccess.ErrNoToken.Error()}
	}
	return out
}

func blocked(err error) *BlockedState {
	var g *remoteaccess.GuardError
	if errors.As(err, &g) {
		return &BlockedState{Code: g.Code, Message: g.Msg}
	}
	return &BlockedState{Code: "blocked", Message: err.Error()}
}

// HandleGetStatus returns the tunnel status and the (secret-free) settings.
func (h *TunnelHandler) HandleGetStatus(c *fiber.Ctx) error {
	return c.JSON(h.status())
}

func (h *TunnelHandler) unavailable(c *fiber.Ctx) error {
	return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
		"error": "Remote access is not available on this server", "code": "unavailable",
	})
}

// HandleUpdateSettings: PUT /api/tunnel/settings {authtoken?, domain?,
// autostart?, protection?}. Omitted fields are unchanged; the token is never
// echoed back.
func (h *TunnelHandler) HandleUpdateSettings(c *fiber.Ctx) error {
	if h.ctl == nil {
		return h.unavailable(c)
	}
	var u remoteaccess.Update
	if err := json.Unmarshal(c.Body(), &u); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body", "code": "invalid_body"})
	}
	restarted, err := h.ctl.UpdateSettings(u)
	if err != nil {
		return h.startError(c, err, fiber.StatusBadRequest)
	}
	out := h.status()
	return c.JSON(fiber.Map{"ok": true, "restarted": restarted, "tunnel": out})
}

// HandleStart: POST /api/tunnel/start. Returns once the connection attempt
// is underway; poll the status for connected/error.
func (h *TunnelHandler) HandleStart(c *fiber.Ctx) error {
	if h.ctl == nil {
		return h.unavailable(c)
	}
	if err := h.ctl.Start(); err != nil {
		return h.startError(c, err, fiber.StatusInternalServerError)
	}
	return c.JSON(h.status())
}

// HandleStop: POST /api/tunnel/stop. Only the tunnel listener closes.
func (h *TunnelHandler) HandleStop(c *fiber.Ctx) error {
	if h.ctl == nil {
		return h.unavailable(c)
	}
	if err := h.ctl.Stop(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(h.status())
}

// HandleDeleteToken: DELETE /api/tunnel/token (stops a tunnel using it).
func (h *TunnelHandler) HandleDeleteToken(c *fiber.Ctx) error {
	if h.ctl == nil {
		return h.unavailable(c)
	}
	if err := h.ctl.DeleteToken(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(h.status())
}

func (h *TunnelHandler) startError(c *fiber.Ctx, err error, fallback int) error {
	var g *remoteaccess.GuardError
	switch {
	case errors.As(err, &g):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": g.Msg, "code": g.Code})
	case errors.Is(err, remoteaccess.ErrNoToken):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error(), "code": "no_token"})
	case errors.Is(err, remoteaccess.ErrInvalidToken), errors.Is(err, remoteaccess.ErrInvalidDomain):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error(), "code": "invalid"})
	}
	return c.Status(fallback).JSON(fiber.Map{"error": err.Error()})
}

// HandleSetAPIKey: PUT /api/tunnel/api-key {api_key}. Adds or replaces the
// optional ngrok API key (used to list and stop agent sessions). The key is
// never echoed back: the status only has api_key_set + last 4.
func (h *TunnelHandler) HandleSetAPIKey(c *fiber.Ctx) error {
	if h.ctl == nil {
		return h.unavailable(c)
	}
	var body struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body", "code": "invalid_body"})
	}
	if err := h.ctl.SetAPIKey(body.APIKey); err != nil {
		if errors.Is(err, remoteaccess.ErrInvalidAPIKey) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error(), "code": "invalid"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(h.status())
}

// HandleDeleteAPIKey: DELETE /api/tunnel/api-key.
func (h *TunnelHandler) HandleDeleteAPIKey(c *fiber.Ctx) error {
	if h.ctl == nil {
		return h.unavailable(c)
	}
	if err := h.ctl.DeleteAPIKey(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(h.status())
}

// HandleListAgentSessions: GET /api/tunnel/sessions. The account's connected
// ngrok agents (needs the API key), this Eggify's own session marked.
func (h *TunnelHandler) HandleListAgentSessions(c *fiber.Ctx) error {
	if h.ctl == nil {
		return h.unavailable(c)
	}
	sessions, err := h.ctl.AgentSessions(c.UserContext())
	if err != nil {
		return agentAPIError(c, err)
	}
	if sessions == nil {
		sessions = []remoteaccess.AgentSession{}
	}
	return c.JSON(fiber.Map{"sessions": sessions})
}

// HandleStopAgentSession: POST /api/tunnel/sessions/:id/stop.
func (h *TunnelHandler) HandleStopAgentSession(c *fiber.Ctx) error {
	if h.ctl == nil {
		return h.unavailable(c)
	}
	id := c.Params("id")
	if !remoteaccess.ValidTunnelSessionID(id) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid agent session id", "code": "invalid"})
	}
	if err := h.ctl.StopAgentSession(c.UserContext(), id); err != nil {
		return agentAPIError(c, err)
	}
	return c.JSON(fiber.Map{"ok": true, "stopped": id})
}

// agentAPIError maps agent session errors to HTTP answers with a code the
// UI can act on.
func agentAPIError(c *fiber.Ctx, err error) error {
	var ae *remoteaccess.APIError
	switch {
	case errors.Is(err, remoteaccess.ErrNoAPIKey):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error(), "code": "no_api_key"})
	case errors.Is(err, remoteaccess.ErrOwnSession):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error(), "code": "own_session"})
	case errors.As(err, &ae):
		status := fiber.StatusBadGateway
		switch ae.Code {
		case remoteaccess.APIErrInvalidKey, remoteaccess.APIErrForbidden:
			status = fiber.StatusBadRequest
		case remoteaccess.APIErrRateLimited:
			status = fiber.StatusTooManyRequests
		case remoteaccess.APIErrNotFound:
			status = fiber.StatusNotFound
		}
		return c.Status(status).JSON(fiber.Map{"error": ae.Msg, "code": ae.Code})
	}
	return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error(), "code": "ngrok_unreachable"})
}

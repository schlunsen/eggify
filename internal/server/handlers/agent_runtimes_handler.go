// Package handlers - agent_runtimes_handler.go reports whether the agent CLIs
// (Claude Code, OpenAI Codex) are installed and logged in, and runs their own
// login flows for the onboarding wizard.
package handlers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/agentruntime"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// AgentLoginAction names the privileged login actions for
// principal.RequireUser (routes/agent_runtimes.go). Signing a CLI in changes
// which account every agent session bills to, so an agent holding the API key
// must not be able to start, steer or cancel it. There is no MCP tool for it.
const AgentLoginAction = "Signing an agent CLI in"

// AgentRuntimesHandler serves /api/agent/runtimes.
type AgentRuntimesHandler struct {
	detector *agentruntime.Detector
	logins   *agentruntime.LoginManager
}

// NewAgentRuntimesHandler builds the handler. Provider keys stored in repo
// (Settings → Providers) count as "ready" for the runtime that uses them.
func NewAgentRuntimesHandler(repo *database.Repository) *AgentRuntimesHandler {
	keys := func(ids ...string) []string {
		if repo == nil {
			return nil
		}
		var out []string
		for _, id := range ids {
			p, err := repo.GetProvider(id)
			if err == nil && p != nil && p.APIKey != nil && strings.TrimSpace(*p.APIKey) != "" {
				out = append(out, id)
			}
		}
		return out
	}
	d := agentruntime.NewDetector(keys)
	codexUsageSource = d.CodexUsage
	return &AgentRuntimesHandler{detector: d, logins: agentruntime.NewLoginManager(d)}
}

// NewAgentRuntimesHandlerWith is for tests.
func NewAgentRuntimesHandlerWith(d *agentruntime.Detector, l *agentruntime.LoginManager) *AgentRuntimesHandler {
	return &AgentRuntimesHandler{detector: d, logins: l}
}

// Close stops running login flows (server shutdown).
func (h *AgentRuntimesHandler) Close() {
	if h != nil && h.logins != nil {
		h.logins.Close()
	}
}

// HandleList returns both runtimes. ?refresh=1 skips the short cache.
// Readable with the API key (agents may check what is available), but the
// masked account hint and login flow details are only sent to a signed-in user.
func (h *AgentRuntimesHandler) HandleList(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.UserContext(), 30*time.Second)
	defer cancel()
	list := h.detector.Check(ctx, c.Query("refresh") == "1")
	isUser := principal.Allowed(principal.From(c))
	anyReady := false
	logins := fiber.Map{}
	for i := range list {
		if list[i].Ready {
			anyReady = true
		}
		if !isUser {
			list[i].Auth.Account = ""
			continue
		}
		if f, ok := h.logins.Get(list[i].ID); ok {
			logins[list[i].ID] = f
		}
	}
	return c.JSON(fiber.Map{"runtimes": list, "any_ready": anyReady, "logins": logins})
}

func (h *AgentRuntimesHandler) runtimeParam(c *fiber.Ctx) (string, error) {
	id := c.Params("runtime")
	if !agentruntime.IsRuntime(id) {
		return "", c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "unknown runtime"})
	}
	return id, nil
}

// HandleLoginStart starts the CLI's own login. Privileged.
// Body: {"mode": "browser" | "console" (claude) | "device" (codex)}.
func (h *AgentRuntimesHandler) HandleLoginStart(c *fiber.Ctx) error {
	id, err := h.runtimeParam(c)
	if id == "" {
		return err
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&body); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
		}
	}
	flow, err := h.logins.Start(id, body.Mode)
	switch {
	case errors.Is(err, agentruntime.ErrBadMode):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	case errors.Is(err, agentruntime.ErrNotInstalled):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	case err != nil:
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": flow.Message, "flow": flow})
	}
	logging.Info("🔑 %s login started (mode %s) by %v", id, flow.Mode, c.Locals("username"))
	return c.JSON(fiber.Map{"flow": flow})
}

// HandleLoginStatus returns the runtime's current login flow. Privileged
// (it carries the sign-in URL).
func (h *AgentRuntimesHandler) HandleLoginStatus(c *fiber.Ctx) error {
	id, err := h.runtimeParam(c)
	if id == "" {
		return err
	}
	flow, ok := h.logins.Get(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": agentruntime.ErrNoFlow.Error()})
	}
	return c.JSON(fiber.Map{"flow": flow})
}

// HandleLoginCancel cancels the runtime's login flow. Privileged.
func (h *AgentRuntimesHandler) HandleLoginCancel(c *fiber.Ctx) error {
	id, err := h.runtimeParam(c)
	if id == "" {
		return err
	}
	flow, ok := h.logins.Cancel(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": agentruntime.ErrNoFlow.Error()})
	}
	return c.JSON(fiber.Map{"flow": flow})
}

// HandleLoginCode sends the code from Claude's fallback sign-in page to the
// waiting `claude auth login`. Privileged. Body: {"code": "..."}. The code is
// never logged.
func (h *AgentRuntimesHandler) HandleLoginCode(c *fiber.Ctx) error {
	id, err := h.runtimeParam(c)
	if id == "" {
		return err
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	switch err := h.logins.SubmitCode(id, body.Code); {
	case errors.Is(err, agentruntime.ErrBadCode):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	case errors.Is(err, agentruntime.ErrNoFlow), errors.Is(err, agentruntime.ErrNoCode):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	case err != nil:
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "could not send the code to the CLI"})
	}
	flow, _ := h.logins.Get(id)
	return c.JSON(fiber.Map{"flow": flow})
}

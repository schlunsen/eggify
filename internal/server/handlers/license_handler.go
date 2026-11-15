package handlers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/schlunsen/eggify/internal/license"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// LicenseAction names the license changes in "requires a signed-in user"
// refusals.
const LicenseAction = "Activating or deactivating Eggify"

// LicenseController is the license manager as seen by the API
// (*license.Manager).
type LicenseController interface {
	Status() license.Status
	Activate(ctx context.Context, serial, machine string) (license.Status, error)
	Deactivate(ctx context.Context) (license.Status, error)
	CheckNow(ctx context.Context) (license.Status, error)
}

// LicenseHandler serves /api/license.
type LicenseHandler struct {
	ctl LicenseController
	// hasUsers reports whether an account exists. Before the first account
	// (a fresh install) activation needs no user: the activation page comes
	// before /setup. nil counts as "accounts exist".
	hasUsers func() bool
}

// NewLicenseHandler creates the handler; ctl may be nil (status
// "unlicensed", not required; changes 503).
func NewLicenseHandler(ctl LicenseController, hasUsers func() bool) *LicenseHandler {
	h := &LicenseHandler{hasUsers: hasUsers}
	if m, ok := ctl.(*license.Manager); ok && m == nil {
		return h
	}
	h.ctl = ctl
	return h
}

// ActivateGate is the route gate of POST /api/license/activate: a signed-in
// admin (principal.RequireUser) once an account exists, so an agent holding
// the API key can't swap in its own serial. Before the first account it is
// open while the install is not activated: the same window in which anyone
// local may create that account on /setup, and in which no agent can run
// (nothing works unlicensed). An install activated before setup can't be
// re-activated until the account exists.
func (h *LicenseHandler) ActivateGate() fiber.Handler {
	requireUser := principal.RequireUser(LicenseAction)
	return func(c *fiber.Ctx) error {
		if h.hasUsers != nil && !h.hasUsers() &&
			(h.ctl == nil || h.ctl.Status().State != license.StateActive) {
			return c.Next()
		}
		return requireUser(c)
	}
}

func (h *LicenseHandler) unavailable(c *fiber.Ctx) error {
	return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
		"error": "Licensing isn't available on this server", "code": "license_unavailable",
	})
}

// HandleStatus is GET /api/license.
func (h *LicenseHandler) HandleStatus(c *fiber.Ctx) error {
	if h.ctl == nil {
		return c.JSON(license.Status{State: license.StateUnlicensed})
	}
	return c.JSON(h.ctl.Status())
}

// licenseError answers a failed activate/deactivate/check: the license
// server's code and plain words, plus the machines for seats_full.
func licenseError(c *fiber.Ctx, st license.Status, err error) error {
	body := fiber.Map{"error": err.Error(), "status": st}
	status := fiber.StatusBadGateway
	var ae *license.APIError
	if errors.As(err, &ae) {
		body["code"] = ae.Code
		if len(ae.Activations) > 0 {
			body["activations"] = ae.Activations
		}
		switch {
		case ae.Code == license.CodeOffline || ae.Code == license.CodeServerError:
			status = fiber.StatusBadGateway
		case ae.Status >= 400 && ae.Status < 500:
			// Pass the license server's verdict on (400 invalid_serial,
			// 404 unknown_serial, 409 seats_full, 429, ...). Its 401s are
			// about this install's signature, not the caller: 400 here.
			status = ae.Status
			if status == fiber.StatusUnauthorized || status == fiber.StatusForbidden {
				status = fiber.StatusBadRequest
			}
		}
	} else {
		body["code"] = "license_error"
		status = fiber.StatusInternalServerError
	}
	return c.Status(status).JSON(body)
}

// HandleActivate is POST /api/license/activate {serial, machine?}.
func (h *LicenseHandler) HandleActivate(c *fiber.Ctx) error {
	if h.ctl == nil {
		return h.unavailable(c)
	}
	var req struct {
		Serial  string `json:"serial"`
		Machine string `json:"machine"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body", "code": "bad_request"})
	}
	machine := strings.TrimSpace(req.Machine)
	if len([]rune(machine)) > 100 {
		machine = string([]rune(machine)[:100])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := h.ctl.Activate(ctx, req.Serial, machine)
	if err != nil {
		return licenseError(c, st, err)
	}
	return c.JSON(st)
}

// HandleDeactivate is POST /api/license/deactivate.
func (h *LicenseHandler) HandleDeactivate(c *fiber.Ctx) error {
	if h.ctl == nil {
		return h.unavailable(c)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := h.ctl.Deactivate(ctx)
	if err != nil {
		return licenseError(c, st, err)
	}
	return c.JSON(st)
}

// HandleCheck is POST /api/license/check (Settings → Check now): renew now.
func (h *LicenseHandler) HandleCheck(c *fiber.Ctx) error {
	if h.ctl == nil {
		return h.unavailable(c)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := h.ctl.CheckNow(ctx)
	if err != nil {
		return licenseError(c, st, err)
	}
	return c.JSON(st)
}

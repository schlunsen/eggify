package handlers

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"

	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/machineproxy"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// PairedMachinesAction names these routes in user_required refusals.
const PairedMachinesAction = "Adding your other Macs to a device"

// PairedMachine is another of the owner's Macs that the desktop app on this
// Mac is paired with (ADR 014). Never a token.
type PairedMachine struct {
	ID      string `json:"id"`       // relay install id
	Name    string `json:"name"`     // its host name
	BaseURL string `json:"base_url"` // its relay address
}

// PairedMachines is the desktop app's machine list as seen by the server
// (cmd/eggify-desktop's machineManager). The CLI server has none.
type PairedMachines interface {
	List() []PairedMachine
	// PairCode has the machine mint a pair link for another device of the
	// owner, using this Mac's paired session there. The session token
	// stays in the desktop app; only the link comes back.
	PairCode(ctx context.Context, id, deviceName string) (pairURL string, expiresAt time.Time, err error)
}

// ErrPairedMachineNotFound is PairCode's answer for an unknown id.
var ErrPairedMachineNotFound = errors.New("no such paired Mac")

// MachinesHandler serves GET /api/machines/paired and
// POST /api/machines/paired/:id/pair-code. The iPhone, signed in to this
// Mac, uses them to add the Macs this Mac knows with its own session on
// each (it redeems the link at that Mac directly).
type MachinesHandler struct {
	src func() PairedMachines
}

// NewMachinesHandler creates the handler; src may be nil or return nil (no
// desktop app: an empty list and 404 for pair codes).
func NewMachinesHandler(src func() PairedMachines) *MachinesHandler {
	return &MachinesHandler{src: src}
}

func (h *MachinesHandler) machines() PairedMachines {
	if h == nil || h.src == nil {
		return nil
	}
	return h.src()
}

// HandleList: GET /api/machines/paired → {machines: [{id, name, base_url}]}.
func (h *MachinesHandler) HandleList(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	list := []PairedMachine{}
	if pm := h.machines(); pm != nil {
		list = append(list, pm.List()...)
	}
	return c.JSON(fiber.Map{"machines": list, "available": h.machines() != nil})
}

type pairCodeRequest struct {
	DeviceName string `json:"device_name"`
}

// HandlePairCode: POST /api/machines/paired/:id/pair-code {device_name} →
// {pair_url, expires_at, name}. The pair URL is a credential for the
// caller's account on the other Mac: it is never logged.
func (h *MachinesHandler) HandlePairCode(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	pm := h.machines()
	id := c.Params("id")
	if pm == nil || id == "" || len(id) > 64 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "No such paired Mac", "code": "machine_not_found"})
	}
	var req pairCodeRequest
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}
	}
	name := ""
	for _, m := range pm.List() {
		if m.ID == id {
			name = m.Name
		}
	}
	if name == "" {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "No such paired Mac", "code": "machine_not_found"})
	}
	caller, _ := principal.Username(c)
	logging.Info("Pair code on paired machine %s requested by %s", id, caller)

	ctx, cancel := context.WithTimeout(c.UserContext(), 30*time.Second)
	defer cancel()
	pairURL, expiresAt, err := pm.PairCode(ctx, id, clipDeviceName(req.DeviceName))
	switch {
	case errors.Is(err, ErrPairedMachineNotFound):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "No such paired Mac", "code": "machine_not_found"})
	case errors.Is(err, machineproxy.ErrSignedOut):
		logging.Warning("Pair code on paired machine %s: this Mac was signed out there", id)
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": name + " signed this Mac out. Pair it again on this Mac, or scan " + name + "'s own QR code.",
			"code":  "machine_signed_out",
		})
	case err != nil:
		logging.Warning("Pair code on paired machine %s failed: %v", id, err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Couldn't reach " + name + ": " + err.Error(), "code": "machine_unreachable"})
	}
	return c.JSON(fiber.Map{"pair_url": pairURL, "expires_at": expiresAt.UTC(), "name": name})
}

func clipDeviceName(s string) string {
	s = strings.TrimSpace(s)
	for len(s) > 64 {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}

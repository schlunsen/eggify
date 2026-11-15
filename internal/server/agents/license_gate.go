package agents

import (
	"sync/atomic"

	fiberws "github.com/gofiber/websocket/v2"
)

// LicenseRequiredCode is the error code of a refusal while Eggify is not
// activated (REST 402 and WebSocket "code"; see internal/license).
const LicenseRequiredCode = "license_required"

// licenseGate returns nil when the app may be used. The server installs it
// (license.Manager.Gate); nil means licensing is not in play.
var licenseGate atomic.Pointer[func() error]

// SetLicenseGate installs the license check prompts go through. nil removes it.
func SetLicenseGate(fn func() error) {
	if fn == nil {
		licenseGate.Store(nil)
		return
	}
	licenseGate.Store(&fn)
}

// checkLicense refuses work while the install is not licensed. The HTTP
// middleware already refuses /agent/ws and the API; this is defence in depth
// for every path that starts a turn (WebSocket prompts, loops, handovers,
// resumes after a restart or the usage limit).
func checkLicense() error {
	if fn := licenseGate.Load(); fn != nil {
		return (*fn)()
	}
	return nil
}

// sendLicenseRequired tells a WebSocket client the prompt was refused.
func (h *AgentHandler) sendLicenseRequired(c *fiberws.Conn, err error) {
	_ = h.safeWriteJSON(c, map[string]interface{}{
		"type":    "error",
		"code":    LicenseRequiredCode,
		"message": err.Error(),
	})
}

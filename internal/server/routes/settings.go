package routes

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/server/handlers"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// RegisterSettings configures user settings endpoints
func RegisterSettings(api fiber.Router, h *handlers.SettingsHandler) {
	api.Get("/settings", h.HandleGetAllSettings)
	api.Get("/settings/:key", h.HandleGetSetting)
	api.Put("/settings/:key", requireUserForSecuritySetting, h.HandleUpdateSetting)
	api.Delete("/settings/:key", requireUserForSecuritySetting, h.HandleDeleteSetting)
}

// securitySettingMarkers are substrings of setting keys that affect security
// (e.g. default_permission_mode). Writing those needs a signed-in user; other
// preferences (hotkeys, diff location, STT engine, ...) keep working with the
// API key, which the desktop app uses.
var securitySettingMarkers = []string{
	"permission", "yolo", "auth", "api_key", "apikey", "token", "secret",
	"password", "provider", "base_url", "connector", "mcp", "hook", "trust", "sandbox",
	"tunnel", "ngrok", "remote_access",
}

// IsSecuritySetting reports whether writing the user setting key is privileged.
func IsSecuritySetting(key string) bool {
	k := strings.ToLower(key)
	for _, m := range securitySettingMarkers {
		if strings.Contains(k, m) {
			return true
		}
	}
	return false
}

func requireUserForSecuritySetting(c *fiber.Ctx) error {
	if IsSecuritySetting(c.Params("key")) {
		if ok, err := principal.Check(c, "Changing this setting"); !ok {
			return err
		}
	}
	return c.Next()
}

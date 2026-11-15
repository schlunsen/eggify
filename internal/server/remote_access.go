package server

import (
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/relay"
	"github.com/schlunsen/eggify/internal/remoteaccess"
	"github.com/schlunsen/eggify/internal/version"
)

// Remote access: an ngrok tunnel served as an *additional* listener of the
// Fiber app (fasthttp serves any number of listeners). The local listener
// (CLI 127.0.0.1:3333, desktop loopback webview listener) keeps running; the
// tunnel can be started and stopped at runtime from Settings, the API or the
// desktop tray. See internal/remoteaccess.
//
// Before, `--tunnel` / EGGIFY_TUNNEL_ENABLED replaced the main listener and
// turned local TLS off. Now it only means "start remote access at boot"; the
// local listener keeps its own TLS setting.

// remoteAccessFile holds the encrypted remote access settings.
func (s *Server) remoteAccessFile() string {
	return filepath.Join(s.claudeDir, "eggify", "remote_access.json")
}

// newRemoteAccessProvider is the tunnel provider; tests replace it.
var newRemoteAccessProvider = func() remoteaccess.Provider {
	return remoteaccess.NewNgrokProvider(version.Version)
}

// newRelayProvider opens relay-mode tunnels (Eggify's own relay, TLS ends
// here with the install's key); tests replace it.
var newRelayProvider = func() remoteaccess.Provider { return &remoteaccess.RelayProvider{} }

// newNgrokAPI is the ngrok REST API client for agent sessions (nil:
// api.ngrok.com); the eggify_e2e build points it at a fake server.
var newNgrokAPI = func() *remoteaccess.NgrokAPI { return nil }

// setupRemoteAccess creates the manager. key is the connector encryption key.
func (s *Server) setupRemoteAccess(key []byte) {
	var store *remoteaccess.Store
	if st, err := remoteaccess.NewStore(s.remoteAccessFile(), key); err != nil {
		logging.Error("Remote access settings store unavailable: %v", err)
	} else {
		store = st
	}
	s.remoteAccess = remoteaccess.NewManager(remoteaccess.Config{
		Provider: newRemoteAccessProvider(),
		Serve:    s.serveTunnel,
		Store:    store,
		Env: remoteaccess.EnvSettings{
			AuthToken: s.config.Tunnel.AuthToken,
			Domain:    s.config.Tunnel.Domain,
		},
		Guard:            s.remoteAccessGuard,
		API:              newNgrokAPI(),
		RelayProvider:    s.withRelayLicense(newRelayProvider()),
		DefaultRelayAddr: os.Getenv("EGGIFY_RELAY_ADDR"),
		Logf:             func(format string, args ...any) { logging.Info(format, args...) },
	})
	if !s.quiet {
		s.remoteAccess.Subscribe(func(st remoteaccess.Status) {
			switch st.Status {
			case remoteaccess.StateConnected:
				fmt.Printf("🌍 Remote access: %s\n", st.PublicURL)
			case remoteaccess.StateError:
				fmt.Printf("❌ Remote access failed: %s\n", st.Error)
			}
		})
	}
}

// RemoteAccess returns the remote access manager (nil before Setup). The
// desktop app's tray uses it in-process.
func (s *Server) RemoteAccess() *remoteaccess.Manager { return s.remoteAccess }

// remoteAccessGuard: a public URL is only allowed with user authentication
// on and the admin account created. With user auth off nothing tells the
// human from anyone else, and without an admin, whoever opens the URL first
// could create one on /setup.
func (s *Server) remoteAccessGuard() error {
	if s.sessionAuthMiddleware == nil || s.userStore == nil {
		return &remoteaccess.GuardError{
			Code: "user_auth_disabled",
			Msg: "Remote access needs user authentication. Turn it on (EGGIFY_AUTH_USER_AUTH_ENABLED=true or " +
				"\"user_auth_enabled\": true in ~/.claude/eggify/config.json) and restart Eggify.",
		}
	}
	if !s.userStore.HasUsers() {
		return &remoteaccess.GuardError{
			Code: "setup_required",
			Msg:  "Create the admin account (open Eggify locally and finish /setup) before turning on remote access.",
		}
	}
	return nil
}

// serveTunnel serves the app on a tunnel listener until it is closed. It uses
// the app's fasthttp server directly (no Fiber startup banner / OnListen
// hooks per tunnel); Handler() builds the route tree if no listener did yet.
func (s *Server) serveTunnel(ln net.Listener) error {
	s.app.Handler()
	return s.app.Server().Serve(ln)
}

// autostartRemoteAccess starts the tunnel at boot when the stored settings
// say so (Settings → Remote access → "start when Eggify starts") or the CLI
// asked for it (--tunnel / EGGIFY_TUNNEL_ENABLED). Only an explicit CLI
// request that can't even begin (no token, user auth off) stops the server,
// as --tunnel did before; connection errors show up in the status.
func (s *Server) autostartRemoteAccess() error {
	if s.remoteAccess == nil {
		return nil
	}
	cliRequested := s.config != nil && s.config.Tunnel.Enabled
	if !cliRequested && !s.remoteAccess.Settings().Autostart {
		return nil
	}
	if err := s.remoteAccess.Start(); err != nil {
		if cliRequested {
			return fmt.Errorf("remote access (--tunnel): %w", err)
		}
		logging.Warning("Remote access autostart skipped: %v", err)
	}
	return nil
}

// tunnelOnlyLocalPaths are never served over the public tunnel: the desktop
// bridge and loopback-only internal endpoints (they also check the peer
// address themselves; tunnel connections never report a loopback peer).
var tunnelOnlyLocalPaths = []string{"/__desktop", "/wails", "/internal"}

// isTunnelRequest reports whether c arrived through the public tunnel.
func isTunnelRequest(c *fiber.Ctx) bool {
	return remoteaccess.IsTunnelConn(c.Context().Conn())
}

// refuseAPIKeyOverTunnel answers 401 for a request that presented the API key
// through the public tunnel. The key is a file every local process can read,
// agents included; a prompt-injected agent could send it out together with
// the public URL. Over the tunnel only a signed-in user's session works.
func refuseAPIKeyOverTunnel(c *fiber.Ctx) error {
	logging.Warning("Refused the API key over remote access from %s %s", c.IP(), c.Path())
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"error":   "Unauthorized",
		"code":    "api_key_not_allowed_remotely",
		"message": remoteaccess.APIKeyRefusedMessage,
	})
}

// requestIsHTTPS reports whether the client reached Eggify over https: a TLS
// listener, or the ngrok tunnel (https at the edge). Request headers such as
// X-Forwarded-Proto never count; any local process could send them.
func requestIsHTTPS(c *fiber.Ctx) bool {
	return c.Context().IsTLS() || isTunnelRequest(c)
}

// rawRequestPath is the request path exactly as sent (before Fiber's or
// fasthttp's normalising), without the query string.
func rawRequestPath(c *fiber.Ctx) string {
	uri := string(c.Context().Request.Header.RequestURI())
	if i := strings.IndexAny(uri, "?#"); i >= 0 {
		uri = uri[:i]
	}
	return uri
}

// suspiciousTunnelPath reports a raw path no legitimate client sends: dot
// segments, empty segments, or percent-encoded '.', '/' or '\'. Fiber routes
// on the raw path while checks elsewhere clean it, so such paths could match
// one thing in a guard and another in the router.
func suspiciousTunnelPath(raw string) bool {
	p := strings.ToLower(raw)
	if strings.Contains(p, "%2e") || strings.Contains(p, "%2f") || strings.Contains(p, "%5c") ||
		strings.Contains(p, "\\") {
		return true
	}
	if strings.Contains(p, "//") || strings.Contains(p, "/./") || strings.Contains(p, "/../") ||
		strings.HasSuffix(p, "/..") || strings.HasSuffix(p, "/.") || p == "." || p == ".." {
		return true
	}
	return false
}

// tunnelPathDenied reports whether p (lower case) is a local-only path.
func tunnelPathDenied(p string) bool {
	for _, deny := range tunnelOnlyLocalPaths {
		if p == deny || strings.HasPrefix(p, deny+"/") {
			return true
		}
	}
	return false
}

// tunnelPathGuard answers 404 for local-only paths requested over the tunnel,
// and marks every tunnel request (remoteaccess.ViaTunnelLocal) so WebSocket
// handlers, which no longer see the tunnel connection, can refuse the API key.
//
// SECURITY: Fiber matches routes on the raw path, so the guard looks at the
// raw path as well as the cleaned one (`/wails/../api/health` cleans to
// /api/health but is routed to /wails/*), and refuses any tunnel request
// with dot segments, empty segments or encoded separators outright.
func (s *Server) tunnelPathGuard(c *fiber.Ctx) error {
	if !isTunnelRequest(c) {
		return c.Next()
	}
	c.Locals(remoteaccess.ViaTunnelLocal, true)
	raw := rawRequestPath(c)
	if suspiciousTunnelPath(raw) {
		return c.SendStatus(fiber.StatusNotFound)
	}
	for _, cand := range []string{raw, c.Path()} {
		lower := strings.ToLower(cand)
		if tunnelPathDenied(lower) || tunnelPathDenied(path.Clean("/"+lower)) {
			return c.SendStatus(fiber.StatusNotFound)
		}
	}
	return c.Next()
}

// withRelayLicense lets the relay provider present this install's license,
// so the Eggify relay (subscribers only) admits it.
func (s *Server) withRelayLicense(p remoteaccess.Provider) remoteaccess.Provider {
	if rp, ok := p.(*remoteaccess.RelayProvider); ok && rp.License == nil {
		rp.License = func() *relay.LicenseCredentials {
			if s.license == nil {
				return nil
			}
			token, key := s.license.RelayCredentials()
			if token == "" {
				return nil
			}
			return &relay.LicenseCredentials{Token: token, Key: key}
		}
	}
	return p
}

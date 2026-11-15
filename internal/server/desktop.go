package server

import (
	"fmt"
	"net"

	"github.com/gofiber/fiber/v2"

	"github.com/schlunsen/eggify/internal/server/handlers"
)

// PairedMachines is the desktop app's list of the owner's other Macs (ADR
// 014), for GET /api/machines/paired and POST …/:id/pair-code.
type PairedMachines = handlers.PairedMachines

// PairedMachine is one entry of PairedMachines.List (no tokens).
type PairedMachine = handlers.PairedMachine

// DesktopOptions configures the server when it runs in-process inside the
// desktop app (cmd/eggify-desktop). The CLI never sets it.
type DesktopOptions struct {
	// Routes registers desktop-only routes on the Fiber app. They are added
	// before any middleware (security headers, CORS, auth) and before the SPA
	// fallback, so they must authenticate requests themselves.
	Routes func(app *fiber.App)

	// PairedMachines returns the desktop app's paired Macs (nil while there
	// are none to offer). Called per request, so the app may create its
	// machine manager after EnableDesktopMode.
	PairedMachines func() PairedMachines
}

// pairedMachines is the desktop's paired Macs, nil outside the desktop app.
func (s *Server) pairedMachines() handlers.PairedMachines {
	if s.desktop == nil || s.desktop.PairedMachines == nil {
		return nil
	}
	return s.desktop.PairedMachines()
}

// EnableDesktopMode switches the server to the desktop app's loopback mode.
// Call it before Setup.
//
// In desktop mode the server is reached only by the app's own webview over a
// listener bound to 127.0.0.1 (see Serve):
//   - TLS is off. The traffic never leaves the machine, and a self-signed
//     certificate would have to be trusted by WKWebView. With TLS off the
//     session cookie is not marked Secure, so WebKit keeps it on
//     http://127.0.0.1.
//   - The bind host is forced to 127.0.0.1 (a plain-HTTP listener must never
//     be reachable from the network, whatever config.json says), and the CLI's
//     --tunnel / EGGIFY_TUNNEL_ENABLED is ignored. Remote access (ngrok) is
//     still available as an extra listener, switched on from Settings or the
//     tray (and at launch when its autostart setting is on).
//
// Authentication is unchanged: the API key still only identifies "a local
// process", and privileged actions still need a signed-in user.
func (s *Server) EnableDesktopMode(opts DesktopOptions) {
	s.desktop = &opts
}

// applyDesktopConfig forces the desktop overrides onto the loaded config. The
// config is not saved, so config.json keeps the CLI's settings.
func (s *Server) applyDesktopConfig(config *Config) {
	if s.desktop == nil {
		return
	}
	config.TLS.Enabled = false
	config.Server.Host = "127.0.0.1"
	config.Tunnel.Enabled = false
	s.tlsConfig = &TLSConfig{Enabled: false}
}

// registerDesktopRoutes runs DesktopOptions.Routes (desktop mode only).
func (s *Server) registerDesktopRoutes() {
	if s.desktop != nil && s.desktop.Routes != nil {
		s.desktop.Routes(s.app)
	}
}

// Serve serves the app on ln instead of opening its own listener. It is only
// available in desktop mode and requires ln to be a loopback TCP listener, so
// the plain-HTTP server can't be exposed by mistake.
func (s *Server) Serve(ln net.Listener) error {
	if s.desktop == nil {
		return fmt.Errorf("Serve is only available in desktop mode")
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		return fmt.Errorf("desktop listener must be a loopback TCP listener, got %s", ln.Addr())
	}
	_ = s.autostartRemoteAccess() // settings autostart only; errors are in the status
	return s.app.Listener(ln)
}

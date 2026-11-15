package server

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/schlunsen/eggify/internal/connectors"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/n0"
	"github.com/schlunsen/eggify/internal/server/handlers"
)

// setupN0 creates the n0 connection manager and starts the loopback-only
// listener that serves the eggify-tools → Eggify n0 proxy and the loopback OAuth
// callback. It runs before the auth middleware is installed so the public
// callback route can be registered pre-auth.
func (s *Server) setupN0() {
	if s.db == nil {
		return
	}
	key, err := connectors.LoadOrGenerateConnectorKey(s.claudeDir)
	if err != nil {
		// Don't fall back to an ephemeral key: a token refresh would persist the
		// rotated pair under a key that is gone after a restart, and n0 revokes
		// the connection when the spent refresh token is replayed. This happens
		// e.g. when the key is in a locked/unreachable macOS keychain.
		logging.Error("n0: disabled for this run, connector key unavailable: %v", err)
		return
	}
	s.n0Manager = n0.NewManager(s.db, key)
	ls, err := n0.StartLoopback(s.n0Manager, 0)
	if err != nil {
		logging.Warning("n0: loopback listener failed to start, n0 tools are unavailable: %v", err)
	} else {
		s.n0Loopback = ls
		logging.Info("n0: loopback proxy/callback listening on %s", ls.BaseURL())
	}
	s.n0Handler = handlers.NewN0Handler(s.n0Manager, s.db, key, func() string {
		if s.n0Loopback == nil {
			return ""
		}
		return s.n0Loopback.CallbackURL()
	})
}

// wireN0Sessions connects the n0 manager to the agent session manager (so
// every runtime's eggify-tools process can reach the proxy) and to the
// session-ownership checks.
func (s *Server) wireN0Sessions() {
	if s.n0Manager == nil || s.agentHandler == nil || s.agentHandler.SessionManager == nil {
		return
	}
	proxyURL := ""
	if s.n0Loopback != nil {
		proxyURL = s.n0Loopback.ProxyURL()
	}
	sm := s.agentHandler.SessionManager
	sm.SetN0(s.n0Manager, proxyURL)
	if s.n0Loopback != nil {
		// Session control endpoints (tool approvals, handovers) for eggify-tools.
		s.n0Loopback.Handle(n0.ControlPathPrefix, sm.ControlHandler())
	}
	if s.n0Handler != nil {
		var authorize func(*fiber.Ctx, uuid.UUID) bool
		if s.agentSessionHandler != nil {
			authorize = s.agentSessionHandler.AuthorizeSession
		}
		s.n0Handler.SetSessionHooks(authorize, sm.MarkSessionToolsStale)
	}
}

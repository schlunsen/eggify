package server

import (
	"os"
	"strings"

	"github.com/schlunsen/eggify/internal/credproxy"
	"github.com/schlunsen/eggify/internal/logging"
)

// setupProviderProxy starts the loopback model-provider proxy and hands it to
// the agent session manager, so Claude CLI and Codex sessions get a
// per-session capability instead of the real provider API key.
//
// EGGIFY_PROVIDER_PROXY=off disables it (keys are then put in the agent
// environment as before) - a troubleshooting escape hatch only.
func (s *Server) setupProviderProxy() {
	if s.agentHandler == nil || s.agentHandler.SessionManager == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("EGGIFY_PROVIDER_PROXY"))) {
	case "off", "0", "false", "disabled":
		logging.Warning("provider proxy disabled by EGGIFY_PROVIDER_PROXY - agents receive provider API keys in their environment")
		return
	}
	sm := s.agentHandler.SessionManager
	p, err := credproxy.Start(sm.ResolveProviderUpstream, 0)
	if err != nil {
		logging.Warning("provider proxy failed to start, agents will receive provider API keys in their environment: %v", err)
		return
	}
	s.providerProxy = p
	sm.SetProviderProxy(p)
	logging.Info("provider proxy listening on %s", p.BaseURL(credproxy.KindAnthropic))
}

package remoteaccess

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

// Agent sessions: with an optional ngrok API key, Eggify lists the account's
// connected ngrok agents (each one uses one of the account's simultaneous
// agent sessions; free accounts have 3, see ERR_NGROK_108) and can stop
// them. This Eggify's own session is recognisable by its metadata.

// ErrNoAPIKey: listing or stopping agent sessions needs an ngrok API key.
var ErrNoAPIKey = errors.New("add an ngrok API key first (dashboard.ngrok.com/api-keys)")

// ErrOwnSession: stopping this Eggify's live session goes through Stop.
var ErrOwnSession = errors.New("this is this Eggify's own session; turn remote access off instead")

// agentTimeout bounds one list/stop call to the ngrok API.
const agentTimeout = 20 * time.Second

// AgentMetadata is the metadata Eggify sets on its ngrok agent session:
// "eggify:<hostname>:<install id>:<nonce>". The nonce changes per start, so
// the live session can be told from stale ones of the same install.
func AgentMetadata(hostname, installID, nonce string) string {
	clean := func(s string) string {
		s = strings.Map(func(r rune) rune {
			if r == ':' || r < ' ' || r > '~' {
				return '-'
			}
			return r
		}, strings.TrimSpace(s))
		if len(s) > 64 {
			s = s[:64]
		}
		return s
	}
	return "eggify:" + clean(hostname) + ":" + clean(installID) + ":" + clean(nonce)
}

// parseAgentMetadata splits Eggify's metadata (ok=false for other agents).
func parseAgentMetadata(md string) (hostname, installID, nonce string, ok bool) {
	parts := strings.Split(md, ":")
	if len(parts) != 4 || parts[0] != "eggify" {
		return "", "", "", false
	}
	return parts[1], parts[2], parts[3], true
}

// AgentSession is a connected ngrok agent, as shown in Settings.
type AgentSession struct {
	TunnelSession
	// Eggify: started by an Eggify (any install); EggifyHost is its host name.
	Eggify     bool   `json:"eggify"`
	EggifyHost string `json:"eggify_host,omitempty"`
	// ThisInstall: started by this Eggify install. Current: it is the
	// session remote access is using right now.
	ThisInstall bool `json:"this_install"`
	Current     bool `json:"current"`
}

// SetAPIKey saves the optional ngrok API key.
func (m *Manager) SetAPIKey(key string) error {
	k, err := NormalizeAPIKey(key)
	if err != nil {
		return err
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	next := m.settings
	m.mu.Unlock()
	next.APIKey = k
	return m.save(next)
}

// DeleteAPIKey removes the ngrok API key.
func (m *Manager) DeleteAPIKey() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	next := m.settings
	m.mu.Unlock()
	if next.APIKey == "" {
		return nil
	}
	next.APIKey = ""
	return m.save(next)
}

func (m *Manager) apiKey() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings.APIKey
}

// AgentSessions lists the account's connected ngrok agents, this Eggify's
// first, then newest first.
func (m *Manager) AgentSessions(ctx context.Context) ([]AgentSession, error) {
	key := m.apiKey()
	if key == "" {
		return nil, ErrNoAPIKey
	}
	ctx, cancel := context.WithTimeout(ctx, agentTimeout)
	defer cancel()
	list, err := m.cfg.API.ListTunnelSessions(ctx, key)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	installID, nonce, running := m.settings.InstallID, m.nonce, m.cancel != nil
	m.mu.Unlock()
	out := make([]AgentSession, 0, len(list))
	for _, ts := range list {
		a := AgentSession{TunnelSession: ts}
		if host, inst, n, ok := parseAgentMetadata(ts.Metadata); ok {
			a.Eggify, a.EggifyHost = true, host
			a.ThisInstall = installID != "" && inst == installID
			a.Current = a.ThisInstall && running && nonce != "" && n == nonce
		}
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Current != out[j].Current {
			return out[i].Current
		}
		return out[i].StartedAt > out[j].StartedAt
	})
	return out, nil
}

// StopAgentSession stops the ngrok agent behind session id. This Eggify's
// live session is refused (ErrOwnSession): use Stop.
func (m *Manager) StopAgentSession(ctx context.Context, id string) error {
	key := m.apiKey()
	if key == "" {
		return ErrNoAPIKey
	}
	if !ValidTunnelSessionID(id) {
		return &APIError{Status: 400, Code: APIErrNotFound, Msg: "invalid agent session id"}
	}
	ctx, cancel := context.WithTimeout(ctx, agentTimeout)
	defer cancel()
	m.mu.Lock()
	nonce, installID, running := m.nonce, m.settings.InstallID, m.cancel != nil
	m.mu.Unlock()
	if running && nonce != "" {
		// Look the session up first, so the live one isn't stopped by mistake.
		list, err := m.cfg.API.ListTunnelSessions(ctx, key)
		if err != nil {
			return err
		}
		for _, ts := range list {
			if ts.ID != id {
				continue
			}
			if _, inst, n, ok := parseAgentMetadata(ts.Metadata); ok && inst == installID && n == nonce {
				return ErrOwnSession
			}
		}
	}
	if err := m.cfg.API.StopTunnelSession(ctx, key, id); err != nil {
		return err
	}
	m.cfg.Logf("remote access: stopped ngrok agent session %s", id)
	return nil
}

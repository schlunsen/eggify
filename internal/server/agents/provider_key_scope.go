package agents

import (
	"strings"

	"github.com/schlunsen/eggify/internal/llmclient"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/providers"
)

// Stored provider keys only go to their provider's own host.
//
// A session can carry its own base_url (create_session, change_session_model,
// PATCH .../model). Before, a Claude CLI, Codex or direct session whose
// provider row had no URL of its own sent the STORED key (providers table,
// server config or env) to whatever base_url the session named, so anyone who
// could set a session's base_url could collect the key at their own server.
// Now a session base_url that is not one of the provider's own origins (its
// custom_url / base_url row, its registry URL, or the built-in default) gets
// only a key supplied with the session itself. Without one it gets
// noStoredKeyPlaceholder, a non-secret value that also keeps the Claude CLI
// and Codex from falling back to their own logins (whose tokens would
// otherwise go to that host).

// noStoredKeyPlaceholder is the API key a session talking to a custom base
// URL gets when it brought no key of its own. Local servers (Ollama, LiteLLM)
// accept any key.
const noStoredKeyPlaceholder = "eggify-no-stored-key"

// providerOwnBases lists the base URLs a provider's stored key belongs to:
// its DB custom_url and base_url, its registry base_url, and defaults.
func (sm *SessionManager) providerOwnBases(providerID string, defaults ...string) []string {
	out := append([]string{}, defaults...)
	if providerID == "" {
		return out
	}
	if sm != nil && sm.db != nil {
		var custom, base string
		if err := sm.db.QueryRow("SELECT COALESCE(custom_url, ''), COALESCE(base_url, '') FROM providers WHERE provider_id = ? LIMIT 1", providerID).Scan(&custom, &base); err == nil {
			out = append(out, custom, base)
		}
	}
	if p := providers.GetProviderByID(providerID); p != nil {
		out = append(out, p.BaseURL)
	}
	return out
}

// storedKeyMayReach reports whether a stored key of providerID may be sent to
// base: base is a valid base URL whose origin (scheme, host, port) is one of
// the provider's own (providerOwnBases).
func (sm *SessionManager) storedKeyMayReach(providerID, base string, defaults ...string) bool {
	if _, err := llmclient.ValidateBaseURL(base); err != nil {
		return false
	}
	origin := llmclient.Origin(base)
	if origin == "" {
		return false
	}
	for _, own := range sm.providerOwnBases(providerID, defaults...) {
		if strings.TrimSpace(own) != "" && llmclient.Origin(own) == origin {
			return true
		}
	}
	return false
}

// sessionKeyForCustomBase is the key a session gets for a base URL that is
// not its provider's own: its own session key, or the placeholder.
func sessionKeyForCustomBase(s *Session, providerID, base string) string {
	if s.Options.APIKey != nil && *s.Options.APIKey != "" {
		return *s.Options.APIKey
	}
	logging.Warning("SECURITY: session %s points provider %q at %s, which is not that provider's own host; its stored API key is not sent there",
		s.ID, providerID, llmclient.Origin(base))
	return noStoredKeyPlaceholder
}

// claudeDefaultBase is where a Claude CLI provider's key goes when its
// providers row has no URL of its own.
func claudeDefaultBase(providerID string) []string {
	switch providerID {
	case "glm":
		return []string{"https://api.z.ai/api/anthropic"}
	case "deepseek":
		return []string{"https://api.deepseek.com"}
	case "openai":
		return []string{"https://api.openai.com/v1"}
	}
	return []string{"https://api.anthropic.com"}
}

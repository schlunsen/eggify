// Package n0 makes Eggify a first-class n0 client: OAuth connections stored
// (encrypted) in the Eggify database, a Streamable HTTP MCP client for n0's
// /api/v1/mcp endpoint, and a loopback-only proxy that the eggify-tools stdio
// MCP server forwards n0_* tool calls to.
//
// Security model: n0 access and refresh tokens only ever exist inside the
// Eggify server process. Agents (any runtime) see n0's tools through eggify-tools,
// which holds a session-scoped proxy capability — never an n0 credential.
package n0

import (
	"errors"
	"net/url"
	"strings"

	"github.com/schlunsen/eggify/internal/n0/oauth"
)

// Host is a known n0 instance.
type Host struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	URL     string `json:"url"`
	Default bool   `json:"default,omitempty"`
}

// DefaultHost is the public n0 instance.
const DefaultHost = "https://nzero.pro"

// KnownHosts lists the n0 instances offered in the UI.
var KnownHosts = []Host{
	{ID: "nzero", Label: "n0", URL: DefaultHost, Default: true},
}

// MCPPath is where n0 serves its MCP endpoint.
const MCPPath = "/api/v1/mcp"

// NormalizeHost validates a user supplied n0 base URL and returns
// "scheme://host[:port]". https is required except for loopback dev hosts.
func NormalizeHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultHost, nil
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("invalid n0 URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("the n0 URL must be a plain https://host")
	}
	host := strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
	if err := oauth.CheckEndpoint(host); err != nil {
		return "", errors.New("the n0 URL must use https://")
	}
	return host, nil
}

// MCPURL returns the MCP endpoint of an n0 host.
func MCPURL(host string) string { return strings.TrimSuffix(host, "/") + MCPPath }

// ConnectedToolsURL links to n0's Security page, where the "Connected tools"
// tab lists (and revokes) connections. n0 has no deep link to the tab itself.
func ConnectedToolsURL(host string) string {
	return strings.TrimSuffix(host, "/") + "/api-tokens"
}

// ReauthMessage is the tool error shown when a connection can no longer be used.
const ReauthMessage = "n0 connection expired or was disconnected — reconnect it in Eggify → Connectors."

// SessionHint is added to the system prompt of sessions that have n0 enabled.
const SessionHint = "You can build and deploy the user's n0 app with the n0_* tools. Call n0_connection_info first and read n0_app_guide before creating a new app. Don't ask the user for API keys."

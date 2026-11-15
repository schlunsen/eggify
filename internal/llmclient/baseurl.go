package llmclient

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// ErrInvalidBaseURL is returned by ValidateBaseURL.
var ErrInvalidBaseURL = errors.New("invalid base URL")

// ValidateBaseURL checks a model-provider base URL (a provider's custom_url or
// a session's base_url) and returns it normalised:
//   - http or https only, with a host and no user:password@;
//   - https is required except for loopback hosts (localhost, 127.0.0.0/8,
//     ::1), so local LLM servers (Ollama, LiteLLM, vLLM) keep working;
//   - the fragment is dropped, surrounding whitespace trimmed.
//
// Private/LAN addresses are allowed over https (and loopback over http):
// blocking them would break LAN LLM servers. Stored provider keys are kept
// away from hosts other than the provider's own elsewhere (agents package).
func ValidateBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrInvalidBaseURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ErrInvalidBaseURL
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("invalid base URL: only http and https are allowed")
	}
	if u.Host == "" || u.Hostname() == "" || u.Opaque != "" {
		return "", errors.New("invalid base URL: missing host")
	}
	if u.User != nil {
		return "", errors.New("invalid base URL: credentials in the URL are not allowed")
	}
	if strings.ContainsAny(u.Host, " \t\r\n") {
		return "", ErrInvalidBaseURL
	}
	if scheme == "http" && !IsLoopbackHost(u.Hostname()) {
		return "", errors.New("invalid base URL: https is required (plain http only for localhost)")
	}
	u.Scheme = scheme
	u.Fragment = ""
	u.RawFragment = ""
	return u.String(), nil
}

// IsLoopbackHost reports whether host (no port) is localhost or a loopback IP.
func IsLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Origin returns scheme://host:port of a URL with default ports made
// explicit, lower-cased, or "" when raw is not an http(s) URL with a host.
// Two URLs with the same Origin reach the same server.
func Origin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	switch scheme {
	case "https":
		if port == "" {
			port = "443"
		}
	case "http":
		if port == "" {
			port = "80"
		}
	default:
		return ""
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	return scheme + "://" + net.JoinHostPort(host, port)
}

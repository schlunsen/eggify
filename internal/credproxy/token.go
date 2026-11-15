// Package credproxy keeps model-provider API keys inside the Eggify server.
//
// Agent runtimes that call a model API themselves (the Claude CLI, the Codex
// app-server) are pointed at a loopback reverse proxy instead of the real
// provider and given a per-session capability instead of the real key. The
// proxy checks the capability, looks up the session's CURRENT provider (base
// URL + key) on every request, swaps the auth header and streams the request
// and response through untouched.
//
// An agent that dumps its environment therefore only finds a capability that:
//   - is bound to one session and one API kind,
//   - only works from this machine (loopback peer, no forwarding headers),
//   - only reaches the provider API paths, and
//   - dies with the Eggify server process (the HMAC key lives only in memory).
package credproxy

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
)

// Kind is the API family a capability and route are for.
type Kind string

const (
	// KindAnthropic is the Anthropic Messages API (Claude CLI sessions,
	// including Anthropic-compatible providers such as DeepSeek, GLM, Kimi).
	KindAnthropic Kind = "anthropic"
	// KindOpenAI is the OpenAI Responses API used by the Codex CLI.
	KindOpenAI Kind = "openai"
)

// TokenPrefix marks an Eggify provider capability. It lets other code recognise a
// capability (e.g. to avoid overwriting it) without verifying it.
const TokenPrefix = "eggify-cap1."

var (
	keyOnce sync.Once
	hmacKey []byte
)

func key() []byte {
	keyOnce.Do(func() {
		hmacKey = make([]byte, 32)
		if _, err := rand.Read(hmacKey); err != nil {
			panic("credproxy: cannot generate capability key: " + err.Error())
		}
	})
	return hmacKey
}

func mac(sessionID string, kind Kind) string {
	m := hmac.New(sha256.New, key())
	m.Write([]byte("eggify-provider-proxy:v1:" + string(kind) + ":" + sessionID))
	return hex.EncodeToString(m.Sum(nil))
}

// Token returns the capability a session's runtime uses in place of the real
// API key for the given API kind.
func Token(sessionID string, kind Kind) string {
	return TokenPrefix + sessionID + "." + mac(sessionID, kind)
}

// VerifyToken checks a capability for the given API kind in constant time and
// returns the session it belongs to.
func VerifyToken(token string, kind Kind) (sessionID string, ok bool) {
	if !strings.HasPrefix(token, TokenPrefix) {
		return "", false
	}
	rest := token[len(TokenPrefix):]
	i := strings.LastIndexByte(rest, '.')
	if i <= 0 || i == len(rest)-1 {
		return "", false
	}
	sessionID, sig := rest[:i], rest[i+1:]
	if !hmac.Equal([]byte(mac(sessionID, kind)), []byte(sig)) {
		return "", false
	}
	return sessionID, true
}

// IsToken reports whether s looks like an Eggify provider capability.
func IsToken(s string) bool { return strings.HasPrefix(s, TokenPrefix) }

// Package capability issues session-scoped capabilities for loopback routes
// that a session's eggify-tools process calls (for example /internal/apps).
//
// A capability is an HMAC of (purpose, session id) under a random key that
// lives only in the Eggify server's memory, like n0.ProxyToken. It is handed to
// the eggify-tools process of ONE session through its environment, so:
//   - it is useless for another purpose or another session,
//   - it dies with the server process, and
//   - an arbitrary local process (or an agent that only read ~/.claude/eggify)
//     cannot mint one.
package capability

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// Purposes.
const (
	// Apps authorizes /internal/apps (deploy_app, list_deployed_apps, undeploy_app).
	Apps = "apps"
)

// Environment variables of a session's eggify-tools process.
const (
	EnvSessionID = "EGGIFY_SESSION_ID" // same name n0 uses
	// EnvCoreTools ("1") makes eggify-tools serve its own Bash/Read/Write/Edit/
	// Glob/Grep; only the direct (OpenAI-compatible) loop sets it.
	EnvCoreTools = "EGGIFY_TOOLS_CORE"
	EnvAppsToken = "EGGIFY_APPS_TOKEN"
)

// HeaderSessionID carries the session id next to "Authorization: Bearer <capability>".
const HeaderSessionID = "X-Eggify-Session-Id"

var (
	keyOnce sync.Once
	key     []byte
)

func hmacKey() []byte {
	keyOnce.Do(func() {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			panic("capability: cannot generate key: " + err.Error())
		}
	})
	return key
}

// Token returns the capability for purpose and sessionID.
func Token(purpose, sessionID string) string {
	mac := hmac.New(sha256.New, hmacKey())
	mac.Write([]byte("eggify-capability:v1:" + purpose + ":" + sessionID))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a capability in constant time.
func Verify(purpose, sessionID, token string) bool {
	if purpose == "" || sessionID == "" || token == "" {
		return false
	}
	return hmac.Equal([]byte(Token(purpose, sessionID)), []byte(token))
}

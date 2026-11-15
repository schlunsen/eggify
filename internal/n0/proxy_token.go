package n0

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
)

// The proxy capability lets the eggify-tools process of ONE session invoke that
// session's n0 tools through the Eggify server. It is an HMAC of the session id
// under a random key that lives only in the Eggify server's memory, so it:
//   - is useless for any other session,
//   - dies with the server process (agents die with it too), and
//   - is not an n0 credential: there is no route that turns it into a token.
var (
	proxyKeyOnce sync.Once
	proxyKey     []byte
)

func proxyHMACKey() []byte {
	proxyKeyOnce.Do(func() {
		proxyKey = make([]byte, 32)
		if _, err := rand.Read(proxyKey); err != nil {
			panic("n0: cannot generate proxy key: " + err.Error())
		}
	})
	return proxyKey
}

// ProxyToken returns the proxy capability for a session.
func ProxyToken(sessionID string) string {
	mac := hmac.New(sha256.New, proxyHMACKey())
	mac.Write([]byte("eggify-n0-proxy:v1:" + sessionID))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyProxyToken checks a proxy capability in constant time.
func VerifyProxyToken(sessionID, token string) bool {
	if sessionID == "" || token == "" {
		return false
	}
	return hmac.Equal([]byte(ProxyToken(sessionID)), []byte(token))
}

// Environment variable names the eggify-tools process reads.
const (
	EnvSessionID  = "EGGIFY_SESSION_ID"
	EnvProxyURL   = "EGGIFY_N0_PROXY_URL"
	EnvProxyToken = "EGGIFY_N0_PROXY_TOKEN"
	// EnvControlURL is the loopback base URL of Eggify's session control
	// endpoints (ControlPath*). eggify-tools authenticates to them with the same
	// session capability (EnvProxyToken) it uses for the n0 proxy.
	EnvControlURL = "EGGIFY_CONTROL_URL"
)

// Session control endpoints served on the same loopback listener. They let a
// session's eggify-tools process ask the Eggify server — which enforces the rules —
// instead of acting on eggify.db itself.
const (
	ControlPathPrefix         = "/internal/eggify/"
	ControlToolGuardPath      = "/internal/eggify/tool-guard"
	ControlHandoverCreatePath = "/internal/eggify/handover/create"
	ControlHandoverAcceptPath = "/internal/eggify/handover/accept"
	ControlLoopStartPath      = "/internal/eggify/loop/start"
	ControlLoopGetPath        = "/internal/eggify/loop/get"
	ControlLoopMessagePath    = "/internal/eggify/loop/message"
	ControlLoopToParentPath   = "/internal/eggify/loop/message-parent"
	ControlWorkerStartPath    = "/internal/eggify/worker/start"
)

// ProxyEnv is the environment given to a session's eggify-tools process.
// proxyURL is empty when the Eggify server has no n0 proxy running.
func ProxyEnv(sessionID, proxyURL string) map[string]string {
	if sessionID == "" || proxyURL == "" {
		return nil
	}
	return map[string]string{
		EnvSessionID:  sessionID,
		EnvProxyURL:   proxyURL,
		EnvProxyToken: ProxyToken(sessionID),
		EnvControlURL: strings.TrimSuffix(proxyURL, ProxyPath),
	}
}

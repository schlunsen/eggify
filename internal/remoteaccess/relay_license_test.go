package remoteaccess

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schlunsen/eggify/internal/relay"
)

func relayToken(t *testing.T, server ed25519.PrivateKey, install ed25519.PublicKey, expires time.Time) string {
	t.Helper()
	enc := base64.RawURLEncoding
	sum := sha256.Sum256(install)
	payload, _ := json.Marshal(map[string]any{
		"v": 1, "install": enc.EncodeToString(sum[:]), "features": []string{"app", "relay"},
		"issued_at": time.Now().Unix(), "expires_at": expires.Unix(), "relay_until": nil,
	})
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(ed25519.Sign(server, payload))
}

// A relay refusal for the license (none yet, then an expired token) does not
// leave the tunnel off or waiting for a long backoff once the license changes.
func TestRelayMode_RetriesWhenTheLicenseChanges(t *testing.T) {
	serverPub, serverKey, _ := ed25519.GenerateKey(rand.Reader)
	_, installKey, _ := ed25519.GenerateKey(rand.Reader)
	installPub := installKey.Public().(ed25519.PublicKey)
	relayAddr, pool := startTestRelayWith(t, func(c *relay.ServerConfig) {
		c.LicenseKey = serverPub
		c.Allowlist = &relay.Allowlist{Path: filepath.Join(t.TempDir(), "none.txt")}
	})
	var creds atomic.Pointer[relay.LicenseCredentials]
	store, _ := NewStore(filepath.Join(t.TempDir(), "x.json"), make([]byte, 32))
	m := NewManager(Config{
		RelayProvider: &RelayProvider{RootCAs: pool, ServerName: "relay.test",
			License: func() *relay.LicenseCredentials { return creds.Load() }},
		Serve: func(ln net.Listener) error {
			return http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
		},
		Store: store, MinBackoff: time.Minute, MaxBackoff: time.Minute, // a retry that waits for the backoff fails the test
	})
	mode := ModeRelay
	if _, err := m.UpdateSettings(Update{Mode: &mode}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.settings.RelayAddr = relayAddr
	m.mu.Unlock()
	defer m.Stop()

	// No license: a permanent refusal, the tunnel stays off.
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "refused", func() bool { return m.Status().Status == StateError && m.Status().ErrorCode == "relay_refused" })

	// An expired token: refused, but not permanent (retrying with backoff).
	creds.Store(&relay.LicenseCredentials{Token: relayToken(t, serverKey, installPub, time.Now().Add(-time.Minute)), Key: installKey})
	m.RetryAfterLicenseChange()
	waitFor(t, "expired, retrying", func() bool {
		s := m.Status()
		return s.Status == StateConnecting && s.Error != ""
	})

	// A renewed token: the license change reconnects at once.
	creds.Store(&relay.LicenseCredentials{Token: relayToken(t, serverKey, installPub, time.Now().Add(time.Hour)), Key: installKey})
	m.RetryAfterLicenseChange()
	waitFor(t, "connected", func() bool { return m.Status().Status == StateConnected })
}

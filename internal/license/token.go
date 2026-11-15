package license

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ServerPublicKeyBase64 is the license server's Ed25519 public key (raw 32
// bytes, standard base64), from docs/licensing-protocol.md.
const ServerPublicKeyBase64 = "LbjYx4fbRqbyIzHknzQ09vRBliThR1ZH03+Dp3QgJP4="

// serverPublicKey is the built-in key. Only this package's tests replace it.
var serverPublicKey = mustPublicKey(ServerPublicKeyBase64)

// extraKeys are further accepted server keys (key rotation: ship the new
// key next to the built-in one, switch the server over, drop the old one in
// a later release). The built-in key is always accepted.
var (
	extraKeysMu sync.RWMutex
	extraKeys   []ed25519.PublicKey
)

// AddServerPublicKeys accepts further server public keys (raw 32 bytes,
// standard base64) next to the built-in one. An invalid entry is an error
// and none is added.
func AddServerPublicKeys(b64 ...string) error {
	var keys []ed25519.PublicKey
	for _, s := range b64 {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return errors.New("license: server public key must be 32 raw bytes in standard base64")
		}
		keys = append(keys, ed25519.PublicKey(raw))
	}
	extraKeysMu.Lock()
	extraKeys = append(extraKeys, keys...)
	extraKeysMu.Unlock()
	return nil
}

// ServerPublicKeys is every key a token may be signed with: the built-in
// one first, then the added ones.
func ServerPublicKeys() []ed25519.PublicKey {
	extraKeysMu.RLock()
	defer extraKeysMu.RUnlock()
	return append([]ed25519.PublicKey{serverPublicKey}, extraKeys...)
}

func mustPublicKey(b64 string) ed25519.PublicKey {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		panic("license: bad server public key")
	}
	return ed25519.PublicKey(raw)
}

// FeatureApp is the feature the desktop app needs.
const FeatureApp = "app"

// Claims is the payload of a license token.
type Claims struct {
	V         int      `json:"v"`
	SerialID  string   `json:"serial_id"`
	Install   string   `json:"install"`
	Plan      string   `json:"plan"`
	Features  []string `json:"features"`
	Machine   string   `json:"machine"`
	IssuedAt  int64    `json:"issued_at"`
	ExpiresAt int64    `json:"expires_at"`
	// RelayUntil ends the relay subscription (unix seconds); nil = none.
	RelayUntil *int64 `json:"relay_until,omitempty"`
}

// RelayEntitled reports whether the token covers the relay at now: feature
// "relay", and relay_until (when set) not passed.
func (c *Claims) RelayEntitled(now time.Time) bool {
	if !c.Has("relay") {
		return false
	}
	return c.RelayUntil == nil || now.Unix() < *c.RelayUntil
}

// Expires is ExpiresAt as a time.
func (c *Claims) Expires() time.Time { return time.Unix(c.ExpiresAt, 0) }

// Has reports whether the token covers feature.
func (c *Claims) Has(feature string) bool { return slices.Contains(c.Features, feature) }

// Token verification errors. ErrTokenExpired comes with the claims (the
// token is genuine, only too old); the others mean the token can't be used.
var (
	ErrTokenMalformed    = errors.New("the license is damaged")
	ErrTokenSignature    = errors.New("the license signature is not valid")
	ErrTokenVersion      = errors.New("the license is from a newer version of Eggify")
	ErrTokenWrongInstall = errors.New("the license belongs to another computer")
	ErrTokenExpired      = errors.New("the license has expired")
)

// VerifyToken checks a token for installID at now: signature with the
// server public key, v == 1, install == installID, now < expires_at. On
// ErrTokenExpired the claims are returned too.
func VerifyToken(token, installID string, now time.Time) (*Claims, error) {
	return verifyToken(token, installID, now, ServerPublicKeys()...)
}

// VerifyTokenWithKeys is VerifyToken with an explicit set of accepted server
// keys. The relay uses it, so app and relay verify tokens identically.
func VerifyTokenWithKeys(token, installID string, now time.Time, keys ...ed25519.PublicKey) (*Claims, error) {
	return verifyToken(token, installID, now, keys...)
}

func verifyToken(token, installID string, now time.Time, keys ...ed25519.PublicKey) (*Claims, error) {
	payloadB64, sigB64, ok := strings.Cut(token, ".")
	if !ok || payloadB64 == "" || sigB64 == "" || strings.Contains(sigB64, ".") {
		return nil, ErrTokenMalformed
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, ErrTokenMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, ErrTokenMalformed
	}
	verified := false
	for _, pub := range keys {
		// ed25519.Verify panics on a key of the wrong size.
		if len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, payload, sig) {
			verified = true
			break
		}
	}
	if !verified {
		return nil, ErrTokenSignature
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, ErrTokenMalformed
	}
	if c.V != 1 {
		return nil, ErrTokenVersion
	}
	if c.Install != installID {
		return nil, ErrTokenWrongInstall
	}
	if now.Unix() >= c.ExpiresAt {
		return &c, ErrTokenExpired
	}
	return &c, nil
}

// InstallKeyString is install_key on the wire: the raw 32-byte public key,
// base64url without padding.
func InstallKeyString(pub ed25519.PublicKey) string {
	return base64.RawURLEncoding.EncodeToString(pub)
}

// InstallID is base64url_nopad(sha256(raw public key)).
func InstallID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// SignedMessage is the message a signed request signs:
// "eggify-license-v1\n" + action + "\n" + install_key + "\n" + timestamp +
// "\n" + serial (serial only for activate, "" otherwise).
func SignedMessage(action, installKey string, timestamp int64, serial string) string {
	return "eggify-license-v1\n" + action + "\n" + installKey + "\n" +
		strconv.FormatInt(timestamp, 10) + "\n" + serial
}

// SignRequest returns base64url_nopad(Ed25519(priv, SignedMessage(...))).
func SignRequest(priv ed25519.PrivateKey, action string, timestamp int64, serial string) string {
	pub := priv.Public().(ed25519.PublicKey)
	msg := SignedMessage(action, InstallKeyString(pub), timestamp, serial)
	return base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(msg)))
}

// Package relay is Eggify's own remote-access relay: a small public server
// that forwards encrypted connections to Eggify installs by name, without
// being able to read them.
//
//	phone ──TLS (Eggify's own certificate)──▶ relay ──same bytes──▶ Eggify
//	                                         (routes by SNI only)
//
// Each install has a TLS identity (an ECDSA P-256 key and a self-signed
// certificate) created on the Mac. Its public address is derived from the
// key, https://<ID>.<suffix>, so names need no registration: the install
// proves to the relay that it holds the key whose hash is the name. The
// iOS app pins the key's hash (Pin), which it gets from the pairing QR code,
// so neither the relay nor any certificate authority can impersonate the
// Mac.
package relay

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// IDLength is the number of base32 characters in an install's name
// (160 bits of the key hash).
const IDLength = 32

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Identity is an install's TLS identity.
type Identity struct {
	Certificate tls.Certificate
	// SPKI is the DER SubjectPublicKeyInfo of the key.
	SPKI []byte
}

// GenerateIdentityPEM creates a new P-256 key and returns it PEM encoded
// (the only thing that needs storing; the certificate is rebuilt from it).
func GenerateIdentityPEM() (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})), nil
}

// ParseIdentity loads a key made by GenerateIdentityPEM and builds its
// self-signed certificate. Clients never check the certificate's names,
// dates or issuer, only its key (Pin), so a fresh certificate per start is
// fine.
func ParseIdentity(keyPEM string) (*Identity, error) {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return nil, errors.New("relay identity: not an EC private key")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("relay identity: %w", err)
	}
	if key.Curve != elliptic.P256() {
		return nil, errors.New("relay identity: key must be P-256")
	}
	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Eggify " + IDFromSPKI(spki)},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return &Identity{
		Certificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		SPKI:        spki,
	}, nil
}

// ID is the install's name at the relay.
func (id *Identity) ID() string { return IDFromSPKI(id.SPKI) }

// Pin is what clients pin: base64url (no padding) of SHA-256(SPKI).
func (id *Identity) Pin() string { return PinFromSPKI(id.SPKI) }

// IDFromSPKI is the lower-case base32 of the first 20 bytes of SHA-256(spki).
func IDFromSPKI(spki []byte) string {
	sum := sha256.Sum256(spki)
	return strings.ToLower(idEncoding.EncodeToString(sum[:20]))
}

// PinFromSPKI is base64url (no padding) of SHA-256(spki).
func PinFromSPKI(spki []byte) string {
	sum := sha256.Sum256(spki)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// IDFromPin is the install name a pin belongs to (IDFromSPKI of the same
// key), so a client can check that an address and a pin go together.
func IDFromPin(pin string) (string, error) {
	sum, err := base64.RawURLEncoding.DecodeString(pin)
	if err != nil || len(sum) != sha256.Size {
		return "", errors.New("relay: malformed pin")
	}
	return strings.ToLower(idEncoding.EncodeToString(sum[:20])), nil
}

// ValidID reports whether s looks like an install name.
func ValidID(s string) bool {
	if len(s) != IDLength {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '2' && r <= '7') {
			return false
		}
	}
	return true
}

// ServerTLSConfig is the TLS config an install serves its clients with.
func (id *Identity) ServerTLSConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{id.Certificate},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"},
	}
}

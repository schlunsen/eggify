package relay

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/schlunsen/eggify/internal/license"
)

// Who may use a relay (docs/licensing-protocol.md, "Relay admission"): an
// install on the allowlist, or one with a valid Eggify license token that
// covers "relay" and that proves it holds the token's install key. A relay
// configured with neither admits everyone (tests, self-hosted relays).
//
// Admission is not forever: the relay remembers when it ends (the token's
// expires_at or relay_until, whichever is first; never for allowlisted
// installs, which are re-checked against the file instead) and closes the
// install's session then (Server.watchAdmissions).

// SubscribersOnlyMessage is the permanent refusal an install without a license gets.
const SubscribersOnlyMessage = "This relay is for Eggify subscribers. Activate your license, or use ngrok in Settings → Remote access."

// Refusal codes (resultMsg.Code). Old apps ignore the code and read only the
// message and the permanent flag.
const (
	// CodeSubscribersOnly: no usable license (permanent until the license changes).
	CodeSubscribersOnly = "subscribers_only"
	// CodeLicenseExpired: the token is genuine but expired; renewing fixes it.
	CodeLicenseExpired = "license_expired"
	// CodeRelayEnded: the token's relay_until has passed; a renewed token may extend it.
	CodeRelayEnded = "relay_ended"
	// CodeTooManyInstalls: this license already runs the most relay addresses.
	CodeTooManyInstalls = "too_many_installs"
	// CodeBadVersion / CodeBadProof / CodeRelayFull: protocol-level refusals.
	CodeBadVersion = "bad_version"
	CodeBadProof   = "bad_proof"
	CodeRelayFull  = "relay_full"
)

// Messages for the refusals that retrying (or renewing) can fix.
const (
	licenseExpiredMessage  = "Your Eggify license has expired. Connect to the license server (Settings → License → Check now) and Eggify will reconnect."
	relayEndedMessage      = "Your relay subscription has ended. Renew it, or use ngrok in Settings → Remote access."
	tooManyInstallsMessage = "This license already uses the most relay addresses it may run at once. Switch the other installs off, or use ngrok."
)

// DefaultMaxIDsPerLicense is how many relay addresses (install ids) one
// license install may hold at the same time.
const DefaultMaxIDsPerLicense = 3

var b64url = base64.RawURLEncoding

// Allowlist is a file of relay install ids, one per line (# comments). The
// file is read on every lookup and parsed again only when its content hash
// changed (modification time and size miss same-size edits within one
// timestamp tick).
type Allowlist struct {
	Path string

	mu   sync.Mutex
	hash [sha256.Size]byte
	ids  map[string]bool
}

// load returns the listed ids. An error (missing, unreadable) is never
// cached: the next call reads the file again.
func (a *Allowlist) load() (map[string]bool, error) {
	if a == nil || a.Path == "" {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(a.Path)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.ids, a.hash = nil, [sha256.Size]byte{}
		return nil, err
	}
	sum := sha256.Sum256(data)
	if a.ids == nil || sum != a.hash {
		a.ids, a.hash = parseAllowlist(data), sum
	}
	return a.ids, nil
}

// Has reports whether id is listed. A missing or unreadable file lists nobody.
func (a *Allowlist) Has(id string) bool {
	ids, err := a.load()
	return err == nil && ids[strings.ToLower(id)]
}

func parseAllowlist(data []byte) map[string]bool {
	ids := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if line = strings.ToLower(strings.TrimSpace(line)); line != "" {
			ids[line] = true
		}
	}
	return ids
}

// licenseProofMessage is what an install signs with its license key to show
// it holds the key its token names (bound to this relay and this nonce).
// Version 1; kept for relays and apps that predate the bound proof.
func licenseProofMessage(relayName string, nonce []byte) []byte {
	return []byte("eggify-relay-license-v1\n" + relayName + "\n" + b64url.EncodeToString(nonce))
}

// licenseProofMessageBound is the version 2 proof: it also names the relay
// identity (SHA-256 of the install's SPKI) the license is presented for, so
// the proof can't be reused for another relay address.
func licenseProofMessageBound(relayName string, nonce, spki []byte) []byte {
	sum := sha256.Sum256(spki)
	return []byte("eggify-relay-license-v2\n" + relayName + "\n" + b64url.EncodeToString(nonce) + "\n" + b64url.EncodeToString(sum[:]))
}

// LicenseCredentials is what an install presents: its license token and the
// install key that token was issued to.
type LicenseCredentials struct {
	Token string
	Key   ed25519.PrivateKey
}

// admission is the outcome of the admission check.
type admission struct {
	ok        bool
	reason    string // for the log
	code      string
	message   string // sent to the install
	permanent bool
	// until ends the admission (zero: never).
	until time.Time
	// viaAllowlist: admitted by the allowlist, re-checked against the file.
	viaAllowlist bool
	// licenseInstall is the token's install id (cap on relay ids per license).
	licenseInstall string
}

func refuse(reason, code, message string, permanent bool) admission {
	return admission{reason: reason, code: code, message: message, permanent: permanent}
}

func subscribersOnly(reason string) admission {
	return refuse(reason, CodeSubscribersOnly, SubscribersOnlyMessage, true)
}

// licenseKeys are the server keys that may sign accepted licenses.
func (s *Server) licenseKeys() []ed25519.PublicKey {
	keys := append([]ed25519.PublicKey(nil), s.cfg.LicenseKeys...)
	if len(s.cfg.LicenseKey) > 0 {
		keys = append(keys, s.cfg.LicenseKey)
	}
	return keys
}

// admit decides whether the install may connect.
func (s *Server) admit(id string, hello helloMsg, proof proofMsg, nonce []byte, now time.Time) admission {
	keys := s.licenseKeys()
	if s.cfg.Allowlist == nil && len(keys) == 0 {
		return admission{ok: true}
	}
	if s.cfg.Allowlist.Has(id) {
		return admission{ok: true, viaAllowlist: true}
	}
	if len(keys) == 0 {
		return subscribersOnly("not on the allowlist")
	}
	if hello.License == "" {
		return subscribersOnly("no license")
	}
	rawKey, err := b64url.DecodeString(hello.LicenseKey)
	if err != nil || len(rawKey) != ed25519.PublicKeySize {
		return subscribersOnly("bad license key")
	}
	key := ed25519.PublicKey(rawKey)
	// The same verification the app uses (signature length, extra '.',
	// version, install binding, expiry).
	claims, err := license.VerifyTokenWithKeys(hello.License, license.InstallID(key), now, keys...)
	switch {
	case errors.Is(err, license.ErrTokenExpired):
		return refuse("license expired", CodeLicenseExpired, licenseExpiredMessage, false)
	case err != nil:
		return subscribersOnly(err.Error())
	}
	if !claims.Has("relay") {
		return subscribersOnly("license doesn't include the relay")
	}
	if claims.RelayUntil != nil && now.Unix() >= *claims.RelayUntil {
		return refuse("relay subscription ended", CodeRelayEnded, relayEndedMessage, false)
	}
	if !s.verifyLicenseProof(key, hello, proof, nonce) {
		return subscribersOnly("license key proof failed")
	}
	until := time.Unix(claims.ExpiresAt, 0)
	if claims.RelayUntil != nil && time.Unix(*claims.RelayUntil, 0).Before(until) {
		until = time.Unix(*claims.RelayUntil, 0)
	}
	return admission{ok: true, until: until, licenseInstall: claims.Install}
}

// verifyLicenseProof checks that the install holds the license key. The bound
// proof (v2) is verified when sent; the plain one (v1, from apps that predate
// v2) is accepted unless RequireBoundLicenseProof is set.
func (s *Server) verifyLicenseProof(key ed25519.PublicKey, hello helloMsg, proof proofMsg, nonce []byte) bool {
	if proof.LicenseSigBound != "" {
		sig, err := b64url.DecodeString(proof.LicenseSigBound)
		return err == nil && ed25519.Verify(key, licenseProofMessageBound(s.cfg.ControlName, nonce, hello.SPKI), sig)
	}
	if s.cfg.RequireBoundLicenseProof {
		return false
	}
	sig, err := b64url.DecodeString(proof.LicenseSig)
	return err == nil && ed25519.Verify(key, licenseProofMessage(s.cfg.ControlName, nonce), sig)
}

// watchAdmissions closes installs whose admission has ended: a license that
// expired or whose relay subscription ended, or an allowlisted install that
// was taken off the file. They reconnect and are admitted (or refused) again.
func (s *Server) watchAdmissions(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.dropEnded(time.Now())
		}
	}
}

func (s *Server) dropEnded(now time.Time) {
	var ended []*install
	var listed map[string]bool
	var listErr error
	loaded := false
	s.mu.Lock()
	for id, in := range s.installs {
		switch {
		case !in.until.IsZero() && !now.Before(in.until):
			ended = append(ended, in)
		case in.viaAllowlist:
			if !loaded {
				listed, listErr = s.cfg.Allowlist.load()
				loaded = true
			}
			// A read error other than "no file" is not a reason to cut anyone off.
			if (listErr == nil && !listed[id]) || errors.Is(listErr, os.ErrNotExist) {
				ended = append(ended, in)
			}
		}
	}
	s.mu.Unlock()
	for _, in := range ended {
		s.cfg.Logf("relay: install %s no longer admitted; closing", in.id)
		_ = in.session.Close()
	}
}

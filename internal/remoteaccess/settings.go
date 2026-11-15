// Package remoteaccess runs Eggify's optional public tunnel (ngrok) as an
// extra listener next to the server's normal one, and keeps its settings.
//
// The tunnel can be started and stopped at any time while the server keeps
// serving its local listener (CLI: 127.0.0.1:3333, desktop: the webview's
// loopback listener). See Manager.
package remoteaccess

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Protection modes for ngrok's edge (in front of Eggify's own login, which
// always stays on).
const (
	ProtectionNone  = "none"
	ProtectionOAuth = "oauth"
	ProtectionBasic = "basic"
)

// oauthProviders are the ngrok-managed OAuth providers offered in the UI.
var oauthProviders = map[string]bool{
	"google": true, "github": true, "microsoft": true, "gitlab": true,
}

// Protection is optional edge protection enforced by ngrok before a request
// reaches Eggify.
type Protection struct {
	Mode string `json:"mode"` // none | oauth | basic
	// OAuth: ngrok's managed OAuth app for the provider; only the listed
	// emails/domains get through (at least one is required).
	OAuthProvider string   `json:"oauth_provider,omitempty"`
	AllowEmails   []string `json:"allow_emails,omitempty"`
	AllowDomains  []string `json:"allow_domains,omitempty"`
	// Basic auth. Note: it occupies the Authorization header, so the iOS app
	// (Bearer session token) can't get through it.
	BasicUser     string `json:"basic_user,omitempty"`
	BasicPassword string `json:"basic_password,omitempty"`
}

// Settings are the persisted remote access settings.
type Settings struct {
	AuthToken  string     `json:"authtoken,omitempty"`
	Domain     string     `json:"domain,omitempty"`
	Autostart  bool       `json:"autostart"`
	Protection Protection `json:"protection"`
	// APIKey is an optional ngrok API key (dashboard.ngrok.com/api-keys, not
	// the authtoken). With it Eggify lists and stops the account's agent
	// sessions. Never sent to a client (only api_key_set + last 4).
	APIKey string `json:"api_key,omitempty"`
	// InstallID identifies this Eggify install in the agent session metadata.
	InstallID string `json:"install_id,omitempty"`
	// Mode picks the tunnel: ModeNgrok ("" for older files) or ModeRelay.
	Mode string `json:"mode,omitempty"`
	// RelayAddr is the relay's host:port ("" = the build's default relay).
	RelayAddr string `json:"relay_addr,omitempty"`
	// RelayKey is this install's TLS identity for the relay (PEM, P-256).
	// Its public key's hash is the install's relay address and the pin in
	// pairing QR codes, so it is created once and kept.
	RelayKey  string    `json:"relay_key,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Tunnel modes.
const (
	ModeNgrok = "ngrok"
	// ModeRelay: Eggify's own relay forwards encrypted connections it can't
	// read; TLS ends in Eggify with the install's own key, which paired
	// phones pin.
	ModeRelay = "relay"
)

// NormalizeMode maps "" to ModeNgrok and refuses unknown modes.
func NormalizeMode(mode string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(mode)); m {
	case "", ModeNgrok:
		return ModeNgrok, nil
	case ModeRelay:
		return ModeRelay, nil
	default:
		return "", fmt.Errorf("unknown remote access mode %q (use ngrok or relay)", mode)
	}
}

// ErrInvalidRelayAddr is returned for a malformed relay address.
var ErrInvalidRelayAddr = errors.New("the relay address must be a host name, optionally with a port (relay.example.com:8443)")

// NormalizeRelayAddr returns host:port ("" stays "": the default relay).
// The port defaults to 443.
func NormalizeRelayAddr(addr string) (string, error) {
	a := strings.ToLower(strings.TrimSpace(addr))
	a = strings.TrimPrefix(a, "https://")
	a = strings.TrimSuffix(a, "/")
	if a == "" {
		return "", nil
	}
	host, port := a, "443"
	if h, p, err := net.SplitHostPort(a); err == nil {
		host, port = h, p
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", ErrInvalidRelayAddr
	}
	if d, err := NormalizeDomain(host); err != nil || d == "" {
		return "", ErrInvalidRelayAddr
	}
	return net.JoinHostPort(host, strconv.Itoa(n)), nil
}

// TokenHint is "…" plus the token's last 4 characters ("" without a token).
func TokenHint(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 8 {
		return "…"
	}
	return "…" + token[len(token)-4:]
}

// Validation errors.
var (
	ErrInvalidAPIKey = errors.New("the ngrok API key looks invalid (create one at dashboard.ngrok.com/api-keys; it is not the authtoken)")
	ErrInvalidToken  = errors.New("the ngrok authtoken looks invalid (paste it from dashboard.ngrok.com → Your Authtoken)")
	ErrInvalidDomain = errors.New("the domain must be a host name such as myapp.ngrok.app")
)

// NormalizeToken trims and checks an authtoken.
func NormalizeToken(token string) (string, error) {
	token = strings.TrimSpace(token)
	if len(token) < 10 || len(token) > 512 {
		return "", ErrInvalidToken
	}
	for _, r := range token {
		if r <= ' ' || r == 0x7f || r > 0x7e {
			return "", ErrInvalidToken
		}
	}
	return token, nil
}

// NormalizeAPIKey trims and checks an ngrok API key.
func NormalizeAPIKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if len(key) < 10 || len(key) > 512 {
		return "", ErrInvalidAPIKey
	}
	for _, r := range key {
		if r <= ' ' || r == 0x7f || r > 0x7e {
			return "", ErrInvalidAPIKey
		}
	}
	return key, nil
}

// NormalizeDomain trims a reserved domain ("" = random ngrok URL). A pasted
// URL ("https://x.ngrok.app/") is reduced to its host.
func NormalizeDomain(domain string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(domain))
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	d = strings.TrimSuffix(d, "/")
	if d == "" {
		return "", nil
	}
	if len(d) > 253 || !strings.Contains(d, ".") || strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") {
		return "", ErrInvalidDomain
	}
	for _, r := range d {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
		default:
			return "", ErrInvalidDomain
		}
	}
	return d, nil
}

// Normalize validates p and returns its canonical form.
func (p Protection) Normalize() (Protection, error) {
	out := Protection{Mode: strings.ToLower(strings.TrimSpace(p.Mode))}
	switch out.Mode {
	case "", ProtectionNone:
		return Protection{Mode: ProtectionNone}, nil
	case ProtectionOAuth:
		out.OAuthProvider = strings.ToLower(strings.TrimSpace(p.OAuthProvider))
		if !oauthProviders[out.OAuthProvider] {
			return Protection{}, fmt.Errorf("unsupported OAuth provider %q (use google, github, microsoft or gitlab)", p.OAuthProvider)
		}
		for _, e := range p.AllowEmails {
			e = strings.ToLower(strings.TrimSpace(e))
			if e == "" {
				continue
			}
			if a, err := mail.ParseAddress(e); err != nil || a.Address != e {
				return Protection{}, fmt.Errorf("invalid email %q", e)
			}
			out.AllowEmails = append(out.AllowEmails, e)
		}
		for _, d := range p.AllowDomains {
			d = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(d)), "@")
			if d == "" {
				continue
			}
			nd, err := NormalizeDomain(d)
			if err != nil || nd == "" {
				return Protection{}, fmt.Errorf("invalid email domain %q", d)
			}
			out.AllowDomains = append(out.AllowDomains, nd)
		}
		if len(out.AllowEmails) == 0 && len(out.AllowDomains) == 0 {
			return Protection{}, errors.New("OAuth protection needs at least one allowed email or domain")
		}
		return out, nil
	case ProtectionBasic:
		out.BasicUser = strings.TrimSpace(p.BasicUser)
		out.BasicPassword = p.BasicPassword
		if out.BasicUser == "" || strings.ContainsAny(out.BasicUser, ": \t\r\n") {
			return Protection{}, errors.New("basic auth needs a user name without spaces or colons")
		}
		if len(out.BasicPassword) < 8 || len(out.BasicPassword) > 128 {
			return Protection{}, errors.New("the basic auth password must be 8-128 characters")
		}
		return out, nil
	default:
		return Protection{}, fmt.Errorf("unknown protection mode %q", p.Mode)
	}
}

// Store keeps Settings in one AES-256-GCM encrypted file. The key is the
// connector encryption key (internal/secretstore: macOS keychain for the
// notarized desktop app, otherwise ~/.claude/connector_encryption.key), the
// same key that protects connector credentials and n0 tokens.
//
// The whole settings document is encrypted, not just the token: GCM
// authenticates it, so a process that can write the file but doesn't hold
// the key can't switch on autostart or swap in its own token/domain.
type Store struct {
	path string
	key  []byte
	mu   sync.Mutex
}

// storeAAD binds ciphertexts to this use of the key.
const storeAAD = "eggify-remote-access-v1"

type storeFile struct {
	Version    int    `json:"version"`
	Ciphertext string `json:"ciphertext"`
}

// NewStore returns a store for path; key must be 32 bytes.
func NewStore(path string, key []byte) (*Store, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("remote access store: key must be 32 bytes, got %d", len(key))
	}
	k := make([]byte, 32)
	copy(k, key)
	return &Store{path: path, key: k}, nil
}

// Path is the settings file.
func (s *Store) Path() string { return s.path }

// Load returns the stored settings (zero Settings when the file is missing).
// A file that doesn't decrypt is an error, never silently ignored.
func (s *Store) Load() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{Protection: Protection{Mode: ProtectionNone}}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var f storeFile
	if err := json.Unmarshal(raw, &f); err != nil || f.Version != 1 {
		return Settings{}, errors.New("remote access settings file is corrupt")
	}
	ct, err := base64.StdEncoding.DecodeString(f.Ciphertext)
	if err != nil {
		return Settings{}, errors.New("remote access settings file is corrupt")
	}
	pt, err := s.open(ct)
	if err != nil {
		return Settings{}, errors.New("remote access settings could not be decrypted (was the encryption key replaced?)")
	}
	var st Settings
	if err := json.Unmarshal(pt, &st); err != nil {
		return Settings{}, errors.New("remote access settings file is corrupt")
	}
	if st.Protection.Mode == "" {
		st.Protection.Mode = ProtectionNone
	}
	return st, nil
}

// Save encrypts and atomically writes st (0600).
func (s *Store) Save(st Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st.UpdatedAt = time.Now().UTC()
	pt, err := json.Marshal(st)
	if err != nil {
		return err
	}
	ct, err := s.seal(pt)
	if err != nil {
		return err
	}
	out, err := json.Marshal(storeFile{Version: 1, Ciphertext: base64.StdEncoding.EncodeToString(ct)})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".remote_access-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

func (s *Store) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Store) seal(pt []byte) ([]byte, error) {
	g, err := s.gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, pt, []byte(storeAAD)), nil
}

func (s *Store) open(ct []byte) ([]byte, error) {
	g, err := s.gcm()
	if err != nil {
		return nil, err
	}
	if len(ct) < g.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	return g.Open(nil, ct[:g.NonceSize()], ct[g.NonceSize():], []byte(storeAAD))
}

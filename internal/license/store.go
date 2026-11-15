package license

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State is what an install keeps about its license, in license.json.
type State struct {
	// InstallKey is the Ed25519 private key (64 bytes: seed + public key),
	// made on the first activation and kept for the life of the install.
	InstallKey []byte `json:"install_key,omitempty"`
	// Token is the current signed license ("" when not activated).
	Token string `json:"token,omitempty"`
	// SerialHint is the display form of the activated serial ("EGG-…-V5N0T").
	SerialHint string `json:"serial_hint,omitempty"`
	Seats      int    `json:"seats,omitempty"`
	SeatsUsed  int    `json:"seats_used,omitempty"`
	// LastChecked is the last time the server issued a token (activate/renew).
	LastChecked time.Time `json:"last_checked,omitzero"`
	// LastAttempt / LastResult describe the latest renewal attempt
	// ("ok", or an error code such as "offline" or "serial_revoked").
	LastAttempt time.Time `json:"last_attempt,omitzero"`
	LastResult  string    `json:"last_result,omitempty"`
	// Notice is a message for the user about the license going away
	// (revoked, deactivated elsewhere); cleared by the next activation.
	Notice    string    `json:"notice,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	// HighWater is the latest time this install has seen (now, token
	// issued_at, last attempt); a clock earlier than it by more than a few
	// minutes counts as rolled back and the license as expired.
	HighWater time.Time `json:"high_water,omitzero"`
}

// PrivateKey returns the install key, or nil when there is none yet.
func (s State) PrivateKey() ed25519.PrivateKey {
	if len(s.InstallKey) != ed25519.PrivateKeySize {
		return nil
	}
	return ed25519.PrivateKey(s.InstallKey)
}

// Store keeps State in one AES-256-GCM encrypted file (0600), encrypted with
// the connector encryption key like the remote access settings
// (internal/remoteaccess.Store): a process that can write the file but
// doesn't hold the key can neither read the install key nor swap the state.
type Store struct {
	path string
	key  []byte
	mu   sync.Mutex
}

// storeAAD binds ciphertexts to this use of the key.
const storeAAD = "eggify-license-v1"

type storeFile struct {
	Version    int    `json:"version"`
	Ciphertext string `json:"ciphertext"`
}

// NewStore returns a store for path; key must be 32 bytes.
func NewStore(path string, key []byte) (*Store, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("license store: key must be 32 bytes, got %d", len(key))
	}
	k := make([]byte, 32)
	copy(k, key)
	return &Store{path: path, key: k}, nil
}

// Path is the license file.
func (s *Store) Path() string { return s.path }

// Load returns the stored state (zero State when the file is missing). A file
// that doesn't decrypt is an error, never silently ignored.
func (s *Store) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var f storeFile
	if err := json.Unmarshal(raw, &f); err != nil || f.Version != 1 {
		return State{}, errors.New("the license file is corrupt")
	}
	ct, err := base64.StdEncoding.DecodeString(f.Ciphertext)
	if err != nil {
		return State{}, errors.New("the license file is corrupt")
	}
	pt, err := s.open(ct)
	if err != nil {
		return State{}, errors.New("the license file could not be decrypted (was the encryption key replaced?)")
	}
	var st State
	if err := json.Unmarshal(pt, &st); err != nil {
		return State{}, errors.New("the license file is corrupt")
	}
	return st, nil
}

// Save encrypts and atomically writes st (0600).
func (s *Store) Save(st State) error {
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
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".license-*")
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

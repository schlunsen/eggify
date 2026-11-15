// Package secretstore keeps the server's key material (the keys that encrypt
// MFA secrets and connector credentials) out of plain files where the
// platform can do better.
//
// Two backends exist:
//
//   - file: the historical behaviour. Each secret is a 0600 file under
//     ~/.claude (any process running as the user, including an agent, can
//     read it).
//   - keychain: a macOS login-keychain item created by the eggify binary through
//     the Security framework (darwin + cgo builds only). The item's ACL trusts
//     only the creating binary, so `cat`, `security find-generic-password -w`
//     or any other binary make macOS ask the user for their password.
//
// The backend is chosen by EGGIFY_SECRET_STORE (or auth.secret_store in
// config.json): "file", "keychain" or "auto" (default). "auto" picks the
// keychain only where it actually protects: a darwin+cgo binary signed with a
// Developer ID team and the hardened runtime (e.g. the notarized desktop
// app's sidecar). Ad-hoc signed builds (Homebrew/CLI releases, `go build`)
// stay on files in auto mode, because their keychain ACL is bound to the
// binary's cdhash (every update prompts for the login password) and, without
// the hardened runtime, code can be injected into them anyway.
//
// Once secrets live in the keychain a marker file (secret_store.json, no
// secret material) records that, so every later eggify start — including a
// differently built binary in auto mode — keeps using the keychain instead of
// silently generating new keys, which would make encrypted data unreadable.
// EGGIFY_SECRET_STORE=file moves the secrets back to files and removes the marker.
package secretstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Names of the secrets managed by the store.
const (
	// MFAEncryptionKey encrypts users' TOTP secrets (~/.claude/eggify/mfa_encryption.key).
	MFAEncryptionKey = "mfa-encryption-key"
	// ConnectorEncryptionKey encrypts connector credentials and n0 tokens
	// (~/.claude/connector_encryption.key).
	ConnectorEncryptionKey = "connector-encryption-key"
)

// Mode selects the backend.
type Mode string

const (
	ModeAuto     Mode = "auto"
	ModeFile     Mode = "file"
	ModeKeychain Mode = "keychain"
)

// EnvMode is the environment variable that overrides the configured mode.
const EnvMode = "EGGIFY_SECRET_STORE"

// markerFile records that secrets were moved to the keychain. It holds no
// secret material.
const markerFile = "secret_store.json"

var (
	// ErrNotFound is returned by a Backend when the item does not exist.
	ErrNotFound = errors.New("secret not found")
	// ErrKeychainUnavailable means this build has no keychain backend.
	ErrKeychainUnavailable = errors.New("the macOS keychain backend is not available in this build (needs darwin and cgo)")
)

// Backend stores opaque secret values by account name.
type Backend interface {
	Name() string
	Get(account string) ([]byte, error) // ErrNotFound when missing
	Set(account string, value []byte) error
	Delete(account string) error // nil when missing
}

// Options configures Open.
type Options struct {
	ClaudeDir string // ~/.claude
	Mode      Mode   // "" = auto
	// Keychain is the keychain backend; nil = none available.
	Keychain Backend
	// Protects reports whether the keychain actually protects secrets for this
	// binary (used by auto mode) and why.
	Protects func() (bool, string)
	Logf     func(format string, args ...any)
}

// Store loads and creates secrets on one backend.
type Store struct {
	claudeDir string
	keychain  Backend // nil = file backend
	reason    string
	logf      func(format string, args ...any)
	mu        sync.Mutex
}

type marker struct {
	Backend string   `json:"backend"`
	Secrets []string `json:"secrets"`
	Note    string   `json:"note,omitempty"`
}

// LegacyPath is where the file backend keeps a secret.
func LegacyPath(claudeDir, name string) string {
	switch name {
	case MFAEncryptionKey:
		return filepath.Join(claudeDir, "eggify", "mfa_encryption.key")
	case ConnectorEncryptionKey:
		return filepath.Join(claudeDir, "connector_encryption.key")
	}
	return filepath.Join(claudeDir, "eggify", "secrets", name)
}

// ParseMode normalises a mode string; unknown values fall back to auto.
func ParseMode(s string) (Mode, bool) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case "", ModeAuto:
		return ModeAuto, true
	case ModeFile:
		return ModeFile, true
	case ModeKeychain:
		return ModeKeychain, true
	}
	return ModeAuto, false
}

// Open picks the backend for opts.ClaudeDir and, when the user switched back
// to files, moves keychain secrets back to files.
func Open(opts Options) (*Store, error) {
	if opts.ClaudeDir == "" {
		return nil, errors.New("secretstore: empty claude dir")
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	if opts.Mode == "" {
		opts.Mode = ModeAuto
	}
	s := &Store{claudeDir: opts.ClaudeDir, logf: opts.Logf}

	m, err := s.readMarker()
	if err != nil {
		return nil, err
	}

	if m != nil {
		// Secrets already live in the keychain: stay there unless the user
		// explicitly asked for files.
		if opts.Keychain == nil {
			return nil, fmt.Errorf("secrets are stored in the macOS keychain (%s) but %w; run a darwin build of eggify with EGGIFY_SECRET_STORE=file to move them back to files",
				s.markerPath(), ErrKeychainUnavailable)
		}
		s.keychain = opts.Keychain
		if opts.Mode == ModeFile {
			if err := s.moveToFiles(m); err != nil {
				return nil, fmt.Errorf("moving secrets from the keychain back to files: %w", err)
			}
			s.keychain = nil
			s.reason = "EGGIFY_SECRET_STORE=file (moved from keychain)"
			return s, nil
		}
		s.reason = "secrets were moved to the keychain earlier (" + s.markerPath() + ")"
		return s, nil
	}

	switch opts.Mode {
	case ModeFile:
		s.reason = "file mode selected"
	case ModeKeychain:
		if opts.Keychain == nil {
			s.reason = "keychain requested but unavailable in this build; using files"
			s.logf("secretstore: %s", s.reason)
		} else {
			s.keychain = opts.Keychain
			s.reason = "keychain mode selected"
			if opts.Protects != nil {
				if ok, why := opts.Protects(); !ok {
					s.logf("secretstore: keychain selected, but protection is limited: %s", why)
				}
			}
		}
	default: // auto
		if opts.Keychain == nil {
			s.reason = "auto: no keychain backend in this build"
		} else if opts.Protects == nil {
			s.reason = "auto: protection unknown; using files"
		} else if ok, why := opts.Protects(); ok {
			s.keychain = opts.Keychain
			s.reason = "auto: " + why
		} else {
			s.reason = "auto: " + why + "; using files (set EGGIFY_SECRET_STORE=keychain to opt in)"
		}
	}
	return s, nil
}

// Backend reports "keychain" or "file".
func (s *Store) Backend() string {
	if s.keychain != nil {
		return s.keychain.Name()
	}
	return "file"
}

// Reason explains why the backend was chosen.
func (s *Store) Reason() string { return s.reason }

func (s *Store) account(name string) string {
	return name + "@" + s.claudeDir
}

func (s *Store) markerPath() string {
	return filepath.Join(s.claudeDir, "eggify", markerFile)
}

func (s *Store) readMarker() (*marker, error) {
	data, err := os.ReadFile(s.markerPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.markerPath(), err)
	}
	var m marker
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", s.markerPath(), err)
	}
	if m.Backend != string(ModeKeychain) {
		return nil, nil
	}
	return &m, nil
}

func (s *Store) writeMarker(m *marker) error {
	sort.Strings(m.Secrets)
	m.Backend = string(ModeKeychain)
	m.Note = "Eggify keeps these secrets in the macOS login keychain (service \"" + KeychainService + "\"). Set EGGIFY_SECRET_STORE=file to move them back to files. Do not delete this file while the items exist."
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.markerPath()), 0o700); err != nil {
		return err
	}
	tmp := s.markerPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.markerPath())
}

func (s *Store) recordInMarker(name string) error {
	m, err := s.readMarker()
	if err != nil {
		return err
	}
	if m == nil {
		m = &marker{}
	}
	for _, n := range m.Secrets {
		if n == name {
			return nil
		}
	}
	m.Secrets = append(m.Secrets, name)
	return s.writeMarker(m)
}

// LoadOrCreate returns the secret, creating it with generate when it does not
// exist yet. validate (optional) checks a loaded value. With the keychain
// backend an existing legacy file is migrated into the keychain and removed.
func (s *Store) LoadOrCreate(name string, generate func() ([]byte, error), validate func([]byte) error) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keychain == nil {
		return s.fileLoadOrCreate(name, generate, validate)
	}
	return s.keychainLoadOrCreate(name, generate, validate)
}

func check(validate func([]byte) error, v []byte) error {
	if validate == nil {
		return nil
	}
	return validate(v)
}

func (s *Store) fileLoadOrCreate(name string, generate func() ([]byte, error), validate func([]byte) error) ([]byte, error) {
	path := LegacyPath(s.claudeDir, name)
	data, err := os.ReadFile(path)
	if err == nil {
		if err := check(validate, data); err != nil {
			return nil, err
		}
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	v, err := generate()
	if err != nil {
		return nil, err
	}
	if err := writeSecretFile(path, v); err != nil {
		return nil, err
	}
	return v, nil
}

func writeSecretFile(path string, v []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, v, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func (s *Store) keychainLoadOrCreate(name string, generate func() ([]byte, error), validate func([]byte) error) ([]byte, error) {
	acct := s.account(name)
	path := LegacyPath(s.claudeDir, name)

	v, err := s.keychain.Get(acct)
	switch {
	case err == nil:
		if err := check(validate, v); err != nil {
			return nil, fmt.Errorf("keychain item %s: %w", name, err)
		}
		// A legacy file left behind (e.g. a crash between migrate and remove,
		// or an older eggify recreated it) must not keep the secret readable.
		if old, ferr := os.ReadFile(path); ferr == nil {
			if bytes.Equal(old, v) {
				if rerr := os.Remove(path); rerr != nil {
					s.logf("secretstore: could not remove %s after migration: %v", path, rerr)
				}
			} else {
				s.logf("secretstore: %s differs from the keychain copy of %s; leaving it in place — data encrypted with it may be unreadable", path, name)
			}
		}
		_ = s.recordInMarker(name)
		return v, nil
	case errors.Is(err, ErrNotFound):
	default:
		return nil, fmt.Errorf("reading %s from the keychain: %w", name, err)
	}

	// Not in the keychain yet: migrate the legacy file if there is one.
	if old, ferr := os.ReadFile(path); ferr == nil {
		if err := check(validate, old); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if err := s.storeVerified(acct, old); err != nil {
			return nil, fmt.Errorf("migrating %s to the keychain: %w", path, err)
		}
		if err := s.recordInMarker(name); err != nil {
			return nil, fmt.Errorf("recording migration of %s: %w", name, err)
		}
		// No backup copy is kept: a readable backup would defeat the move.
		// The value was read back from the keychain and compared first.
		if err := os.Remove(path); err != nil {
			s.logf("secretstore: migrated %s to the keychain but could not remove %s: %v", name, path, err)
		} else {
			s.logf("secretstore: moved %s from %s into the macOS keychain", name, path)
		}
		return old, nil
	} else if !errors.Is(ferr, os.ErrNotExist) {
		return nil, fmt.Errorf("reading %s: %w", path, ferr)
	}

	// Refuse to mint a new key for a secret the marker says is in the
	// keychain: the item vanished (deleted, other keychain), and a new key
	// would make everything encrypted with the old one unreadable.
	if m, merr := s.readMarker(); merr == nil && m != nil {
		for _, n := range m.Secrets {
			if n == name {
				return nil, fmt.Errorf("%s is recorded in %s as stored in the keychain, but the keychain item is missing; refusing to generate a new key (restore the item, or delete the marker entry to start over and lose data encrypted with the old key)", name, s.markerPath())
			}
		}
	}

	nv, err := generate()
	if err != nil {
		return nil, err
	}
	if err := s.storeVerified(acct, nv); err != nil {
		return nil, fmt.Errorf("storing %s in the keychain: %w", name, err)
	}
	if err := s.recordInMarker(name); err != nil {
		return nil, fmt.Errorf("recording %s: %w", name, err)
	}
	return nv, nil
}

// storeVerified writes v and reads it back before anything relies on it.
func (s *Store) storeVerified(acct string, v []byte) error {
	if err := s.keychain.Set(acct, v); err != nil {
		return err
	}
	got, err := s.keychain.Get(acct)
	if err != nil {
		return fmt.Errorf("read-back failed: %w", err)
	}
	if !bytes.Equal(got, v) {
		return errors.New("read-back returned a different value")
	}
	return nil
}

// moveToFiles writes every keychain secret listed in m back to its legacy
// file, deletes the item and finally removes the marker.
func (s *Store) moveToFiles(m *marker) error {
	for _, name := range m.Secrets {
		acct := s.account(name)
		v, err := s.keychain.Get(acct)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", name, err)
		}
		path := LegacyPath(s.claudeDir, name)
		if err := writeSecretFile(path, v); err != nil {
			return err
		}
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, v) {
			return fmt.Errorf("verifying %s failed", path)
		}
		if err := s.keychain.Delete(acct); err != nil {
			s.logf("secretstore: wrote %s but could not delete the keychain item: %v", path, err)
		}
		s.logf("secretstore: moved %s from the macOS keychain to %s", name, path)
	}
	if err := os.Remove(s.markerPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ---- process-wide stores ---------------------------------------------------

var (
	regMu      sync.Mutex
	regStores  = map[string]*Store{}
	regModes   = map[string]Mode{}
	openStore  = Open // replaced in tests
	platformKC = newPlatformKeychain
)

// Configure sets the configured mode (config.json) for claudeDir. The
// EGGIFY_SECRET_STORE environment variable still wins. Call it before For.
func Configure(claudeDir, mode string) {
	m, ok := ParseMode(mode)
	if !ok {
		log.Printf("secretstore: unknown secret_store %q, using auto", mode)
	}
	regMu.Lock()
	defer regMu.Unlock()
	if regModes[claudeDir] != m {
		delete(regStores, claudeDir)
	}
	regModes[claudeDir] = m
}

// For returns the process-wide store for claudeDir, opening it on first use.
func For(claudeDir string) (*Store, error) {
	regMu.Lock()
	defer regMu.Unlock()
	if s, ok := regStores[claudeDir]; ok {
		return s, nil
	}
	mode := regModes[claudeDir]
	if env := os.Getenv(EnvMode); env != "" {
		m, ok := ParseMode(env)
		if !ok {
			log.Printf("secretstore: unknown %s=%q, using auto", EnvMode, env)
		}
		mode = m
	}
	kc, protects := platformKC()
	s, err := openStore(Options{ClaudeDir: claudeDir, Mode: mode, Keychain: kc, Protects: protects})
	if err != nil {
		return nil, err
	}
	if s.Backend() != "file" {
		log.Printf("secretstore: using %s (%s)", s.Backend(), s.Reason())
	}
	regStores[claudeDir] = s
	return s, nil
}

// LoadOrCreate is shorthand for For(claudeDir) + Store.LoadOrCreate.
func LoadOrCreate(claudeDir, name string, generate func() ([]byte, error), validate func([]byte) error) ([]byte, error) {
	s, err := For(claudeDir)
	if err != nil {
		return nil, err
	}
	return s.LoadOrCreate(name, generate, validate)
}

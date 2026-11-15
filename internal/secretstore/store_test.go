package secretstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeKeychain is an in-memory Backend. It never touches the real keychain.
type fakeKeychain struct {
	mu        sync.Mutex
	items     map[string][]byte
	getErr    error
	setErr    error
	corrupt   bool // Set stores a different value (read-back must catch it)
	deleted   []string
	setCalled int
}

func newFake() *fakeKeychain { return &fakeKeychain{items: map[string][]byte{}} }

func (f *fakeKeychain) Name() string { return "keychain" }
func (f *fakeKeychain) Get(a string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	v, ok := f.items[a]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), v...), nil
}
func (f *fakeKeychain) Set(a string, v []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalled++
	if f.setErr != nil {
		return f.setErr
	}
	v = append([]byte(nil), v...)
	if f.corrupt {
		v = append(v, 'x')
	}
	f.items[a] = v
	return nil
}
func (f *fakeKeychain) Delete(a string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, a)
	f.deleted = append(f.deleted, a)
	return nil
}

func protects(ok bool) func() (bool, string) {
	return func() (bool, string) { return ok, "test" }
}

func quiet(string, ...any) {}

func key32(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func gen(b byte) func() ([]byte, error) {
	return func() ([]byte, error) { return key32(b), nil }
}

func valid32(v []byte) error {
	if len(v) != 32 {
		return errors.New("bad length")
	}
	return nil
}

func open(t *testing.T, dir string, mode Mode, kc Backend, p func() (bool, string)) *Store {
	t.Helper()
	s, err := Open(Options{ClaudeDir: dir, Mode: mode, Keychain: kc, Protects: p, Logf: quiet})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func writeLegacy(t *testing.T, dir, name string, v []byte) string {
	t.Helper()
	p := LegacyPath(dir, name)
	if err := writeSecretFile(p, v); err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestLegacyPaths(t *testing.T) {
	if got := LegacyPath("/h/.claude", MFAEncryptionKey); got != "/h/.claude/eggify/mfa_encryption.key" {
		t.Fatal(got)
	}
	if got := LegacyPath("/h/.claude", ConnectorEncryptionKey); got != "/h/.claude/connector_encryption.key" {
		t.Fatal(got)
	}
}

func TestFileBackendCreatesAndReloads(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, ModeFile, newFake(), protects(true))
	if s.Backend() != "file" {
		t.Fatalf("backend = %s", s.Backend())
	}
	v, err := s.LoadOrCreate(MFAEncryptionKey, gen(1), valid32)
	if err != nil {
		t.Fatal(err)
	}
	p := LegacyPath(dir, MFAEncryptionKey)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v", fi.Mode().Perm())
	}
	again, err := s.LoadOrCreate(MFAEncryptionKey, gen(2), valid32)
	if err != nil || !bytes.Equal(again, v) {
		t.Fatalf("reload = %v, %v", again, err)
	}
}

func TestFileBackendRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	writeLegacy(t, dir, MFAEncryptionKey, []byte("short"))
	s := open(t, dir, ModeFile, nil, nil)
	if _, err := s.LoadOrCreate(MFAEncryptionKey, gen(1), valid32); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestAutoModeSelection(t *testing.T) {
	cases := []struct {
		name string
		kc   Backend
		p    func() (bool, string)
		want string
	}{
		{"no keychain in build", nil, nil, "file"},
		{"keychain does not protect (ad-hoc signed)", newFake(), protects(false), "file"},
		{"protection unknown", newFake(), nil, "file"},
		{"keychain protects (developer id + hardened runtime)", newFake(), protects(true), "keychain"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := open(t, t.TempDir(), ModeAuto, c.kc, c.p)
			if s.Backend() != c.want {
				t.Fatalf("backend = %s (%s), want %s", s.Backend(), s.Reason(), c.want)
			}
		})
	}
}

func TestKeychainModeWithoutBackendFallsBackToFile(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, ModeKeychain, nil, nil)
	if s.Backend() != "file" {
		t.Fatalf("backend = %s", s.Backend())
	}
	if _, err := s.LoadOrCreate(ConnectorEncryptionKey, gen(1), valid32); err != nil {
		t.Fatal(err)
	}
	if !exists(LegacyPath(dir, ConnectorEncryptionKey)) {
		t.Fatal("expected file")
	}
}

func TestKeychainMigratesExistingFile(t *testing.T) {
	dir := t.TempDir()
	p := writeLegacy(t, dir, ConnectorEncryptionKey, key32(7))
	kc := newFake()
	s := open(t, dir, ModeKeychain, kc, protects(false))

	v, err := s.LoadOrCreate(ConnectorEncryptionKey, gen(9), valid32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(v, key32(7)) {
		t.Fatal("migration must keep the existing key, not generate a new one")
	}
	if exists(p) {
		t.Fatal("legacy file should be removed after migration")
	}
	if got := kc.items[s.account(ConnectorEncryptionKey)]; !bytes.Equal(got, key32(7)) {
		t.Fatal("keychain does not hold the migrated key")
	}
	m, err := s.readMarker()
	if err != nil || m == nil || len(m.Secrets) != 1 || m.Secrets[0] != ConnectorEncryptionKey {
		t.Fatalf("marker = %+v, %v", m, err)
	}
	data, _ := os.ReadFile(s.markerPath())
	if bytes.Contains(data, key32(7)) {
		t.Fatal("marker must not contain secret material")
	}

	// Next start loads from the keychain.
	s2 := open(t, dir, ModeKeychain, kc, nil)
	v2, err := s2.LoadOrCreate(ConnectorEncryptionKey, gen(9), valid32)
	if err != nil || !bytes.Equal(v2, key32(7)) {
		t.Fatalf("reload = %v, %v", v2, err)
	}
}

func TestKeychainMigrationReadBackMismatchKeepsFile(t *testing.T) {
	dir := t.TempDir()
	p := writeLegacy(t, dir, MFAEncryptionKey, key32(3))
	kc := newFake()
	kc.corrupt = true
	s := open(t, dir, ModeKeychain, kc, nil)
	if _, err := s.LoadOrCreate(MFAEncryptionKey, gen(1), valid32); err == nil {
		t.Fatal("expected read-back error")
	}
	if !exists(p) {
		t.Fatal("legacy file must survive a failed migration")
	}
	if m, _ := s.readMarker(); m != nil {
		t.Fatal("no marker after a failed migration")
	}
}

func TestKeychainMigrationWriteFailureKeepsFile(t *testing.T) {
	dir := t.TempDir()
	p := writeLegacy(t, dir, MFAEncryptionKey, key32(3))
	kc := newFake()
	kc.setErr = errors.New("denied")
	s := open(t, dir, ModeKeychain, kc, nil)
	if _, err := s.LoadOrCreate(MFAEncryptionKey, gen(1), valid32); err == nil {
		t.Fatal("expected error")
	}
	if !exists(p) {
		t.Fatal("legacy file must survive")
	}
}

func TestKeychainGeneratesNewSecretWithoutFile(t *testing.T) {
	dir := t.TempDir()
	kc := newFake()
	s := open(t, dir, ModeAuto, kc, protects(true))
	v, err := s.LoadOrCreate(MFAEncryptionKey, gen(5), valid32)
	if err != nil || !bytes.Equal(v, key32(5)) {
		t.Fatalf("v=%v err=%v", v, err)
	}
	if exists(LegacyPath(dir, MFAEncryptionKey)) {
		t.Fatal("no file should be written with the keychain backend")
	}
	if m, _ := s.readMarker(); m == nil {
		t.Fatal("marker expected")
	}
}

func TestKeychainReadErrorDoesNotFallBackToFile(t *testing.T) {
	dir := t.TempDir()
	kc := newFake()
	kc.getErr = errors.New("errSecInteractionNotAllowed")
	s := open(t, dir, ModeKeychain, kc, nil)
	if _, err := s.LoadOrCreate(MFAEncryptionKey, gen(1), valid32); err == nil {
		t.Fatal("expected error")
	}
	if exists(LegacyPath(dir, MFAEncryptionKey)) || kc.setCalled != 0 {
		t.Fatal("must not create a new key when the keychain cannot be read")
	}
}

func TestMarkerIsSticky(t *testing.T) {
	dir := t.TempDir()
	kc := newFake()
	s := open(t, dir, ModeKeychain, kc, nil)
	if _, err := s.LoadOrCreate(MFAEncryptionKey, gen(4), valid32); err != nil {
		t.Fatal(err)
	}
	// A differently signed binary in auto mode (e.g. the CLI next to the
	// desktop app) must keep using the keychain, not mint a new file key.
	s2 := open(t, dir, ModeAuto, kc, protects(false))
	if s2.Backend() != "keychain" {
		t.Fatalf("backend = %s", s2.Backend())
	}
	v, err := s2.LoadOrCreate(MFAEncryptionKey, gen(8), valid32)
	if err != nil || !bytes.Equal(v, key32(4)) {
		t.Fatalf("v=%v err=%v", v, err)
	}
}

func TestMarkerWithoutKeychainBackendFails(t *testing.T) {
	dir := t.TempDir()
	kc := newFake()
	s := open(t, dir, ModeKeychain, kc, nil)
	if _, err := s.LoadOrCreate(MFAEncryptionKey, gen(4), valid32); err != nil {
		t.Fatal(err)
	}
	_, err := Open(Options{ClaudeDir: dir, Mode: ModeAuto, Logf: quiet})
	if err == nil || !errors.Is(err, ErrKeychainUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingItemRecordedInMarkerIsNotRegenerated(t *testing.T) {
	dir := t.TempDir()
	kc := newFake()
	s := open(t, dir, ModeKeychain, kc, nil)
	if _, err := s.LoadOrCreate(MFAEncryptionKey, gen(4), valid32); err != nil {
		t.Fatal(err)
	}
	delete(kc.items, s.account(MFAEncryptionKey)) // user deleted it in Keychain Access
	_, err := s.LoadOrCreate(MFAEncryptionKey, gen(6), valid32)
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("err = %v", err)
	}
	// A secret never stored before can still be created.
	if _, err := s.LoadOrCreate(ConnectorEncryptionKey, gen(6), valid32); err != nil {
		t.Fatal(err)
	}
}

func TestLeftoverLegacyFileIsRemovedWhenEqual(t *testing.T) {
	dir := t.TempDir()
	kc := newFake()
	s := open(t, dir, ModeKeychain, kc, nil)
	if _, err := s.LoadOrCreate(MFAEncryptionKey, gen(4), valid32); err != nil {
		t.Fatal(err)
	}
	p := writeLegacy(t, dir, MFAEncryptionKey, key32(4))
	if _, err := s.LoadOrCreate(MFAEncryptionKey, gen(4), valid32); err != nil {
		t.Fatal(err)
	}
	if exists(p) {
		t.Fatal("equal leftover file should be removed")
	}
	// A differing file is left alone (it may be the only copy of an old key).
	p = writeLegacy(t, dir, MFAEncryptionKey, key32(9))
	v, err := s.LoadOrCreate(MFAEncryptionKey, gen(4), valid32)
	if err != nil || !bytes.Equal(v, key32(4)) {
		t.Fatal("keychain copy wins")
	}
	if !exists(p) {
		t.Fatal("differing file must be kept")
	}
}

func TestSwitchBackToFileMovesSecretsOut(t *testing.T) {
	dir := t.TempDir()
	kc := newFake()
	s := open(t, dir, ModeKeychain, kc, nil)
	for i, n := range []string{MFAEncryptionKey, ConnectorEncryptionKey} {
		if _, err := s.LoadOrCreate(n, gen(byte(10+i)), valid32); err != nil {
			t.Fatal(err)
		}
	}
	s2 := open(t, dir, ModeFile, kc, nil)
	if s2.Backend() != "file" {
		t.Fatalf("backend = %s", s2.Backend())
	}
	if exists(s.markerPath()) {
		t.Fatal("marker should be removed")
	}
	if len(kc.items) != 0 {
		t.Fatalf("keychain items left: %d", len(kc.items))
	}
	for i, n := range []string{MFAEncryptionKey, ConnectorEncryptionKey} {
		v, err := s2.LoadOrCreate(n, gen(99), valid32)
		if err != nil || !bytes.Equal(v, key32(byte(10+i))) {
			t.Fatalf("%s: v=%v err=%v", n, v, err)
		}
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"": ModeAuto, "AUTO": ModeAuto, " file ": ModeFile, "keychain": ModeKeychain} {
		if got, ok := ParseMode(in); !ok || got != want {
			t.Fatalf("%q -> %v %v", in, got, ok)
		}
	}
	if got, ok := ParseMode("vault"); ok || got != ModeAuto {
		t.Fatal("unknown mode must fall back to auto")
	}
}

func TestRegistryHonoursEnvAndConfig(t *testing.T) {
	kc := newFake()
	orig := platformKC
	platformKC = func() (Backend, func() (bool, string)) { return kc, protects(false) }
	t.Cleanup(func() { platformKC = orig })

	// Config says keychain; no env.
	t.Setenv(EnvMode, "")
	dir := t.TempDir()
	Configure(dir, "keychain")
	v, err := LoadOrCreate(dir, MFAEncryptionKey, gen(1), valid32)
	if err != nil || !bytes.Equal(v, key32(1)) {
		t.Fatalf("v=%v err=%v", v, err)
	}
	if exists(LegacyPath(dir, MFAEncryptionKey)) {
		t.Fatal("expected keychain storage")
	}

	// Env wins over config.
	dir2 := t.TempDir()
	Configure(dir2, "keychain")
	t.Setenv(EnvMode, "file")
	if _, err := LoadOrCreate(dir2, MFAEncryptionKey, gen(2), valid32); err != nil {
		t.Fatal(err)
	}
	if !exists(LegacyPath(dir2, MFAEncryptionKey)) {
		t.Fatal("env file mode should write a file")
	}

	// Default (auto) with a non-protecting keychain keeps files.
	t.Setenv(EnvMode, "")
	dir3 := t.TempDir()
	if _, err := LoadOrCreate(dir3, ConnectorEncryptionKey, gen(3), valid32); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dir3, "connector_encryption.key")) {
		t.Fatal("auto should keep files when the keychain does not protect")
	}

	// The store is cached per directory.
	a, _ := For(dir3)
	b, _ := For(dir3)
	if a != b {
		t.Fatal("expected cached store")
	}
}

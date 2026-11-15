package connectors

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/schlunsen/eggify/internal/secretstore"
)

func TestLoadOrGenerateConnectorKeyFileBackend(t *testing.T) {
	t.Setenv(secretstore.EnvMode, "file")
	dir := t.TempDir()
	k1, err := LoadOrGenerateConnectorKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(k1) != 32 {
		t.Fatalf("len = %d", len(k1))
	}
	path := filepath.Join(dir, "connector_encryption.key")
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, k1) {
		t.Fatalf("key file: %v", err)
	}
	k2, err := LoadOrGenerateConnectorKey(dir)
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatal("key must be stable across loads")
	}
	// Ciphertext made with the key still decrypts.
	enc, err := Encrypt("sk-test", k1)
	if err != nil {
		t.Fatal(err)
	}
	if dec, _ := Decrypt(enc, k2); dec != "sk-test" {
		t.Fatalf("dec = %q", dec)
	}
}

func TestLoadOrGenerateConnectorKeyRejectsBadLength(t *testing.T) {
	t.Setenv(secretstore.EnvMode, "file")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "connector_encryption.key"), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrGenerateConnectorKey(dir); err == nil {
		t.Fatal("expected error")
	}
}

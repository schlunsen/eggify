package server

import (
	"crypto/rand"
	"fmt"
	"path/filepath"

	"github.com/schlunsen/eggify/internal/secretstore"
)

// LoadOrGenerateEncryptionKey loads the per-installation MFA encryption key or
// generates a new one. configDir is ~/.claude/eggify. The key lives in the secret
// store: ~/.claude/eggify/mfa_encryption.key (0600) with the file backend, or the
// macOS login keychain (see internal/secretstore).
func LoadOrGenerateEncryptionKey(configDir string) ([]byte, error) {
	return secretstore.LoadOrCreate(filepath.Dir(configDir), secretstore.MFAEncryptionKey, newAES256Key, validateAES256Key)
}

func newAES256Key() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate encryption key: %w", err)
	}
	return key, nil
}

func validateAES256Key(data []byte) error {
	if len(data) != 32 {
		return fmt.Errorf("invalid encryption key length: expected 32 bytes, got %d", len(data))
	}
	return nil
}

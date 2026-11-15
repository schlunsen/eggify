//go:build !darwin || !cgo

package secretstore

// KeychainService is the service name of Eggify's keychain items.
const KeychainService = "Eggify"

// newPlatformKeychain: no keychain backend outside darwin+cgo builds, so the
// file backend is used.
func newPlatformKeychain() (Backend, func() (bool, string)) {
	return nil, nil
}

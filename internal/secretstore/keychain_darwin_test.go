//go:build darwin && cgo

package secretstore

import (
	"strings"
	"testing"
)

// The test binary is ad-hoc (linker) signed, so auto mode must not pick the
// keychain for it. This only reads the code signature; it never touches the
// keychain.
func TestSigningProtectsAdHocBinary(t *testing.T) {
	ok, why := signingProtects()
	if ok {
		t.Fatalf("ad-hoc signed test binary reported as protected: %s", why)
	}
	if !strings.Contains(why, "ad-hoc") && !strings.Contains(why, "signature") {
		t.Fatalf("unexpected reason: %s", why)
	}
}

package license

import "os"

// Required switches licensing on. It is a string so release builds can set
// it with
//
//	-ldflags "-X github.com/schlunsen/eggify/internal/license.Required=true"
//
// (scripts/build-desktop-wails.sh does). Dev builds, `go test` and CI leave
// it empty and are unaffected; EGGIFY_LICENSE_REQUIRED=1 turns it on for a
// dev build.
var Required = ""

// Enforced reports whether this build requires a license.
func Enforced() bool {
	if Required == "true" || Required == "1" {
		return true
	}
	v := os.Getenv("EGGIFY_LICENSE_REQUIRED")
	return v == "1" || v == "true"
}

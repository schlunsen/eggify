//go:build !windows

package handlers

import (
	"errors"
	"syscall"
)

// openNoFollow makes an open fail when the last path element is a symbolic
// link.
const openNoFollow = syscall.O_NOFOLLOW

// isNoFollowError reports whether err is what such an open returns.
func isNoFollowError(err error) bool {
	return errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK)
}

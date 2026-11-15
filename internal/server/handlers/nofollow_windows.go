//go:build windows

package handlers

// Windows has no O_NOFOLLOW; the Lstat checks and os.Root still apply.
const openNoFollow = 0

func isNoFollowError(error) bool { return false }

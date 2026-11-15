//go:build !windows

package mcp

import "syscall"

// noFollowFlag makes opening a symbolic link in the last path component fail.
const noFollowFlag = syscall.O_NOFOLLOW

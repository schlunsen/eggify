//go:build !windows

package handlers

import "syscall"

// dirWritable reports whether the server process may create files in dir.
func dirWritable(dir string) bool {
	return syscall.Access(dir, 0x2 /* W_OK */) == nil
}

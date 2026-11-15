//go:build darwin

package mcp

import (
	"bytes"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// fdPath is the true path of an open file as the kernel knows it (F_GETPATH):
// on APFS it has the case the name is stored with, whatever case was typed.
func fdPath(f *os.File) string {
	var buf [1024]byte // MAXPATHLEN
	if _, _, e := unix.Syscall(unix.SYS_FCNTL, f.Fd(), unix.F_GETPATH, uintptr(unsafe.Pointer(&buf[0]))); e != 0 {
		return ""
	}
	if i := bytes.IndexByte(buf[:], 0); i >= 0 {
		return string(buf[:i])
	}
	return ""
}

// foldPaths: APFS/HFS+ volumes are case- and normalisation-insensitive by default.
const foldPaths = true

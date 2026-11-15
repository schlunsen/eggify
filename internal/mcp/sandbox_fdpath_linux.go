//go:build linux

package mcp

import (
	"os"
	"strconv"
)

func fdPath(f *os.File) string {
	p, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(int(f.Fd())))
	if err != nil {
		return ""
	}
	return p
}

const foldPaths = false

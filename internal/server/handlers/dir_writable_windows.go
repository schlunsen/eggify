//go:build windows

package handlers

import "os"

// dirWritable approximates write access on Windows from the read-only bit.
func dirWritable(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.Mode().Perm()&0o200 != 0
}

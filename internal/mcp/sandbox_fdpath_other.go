//go:build !darwin && !linux

package mcp

import "os"

// No true-path lookup here: the folded comparison is the only layer.
func fdPath(*os.File) string { return "" }

const foldPaths = true

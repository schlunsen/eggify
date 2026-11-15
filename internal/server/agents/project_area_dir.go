package agents

import (
	"path/filepath"
	"strings"

	"github.com/schlunsen/eggify/internal/logging"
)

// areaWorkingDirectory returns the working directory of a session started in
// a project area: the project folder joined with the area's relative path.
//
// SECURITY: the REST handlers validate relative_path when an area is saved,
// but a row stored before that check (or edited in the database) may hold
// "../../.." or an absolute path, or the folder may since have become a
// symbolic link out of the project. Such an area never moves a session out
// of the project: the project folder is used instead (ok is false).
func areaWorkingDirectory(projectPath, relativePath string) (dir string, ok bool) {
	root := filepath.Clean(projectPath)
	rel := strings.TrimSpace(relativePath)
	if rel == "" || rel == "." {
		return root, true
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) || strings.HasPrefix(rel, "~") || filepath.VolumeName(rel) != "" {
		return root, false
	}
	for _, seg := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return root, false
		}
	}
	dir = filepath.Join(root, rel)
	if !isWithin(dir, root) {
		return root, false
	}
	// Symlinks resolved on both sides (in the part of the path that exists).
	if !isWithin(resolvePathForMatch(dir), resolvePathForMatch(root)) {
		return root, false
	}
	return dir, true
}

// sessionAreaDirectory is areaWorkingDirectory with a warning in the log when
// the area's path is refused.
func sessionAreaDirectory(projectPath, areaID, relativePath string) string {
	dir, ok := areaWorkingDirectory(projectPath, relativePath)
	if !ok {
		logging.Warning("Project area %s has a path outside its project (%q); using the project folder %s", areaID, relativePath, dir)
	}
	return dir
}

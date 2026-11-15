package handlers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Where a project may point. A project's folder is served by the file browser
// (/api/projects/:id/files/*) and is the working directory of its sessions,
// so a project at "/" or ~/.claude/eggify would hand out every file the
// user can read (API key, database, keys). Agents hold the API key, so
// creating a project or moving its folder needs a signed-in user
// (ProjectChangeAction) and the folder must pass validateProjectFolder.
// Rows written before these checks are re-checked when files are served
// (checkProjectRoot).

// ProjectChangeAction is the privileged-action label of creating a project
// and of changing a project's folder or system prompt (which is put into
// every new session of the project).
const ProjectChangeAction = "Creating a project or changing its folder or system prompt"

// sandboxProjectRoot is the default project folder of sandboxes (seeded when
// /app exists); it is allowed although it is outside the home folder.
const sandboxProjectRoot = "/app"

// systemProjectRoots are folders that are never a project root themselves.
var systemProjectRoots = []string{
	"/etc", "/private", "/private/etc", "/private/var", "/var", "/usr", "/bin", "/sbin",
	"/root", "/System", "/Library", "/Users", "/home", "/Volumes", "/tmp", "/private/tmp",
}

// validateProjectFolder checks the folder a project is about to point at:
// an absolute (or ~/) path to an existing folder inside the home folder (not
// the home folder itself), outside hidden folders (~/.claude/projects
// excepted), after resolving symlinks. The sandbox's /app is allowed too. It
// returns the cleaned path to store.
func validateProjectFolder(raw string) (string, error) {
	p, err := expandUserPath(raw)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("%s does not exist", displayPath(p, userHome()))
	}
	if !info.IsDir() {
		return "", errors.New("that path is a file, not a folder")
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", errors.New("invalid path")
	}
	if inSandboxRoot(real) {
		return p, nil
	}
	home := userHome()
	if home == "" {
		return "", errors.New("cannot determine home directory")
	}
	rel, err := filepath.Rel(resolveExisting(home), real)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("project folders must be inside your home folder (and not the home folder itself)")
	}
	if hiddenUnderHome(rel) {
		return "", errors.New("project folders cannot be inside hidden folders such as ~/.claude, ~/.ssh or ~/.config")
	}
	return p, nil
}

// checkProjectRoot refuses to serve files of a project whose folder is a
// place no project may be: "/", the home folder or one of its parents, a
// hidden folder in the home folder (~/.claude/eggify, ~/.ssh, ~/.codex,
// ~/.config, ...; ~/.claude/projects excepted) or a system folder. It covers
// projects created before validateProjectFolder existed; folders outside the
// home folder (other disks) stay browsable.
func checkProjectRoot(root string) error {
	if strings.TrimSpace(root) == "" {
		return errors.New("project has no folder")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return errors.New("invalid project root")
	}
	candidates := []string{filepath.Clean(abs)}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		candidates = append(candidates, real)
	}
	home := userHome()
	homes := []string{}
	if home != "" {
		homes = append(homes, home, resolveExisting(home))
	}
	for _, p := range candidates {
		if inSandboxRoot(p) {
			continue
		}
		if p == string(filepath.Separator) {
			return errors.New("this project's folder is the root of the disk; files are not served from it")
		}
		for _, sys := range systemProjectRoots {
			if p == sys {
				return fmt.Errorf("this project's folder (%s) is a system folder; files are not served from it", p)
			}
		}
		for _, h := range homes {
			if pathWithin(p, h) {
				return errors.New("this project's folder contains your home folder; files are not served from it")
			}
			if rel, err := filepath.Rel(h, p); err == nil && pathWithin(h, p) && hiddenUnderHome(rel) {
				return errors.New("this project's folder is inside a hidden folder of your home folder; files are not served from it")
			}
		}
	}
	return nil
}

// hiddenUnderHome reports whether rel (relative to the home folder) is in a
// hidden folder, the legacy ~/.claude/projects clone folder excepted.
func hiddenUnderHome(rel string) bool {
	if rel == legacyCloneDir {
		return true // the folder itself holds every Claude Code transcript
	}
	if strings.HasPrefix(rel, legacyCloneDir+string(filepath.Separator)) {
		rel = strings.TrimPrefix(rel, legacyCloneDir+string(filepath.Separator))
	}
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		if strings.HasPrefix(seg, ".") && seg != "." {
			return true
		}
	}
	return false
}

// inSandboxRoot reports whether p is the sandbox project folder or inside it.
func inSandboxRoot(p string) bool {
	return pathWithin(sandboxProjectRoot, p)
}

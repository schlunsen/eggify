package handlers

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
)

// A project area names a folder of the project. Its context_prompt is put in
// front of the system prompt of every session started in the area (and again
// when such a session is restored), and its relative_path becomes that
// session's working directory. Agents hold the API key, so creating, changing
// or deleting an area needs a signed-in user (ProjectAreaChangeAction), and
// the path must stay inside the project (validateAreaRelativePath; the
// session manager checks stored rows again when it uses them).

// ProjectAreaChangeAction is the privileged-action label of creating,
// changing or deleting a project area.
const ProjectAreaChangeAction = "Creating, changing or deleting a project area"

const maxAreaRelativePathLen = 1024

// validateAreaRelativePath checks an area's folder: a relative path without
// ".." that, after resolving symlinks, is still inside the project folder.
// Empty or "." is the project folder itself. It returns the cleaned path to
// store (slashes as given by filepath.Clean, "" kept as "").
func validateAreaRelativePath(projectPath, raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", nil
	}
	if len(p) > maxAreaRelativePathLen || strings.ContainsRune(p, 0) {
		return "", errors.New("relative_path is not a valid path")
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || strings.HasPrefix(p, "~") || filepath.VolumeName(p) != "" {
		return "", errors.New("relative_path must be relative to the project folder")
	}
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return "", errors.New(`relative_path must not contain ".."`)
		}
	}
	clean := filepath.Clean(p)

	absRoot, err := filepath.Abs(projectPath)
	if err != nil || strings.TrimSpace(projectPath) == "" {
		return "", errors.New("project has no folder")
	}
	target := filepath.Join(absRoot, clean)
	if !pathWithin(absRoot, target) {
		return "", errors.New("relative_path must stay inside the project folder")
	}
	realRoot, err := evalSymlinksAllowMissing(absRoot)
	if err != nil {
		return "", errors.New("invalid project folder")
	}
	realTarget, err := evalSymlinksAllowMissing(target)
	if err != nil {
		return "", errors.New("relative_path is not a valid path")
	}
	if !pathWithin(realRoot, realTarget) {
		return "", errors.New("relative_path leads outside the project folder (symbolic link)")
	}
	return clean, nil
}

// areaOfProject loads the area :areaId and checks that it belongs to project
// :id. When it returns nil the 404 has been written.
func (h *ProjectHandler) areaOfProject(c *fiber.Ctx) (*database.ProjectArea, error) {
	area, err := h.repo.GetProjectArea(c.Params("areaId"))
	if err != nil || area == nil || area.ProjectID != c.Params("id") {
		return nil, c.Status(404).JSON(fiber.Map{
			"error": "Project area not found",
		})
	}
	return area, nil
}

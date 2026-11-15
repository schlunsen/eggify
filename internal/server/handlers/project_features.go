package handlers

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
)

// Feature files are <project>/.claude/features/<name>.md. The repository
// decides what is in that folder, so a checkout can ship
// ".claude/features/x.md" as a link to ~/.zshenv, or ".claude/features" as a
// link to ~/.claude. These helpers keep every read, write and delete inside
// the project:
//
//   - the name is a plain file name (featureName);
//   - the project folder passes checkProjectRoot, like the file browser;
//   - ".claude", ".claude/features" and the file itself must not be symbolic
//     links, and the file must be a regular file;
//   - all file access goes through an os.Root of the project folder, which
//     refuses any path that leaves it (also when a folder is swapped for a
//     link between the check and the open), and the file is opened without
//     following a final link.

// FeatureChangeAction is the privileged-action label of writing or deleting
// a project's feature files: they are files in the user's project, and their
// text is what a "/feature" command puts into a prompt.
const FeatureChangeAction = "Changing a project's feature files"

const (
	maxFeatureNameLen = 100
	// maxFeatureFileSize bounds what is read into a response.
	maxFeatureFileSize = 4 << 20
)

var (
	errFeatureNotFound = errors.New("feature not found")
	errFeatureLink     = errors.New("feature files must be regular files in the project's .claude/features folder; symbolic links are not followed")
	errFeatureTooLarge = errors.New("feature file is too large")
)

// featureDirParts are the folders from the project root down to the feature
// files.
var featureDirParts = []string{".claude", "features"}

// featureName validates the :name URL param: a plain file name of letters,
// digits, spaces, '-', '_' and '.', not starting with a dot, at most
// maxFeatureNameLen bytes. Returns an error string if invalid.
func featureName(raw string) (string, string) {
	const invalid = "invalid feature name"
	if raw == "" || len(raw) > maxFeatureNameLen || !utf8.ValidString(raw) {
		return "", invalid
	}
	if strings.HasPrefix(raw, ".") || raw != strings.TrimSpace(raw) {
		return "", invalid
	}
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case r == '-', r == '_', r == '.', r == ' ':
		default:
			return "", invalid // separators, control characters, ...
		}
	}
	return raw, ""
}

// featureStore is one project's feature folder, opened for a request.
type featureStore struct {
	root *os.Root
}

// openFeatureStore opens the project folder. The error is safe to show.
func openFeatureStore(projectPath string) (*featureStore, error) {
	if err := checkProjectRoot(projectPath); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(projectPath)
	if err != nil {
		return nil, errors.New("project folder not found")
	}
	return &featureStore{root: root}, nil
}

func (s *featureStore) Close() { _ = s.root.Close() }

// dir checks the folders down to .claude/features: each must be a real
// folder, not a link. With create it makes the missing ones. exists is false
// (and err nil) when the folder is not there.
func (s *featureStore) dir(create bool) (dir string, exists bool, err error) {
	for _, part := range featureDirParts {
		dir = filepath.Join(dir, part)
		info, err := s.root.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			if !create {
				return "", false, nil
			}
			if err := s.root.Mkdir(dir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
				return "", false, err
			}
			if info, err = s.root.Lstat(dir); err != nil {
				return "", false, err
			}
		} else if err != nil {
			return "", false, err
		}
		if !info.IsDir() { // a symbolic link is not a directory to Lstat
			return "", false, errFeatureLink
		}
	}
	return dir, true, nil
}

// file returns the feature's path inside the root after checking that it is
// a regular file. exists is false when there is no such file.
func (s *featureStore) file(name string, create bool) (path string, exists bool, err error) {
	dir, ok, err := s.dir(create)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	path = filepath.Join(dir, name+".md")
	info, err := s.root.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, errFeatureLink
	}
	return path, true, nil
}

// List returns the names of the regular .md files in the feature folder.
func (s *featureStore) List() ([]string, error) {
	names := []string{}
	dir, ok, err := s.dir(false)
	if err != nil || !ok {
		return names, err
	}
	f, err := s.root.Open(dir)
	if err != nil {
		return names, featureOpenError(err)
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return names, err
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".md") {
			names = append(names, strings.TrimSuffix(entry.Name(), ".md"))
		}
	}
	return names, nil
}

// Read returns the feature's content.
func (s *featureStore) Read(name string) ([]byte, error) {
	path, ok, err := s.file(name, false)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errFeatureNotFound
	}
	f, err := s.root.OpenFile(path, os.O_RDONLY|openNoFollow, 0)
	if err != nil {
		return nil, featureOpenError(err)
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, errFeatureLink
	}
	content, err := io.ReadAll(io.LimitReader(f, maxFeatureFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxFeatureFileSize {
		return nil, errFeatureTooLarge
	}
	return content, nil
}

// Write creates or replaces the feature.
func (s *featureStore) Write(name string, content []byte) error {
	path, _, err := s.file(name, true)
	if err != nil {
		return err
	}
	// Opened without O_TRUNC, so nothing is cut before the file is known to
	// be a regular one.
	f, err := s.root.OpenFile(path, os.O_WRONLY|os.O_CREATE|openNoFollow, 0o644)
	if err != nil {
		return featureOpenError(err)
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return errFeatureLink
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		return err
	}
	return f.Close()
}

// Delete removes the feature file.
func (s *featureStore) Delete(name string) error {
	path, ok, err := s.file(name, false)
	if err != nil {
		return err
	}
	if !ok {
		return errFeatureNotFound
	}
	if err := s.root.Remove(path); err != nil {
		return featureOpenError(err)
	}
	return nil
}

// featureOpenError turns the errors of a path that left the project, or of a
// link that appeared after the check, into the refusal.
func featureOpenError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return errFeatureNotFound
	}
	if isNoFollowError(err) || strings.Contains(err.Error(), "path escapes from parent") {
		return errFeatureLink
	}
	return err
}

// featureErrorStatus maps a feature store error to an HTTP status.
func featureErrorStatus(err error) int {
	switch {
	case errors.Is(err, errFeatureNotFound):
		return 404
	case errors.Is(err, errFeatureLink):
		return 403
	case errors.Is(err, errFeatureTooLarge):
		return 413
	}
	return 500
}

// featureErrorText is the message sent for a feature store error.
func featureErrorText(err error) string {
	if errors.Is(err, errFeatureNotFound) {
		return "Feature not found"
	}
	return err.Error()
}

// ==================== Feature File Handlers ====================

// featureStoreFor loads the project and opens its feature folder. When it
// returns nil the response has been written.
func (h *ProjectHandler) featureStoreFor(c *fiber.Ctx) (*featureStore, error) {
	project, err := h.repo.GetProject(c.Params("id"))
	if err != nil {
		return nil, c.Status(404).JSON(fiber.Map{"error": "Project not found"})
	}
	store, err := openFeatureStore(project.Path)
	if err != nil {
		return nil, c.Status(403).JSON(fiber.Map{"error": err.Error()})
	}
	return store, nil
}

func featureError(c *fiber.Ctx, err error) error {
	return c.Status(featureErrorStatus(err)).JSON(fiber.Map{"error": featureErrorText(err)})
}

// HandleListFeatures handles GET /api/projects/:id/features
// Returns all .md files in <project.path>/.claude/features/
func (h *ProjectHandler) HandleListFeatures(c *fiber.Ctx) error {
	store, err := h.featureStoreFor(c)
	if store == nil {
		return err
	}
	defer store.Close()

	names, err := store.List()
	if err != nil {
		return featureError(c, err)
	}
	features := []fiber.Map{}
	for _, name := range names {
		features = append(features, fiber.Map{"name": name})
	}
	return c.JSON(fiber.Map{"features": features})
}

// HandleGetFeature handles GET /api/projects/:id/features/:name
// Returns the content of a single feature markdown file
func (h *ProjectHandler) HandleGetFeature(c *fiber.Ctx) error {
	name, errMsg := featureName(c.Params("name"))
	if errMsg != "" {
		return c.Status(400).JSON(fiber.Map{"error": errMsg})
	}
	store, err := h.featureStoreFor(c)
	if store == nil {
		return err
	}
	defer store.Close()

	content, err := store.Read(name)
	if err != nil {
		return featureError(c, err)
	}
	return c.JSON(fiber.Map{"name": name, "content": string(content)})
}

// HandleSaveFeature handles PUT /api/projects/:id/features/:name
// Creates or updates a feature markdown file. The route needs a signed-in
// user (FeatureChangeAction).
func (h *ProjectHandler) HandleSaveFeature(c *fiber.Ctx) error {
	name, errMsg := featureName(c.Params("name"))
	if errMsg != "" {
		return c.Status(400).JSON(fiber.Map{"error": errMsg})
	}

	var body struct {
		Content string `json:"content"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	store, err := h.featureStoreFor(c)
	if store == nil {
		return err
	}
	defer store.Close()

	if err := store.Write(name, []byte(body.Content)); err != nil {
		return featureError(c, err)
	}
	return c.JSON(fiber.Map{"name": name, "content": body.Content})
}

// HandleDeleteFeature handles DELETE /api/projects/:id/features/:name
// Removes a feature markdown file. The route needs a signed-in user
// (FeatureChangeAction).
func (h *ProjectHandler) HandleDeleteFeature(c *fiber.Ctx) error {
	name, errMsg := featureName(c.Params("name"))
	if errMsg != "" {
		return c.Status(400).JSON(fiber.Map{"error": errMsg})
	}
	store, err := h.featureStoreFor(c)
	if store == nil {
		return err
	}
	defer store.Close()

	if err := store.Delete(name); err != nil {
		return featureError(c, err)
	}
	return c.JSON(fiber.Map{"message": "Feature deleted"})
}

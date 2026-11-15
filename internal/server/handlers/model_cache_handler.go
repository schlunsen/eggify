// Package handlers contains HTTP request handlers for the Eggify server.
// This file implements a caching proxy for ML model files.
// Files are downloaded from HuggingFace on first request, stored on the local
// filesystem (~/.claude/eggify/models/), and served from disk on subsequent requests.
// This ensures the ~2.5GB Parakeet model is downloaded only once and shared
// across all projects, surviving app restarts (unlike browser IndexedDB which
// WKWebView can evict).
package handlers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// ModelCacheHandler handles model file caching on the local filesystem.
// Model files are stored under ~/.claude/eggify/models/<version>/<filename>.
type ModelCacheHandler struct {
	cacheDir string
}

// Map from model key to HuggingFace repo ID
var modelRepos = map[string]string{
	"parakeet-tdt-0.6b-v2": "ysdede/parakeet-tdt-0.6b-v2-onnx",
	"parakeet-tdt-0.6b-v3": "istupakov/parakeet-tdt-0.6b-v3-onnx",
}

// validName matches a plain model version or filename such as
// "parakeet-tdt-0.6b-v3", "encoder-model.onnx" or "vocab.txt". It must start
// with a letter or digit, so ".", ".." and dotfiles never match, and it has no
// "%" (SendFile percent-decodes the path it is given).
var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// validModelName reports whether name is a plain version or file name.
func validModelName(name string) bool {
	return len(name) <= 255 && validName.MatchString(name) && !strings.Contains(name, "..")
}

// versionDir returns the cache folder of a model version. It refuses anything
// that is not a direct child of the cache directory.
func (h *ModelCacheHandler) versionDir(version string) (string, bool) {
	if !validModelName(version) {
		return "", false
	}
	dir := filepath.Join(h.cacheDir, version)
	if filepath.Dir(dir) != filepath.Clean(h.cacheDir) || filepath.Base(dir) != version {
		return "", false
	}
	return dir, true
}

// filePath returns the path of a cached model file. It refuses anything that
// is not directly inside the version's folder.
func (h *ModelCacheHandler) filePath(version, filename string) (string, bool) {
	dir, ok := h.versionDir(version)
	if !ok || !validModelName(filename) {
		return "", false
	}
	path := filepath.Join(dir, filename)
	if filepath.Dir(path) != dir || filepath.Base(path) != filename {
		return "", false
	}
	return path, true
}

// NewModelCacheHandler creates a new model cache handler.
func NewModelCacheHandler(claudeDir string) *ModelCacheHandler {
	cacheDir := filepath.Join(claudeDir, "eggify", "models")
	return &ModelCacheHandler{cacheDir: cacheDir}
}

// HandleCheckCache returns which files are cached for a given model version.
// GET /api/model-cache/:version
func (h *ModelCacheHandler) HandleCheckCache(c *fiber.Ctx) error {
	version := c.Params("version")
	dir, ok := h.versionDir(version)
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid model version"})
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return c.JSON(fiber.Map{"files": []string{}, "dir": dir})
	}

	type fileInfo struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	files := make([]fileInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && !strings.HasSuffix(entry.Name(), ".tmp") {
			info, err := entry.Info()
			if err == nil {
				files = append(files, fileInfo{Name: entry.Name(), Size: info.Size()})
			}
		}
	}

	return c.JSON(fiber.Map{"files": files, "dir": dir})
}

// HandleGetOrFetchFile serves a cached model file, or downloads it from
// HuggingFace first if not yet cached. This makes the endpoint a transparent
// caching proxy — the client just fetches from here and doesn't need to know
// about caching.
//
// GET /api/model-cache/:version/:filename
//
// The file is streamed to the client as it's read from disk (or downloaded).
// Content-Length is set so the client can show download progress.
func (h *ModelCacheHandler) HandleGetOrFetchFile(c *fiber.Ctx) error {
	version := c.Params("version")
	filename := c.Params("filename")

	filePath, ok := h.filePath(version, filename)
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid version or filename"})
	}

	// If file exists on disk, serve it directly
	if info, err := os.Stat(filePath); err == nil && info.Size() > 0 {
		contentType := "application/octet-stream"
		if strings.HasSuffix(filename, ".txt") {
			contentType = "text/plain"
		}
		c.Set("Content-Type", contentType)
		c.Set("Content-Length", fmt.Sprintf("%d", info.Size()))
		c.Set("X-Cache", "HIT")
		c.Set("Cache-Control", "public, max-age=31536000, immutable")
		return c.SendFile(filePath)
	}

	// Not cached — download from HuggingFace, save to disk, then serve
	repoID, ok := modelRepos[version]
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("unknown model version: %s", version),
		})
	}

	hfURL := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repoID, filename)

	fmt.Printf("[ModelCache] Downloading %s from HuggingFace (%s)...\n", filename, hfURL)

	resp, err := http.Get(hfURL)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error": fmt.Sprintf("failed to fetch from HuggingFace: %s", err.Error()),
		})
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.Status(resp.StatusCode).JSON(fiber.Map{
			"error": fmt.Sprintf("HuggingFace returned %d for %s", resp.StatusCode, filename),
		})
	}

	// Create cache directory
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create cache directory: " + err.Error(),
		})
	}

	// Write to temp file first for atomic save
	tmpPath := filePath + ".tmp"
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create temp file: " + err.Error(),
		})
	}

	written, err := io.Copy(tmpFile, resp.Body)
	tmpFile.Close()

	if err != nil {
		os.Remove(tmpPath)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error": fmt.Sprintf("download interrupted for %s: %s", filename, err.Error()),
		})
	}

	// Atomic rename: temp → final
	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to finalize cached file: " + err.Error(),
		})
	}

	fmt.Printf("[ModelCache] Cached %s (%d bytes) to disk\n", filename, written)

	// Now serve the freshly cached file
	contentType := "application/octet-stream"
	if strings.HasSuffix(filename, ".txt") {
		contentType = "text/plain"
	}
	c.Set("Content-Type", contentType)
	c.Set("Content-Length", fmt.Sprintf("%d", written))
	c.Set("X-Cache", "MISS")
	c.Set("Cache-Control", "public, max-age=31536000, immutable")

	return c.SendFile(filePath)
}

// HandleDeleteCache deletes all cached files for a model version.
// DELETE /api/model-cache/:version
func (h *ModelCacheHandler) HandleDeleteCache(c *fiber.Ctx) error {
	version := c.Params("version")
	dir, ok := h.versionDir(version)
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid model version"})
	}
	if err := os.RemoveAll(dir); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(fiber.Map{"status": "deleted", "version": version})
}

package handlers

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"regexp"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
)

// symbolPatterns maps language identifiers to regex patterns that match symbol definitions.
// Each pattern should have a named capture group "name" for the symbol name.
var symbolPatterns = map[string][]*regexp.Regexp{
	"go": {
		regexp.MustCompile(`(?m)^func\s+(?:\([^)]+\)\s+)?(?P<name>[A-Za-z_]\w*)\s*\(`),
		regexp.MustCompile(`(?m)^type\s+(?P<name>[A-Za-z_]\w*)\s+`),
		regexp.MustCompile(`(?m)^var\s+(?P<name>[A-Za-z_]\w*)\s+`),
		regexp.MustCompile(`(?m)^const\s+(?P<name>[A-Za-z_]\w*)\s+`),
	},
	"python": {
		regexp.MustCompile(`(?m)^(?:async\s+)?def\s+(?P<name>[A-Za-z_]\w*)\s*\(`),
		regexp.MustCompile(`(?m)^class\s+(?P<name>[A-Za-z_]\w*)\s*[:\(]`),
	},
	"typescript": {
		regexp.MustCompile(`(?m)(?:export\s+)?(?:async\s+)?function\s+(?P<name>[A-Za-z_$]\w*)\s*[<\(]`),
		regexp.MustCompile(`(?m)(?:export\s+)?class\s+(?P<name>[A-Za-z_$]\w*)\s*`),
		regexp.MustCompile(`(?m)(?:export\s+)?interface\s+(?P<name>[A-Za-z_$]\w*)\s*`),
		regexp.MustCompile(`(?m)(?:export\s+)?type\s+(?P<name>[A-Za-z_$]\w*)\s*=`),
		regexp.MustCompile(`(?m)(?:export\s+)?(?:const|let|var)\s+(?P<name>[A-Za-z_$]\w*)\s*=\s*(?:async\s+)?\(`),
		regexp.MustCompile(`(?m)(?:export\s+)?(?:const|let|var)\s+(?P<name>[A-Za-z_$]\w*)\s*=\s*(?:async\s+)?function`),
	},
	"javascript": {
		regexp.MustCompile(`(?m)(?:export\s+)?(?:async\s+)?function\s+(?P<name>[A-Za-z_$]\w*)\s*[<\(]`),
		regexp.MustCompile(`(?m)(?:export\s+)?class\s+(?P<name>[A-Za-z_$]\w*)\s*`),
		regexp.MustCompile(`(?m)(?:export\s+)?(?:const|let|var)\s+(?P<name>[A-Za-z_$]\w*)\s*=\s*(?:async\s+)?\(`),
		regexp.MustCompile(`(?m)(?:export\s+)?(?:const|let|var)\s+(?P<name>[A-Za-z_$]\w*)\s*=\s*(?:async\s+)?function`),
	},
	"rust": {
		regexp.MustCompile(`(?m)(?:pub\s+)?fn\s+(?P<name>[A-Za-z_]\w*)\s*[<\(]`),
		regexp.MustCompile(`(?m)(?:pub\s+)?struct\s+(?P<name>[A-Za-z_]\w*)\s*`),
		regexp.MustCompile(`(?m)(?:pub\s+)?enum\s+(?P<name>[A-Za-z_]\w*)\s*`),
		regexp.MustCompile(`(?m)(?:pub\s+)?trait\s+(?P<name>[A-Za-z_]\w*)\s*`),
		regexp.MustCompile(`(?m)(?:pub\s+)?type\s+(?P<name>[A-Za-z_]\w*)\s*=`),
	},
	"ruby": {
		regexp.MustCompile(`(?m)def\s+(?:self\.)?(?P<name>[A-Za-z_]\w*[?!]?)\s*[\(;]?`),
		regexp.MustCompile(`(?m)class\s+(?P<name>[A-Z]\w*)`),
		regexp.MustCompile(`(?m)module\s+(?P<name>[A-Z]\w*)`),
	},
	"vue": {
		regexp.MustCompile(`(?m)(?:export\s+)?(?:async\s+)?function\s+(?P<name>[A-Za-z_$]\w*)\s*[<\(]`),
		regexp.MustCompile(`(?m)(?:const|let|var)\s+(?P<name>[A-Za-z_$]\w*)\s*=\s*(?:async\s+)?\(`),
	},
}

// videoExts are the video formats browsers can play natively. They are not
// inlined into the read response; the player streams them from /files/raw.
var videoExts = map[string]string{
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",
	".ogv":  "video/ogg",
}

// rawMimeTypes are the Content-Types /files/raw may serve inline. Everything
// else is sent as an attachment so the browser never renders it (e.g. an
// .html or .svg from the project) on the Eggify origin.
var rawMimeTypes = map[string]string{
	".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm",
	".mov": "video/quicktime", ".ogv": "video/ogg",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg",
	".flac": "audio/flac", ".aac": "audio/aac", ".m4a": "audio/mp4",
	".opus": "audio/opus",
	".vtt":  "text/vtt; charset=utf-8",
	".png":  "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp",
}

// FileBrowserHandler handles file browsing endpoints for projects
type FileBrowserHandler struct {
	repo *database.Repository
}

// NewFileBrowserHandler creates a new file browser handler
func NewFileBrowserHandler(repo *database.Repository) *FileBrowserHandler {
	return &FileBrowserHandler{
		repo: repo,
	}
}

// skipDirs contains directory names to skip by default
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
}

// extensionLanguageMap maps file extensions to language identifiers
var extensionLanguageMap = map[string]string{
	".go":   "go",
	".vue":  "vue",
	".ts":   "typescript",
	".js":   "javascript",
	".json": "json",
	".md":   "markdown",
	".css":  "css",
	".html": "html",
	".py":   "python",
	".rs":   "rust",
	".yaml": "yaml",
	".yml":  "yaml",
	".toml": "toml",
	".sh":   "bash",
	".sql":  "sql",
}

// resolveProjectPath resolves relPath inside projectRoot and rejects anything
// that would land outside it: ".." traversal, sibling directories that merely
// share the root as a string prefix (/src/app vs /src/app-secrets), and
// symlinks pointing out of the project. The path need not exist yet (writes).
//
// It also refuses project roots no project may have ("/", the home folder,
// hidden folders such as ~/.claude/eggify; see checkProjectRoot), which
// covers rows created before project folders were validated.
func resolveProjectPath(projectRoot, relPath string) (string, error) {
	if err := checkProjectRoot(projectRoot); err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("invalid project root")
	}
	absPath := filepath.Join(absRoot, filepath.Clean(relPath))
	if !pathWithin(absRoot, absPath) {
		return "", fmt.Errorf("path outside project root")
	}

	// Resolve symlinks on both sides and check again.
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("invalid project root")
	}
	realPath, err := evalSymlinksAllowMissing(absPath)
	if err != nil {
		return "", fmt.Errorf("invalid path")
	}
	if !pathWithin(realRoot, realPath) {
		return "", fmt.Errorf("path outside project root")
	}

	return absPath, nil
}

// pathWithin reports whether target is root or inside it.
func pathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// evalSymlinksAllowMissing resolves symlinks in the longest existing prefix
// of p and appends the not-yet-existing remainder unchanged.
func evalSymlinksAllowMissing(p string) (string, error) {
	var missing []string
	cur := p
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
	}
}

// detectLanguage returns a language identifier for a file extension
func detectLanguage(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if lang, ok := extensionLanguageMap[ext]; ok {
		return lang
	}
	return "text"
}

// base64Encode encodes data to a base64 string
func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// isBinaryContent checks if data contains null bytes (indicating binary content)
func isBinaryContent(data []byte) bool {
	// Check first 512 bytes for null bytes
	checkLen := len(data)
	if checkLen > 512 {
		checkLen = 512
	}
	return bytes.Contains(data[:checkLen], []byte{0})
}

// HandleListFiles handles GET /api/projects/:id/files
// Lists directory entries at a given path within a project
func (h *FileBrowserHandler) HandleListFiles(c *fiber.Ctx) error {
	projectID := c.Params("id")

	project, err := h.repo.GetProject(projectID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{
			"error": "Project not found",
		})
	}

	relPath := c.Query("path", "")

	absPath, pathErr := resolveProjectPath(project.Path, relPath)
	if pathErr != nil {
		return c.Status(400).JSON(fiber.Map{
			"error": pathErr.Error(),
		})
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return c.Status(404).JSON(fiber.Map{
				"error": "Path not found",
			})
		}
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to stat path: %v", err),
		})
	}

	if !info.IsDir() {
		return c.Status(400).JSON(fiber.Map{
			"error": "Path is not a directory",
		})
	}

	dirEntries, err := os.ReadDir(absPath)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to read directory: %v", err),
		})
	}

	type FileEntry struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Size     int64  `json:"size"`
		Modified string `json:"modified"`
	}

	var dirs []FileEntry
	var files []FileEntry

	for _, entry := range dirEntries {
		name := entry.Name()

		// Skip default directories
		if entry.IsDir() && skipDirs[name] {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		fe := FileEntry{
			Name:     name,
			Size:     info.Size(),
			Modified: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
		}

		if entry.IsDir() {
			fe.Type = "dir"
			dirs = append(dirs, fe)
		} else {
			fe.Type = "file"
			files = append(files, fe)
		}
	}

	// Sort alphabetically within each group
	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})

	// Directories first, then files
	entries := append(dirs, files...)

	return c.JSON(fiber.Map{
		"path":    relPath,
		"entries": entries,
	})
}

// HandleReadFile handles GET /api/projects/:id/files/read
// Reads the content of a file within a project
func (h *FileBrowserHandler) HandleReadFile(c *fiber.Ctx) error {
	projectID := c.Params("id")

	project, err := h.repo.GetProject(projectID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{
			"error": "Project not found",
		})
	}

	relPath := c.Query("path")
	if relPath == "" {
		return c.Status(400).JSON(fiber.Map{
			"error": "path query parameter is required",
		})
	}

	maxLines := c.QueryInt("maxLines", 500)

	absPath, pathErr := resolveProjectPath(project.Path, relPath)
	if pathErr != nil {
		return c.Status(400).JSON(fiber.Map{
			"error": pathErr.Error(),
		})
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return c.Status(404).JSON(fiber.Map{
				"error": "File not found",
			})
		}
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to stat file: %v", err),
		})
	}

	if info.IsDir() {
		return c.Status(400).JSON(fiber.Map{
			"error": "Path is a directory, not a file",
		})
	}

	const maxTextSize = 3 * 1024 * 1024   // 3MB for text files
	const maxImageSize = 12 * 1024 * 1024 // 12MB for image files

	// Check if the file is an image
	ext := strings.ToLower(filepath.Ext(info.Name()))
	imageExts := map[string]string{
		".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".gif": "image/gif", ".svg": "image/svg+xml", ".webp": "image/webp",
		".ico": "image/x-icon", ".bmp": "image/bmp",
	}
	audioExts := map[string]string{
		".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg",
		".flac": "audio/flac", ".aac": "audio/aac", ".m4a": "audio/mp4",
		".wma": "audio/x-ms-wma", ".opus": "audio/opus",
	}
	mimeType, isImage := imageExts[ext]
	audioMimeType, isAudio := audioExts[ext]

	if isImage {
		// Image file handling
		if info.Size() > maxImageSize {
			return c.JSON(fiber.Map{
				"path":     relPath,
				"content":  "",
				"language": "image",
				"size":     info.Size(),
				"binary":   true,
				"image":    false,
				"tooLarge": true,
				"message":  "Image too large for preview (max 12 MB)",
			})
		}

		data, err := os.ReadFile(absPath)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{
				"error": fmt.Sprintf("Failed to read file: %v", err),
			})
		}

		// For SVGs, return as text content
		if ext == ".svg" {
			return c.JSON(fiber.Map{
				"path":     relPath,
				"content":  string(data),
				"language": "svg",
				"size":     info.Size(),
				"binary":   false,
				"image":    true,
				"mimeType": mimeType,
			})
		}

		// For raster images, return base64 data URI
		encoded := base64Encode(data)
		dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)

		return c.JSON(fiber.Map{
			"path":     relPath,
			"content":  dataURI,
			"language": "image",
			"size":     info.Size(),
			"binary":   false,
			"image":    true,
			"mimeType": mimeType,
		})
	}

	// Video: return metadata only. The player streams the bytes (with range
	// requests, so seeking works) from /files/raw instead of a data URI.
	if videoMime, isVideo := videoExts[ext]; isVideo {
		return c.JSON(fiber.Map{
			"path":      relPath,
			"content":   "",
			"language":  "video",
			"size":      info.Size(),
			"binary":    false,
			"video":     true,
			"mimeType":  videoMime,
			"subtitles": findSubtitleTracks(absPath, relPath),
		})
	}

	// Audio file handling
	if isAudio {
		const maxAudioSize = 50 * 1024 * 1024 // 50MB for audio files
		if info.Size() > maxAudioSize {
			return c.JSON(fiber.Map{
				"path":     relPath,
				"content":  "",
				"language": "audio",
				"size":     info.Size(),
				"binary":   true,
				"audio":    false,
				"tooLarge": true,
				"message":  "Audio file too large for playback (max 50 MB)",
			})
		}

		data, err := os.ReadFile(absPath)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{
				"error": fmt.Sprintf("Failed to read file: %v", err),
			})
		}

		encoded := base64Encode(data)
		dataURI := fmt.Sprintf("data:%s;base64,%s", audioMimeType, encoded)

		return c.JSON(fiber.Map{
			"path":     relPath,
			"content":  dataURI,
			"language": "audio",
			"size":     info.Size(),
			"binary":   false,
			"audio":    true,
			"mimeType": audioMimeType,
		})
	}

	// Text file handling
	if info.Size() > maxTextSize {
		return c.JSON(fiber.Map{
			"path":     relPath,
			"content":  "",
			"language": detectLanguage(info.Name()),
			"size":     info.Size(),
			"binary":   true,
			"tooLarge": true,
			"message":  "File too large for preview (max 3 MB)",
		})
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to read file: %v", err),
		})
	}

	// Check for binary content
	if isBinaryContent(data) {
		return c.JSON(fiber.Map{
			"path":     relPath,
			"content":  "",
			"language": detectLanguage(info.Name()),
			"size":     info.Size(),
			"binary":   true,
		})
	}

	content := string(data)

	// Truncate to maxLines
	if maxLines > 0 {
		lines := strings.SplitN(content, "\n", maxLines+1)
		if len(lines) > maxLines {
			content = strings.Join(lines[:maxLines], "\n")
		}
	}

	return c.JSON(fiber.Map{
		"path":     relPath,
		"content":  content,
		"language": detectLanguage(info.Name()),
		"size":     info.Size(),
		"binary":   false,
	})
}

// HandleDownloadFile handles GET /api/projects/:id/files/download
// Serves a file for direct download
func (h *FileBrowserHandler) HandleDownloadFile(c *fiber.Ctx) error {
	projectID := c.Params("id")

	project, err := h.repo.GetProject(projectID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{
			"error": "Project not found",
		})
	}

	relPath := c.Query("path")
	if relPath == "" {
		return c.Status(400).JSON(fiber.Map{
			"error": "path query parameter is required",
		})
	}

	absPath, pathErr := resolveProjectPath(project.Path, relPath)
	if pathErr != nil {
		return c.Status(400).JSON(fiber.Map{
			"error": pathErr.Error(),
		})
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return c.Status(404).JSON(fiber.Map{
				"error": "File not found",
			})
		}
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to stat file: %v", err),
		})
	}

	if info.IsDir() {
		return c.Status(400).JSON(fiber.Map{
			"error": "Path is a directory, not a file",
		})
	}

	filename := filepath.Base(absPath)
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	return c.SendFile(absPath)
}

// HandleRawFile handles GET /api/projects/:id/files/raw
// Serves a media file inline so <video>/<audio>/<track> elements can stream
// it. Range requests are supported (seeking). Only known media types are
// served inline; anything else is forced to download.
func (h *FileBrowserHandler) HandleRawFile(c *fiber.Ctx) error {
	project, err := h.repo.GetProject(c.Params("id"))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Project not found"})
	}

	relPath := c.Query("path")
	if relPath == "" {
		return c.Status(400).JSON(fiber.Map{"error": "path query parameter is required"})
	}

	absPath, pathErr := resolveProjectPath(project.Path, relPath)
	if pathErr != nil {
		return c.Status(400).JSON(fiber.Map{"error": pathErr.Error()})
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return c.Status(404).JSON(fiber.Map{"error": "File not found"})
		}
		return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Failed to stat file: %v", err)})
	}
	if info.IsDir() {
		return c.Status(400).JSON(fiber.Map{"error": "Path is a directory, not a file"})
	}

	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Cache-Control", "private, no-cache")
	if mime, ok := rawMimeTypes[strings.ToLower(filepath.Ext(absPath))]; ok {
		c.Set("Content-Disposition", "inline")
		c.Set("Content-Type", mime)
	} else {
		c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(absPath)))
	}

	if err := c.SendFile(absPath); err != nil {
		return err
	}
	// SendFile sets Content-Type from the extension; keep ours (e.g. .mov
	// is not in every system mime table).
	if mime, ok := rawMimeTypes[strings.ToLower(filepath.Ext(absPath))]; ok {
		c.Set("Content-Type", mime)
	}
	return nil
}

// SubtitleTrack is a WebVTT file shown alongside a video.
type SubtitleTrack struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Lang  string `json:"lang,omitempty"`
}

// findSubtitleTracks returns the WebVTT files next to a video that share its
// base name: movie.vtt, movie.en.vtt, movie.da.vtt, ...
func findSubtitleTracks(absVideo, relVideo string) []SubtitleTrack {
	tracks := []SubtitleTrack{}
	dir := filepath.Dir(absVideo)
	base := strings.TrimSuffix(filepath.Base(absVideo), filepath.Ext(absVideo))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return tracks
	}
	relDir := filepath.Dir(relVideo)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.EqualFold(filepath.Ext(name), ".vtt") {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if stem != base && !strings.HasPrefix(stem, base+".") {
			continue
		}
		lang := strings.TrimPrefix(strings.TrimPrefix(stem, base), ".")
		label := lang
		if label == "" {
			label = "Subtitles"
		}
		tracks = append(tracks, SubtitleTrack{
			Path:  filepath.ToSlash(filepath.Join(relDir, name)),
			Label: label,
			Lang:  lang,
		})
	}
	return tracks
}

// HandleSearchFiles handles GET /api/projects/:id/files/search
// Searches for files matching a query within a project
func (h *FileBrowserHandler) HandleSearchFiles(c *fiber.Ctx) error {
	projectID := c.Params("id")

	project, err := h.repo.GetProject(projectID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{
			"error": "Project not found",
		})
	}

	if err := checkProjectRoot(project.Path); err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error()})
	}

	query := c.Query("q")
	if query == "" {
		return c.Status(400).JSON(fiber.Map{
			"error": "q query parameter is required",
		})
	}

	limit := c.QueryInt("limit", 20)
	if limit > 100 {
		limit = 100
	}
	if limit < 1 {
		limit = 1
	}

	queryLower := strings.ToLower(query)

	// Collect file paths (git ls-files, or a capped walk; cached briefly)
	filePaths := projectFileList(project.Path)

	type SearchResult struct {
		Path  string  `json:"path"`
		Type  string  `json:"type"`
		Score float64 `json:"score"`
	}

	var results []SearchResult

	for _, fp := range filePaths {
		fpLower := strings.ToLower(fp)

		// Skip excluded paths
		if strings.Contains(fpLower, ".git/") || strings.Contains(fpLower, "node_modules/") {
			continue
		}

		// Substring match (case-insensitive)
		if !strings.Contains(fpLower, queryLower) {
			continue
		}

		// Score the result
		var score float64
		baseName := strings.ToLower(filepath.Base(fp))

		if baseName == queryLower {
			// Exact filename match
			score = 1.0
		} else if strings.HasPrefix(baseName, queryLower) {
			// Filename prefix match
			score = 0.8
		} else if strings.Contains(baseName, queryLower) {
			// Filename contains match
			score = 0.6
		} else {
			// Path contains match
			score = 0.4
		}

		results = append(results, SearchResult{
			Path:  fp,
			Type:  "file",
			Score: score,
		})
	}

	// Sort by score descending, then alphabetically
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Path < results[j].Path
	})

	// Apply limit
	if len(results) > limit {
		results = results[:limit]
	}

	// Round scores for cleaner JSON
	for i := range results {
		score, _ := strconv.ParseFloat(fmt.Sprintf("%.2f", results[i].Score), 64)
		results[i].Score = score
	}

	return c.JSON(fiber.Map{
		"query":   query,
		"results": results,
	})
}

// HandleFindSymbol handles GET /api/projects/:id/files/symbol
// Searches for a symbol definition across the project files
func (h *FileBrowserHandler) HandleFindSymbol(c *fiber.Ctx) error {
	projectID := c.Params("id")

	project, err := h.repo.GetProject(projectID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{
			"error": "Project not found",
		})
	}

	if err := checkProjectRoot(project.Path); err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error()})
	}

	symbol := c.Query("name")
	if symbol == "" {
		return c.Status(400).JSON(fiber.Map{
			"error": "name query parameter is required",
		})
	}

	// Collect file paths (git ls-files, or a capped walk; cached briefly)
	filePaths := projectFileList(project.Path)

	type SymbolResult struct {
		Path     string `json:"path"`
		Line     int    `json:"line"`
		Column   int    `json:"column"`
		Kind     string `json:"kind"`
		Context  string `json:"context"`
		Language string `json:"language"`
	}

	var results []SymbolResult
	maxResults := 20

	for _, fp := range filePaths {
		if len(results) >= maxResults {
			break
		}

		// Skip excluded paths
		fpLower := strings.ToLower(fp)
		if strings.Contains(fpLower, ".git/") || strings.Contains(fpLower, "node_modules/") {
			continue
		}

		lang := detectLanguage(fp)
		patterns, ok := symbolPatterns[lang]
		if !ok {
			continue
		}

		data, ok := readSymbolCandidate(filepath.Join(project.Path, fp))
		if !ok {
			continue
		}
		for _, m := range findSymbolInFile(data, symbol, patterns, maxResults-len(results)) {
			results = append(results, SymbolResult{
				Path:     fp,
				Line:     m.Line,
				Column:   m.Column,
				Kind:     m.Kind,
				Context:  m.Context,
				Language: lang,
			})
		}
	}

	return c.JSON(fiber.Map{
		"symbol":  symbol,
		"results": results,
	})
}

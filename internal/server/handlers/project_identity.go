package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

// A project's identity is what the project picker shows for it besides the
// name: a logo found in the folder (app icon, favicon, logo file) and a
// one-line summary (package.json description or the README's first
// paragraph). Both are looked up with a fixed list of paths, so a lookup is a
// few dozen stats, and cached per folder.

const (
	projectIconMaxBytes     = 1 << 20
	projectIdentityCacheTTL = 2 * time.Minute
	projectSummaryMaxRunes  = 180
	projectReadmeMaxBytes   = 64 << 10
)

var projectIconTypes = map[string]string{
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".ico":  "image/x-icon",
	".webp": "image/webp",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
}

// Folders that commonly hold a site's public assets, in the order they are
// tried. "" is the project root.
var projectIconDirs = []string{
	"", "public", "static", "assets", "web/public", "frontend/public", "app/public",
	"client/public", "site", "docs/public", "website/public", "landing/public",
	"src/assets", "src/app", "app", ".github",
}

// File names tried in each folder, best (square, high resolution) first.
var projectIconNames = []string{
	"apple-touch-icon.png", "favicon.svg", "icon.svg", "logo.svg",
	"favicon-512x512.png", "android-chrome-512x512.png", "icon-512.png", "favicon-192x192.png",
	"android-chrome-192x192.png", "icon.png", "logo.png", "favicon.png", "favicon.ico",
}

// Native app icons, tried before the web folders. Patterns are globbed
// relative to the project root.
var projectAppIconGlobs = []string{
	"*/Assets.xcassets/AppIcon.appiconset",
	"ios/*/Assets.xcassets/AppIcon.appiconset",
	"ios/*/*/Assets.xcassets/AppIcon.appiconset",
	"macos/*/Assets.xcassets/AppIcon.appiconset",
}

var projectAppIconFiles = []string{
	"src-tauri/icons/icon.png", "src-tauri/icons/128x128@2x.png", "src-tauri/icons/128x128.png",
	"build/appicon.png", "assets/icon.png", "resources/icon.png",
}

type projectIdentity struct {
	IconPath string // absolute path of the logo, "" for none
	IconMod  int64  // logo mtime, used to bust the browser cache
	Summary  string
}

type cachedIdentity struct {
	id projectIdentity
	at time.Time
}

var identityCache = struct {
	mu sync.Mutex
	m  map[string]cachedIdentity
}{m: map[string]cachedIdentity{}}

// lookupProjectIdentity returns the cached identity of a project folder,
// looking it up again when the cache entry is older than the TTL.
func lookupProjectIdentity(root string) projectIdentity {
	identityCache.mu.Lock()
	if c, ok := identityCache.m[root]; ok && time.Since(c.at) < projectIdentityCacheTTL {
		identityCache.mu.Unlock()
		return c.id
	}
	identityCache.mu.Unlock()

	id := projectIdentity{Summary: findProjectSummary(root)}
	if p := findProjectIcon(root); p != "" {
		id.IconPath = p
		if info, err := os.Stat(p); err == nil {
			id.IconMod = info.ModTime().Unix()
		}
	}

	identityCache.mu.Lock()
	identityCache.m[root] = cachedIdentity{id: id, at: time.Now()}
	identityCache.mu.Unlock()
	return id
}

// findProjectIcon returns the absolute path of the best logo in the project
// folder, or "" when there is none. Only regular image files inside the
// folder (after resolving symlinks) and under 1 MiB are considered.
func findProjectIcon(root string) string {
	try := func(rel string) string {
		if _, ok := projectIconTypes[strings.ToLower(filepath.Ext(rel))]; !ok {
			return ""
		}
		abs, err := resolveProjectPath(root, rel)
		if err != nil {
			return ""
		}
		info, err := os.Stat(abs)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > projectIconMaxBytes {
			return ""
		}
		return abs
	}

	for _, pattern := range projectAppIconGlobs {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		sort.Strings(matches)
		for _, dir := range matches {
			if rel, err := filepath.Rel(root, dir); err == nil {
				if p := largestAppIcon(root, rel, try); p != "" {
					return p
				}
			}
		}
	}
	for _, rel := range projectAppIconFiles {
		if p := try(rel); p != "" {
			return p
		}
	}
	for _, dir := range projectIconDirs {
		for _, name := range projectIconNames {
			if p := try(filepath.Join(dir, name)); p != "" {
				return p
			}
		}
	}
	return ""
}

// largestAppIcon picks the biggest PNG of an Xcode AppIcon set (by file size,
// which tracks resolution closely enough for a 40 px tile).
func largestAppIcon(root, relDir string, try func(string) string) string {
	entries, err := os.ReadDir(filepath.Join(root, relDir))
	if err != nil {
		return ""
	}
	best, bestSize := "", int64(0)
	for _, e := range entries {
		if !strings.EqualFold(filepath.Ext(e.Name()), ".png") {
			continue
		}
		p := try(filepath.Join(relDir, e.Name()))
		if p == "" {
			continue
		}
		if info, err := os.Stat(p); err == nil && info.Size() > bestSize {
			best, bestSize = p, info.Size()
		}
	}
	return best
}

var (
	mdLinkRe     = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	mdHTMLRe     = regexp.MustCompile(`<[^>]+>`)
	mdEmphasisRe = regexp.MustCompile("[*_`]+")
	spaceRe      = regexp.MustCompile(`\s+`)
)

// findProjectSummary returns a one-line description of the project: the
// package.json description, else the first prose paragraph of the README.
func findProjectSummary(root string) string {
	if b, err := readSmallFile(root, "package.json", projectReadmeMaxBytes); err == nil {
		var pkg struct {
			Description string `json:"description"`
		}
		if json.Unmarshal(b, &pkg) == nil && strings.TrimSpace(pkg.Description) != "" {
			return clampRunes(cleanSummary(pkg.Description), projectSummaryMaxRunes)
		}
	}
	for _, name := range []string{"README.md", "readme.md", "Readme.md", "README", "README.txt"} {
		b, err := readSmallFile(root, name, projectReadmeMaxBytes)
		if err != nil {
			continue
		}
		if s := readmeSummary(string(b)); s != "" {
			return clampRunes(s, projectSummaryMaxRunes)
		}
		return ""
	}
	return ""
}

func readSmallFile(root, rel string, max int64) ([]byte, error) {
	abs, err := resolveProjectPath(root, rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a file")
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max))
}

// readmeSummary returns the first paragraph of a README that reads like
// prose: not a heading, badge row, code block, table, list, or HTML block.
func readmeSummary(text string) string {
	var para []string
	inFence := false
	flush := func() string {
		s := cleanSummary(strings.Join(para, " "))
		para = para[:0]
		// Need a few words of real text (not just a leftover badge alt text),
		// and not a disclaimer.
		lower := strings.ToLower(s)
		if len(strings.Fields(s)) >= 4 && !strings.HasPrefix(s, "⚠") &&
			!strings.Contains(lower, "not affiliated") && !strings.HasPrefix(lower, "note:") {
			return s
		}
		return ""
	}
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inFence = !inFence
			para = para[:0]
			continue
		}
		if inFence {
			continue
		}
		skip := line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "|") ||
			strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "+") ||
			strings.HasPrefix(line, "<") || strings.HasPrefix(line, "![") || strings.HasPrefix(line, "[![") ||
			strings.HasPrefix(line, "=")
		if skip {
			if len(para) > 0 {
				if s := flush(); s != "" {
					return s
				}
			}
			continue
		}
		para = append(para, strings.TrimPrefix(line, "> "))
	}
	if len(para) > 0 {
		return flush()
	}
	return ""
}

func cleanSummary(s string) string {
	s = mdLinkRe.ReplaceAllString(s, "$1")
	s = mdHTMLRe.ReplaceAllString(s, "")
	s = mdEmphasisRe.ReplaceAllString(s, "")
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

func clampRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := string(r[:max])
	if i := strings.LastIndex(cut, " "); i > max/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:-") + "…"
}

// projectIconURL is the URL the picker loads a project's logo from, with the
// file's mtime so a changed logo is fetched again.
func projectIconURL(projectID string, id projectIdentity) string {
	if id.IconPath == "" {
		return ""
	}
	return fmt.Sprintf("/api/projects/%s/icon?v=%d", projectID, id.IconMod)
}

// HandleProjectIcon handles GET /api/projects/:id/icon: the logo found in the
// project folder (see findProjectIcon), 404 when there is none. SVGs are sent
// with a CSP that blocks scripts, in case one is opened directly.
func (h *ProjectHandler) HandleProjectIcon(c *fiber.Ctx) error {
	project, err := h.repo.GetProject(c.Params("id"))
	if err != nil || project == nil {
		return c.Status(404).JSON(fiber.Map{"error": "Project not found"})
	}
	id := lookupProjectIdentity(project.Path)
	if id.IconPath == "" {
		return c.Status(404).JSON(fiber.Map{"error": "No icon"})
	}
	b, err := os.ReadFile(id.IconPath)
	if err != nil || len(b) > projectIconMaxBytes {
		return c.Status(404).JSON(fiber.Map{"error": "No icon"})
	}
	c.Set("Content-Type", projectIconTypes[strings.ToLower(filepath.Ext(id.IconPath))])
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox")
	c.Set("Cache-Control", "private, max-age=86400")
	return c.Send(b)
}

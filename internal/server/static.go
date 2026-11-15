// Package server provides static file serving for the analytics dashboard.
// This file embeds the Nuxt-generated static files from frontend/.output/public
package server

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
)

//go:embed all:frontend/.output/public
var frontendFiles embed.FS

// embeddedPublicFS is the generated frontend (frontend/.output/public).
func embeddedPublicFS() (fs.FS, error) {
	return fs.Sub(frontendFiles, "frontend/.output/public")
}

// ServeStaticFiles adds static file serving to the server
func (s *Server) ServeStaticFiles() {
	// Get the public subdirectory from the embedded filesystem
	publicFS, err := embeddedPublicFS()
	if err != nil {
		panic("Failed to load embedded frontend files: " + err.Error())
	}

	// The dashboard's own scripts, styles and HTML go out compressed (about
	// a quarter of the bytes), which matters when another Mac loads them
	// through the Eggify relay. Media, images and wasm are left alone.
	s.app.Use(compress.New(compress.Config{
		Level: compress.LevelDefault,
		Next:  func(c *fiber.Ctx) bool { return !compressibleStatic(c) },
	}))

	// Serve static files from embedded filesystem using http.FS wrapper
	// Important: Only serve static files for non-API routes
	s.app.Use("/", func(c *fiber.Ctx) error {
		path := c.Path()
		// Skip filesystem middleware for API and WebSocket routes - they're handled by API handlers
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/ws") {
			return c.Next()
		}

		maxAge := 0
		if immutableAsset(path) {
			// Content-hashed file names: a new build has new names, so the
			// browser never needs to ask again (machine switches and
			// reloads load the dashboard from its cache).
			maxAge = 31536000
		}
		// Serve static files for everything else
		err := filesystem.New(filesystem.Config{
			Root:         http.FS(publicFS),
			Browse:       false,
			Index:        "index.html",
			NotFoundFile: "404.html",
			MaxAge:       maxAge,
		})(c)
		if err == nil {
			switch {
			case maxAge > 0 && c.Response().StatusCode() == fiber.StatusOK:
				c.Set(fiber.HeaderCacheControl, "public, max-age=31536000, immutable")
			case strings.HasPrefix(string(c.Response().Header.ContentType()), "text/html"):
				// The HTML shell is always revalidated, so a new build's
				// asset names are picked up at once.
				c.Set(fiber.HeaderCacheControl, "no-cache")
			}
		}
		return err
	})

	// Fallback to index.html for SPA routes (must come after API/WS routes)
	s.app.Use(func(c *fiber.Ctx) error {
		// Skip for API routes and WebSocket
		path := c.Path()
		if strings.HasPrefix(path, "/api") || strings.HasPrefix(path, "/ws") {
			return c.Next()
		}

		// Check if file exists in embedded FS
		if _, err := publicFS.Open(strings.TrimPrefix(path, "/")); err == nil {
			return c.Next()
		}

		// Serve index.html for non-existent routes (SPA fallback)
		indexContent, err := fs.ReadFile(publicFS, "index.html")
		if err != nil {
			return c.Status(404).SendString("404 Not Found")
		}

		c.Set("Content-Type", "text/html; charset=utf-8")
		c.Set(fiber.HeaderCacheControl, "no-cache")
		return c.Send(indexContent)
	})
}

// immutableAsset reports whether path is one of Nuxt's content-hashed build
// files (/_nuxt/<name>.<hash>.<ext> or /_nuxt/<hash>.<ext>). The build
// manifest (/_nuxt/builds/...) changes in place and is not one.
func immutableAsset(path string) bool {
	rest, ok := strings.CutPrefix(path, "/_nuxt/")
	if !ok || rest == "" || strings.Contains(rest, "/") {
		return false
	}
	dot := strings.LastIndexByte(rest, '.')
	if dot <= 0 {
		return false
	}
	stem := rest[:dot]
	// The hash is the stem's last 8 characters, the whole stem or after a
	// '.' or '-' (Vite: [hash], [name].[hash], [name]-[hash]).
	if len(stem) < 8 || (len(stem) > 8 && !strings.ContainsRune(".-", rune(stem[len(stem)-9]))) {
		return false
	}
	stem = stem[len(stem)-8:]
	for _, c := range stem {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// compressibleStatic: GETs of the frontend's text files (scripts, styles,
// JSON, SVG and the HTML pages), never /api or WebSockets.
func compressibleStatic(c *fiber.Ctx) bool {
	if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
		return false
	}
	p := c.Path()
	if strings.HasPrefix(p, "/api") || strings.HasPrefix(p, "/ws") || strings.HasPrefix(p, "/agent/ws") ||
		strings.HasPrefix(p, "/internal") || strings.HasPrefix(p, "/wails") || strings.HasPrefix(p, "/__") {
		return false
	}
	dot := strings.LastIndexByte(p, '.')
	if dot < strings.LastIndexByte(p, '/') {
		return true // a page route: the SPA shell
	}
	switch p[dot:] {
	case ".js", ".mjs", ".css", ".json", ".svg", ".html", ".txt", ".map":
		return true
	}
	return false
}

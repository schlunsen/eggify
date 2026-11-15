package handlers

import (
	"errors"
	"fmt"
	"os"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/sessionmedia"
)

// HandleGetSessionImage serves one image a session's agent showed in its reply
// (GET /api/agent/sessions/:id/image?path=output/chart.png). The path is
// relative to the session's working directory (or absolute, or ~/) and must
// pass sessionmedia's folder rules: inside the working directory, its project
// or its Claude scratchpad, never ~/.ssh, ~/.claude and the like, an image by
// extension and by its first bytes, at most 50 MB. Read-only, API key allowed.
func (h *AgentSessionHandler) HandleGetSessionImage(c *fiber.Ctx) error {
	if h.agentHandler == nil || h.agentHandler.SessionManager == nil {
		return c.Status(503).JSON(fiber.Map{"error": "agent handler not initialized"})
	}
	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid session ID"})
	}
	meta, err := h.agentHandler.SessionManager.Storage.GetSession(sessionID)
	if err != nil || meta == nil {
		return c.Status(404).JSON(fiber.Map{"error": "session not found"})
	}
	// SECURITY: the files are the session's work.
	if !h.AuthorizeSession(c, sessionID) {
		return nil
	}
	ref := c.Query("path")
	if ref == "" || len(ref) > 4096 {
		return c.Status(400).JSON(fiber.Map{"error": "path required"})
	}
	real, info, err := h.sessionMediaRoots(meta).ResolveImage(ref)
	switch {
	case errors.Is(err, sessionmedia.ErrOutside), errors.Is(err, sessionmedia.ErrProtected):
		return c.Status(403).JSON(fiber.Map{"error": "image " + err.Error()})
	case errors.Is(err, sessionmedia.ErrTooLarge):
		return c.Status(413).JSON(fiber.Map{"error": err.Error()})
	case err != nil:
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}

	etag := fmt.Sprintf(`"%x-%x"`, info.Size(), info.ModTime().UnixNano())
	c.Set("ETag", etag)
	c.Set("Cache-Control", "private, no-cache") // revalidate: agents rewrite files in place
	c.Set("X-Content-Type-Options", "nosniff")
	if c.Get("If-None-Match") == etag {
		return c.SendStatus(fiber.StatusNotModified)
	}
	mime := sessionmedia.ImageMimeType(real)
	if mime == "image/svg+xml" {
		// Shown through <img> (no scripts there), but opened on its own an SVG
		// is a document: no scripts, no loads, no navigation.
		c.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox")
	}
	f, err := os.Open(real)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "file not found"})
	}
	c.Set("Content-Type", mime)
	return c.SendStream(f, int(info.Size()))
}

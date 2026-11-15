package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/agents"
	"github.com/schlunsen/eggify/internal/sessionmedia"
)

// Media a session made (audio and video renders), for the chat's players.
//
//	GET  /api/agent/sessions/:id/media                    → {items, ffmpeg}
//	GET  /api/agent/sessions/:id/media/:mediaId           → the file (Range works); ?variant=compatible for the converted copy
//	GET  /api/agent/sessions/:id/media/:mediaId/poster    → a JPEG frame of a video (needs ffmpeg)
//	POST /api/agent/sessions/:id/media/:mediaId/convert   → start an iPhone-friendly copy → {state}
//
// Only files the session itself named, inside its folders, are listed or
// served (see internal/sessionmedia). Read-only, so the API key may use it.

// sessionMediaScanLimit is how many of a session's latest messages are scanned.
const sessionMediaScanLimit = 3000

type sessionMediaCacheEntry struct {
	key   string
	at    time.Time
	items []sessionmedia.Item
}

var (
	sessionMediaCacheMu sync.Mutex
	sessionMediaCache   = map[uuid.UUID]sessionMediaCacheEntry{}
	sessionMediaToolsMu sync.Mutex
	sessionMediaTools   *sessionmedia.Tools
)

func (h *AgentSessionHandler) mediaTools() *sessionmedia.Tools {
	sessionMediaToolsMu.Lock()
	defer sessionMediaToolsMu.Unlock()
	if sessionMediaTools == nil {
		home, _ := os.UserHomeDir()
		sessionMediaTools = &sessionmedia.Tools{Dir: filepath.Join(home, ".claude", "eggify", "media-cache")}
	}
	return sessionMediaTools
}

// sessionMediaRoots returns the folders a session's media may come from.
func (h *AgentSessionHandler) sessionMediaRoots(meta *agents.SessionMetadata) sessionmedia.Roots {
	home, _ := os.UserHomeDir()
	roots := sessionmedia.Roots{Home: home, TempDirs: []string{"/tmp", "/private/tmp"}, UID: os.Getuid()}
	var opts struct {
		WorkingDirectory *string `json:"working_directory"`
	}
	if meta.OptionsJSON != "" && json.Unmarshal([]byte(meta.OptionsJSON), &opts) == nil && opts.WorkingDirectory != nil {
		roots.WorkingDir = *opts.WorkingDirectory
	}
	if meta.ProjectID != nil && *meta.ProjectID != "" && h.repo != nil {
		if p, err := h.repo.GetProject(*meta.ProjectID); err == nil && p != nil {
			roots.ProjectDir = p.Path
			if roots.WorkingDir == "" {
				roots.WorkingDir = p.Path
			}
		}
	}
	return roots
}

// sessionMedia loads (or reuses for a few seconds) a session's media list.
// It answers itself on errors and returns ok=false then.
func (h *AgentSessionHandler) sessionMedia(c *fiber.Ctx) (uuid.UUID, []sessionmedia.Item, bool) {
	if h.agentHandler == nil || h.agentHandler.SessionManager == nil {
		_ = c.Status(503).JSON(fiber.Map{"error": "agent handler not initialized"})
		return uuid.Nil, nil, false
	}
	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		_ = c.Status(400).JSON(fiber.Map{"error": "invalid session ID"})
		return uuid.Nil, nil, false
	}
	storage := h.agentHandler.SessionManager.Storage
	meta, err := storage.GetSession(sessionID)
	if err != nil || meta == nil {
		_ = c.Status(404).JSON(fiber.Map{"error": "session not found"})
		return uuid.Nil, nil, false
	}
	// SECURITY: the files are the session's work.
	if !h.AuthorizeSession(c, sessionID) {
		return uuid.Nil, nil, false
	}

	count, _ := storage.GetMessageCount(sessionID)
	key := fmt.Sprintf("%d:%s", count, meta.UpdatedAt.Format(time.RFC3339Nano))
	sessionMediaCacheMu.Lock()
	cached, hit := sessionMediaCache[sessionID]
	sessionMediaCacheMu.Unlock()
	if hit && cached.key == key && time.Since(cached.at) < 5*time.Second {
		return sessionID, cached.items, true
	}

	records, _, err := storage.GetLatestMessages(sessionID, sessionMediaScanLimit, 0)
	if err != nil {
		logging.Warning("Session media: failed to read messages of %s: %v", sessionID, err)
		_ = c.Status(500).JSON(fiber.Map{"error": "failed to read the session's messages"})
		return uuid.Nil, nil, false
	}
	messages := make([]sessionmedia.Message, 0, len(records))
	for _, r := range records {
		messages = append(messages, sessionmedia.Message{ID: r.ID.String(), Sequence: r.Sequence, Role: r.Role,
			Content: r.Content, ToolUses: r.ToolUses, At: r.Timestamp})
	}
	items := sessionmedia.Collect(sessionmedia.FindReferences(messages), h.sessionMediaRoots(meta))

	sessionMediaCacheMu.Lock()
	sessionMediaCache[sessionID] = sessionMediaCacheEntry{key: key, at: time.Now(), items: items}
	sessionMediaCacheMu.Unlock()
	return sessionID, items, true
}

func findMediaItem(items []sessionmedia.Item, id string) (sessionmedia.Item, bool) {
	for _, it := range items {
		if it.ID == id {
			return it, true
		}
	}
	return sessionmedia.Item{}, false
}

type sessionMediaItemJSON struct {
	sessionmedia.Item
	URL        string `json:"url"`
	PosterURL  string `json:"poster_url,omitempty"`
	Compatible string `json:"compatible,omitempty"` // conversion state: running | ready | failed
	CompatURL  string `json:"compatible_url,omitempty"`
	CompatErr  string `json:"compatible_error,omitempty"`
}

// HandleListSessionMedia lists the session's audio and video files, newest first.
func (h *AgentSessionHandler) HandleListSessionMedia(c *fiber.Ctx) error {
	sessionID, items, ok := h.sessionMedia(c)
	if !ok {
		return nil
	}
	tools := h.mediaTools()
	ffmpeg := tools.Available()
	base := "/api/agent/sessions/" + sessionID.String() + "/media/"
	out := make([]sessionMediaItemJSON, 0, len(items))
	for _, it := range items {
		j := sessionMediaItemJSON{Item: it, URL: base + it.ID}
		if it.Kind == sessionmedia.Video && ffmpeg {
			j.PosterURL = base + it.ID + "/poster"
		}
		st := tools.ConvertStatus(it)
		j.Compatible, j.CompatErr = st.State, st.Error
		if st.State == "ready" {
			j.CompatURL = base + it.ID + "?variant=compatible"
		}
		out = append(out, j)
	}
	return c.JSON(fiber.Map{"items": out, "ffmpeg": ffmpeg})
}

// HandleGetSessionMedia streams one file (or its converted copy).
func (h *AgentSessionHandler) HandleGetSessionMedia(c *fiber.Ctx) error {
	_, items, ok := h.sessionMedia(c)
	if !ok {
		return nil
	}
	it, found := findMediaItem(items, c.Params("mediaId"))
	if !found {
		return c.Status(404).JSON(fiber.Map{"error": "media not found"})
	}
	path, mime := it.RealPath, it.MimeType
	if c.Query("variant") == "compatible" {
		p, ready := h.mediaTools().Compatible(it)
		if !ready {
			return c.Status(404).JSON(fiber.Map{"error": "no converted copy yet"})
		}
		path, mime = p, sessionmedia.MimeType(p)
	} else if info, err := os.Stat(path); err != nil || info.Size() != it.Size || !sessionmedia.LooksLikeMedia(path) {
		// Changed since it was listed: let the client reload the list.
		return c.Status(409).JSON(fiber.Map{"error": "the file changed; reload the media list"})
	}
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Cache-Control", "private, no-cache")
	c.Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, strings.ReplaceAll(filepath.Base(it.Name), `"`, "")))
	c.Set("Content-Type", mime)
	// it.RealPath was resolved and checked when the list was built. If it now
	// resolves elsewhere (a folder on the way became a link), don't follow it.
	real, err := filepath.EvalSymlinks(path)
	if err != nil || real != path {
		if c.Query("variant") != "compatible" {
			return c.Status(409).JSON(fiber.Map{"error": "the file changed; reload the media list"})
		}
		if err == nil {
			path = real
		}
	}
	return sendCheckedFile(c, path)
}

// HandleGetSessionMediaPoster returns a JPEG frame of a video.
func (h *AgentSessionHandler) HandleGetSessionMediaPoster(c *fiber.Ctx) error {
	_, items, ok := h.sessionMedia(c)
	if !ok {
		return nil
	}
	it, found := findMediaItem(items, c.Params("mediaId"))
	if !found || it.Kind != sessionmedia.Video {
		return c.Status(404).JSON(fiber.Map{"error": "media not found"})
	}
	path, err := h.mediaTools().Poster(c.UserContext(), it)
	if err != nil {
		status := 500
		if errors.Is(err, sessionmedia.ErrNoFFmpeg) {
			status = 404
		}
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}
	c.Set("Cache-Control", "private, max-age=3600")
	c.Set("Content-Type", "image/jpeg")
	return sendCheckedFile(c, path)
}

// HandleConvertSessionMedia starts an iPhone-friendly copy (H.264/AAC MP4, or AAC M4A).
func (h *AgentSessionHandler) HandleConvertSessionMedia(c *fiber.Ctx) error {
	sessionID, items, ok := h.sessionMedia(c)
	if !ok {
		return nil
	}
	it, found := findMediaItem(items, c.Params("mediaId"))
	if !found {
		return c.Status(404).JSON(fiber.Map{"error": "media not found"})
	}
	job, err := h.mediaTools().Convert(it)
	if err != nil {
		status := 500
		if errors.Is(err, sessionmedia.ErrNoFFmpeg) {
			status = 501
		}
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}
	resp := fiber.Map{"state": job.State}
	if job.State == "ready" {
		resp["compatible_url"] = "/api/agent/sessions/" + sessionID.String() + "/media/" + it.ID + "?variant=compatible"
	}
	return c.JSON(resp)
}

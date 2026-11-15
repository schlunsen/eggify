package handlers

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/changelog"
	"github.com/schlunsen/eggify/internal/version"
)

const (
	defaultChangelogLimit = 20
	maxChangelogLimit     = 200
)

// ChangelogHandler serves the embedded CHANGELOG.md (read-only).
type ChangelogHandler struct {
	releases func() []changelog.Release
}

// NewChangelogHandler creates the changelog handler.
func NewChangelogHandler() *ChangelogHandler { return &ChangelogHandler{releases: changelog.All} }

// HandleGetChangelog returns releases newest first: ?limit= (default 20, max 200)
// and ?since=<version> (only releases newer than it; a malformed version is a
// 400). A non-empty Unreleased block comes first, flagged `unreleased`; it counts
// in total and is always newer than any since. total counts the matches before limit.
func (h *ChangelogHandler) HandleGetChangelog(c *fiber.Ctx) error {
	limit := c.QueryInt("limit", defaultChangelogLimit)
	if limit < 1 {
		limit = defaultChangelogLimit
	}
	if limit > maxChangelogLimit {
		limit = maxChangelogLimit
	}
	since := strings.TrimSpace(c.Query("since"))
	if since != "" && !changelog.ValidVersion(since) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "since must be a version like 2.21.0"})
	}

	all := h.releases()
	matching := make([]changelog.Release, 0, len(all))
	for _, r := range all {
		if since != "" && !r.Unreleased && changelog.CompareVersions(r.Version, since) <= 0 {
			continue
		}
		matching = append(matching, r)
	}
	total := len(matching)
	if len(matching) > limit {
		matching = matching[:limit]
	}
	return c.JSON(fiber.Map{
		"current":  version.Version,
		"total":    total,
		"releases": matching,
	})
}

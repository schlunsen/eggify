// Package handlers contains HTTP request handlers for the Eggify server.
// This file implements the review queue for memories and skills that agents
// wrote. Agent-written items start pending and are kept out of every prompt
// until a user approves them here.
package handlers

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/principal"
	"github.com/schlunsen/eggify/internal/skills"
)

// ReviewHandler serves the review queue endpoints.
type ReviewHandler struct {
	repo *database.Repository
	// Store holds skill snapshots (default: ~/.claude/eggify). Approving a
	// skill with a folder makes sure its snapshot exists first.
	Store *skills.Store
}

// NewReviewHandler creates a review handler.
func NewReviewHandler(repo *database.Repository) *ReviewHandler {
	return &ReviewHandler{repo: repo, Store: skills.DefaultStore()}
}

// isAPIKeyOnly reports whether the request authenticated with the API key and
// no signed-in user. Agents run as the same OS user as Eggify and can read the
// key, so such a request may be an agent acting on its own. Used to record
// provenance (agent-possible writes go to the review queue).
func isAPIKeyOnly(c *fiber.Ctx) bool {
	return principal.From(c).Kind == principal.APIKey
}

// reviewerName names the principal making a review decision.
func reviewerName(c *fiber.Ctx) string {
	if username, ok := principal.Username(c); ok {
		return "user:" + username
	}
	// User auth disabled (EGGIFY_AUTH_USER_AUTH_ENABLED=false): there is no
	// principal to name, only the local dashboard.
	return "user:local"
}

// ReviewAction is the privileged action behind approve/reject. The routes
// gate it with principal.RequireUser(ReviewAction): an agent that read
// ~/.claude/eggify/.secret must not approve its own memories or skills, and a
// session token cannot be lifted from the database (it is stored hashed).
// With user auth disabled the request is allowed and a warning is logged.
const ReviewAction = "Reviewing agent-written memories and skills"

// reviewItemSession adds the session that wrote an item, for the UI.
func reviewItemSession(createdBy string) fiber.Map {
	return fiber.Map{
		"created_by_session_id": database.AgentSessionOf(createdBy),
		"agent_authored":        database.IsAgentAuthored(createdBy),
	}
}

// HandleListPending handles GET /api/review/pending?kind=memory|skill&project_id=
func (h *ReviewHandler) HandleListPending(c *fiber.Ctx) error {
	kind := c.Query("kind")
	resp := fiber.Map{}

	if kind == "" || kind == "memory" {
		memories, err := h.repo.Memory.ListPendingMemories(c.Query("project_id"), c.QueryInt("limit", 200))
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Failed to list pending memories: %v", err)})
		}
		items := make([]fiber.Map, 0, len(memories))
		for _, m := range memories {
			item := fiber.Map{"memory": m}
			for k, v := range reviewItemSession(m.CreatedBy) {
				item[k] = v
			}
			items = append(items, item)
		}
		resp["memories"] = items
	}

	if kind == "" || kind == "skill" {
		skills, err := h.repo.Skill.ListPendingSkills()
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Failed to list pending skills: %v", err)})
		}
		items := make([]fiber.Map, 0, len(skills))
		for _, s := range skills {
			item := fiber.Map{"skill": s}
			for k, v := range reviewItemSession(s.CreatedBy) {
				item[k] = v
			}
			// A skill from a folder: the reviewer approves every file in it
			// (scripts, templates), so list them.
			if folder, ok := h.Store.FolderFiles(s); ok {
				item["folder"] = folder
			}
			items = append(items, item)
		}
		resp["skills"] = items
	}

	return c.JSON(resp)
}

// HandlePendingCounts handles GET /api/review/pending/counts
func (h *ReviewHandler) HandlePendingCounts(c *fiber.Ctx) error {
	byProject, err := h.repo.Memory.CountPendingMemories()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	memories := 0
	for _, n := range byProject {
		memories += n
	}
	skills, err := h.repo.Skill.CountPendingSkills()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"memories":            memories,
		"skills":              skills,
		"memories_by_project": byProject,
		"total":               memories + skills,
	})
}

// memoryReviewRequest is the optional body of an approve call (Edit+Approve).
type memoryReviewRequest struct {
	Title      *string `json:"title"`
	Content    *string `json:"content"`
	MemoryType *string `json:"memory_type"`
	Tags       *string `json:"tags"`
	Importance *int    `json:"importance"`
}

func (h *ReviewHandler) reviewMemory(c *fiber.Ctx, status string) error {
	id := c.Params("id")
	review := database.MemoryReview{Status: status, ReviewedBy: reviewerName(c)}
	if status == database.ReviewStatusApproved && len(c.Body()) > 0 {
		var req memoryReviewRequest
		if err := c.BodyParser(&req); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
		}
		if req.MemoryType != nil && *req.MemoryType != "" && !validMemoryTypes[*req.MemoryType] {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid memory type: " + *req.MemoryType})
		}
		if req.Importance != nil && (*req.Importance < 1 || *req.Importance > 10) {
			return c.Status(400).JSON(fiber.Map{"error": "Importance must be between 1 and 10"})
		}
		review.Title, review.Content, review.MemoryType = req.Title, req.Content, req.MemoryType
		review.Tags, review.Importance = req.Tags, req.Importance
	}
	m, err := h.repo.Memory.ReviewMemory(id, review)
	if err != nil {
		return reviewError(c, err)
	}
	logging.Info("review: memory %s %s by %s", id, status, review.ReviewedBy)
	return c.JSON(fiber.Map{"memory": m})
}

// HandleApproveMemory handles POST /api/review/memories/:id/approve
func (h *ReviewHandler) HandleApproveMemory(c *fiber.Ctx) error {
	return h.reviewMemory(c, database.ReviewStatusApproved)
}

// HandleRejectMemory handles POST /api/review/memories/:id/reject
func (h *ReviewHandler) HandleRejectMemory(c *fiber.Ctx) error {
	return h.reviewMemory(c, database.ReviewStatusRejected)
}

// skillReviewRequest is the optional body of an approve call (Edit+Approve).
type skillReviewRequest struct {
	Description *string `json:"description"`
	Body        *string `json:"body"`
}

func (h *ReviewHandler) reviewSkill(c *fiber.Ctx, status string) error {
	id := c.Params("id")
	review := database.SkillReview{Status: status, ReviewedBy: reviewerName(c)}
	if status == database.ReviewStatusApproved && len(c.Body()) > 0 {
		var req skillReviewRequest
		if err := c.BodyParser(&req); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
		}
		if req.Body != nil && len(*req.Body) > maxSkillBodyLen {
			return c.Status(400).JSON(fiber.Map{"error": "Skill body too large (max 100KB)"})
		}
		if req.Description != nil && len(*req.Description) > maxSkillDescriptionLen {
			return c.Status(400).JSON(fiber.Map{"error": "Description too long (max 2000 characters)"})
		}
		review.Description, review.Body = req.Description, req.Body
	}
	// A skill with a folder runs from a snapshot of the reviewed files. Build
	// it before approving: if the queued files are gone (the folder changed
	// and was never staged), the approval would cover nothing usable.
	var snapshotKey string
	if status == database.ReviewStatusApproved {
		pending, err := h.repo.Skill.GetSkillByID(id)
		if err != nil {
			return reviewError(c, err)
		}
		if pending != nil && pending.Status == database.ReviewStatusPending && pending.FolderHash != "" {
			approved := *pending
			if review.Description != nil && *review.Description != "" {
				approved.Description = *review.Description
			}
			if review.Body != nil && *review.Body != "" {
				approved.Body = *review.Body
			}
			if _, err := h.Store.EnsureSnapshot(&approved); err != nil {
				return c.Status(409).JSON(fiber.Map{"error": err.Error(), "code": "skill_folder_changed"})
			}
			snapshotKey = approved.SnapshotKey()
		}
	}
	s, err := h.repo.Skill.ReviewSkill(id, review)
	if err != nil {
		return reviewError(c, err)
	}
	if snapshotKey != "" {
		h.Store.PruneSnapshots(id, snapshotKey)
	}
	logging.Info("review: skill %s (%s) %s by %s", s.Name, id, status, review.ReviewedBy)
	return c.JSON(fiber.Map{"skill": s})
}

// HandleApproveSkill handles POST /api/review/skills/:id/approve
func (h *ReviewHandler) HandleApproveSkill(c *fiber.Ctx) error {
	return h.reviewSkill(c, database.ReviewStatusApproved)
}

// HandleRejectSkill handles POST /api/review/skills/:id/reject
func (h *ReviewHandler) HandleRejectSkill(c *fiber.Ctx) error {
	return h.reviewSkill(c, database.ReviewStatusRejected)
}

func reviewError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return c.Status(404).JSON(fiber.Map{"error": "Not found"})
	case errors.Is(err, database.ErrNotPending):
		return c.Status(409).JSON(fiber.Map{"error": "This item is not pending review"})
	default:
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
}

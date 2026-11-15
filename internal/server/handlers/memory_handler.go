package handlers

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
)

// MemoryHandler handles Memory Palace API endpoints
type MemoryHandler struct {
	repo *database.Repository
}

// NewMemoryHandler creates a new memory handler
func NewMemoryHandler(repo *database.Repository) *MemoryHandler {
	return &MemoryHandler{repo: repo}
}

// validMemoryTypes defines allowed memory types
var validMemoryTypes = map[string]bool{
	"decision":     true,
	"pattern":      true,
	"gotcha":       true,
	"preference":   true,
	"architecture": true,
	"convention":   true,
	"note":         true,
}

// validMemorySources defines allowed memory sources
var validMemorySources = map[string]bool{
	"agent": true,
	"user":  true,
	"auto":  true,
}

// HandleGetMemories handles GET /api/projects/:id/memories
func (h *MemoryHandler) HandleGetMemories(c *fiber.Ctx) error {
	projectID := c.Params("id")
	if projectID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Project ID is required"})
	}

	query := &database.MemoryQuery{
		ProjectID:  projectID,
		AreaID:     c.Query("area_id"),
		MemoryType: c.Query("type"),
		Search:     c.Query("search"),
		Limit:      c.QueryInt("limit", 100),
		Offset:     c.QueryInt("offset", 0),
	}

	if c.Query("pinned") == "true" {
		pinned := true
		query.Pinned = &pinned
	}

	if c.Query("archived") == "true" {
		archived := true
		query.Archived = &archived
	} else if c.Query("archived") == "all" {
		query.IncludeArchived = true
	}

	// Review status: default hides rejected memories; "all" shows every one.
	switch st := c.Query("status"); st {
	case "":
		query.Statuses = []string{database.ReviewStatusApproved, database.ReviewStatusPending}
	case "all":
	case database.ReviewStatusApproved, database.ReviewStatusPending, database.ReviewStatusRejected:
		query.Statuses = []string{st}
	default:
		return c.Status(400).JSON(fiber.Map{"error": "Invalid status: " + st})
	}

	memories, err := h.repo.Memory.GetMemories(query)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to retrieve memories: %v", err),
		})
	}

	if memories == nil {
		memories = []*database.Memory{}
	}

	return c.JSON(fiber.Map{
		"memories": memories,
	})
}

// HandleCreateMemory handles POST /api/projects/:id/memories
func (h *MemoryHandler) HandleCreateMemory(c *fiber.Ctx) error {
	projectID := c.Params("id")
	if projectID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Project ID is required"})
	}

	var memory database.Memory
	if err := c.BodyParser(&memory); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid memory data"})
	}

	// Validate required fields
	if memory.Title == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Title is required"})
	}
	if memory.Content == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Content is required"})
	}
	if memory.MemoryType == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Memory type is required"})
	}
	if !validMemoryTypes[memory.MemoryType] {
		return c.Status(400).JSON(fiber.Map{
			"error": fmt.Sprintf("Invalid memory type: %s. Must be one of: decision, pattern, gotcha, preference, architecture, convention, note", memory.MemoryType),
		})
	}

	// Set defaults
	memory.ProjectID = projectID
	if memory.ID == "" {
		memory.ID = uuid.New().String()
	}
	if memory.Source == "" {
		memory.Source = "user"
	}
	if !validMemorySources[memory.Source] {
		memory.Source = "user"
	}
	if memory.Importance < 1 || memory.Importance > 10 {
		memory.Importance = 5
	}
	if memory.Tags == "" {
		memory.Tags = "[]"
	}

	now := time.Now()
	memory.CreatedAt = now
	memory.UpdatedAt = now

	// Provenance is decided here, never by the request body. A memory created
	// with only the API key may come from an agent (agents can read the key),
	// so it waits for review like one written with store_memory.
	memory.ReviewedBy, memory.ReviewedAt = nil, nil
	if isAPIKeyOnly(c) {
		memory.CreatedBy = database.CreatedByAPIKey
		memory.Status = database.ReviewStatusPending
		memory.Importance = database.ClampAgentImportance(memory.Importance)
	} else {
		memory.CreatedBy = database.CreatedByUser
		memory.Status = database.ReviewStatusApproved
	}

	if err := h.repo.Memory.CreateMemory(&memory); err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to create memory: %v", err),
		})
	}

	return c.Status(201).JSON(fiber.Map{
		"memory": memory,
	})
}

// HandleGetMemory handles GET /api/projects/:id/memories/:memId
func (h *MemoryHandler) HandleGetMemory(c *fiber.Ctx) error {
	memID := c.Params("memId")
	if memID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Memory ID is required"})
	}

	memory, err := h.repo.Memory.GetMemory(memID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to retrieve memory: %v", err),
		})
	}
	if memory == nil {
		return c.Status(404).JSON(fiber.Map{"error": "Memory not found"})
	}

	return c.JSON(fiber.Map{
		"memory": memory,
	})
}

// HandleUpdateMemory handles PUT /api/projects/:id/memories/:memId
func (h *MemoryHandler) HandleUpdateMemory(c *fiber.Ctx) error {
	memID := c.Params("memId")
	if memID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Memory ID is required"})
	}

	// Fetch existing memory
	existing, err := h.repo.Memory.GetMemory(memID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to retrieve memory: %v", err),
		})
	}
	if existing == nil {
		return c.Status(404).JSON(fiber.Map{"error": "Memory not found"})
	}

	// Parse update fields
	var update database.Memory
	if err := c.BodyParser(&update); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid memory data"})
	}

	// Apply updates to existing memory
	if update.Title != "" {
		existing.Title = update.Title
	}
	if update.Content != "" {
		existing.Content = update.Content
	}
	if update.MemoryType != "" {
		if !validMemoryTypes[update.MemoryType] {
			return c.Status(400).JSON(fiber.Map{
				"error": fmt.Sprintf("Invalid memory type: %s", update.MemoryType),
			})
		}
		existing.MemoryType = update.MemoryType
	}
	if update.Tags != "" {
		existing.Tags = update.Tags
	}
	if update.Importance >= 1 && update.Importance <= 10 {
		existing.Importance = update.Importance
	}
	existing.AreaID = update.AreaID

	// Status only changes through the review endpoints, except that an edit
	// made with only the API key (possibly an agent) sends an approved memory
	// back to the review queue.
	if isAPIKeyOnly(c) && (update.Title != "" || update.Content != "" || update.Importance != 0) &&
		existing.Status == database.ReviewStatusApproved {
		existing.Status = database.ReviewStatusPending
		existing.Importance = database.ClampAgentImportance(existing.Importance)
	}

	if err := h.repo.Memory.UpdateMemory(existing); err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to update memory: %v", err),
		})
	}

	return c.JSON(fiber.Map{
		"memory": existing,
	})
}

// HandleDeleteMemory handles DELETE /api/projects/:id/memories/:memId
func (h *MemoryHandler) HandleDeleteMemory(c *fiber.Ctx) error {
	memID := c.Params("memId")
	if memID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Memory ID is required"})
	}

	if err := h.repo.Memory.DeleteMemory(memID); err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to delete memory: %v", err),
		})
	}

	return c.JSON(fiber.Map{"success": true})
}

// HandleTogglePin handles POST /api/projects/:id/memories/:memId/pin
func (h *MemoryHandler) HandleTogglePin(c *fiber.Ctx) error {
	memID := c.Params("memId")
	if memID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Memory ID is required"})
	}

	var body struct {
		Pinned bool `json:"pinned"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if err := h.repo.Memory.PinMemory(memID, body.Pinned); err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to update pin status: %v", err),
		})
	}

	return c.JSON(fiber.Map{"success": true, "pinned": body.Pinned})
}

// HandleToggleArchive handles POST /api/projects/:id/memories/:memId/archive
func (h *MemoryHandler) HandleToggleArchive(c *fiber.Ctx) error {
	memID := c.Params("memId")
	if memID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Memory ID is required"})
	}

	var body struct {
		Archived bool `json:"archived"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if err := h.repo.Memory.ArchiveMemory(memID, body.Archived); err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to update archive status: %v", err),
		})
	}

	return c.JSON(fiber.Map{"success": true, "archived": body.Archived})
}

// HandleGetMemoryStats handles GET /api/projects/:id/memories/stats
func (h *MemoryHandler) HandleGetMemoryStats(c *fiber.Ctx) error {
	projectID := c.Params("id")
	if projectID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Project ID is required"})
	}

	stats, err := h.repo.Memory.GetMemoryStats(projectID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to retrieve memory stats: %v", err),
		})
	}

	return c.JSON(fiber.Map{
		"stats": stats,
	})
}

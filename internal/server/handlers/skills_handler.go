// Package handlers contains HTTP request handlers for the Eggify server.
// This file implements skill management endpoints for the skills system.
package handlers

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/skills"
)

const (
	maxSkillBodyLen        = 100000 // 100KB max skill body
	maxSkillDescriptionLen = 2000
	maxSkillNameLen        = 200
)

// SkillsHandler handles skill management endpoints
type SkillsHandler struct {
	repo      *database.Repository
	claudeDir string
	// Store holds skill snapshots (default: ~/.claude/eggify).
	Store *skills.Store
}

// NewSkillsHandler creates a new skills handler
func NewSkillsHandler(repo *database.Repository, claudeDir string) *SkillsHandler {
	return &SkillsHandler{
		repo:      repo,
		claudeDir: claudeDir,
		Store:     skills.DefaultStore(),
	}
}

// HandleGetSkills returns all discovered skills, optionally filtered by scope
func (h *SkillsHandler) HandleGetSkills(c *fiber.Ctx) error {
	scope := c.Query("scope")

	// Get skills from database cache
	dbSkills, err := h.repo.Skill.GetAllSkills(scope)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to fetch skills: " + err.Error(),
		})
	}

	// Only approved skills are usable, so pickers and slash-command lists
	// show only those. ?status=all lists every skill; pending ones are served
	// by /api/review/pending.
	if c.Query("status") != "all" {
		approved := make([]*database.Skill, 0, len(dbSkills))
		for _, s := range dbSkills {
			if s.Status == database.ReviewStatusApproved {
				approved = append(approved, s)
			}
		}
		dbSkills = approved
	}

	return c.JSON(fiber.Map{
		"skills": dbSkills,
		"count":  len(dbSkills),
	})
}

// HandleGetSkill returns a single skill by name
func (h *SkillsHandler) HandleGetSkill(c *fiber.Ctx) error {
	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Skill name is required",
		})
	}

	skill, err := h.repo.Skill.GetSkillByName(name)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to fetch skill: " + err.Error(),
		})
	}
	if skill == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Skill not found: " + name,
		})
	}

	return c.JSON(skill)
}

// HandleDiscoverSkills scans the filesystem for skills and syncs them to the
// database. It stays callable with the API key: skill files can be written by
// agents, so discovery only ever creates pending review items (or keeps an
// unchanged file's review decision) and never approves or overwrites a
// pending skill, whoever calls it (skills.SyncDiscovered).
func (h *SkillsHandler) HandleDiscoverSkills(c *fiber.Ctx) error {
	projectDir := c.Query("project_dir")

	// Validate project_dir to prevent path traversal
	if projectDir != "" {
		if !filepath.IsAbs(projectDir) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "project_dir must be an absolute path",
			})
		}
		cleaned := filepath.Clean(projectDir)
		if strings.Contains(cleaned, "..") {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "project_dir must not contain path traversal",
			})
		}
		projectDir = cleaned
	}

	found := skills.Discover(projectDir)
	report := skills.SyncDiscovered(h.repo.Skill, h.Store, found.Skills)
	// Files that could not be parsed come first, then files that could not
	// be stored.
	report.Errors = append(append([]skills.DiscoveryIssue{}, found.Errors...), report.Errors...)
	for _, e := range report.Errors {
		logging.Warning("Skill discovery: %s", e)
	}
	for _, s := range report.SkippedItems {
		logging.Info("Skill discovery: skipped %s (%s): %s", s.Path, s.Name, s.Reason)
	}
	if report.Pending > 0 {
		logging.Info("Skill discovery: %d skill file(s) wait for review", report.Pending)
	}

	skillsOut := found.Skills
	if skillsOut == nil {
		skillsOut = []*skills.ParsedSkill{}
	}
	return c.JSON(fiber.Map{
		"discovered":    report.Discovered,
		"synced":        report.Synced,
		"pending":       report.Pending,
		"unchanged":     report.Unchanged,
		"skipped":       report.Skipped,
		"skipped_items": nonNil(report.SkippedItems),
		"errors":        nonNil(report.Errors),
		"skills":        skillsOut,
	})
}

// nonNil turns a nil slice into an empty one so it is sent as [] not null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// CreateSkillRequest represents a request to create a new skill
type CreateSkillRequest struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Scope       string                   `json:"scope"` // 'personal' or 'project'
	Body        string                   `json:"body"`
	Frontmatter *skills.SkillFrontmatter `json:"frontmatter,omitempty"`
}

// HandleCreateSkill creates a new skill
func (h *SkillsHandler) HandleCreateSkill(c *fiber.Ctx) error {
	var req CreateSkillRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Skill name is required",
		})
	}
	if len(req.Name) > maxSkillNameLen {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Skill name too long (max 200 characters)",
		})
	}
	if req.Body == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Skill body is required",
		})
	}
	if len(req.Body) > maxSkillBodyLen {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Skill body too large (max 100KB)",
		})
	}
	if len(req.Description) > maxSkillDescriptionLen {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Description too long (max 2000 characters)",
		})
	}
	if req.Scope == "" {
		req.Scope = "project"
	}
	if req.Scope != "personal" && req.Scope != "project" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Scope must be 'personal' or 'project'",
		})
	}

	// Build frontmatter if not provided
	if req.Frontmatter == nil {
		req.Frontmatter = &skills.SkillFrontmatter{
			Name:        req.Name,
			Description: req.Description,
		}
	} else {
		req.Frontmatter.Name = req.Name
		if req.Frontmatter.Description == "" {
			req.Frontmatter.Description = req.Description
		}
	}

	// Validate the skill
	parsedSkill := &skills.ParsedSkill{
		Frontmatter: *req.Frontmatter,
		Body:        req.Body,
		Scope:       req.Scope,
	}

	validationErrors := skills.Validate(parsedSkill)
	if len(validationErrors) > 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error":             "Skill validation failed",
			"validation_errors": validationErrors,
		})
	}

	// Save to database
	fmJSON, _ := json.Marshal(req.Frontmatter)

	dbSkill := &database.Skill{
		Name:                   req.Name,
		Description:            req.Frontmatter.Description,
		Scope:                  req.Scope,
		Path:                   "", // Will be set when written to filesystem
		FrontmatterJSON:        string(fmJSON),
		Body:                   req.Body,
		UserInvocable:          req.Frontmatter.IsUserInvocable(),
		DisableModelInvocation: req.Frontmatter.DisableModelInvocation,
		AllowedTools:           string(req.Frontmatter.AllowedTools),
		Model:                  req.Frontmatter.Model,
		Effort:                 req.Frontmatter.Effort,
		Context:                req.Frontmatter.Context,
		Agent:                  req.Frontmatter.Agent,
		ArgumentHint:           req.Frontmatter.ArgumentHint,
		Shell:                  req.Frontmatter.Shell,
	}
	// A skill written with only the API key may come from an agent (agents
	// can read the key): it waits for review before it is used anywhere.
	if isAPIKeyOnly(c) {
		dbSkill.CreatedBy = database.CreatedByAPIKey
		dbSkill.Status = database.ReviewStatusPending
	}

	if err := h.repo.Skill.SaveSkill(dbSkill); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save skill: " + err.Error(),
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"success": true,
		"message": "Skill '" + req.Name + "' created successfully",
		"skill":   dbSkill,
	})
}

// UpdateSkillRequest represents a request to update a skill
type UpdateSkillRequest struct {
	Description string                   `json:"description"`
	Body        string                   `json:"body"`
	Frontmatter *skills.SkillFrontmatter `json:"frontmatter,omitempty"`
}

// HandleUpdateSkill updates an existing skill
func (h *SkillsHandler) HandleUpdateSkill(c *fiber.Ctx) error {
	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Skill name is required",
		})
	}

	// Check skill exists
	existing, err := h.repo.Skill.GetSkillByName(name)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to fetch skill: " + err.Error(),
		})
	}
	if existing == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Skill not found: " + name,
		})
	}

	var req UpdateSkillRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	// Update fields
	if req.Description != "" {
		existing.Description = req.Description
	}
	if req.Body != "" {
		existing.Body = req.Body
	}
	if req.Frontmatter != nil {
		fmJSON, _ := json.Marshal(req.Frontmatter)
		existing.FrontmatterJSON = string(fmJSON)
		existing.AllowedTools = string(req.Frontmatter.AllowedTools)
		existing.Model = req.Frontmatter.Model
		existing.Effort = req.Frontmatter.Effort
		existing.Context = req.Frontmatter.Context
		existing.Agent = req.Frontmatter.Agent
		existing.ArgumentHint = req.Frontmatter.ArgumentHint
		existing.Shell = req.Frontmatter.Shell
		existing.UserInvocable = req.Frontmatter.IsUserInvocable()
		existing.DisableModelInvocation = req.Frontmatter.DisableModelInvocation
	}
	// An edit made with only the API key (possibly an agent) needs review.
	if isAPIKeyOnly(c) {
		existing.CreatedBy = database.CreatedByAPIKey
		existing.Status = database.ReviewStatusPending
	}

	if err := h.repo.Skill.SaveSkill(existing); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to update skill: " + err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"success": true,
		"message": "Skill '" + name + "' updated successfully",
		"skill":   existing,
	})
}

// HandleDeleteSkill deletes a skill by name
func (h *SkillsHandler) HandleDeleteSkill(c *fiber.Ctx) error {
	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Skill name is required",
		})
	}

	if err := h.repo.Skill.DeleteSkill(name); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to delete skill: " + err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"success": true,
		"message": "Skill '" + name + "' deleted successfully",
	})
}

// HandleGetSkillTemplates returns pre-built skill templates loaded from embedded .md files.
// Supports optional ?category= query param for filtering.
func (h *SkillsHandler) HandleGetSkillTemplates(c *fiber.Ctx) error {
	templates, err := skills.LoadTemplates()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to load templates: " + err.Error()})
	}

	// Optional category filter
	if cat := c.Query("category"); cat != "" {
		filtered := make([]skills.SkillTemplate, 0)
		for _, t := range templates {
			if strings.EqualFold(t.Category, cat) {
				filtered = append(filtered, t)
			}
		}
		templates = filtered
	}

	// Collect unique categories for the frontend
	catSet := make(map[string]bool)
	for _, t := range templates {
		catSet[t.Category] = true
	}
	categories := make([]string, 0, len(catSet))
	for cat := range catSet {
		categories = append(categories, cat)
	}

	return c.JSON(fiber.Map{
		"templates":  templates,
		"categories": categories,
		"count":      len(templates),
	})
}

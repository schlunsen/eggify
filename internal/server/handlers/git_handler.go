package handlers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/git"
	"github.com/schlunsen/eggify/internal/mcp"
)

// marshalProjectSettings safely constructs JSON settings using json.Marshal
// to prevent JSON injection via user-controlled strings.
func marshalProjectSettings(owner, fullName, cloneURL, htmlURL string, stars int, language string) string {
	settings := map[string]interface{}{
		"owner":     owner,
		"full_name": fullName,
		"clone_url": cloneURL,
		"html_url":  htmlURL,
		"stars":     stars,
		"language":  language,
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// GitHandler handles Git-related endpoints
type GitHandler struct {
	repo      *database.Repository
	claudeDir string
}

// NewGitHandler creates a new Git handler
func NewGitHandler(repo *database.Repository, claudeDir string) *GitHandler {
	return &GitHandler{
		repo:      repo,
		claudeDir: claudeDir,
	}
}

// HandleSearchGitHubRepositories searches for repositories on GitHub
// GET /api/git/search?q=language:go&page=1&per_page=10
func (h *GitHandler) HandleSearchGitHubRepositories(c *fiber.Ctx) error {
	query := c.Query("q")
	if query == "" {
		return c.Status(400).JSON(fiber.Map{
			"error": "q (query) parameter is required",
		})
	}

	pageStr := c.Query("page", "1")
	perPageStr := c.Query("per_page", "30")
	sort := c.Query("sort", "stars")
	order := c.Query("order", "desc")

	page, err := strconv.Atoi(pageStr)
	if err != nil {
		page = 1
	}

	perPage, err := strconv.Atoi(perPageStr)
	if err != nil {
		perPage = 30
	}

	// Create git service (no database needed, data is live from GitHub API)
	gitService := git.NewService(filepath.Join(os.Getenv("HOME"), ".claude", "projects"))

	// Search GitHub
	response, err := gitService.SearchGitHubRepositories(query, page, perPage, sort, order)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to search GitHub: %v", err),
		})
	}

	// Convert repositories to search results
	results := make([]*git.SearchResult, len(response.Items))
	for i, repo := range response.Items {
		results[i] = repo.ToSearchResult()
	}

	return c.JSON(fiber.Map{
		"total_count": response.TotalCount,
		"results":     results,
		"page":        page,
		"per_page":    perPage,
	})
}

// HandleTrendingRepositories fetches trending repositories from GitHub REST API with caching
// GET /api/git/trending?language=go&days=7&page=1&per_page=10
// Uses REST API directly (bypasses gh CLI) with 1-hour caching for reliability
func (h *GitHandler) HandleTrendingRepositories(c *fiber.Ctx) error {
	language := c.Query("language", "")
	daysStr := c.Query("days", "7")
	pageStr := c.Query("page", "1")
	perPageStr := c.Query("per_page", "30")

	page, err := strconv.Atoi(pageStr)
	if err != nil {
		page = 1
	}

	perPage, err := strconv.Atoi(perPageStr)
	if err != nil {
		perPage = 30
	}

	days, err := strconv.Atoi(daysStr)
	if err != nil || days < 1 {
		days = 7
	}

	// Create git service
	gitService := git.NewService(filepath.Join(os.Getenv("HOME"), ".claude", "projects"))

	// Use the TrendingRepositories method which uses REST API directly with caching
	response, err := gitService.TrendingRepositories(language, days, page, perPage)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to fetch trending repositories: %v", err),
		})
	}

	// Convert repositories to search results
	results := make([]*git.SearchResult, len(response.Items))
	for i, repo := range response.Items {
		results[i] = repo.ToSearchResult()
	}

	return c.JSON(fiber.Map{
		"total_count": response.TotalCount,
		"results":     results,
		"page":        page,
		"per_page":    perPage,
	})
}

// HandleCloneRepository clones a git repository
// POST /api/git/clone
// Request body: { "clone_url": "...", "owner": "...", "repo_name": "...", "full_name": "...", "description": "...", "html_url": "...", "stars": 0, "language": "...", "depth": 1, "custom_path": "..." }
func (h *GitHandler) HandleCloneRepository(c *fiber.Ctx) error {
	type CloneRequest struct {
		CloneURL    string `json:"clone_url"`
		Owner       string `json:"owner"`
		RepoName    string `json:"repo_name"`
		FullName    string `json:"full_name"`
		Description string `json:"description"`
		HTMLURL     string `json:"html_url"`
		Stars       int    `json:"stars"`
		Language    string `json:"language"`
		Depth       *int   `json:"depth"`
		CustomPath  string `json:"custom_path"`
	}

	var req CloneRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	// Validate required fields
	if req.CloneURL == "" || req.Owner == "" || req.RepoName == "" {
		return c.Status(400).JSON(fiber.Map{
			"error": "clone_url, owner, and repo_name are required",
		})
	}

	// SECURITY: same clone rules as the new-project flow (parseCloneSource:
	// https only, no credentials, no localhost/private literals); the DNS
	// check, redirect and protocol limits and address pinning happen in
	// git.Service.CloneRepository (git.PrepareClone).
	src, perr := parseCloneSource(req.CloneURL)
	if perr != nil {
		return c.Status(400).JSON(fiber.Map{"error": perr.Error()})
	}
	req.CloneURL = src.URL

	// SECURITY: Validate owner and repo_name to prevent path traversal
	if strings.Contains(req.Owner, "..") || strings.ContainsAny(req.Owner, "/\\") {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid owner name"})
	}
	if strings.Contains(req.RepoName, "..") || strings.ContainsAny(req.RepoName, "/\\") {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid repository name"})
	}

	// Determine base directory - use custom path if provided, otherwise default
	var baseDir string
	if req.CustomPath != "" {
		// SECURITY: Validate custom path doesn't contain traversal
		if strings.Contains(req.CustomPath, "..") {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid custom path"})
		}
		expanded, err := expandUserPath(req.CustomPath)
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"error": fmt.Sprintf("Invalid custom path: %v", err)})
		}
		baseDir = expanded
	} else {
		// The configured default clone path (Settings → Git), ~ expanded.
		baseDir = (&ProjectHandler{repo: h.repo}).defaultProjectsDir()
	}

	// Ensure base directory exists
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to create projects directory: %v", err),
		})
	}

	// Create git service
	gitService := git.NewService(baseDir)

	// Clone repository with options
	cloneOptions := &git.CloneOptions{
		Depth: req.Depth,
	}
	clonedPath, err := gitService.CloneRepository(req.CloneURL, req.Owner, req.RepoName, cloneOptions)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to clone repository: %v", err),
		})
	}

	// Create a project record in the database
	if h.repo != nil {
		projectID := uuid.New().String()
		project := &database.Project{
			ID:          projectID,
			Name:        req.RepoName,
			Path:        clonedPath,
			Description: req.Description,
			IsActive:    true,
			Color:       randomProjectColor(),
			Settings:    marshalProjectSettings(req.Owner, req.FullName, req.CloneURL, req.HTMLURL, req.Stars, req.Language),
		}

		if err := h.repo.CreateProject(project); err != nil {
			// Log the error but don't fail the clone - the files are already there
			fmt.Printf("⚠️  warning: Failed to save project to database: %v\n", err)
		}
	}

	// Ensure .mcp.json exists in the cloned project so agent sessions
	// get MCP tools (handover, GPU, deploy, etc.) via --setting-sources local.
	if _, err := mcp.EnsureMCPConfig(clonedPath); err != nil {
		fmt.Printf("⚠️  warning: Failed to create .mcp.json in cloned project: %v\n", err)
	}

	return c.JSON(fiber.Map{
		"success": true,
		"message": "Repository cloned successfully",
		"path":    clonedPath,
		"owner":   req.Owner,
		"repo":    req.RepoName,
	})
}

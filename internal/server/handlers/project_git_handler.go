package handlers

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/server/agents"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// Project Git page: GET /api/projects/:id/git/overview and
// GET /api/projects/:id/git/changes. Read-only, like the file browser
// (the API key may read), and refused for project folders the file browser
// refuses (checkProjectRoot).

const (
	projectGitTimeout       = 20 * time.Second
	projectGitMaxSessionsWT = 8
)

// gitWorktreeSession is an Eggify session working in a worktree.
type gitWorktreeSession struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

// HandleProjectGitOverview handles GET /api/projects/:id/git/overview:
// branches, worktrees (with the sessions working in each) and recent commits.
func (h *ProjectHandler) HandleProjectGitOverview(c *fiber.Ctx) error {
	root, ok := h.projectGitRoot(c)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(c.Context(), projectGitTimeout)
	defer cancel()
	ov, err := agents.GetRepoOverview(ctx, root)
	if err != nil {
		return c.JSON(fiber.Map{"is_repo": false, "error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"is_repo":              true,
		"overview":             ov,
		"sessions_by_worktree": h.sessionsByWorktree(c, ov.Worktrees),
	})
}

// HandleProjectGitChanges handles GET /api/projects/:id/git/changes.
//
//	?scope=uncommitted&worktree=<path>   uncommitted work in a checkout (default: the project folder)
//	?scope=branch&ref=<branch>[&base=<branch>]   what the branch adds on top of base (default: the default branch)
//	&path=<file>   only that file (for patches the first response omitted)
func (h *ProjectHandler) HandleProjectGitChanges(c *fiber.Ctx) error {
	root, ok := h.projectGitRoot(c)
	if !ok {
		return nil
	}
	only := c.Query("path")
	if len(only) > 4096 || strings.ContainsRune(only, 0) || !utf8.ValidString(only) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid path"})
	}
	ctx, cancel := context.WithTimeout(c.Context(), projectGitTimeout)
	defer cancel()

	switch c.Query("scope", "uncommitted") {
	case "uncommitted":
		dir := root
		if wt := c.Query("worktree"); wt != "" {
			found, ok := projectWorktree(ctx, root, wt)
			if !ok {
				return c.Status(404).JSON(fiber.Map{"error": "not a worktree of this project"})
			}
			dir = found
		}
		ch, err := agents.GetUncommittedChanges(ctx, dir, only)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(ch)
	case "branch":
		ref := c.Query("ref")
		base := c.Query("base")
		if base == "" {
			base = agents.DefaultBranch(ctx, root)
		}
		if err := agents.ValidateGitRev(ref); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
		if err := agents.ValidateGitRev(base); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "no default branch to compare with"})
		}
		ch, err := agents.GetBranchChanges(ctx, root, base, ref, only)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(ch)
	default:
		return c.Status(400).JSON(fiber.Map{"error": "scope must be uncommitted or branch"})
	}
}

// projectGitRoot loads the project and checks its folder may be read.
func (h *ProjectHandler) projectGitRoot(c *fiber.Ctx) (string, bool) {
	project, err := h.repo.GetProject(c.Params("id"))
	if err != nil || project == nil {
		c.Status(404).JSON(fiber.Map{"error": "project not found"})
		return "", false
	}
	if err := checkProjectRoot(project.Path); err != nil {
		c.Status(403).JSON(fiber.Map{"error": err.Error()})
		return "", false
	}
	return project.Path, true
}

// projectWorktree returns the checkout of root's repository whose path is
// want. Only paths `git worktree list` reports are accepted, so the endpoint
// never runs git in an arbitrary folder.
func projectWorktree(ctx context.Context, root, want string) (string, bool) {
	list, err := agents.ListWorktrees(root)
	if err != nil {
		return "", false
	}
	clean := filepath.Clean(want)
	for _, wt := range list {
		if filepath.Clean(wt.Path) == clean {
			return wt.Path, true
		}
	}
	return "", false
}

// sessionsByWorktree maps each worktree path to the open sessions (not
// ended) working in it, newest first. A session belongs to the deepest
// worktree containing its folder (linked worktrees often live inside the
// main checkout, under .worktrees/).
func (h *ProjectHandler) sessionsByWorktree(c *fiber.Ctx, worktrees []agents.GitOverviewWorktree) map[string][]gitWorktreeSession {
	out := map[string][]gitWorktreeSession{}
	if h.agentHandler == nil || h.agentHandler.SessionManager == nil || len(worktrees) == 0 {
		return out
	}
	all, err := h.agentHandler.SessionManager.Storage.ListSessions("all")
	if err != nil {
		return out
	}
	type wtPath struct{ path, key string }
	paths := make([]wtPath, 0, len(worktrees))
	for _, wt := range worktrees {
		p := filepath.Clean(wt.Path)
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		paths = append(paths, wtPath{p, wt.Path})
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i].path) > len(paths[j].path) })

	var matched []*agents.SessionMetadata
	keys := map[uuid.UUID]string{}
	names := map[uuid.UUID]string{}
	for _, s := range all {
		if s.Status == "ended" || s.EndedAt != nil {
			continue
		}
		dir, name := sessionFolder(s.OptionsJSON)
		if dir == "" {
			continue
		}
		dir = filepath.Clean(dir)
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			dir = real
		}
		for _, p := range paths {
			if pathWithin(p.path, dir) {
				matched = append(matched, s)
				keys[s.ID], names[s.ID] = p.key, name
				break
			}
		}
	}
	if username, ok := principal.Username(c); ok && h.visibleSessions != nil && len(matched) > 0 {
		ids := make([]uuid.UUID, 0, len(matched))
		for _, s := range matched {
			ids = append(ids, s.ID)
		}
		visible := h.visibleSessions(username, ids)
		kept := matched[:0]
		for _, s := range matched {
			if visible[s.ID] {
				kept = append(kept, s)
			}
		}
		matched = kept
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].UpdatedAt.After(matched[j].UpdatedAt) })
	for _, s := range matched {
		key := keys[s.ID]
		if len(out[key]) >= projectGitMaxSessionsWT {
			continue
		}
		name := names[s.ID]
		if name == "" {
			name = "Session " + s.ID.String()[:8]
		}
		out[key] = append(out[key], gitWorktreeSession{ID: s.ID.String(), Name: name, Status: s.Status, UpdatedAt: s.UpdatedAt})
	}
	return out
}

// sessionFolder reads where a session works from its stored options: the
// folder it was last seen working in (it may have moved into a worktree),
// else the one it was created in.
func sessionFolder(optionsJSON string) (dir, name string) {
	if optionsJSON == "" {
		return "", ""
	}
	var opts struct {
		AgentName        *string `json:"agent_name"`
		WorkingDirectory *string `json:"working_directory"`
		Workspace        *struct {
			WorkingDirectory string `json:"working_directory"`
			WorktreePath     string `json:"worktree_path"`
		} `json:"workspace"`
	}
	if json.Unmarshal([]byte(optionsJSON), &opts) != nil {
		return "", ""
	}
	if opts.AgentName != nil {
		name = *opts.AgentName
	}
	switch {
	case opts.Workspace != nil && opts.Workspace.WorktreePath != "":
		dir = opts.Workspace.WorktreePath
	case opts.Workspace != nil && opts.Workspace.WorkingDirectory != "":
		dir = opts.Workspace.WorkingDirectory
	case opts.WorkingDirectory != nil:
		dir = *opts.WorkingDirectory
	}
	return dir, name
}

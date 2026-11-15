package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/schlunsen/eggify/internal/gitexec"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/git"
)

// The new-project flow (Projects page → "New project") has three starting
// points: register an existing folder, clone a repository, or create an empty
// folder. Everything that touches the disk lives here, behind
// principal.RequireUser (see routes/project.go):
//
//	GET    /api/projects/overview              projects + session activity + git state, one request
//	GET    /api/projects/setup/defaults        default projects folder, home, git availability
//	GET    /api/projects/setup/inspect?path=   what is at a path (exists, git, already a project, can create)
//	GET    /api/projects/setup/complete?prefix= directory autocomplete for typed paths
//	POST   /api/projects/setup                 {mode: existing|empty|clone, ...}
//	GET    /api/projects/setup/jobs/:jobId     clone progress
//	DELETE /api/projects/setup/jobs/:jobId     cancel a clone
//
// New folders (empty projects and clone destinations) must be inside the
// user's home directory, outside hidden directories (~/.claude, ~/.ssh, ...;
// the legacy ~/.claude/projects clone folder excepted), must not exist yet and
// are named by a plain folder name: no separators, no "..".

// ProjectSetupAction is the privileged-action label of the new-project endpoints.
const ProjectSetupAction = "Creating projects, cloning repositories and browsing folders"

const (
	maxProjectNameLen   = 100
	maxProjectDescLen   = 2000
	cloneTimeout        = 15 * time.Minute
	cloneJobRetention   = time.Hour
	overviewGitTimeout  = 2 * time.Second
	overviewGitCacheTTL = 15 * time.Second
	overviewGitWorkers  = 8
	completeMaxResults  = 12
	completeMaxScan     = 2000
)

var (
	folderNameRe   = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9 ._-]*$`)
	hexColorRe     = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	ownerRepoRe    = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)/([A-Za-z0-9._-]+?)(?:\.git)?$`)
	scpLikeRe      = regexp.MustCompile(`^[A-Za-z0-9._-]+@([A-Za-z0-9.-]+):([A-Za-z0-9._/-]+?)(?:\.git)?/?$`)
	cloneStageRe   = regexp.MustCompile(`^(?:remote:\s*)?([A-Za-z][A-Za-z ]+):\s+(\d{1,3})%`)
	legacyCloneDir = filepath.Join(".claude", "projects")
)

// ==================== Path helpers ====================

func userHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Clean(home)
}

// displayPath shortens a path under the home directory to ~/...
func displayPath(p, home string) string {
	if home == "" || p == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// expandUserPath expands a leading ~ and requires an absolute result. Unlike
// fileops.ExpandTildePath it never resolves relative paths against the
// server's working directory, and it refuses ".." segments outright.
func expandUserPath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", errors.New("path is required")
	}
	if strings.ContainsRune(p, 0) {
		return "", errors.New("invalid path")
	}
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return "", errors.New("path must not contain \"..\"")
		}
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home := userHome()
		if home == "" {
			return "", errors.New("cannot determine home directory")
		}
		p = filepath.Join(home, strings.TrimPrefix(p[1:], "/"))
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("use an absolute path or one starting with ~/")
	}
	return filepath.Clean(p), nil
}

// resolveExisting resolves symlinks of the longest existing prefix of p and
// re-appends the rest, so a policy check sees where a new folder really lands.
func resolveExisting(p string) string {
	rest := []string{}
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				r = filepath.Join(r, rest[i])
			}
			return r
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}

// validateFolderName checks a single new folder name.
func validateFolderName(name string) error {
	if name == "" {
		return errors.New("folder name is required")
	}
	if len(name) > maxProjectNameLen {
		return fmt.Errorf("folder name is longer than %d characters", maxProjectNameLen)
	}
	if name == "." || name == ".." || strings.Contains(name, "..") {
		return errors.New("folder name must not contain \"..\"")
	}
	if !folderNameRe.MatchString(name) || strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
		return errors.New("folder name may only use letters, numbers, spaces, dots, dashes and underscores")
	}
	return nil
}

// newFolderTarget validates parent+name for a folder Eggify is about to create
// and returns the absolute target. The target must not exist.
func newFolderTarget(parent, name string) (string, error) {
	if err := validateFolderName(name); err != nil {
		return "", err
	}
	parentAbs, err := expandUserPath(parent)
	if err != nil {
		return "", err
	}
	target := filepath.Join(parentAbs, name)
	if err := checkCreatablePath(target); err != nil {
		return "", err
	}
	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("%s already exists", displayPath(target, userHome()))
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("cannot check %s: %v", target, err)
	}
	return target, nil
}

// checkCreatablePath applies the location policy for new folders.
func checkCreatablePath(target string) error {
	home := userHome()
	if home == "" {
		return errors.New("cannot determine home directory")
	}
	realHome := resolveExisting(home)
	real := resolveExisting(target)
	rel, err := filepath.Rel(realHome, real)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("new project folders must be inside your home folder")
	}
	if rel == legacyCloneDir || strings.HasPrefix(rel, legacyCloneDir+string(filepath.Separator)) {
		rel = strings.TrimPrefix(strings.TrimPrefix(rel, legacyCloneDir), string(filepath.Separator))
	}
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		if strings.HasPrefix(seg, ".") {
			return errors.New("new project folders cannot be created inside hidden folders")
		}
	}
	return nil
}

// ==================== Git helpers ====================

type gitInfo struct {
	IsRepo bool   `json:"is_repo"`
	Branch string `json:"branch,omitempty"`
	Remote string `json:"remote,omitempty"`
	Dirty  *bool  `json:"dirty,omitempty"`
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"--no-optional-locks", "-C", dir}, args...)
	cmd := gitexec.CommandContext(ctx, full...)
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// probeGit reads branch and dirty state with `git status` (no index lock) and,
// when withRemote, the origin URL. Missing git or a non-repo yields IsRepo=false.
func probeGit(ctx context.Context, dir string, withRemote bool) gitInfo {
	var info gitInfo
	out, err := runGit(ctx, dir, "status", "--porcelain=v1", "-b")
	if err != nil {
		return info
	}
	info.IsRepo = true
	lines := strings.Split(out, "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "## ") {
		head := strings.TrimPrefix(lines[0], "## ")
		switch {
		case strings.HasPrefix(head, "No commits yet on "):
			head = strings.TrimPrefix(head, "No commits yet on ")
		case strings.HasPrefix(head, "Initial commit on "):
			head = strings.TrimPrefix(head, "Initial commit on ")
		}
		if i := strings.Index(head, "..."); i >= 0 {
			head = head[:i]
		}
		if i := strings.Index(head, " "); i >= 0 {
			head = head[:i]
		}
		if head == "HEAD" {
			head = "detached"
		}
		info.Branch = head
		lines = lines[1:]
	}
	dirty := false
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			dirty = true
			break
		}
	}
	info.Dirty = &dirty
	if withRemote {
		if remote, err := runGit(ctx, dir, "config", "--get", "remote.origin.url"); err == nil {
			info.Remote = redactURLCredentials(remote)
		}
	}
	return info
}

func redactURLCredentials(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

type cachedGit struct {
	info gitInfo
	at   time.Time
}

// ==================== Clone source parsing ====================

// cloneSource is a normalised clone request.
type cloneSource struct {
	URL  string `json:"url"`
	Name string `json:"name"`
}

// parseCloneSource accepts an HTTPS URL, owner/repo (GitHub), host/owner/repo
// or git@host:owner/repo, and returns an HTTPS clone URL. Only HTTPS is
// cloned: no local paths, file://, ssh:// or ext:: transports.
func parseCloneSource(raw string) (*cloneSource, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return nil, errors.New("enter a repository URL or owner/repo")
	}
	if m := ownerRepoRe.FindStringSubmatch(s); m != nil && !strings.Contains(m[1], ".") {
		s = "https://github.com/" + m[1] + "/" + m[2] + ".git"
	} else if m := scpLikeRe.FindStringSubmatch(s); m != nil {
		s = "https://" + m[1] + "/" + m[2] + ".git"
	} else if !strings.Contains(s, "://") && strings.Count(s, "/") >= 2 && strings.Contains(strings.SplitN(s, "/", 2)[0], ".") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, errors.New("that does not look like a repository URL")
	}
	if u.Scheme != "https" {
		return nil, errors.New("only HTTPS repository URLs can be cloned (try https://… or owner/repo)")
	}
	if u.User != nil {
		return nil, errors.New("remove credentials from the URL; use a public URL or your git credential helper")
	}
	if err := checkCloneHost(u.Hostname()); err != nil {
		return nil, err
	}
	if u.RawQuery != "" || u.Fragment != "" {
		u.RawQuery, u.Fragment = "", ""
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) < 2 || segs[len(segs)-1] == "" {
		return nil, errors.New("the URL must point to a repository, e.g. https://github.com/owner/repo")
	}
	name := strings.TrimSuffix(segs[len(segs)-1], ".git")
	if err := validateFolderName(name); err != nil {
		return nil, fmt.Errorf("unusable repository name %q", name)
	}
	return &cloneSource{URL: u.String(), Name: name}, nil
}

// checkCloneHost refuses localhost and private/loopback/link-local literals
// in any notation git accepts (git.CheckCloneHostLiteral, shared with
// /api/git/clone). DNS answers are checked when the clone runs.
func checkCloneHost(host string) error {
	_, err := git.CheckCloneHostLiteral(host)
	return err
}

// ==================== Clone jobs ====================

type cloneJob struct {
	ID        string            `json:"id"`
	Status    string            `json:"status"` // running | done | error | cancelled
	Stage     string            `json:"stage"`
	Percent   int               `json:"percent"` // overall 0-100
	Output    []string          `json:"output"`
	Error     string            `json:"error,omitempty"`
	Project   *database.Project `json:"project,omitempty"`
	Path      string            `json:"path"`
	StartedAt time.Time         `json:"started_at"`
	EndedAt   *time.Time        `json:"ended_at,omitempty"`
	cancel    context.CancelFunc
}

type projectSetupState struct {
	mu       sync.Mutex
	jobs     map[string]*cloneJob
	gitCache map[string]cachedGit
}

var setupStateMu sync.Mutex

func (h *ProjectHandler) setupState() *projectSetupState {
	setupStateMu.Lock()
	defer setupStateMu.Unlock()
	if h.setup == nil {
		h.setup = &projectSetupState{jobs: map[string]*cloneJob{}, gitCache: map[string]cachedGit{}}
	}
	return h.setup
}

func (st *projectSetupState) snapshot(id string) (*cloneJob, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	j, ok := st.jobs[id]
	if !ok {
		return nil, false
	}
	c := *j
	c.Output = append([]string{}, j.Output...)
	return &c, true
}

func (st *projectSetupState) update(id string, fn func(j *cloneJob)) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if j, ok := st.jobs[id]; ok {
		fn(j)
	}
}

func (st *projectSetupState) prune(now time.Time) {
	for id, j := range st.jobs {
		if j.EndedAt != nil && now.Sub(*j.EndedAt) > cloneJobRetention {
			delete(st.jobs, id)
		}
	}
}

// cloneStageWeight maps git's progress stages onto one overall bar.
func cloneOverall(stage string, pct int) int {
	switch strings.ToLower(stage) {
	case "counting objects", "enumerating objects":
		return pct * 5 / 100
	case "compressing objects":
		return 5 + pct*5/100
	case "receiving objects":
		return 10 + pct*70/100
	case "resolving deltas":
		return 80 + pct*15/100
	case "updating files", "checking out files", "updating references":
		return 95 + pct*5/100
	}
	return -1
}

// splitCR splits git's progress stream on \r as well as \n.
func splitCR(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// cloneRunner is swapped out in tests.
var cloneRunner = runGitClone

func runGitClone(ctx context.Context, src, target string, depth int, progress func(line string)) error {
	// SECURITY: DNS check, no redirects, https only, pinned address (git.PrepareClone).
	guard, err := git.PrepareClone(ctx, src)
	if err != nil {
		return err
	}
	args := append(guard, "clone", "--progress")
	if depth > 0 {
		args = append(args, "--depth", strconv.Itoa(depth))
	}
	args = append(args, "--", src, target)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = git.CloneEnv()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not run git: %w", err)
	}
	sc := bufio.NewScanner(stderr)
	sc.Split(splitCR)
	var tail []string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		progress(line)
		if !cloneStageRe.MatchString(line) {
			tail = append(tail, line)
			if len(tail) > 6 {
				tail = tail[1:]
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return errors.New("git clone timed out after 15 minutes")
		}
		if ctx.Err() == context.Canceled {
			return context.Canceled
		}
		msg := strings.Join(tail, "\n")
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

// ==================== Handlers ====================

func (h *ProjectHandler) defaultProjectsDir() string {
	raw := ""
	if h.repo != nil {
		if s, err := h.repo.GetUserSetting("default_clone_path"); err == nil && s != nil {
			raw = strings.Trim(strings.TrimSpace(s.Value), `"`)
		}
	}
	if raw == "" {
		raw = "~/projects"
	}
	if p, err := expandUserPath(raw); err == nil {
		return p
	}
	return filepath.Join(userHome(), "projects")
}

// HandleSetupDefaults handles GET /api/projects/setup/defaults
func (h *ProjectHandler) HandleSetupDefaults(c *fiber.Ctx) error {
	home := userHome()
	dir := h.defaultProjectsDir()
	_, gitErr := exec.LookPath("git")
	return c.JSON(fiber.Map{
		"home":                 home,
		"projects_dir":         dir,
		"projects_dir_display": displayPath(dir, home),
		"git_available":        gitErr == nil,
	})
}

// HandleSetupInspect handles GET /api/projects/setup/inspect?path=
func (h *ProjectHandler) HandleSetupInspect(c *fiber.Ctx) error {
	home := userHome()
	p, err := expandUserPath(c.Query("path"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	res := fiber.Map{
		"path":           p,
		"display_path":   displayPath(p, home),
		"suggested_name": filepath.Base(p),
		"exists":         false,
		"is_dir":         false,
	}
	if info, statErr := os.Stat(p); statErr == nil {
		res["exists"] = true
		res["is_dir"] = info.IsDir()
		if info.IsDir() {
			res["writable"] = dirWritable(p)
			if entries, rdErr := readDirNames(p, 3); rdErr == nil {
				res["empty"] = len(entries) == 0
			} else {
				res["readable"] = false
			}
			ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
			res["git"] = probeGit(ctx, p, true)
			cancel()
		}
	} else {
		parent := filepath.Dir(p)
		pinfo, perr := os.Stat(parent)
		res["parent_exists"] = perr == nil && pinfo.IsDir()
		if err := checkCreatablePath(p); err != nil {
			res["create_error"] = err.Error()
		} else if verr := validateFolderName(filepath.Base(p)); verr != nil {
			res["create_error"] = verr.Error()
		}
		res["can_create"] = res["create_error"] == nil
	}
	if h.repo != nil {
		if existing, gerr := h.repo.GetProjectByPath(p); gerr == nil && existing != nil {
			res["existing_project"] = fiber.Map{"id": existing.ID, "name": existing.Name, "color": existing.Color, "path": existing.Path}
		}
	}
	return c.JSON(res)
}

// readDirNames lists up to n entries, ignoring .DS_Store.
func readDirNames(dir string, n int) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(n + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	out := names[:0]
	for _, name := range names {
		if name != ".DS_Store" {
			out = append(out, name)
		}
	}
	return out, nil
}

// HandleSetupComplete handles GET /api/projects/setup/complete?prefix=
// It lists matching sub-directories for the typed-path autocomplete.
func (h *ProjectHandler) HandleSetupComplete(c *fiber.Ctx) error {
	home := userHome()
	raw := strings.TrimSpace(c.Query("prefix"))
	if raw == "" {
		raw = "~/"
	}
	trailing := strings.HasSuffix(raw, "/")
	p, err := expandUserPath(raw)
	if err != nil {
		return c.JSON(fiber.Map{"suggestions": []fiber.Map{}})
	}
	dir, base := p, ""
	if !trailing && raw != "~" {
		dir, base = filepath.Dir(p), filepath.Base(p)
	}
	f, err := os.Open(dir)
	if err != nil {
		return c.JSON(fiber.Map{"suggestions": []fiber.Map{}})
	}
	defer f.Close()
	entries, _ := f.ReadDir(completeMaxScan)
	lowerBase := strings.ToLower(base)
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(name), lowerBase) {
			continue
		}
		isDir := e.IsDir()
		if !isDir && e.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(dir, name)); err == nil && info.IsDir() {
				isDir = true
			}
		}
		if isDir {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	if len(names) > completeMaxResults {
		names = names[:completeMaxResults]
	}
	out := make([]fiber.Map, 0, len(names))
	for _, name := range names {
		full := filepath.Join(dir, name)
		out = append(out, fiber.Map{"path": full, "display": displayPath(full, home), "name": name})
	}
	return c.JSON(fiber.Map{"suggestions": out})
}

type setupRequest struct {
	Mode        string `json:"mode"` // existing | empty | clone
	Name        string `json:"name"`
	Path        string `json:"path"`   // existing: the folder
	Parent      string `json:"parent"` // empty/clone: where the new folder goes
	Folder      string `json:"folder"` // empty/clone: the new folder's name
	Source      string `json:"source"` // clone: URL or owner/repo
	Depth       int    `json:"depth"`  // clone: optional shallow depth
	GitInit     bool   `json:"git_init"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

func (h *ProjectHandler) newProjectRecord(req *setupRequest, path string, settings string) (*database.Project, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = filepath.Base(path)
	}
	color := strings.TrimSpace(req.Color)
	if !hexColorRe.MatchString(color) {
		color = randomProjectColor()
	}
	now := time.Now()
	p := &database.Project{
		ID:          uuid.New().String(),
		Name:        name,
		Path:        path,
		Description: strings.TrimSpace(req.Description),
		Color:       color,
		Settings:    settings,
		IsActive:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := h.repo.CreateProject(p); err != nil {
		return nil, err
	}
	return p, nil
}

func setupError(c *fiber.Ctx, status int, msg string) error {
	return c.Status(status).JSON(fiber.Map{"error": msg})
}

// HandleSetupProject handles POST /api/projects/setup
func (h *ProjectHandler) HandleSetupProject(c *fiber.Ctx) error {
	var req setupRequest
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return setupError(c, 400, "Invalid request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	if len(req.Name) > maxProjectNameLen {
		return setupError(c, 400, fmt.Sprintf("Name is longer than %d characters", maxProjectNameLen))
	}
	if len(req.Description) > maxProjectDescLen {
		return setupError(c, 400, "Description is too long")
	}
	if h.repo == nil {
		return setupError(c, 503, "Database unavailable")
	}

	switch req.Mode {
	case "existing":
		path, err := expandUserPath(req.Path)
		if err != nil {
			return setupError(c, 400, err.Error())
		}
		if path == string(filepath.Separator) {
			return setupError(c, 400, "Pick a project folder, not the root of the disk")
		}
		info, err := os.Stat(path)
		if err != nil {
			return setupError(c, 400, fmt.Sprintf("%s does not exist", displayPath(path, userHome())))
		}
		if !info.IsDir() {
			return setupError(c, 400, "That path is a file, not a folder")
		}
		// Never a folder whose files must not be served (home folder,
		// hidden folders such as ~/.claude/eggify or ~/.ssh, system folders).
		if err := checkProjectRoot(path); err != nil {
			return setupError(c, 400, err.Error())
		}
		if existing, gerr := h.repo.GetProjectByPath(path); gerr == nil && existing != nil {
			return c.Status(409).JSON(fiber.Map{"error": "This folder is already a project", "existing_project": existing})
		}
		p, err := h.newProjectRecord(&req, path, "")
		if err != nil {
			return setupError(c, 500, fmt.Sprintf("Failed to create project: %v", err))
		}
		return c.Status(201).JSON(fiber.Map{"project": p})

	case "empty":
		folder := strings.TrimSpace(req.Folder)
		if folder == "" {
			folder = req.Name
		}
		target, err := newFolderTarget(req.Parent, folder)
		if err != nil {
			return setupError(c, 400, err.Error())
		}
		if existing, gerr := h.repo.GetProjectByPath(target); gerr == nil && existing != nil {
			return c.Status(409).JSON(fiber.Map{"error": "This folder is already a project", "existing_project": existing})
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return setupError(c, 500, fmt.Sprintf("Could not create %s: %v", filepath.Dir(target), err))
		}
		if err := os.Mkdir(target, 0o755); err != nil {
			return setupError(c, 400, fmt.Sprintf("Could not create folder: %v", err))
		}
		warning := ""
		if req.GitInit {
			ctx, cancel := context.WithTimeout(c.Context(), 20*time.Second)
			cmd := gitexec.CommandContext(ctx, "init", "--quiet", target)
			if out, gerr := cmd.CombinedOutput(); gerr != nil {
				warning = fmt.Sprintf("Folder created, but git init failed: %s", strings.TrimSpace(string(out)))
			}
			cancel()
		}
		p, err := h.newProjectRecord(&req, target, "")
		if err != nil {
			_ = os.RemoveAll(target) // the folder did not exist before this request
			return setupError(c, 500, fmt.Sprintf("Failed to create project: %v", err))
		}
		res := fiber.Map{"project": p}
		if warning != "" {
			res["warning"] = warning
		}
		return c.Status(201).JSON(res)

	case "clone":
		src, err := parseCloneSource(req.Source)
		if err != nil {
			return setupError(c, 400, err.Error())
		}
		folder := strings.TrimSpace(req.Folder)
		if folder == "" {
			folder = src.Name
		}
		target, err := newFolderTarget(req.Parent, folder)
		if err != nil {
			return setupError(c, 400, err.Error())
		}
		if req.Depth < 0 || req.Depth > 1000000 {
			return setupError(c, 400, "Invalid depth")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return setupError(c, 500, fmt.Sprintf("Could not create %s: %v", filepath.Dir(target), err))
		}
		if req.Name == "" {
			req.Name = src.Name
		}
		job := h.startClone(&req, src, target)
		return c.Status(202).JSON(fiber.Map{"job": job})
	}
	return setupError(c, 400, "mode must be existing, empty or clone")
}

func (h *ProjectHandler) startClone(req *setupRequest, src *cloneSource, target string) *cloneJob {
	st := h.setupState()
	ctx, cancel := context.WithTimeout(context.Background(), cloneTimeout)
	job := &cloneJob{
		ID:        uuid.New().String(),
		Status:    "running",
		Stage:     "Starting",
		Path:      target,
		StartedAt: time.Now(),
		Output:    []string{},
		cancel:    cancel,
	}
	st.mu.Lock()
	st.prune(time.Now())
	st.jobs[job.ID] = job
	snap := *job
	st.mu.Unlock()

	reqCopy := *req
	go func() {
		defer cancel()
		err := cloneRunner(ctx, src.URL, target, req.Depth, func(line string) {
			st.update(job.ID, func(j *cloneJob) {
				if m := cloneStageRe.FindStringSubmatch(line); m != nil {
					pct, _ := strconv.Atoi(m[2])
					j.Stage = strings.TrimSpace(m[1])
					if overall := cloneOverall(j.Stage, pct); overall > j.Percent {
						j.Percent = overall
					}
					return
				}
				j.Output = append(j.Output, line)
				if len(j.Output) > 20 {
					j.Output = j.Output[len(j.Output)-20:]
				}
			})
		})
		now := time.Now()
		if err != nil {
			_ = os.RemoveAll(target) // the target did not exist before this job
			st.update(job.ID, func(j *cloneJob) {
				j.EndedAt = &now
				if errors.Is(err, context.Canceled) {
					j.Status, j.Error = "cancelled", "Clone cancelled"
				} else {
					j.Status, j.Error = "error", err.Error()
				}
			})
			return
		}
		settings, _ := json.Marshal(map[string]string{"clone_url": src.URL})
		p, perr := h.newProjectRecord(&reqCopy, target, string(settings))
		st.update(job.ID, func(j *cloneJob) {
			j.EndedAt = &now
			if perr != nil {
				j.Status, j.Error = "error", fmt.Sprintf("Cloned to %s, but the project could not be saved: %v", target, perr)
				return
			}
			j.Status, j.Stage, j.Percent, j.Project = "done", "Done", 100, p
		})
	}()
	return &snap
}

// HandleSetupJob handles GET /api/projects/setup/jobs/:jobId
func (h *ProjectHandler) HandleSetupJob(c *fiber.Ctx) error {
	job, ok := h.setupState().snapshot(c.Params("jobId"))
	if !ok {
		return setupError(c, 404, "Job not found")
	}
	return c.JSON(fiber.Map{"job": job})
}

// HandleCancelSetupJob handles DELETE /api/projects/setup/jobs/:jobId
func (h *ProjectHandler) HandleCancelSetupJob(c *fiber.Ctx) error {
	st := h.setupState()
	st.mu.Lock()
	j, ok := st.jobs[c.Params("jobId")]
	if ok && j.Status == "running" && j.cancel != nil {
		j.cancel()
	}
	st.mu.Unlock()
	if !ok {
		return setupError(c, 404, "Job not found")
	}
	return c.JSON(fiber.Map{"status": "cancelling"})
}

// projectOverview is one row of GET /api/projects/overview.
type projectOverview struct {
	*database.Project
	SessionCount int      `json:"session_count"`
	RunningCount int      `json:"running_count"`
	LastActivity string   `json:"last_activity,omitempty"`
	DisplayPath  string   `json:"display_path"`
	Missing      bool     `json:"missing"`
	Git          *gitInfo `json:"git,omitempty"`
	IconURL      string   `json:"icon_url,omitempty"`
	Summary      string   `json:"summary,omitempty"`
}

// HandleProjectsOverview handles GET /api/projects/overview: every project
// with its session activity and git branch/dirty state in one request.
// ?git=0 skips the git probe.
func (h *ProjectHandler) HandleProjectsOverview(c *fiber.Ctx) error {
	projects, err := h.repo.GetAllProjects()
	if err != nil {
		return setupError(c, 500, fmt.Sprintf("Failed to retrieve projects: %v", err))
	}
	activity, err := h.repo.Project.GetProjectsActivity()
	if err != nil {
		activity = map[string]*database.ProjectActivity{}
	}
	home := userHome()
	rows := make([]*projectOverview, len(projects))
	for i, p := range projects {
		row := &projectOverview{Project: p, DisplayPath: displayPath(p.Path, home)}
		if a := activity[p.ID]; a != nil {
			row.SessionCount, row.RunningCount, row.LastActivity = a.SessionCount, a.RunningCount, a.LastActivity
		}
		if info, statErr := os.Stat(p.Path); statErr != nil || !info.IsDir() {
			row.Missing = true
		} else {
			id := lookupProjectIdentity(p.Path)
			row.IconURL, row.Summary = projectIconURL(p.ID, id), id.Summary
		}
		rows[i] = row
	}

	if c.Query("git") != "0" {
		h.fillGit(c.Context(), rows)
	}
	return c.JSON(fiber.Map{"projects": rows, "home": home})
}

func (h *ProjectHandler) fillGit(parent context.Context, rows []*projectOverview) {
	st := h.setupState()
	now := time.Now()
	var todo []*projectOverview
	st.mu.Lock()
	for _, r := range rows {
		if r.Missing {
			continue
		}
		if cg, ok := st.gitCache[r.Path]; ok && now.Sub(cg.at) < overviewGitCacheTTL {
			info := cg.info
			r.Git = &info
			continue
		}
		todo = append(todo, r)
	}
	st.mu.Unlock()

	sem := make(chan struct{}, overviewGitWorkers)
	var wg sync.WaitGroup
	for _, r := range todo {
		wg.Add(1)
		sem <- struct{}{}
		go func(r *projectOverview) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(parent, overviewGitTimeout)
			info := probeGit(ctx, r.Path, false)
			timedOut := ctx.Err() != nil
			cancel()
			r.Git = &info
			if !timedOut {
				st.mu.Lock()
				st.gitCache[r.Path] = cachedGit{info: info, at: time.Now()}
				st.mu.Unlock()
			}
		}(r)
	}
	wg.Wait()
	st.mu.Lock()
	for p, cg := range st.gitCache {
		if time.Since(cg.at) > 4*overviewGitCacheTTL {
			delete(st.gitCache, p)
		}
	}
	st.mu.Unlock()
}

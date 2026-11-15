package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/schlunsen/eggify/internal/gitexec"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// GetGitBranch returns the current git branch for the given directory.
// Returns empty string if not a git repository or if there's an error.
func GetGitBranch(workingDir string) string {
	if workingDir == "" {
		return ""
	}

	cmd := gitexec.Command("rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = workingDir
	gitexec.Harden(cmd)

	output, err := cmd.Output()
	if err != nil {
		// Not a git repository or error occurred
		return ""
	}

	branch := strings.TrimSpace(string(output))
	return branch
}

// IsGitRepository checks if the given directory is a git repository
func IsGitRepository(workingDir string) bool {
	if workingDir == "" {
		return false
	}

	cmd := gitexec.Command("rev-parse", "--git-dir")
	cmd.Dir = workingDir
	gitexec.Harden(cmd)

	return cmd.Run() == nil
}

// ValidateWorkingDirectory validates that the provided directory path exists and is accessible
func ValidateWorkingDirectory(path string) error {
	if path == "" {
		return fmt.Errorf("working directory path cannot be empty")
	}

	// Check if the path exists
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("directory does not exist: %s", path)
		}
		return fmt.Errorf("cannot access directory %s: %w", path, err)
	}

	// Check if the path is a directory
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", path)
	}

	// Check if the directory is readable (by attempting to read it)
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("directory is not readable: %s", path)
	}
	defer f.Close()

	return nil
}

// BranchFileChange represents a file that changed on the worktree branch vs the source branch
type BranchFileChange struct {
	Path   string `json:"path"`
	Status string `json:"status"` // "added", "modified", "deleted", "renamed"
}

// GitStatusData represents the parsed git status
type GitStatusData struct {
	WorkingDirectory string             `json:"working_directory"`
	WorktreePath     string             `json:"worktree_path"`
	Branch           string             `json:"branch"`
	Ahead            int                `json:"ahead"`
	Behind           int                `json:"behind"`
	Staged           []string           `json:"staged"`
	Modified         []string           `json:"modified"`
	Untracked        []string           `json:"untracked"`
	Deleted          []string           `json:"deleted"`
	Clean            bool               `json:"clean"`
	PR               *GitHubPRInfo      `json:"pr,omitempty"`
	IsWorktree       bool               `json:"is_worktree,omitempty"`
	SourceBranch     string             `json:"source_branch,omitempty"`
	BranchFiles      []BranchFileChange `json:"branch_files,omitempty"`
}

// gitStatusCacheTTL bounds how stale a served git status may be. The status is
// polled per agent by the overview and per project by the git watcher, and each
// computation forks git processes; repeated and concurrent callers for the same
// directory share one result for this long.
const gitStatusCacheTTL = 3 * time.Second

// gitStatusStaleTTL is how long an entry is kept after it expires, so
// GetGitStatusQuick can answer from it while a refresh runs.
const gitStatusStaleTTL = 30 * time.Second

type gitStatusCacheEntry struct {
	data      *GitStatusData
	fetchedAt time.Time
}

var (
	gitStatusMu     sync.Mutex
	gitStatusCache  = map[string]gitStatusCacheEntry{}
	gitStatusFlight singleflight.Group
	gitStatusGen    uint64 // bumped by InvalidateGitStatus; guarded by gitStatusMu
	// Swapped out in tests.
	gitStatusCompute = computeGitStatus
	gitStatusNow     = time.Now
)

// GetGitStatus returns the git status for the given directory, served from a
// short-lived cache. Concurrent callers for the same directory share a single
// computation. The result is a private copy the caller may modify.
func GetGitStatus(workingDir string) (*GitStatusData, error) {
	if data, ok := lookupGitStatus(workingDir); ok {
		return data, nil
	}
	return fetchGitStatus(workingDir)
}

// GetGitStatusQuick is GetGitStatus for polled reads (the dashboard's git
// panel): an expired entry younger than gitStatusStaleTTL is answered at once
// and refreshed in the background, so the poll after it is current. A slow
// repository (or a busy machine reached over the relay) then costs the page
// one poll of staleness instead of seconds per request.
func GetGitStatusQuick(workingDir string) (*GitStatusData, error) {
	if data, ok := lookupGitStatus(workingDir); ok {
		return data, nil
	}
	gitStatusMu.Lock()
	entry, ok := gitStatusCache[workingDir]
	stale := ok && gitStatusNow().Sub(entry.fetchedAt) < gitStatusStaleTTL
	gitStatusMu.Unlock()
	if stale {
		go func() { _, _ = fetchGitStatus(workingDir) }()
		return entry.data.clone(), nil
	}
	return fetchGitStatus(workingDir)
}

// GetGitStatusFresh computes the git status now, bypassing the cache, and
// stores the result so later cached reads see it.
func GetGitStatusFresh(workingDir string) (*GitStatusData, error) {
	return fetchGitStatus(workingDir)
}

// InvalidateGitStatus drops any cached status for workingDir. Call it after
// eggify itself changes the repository so the next read is fresh.
func InvalidateGitStatus(workingDir string) {
	gitStatusMu.Lock()
	delete(gitStatusCache, workingDir)
	gitStatusGen++
	gitStatusMu.Unlock()
	// Later callers must not join a computation that started before the change.
	gitStatusFlight.Forget(workingDir)
	branchFilesMu.Lock()
	for key := range branchFilesCache {
		if strings.HasPrefix(key, workingDir+"\x00") {
			delete(branchFilesCache, key)
			branchFilesFlight.Forget(key)
		}
	}
	branchFilesMu.Unlock()
}

func lookupGitStatus(workingDir string) (*GitStatusData, bool) {
	gitStatusMu.Lock()
	defer gitStatusMu.Unlock()
	entry, ok := gitStatusCache[workingDir]
	if !ok || gitStatusNow().Sub(entry.fetchedAt) >= gitStatusCacheTTL {
		return nil, false
	}
	return entry.data.clone(), true
}

func fetchGitStatus(workingDir string) (*GitStatusData, error) {
	v, err, _ := gitStatusFlight.Do(workingDir, func() (interface{}, error) {
		gitStatusMu.Lock()
		gen := gitStatusGen
		gitStatusMu.Unlock()
		data, err := gitStatusCompute(workingDir)
		if err != nil {
			// Never serve a failure from the cache: a transient error is
			// retried and a removed directory reports itself immediately.
			gitStatusMu.Lock()
			delete(gitStatusCache, workingDir)
			gitStatusMu.Unlock()
			return nil, err
		}
		gitStatusMu.Lock()
		defer gitStatusMu.Unlock()
		if gen != gitStatusGen {
			// Invalidated while computing: answer this caller, cache nothing.
			return data, nil
		}
		now := gitStatusNow()
		for dir, entry := range gitStatusCache {
			if now.Sub(entry.fetchedAt) >= gitStatusStaleTTL {
				delete(gitStatusCache, dir)
			}
		}
		gitStatusCache[workingDir] = gitStatusCacheEntry{data: data, fetchedAt: now}
		return data, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*GitStatusData).clone(), nil
}

// clone copies the status so callers can enrich it without touching the cache.
func (d *GitStatusData) clone() *GitStatusData {
	if d == nil {
		return nil
	}
	c := *d
	c.Staged = append([]string{}, d.Staged...)
	c.Modified = append([]string{}, d.Modified...)
	c.Untracked = append([]string{}, d.Untracked...)
	c.Deleted = append([]string{}, d.Deleted...)
	if d.BranchFiles != nil {
		c.BranchFiles = append([]BranchFileChange{}, d.BranchFiles...)
	}
	if d.PR != nil {
		pr := *d.PR
		c.PR = &pr
	}
	return &c
}

// computeGitStatus runs `git status` and one `git rev-parse` (plus a cached gh
// lookup). It deliberately does not run `git update-index --refresh`: git
// status refreshes stat information itself, and update-index takes index.lock,
// which collides with the agent's own git commands.
func computeGitStatus(workingDir string) (*GitStatusData, error) {
	if workingDir == "" {
		return nil, fmt.Errorf("not a git repository")
	}

	// Execute: git status --porcelain -b
	cmd := gitexec.Command("status", "--porcelain", "-b")
	cmd.Dir = workingDir
	gitexec.Harden(cmd)

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && strings.Contains(string(exitErr.Stderr), "not a git repository") {
			return nil, fmt.Errorf("not a git repository")
		}
		return nil, fmt.Errorf("failed to get git status: %w", err)
	}

	// Parse output and categorize files
	data, err := parseGitStatusOutput(string(output))
	if err != nil {
		return nil, err
	}

	if workspace, err := inspectWorkspacePaths(workingDir); err == nil {
		data.WorkingDirectory = workspace.WorkingDirectory
		data.WorktreePath = workspace.WorktreePath
		data.IsWorktree = workspace.WorktreePath != ""
	}

	// Try to get GitHub PR info for the current branch (non-blocking). The
	// branch comes from the status header instead of another git process.
	if branch := statusHeaderBranch(string(output)); branch != "" {
		if prInfo, _ := gitHubPRForBranch(workingDir, branch); prInfo != nil {
			data.PR = prInfo
		}
	}

	return data, nil
}

// statusHeaderBranch returns the branch named by a `git status --porcelain -b`
// header the way `git rev-parse --abbrev-ref HEAD` would: "HEAD" when detached,
// and "" while the branch has no commits yet.
func statusHeaderBranch(output string) string {
	line, _, _ := strings.Cut(output, "\n")
	rest, ok := strings.CutPrefix(line, "## ")
	if !ok {
		return ""
	}
	switch {
	case strings.HasPrefix(rest, "HEAD (no branch)"):
		return "HEAD"
	case strings.HasPrefix(rest, "No commits yet on "), strings.HasPrefix(rest, "Initial commit on "):
		return ""
	}
	return parseBranchLine(line)
}

// parseGitStatusOutput parses the output of git status --porcelain -b
func parseGitStatusOutput(output string) (*GitStatusData, error) {
	lines := strings.Split(output, "\n")
	data := &GitStatusData{
		Staged:    []string{},
		Modified:  []string{},
		Untracked: []string{},
		Deleted:   []string{},
	}

	for _, line := range lines {
		// Parse branch info: ## main...origin/main [ahead 2, behind 1]
		if strings.HasPrefix(line, "##") {
			data.Branch = parseBranchLine(line)
			data.Ahead, data.Behind = parseAheadBehind(line)
			continue
		}

		if len(line) < 3 {
			continue
		}

		status := line[0:2]
		file := strings.TrimSpace(line[3:])

		// Handle renamed files (format: "R  old -> new")
		if strings.Contains(file, " -> ") {
			parts := strings.Split(file, " -> ")
			if len(parts) == 2 {
				file = parts[1] // Use the new filename
			}
		}

		switch {
		case status[0] == 'A': // Added to staging
			data.Staged = append(data.Staged, file)
		case status[0] == 'M': // Modified and staged
			data.Staged = append(data.Staged, file)
		case status[0] == 'D': // Deleted and staged
			data.Staged = append(data.Staged, file+" (deleted)")
		case status[0] == 'R': // Renamed and staged
			data.Staged = append(data.Staged, file)
		case status[1] == 'M': // Modified but not staged
			data.Modified = append(data.Modified, file)
		case status[1] == 'D': // Deleted but not staged
			data.Deleted = append(data.Deleted, file)
		case strings.HasPrefix(status, "??"): // Untracked
			data.Untracked = append(data.Untracked, file)
		}
	}

	data.Clean = len(data.Staged) == 0 &&
		len(data.Modified) == 0 &&
		len(data.Untracked) == 0 &&
		len(data.Deleted) == 0

	return data, nil
}

// parseBranchLine extracts the branch name from the git status header
func parseBranchLine(line string) string {
	// Format: ## branch-name...origin/branch-name [ahead 2, behind 1]
	// or: ## branch-name
	line = strings.TrimPrefix(line, "## ")

	// Remove tracking info and ahead/behind info
	if idx := strings.Index(line, "..."); idx != -1 {
		line = line[:idx]
	}
	if idx := strings.Index(line, " ["); idx != -1 {
		line = line[:idx]
	}

	return strings.TrimSpace(line)
}

// parseAheadBehind extracts ahead and behind counts from the git status header
func parseAheadBehind(line string) (int, int) {
	ahead := 0
	behind := 0

	// Format: ## branch...origin/branch [ahead 2, behind 1]
	// or: ## branch...origin/branch [ahead 2]
	// or: ## branch...origin/branch [behind 1]

	// Extract the bracketed section
	startIdx := strings.Index(line, "[")
	endIdx := strings.Index(line, "]")
	if startIdx == -1 || endIdx == -1 {
		return ahead, behind
	}

	bracketContent := line[startIdx+1 : endIdx]

	// Parse ahead
	aheadRegex := regexp.MustCompile(`ahead (\d+)`)
	if matches := aheadRegex.FindStringSubmatch(bracketContent); len(matches) > 1 {
		ahead, _ = strconv.Atoi(matches[1])
	}

	// Parse behind
	behindRegex := regexp.MustCompile(`behind (\d+)`)
	if matches := behindRegex.FindStringSubmatch(bracketContent); len(matches) > 1 {
		behind, _ = strconv.Atoi(matches[1])
	}

	return ahead, behind
}

// GitHubPRInfo represents information about a GitHub pull request
type GitHubPRInfo struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	State  string `json:"state"`
}

// GetGitHubRemoteURL extracts the GitHub repository URL from git remote
func GetGitHubRemoteURL(workingDir string) (string, error) {
	if !IsGitRepository(workingDir) {
		return "", fmt.Errorf("not a git repository")
	}

	// Get the remote URL
	cmd := gitexec.Command("remote", "get-url", "origin")
	cmd.Dir = workingDir
	gitexec.Harden(cmd)

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get remote URL: %w", err)
	}

	remoteURL := strings.TrimSpace(string(output))
	return remoteURL, nil
}

// ParseGitHubRepoFromURL extracts owner/repo from a GitHub URL
func ParseGitHubRepoFromURL(remoteURL string) (owner, repo string, err error) {
	// Handle SSH URLs: git@github.com:owner/repo.git
	// Handle HTTPS URLs: https://github.com/owner/repo.git

	var repoPath string

	if strings.HasPrefix(remoteURL, "git@github.com:") {
		repoPath = strings.TrimPrefix(remoteURL, "git@github.com:")
	} else if strings.HasPrefix(remoteURL, "https://github.com/") {
		repoPath = strings.TrimPrefix(remoteURL, "https://github.com/")
	} else {
		return "", "", fmt.Errorf("not a GitHub repository")
	}

	// Remove .git suffix
	repoPath = strings.TrimSuffix(repoPath, ".git")

	// Split into owner/repo
	parts := strings.Split(repoPath, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid GitHub repository path")
	}

	return parts[0], parts[1], nil
}

// githubPRCacheTTL bounds how stale PR information may be. The GitHub lookup is
// the only network call in the git status path, and that path is polled: the
// project watcher refreshes every 2s and the agent overview asks once per agent.
// Without caching that is thousands of `gh` invocations an hour, which exhausts
// the GitHub API rate limit. PR state changes rarely, so serving it from a short
// cache costs nothing and removes almost all of the traffic.
const githubPRCacheTTL = 2 * time.Minute

// githubPRTimeout stops a wedged `gh` call from blocking a watcher poll forever.
const githubPRTimeout = 10 * time.Second

type githubPRCacheEntry struct {
	info      *GitHubPRInfo
	fetchedAt time.Time
}

var (
	githubPRMu    sync.Mutex
	githubPRCache = map[string]githubPRCacheEntry{}
	githubPRLocks = map[string]*sync.Mutex{}
	// Swapped out in tests.
	githubPRFetch = fetchGitHubPRForBranch
	githubPRNow   = time.Now
)

// GetGitHubPRForBranch checks if there's a GitHub PR for the current branch using
// gh CLI, serving repeated lookups for the same branch from a short-lived cache.
func GetGitHubPRForBranch(workingDir string) (*GitHubPRInfo, error) {
	if !IsGitRepository(workingDir) {
		return nil, fmt.Errorf("not a git repository")
	}

	// Get current branch
	branch := GetGitBranch(workingDir)
	if branch == "" {
		return nil, fmt.Errorf("could not determine current branch")
	}
	return gitHubPRForBranch(workingDir, branch)
}

// gitHubPRForBranch looks up the PR for a branch the caller already resolved.
func gitHubPRForBranch(workingDir, branch string) (*GitHubPRInfo, error) {
	// Check if gh CLI is available
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh CLI not found")
	}

	return cachedGitHubPR(workingDir+"\x00"+branch, func() (*GitHubPRInfo, error) {
		return githubPRFetch(workingDir, branch)
	})
}

// cachedGitHubPR returns a cached lookup when one is fresh, and otherwise runs
// fetch once on behalf of every caller waiting on the same key. A branch with no
// PR is cached too: that is the common case, and it used to re-run `gh` on every
// single poll.
func cachedGitHubPR(key string, fetch func() (*GitHubPRInfo, error)) (*GitHubPRInfo, error) {
	if info, ok := lookupGitHubPR(key); ok {
		return info, nil
	}

	lock := githubPRKeyLock(key)
	lock.Lock()
	defer lock.Unlock()

	// Another caller may have populated the cache while we waited for the lock.
	if info, ok := lookupGitHubPR(key); ok {
		return info, nil
	}

	info, err := fetch()
	if err != nil {
		// Leave the cache alone so a transient failure is retried, not pinned.
		return nil, err
	}
	storeGitHubPR(key, info)
	return info, nil
}

func lookupGitHubPR(key string) (*GitHubPRInfo, bool) {
	githubPRMu.Lock()
	defer githubPRMu.Unlock()
	entry, ok := githubPRCache[key]
	if !ok || githubPRNow().Sub(entry.fetchedAt) >= githubPRCacheTTL {
		return nil, false
	}
	return entry.info, true
}

func storeGitHubPR(key string, info *GitHubPRInfo) {
	githubPRMu.Lock()
	defer githubPRMu.Unlock()
	now := githubPRNow()
	// Directories and branches come and go; drop what can no longer be served.
	for cached, entry := range githubPRCache {
		if now.Sub(entry.fetchedAt) >= githubPRCacheTTL {
			delete(githubPRCache, cached)
			delete(githubPRLocks, cached)
		}
	}
	githubPRCache[key] = githubPRCacheEntry{info: info, fetchedAt: now}
}

func githubPRKeyLock(key string) *sync.Mutex {
	githubPRMu.Lock()
	defer githubPRMu.Unlock()
	lock, ok := githubPRLocks[key]
	if !ok {
		lock = &sync.Mutex{}
		githubPRLocks[key] = lock
	}
	return lock
}

// fetchGitHubPRForBranch asks the gh CLI for the branch's PR. A missing PR is
// reported as (nil, nil) so it can be cached like any other answer.
func fetchGitHubPRForBranch(workingDir, branch string) (*GitHubPRInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), githubPRTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", "pr", "view", branch, "--json", "number,title,url,state")
	cmd.Dir = workingDir
	cmd.Env = gitexec.Env(nil) // gh runs git in this folder too

	output, err := cmd.Output()
	if err != nil {
		// No PR found for this branch (or other error)
		return nil, nil
	}

	// Parse JSON response
	var prInfo GitHubPRInfo
	if err := json.Unmarshal(output, &prInfo); err != nil {
		return nil, fmt.Errorf("failed to parse PR info: %w", err)
	}

	return &prInfo, nil
}

// GitDiffFile represents a single file's diff information
type GitDiffFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"` // "modified", "added", "deleted", "renamed"
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary,omitempty"`
	Diff      string `json:"diff"`
}

// GitDiffData represents the complete diff information
type GitDiffData struct {
	Stats struct {
		FilesChanged int `json:"filesChanged"`
		Additions    int `json:"additions"`
		Deletions    int `json:"deletions"`
	} `json:"stats"`
	Files []GitDiffFile `json:"files"`
}

// GetGitDiff returns the full diff for all changed files in the working directory
// If baseBranch is provided and working tree is clean, compares current branch against base branch
func GetGitDiff(workingDir string) (*GitDiffData, error) {
	return GetGitDiffWithBase(workingDir, "")
}

// safeGitRevRe matches the characters that can appear in a branch name, tag,
// remote-tracking ref or commit id. Anything else (spaces, quotes, shell or
// pathspec magic) is rejected.
var safeGitRevRe = regexp.MustCompile(`^[A-Za-z0-9._/@^~+-]+$`)

// ValidateGitRev reports whether rev is safe to pass to git as a revision.
// SECURITY: the base branch comes from a query parameter; a value starting
// with "-" would be parsed as an option (e.g. --output=<file> makes git diff
// write a file).
func ValidateGitRev(rev string) error {
	if rev == "" || len(rev) > 255 || strings.HasPrefix(rev, "-") ||
		strings.Contains(rev, "..") || !safeGitRevRe.MatchString(rev) {
		return fmt.Errorf("invalid git revision %q", rev)
	}
	return nil
}

// GetGitDiffWithBase returns the full diff, optionally against a base branch
func GetGitDiffWithBase(workingDir string, baseBranch string) (*GitDiffData, error) {
	if baseBranch != "" {
		if err := ValidateGitRev(baseBranch); err != nil {
			return nil, err
		}
	}
	if !IsGitRepository(workingDir) {
		return nil, fmt.Errorf("not a git repository")
	}

	// Get list of changed files first
	statusData, err := GetGitStatusFresh(workingDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get git status: %w", err)
	}

	// If working tree is clean and a base branch is provided, get diff against base branch
	if statusData.Clean && baseBranch != "" {
		currentBranch := GetGitBranch(workingDir)
		// Don't compare main/master against itself
		if currentBranch == "main" || currentBranch == "master" {
			return &GitDiffData{Files: []GitDiffFile{}}, nil
		}
		return getGitDiffVsBranch(workingDir, baseBranch)
	}

	diffData := &GitDiffData{
		Files: []GitDiffFile{},
	}

	// Collect all changed files (staged, modified, deleted)
	allFiles := make(map[string]string) // path -> status

	// Process staged files
	for _, file := range statusData.Staged {
		// Remove " (deleted)" suffix if present
		cleanFile := strings.TrimSuffix(file, " (deleted)")
		if strings.Contains(file, "(deleted)") {
			allFiles[cleanFile] = "deleted"
		} else {
			// Check if it's a new file or modified file
			cmd := gitexec.Command("diff", "--cached", "--name-status", "--", cleanFile)
			cmd.Dir = workingDir
			gitexec.Harden(cmd)
			output, _ := cmd.Output()
			status := strings.Fields(string(output))
			if len(status) > 0 {
				switch status[0] {
				case "A":
					allFiles[cleanFile] = "added"
				case "M":
					allFiles[cleanFile] = "modified"
				case "D":
					allFiles[cleanFile] = "deleted"
				default:
					allFiles[cleanFile] = "modified"
				}
			} else {
				allFiles[cleanFile] = "modified"
			}
		}
	}

	// Process modified files (unstaged)
	for _, file := range statusData.Modified {
		if _, exists := allFiles[file]; !exists {
			allFiles[file] = "modified"
		}
	}

	// Process untracked files
	for _, file := range statusData.Untracked {
		allFiles[file] = "added"
	}

	// Process deleted files
	for _, file := range statusData.Deleted {
		allFiles[file] = "deleted"
	}

	// Get diff for each file
	for path, status := range allFiles {
		fileDiff, err := getFileDiff(workingDir, path, status)
		if err != nil {
			// Log error but continue processing other files
			continue
		}

		diffData.Files = append(diffData.Files, *fileDiff)
		diffData.Stats.FilesChanged++
		diffData.Stats.Additions += fileDiff.Additions
		diffData.Stats.Deletions += fileDiff.Deletions
	}

	return diffData, nil
}

// getFileDiff gets the diff for a single file
func getFileDiff(workingDir, path, status string) (*GitDiffFile, error) {
	fileDiff := &GitDiffFile{
		Path:   path,
		Status: status,
	}

	var cmd *exec.Cmd

	switch status {
	case "added":
		// For new untracked files, read the content and format as added lines
		fullPath := filepath.Join(workingDir, path)
		if fileInfo, err := os.Stat(fullPath); err == nil && !fileInfo.IsDir() {
			// Check if file is staged or untracked
			statusCmd := gitexec.Command("status", "--porcelain", "--", path)
			statusCmd.Dir = workingDir
			gitexec.Harden(statusCmd)
			statusOutput, _ := statusCmd.Output()
			statusStr := strings.TrimSpace(string(statusOutput))

			if strings.HasPrefix(statusStr, "A ") || strings.HasPrefix(statusStr, "A") {
				// Staged new file - use git diff --cached
				cmd = gitexec.Command("diff", "--cached", "--", path)
				cmd.Dir = workingDir
				gitexec.Harden(cmd)
				output, _ := cmd.Output()
				fileDiff.Diff = string(output)
			} else {
				// Untracked file: a patch that adds it, built without
				// following symlinks; binary files carry no text.
				u := untrackedFile(workingDir, path)
				fileDiff.Diff, fileDiff.Additions, fileDiff.Binary = u.Diff, u.Additions, u.Binary
			}
		}

	case "deleted":
		// For deleted files, show the entire content as removed
		cmd = gitexec.Command("diff", "HEAD", "--", path)

	case "modified":
		// Check if file is staged or unstaged
		statusCmd := gitexec.Command("diff", "--name-only", "--cached", "--", path)
		statusCmd.Dir = workingDir
		gitexec.Harden(statusCmd)
		stagedOutput, _ := statusCmd.Output()

		if strings.TrimSpace(string(stagedOutput)) != "" {
			// File is staged - show staged diff
			cmd = gitexec.Command("diff", "--cached", "--", path)
		} else {
			// File is unstaged - show working tree diff
			cmd = gitexec.Command("diff", "--", path)
		}

	default:
		cmd = gitexec.Command("diff", "--", path)
	}

	// Execute git command if set (for modified/deleted files)
	if cmd != nil && fileDiff.Diff == "" {
		cmd.Dir = workingDir
		gitexec.Harden(cmd)
		output, err := cmd.Output()
		if err == nil {
			fileDiff.Diff = string(output)

			// Count additions and deletions
			for _, line := range strings.Split(fileDiff.Diff, "\n") {
				if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
					fileDiff.Additions++
				} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
					fileDiff.Deletions++
				}
			}
		}
	}

	return fileDiff, nil
}

// getGitDiffVsBranch gets the diff between current branch and base branch
func getGitDiffVsBranch(workingDir, baseBranch string) (*GitDiffData, error) {
	diffData := &GitDiffData{
		Files: []GitDiffFile{},
	}

	// Get list of changed files between branches: git diff --name-status base...HEAD
	cmd := gitexec.Command("diff", "--name-status", baseBranch+"...HEAD", "--")
	cmd.Dir = workingDir
	gitexec.Harden(cmd)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get branch diff: %w", err)
	}

	// Parse file status
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		statusCode := parts[0]
		filePath := parts[1]

		var status string
		switch statusCode[0] {
		case 'A':
			status = "added"
		case 'M':
			status = "modified"
		case 'D':
			status = "deleted"
		case 'R':
			status = "renamed"
			if len(parts) > 2 {
				filePath = parts[2] // Use new name for renamed files
			}
		default:
			status = "modified"
		}

		// Get diff for this file
		fileDiff, err := getFileDiffVsBranch(workingDir, filePath, baseBranch, status)
		if err != nil {
			continue
		}

		diffData.Files = append(diffData.Files, *fileDiff)
		diffData.Stats.FilesChanged++
		diffData.Stats.Additions += fileDiff.Additions
		diffData.Stats.Deletions += fileDiff.Deletions
	}

	return diffData, nil
}

// getFileDiffVsBranch gets the diff for a single file compared to base branch
func getFileDiffVsBranch(workingDir, path, baseBranch, status string) (*GitDiffFile, error) {
	fileDiff := &GitDiffFile{
		Path:   path,
		Status: status,
	}

	// Get diff for this file: git diff base...HEAD -- path
	cmd := gitexec.Command("diff", baseBranch+"...HEAD", "--", path)
	cmd.Dir = workingDir
	gitexec.Harden(cmd)

	output, err := cmd.Output()
	if err != nil {
		// For added files in branch, try showing full content
		if status == "added" {
			u := untrackedFile(workingDir, path)
			if !u.Omitted || u.Binary {
				fileDiff.Diff, fileDiff.Additions, fileDiff.Binary = u.Diff, u.Additions, u.Binary
				return fileDiff, nil
			}
		}
		return fileDiff, err
	}

	fileDiff.Diff = string(output)

	// Count additions and deletions
	for _, line := range strings.Split(fileDiff.Diff, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			fileDiff.Additions++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			fileDiff.Deletions++
		}
	}

	return fileDiff, nil
}

type branchFilesEntry struct {
	files     []BranchFileChange
	fetchedAt time.Time
}

var (
	branchFilesMu     sync.Mutex
	branchFilesCache  = map[string]branchFilesEntry{}
	branchFilesFlight singleflight.Group
	// Swapped out in tests.
	branchFilesCompute = GetBranchChangedFiles
)

// GetBranchChangedFilesQuick is GetBranchChangedFiles for the polled git
// panel, cached like GetGitStatusQuick: it runs several git commands, and the
// files committed on a branch only change with a commit.
func GetBranchChangedFilesQuick(workingDir, sourceBranch string) ([]BranchFileChange, error) {
	key := workingDir + "\x00" + sourceBranch
	branchFilesMu.Lock()
	entry, ok := branchFilesCache[key]
	branchFilesMu.Unlock()
	fetch := func() ([]BranchFileChange, error) {
		v, err, _ := branchFilesFlight.Do(key, func() (interface{}, error) {
			files, err := branchFilesCompute(workingDir, sourceBranch)
			branchFilesMu.Lock()
			defer branchFilesMu.Unlock()
			now := gitStatusNow()
			for k, e := range branchFilesCache {
				if now.Sub(e.fetchedAt) >= gitStatusStaleTTL {
					delete(branchFilesCache, k)
				}
			}
			if err != nil {
				delete(branchFilesCache, key)
				return nil, err
			}
			branchFilesCache[key] = branchFilesEntry{files: files, fetchedAt: now}
			return files, nil
		})
		if err != nil {
			return nil, err
		}
		return append([]BranchFileChange(nil), v.([]BranchFileChange)...), nil
	}
	if ok {
		age := gitStatusNow().Sub(entry.fetchedAt)
		if age < gitStatusCacheTTL {
			return append([]BranchFileChange(nil), entry.files...), nil
		}
		if age < gitStatusStaleTTL {
			go func() { _, _ = fetch() }()
			return append([]BranchFileChange(nil), entry.files...), nil
		}
	}
	return fetch()
}

// GetBranchChangedFiles returns the list of files changed between the current branch and a source branch.
// This is useful for worktrees to show what files have been committed on the branch.
func GetBranchChangedFiles(workingDir string, sourceBranch string) ([]BranchFileChange, error) {
	if !IsGitRepository(workingDir) {
		return nil, fmt.Errorf("not a git repository")
	}

	if sourceBranch == "" {
		// Try to detect default branch (main or master)
		sourceBranch = detectDefaultBranch(workingDir)
		if sourceBranch == "" {
			return nil, fmt.Errorf("could not determine source branch")
		}
	}

	// Check if source branch exists locally, try with origin/ prefix if not
	checkCmd := gitexec.Command("rev-parse", "--verify", sourceBranch)
	checkCmd.Dir = workingDir
	gitexec.Harden(checkCmd)
	if err := checkCmd.Run(); err != nil {
		// Try with origin/ prefix
		originBranch := "origin/" + sourceBranch
		checkCmd2 := gitexec.Command("rev-parse", "--verify", originBranch)
		checkCmd2.Dir = workingDir
		gitexec.Harden(checkCmd2)
		if err := checkCmd2.Run(); err != nil {
			return nil, fmt.Errorf("source branch %s not found", sourceBranch)
		}
		sourceBranch = originBranch
	}

	// Get the list of changed files: git diff --name-status sourceBranch...HEAD
	cmd := gitexec.Command("diff", "--name-status", sourceBranch+"...HEAD")
	cmd.Dir = workingDir
	gitexec.Harden(cmd)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get branch diff: %w", err)
	}

	var files []BranchFileChange
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		statusCode := parts[0]
		filePath := parts[1]

		var status string
		switch statusCode[0] {
		case 'A':
			status = "added"
		case 'M':
			status = "modified"
		case 'D':
			status = "deleted"
		case 'R':
			status = "renamed"
			if len(parts) > 2 {
				filePath = parts[2]
			}
		default:
			status = "modified"
		}

		files = append(files, BranchFileChange{
			Path:   filePath,
			Status: status,
		})
	}

	return files, nil
}

// detectDefaultBranch tries to determine the default branch (main or master)
func detectDefaultBranch(workingDir string) string {
	// Try "main" first
	cmd := gitexec.Command("rev-parse", "--verify", "main")
	cmd.Dir = workingDir
	gitexec.Harden(cmd)
	if err := cmd.Run(); err == nil {
		return "main"
	}

	// Try "master"
	cmd = gitexec.Command("rev-parse", "--verify", "master")
	cmd.Dir = workingDir
	gitexec.Harden(cmd)
	if err := cmd.Run(); err == nil {
		return "master"
	}

	// Try origin/main
	cmd = gitexec.Command("rev-parse", "--verify", "origin/main")
	cmd.Dir = workingDir
	gitexec.Harden(cmd)
	if err := cmd.Run(); err == nil {
		return "origin/main"
	}

	// Try origin/master
	cmd = gitexec.Command("rev-parse", "--verify", "origin/master")
	cmd.Dir = workingDir
	gitexec.Harden(cmd)
	if err := cmd.Run(); err == nil {
		return "origin/master"
	}

	return ""
}

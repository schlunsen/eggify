package agents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/schlunsen/eggify/internal/gitexec"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Project git overview: branches, worktrees and their changes, for the
// project Git page (GET /api/projects/:id/git/overview|changes). Everything
// here is read-only and runs git without optional locks, so it never blocks
// an agent working in the same repository.

const (
	overviewMaxBranches   = 200
	overviewMaxWorktrees  = 40
	overviewRecentCommits = 15
	changesMaxCommits     = 100
	// A file's patch is cut after this many bytes (truncated: true).
	changesMaxFilePatch = 256 << 10
	// Once the response holds this many patch bytes, further files come
	// without their patch (omitted: true); the page loads them one by one.
	changesMaxTotalPatch = 6 << 20
	// Untracked files bigger than this are listed without content.
	untrackedMaxBytes   = 1 << 20
	changesMaxUntracked = 300
)

// emptyTreeSHA is git's empty tree, the base for a repository without commits.
const emptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// GitCommitInfo is one commit in a list.
type GitCommitInfo struct {
	SHA     string   `json:"sha"`
	Short   string   `json:"short"`
	Subject string   `json:"subject"`
	Author  string   `json:"author"`
	At      int64    `json:"at"` // unix seconds (committer date)
	Refs    []string `json:"refs,omitempty"`
}

// GitDirtyCounts counts a checkout's uncommitted changes.
type GitDirtyCounts struct {
	Staged     int `json:"staged"`
	Unstaged   int `json:"unstaged"`
	Untracked  int `json:"untracked"`
	Conflicted int `json:"conflicted"`
}

// Total is the number of changed paths.
func (d GitDirtyCounts) Total() int {
	return d.Staged + d.Unstaged + d.Untracked + d.Conflicted
}

// GitOverviewWorktree is one checkout of the repository.
type GitOverviewWorktree struct {
	Path       string          `json:"path"`
	Branch     string          `json:"branch,omitempty"` // empty when detached
	Head       string          `json:"head"`
	IsMain     bool            `json:"is_main"`
	Locked     bool            `json:"locked"`
	Prunable   bool            `json:"prunable"`
	Missing    bool            `json:"missing"`
	Dirty      *GitDirtyCounts `json:"dirty,omitempty"`
	Upstream   string          `json:"upstream,omitempty"`
	Ahead      int             `json:"ahead"` // vs upstream
	Behind     int             `json:"behind"`
	BaseAhead  int             `json:"base_ahead"` // vs the default branch
	BaseBehind int             `json:"base_behind"`
	LastCommit *GitCommitInfo  `json:"last_commit,omitempty"`
}

// GitOverviewBranch is one local branch.
type GitOverviewBranch struct {
	Name         string `json:"name"`
	Head         string `json:"head"`
	Subject      string `json:"subject"`
	Author       string `json:"author"`
	At           int64  `json:"at"`
	Upstream     string `json:"upstream,omitempty"`
	UpstreamGone bool   `json:"upstream_gone,omitempty"`
	Ahead        int    `json:"ahead"` // vs upstream
	Behind       int    `json:"behind"`
	BaseAhead    int    `json:"base_ahead"` // vs the default branch
	BaseBehind   int    `json:"base_behind"`
	Merged       bool   `json:"merged"` // reachable from the default branch
	Current      bool   `json:"current"`
	Worktree     string `json:"worktree,omitempty"` // checked out there
	IsDefault    bool   `json:"is_default"`
}

// GitRepoOverview is the whole overview.
type GitRepoOverview struct {
	Root           string                `json:"root"`
	CurrentBranch  string                `json:"current_branch"`
	DefaultBranch  string                `json:"default_branch"`
	Remote         string                `json:"remote,omitempty"`         // credentials removed
	RemoteWebURL   string                `json:"remote_web_url,omitempty"` // https URL for GitHub/GitLab-style hosts
	Worktrees      []GitOverviewWorktree `json:"worktrees"`
	Branches       []GitOverviewBranch   `json:"branches"`
	BranchesCapped bool                  `json:"branches_capped,omitempty"`
	RecentCommits  []GitCommitInfo       `json:"recent_commits"`
	GeneratedAt    time.Time             `json:"generated_at"`
}

// gitRun runs git in dir without optional locks or prompts.
func gitRun(ctx context.Context, dir string, args ...string) ([]byte, error) {
	full := append([]string{"--no-optional-locks", "-c", "core.quotePath=false", "-C", dir}, args...)
	cmd := gitexec.CommandContext(ctx, full...)
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("git %s: %s", args[0], msg)
	}
	return out, nil
}

func gitRunString(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := gitRun(ctx, dir, args...)
	return strings.TrimSpace(string(out)), err
}

// DefaultBranch picks the branch others are compared with: origin's HEAD,
// else main, else master, else the current branch.
func DefaultBranch(ctx context.Context, dir string) string {
	if out, err := gitRunString(ctx, dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		name := strings.TrimPrefix(out, "origin/")
		if name != "" && refExists(ctx, dir, "refs/heads/"+name) {
			return name
		}
	}
	for _, name := range []string{"main", "master"} {
		if refExists(ctx, dir, "refs/heads/"+name) {
			return name
		}
	}
	out, _ := gitRunString(ctx, dir, "branch", "--show-current")
	return out
}

func refExists(ctx context.Context, dir, ref string) bool {
	_, err := gitRun(ctx, dir, "show-ref", "--verify", "--quiet", ref)
	return err == nil
}

// SanitizeRemoteURL removes credentials from a remote URL and returns it with
// a browsable https URL when the host looks like a forge.
func SanitizeRemoteURL(raw string) (clean, web string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	// scp-like: git@github.com:owner/repo.git
	if !strings.Contains(raw, "://") {
		if at := strings.Index(raw, "@"); at >= 0 {
			raw = raw[at+1:]
		}
		host, path, ok := strings.Cut(raw, ":")
		if !ok || host == "" || strings.Contains(host, "/") {
			return raw, ""
		}
		path = strings.TrimSuffix(strings.TrimPrefix(path, "/"), ".git")
		return host + ":" + path, "https://" + host + "/" + path
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	u.User = nil
	clean = u.String()
	if u.Scheme == "https" || u.Scheme == "http" || u.Scheme == "ssh" || u.Scheme == "git" {
		path := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git")
		if u.Hostname() != "" && path != "" {
			web = "https://" + u.Hostname() + "/" + path
		}
	}
	return clean, web
}

// GetRepoOverview reads branches, worktrees and recent commits of the
// repository at dir.
func GetRepoOverview(ctx context.Context, dir string) (*GitRepoOverview, error) {
	root, err := gitRunString(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, errors.New("not a git repository")
	}
	ov := &GitRepoOverview{Root: root, GeneratedAt: time.Now(), Worktrees: []GitOverviewWorktree{}, Branches: []GitOverviewBranch{}, RecentCommits: []GitCommitInfo{}}
	ov.CurrentBranch, _ = gitRunString(ctx, root, "branch", "--show-current")
	ov.DefaultBranch = DefaultBranch(ctx, root)
	if remote, err := gitRunString(ctx, root, "remote", "get-url", "origin"); err == nil {
		ov.Remote, ov.RemoteWebURL = SanitizeRemoteURL(remote)
	}

	ov.Branches, ov.BranchesCapped = listOverviewBranches(ctx, root, ov.DefaultBranch, ov.CurrentBranch)
	ov.Worktrees = listOverviewWorktrees(ctx, root, ov.DefaultBranch)
	byWorktreeBranch := map[string]string{}
	for _, wt := range ov.Worktrees {
		if wt.Branch != "" {
			byWorktreeBranch[wt.Branch] = wt.Path
		}
	}
	for i := range ov.Branches {
		if p, ok := byWorktreeBranch[ov.Branches[i].Name]; ok && ov.Branches[i].Worktree == "" {
			ov.Branches[i].Worktree = p
		}
	}
	if ov.DefaultBranch != "" {
		ov.RecentCommits = gitLog(ctx, root, overviewRecentCommits, ov.DefaultBranch)
	}
	return ov, nil
}

// listOverviewBranches lists local branches, newest commit first.
func listOverviewBranches(ctx context.Context, root, base, current string) ([]GitOverviewBranch, bool) {
	fields := []string{"%(refname:short)", "%(objectname:short)", "%(upstream:short)", "%(upstream:track,nobracket)",
		"%(committerdate:unix)", "%(authorname)", "%(contents:subject)", "%(worktreepath)"}
	withAB := base != "" && refExists(ctx, root, "refs/heads/"+base)
	format := strings.Join(fields, "%00")
	if withAB {
		format += "%00%(ahead-behind:refs/heads/" + base + ")"
	}
	args := []string{"for-each-ref", "--sort=-committerdate", fmt.Sprintf("--count=%d", overviewMaxBranches+1), "--format=" + format, "refs/heads"}
	out, err := gitRun(ctx, root, args...)
	nativeAB := withAB
	if err != nil && withAB {
		// git older than 2.41 has no ahead-behind atom.
		nativeAB = false
		format = strings.Join(fields, "%00")
		args[3] = "--format=" + format
		out, err = gitRun(ctx, root, args...)
	}
	if err != nil {
		return []GitOverviewBranch{}, false
	}
	merged := map[string]bool{}
	if withAB {
		if m, err := gitRun(ctx, root, "for-each-ref", "--merged=refs/heads/"+base, "--format=%(refname:short)", "refs/heads"); err == nil {
			for _, name := range strings.Split(string(m), "\n") {
				if name = strings.TrimSpace(name); name != "" {
					merged[name] = true
				}
			}
		}
	}
	var branches []GitOverviewBranch
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\x00")
		if len(f) < len(fields) {
			continue
		}
		b := GitOverviewBranch{Name: f[0], Head: f[1], Upstream: f[2], Author: f[5], Subject: f[6], Worktree: f[7]}
		b.At, _ = strconv.ParseInt(f[4], 10, 64)
		b.Ahead, b.Behind, b.UpstreamGone = parseTrack(f[3])
		if nativeAB && len(f) > len(fields) {
			if a, bb, ok := strings.Cut(f[len(fields)], " "); ok {
				b.BaseAhead, _ = strconv.Atoi(a)
				b.BaseBehind, _ = strconv.Atoi(bb)
			}
		}
		b.Current = b.Name == current
		b.IsDefault = b.Name == base
		// The current branch is work in progress even before its first commit.
		b.Merged = merged[b.Name] && !b.IsDefault && !b.Current
		branches = append(branches, b)
	}
	capped := len(branches) > overviewMaxBranches
	if capped {
		branches = branches[:overviewMaxBranches]
	}
	if withAB && !nativeAB {
		for i := range branches {
			if i >= 60 {
				break
			}
			branches[i].BaseAhead, branches[i].BaseBehind = aheadBehind(ctx, root, "refs/heads/"+branches[i].Name, "refs/heads/"+base)
		}
	}
	if branches == nil {
		branches = []GitOverviewBranch{}
	}
	return branches, capped
}

// parseTrack reads %(upstream:track,nobracket): "ahead 2, behind 1", "gone".
func parseTrack(s string) (ahead, behind int, gone bool) {
	if s == "gone" {
		return 0, 0, true
	}
	for _, part := range strings.Split(s, ",") {
		word, n, ok := strings.Cut(strings.TrimSpace(part), " ")
		if !ok {
			continue
		}
		v, _ := strconv.Atoi(n)
		switch word {
		case "ahead":
			ahead = v
		case "behind":
			behind = v
		}
	}
	return
}

// aheadBehind counts commits on ref not on base, and on base not on ref.
func aheadBehind(ctx context.Context, dir, ref, base string) (int, int) {
	out, err := gitRunString(ctx, dir, "rev-list", "--left-right", "--count", ref+"..."+base, "--")
	if err != nil {
		return 0, 0
	}
	a, b, ok := strings.Cut(out, "\t")
	if !ok {
		return 0, 0
	}
	ahead, _ := strconv.Atoi(a)
	behind, _ := strconv.Atoi(strings.TrimSpace(b))
	return ahead, behind
}

// listOverviewWorktrees lists every checkout with its uncommitted changes and
// position relative to its upstream and the default branch. Each checkout is
// probed in parallel.
func listOverviewWorktrees(ctx context.Context, root, base string) []GitOverviewWorktree {
	out, err := gitRun(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return []GitOverviewWorktree{}
	}
	infos := parseWorktreeListOutput(string(out))
	if len(infos) > overviewMaxWorktrees {
		infos = infos[:overviewMaxWorktrees]
	}
	result := make([]GitOverviewWorktree, len(infos))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i, info := range infos {
		branch := info.Branch
		if branch == "(detached)" {
			branch = ""
		}
		result[i] = GitOverviewWorktree{
			Path: info.Path, Branch: branch, Head: info.HEAD, IsMain: i == 0 || info.IsMain,
			Locked: info.Locked, Prunable: info.Prunable,
		}
		if st, err := os.Stat(info.Path); err != nil || !st.IsDir() {
			result[i].Missing = true
			continue
		}
		wg.Add(1)
		go func(wt *GitOverviewWorktree) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			probeOverviewWorktree(ctx, wt, base)
		}(&result[i])
	}
	wg.Wait()
	return result
}

func probeOverviewWorktree(ctx context.Context, wt *GitOverviewWorktree, base string) {
	if out, err := gitRun(ctx, wt.Path, "status", "--porcelain=v2", "--branch", "-z"); err == nil {
		dirty, upstream, ahead, behind := parseStatusV2(out)
		wt.Dirty, wt.Upstream, wt.Ahead, wt.Behind = &dirty, upstream, ahead, behind
	}
	if commits := gitLog(ctx, wt.Path, 1, "HEAD"); len(commits) == 1 {
		wt.LastCommit = &commits[0]
	}
	if base != "" && wt.Branch != base {
		wt.BaseAhead, wt.BaseBehind = aheadBehind(ctx, wt.Path, "HEAD", "refs/heads/"+base)
	}
}

// parseStatusV2 reads `git status --porcelain=v2 --branch -z`.
func parseStatusV2(out []byte) (d GitDirtyCounts, upstream string, ahead, behind int) {
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		switch {
		case strings.HasPrefix(e, "# branch.upstream "):
			upstream = strings.TrimPrefix(e, "# branch.upstream ")
		case strings.HasPrefix(e, "# branch.ab "):
			for _, f := range strings.Fields(strings.TrimPrefix(e, "# branch.ab ")) {
				v, _ := strconv.Atoi(f[1:])
				if f[0] == '+' {
					ahead = v
				} else {
					behind = v
				}
			}
		case strings.HasPrefix(e, "1 "), strings.HasPrefix(e, "2 "):
			if len(e) >= 4 {
				if e[2] != '.' {
					d.Staged++
				}
				if e[3] != '.' {
					d.Unstaged++
				}
			}
			if strings.HasPrefix(e, "2 ") {
				i++ // the rename's original path
			}
		case strings.HasPrefix(e, "u "):
			d.Conflicted++
		case strings.HasPrefix(e, "? "):
			d.Untracked++
		}
	}
	return
}

// gitLog lists up to n commits reachable from rev (a "a..b" range is allowed
// for internal callers).
func gitLog(ctx context.Context, dir string, n int, rev string) []GitCommitInfo {
	out, err := gitRun(ctx, dir, "log", fmt.Sprintf("-n%d", n), "--no-color", "--format=%H%x00%h%x00%s%x00%an%x00%ct%x00%D%x1e", rev, "--")
	if err != nil {
		return []GitCommitInfo{}
	}
	commits := []GitCommitInfo{}
	for _, rec := range strings.Split(string(out), "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x00")
		if len(f) < 6 {
			continue
		}
		c := GitCommitInfo{SHA: f[0], Short: f[1], Subject: f[2], Author: f[3]}
		c.At, _ = strconv.ParseInt(f[4], 10, 64)
		for _, r := range strings.Split(strings.TrimSpace(f[5]), ", ") {
			if r = strings.TrimSpace(r); r != "" {
				c.Refs = append(c.Refs, r)
			}
		}
		commits = append(commits, c)
	}
	return commits
}

// ---------------------------------------------------------------- changes

// GitChangeFile is one changed file. Diff holds the unified patch (headers
// included) unless the file is binary or its patch was omitted.
type GitChangeFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Status    string `json:"status"` // added, modified, deleted, renamed, copied, typechange, conflicted
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary,omitempty"`
	Size      int64  `json:"size,omitempty"` // untracked files
	Staged    bool   `json:"staged,omitempty"`
	Unstaged  bool   `json:"unstaged,omitempty"`
	Untracked bool   `json:"untracked,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Omitted   bool   `json:"omitted,omitempty"`
	Diff      string `json:"diff,omitempty"`
}

// GitChanges is a set of changes: a checkout's uncommitted work, or what a
// branch adds on top of the default branch.
type GitChanges struct {
	Scope   string          `json:"scope"` // uncommitted | branch
	Base    string          `json:"base,omitempty"`
	Ref     string          `json:"ref,omitempty"`
	Stats   GitChangeStats  `json:"stats"`
	Files   []GitChangeFile `json:"files"`
	Commits []GitCommitInfo `json:"commits,omitempty"`
}

// GitChangeStats sums the files.
type GitChangeStats struct {
	FilesChanged int `json:"files_changed"`
	Additions    int `json:"additions"`
	Deletions    int `json:"deletions"`
}

// GetBranchChanges returns what ref adds on top of base (base...ref) and the
// commits involved. dir is any checkout of the repository. onlyPath limits it
// to one file.
func GetBranchChanges(ctx context.Context, dir, base, ref, onlyPath string) (*GitChanges, error) {
	if err := ValidateGitRev(base); err != nil {
		return nil, err
	}
	if err := ValidateGitRev(ref); err != nil {
		return nil, err
	}
	ch := &GitChanges{Scope: "branch", Base: base, Ref: ref}
	files, err := diffFiles(ctx, dir, []string{base + "..." + ref}, onlyPath)
	if err != nil {
		return nil, err
	}
	ch.Files = files
	if onlyPath == "" {
		ch.Commits = gitLog(ctx, dir, changesMaxCommits, base+".."+ref)
	}
	ch.sum()
	return ch, nil
}

// GetUncommittedChanges returns everything in the checkout at dir that is not
// committed: staged and unstaged changes to tracked files (vs HEAD) and
// untracked files.
func GetUncommittedChanges(ctx context.Context, dir, onlyPath string) (*GitChanges, error) {
	ch := &GitChanges{Scope: "uncommitted", Ref: "HEAD"}
	base := "HEAD"
	if _, err := gitRun(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
		base = emptyTreeSHA
	}
	files, err := diffFiles(ctx, dir, []string{base}, onlyPath)
	if err != nil {
		return nil, err
	}
	// Staged / unstaged / conflicted markers from git status.
	if out, err := gitRun(ctx, dir, "status", "--porcelain=v2", "-z", "--untracked-files=all"); err == nil {
		marks := statusMarks(out)
		for i := range files {
			if m, ok := marks[files[i].Path]; ok {
				files[i].Staged, files[i].Unstaged = m.staged, m.unstaged
				if m.conflicted {
					files[i].Status = "conflicted"
				}
			}
		}
		var untracked []string
		for p, m := range marks {
			// A trailing slash is a nested repository (e.g. a linked worktree).
			if m.untracked && !strings.HasSuffix(p, "/") {
				untracked = append(untracked, p)
			}
		}
		sort.Strings(untracked)
		total := patchBytes(files)
		for i, p := range untracked {
			if onlyPath != "" && p != onlyPath {
				continue
			}
			if i >= changesMaxUntracked && onlyPath == "" {
				files = append(files, GitChangeFile{Path: p, Status: "added", Untracked: true, Omitted: true})
				continue
			}
			f := untrackedFile(dir, p)
			if total > changesMaxTotalPatch && onlyPath == "" && f.Diff != "" {
				f.Diff, f.Omitted = "", true
			}
			total += len(f.Diff)
			files = append(files, f)
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	ch.Files = files
	ch.sum()
	return ch, nil
}

func (ch *GitChanges) sum() {
	if ch.Files == nil {
		ch.Files = []GitChangeFile{}
	}
	ch.Stats = GitChangeStats{FilesChanged: len(ch.Files)}
	for _, f := range ch.Files {
		ch.Stats.Additions += f.Additions
		ch.Stats.Deletions += f.Deletions
	}
}

func patchBytes(files []GitChangeFile) int {
	n := 0
	for _, f := range files {
		n += len(f.Diff)
	}
	return n
}

type statusMark struct{ staged, unstaged, untracked, conflicted bool }

// statusMarks maps paths to their state in `git status --porcelain=v2 -z`.
func statusMarks(out []byte) map[string]statusMark {
	marks := map[string]statusMark{}
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		switch {
		case strings.HasPrefix(e, "1 "):
			// 1 XY sub mH mI mW hH hI path
			if f := strings.SplitN(e, " ", 9); len(f) == 9 {
				marks[f[8]] = statusMark{staged: f[1][0] != '.', unstaged: f[1][1] != '.'}
			}
		case strings.HasPrefix(e, "2 "):
			// 2 XY sub mH mI mW hH hI Xscore path, then origPath
			if f := strings.SplitN(e, " ", 10); len(f) == 10 {
				marks[f[9]] = statusMark{staged: f[1][0] != '.', unstaged: f[1][1] != '.'}
			}
			i++
		case strings.HasPrefix(e, "u "):
			// u XY sub m1 m2 m3 mW h1 h2 h3 path
			if f := strings.SplitN(e, " ", 11); len(f) == 11 {
				marks[f[10]] = statusMark{conflicted: true, unstaged: true}
			}
		case strings.HasPrefix(e, "? "):
			marks[strings.TrimPrefix(e, "? ")] = statusMark{untracked: true}
		}
	}
	return marks
}

// diffFiles runs `git diff <revs>` once for stats (numstat), statuses
// (name-status) and the patch, and pairs them up by order.
func diffFiles(ctx context.Context, dir string, revs []string, onlyPath string) ([]GitChangeFile, error) {
	common := []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "-M", "-z"}
	tail := append(append([]string{}, revs...), "--")
	if onlyPath != "" {
		tail = append(tail, ":(literal)"+onlyPath)
	}
	nameStatus, err := gitRun(ctx, dir, append(append(append([]string{}, common...), "--name-status"), tail...)...)
	if err != nil {
		return nil, err
	}
	files := parseNameStatusZ(nameStatus)
	if len(files) == 0 {
		return []GitChangeFile{}, nil
	}
	if numstat, err := gitRun(ctx, dir, append(append(append([]string{}, common...), "--numstat"), tail...)...); err == nil {
		applyNumstatZ(files, numstat)
	}
	patchArgs := append([]string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "-M"}, tail...)
	patch, err := gitRun(ctx, dir, patchArgs...)
	if err == nil {
		chunks := splitPatch(patch)
		if len(chunks) == len(files) {
			total := 0
			for i := range files {
				if files[i].Binary {
					continue
				}
				d := chunks[i]
				if len(d) > changesMaxFilePatch {
					d, files[i].Truncated = cutAtLine(d, changesMaxFilePatch), true
				}
				if total > changesMaxTotalPatch && onlyPath == "" {
					files[i].Omitted = true
					continue
				}
				total += len(d)
				files[i].Diff = d
			}
		} else {
			for i := range files {
				files[i].Omitted = !files[i].Binary
			}
		}
	}
	return files, nil
}

// parseNameStatusZ reads `--name-status -z`: status \0 path \0 (renames and
// copies: status \0 old \0 new \0).
func parseNameStatusZ(out []byte) []GitChangeFile {
	f := strings.Split(string(out), "\x00")
	var files []GitChangeFile
	for i := 0; i < len(f); i++ {
		code := f[i]
		if code == "" {
			continue
		}
		if i+1 >= len(f) {
			break
		}
		file := GitChangeFile{}
		switch code[0] {
		case 'A':
			file.Status = "added"
		case 'D':
			file.Status = "deleted"
		case 'R', 'C':
			if code[0] == 'R' {
				file.Status = "renamed"
			} else {
				file.Status = "copied"
			}
			if i+2 >= len(f) {
				i = len(f)
				continue
			}
			file.OldPath, file.Path = f[i+1], f[i+2]
			files = append(files, file)
			i += 2
			continue
		case 'T':
			file.Status = "typechange"
		case 'U':
			file.Status = "conflicted"
		default:
			file.Status = "modified"
		}
		file.Path = f[i+1]
		files = append(files, file)
		i++
	}
	return files
}

// applyNumstatZ reads `--numstat -z`: "adds\tdels\tpath\0", or for renames
// "adds\tdels\t\0old\0new\0". Binary files have "-" counts.
func applyNumstatZ(files []GitChangeFile, out []byte) {
	f := strings.Split(string(out), "\x00")
	idx := 0
	for i := 0; i < len(f) && idx < len(files); i++ {
		rec := f[i]
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		if parts[2] == "" {
			i += 2 // old and new path follow
		}
		if parts[0] == "-" && parts[1] == "-" {
			files[idx].Binary = true
		} else {
			files[idx].Additions, _ = strconv.Atoi(parts[0])
			files[idx].Deletions, _ = strconv.Atoi(parts[1])
		}
		idx++
	}
}

// splitPatch cuts a full patch into one chunk per "diff --git" header.
func splitPatch(patch []byte) []string {
	s := string(patch)
	if s == "" {
		return nil
	}
	var chunks []string
	start := strings.Index(s, "diff --git ")
	if start < 0 {
		return nil
	}
	s = s[start:]
	for {
		next := strings.Index(s[1:], "\ndiff --git ")
		if next < 0 {
			chunks = append(chunks, strings.TrimRight(s, "\n"))
			break
		}
		chunks = append(chunks, s[:next+1])
		s = s[next+2:]
	}
	return chunks
}

func cutAtLine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	if i := strings.LastIndex(s, "\n"); i > 0 {
		s = s[:i]
	}
	return s
}

// untrackedFile describes an untracked file and, for small text files, a
// patch that adds it. Symlinks are never followed: the patch shows the link
// target, like git does, so a link to a secret outside the folder does not
// leak its content.
func untrackedFile(dir, rel string) GitChangeFile {
	f := GitChangeFile{Path: rel, Status: "added", Untracked: true}
	full := filepath.Join(dir, rel)
	if !pathInside(dir, full) {
		f.Omitted = true
		return f
	}
	info, err := os.Lstat(full)
	if err != nil {
		f.Omitted = true
		return f
	}
	f.Size = info.Size()
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(full)
		if err != nil {
			f.Omitted = true
			return f
		}
		f.Additions = 1
		f.Diff = untrackedPatch(rel, "120000", []string{target}, true)
		return f
	}
	if !info.Mode().IsRegular() {
		f.Omitted = true
		return f
	}
	if info.Size() > untrackedMaxBytes {
		f.Omitted = true
		f.Binary = looksBinaryFile(full)
		return f
	}
	content, err := os.ReadFile(full)
	if err != nil {
		f.Omitted = true
		return f
	}
	if isBinaryContent(content) {
		f.Binary = true
		return f
	}
	text := string(content)
	noNewline := !strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	f.Additions = len(lines)
	f.Diff = untrackedPatch(rel, "100644", lines, noNewline && text != "")
	if len(f.Diff) > changesMaxFilePatch {
		f.Diff, f.Truncated = cutAtLine(f.Diff, changesMaxFilePatch), true
	}
	return f
}

func untrackedPatch(rel, mode string, lines []string, noNewline bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\nnew file mode %s\n--- /dev/null\n+++ b/%s\n", rel, rel, mode, rel)
	if len(lines) == 0 {
		return strings.TrimRight(b.String(), "\n")
	}
	fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, l := range lines {
		b.WriteString("+")
		b.WriteString(l)
		b.WriteString("\n")
	}
	if noNewline {
		b.WriteString("\\ No newline at end of file\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// isBinaryContent applies git's rule: a NUL byte in the first 8000 bytes.
func isBinaryContent(b []byte) bool {
	if len(b) > 8000 {
		b = b[:8000]
	}
	return bytes.IndexByte(b, 0) >= 0
}

func looksBinaryFile(path string) bool {
	fh, err := os.Open(path)
	if err != nil {
		return false
	}
	defer fh.Close()
	buf := make([]byte, 8000)
	n, _ := fh.Read(buf)
	return isBinaryContent(buf[:n])
}

func pathInside(dir, full string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(full))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

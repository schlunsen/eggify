package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/stretchr/testify/require"
)

// A team never works in the checkout it was started in: in a git repository
// it gets its own worktree and branch at creation, in the same subfolder.
func TestTeamSessionGetsItsOwnWorktree(t *testing.T) {
	h, _, _ := newLoopSpawnHarness(t)
	sm := h.SessionManager
	repo := t.TempDir()
	base := gitRepo(t, repo)
	sub := filepath.Join(repo, "sub")

	id := uuid.New()
	team := teamOf("claude", "haiku", "codex", "gpt-6-astra")
	// A client can't place the team: the server decides where it works.
	team.Worktree = &TeamWorktree{Path: "/tmp/elsewhere", Branch: "main"}
	created, err := sm.CreateSession(id, SessionOptions{WorkingDirectory: &sub, Team: team})
	require.NoError(t, err)

	wt := created.Options.Team.Worktree
	require.NotNil(t, wt)
	require.Equal(t, "team/"+shortID(id), wt.Branch)
	require.Equal(t, base, wt.Base)
	require.Equal(t, filepath.Join(resolvePathForMatch(repo), ".worktrees"), resolvePathForMatch(filepath.Dir(wt.Path)))
	require.Equal(t, filepath.Join(wt.Path, "sub"), *created.Options.WorkingDirectory)
	require.FileExists(t, filepath.Join(wt.Path, "sub", "a.txt"))
	require.Equal(t, wt.Branch, GetGitBranch(wt.Path))
	require.Equal(t, base, GetGitBranch(repo), "the user's checkout stays on its branch")
	require.NotNil(t, created.WorktreeID)
	require.Equal(t, wt.Path, created.WorktreePath)

	// The brief tells the orchestrator where it is and to leave the main checkout alone.
	brief := teamBrief(created.Options.Team)
	require.Contains(t, brief, wt.Branch)
	require.Contains(t, brief, "do not touch the main checkout")

	// Its build workers branch off the team's branch, in worktrees of their own.
	session, err := sm.GetSession(id)
	require.NoError(t, err)
	plan, err := sm.prepareWorkerSession(session.ID, WorkerSessionRequest{Task: "build it"})
	require.NoError(t, err)
	require.NotNil(t, plan.worktree)
	require.Equal(t, wt.Branch, plan.worktree.Base)
	require.Equal(t, filepath.Dir(wt.Path), filepath.Dir(plan.worktree.Root), "worker worktrees sit next to the main checkout")

	// Ending the session keeps the worktree: its branch is what the user merges.
	require.NoError(t, os.WriteFile(filepath.Join(wt.Path, "sub", "wip.txt"), []byte("not committed\n"), 0o644))
	require.NoError(t, sm.EndSession(id))
	require.FileExists(t, filepath.Join(wt.Path, "sub", "wip.txt"))
	// The worktrees row needs a project; when there is one it must not ask for clean-up.
	if db := database.GetInstance(); db != nil && created.WorktreeID != nil {
		if row, err := database.NewRepository(db).GetWorktree(*created.WorktreeID); err == nil {
			require.False(t, row.AutoCleanup)
		}
	}
}

func TestTeamWorktreeBranchAndFolders(t *testing.T) {
	h, _, _ := newLoopSpawnHarness(t)
	sm := h.SessionManager

	// Outside a git repository there is nothing to branch from: the team works in the folder.
	plain := t.TempDir()
	created, err := sm.CreateSession(uuid.New(), SessionOptions{WorkingDirectory: &plain, Team: teamOf("claude", "haiku", "claude", "opus")})
	require.NoError(t, err)
	require.Nil(t, created.Options.Team.Worktree)
	require.Equal(t, plain, *created.Options.WorkingDirectory)
	require.NotContains(t, teamBrief(created.Options.Team), "main checkout")

	// A branch the user named is used, and checked like any git revision.
	repo := t.TempDir()
	gitRepo(t, repo)
	named := "team/changelog"
	created, err = sm.CreateSession(uuid.New(), SessionOptions{WorkingDirectory: &repo, Team: teamOf("claude", "haiku", "claude", "opus"), UseWorktree: boolPtr(true), WorktreeBranch: &named})
	require.NoError(t, err)
	require.Equal(t, named, created.Options.Team.Worktree.Branch)
	require.Nil(t, created.Options.UseWorktree)

	bad := "--upload-pack=evil"
	_, err = sm.CreateSession(uuid.New(), SessionOptions{WorkingDirectory: &repo, Team: teamOf("claude", "haiku", "claude", "opus"), WorktreeBranch: &bad})
	require.ErrorContains(t, err, "branch name")

	// git refusing the worktree (the branch exists) fails the creation: no fallback to the checkout.
	_, err = sm.CreateSession(uuid.New(), SessionOptions{WorkingDirectory: &repo, Team: teamOf("claude", "haiku", "claude", "opus"), WorktreeBranch: &named})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "team's git worktree"), err.Error())

	// A session without a team still works in its folder.
	created, err = sm.CreateSession(uuid.New(), SessionOptions{WorkingDirectory: &repo})
	require.NoError(t, err)
	require.Equal(t, repo, *created.Options.WorkingDirectory)
}

func boolPtr(v bool) *bool { return &v }

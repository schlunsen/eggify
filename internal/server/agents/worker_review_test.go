package agents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/n0"
	"github.com/stretchr/testify/require"
)

// gitRepo makes dir a git repository with one commit and returns its branch.
func gitRepo(t *testing.T, dir string) string {
	t.Helper()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "a.txt"), []byte("committed\n"), 0o644))
	run("add", "-A")
	run("commit", "-q", "-m", "first")
	return run("rev-parse", "--abbrev-ref", "HEAD")
}

// A build worker in a git repository gets its own worktree and branch,
// started from the proposer's last commit; nothing exists before approval.
func TestBuildWorkerGetsItsOwnWorktree(t *testing.T) {
	h, parent, rec := newLoopSpawnHarness(t)
	sm := h.SessionManager
	repo := *parent.Options.WorkingDirectory
	base := gitRepo(t, repo)
	// Uncommitted work of the proposer is not in the worktree.
	require.NoError(t, os.WriteFile(filepath.Join(repo, "uncommitted.txt"), []byte("x"), 0o644))

	done := startWorkerAsync(sm, parent.ID, WorkerSessionRequest{Task: "Add a Tag Filter to the sidebar!"})
	id := waitForQuestion(t, parent)
	q := parent.pendingQuestionMessages()[0]
	p := q.Proposal.(WorkerSessionProposal)
	require.True(t, strings.HasPrefix(p.Branch, "worker/"), p.Branch)
	require.Contains(t, p.Branch, "add-a-tag-filter-to-the-sidebar")
	require.Equal(t, base, p.BaseBranch)
	require.Equal(t, p.WorktreePath, p.WorkingDirectory)
	require.Contains(t, q.Question, p.Branch)
	_, err := os.Stat(p.WorktreePath)
	require.True(t, os.IsNotExist(err), "no worktree before the user answers")

	answerQuestion(t, parent, id, UserQuestionAnswerResponse{Answers: []string{WorkerSessionAnswerStart}})
	r := <-done
	require.NoError(t, r.err)
	require.Equal(t, p.Branch, r.res.Branch)

	child := childrenOf(sm, parent.ID)[0]
	require.Equal(t, p.WorktreePath, *child.Options.WorkingDirectory)
	require.Equal(t, p.Branch, child.Options.Worker.Branch)
	require.Equal(t, DefaultMaxReviewRounds, child.Options.Worker.MaxReviewRounds)
	require.Nil(t, child.WorktreeID, "no auto-cleanup record: ending the worker must not delete its work")
	require.FileExists(t, filepath.Join(p.WorktreePath, "sub", "a.txt"))
	require.NoFileExists(t, filepath.Join(p.WorktreePath, "uncommitted.txt"))
	branch, err := gitOutput(p.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD")
	require.NoError(t, err)
	require.Equal(t, p.Branch, branch)

	kickoff := rec.waitFor(t, child.ID).prompt
	require.Contains(t, kickoff, "Commit your work on this branch")
	require.Contains(t, kickoff, p.Branch)

	// Ending the worker keeps the worktree (its changes are uncommitted).
	require.NoError(t, os.WriteFile(filepath.Join(p.WorktreePath, "work.txt"), []byte("wip"), 0o644))
	require.NoError(t, sm.EndSession(child.ID))
	require.FileExists(t, filepath.Join(p.WorktreePath, "work.txt"))
}

func TestBuildWorkerWorktreeOptions(t *testing.T) {
	h, parent, _ := newLoopSpawnHarness(t)
	sm := h.SessionManager
	no, yes := false, true

	// Not a git repository: the folder is shared, unless a worktree was asked for.
	plan, err := sm.prepareWorkerSession(parent.ID, WorkerSessionRequest{Task: "x"})
	require.NoError(t, err)
	require.Nil(t, plan.worktree)
	require.Equal(t, *parent.Options.WorkingDirectory, plan.proposal.WorkingDirectory)
	_, err = sm.prepareWorkerSession(parent.ID, WorkerSessionRequest{Task: "x", Worktree: &yes})
	require.ErrorContains(t, err, "not in a git repository")

	repo := *parent.Options.WorkingDirectory
	gitRepo(t, repo)
	plan, err = sm.prepareWorkerSession(parent.ID, WorkerSessionRequest{Task: "x", Worktree: &no})
	require.NoError(t, err)
	require.NotNil(t, plan.worktree, "a build worker in a repository always gets its own worktree; worktree: false is ignored")
	require.NotEqual(t, resolvePathForMatch(repo), resolvePathForMatch(plan.proposal.WorkingDirectory))

	// A subfolder of the repository stays that subfolder inside the worktree.
	plan, err = sm.prepareWorkerSession(parent.ID, WorkerSessionRequest{Task: "x", WorkingDirectory: "sub"})
	require.NoError(t, err)
	require.NotNil(t, plan.worktree)
	require.Equal(t, filepath.Join(plan.worktree.Root, "sub"), plan.proposal.WorkingDirectory)
	require.Equal(t, filepath.Join(resolvePathForMatch(repo), ".worktrees"), resolvePathForMatch(filepath.Dir(plan.worktree.Root)))

	// A review worker never gets a worktree of its own.
	plan, err = sm.prepareWorkerSession(parent.ID, WorkerSessionRequest{Task: "x", Mode: "review"})
	require.NoError(t, err)
	require.Nil(t, plan.worktree)
}

func TestParseVerdict(t *testing.T) {
	for reply, want := range map[string]string{
		"Looks good.\n<verdict>APPROVE</verdict>":                                     VerdictApprove,
		"<verdict> approved </verdict>":                                               VerdictApprove,
		"1) bug\n<verdict>CHANGES_REQUESTED</verdict>":                                VerdictChangesRequested,
		"<verdict>changes requested</verdict>":                                        VerdictChangesRequested,
		"first <verdict>APPROVE</verdict> then\n<verdict>CHANGES_REQUESTED</verdict>": VerdictChangesRequested,
		"I approve of this.":                                                          "",
		"<verdict>MAYBE</verdict>":                                                    "",
	} {
		require.Equal(t, want, parseVerdict(reply), reply)
	}
}

// seedBuilder is a build worker of parent working in dir.
func seedBuilder(t *testing.T, sm *SessionManager, parent *AgentSession, dir string) *AgentSession {
	t.Helper()
	return seedSwitchSession(t, sm, "claude", "deepseek-flash", func(s *Session) {
		s.Options.SpawnedBySessionID = &parent.ID
		s.Options.WorkingDirectory = &dir
		s.Options.Worker = &WorkerConfig{Task: "build", Mode: WorkerModeBuild, WorktreePath: dir, Branch: "worker/abc-build", BaseBranch: "main", MaxReviewRounds: DefaultMaxReviewRounds}
	})
}

// The review loop: a review names its build worker, each verdict is a round,
// and after the last round with changes requested the builder is closed to
// the proposer.
func TestReviewLoopCountsRoundsAndStopsAtTheCap(t *testing.T) {
	h, parent, _ := newLoopSpawnHarness(t)
	sm := h.SessionManager
	wt := t.TempDir()
	builder := seedBuilder(t, sm, parent, wt)

	// The review runs read-only in the builder's folder.
	plan, err := sm.prepareWorkerSession(parent.ID, WorkerSessionRequest{Task: "review it", Mode: "review", ReviewsSessionID: builder.ID.String(), Provider: "codex", Model: "gpt-6-astra"})
	require.NoError(t, err)
	require.Equal(t, wt, plan.proposal.WorkingDirectory)
	require.True(t, plan.opts.ReadOnly)
	require.Equal(t, 1, plan.proposal.ReviewRound)
	require.Equal(t, DefaultMaxReviewRounds, plan.proposal.MaxReviewRounds)
	require.Equal(t, builder.ID, *plan.opts.Worker.ReviewsSessionID)
	kickoff := buildWorkerKickoffPrompt(parent.ID, plan.proposal)
	require.Contains(t, kickoff, "git --no-optional-locks diff main...HEAD")
	require.Contains(t, kickoff, "worker/abc-build")
	require.Contains(t, kickoff, "<verdict>APPROVE</verdict>")
	require.Contains(t, kickoff, "review round 1 of 3")

	reviewer := seedSwitchSession(t, sm, "codex", "gpt-6-astra", func(s *Session) {
		s.Options.SpawnedBySessionID = &parent.ID
		s.Options.WorkingDirectory = &wt
		s.Options.ReadOnly = true
		s.Options.Worker = &WorkerConfig{Task: "review", Mode: WorkerModeReview, ReviewsSessionID: &builder.ID}
	})
	review := func(text string) string {
		seedMessages(t, sm, reviewer, [2]string{"assistant", text})
		sm.reportWorkerTurnToParent(reviewer) // the report is in the inbox when this returns
		return sm.withInbox(parent, "next")
	}

	// A reply without a verdict is not a round.
	out := review("Still reading.")
	require.Contains(t, out, "gave no verdict")
	require.Zero(t, builder.Options.Worker.ReviewRounds)

	out = review("1) off-by-one in a.go:3\n<verdict>CHANGES_REQUESTED</verdict>")
	require.Contains(t, out, "CHANGES_REQUESTED (review round 1 of 3")
	require.Contains(t, out, "send the findings")
	require.Contains(t, out, builder.ID.String())
	require.Equal(t, 1, builder.Options.Worker.ReviewRounds)
	require.Equal(t, VerdictChangesRequested, builder.Options.Worker.LastVerdict)

	// The proposer can still send the builder back.
	_, err = sm.MessageLoopSessionForAgent(parent.ID, builder.ID, "fix 1")
	require.NoError(t, err)

	review("still wrong\n<verdict>CHANGES_REQUESTED</verdict>")
	out = review("and again\n<verdict>CHANGES_REQUESTED</verdict>")
	require.Contains(t, out, "review round 3 of 3")
	require.Contains(t, out, "last round")
	require.Contains(t, out, "do NOT send the build worker back")
	require.Equal(t, 3, builder.Options.Worker.ReviewRounds)

	// Closed: no more messages to the builder, no new review of it.
	_, err = sm.MessageLoopSessionForAgent(parent.ID, builder.ID, "one more try")
	require.ErrorIs(t, err, ErrReviewRoundsExhausted)
	_, err = sm.prepareWorkerSession(parent.ID, WorkerSessionRequest{Task: "review again", Mode: "review", ReviewsSessionID: builder.ID.String()})
	require.ErrorIs(t, err, ErrReviewRoundsExhausted)
	bid := builder.ID.String()
	code, body := controlRequest(t, sm, n0.ControlLoopMessagePath, parent.ID.String(), n0.ProxyToken(parent.ID.String()), `{"session_id":"`+bid+`","text":"again"}`, nil)
	require.Equal(t, 403, code, body)

	// The rounds are stored with the builder.
	meta, err := sm.Storage.GetSession(builder.ID)
	require.NoError(t, err)
	require.Contains(t, meta.OptionsJSON, `"review_rounds":3`)

	infos, err := sm.GetLoopSessionsForAgent(parent.ID, builder.ID)
	require.NoError(t, err)
	require.Equal(t, 3, infos[0].ReviewRounds)
	require.Equal(t, VerdictChangesRequested, infos[0].LastVerdict)
	require.Equal(t, "worker/abc-build", infos[0].Branch)
}

func TestReviewApprovalTellsTheProposerToMerge(t *testing.T) {
	h, parent, rec := newLoopSpawnHarness(t)
	sm := h.SessionManager
	wt := t.TempDir()
	builder := seedBuilder(t, sm, parent, wt)
	reviewer := seedSwitchSession(t, sm, "codex", "gpt-6-astra", func(s *Session) {
		s.Options.SpawnedBySessionID = &parent.ID
		s.Options.Worker = &WorkerConfig{Task: "review", Mode: WorkerModeReview, ReviewsSessionID: &builder.ID}
	})
	seedMessages(t, sm, reviewer, [2]string{"assistant", "No blocking findings.\n<verdict>APPROVE</verdict>"})

	sm.reportWorkerTurnToParent(reviewer)
	out := sm.withInbox(parent, rec.waitFor(t, parent.ID).prompt)
	require.Contains(t, out, "Verdict: APPROVE (review round 1 of 3")
	require.Contains(t, out, wt)
	require.Contains(t, out, "merge the branch into yours")
	require.Equal(t, VerdictApprove, builder.Options.Worker.LastVerdict)

	// An approved builder can still be given follow-up work.
	_, err := sm.MessageLoopSessionForAgent(parent.ID, builder.ID, "also update the changelog")
	require.NoError(t, err)

	// A build worker's own report says where its work is and how to review it.
	seedMessages(t, sm, builder, [2]string{"assistant", "Changelog updated."})
	sm.reportWorkerTurnToParent(builder)
	out = sm.withInbox(parent, "next")
	require.Contains(t, out, "in the worktree "+wt)
	require.Contains(t, out, "reviews_session_id "+builder.ID.String())
}

func TestReviewOnlyOfYourOwnBuildWorkers(t *testing.T) {
	h, parent, _ := newLoopSpawnHarness(t)
	sm := h.SessionManager
	other := seedSwitchSession(t, sm, "claude", "opus", nil)
	theirs := seedBuilder(t, sm, other, t.TempDir())
	mine := seedBuilder(t, sm, parent, t.TempDir())
	myReviewer := seedSwitchSession(t, sm, "claude", "opus", func(s *Session) {
		s.Options.SpawnedBySessionID = &parent.ID
		s.Options.Worker = &WorkerConfig{Task: "r", Mode: WorkerModeReview}
	})

	_, err := sm.prepareWorkerSession(parent.ID, WorkerSessionRequest{Task: "x", Mode: "review", ReviewsSessionID: theirs.ID.String()})
	require.ErrorIs(t, err, ErrLoopForeignSession)
	_, err = sm.prepareWorkerSession(parent.ID, WorkerSessionRequest{Task: "x", Mode: "review", ReviewsSessionID: uuid.NewString()})
	require.ErrorIs(t, err, ErrLoopForeignSession)
	_, err = sm.prepareWorkerSession(parent.ID, WorkerSessionRequest{Task: "x", Mode: "review", ReviewsSessionID: myReviewer.ID.String()})
	require.ErrorContains(t, err, "must name a build worker")
	_, err = sm.prepareWorkerSession(parent.ID, WorkerSessionRequest{Task: "x", Mode: "build", ReviewsSessionID: mine.ID.String()})
	require.ErrorContains(t, err, "is for mode")
	_, err = sm.prepareWorkerSession(parent.ID, WorkerSessionRequest{Task: "x", Mode: "review", ReviewsSessionID: "nope"})
	require.ErrorContains(t, err, "not a session id")
}

// --- Teams ---

func teamOf(builderProvider, builderModel, reviewerProvider, reviewerModel string) *TeamConfig {
	return &TeamConfig{Builder: TeamMember{builderProvider, builderModel}, Reviewer: TeamMember{reviewerProvider, reviewerModel}}
}

// A team session starts its workers without a card, on the team's models
// whatever the agent asks for, and still sandboxed.
func TestTeamStartsWorkersWithoutACardOnItsModels(t *testing.T) {
	h, parent, rec := newLoopSpawnHarness(t)
	sm := h.SessionManager
	team, err := sm.normalizeTeam(teamOf("claude", "haiku", "codex", "gpt-6-astra"))
	require.NoError(t, err)
	require.Equal(t, DefaultMaxReviewRounds, team.MaxReviewRounds)
	parent.Options.Team = team

	res, err := sm.StartWorkerSessionForAgent(context.Background(), parent.ID, WorkerSessionRequest{Task: "build it", Provider: "claude", Model: "opus"})
	require.NoError(t, err)
	require.Empty(t, parent.pendingQuestionMessages(), "the team setup was the approval")
	require.Equal(t, "haiku", res.Model, "the agent does not pick the model")

	builder := childrenOf(sm, parent.ID)[0]
	require.True(t, *builder.Options.Sandboxed)
	require.Nil(t, builder.Options.Team, "a worker is never a team itself")
	rec.waitFor(t, builder.ID)

	res, err = sm.StartWorkerSessionForAgent(context.Background(), parent.ID, WorkerSessionRequest{Task: "review it", Mode: "review", ReviewsSessionID: builder.ID.String()})
	require.NoError(t, err)
	require.Equal(t, "gpt-6-astra", res.Model)
	require.Equal(t, "codex", res.Provider)
	var reviewer *AgentSession
	for _, c := range childrenOf(sm, parent.ID) {
		if c.ID.String() == res.SessionID {
			reviewer = c
		}
	}
	require.NotNil(t, reviewer)
	require.True(t, reviewer.Options.ReadOnly)

	// The user sees what was started.
	msgs, _, err := sm.GetMessages(parent.ID, 50, 0)
	require.NoError(t, err)
	var notes []string
	for _, m := range msgs {
		notes = append(notes, m.Content)
	}
	require.Contains(t, strings.Join(notes, "\n"), "your team setup")
}

func TestTeamBriefBudgetAndRules(t *testing.T) {
	h, parent, _ := newLoopSpawnHarness(t)
	sm := h.SessionManager

	// Members need a provider and a model that can be sandboxed.
	_, err := sm.normalizeTeam(teamOf("claude", "", "claude", "opus"))
	require.ErrorContains(t, err, "builder needs")
	five := &TeamConfig{Builder: TeamMember{"claude", "haiku"}, Reviewer: TeamMember{"claude", "opus"}, MaxReviewRounds: 99, BriefSent: true}
	team, err := sm.normalizeTeam(five)
	require.NoError(t, err)
	require.Equal(t, teamMaxReviewRounds, team.MaxReviewRounds)
	require.False(t, team.BriefSent, "a client cannot mark the brief as sent")

	// Setting up a team needs a signed-in user; a worker's config is never a client's.
	require.Equal(t, TeamSetupAction, PrivilegedSessionOptions(&SessionOptions{Team: team}))

	// The brief goes in front of the first ordinary prompt, once.
	parent.Options.Team = team
	require.Equal(t, "/clear", sm.withInbox(parent, "/clear"), "a slash command goes to the CLI as typed")
	first := sm.withInbox(parent, "Build the tag filter")
	require.Contains(t, first, "<eggify_team>")
	require.Contains(t, first, "haiku")
	require.Contains(t, first, "At most 5 review rounds")
	require.True(t, strings.HasSuffix(first, "Build the tag filter"))
	require.Equal(t, "and then?", sm.withInbox(parent, "and then?"))
	meta, err := sm.Storage.GetSession(parent.ID)
	require.NoError(t, err)
	require.Contains(t, meta.OptionsJSON, `"brief_sent":true`)

	// The team's review cap applies to its build workers.
	plan, err := sm.prepareWorkerSession(parent.ID, WorkerSessionRequest{Task: "x"})
	require.NoError(t, err)
	require.True(t, plan.team)
	require.Equal(t, teamMaxReviewRounds, plan.opts.Worker.MaxReviewRounds)

	// A team may start a limited number of workers without asking.
	for i := 0; i < teamMaxWorkers; i++ {
		seedSwitchSession(t, sm, "claude", "haiku", func(s *Session) {
			s.Options.SpawnedBySessionID = &parent.ID
			s.Options.Worker = &WorkerConfig{Task: "t", Mode: WorkerModeBuild}
		})
	}
	_, err = sm.prepareWorkerSession(parent.ID, WorkerSessionRequest{Task: "x"})
	require.ErrorContains(t, err, "the most a team may start")
}

// A new session keeps a team only when it is not itself a worker, and a
// handover never carries one.
func TestTeamIsNotInherited(t *testing.T) {
	h, parent, _ := newLoopSpawnHarness(t)
	sm := h.SessionManager
	dir := t.TempDir()
	id := uuid.New()
	created, err := sm.CreateSession(id, SessionOptions{WorkingDirectory: &dir, SpawnedBySessionID: &parent.ID, Team: teamOf("claude", "haiku", "claude", "opus")})
	require.NoError(t, err)
	require.Nil(t, created.Options.Team)

	_, err = sm.CreateSession(uuid.New(), SessionOptions{WorkingDirectory: &dir, Team: teamOf("claude", "", "claude", "opus")})
	require.ErrorContains(t, err, "builder needs")
}

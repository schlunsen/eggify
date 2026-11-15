package agents

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
)

// Teams.
//
// A user can set a session up as a team in Create Session: this session
// orchestrates, and the workers it starts (worker_spawn.go) run on the models
// the user picked for building and reviewing. That choice is the approval:
// a team session starts its workers without a card, always on those models
// and always sandboxed. Only a signed-in user can create one
// (PrivilegedSessionOptions), it does not travel with a handover, and the
// number of workers a team may start is capped.
//
// The orchestrator learns the workflow from a brief put in front of its first
// ordinary prompt (withTeamBrief).
//
// A team never works in the checkout it was started in: in a git repository
// the orchestrator gets its own worktree and branch at creation
// (createTeamWorktree), and its build workers branch off that in worktrees of
// their own (planWorkerWorktree). The team's worktree is kept when the
// session ends (no auto-cleanup), since its branch is what the user merges.

const (
	teamMaxWorkers      = 12 // workers one team session may start in total
	teamMaxReviewRounds = 5
	teamBriefTag        = "eggify_team"

	// TeamSetupAction names the privileged action in user_required refusals.
	TeamSetupAction = "Setting up a team (its workers start without asking)"
)

// TeamMember is the model one of a team's roles runs on.
type TeamMember struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// TeamConfig is SessionOptions.Team.
type TeamConfig struct {
	Builder  TeamMember `json:"builder"`
	Reviewer TeamMember `json:"reviewer"`
	// MaxReviewRounds caps the build–review rounds (default 3, at most 5).
	MaxReviewRounds int `json:"max_review_rounds,omitempty"`
	// BriefSent is set once the orchestrator has been given the workflow.
	BriefSent bool `json:"brief_sent,omitempty"`
	// Worktree is the team's own git worktree, set by the server at creation
	// (never taken from a client); nil outside a git repository.
	Worktree *TeamWorktree `json:"worktree,omitempty"`
}

// TeamWorktree is where a team orchestrator works.
type TeamWorktree struct {
	Path   string `json:"path"`   // the worktree's root
	Branch string `json:"branch"` // the team's branch
	Base   string `json:"base"`   // the branch (or commit) it started from
}

// teamBranchName is "team/<short id>", or the branch the user named.
func teamBranchName(sessionID uuid.UUID, asked *string) (string, error) {
	if asked != nil {
		if name := strings.TrimSpace(*asked); name != "" {
			if err := ValidateGitRev(name); err != nil {
				return "", fmt.Errorf("the team's branch name: %w", err)
			}
			return name, nil
		}
	}
	return "team/" + shortID(sessionID), nil
}

// teamWorktreeResult is what createTeamWorktree made.
type teamWorktreeResult struct {
	id   string // the worktrees row
	path string // the worktree's root
}

// createTeamWorktree gives a new team session its own git worktree and
// branch from the HEAD of its working directory, and moves the session into
// it (same subfolder). nil, nil outside a git repository with a commit (there
// is nothing to branch from; the team works in the folder). A worktree git
// refuses to create fails the session's creation: a team never falls back to
// the user's checkout.
func (sm *SessionManager) createTeamWorktree(sessionID uuid.UUID, options *SessionOptions, projectID *string) (*teamWorktreeResult, error) {
	dir := strings.TrimSpace(codexStr(options.WorkingDirectory))
	if options.Team == nil || dir == "" {
		return nil, nil
	}
	branch, err := teamBranchName(sessionID, options.WorktreeBranch)
	if err != nil {
		return nil, err
	}
	plan, err := planWorktreeAt(dir, sessionID, branch)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		logging.Info("👥 Team session %s: %s is not a git repository with a commit, so it works in the folder", sessionID, dir)
		return nil, nil
	}
	if err := CreateWorktree(dir, plan.Root, WorktreeCreateOptions{BranchName: branch, NewBranch: true}); err != nil {
		return nil, fmt.Errorf("could not create the team's git worktree: %w", err)
	}
	logging.Info("👥 Team session %s: worktree %s on branch %s (from %s)", sessionID, plan.Root, branch, plan.Base)

	res := &teamWorktreeResult{id: uuid.New().String(), path: plan.Root}
	if db := database.GetInstance(); db != nil {
		sid := sessionID.String()
		row := &database.Worktree{
			ID:            res.id,
			SessionID:     &sid,
			WorktreePath:  plan.Root,
			BranchName:    branch,
			IsAutoCreated: true,
			AutoCleanup:   false, // the team's branch outlives the session
		}
		if projectID != nil {
			row.ProjectID = *projectID
		}
		if plan.Base != "" {
			base := plan.Base
			row.SourceBranch = &base
		}
		if err := database.NewRepository(db).CreateWorktree(row); err != nil {
			logging.Warning("Team session %s: could not save its worktree row: %v", sessionID, err)
		}
	}

	team := *options.Team
	team.Worktree = &TeamWorktree{Path: plan.Root, Branch: branch, Base: plan.Base}
	options.Team = &team
	wd := plan.Dir
	options.WorkingDirectory = &wd
	options.UseWorktree = nil // done here, not by the generic session worktree
	return res, nil
}

func (t *TeamConfig) maxReviewRounds() int {
	if t == nil || t.MaxReviewRounds <= 0 {
		return DefaultMaxReviewRounds
	}
	if t.MaxReviewRounds > teamMaxReviewRounds {
		return teamMaxReviewRounds
	}
	return t.MaxReviewRounds
}

// normalizeTeam validates a team given at session creation and fills its
// defaults. Each member needs a provider and a model that can be sandboxed
// (the Claude CLI or Codex).
func (sm *SessionManager) normalizeTeam(t *TeamConfig) (*TeamConfig, error) {
	if t == nil {
		return nil, nil
	}
	out := *t
	out.BriefSent = false
	out.Worktree = nil // set by createTeamWorktree, never by a client
	for _, m := range []struct {
		role   string
		member *TeamMember
	}{{"builder", &out.Builder}, {"reviewer", &out.Reviewer}} {
		m.member.Provider = strings.TrimSpace(m.member.Provider)
		m.member.Model = strings.TrimSpace(m.member.Model)
		if m.member.Provider == "" || m.member.Model == "" {
			return nil, fmt.Errorf("the team's %s needs a provider and a model", m.role)
		}
		p, model := m.member.Provider, m.member.Model
		probe := &AgentSession{Session: Session{Provider: p, ModelName: model, Options: SessionOptions{Provider: &p, Model: &model}}}
		if sm.resolveExecutionPath(probe) == ExecutionPathDirect {
			return nil, fmt.Errorf("the team's %s (%s/%s) cannot be sandboxed: pick a Claude CLI model, an Anthropic-compatible provider or Codex", m.role, p, model)
		}
	}
	out.MaxReviewRounds = out.maxReviewRounds()
	return &out, nil
}

// checkTeamWorkerBudget refuses once a team session has started
// teamMaxWorkers workers (running or not).
func (sm *SessionManager) checkTeamWorkerBudget(caller uuid.UUID) error {
	sm.mu.RLock()
	n := 0
	for _, s := range sm.sessions {
		if s.Options.Worker != nil && s.Options.SpawnedBySessionID != nil && *s.Options.SpawnedBySessionID == caller {
			n++
		}
	}
	sm.mu.RUnlock()
	if n >= teamMaxWorkers {
		return fmt.Errorf("this team has started %d workers, the most a team may start without asking: tell the user what is left and let them decide", teamMaxWorkers)
	}
	return nil
}

// withTeamBrief puts the team's workflow in front of the orchestrator's first
// ordinary prompt.
func (sm *SessionManager) withTeamBrief(session *AgentSession, prompt string) string {
	sm.mu.Lock()
	team := session.Options.Team
	if team == nil || team.BriefSent || session.Options.SpawnedBySessionID != nil {
		sm.mu.Unlock()
		return prompt
	}
	t := *team
	t.BriefSent = true
	session.Options.Team = &t
	sm.mu.Unlock()
	sm.persistSessionState(session)
	logging.Info("👥 Team session %s: workflow brief sent (builder %s, reviewer %s)", session.ID, t.Builder.Model, t.Reviewer.Model)
	return teamBrief(&t) + "\n\n" + prompt
}

// teamBrief is the workflow text for the orchestrator.
func teamBrief(t *TeamConfig) string {
	var b strings.Builder
	b.WriteString("<" + teamBriefTag + ">\n")
	b.WriteString("The user set this session up as a team in Eggify. You are the orchestrator: you plan, hand the building to a build worker and the checking to a review worker, and report to the user.\n")
	fmt.Fprintf(&b, "- Build worker: %s (%s), sandboxed, in its own git worktree and branch.\n", t.Builder.Model, t.Builder.Provider)
	fmt.Fprintf(&b, "- Review worker: %s (%s), sandboxed and read-only.\n", t.Reviewer.Model, t.Reviewer.Provider)
	b.WriteString("The user approved these workers when they set up the team, so start_worker_session starts them without a card, always on those models (do not pass provider or model).\n")
	if wt := t.Worktree; wt != nil {
		fmt.Fprintf(&b, "- You: in the team's own git worktree %s on branch %s", wt.Path, wt.Branch)
		if wt.Base != "" {
			fmt.Fprintf(&b, ", started from %s", wt.Base)
		}
		b.WriteString(". Commit your work on this branch. Do not switch branches, and do not touch the main checkout or other worktrees: the user works there. When you are done, tell the user the branch to merge.\n")
	}
	b.WriteString("\n")
	b.WriteString("Workflow for work that is worth it (for a small change or a question, just do it yourself):\n")
	b.WriteString("1. Plan. Commit anything the builder needs first: its worktree starts from your last commit.\n")
	b.WriteString("2. start_worker_session with mode \"build\" and a self-contained task (the plan, files, constraints, how to check the result). End your turn; its report arrives at the top of a later prompt.\n")
	b.WriteString("3. start_worker_session with mode \"review\", reviews_session_id set to the build worker's session_id, and what to look for. End your turn; its report ends with a verdict.\n")
	fmt.Fprintf(&b, "4. On CHANGES_REQUESTED, send the findings you agree with to the build worker (message_loop_session), and when it reports back ask the reviewer to look again (message_loop_session). At most %d review rounds; after the last one, stop and tell the user what is open.\n", t.maxReviewRounds())
	b.WriteString("5. On APPROVE, commit anything the builder left uncommitted in its worktree, merge its branch into yours, run the project's checks yourself, and tell the user what was built, what you verified and which branch holds it.\n")
	b.WriteString("Never poll: after starting or messaging a worker, end your turn.\n")
	b.WriteString("</" + teamBriefTag + ">")
	return b.String()
}

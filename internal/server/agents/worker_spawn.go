package agents

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/gitexec"
	"github.com/schlunsen/eggify/internal/logging"
)

// Worker sessions started by agents.
//
// An agent can hand a task to a new session on another model or runtime: a
// Claude CLI model (Anthropic-compatible providers such as DeepSeek included)
// or Codex. Like a loop session (loop_spawn.go) the user approves it on a
// card in the proposing session, only a signed-in user can answer, and
// nothing is created before that. Unlike a loop session a worker
//
//   - is always sandboxed: "build" may change its working directory, "review"
//     is read-only (SessionOptions.ReadOnly). Permission prompts are off
//     inside the sandbox, so it never waits on a card nobody is watching;
//   - has no verify command: it works on the task and, at the end of every
//     turn, its reply is reported to the session that started it
//     (reportWorkerTurnToParent), which can answer with message_loop_session.
//
// Build and review form a loop the proposing session drives:
//
//   - a build worker in a git repository gets its own worktree and branch
//     (planWorkerWorktree), started from the proposer's last commit, and
//     commits its work there (the Claude CLI's sandbox lets a worktree write
//     its own git data, checked live; where a sandbox refuses, the changes
//     stay uncommitted). The proposer merges once the work is approved;
//   - a review worker names the build worker it reviews (reviews_session_id),
//     works read-only in that worktree, reads everything the branch changed
//     against its base, committed or not, and ends its reply with a verdict
//     (<verdict>APPROVE</verdict> or <verdict>CHANGES_REQUESTED</verdict>);
//   - each verdict is a review round of that build worker. After the last
//     round (3, or the team's setting) with changes still requested, the
//     proposer can no longer send the builder back and is told to report to
//     the user.
//
// A session the user set up as a team (SessionOptions.Team, team.go) starts
// workers on the team's models without a card.
//
// The sessions talk through the linked-session inbox (session_inbox.go),
// with the same rules as loop sessions: a worker cannot start sessions, and
// only the session that started it can reach it.

// Worker modes.
const (
	WorkerModeBuild  = "build"
	WorkerModeReview = "review"
)

// Review verdicts (WorkerConfig.LastVerdict).
const (
	VerdictApprove          = "approve"
	VerdictChangesRequested = "changes_requested"
)

// Card answers and limits.
const (
	WorkerSessionAnswerStart = "Start"
	WorkerSessionAnswerDeny  = "Deny"

	workerSessionCardKind   = "worker_session"
	workerSessionCardHeader = "Worker session"

	workerMaxTaskChars     = 12000
	workerMaxContextChars  = 8000
	workerMaxReports       = 30 // turn-end reports per worker
	workerReportMaxChars   = 6000
	DefaultMaxReviewRounds = 3
	workerBranchPrefix     = "worker/"
	workerBranchSlugMaxLen = 32
)

// Errors returned to the calling agent.
var (
	// ErrWorkerNested: a session started by another agent tried to start one.
	ErrWorkerNested = errors.New("a session started by another agent session cannot start sessions itself")
	// ErrReviewRoundsExhausted: the build worker has had its last review round.
	ErrReviewRoundsExhausted = errors.New("the review round limit for that build worker is reached: do not send it back again. Tell the user what is still open and let them decide")
)

// WorkerConfig is what a worker session was started for (SessionOptions.Worker).
type WorkerConfig struct {
	Task string `json:"task"`
	Mode string `json:"mode"` // build | review

	// Build workers in a git repository: their own worktree and branch.
	WorktreePath string `json:"worktree_path,omitempty"` // the worktree's root
	Branch       string `json:"branch,omitempty"`
	BaseBranch   string `json:"base_branch,omitempty"` // what the branch started from

	// Review workers: the build worker under review.
	ReviewsSessionID *uuid.UUID `json:"reviews_session_id,omitempty"`

	// Build workers: reviews so far, the latest verdict and the cap.
	ReviewRounds    int    `json:"review_rounds,omitempty"`
	MaxReviewRounds int    `json:"max_review_rounds,omitempty"`
	LastVerdict     string `json:"last_verdict,omitempty"`
}

// WorkerSessionRequest is start_worker_session from eggify-tools.
type WorkerSessionRequest struct {
	Task             string `json:"task"`
	Mode             string `json:"mode,omitempty"`
	WorkingDirectory string `json:"working_directory,omitempty"`
	Model            string `json:"model,omitempty"`
	Provider         string `json:"provider,omitempty"`
	Context          string `json:"context,omitempty"` // extra notes for the worker's first turn
	// Worktree (build mode): a build worker in a git repository always gets
	// its own git worktree and branch; true only adds that it must (refused
	// outside a repository with a commit), false is ignored.
	Worktree *bool `json:"worktree,omitempty"`
	// ReviewsSessionID (review mode): the build worker to review. The review
	// runs in that worker's folder and counts as one of its review rounds.
	ReviewsSessionID string `json:"reviews_session_id,omitempty"`
}

// WorkerSessionProposal is what the approval card shows.
type WorkerSessionProposal struct {
	Task             string `json:"task"`
	Mode             string `json:"mode"`
	WorkingDirectory string `json:"working_directory"`
	Model            string `json:"model"`
	Provider         string `json:"provider"`
	Runtime          string `json:"runtime"` // claude-sdk | codex
	Context          string `json:"context,omitempty"`
	Sandboxed        bool   `json:"sandboxed"`
	ReadOnly         bool   `json:"read_only"`
	ParentSessionID  string `json:"parent_session_id"`
	ProjectID        string `json:"project_id,omitempty"`

	// Build mode with a worktree.
	WorktreePath string `json:"worktree_path,omitempty"`
	Branch       string `json:"branch,omitempty"`
	BaseBranch   string `json:"base_branch,omitempty"`

	// Review mode linked to a build worker.
	ReviewsSessionID string `json:"reviews_session_id,omitempty"`
	ReviewRound      int    `json:"review_round,omitempty"` // the round this review would be
	MaxReviewRounds  int    `json:"max_review_rounds,omitempty"`

	// Team: started without a card, on the team's model.
	Team bool `json:"team,omitempty"`
}

// WorkerSessionResult is the reply to start_worker_session.
type WorkerSessionResult struct {
	Status           string `json:"status"` // started
	SessionID        string `json:"session_id"`
	Mode             string `json:"mode"`
	WorkingDirectory string `json:"working_directory"`
	Model            string `json:"model"`
	Provider         string `json:"provider"`
	Branch           string `json:"branch,omitempty"`
	BaseBranch       string `json:"base_branch,omitempty"`
	Note             string `json:"note"`
}

// workerPlan is everything prepareWorkerSession decided.
type workerPlan struct {
	childID  uuid.UUID
	proposal WorkerSessionProposal
	opts     SessionOptions
	worktree *workerWorktree // nil: the worker shares the folder
	repoDir  string          // where `git worktree add` runs
	team     bool
}

// StartWorkerSessionForAgent validates an agent's proposal, asks the user
// (unless the session is a team the user set up), and then creates the
// worker session and sends it the task. It blocks until the user answers.
func (sm *SessionManager) StartWorkerSessionForAgent(ctx context.Context, caller uuid.UUID, req WorkerSessionRequest) (*WorkerSessionResult, error) {
	plan, err := sm.prepareWorkerSession(caller, req)
	if err != nil {
		return nil, err
	}
	proposal := plan.proposal

	if !plan.team {
		answer, detail, ok := sm.askWorkerSessionApproval(ctx, caller, proposal)
		if !ok {
			logging.Info("🛑 Worker session proposed by %s not approved (%s)", caller, detail)
			msg := "The user did not approve the worker session, so nothing was started. Do not propose it again unless the user asks."
			if detail != "" {
				msg += " (" + detail + ")"
			}
			return nil, errors.New(msg)
		}
		if answer != WorkerSessionAnswerStart {
			logging.Info("🛑 Worker session proposed by %s denied", caller)
			return nil, errors.New("The user denied the worker session, so nothing was started. Do not propose it again unless the user asks; tell the user what you wanted to do instead.")
		}
		// Things may have changed while the card waited.
		if err := sm.checkWorkerSpawnAllowed(caller); err != nil {
			return nil, err
		}
	}

	if wt := plan.worktree; wt != nil {
		if err := CreateWorktree(plan.repoDir, wt.Root, WorktreeCreateOptions{BranchName: wt.Branch, NewBranch: true}); err != nil {
			return nil, fmt.Errorf("could not create the worker's git worktree: %w", err)
		}
		logging.Info("🧑‍🔧 Worker %s: worktree %s on branch %s (from %s)", plan.childID, wt.Root, wt.Branch, wt.Base)
	}

	childID := plan.childID
	created, err := sm.CreateSession(childID, plan.opts)
	if err != nil {
		if wt := plan.worktree; wt != nil {
			// Nothing was written there yet.
			if rmErr := RemoveWorktree(plan.repoDir, wt.Root, true); rmErr != nil {
				logging.Warning("Worker %s: could not remove its unused worktree %s: %v", childID, wt.Root, rmErr)
			}
		}
		return nil, fmt.Errorf("could not create the worker session: %w", err)
	}
	sm.mu.RLock()
	var owner *string
	if parent, ok := sm.sessions[caller]; ok {
		owner = parent.OwnerUserID
	}
	sm.mu.RUnlock()
	if owner != nil {
		if err := sm.SetSessionOwner(childID, owner); err != nil {
			logging.Warning("Worker session %s: could not set owner: %v", childID, err)
		}
	}
	approved := "approved by you"
	if plan.team {
		approved = "your team setup"
	}
	logging.Info("🧑‍🔧 Worker session %s (%s) created for %s (%s): dir=%s model=%s", childID, proposal.Mode, caller, approved, proposal.WorkingDirectory, created.ModelName)

	if parent, err := sm.GetSession(caller); err == nil {
		where := ""
		if proposal.Branch != "" {
			where = " on branch " + proposal.Branch
		}
		sm.emitSessionNotice(parent, fmt.Sprintf("🧑‍🔧 Started %s worker %s on %s%s (%s): %s",
			proposal.Mode, shortID(childID), proposal.Model, where, approved, truncateRunes(oneLine(proposal.Task), 200)))
	}
	if child, err := sm.GetSession(childID); err == nil {
		how := "approved by the user"
		if plan.team {
			how = "part of the team the user set up"
		}
		sm.emitSessionNotice(child, fmt.Sprintf("🧑‍🔧 Worker session started by agent session %s, %s. %s", shortID(caller), how, workerModeNotice(proposal.Mode)))
		sm.broadcastSessionUpdate(child)
	}

	kickoff := buildWorkerKickoffPrompt(caller, proposal)
	go func() {
		if err := sm.loopKickoffSend(childID, kickoff); err != nil {
			logging.Error("🧑‍🔧 Worker session %s: first prompt failed: %v", childID, err)
		}
	}()

	note := "The worker is running. End your turn: its reply arrives in your inbox (at the top of a later prompt) each time it finishes a turn, also if you are busy then. " +
		"Use message_loop_session to answer it or give it more work, and get_loop_session to check on it. Do not poll."
	if proposal.Branch != "" {
		note += fmt.Sprintf(" It works in its own git worktree %s on branch %s (from %s) and commits there; nothing reaches your branch until you merge it. "+
			"To have its work reviewed, start a review worker with reviews_session_id set to this session_id.", proposal.WorktreePath, proposal.Branch, proposal.BaseBranch)
	}
	return &WorkerSessionResult{
		Status:           "started",
		SessionID:        childID.String(),
		Mode:             proposal.Mode,
		WorkingDirectory: proposal.WorkingDirectory,
		Model:            created.ModelName,
		Provider:         proposal.Provider,
		Branch:           proposal.Branch,
		BaseBranch:       proposal.BaseBranch,
		Note:             note,
	}, nil
}

func workerModeNotice(mode string) string {
	if mode == WorkerModeReview {
		return "It is sandboxed and read-only: it cannot change files in its folder."
	}
	return "It is sandboxed, and permission prompts are off inside the sandbox."
}

// prepareWorkerSession validates the request and builds the card and the new
// session's options. Nothing is created.
func (sm *SessionManager) prepareWorkerSession(caller uuid.UUID, req WorkerSessionRequest) (*workerPlan, error) {
	if err := sm.checkWorkerSpawnAllowed(caller); err != nil {
		return nil, err
	}

	task := strings.TrimSpace(req.Task)
	if task == "" {
		return nil, errors.New("task is required")
	}
	if len([]rune(task)) > workerMaxTaskChars {
		return nil, fmt.Errorf("task is too long (max %d characters)", workerMaxTaskChars)
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	switch mode {
	case "":
		mode = WorkerModeBuild
	case WorkerModeBuild, WorkerModeReview:
	default:
		return nil, fmt.Errorf("mode must be %q or %q", WorkerModeBuild, WorkerModeReview)
	}
	extra := truncateRunes(strings.TrimSpace(req.Context), workerMaxContextChars)

	sm.mu.RLock()
	parent, ok := sm.sessions[caller]
	var parentDir, parentProvider, parentModel string
	var parentProject *string
	var parentConnectors []string
	var parentN0 *int64
	var parentPath ExecutionPath
	var team *TeamConfig
	if ok {
		if parent.Options.WorkingDirectory != nil {
			parentDir = *parent.Options.WorkingDirectory
		}
		parentProvider = effectiveProvider(parent)
		parentModel = sm.effectiveModel(parent)
		parentProject = parent.ProjectID
		// Copy, keeping nil (all connectors) apart from an explicit [] (none).
		if parent.Options.Connectors != nil {
			parentConnectors = append([]string{}, parent.Options.Connectors...)
		}
		if parent.Options.N0ConnectionID != nil {
			id := *parent.Options.N0ConnectionID
			parentN0 = &id
		}
		parentPath = sm.resolveExecutionPath(parent)
		if parent.Options.Team != nil {
			t := *parent.Options.Team
			team = &t
		}
	}
	sm.mu.RUnlock()
	if !ok {
		return nil, errors.New("your session is not active")
	}
	if team != nil {
		if err := sm.checkTeamWorkerBudget(caller); err != nil {
			return nil, err
		}
	}
	maxRounds := DefaultMaxReviewRounds
	if team != nil {
		maxRounds = team.maxReviewRounds()
	}

	// A review of a build worker runs in that worker's folder.
	var reviews *uuid.UUID
	reviewRound := 0
	requestedDir := req.WorkingDirectory
	if id := strings.TrimSpace(req.ReviewsSessionID); id != "" {
		if mode != WorkerModeReview {
			return nil, errors.New("reviews_session_id is for mode \"review\"")
		}
		target, err := uuid.Parse(id)
		if err != nil {
			return nil, errors.New("reviews_session_id is not a session id")
		}
		sm.mu.RLock()
		builder, live := sm.sessions[target]
		var builderDir string
		var builderCfg *WorkerConfig
		owned := live && builder.Options.SpawnedBySessionID != nil && *builder.Options.SpawnedBySessionID == caller
		if owned {
			builderCfg = builder.Options.Worker
			if builder.Options.WorkingDirectory != nil {
				builderDir = *builder.Options.WorkingDirectory
			}
		}
		sm.mu.RUnlock()
		if !owned {
			logging.Warning("🛑 Session %s asked for a review of session %s, which it did not start", caller, target)
			return nil, ErrLoopForeignSession
		}
		if builderCfg == nil || builderCfg.Mode != WorkerModeBuild {
			return nil, errors.New("reviews_session_id must name a build worker you started")
		}
		if builderCfg.MaxReviewRounds > 0 {
			maxRounds = builderCfg.MaxReviewRounds
		}
		if builderCfg.ReviewRounds >= maxRounds {
			return nil, ErrReviewRoundsExhausted
		}
		reviews = &target
		reviewRound = builderCfg.ReviewRounds + 1
		if strings.TrimSpace(requestedDir) == "" {
			requestedDir = builderDir
		}
	}

	dir, err := resolveLoopDir(requestedDir, parentDir)
	if err != nil {
		return nil, err
	}

	provider, model := strings.TrimSpace(req.Provider), strings.TrimSpace(req.Model)
	if team != nil {
		// The user picked the team's models; the agent does not choose.
		member := team.Builder
		if mode == WorkerModeReview {
			member = team.Reviewer
		}
		provider, model = member.Provider, member.Model
	}
	if provider == "" && model == "" && parentPath != ExecutionPathDirect {
		provider, model = parentProvider, parentModel
	}
	if provider == "" && model != "" && isClaudeModel(model) {
		provider = "claude"
	}
	// Apply the server defaults here, as CreateSession would, so the runtime
	// checked below and shown on the card is the one that is launched.
	if sm.config != nil {
		if provider == "" {
			provider = sm.config.DefaultProvider
		}
		if model == "" && !isCodexProvider(provider, "") {
			model = sm.config.DefaultModel
		}
	}

	childID := uuid.New()

	// A build worker in a git repository always gets its own worktree and
	// branch, so it never writes to the checkout its proposer (or the user)
	// works in. `worktree: false` used to share the folder; it is ignored.
	var wt *workerWorktree
	repoDir := dir
	if mode == WorkerModeBuild {
		wt, err = planWorkerWorktree(dir, childID, task)
		if err != nil {
			return nil, err
		}
		if wt == nil && req.Worktree != nil && *req.Worktree {
			return nil, fmt.Errorf("worktree was asked for, but %s is not in a git repository with a commit", dir)
		}
		if wt != nil {
			dir = wt.Dir
		}
	}

	yes, no := true, false
	systemPrompt := "You are a helpful AI assistant."
	cfg := &WorkerConfig{Task: task, Mode: mode, ReviewsSessionID: reviews}
	if mode == WorkerModeBuild {
		cfg.MaxReviewRounds = maxRounds
	}
	if wt != nil {
		cfg.WorktreePath, cfg.Branch, cfg.BaseBranch = wt.Root, wt.Branch, wt.Base
	}
	opts := SessionOptions{
		WorkingDirectory:   &dir,
		SystemPrompt:       &systemPrompt,
		Sandboxed:          &yes,
		ReadOnly:           mode == WorkerModeReview,
		Worker:             cfg,
		ParentSessionID:    &caller,
		SpawnedBySessionID: &caller,
		Connectors:         parentConnectors,
		N0ConnectionID:     parentN0,
	}
	if provider != "" {
		opts.Provider = &provider
	}
	if model != "" {
		opts.Model = &model
	}
	if parentProject != nil && *parentProject != "" && (repoDir == parentDir || dir == parentDir) {
		opts.ProjectID = parentProject
	}

	// A worker needs a sandbox: the Claude CLI's or Codex's. Eggify's own
	// loop for OpenAI-compatible models has none.
	probe := &AgentSession{Session: Session{Options: opts, Provider: provider, ModelName: model}}
	path := sm.resolveExecutionPath(probe)
	switch path {
	case ExecutionPathClaudeSDK:
		// Prompts off inside the sandbox; the deny rules and the sandbox hold.
		opts.DangerouslySkipPermissions = &yes
		opts.AllowDangerouslySkipPermissions = &yes
	case ExecutionPathCodex:
		// Codex never asks (approval policy "never"); the sandboxed profile
		// decides what it may do. Explicitly not YOLO, whatever the default is.
		opts.DangerouslySkipPermissions = &no
		if model == "" {
			return nil, errors.New("model is required for a Codex worker (for example gpt-6-astra)")
		}
	default:
		return nil, fmt.Errorf("worker sessions run on the Claude CLI or Codex, which can be sandboxed; %q runs on neither. Leave provider and model empty to use your own", strings.Trim(provider+"/"+model, "/"))
	}
	// Pin both, so nothing filled in later can change what was approved.
	if model == "" {
		model = sm.effectiveModel(probe)
	}
	if provider == "" {
		if path == ExecutionPathCodex {
			provider = CodexProviderID
		} else {
			provider = "claude"
		}
	}
	opts.Provider = &provider
	if model != "" {
		opts.Model = &model
	}

	p := WorkerSessionProposal{
		Task:             task,
		Mode:             mode,
		WorkingDirectory: dir,
		Model:            model,
		Provider:         provider,
		Runtime:          string(path),
		Context:          extra,
		Sandboxed:        true,
		ReadOnly:         opts.ReadOnly,
		ParentSessionID:  caller.String(),
		Team:             team != nil,
	}
	if wt != nil {
		p.WorktreePath, p.Branch, p.BaseBranch = wt.Root, wt.Branch, wt.Base
	}
	if reviews != nil {
		p.ReviewsSessionID, p.ReviewRound, p.MaxReviewRounds = reviews.String(), reviewRound, maxRounds
		// What the reviewer is told about the folder it reads.
		sm.mu.RLock()
		if b, ok := sm.sessions[*reviews]; ok && b.Options.Worker != nil {
			p.WorktreePath, p.Branch, p.BaseBranch = b.Options.Worker.WorktreePath, b.Options.Worker.Branch, b.Options.Worker.BaseBranch
		}
		sm.mu.RUnlock()
	}
	if opts.ProjectID != nil {
		p.ProjectID = *opts.ProjectID
	}
	return &workerPlan{childID: childID, proposal: p, opts: opts, worktree: wt, repoDir: repoDir, team: team != nil}, nil
}

// workerWorktree is the git worktree planned for a build worker.
type workerWorktree struct {
	Root   string // the worktree's root folder
	Dir    string // the worker's working directory inside it
	Branch string
	Base   string // the branch (or commit) it starts from
}

var workerSlugRE = regexp.MustCompile(`[^a-z0-9]+`)

// workerBranchName is "worker/<short id>-<first words of the task>".
func workerBranchName(childID uuid.UUID, task string) string {
	slug := strings.Trim(workerSlugRE.ReplaceAllString(strings.ToLower(oneLine(task)), "-"), "-")
	if len(slug) > workerBranchSlugMaxLen {
		slug = strings.Trim(slug[:workerBranchSlugMaxLen], "-")
	}
	name := workerBranchPrefix + shortID(childID)
	if slug != "" {
		name += "-" + slug
	}
	return name
}

// planWorkerWorktree decides where a build worker's worktree goes: under
// <repository root>/.worktrees, on a new branch from the HEAD of dir. It
// returns nil when dir is not in a git repository that has a commit. Nothing
// is created here.
func planWorkerWorktree(dir string, childID uuid.UUID, task string) (*workerWorktree, error) {
	return planWorktreeAt(dir, childID, workerBranchName(childID, task))
}

// planWorktreeAt plans a worktree for session id on a new branch from the
// HEAD of dir, next to the main checkout (also when dir is a worktree itself),
// keeping dir's subfolder. nil when dir is not in a git repository with a
// commit. Nothing is created here.
func planWorktreeAt(dir string, id uuid.UUID, branch string) (*workerWorktree, error) {
	if !IsGitRepository(dir) {
		return nil, nil
	}
	top, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		return nil, nil
	}
	if _, err := gitOutput(dir, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		return nil, nil // no commit yet: nothing to branch from
	}
	base := GetGitBranch(dir)
	if base == "" {
		if sha, err := gitOutput(dir, "rev-parse", "--short", "HEAD"); err == nil {
			base = sha
		}
	}
	// The folder asked for may be a subfolder of the repository.
	top = filepath.Clean(top)
	rel, err := filepath.Rel(resolvePathForMatch(top), resolvePathForMatch(dir))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		rel = "."
	}
	// Worktrees live next to the main checkout, also when dir is one itself.
	home := top
	// (IsWorktree only looks at the folder itself, so ask at the top level:
	// a subfolder of a worktree used to nest the new one inside it.)
	if IsWorktree(top) {
		if main, err := GetMainWorktreePath(top); err == nil && main != "" {
			home = main
		}
	}
	root := GenerateWorktreePath(home, shortID(id), branch)
	return &workerWorktree{Root: root, Dir: filepath.Join(root, rel), Branch: branch, Base: base}, nil
}

// gitOutput runs a read-only git command in dir and returns its trimmed output.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := gitexec.Command(append([]string{"--no-optional-locks"}, args...)...)
	cmd.Dir = dir
	gitexec.Harden(cmd)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// checkWorkerSpawnAllowed applies the loop-session rules (no nesting, a cap
// on running children) with a message that fits workers.
func (sm *SessionManager) checkWorkerSpawnAllowed(caller uuid.UUID) error {
	err := sm.checkLoopSpawnAllowed(caller)
	switch {
	case errors.Is(err, ErrLoopNested):
		return ErrWorkerNested
	case errors.Is(err, ErrLoopTooMany):
		return fmt.Errorf("you already have %d sessions running that you started; wait for one to finish or ask the user to stop one", loopSpawnMaxRunning)
	}
	return err
}

// askWorkerSessionApproval shows the card and returns the answer.
func (sm *SessionManager) askWorkerSessionApproval(ctx context.Context, caller uuid.UUID, p WorkerSessionProposal) (string, string, bool) {
	question := workerSessionQuestion(p)
	startHint := "Create the session and give it the task now; it is sandboxed, with permission prompts off inside the sandbox"
	if p.ReadOnly {
		startHint = "Create the session and give it the task now; it is sandboxed and cannot change files in the folder"
	}
	input := map[string]interface{}{
		"questions": []interface{}{map[string]interface{}{
			"question": question,
			"header":   workerSessionCardHeader,
			"kind":     workerSessionCardKind,
			"proposal": p,
			"options": []interface{}{
				map[string]interface{}{"label": WorkerSessionAnswerStart, "description": startHint},
				map[string]interface{}{"label": WorkerSessionAnswerDeny, "description": "Don't start it; the agent is told you declined"},
			},
		}},
	}
	answers, detail, ok := sm.askConfirmation(ctx, caller, input)
	if !ok {
		return "", detail, false
	}
	answer, _ := answers[question].(string)
	return answer, "", true
}

// workerSessionQuestion is the plain-text card (clients without the dedicated
// card show it as is).
func workerSessionQuestion(p WorkerSessionProposal) string {
	var b strings.Builder
	if p.ReadOnly {
		b.WriteString("The agent wants to start a review worker: a new sandboxed, read-only session on another model. It can read and run checks but cannot change files in the folder, and it reports back to this session.\n\n")
	} else {
		b.WriteString("The agent wants to start a build worker: a new sandboxed session on another model that may change files in its folder. Permission prompts are off inside the sandbox, and it reports back to this session.\n\n")
	}
	fmt.Fprintf(&b, "Task: %s\n", truncateRunes(p.Task, 1200))
	fmt.Fprintf(&b, "Folder: %s\n", p.WorkingDirectory)
	if !p.ReadOnly && p.Branch != "" {
		fmt.Fprintf(&b, "Git: its own worktree on a new branch %s, from %s\n", p.Branch, p.BaseBranch)
	}
	if p.ReviewsSessionID != "" {
		fmt.Fprintf(&b, "Reviews: build worker %s (review round %d of %d)\n", p.ReviewsSessionID[:8], p.ReviewRound, p.MaxReviewRounds)
	}
	fmt.Fprintf(&b, "Model: %s (%s)", p.Model, p.Provider)
	return b.String()
}

// buildWorkerKickoffPrompt is the worker session's first prompt.
func buildWorkerKickoffPrompt(parent uuid.UUID, p WorkerSessionProposal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a WORKER session: started by another agent session (%s) with the user's approval, to do the task below. You do not see that session's conversation.\n\n", parent)
	b.WriteString("## Task\n")
	b.WriteString(p.Task)
	b.WriteString("\n\n")
	if p.Context != "" {
		b.WriteString("## Notes from the session that started you\n")
		b.WriteString(p.Context)
		b.WriteString("\n\n")
	}
	b.WriteString("## How you run\n")
	if p.ReadOnly {
		fmt.Fprintf(&b, "You are sandboxed and READ-ONLY in %s: you can read files, search and run commands that only read, but you cannot change, create or delete files there (commands may write to their temporary directory). Do not try to work around this; if the task needs a change, describe it in your reply.\n", p.WorkingDirectory)
		if p.ReviewsSessionID != "" {
			b.WriteString("\n## What to review\n")
			if p.Branch != "" {
				fmt.Fprintf(&b, "The work of a build worker: everything branch %s changed since it started from %s, in this folder (its git worktree). ", p.Branch, p.BaseBranch)
				fmt.Fprintf(&b, "Committed work: `git --no-optional-locks log --oneline %[1]s..HEAD` and `git --no-optional-locks diff %[1]s...HEAD`. ", p.BaseBranch)
				b.WriteString("Work not committed yet: `git --no-optional-locks status --porcelain` and `git --no-optional-locks diff HEAD`; read new (untracked) files directly, they are not in a diff. ")
			} else {
				b.WriteString("The work of a build worker: the uncommitted changes in this folder. See them with `git --no-optional-locks status --porcelain` and `git --no-optional-locks diff HEAD`, and read new (untracked) files directly; they are not in the diff. ")
			}
			b.WriteString("Run the project's checks if they only read.\n")
			fmt.Fprintf(&b, "This is review round %d of %d.\n", p.ReviewRound, p.MaxReviewRounds)
		}
		b.WriteString("\n## Your verdict\n")
		b.WriteString("List findings most serious first, each with file and line, what is wrong and why it matters. Approve when nothing blocks merging; small nits alone do not block. ")
		b.WriteString("End your reply with exactly one of these lines:\n<verdict>APPROVE</verdict>\n<verdict>CHANGES_REQUESTED</verdict>\n")
	} else {
		fmt.Fprintf(&b, "You are sandboxed in %s: you may change files there. Permission prompts are off; network access from commands is limited and some paths are not readable. If something you need is blocked, say so in your reply instead of working around it.\n", p.WorkingDirectory)
		if p.Branch != "" {
			fmt.Fprintf(&b, "This folder is your own git worktree on branch %s, started from %s. Commit your work on this branch as you finish each step (`git add -A && git commit -m \"...\"`), so the reviewer sees it; if the sandbox refuses a commit, leave the changes in the working tree and say so in your report. Stay on this branch: do not switch branches, merge, rebase, push or touch other worktrees. The session that started you merges your branch.\n", p.Branch, p.BaseBranch)
		}
		b.WriteString("If a build tool needs a cache or home directory outside this folder, point it at $TMPDIR (for example GOCACHE, GOMODCACHE, npm_config_cache).\n")
	}
	b.WriteString("\n## Reporting\n")
	b.WriteString("Your reply at the end of each turn is sent to the session that started you, automatically. Write it as a self-contained report: what you did or found, where (files and lines), what you verified and how, and what is left or blocked. ")
	b.WriteString("Its answers and follow-up work arrive at the top of a later prompt. To ask it something before you finish, use the eggify-tools tool `message_parent_session`.")
	return b.String()
}

var verdictRE = regexp.MustCompile(`(?i)<verdict>\s*(approved?|changes[ _-]?requested)\s*</verdict>`)

// parseVerdict returns the last verdict in a review worker's reply, or "".
func parseVerdict(reply string) string {
	all := verdictRE.FindAllStringSubmatch(reply, -1)
	if len(all) == 0 {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(all[len(all)-1][1]), "approve") {
		return VerdictApprove
	}
	return VerdictChangesRequested
}

// recordReviewVerdict counts a review round on the build worker under review
// and returns that worker's state after it (nil when it is gone).
func (sm *SessionManager) recordReviewVerdict(builderID uuid.UUID, verdict string) *WorkerConfig {
	sm.mu.Lock()
	builder, ok := sm.sessions[builderID]
	if !ok || builder.Options.Worker == nil {
		sm.mu.Unlock()
		return nil
	}
	cfg := *builder.Options.Worker
	cfg.ReviewRounds++
	cfg.LastVerdict = verdict
	if cfg.MaxReviewRounds <= 0 {
		cfg.MaxReviewRounds = DefaultMaxReviewRounds
	}
	builder.Options.Worker = &cfg
	sm.mu.Unlock()
	sm.persistSessionState(builder)
	sm.broadcastSessionUpdate(builder)
	return &cfg
}

// reviewRoundsExhausted reports whether a build worker has had its last
// review round and changes are still requested.
func reviewRoundsExhausted(cfg *WorkerConfig) bool {
	if cfg == nil || cfg.Mode != WorkerModeBuild || cfg.LastVerdict != VerdictChangesRequested {
		return false
	}
	max := cfg.MaxReviewRounds
	if max <= 0 {
		max = DefaultMaxReviewRounds
	}
	return cfg.ReviewRounds >= max
}

// reportWorkerTurnToParent sends a worker's reply to the session that started
// it. Called when a worker's turn ends.
func (sm *SessionManager) reportWorkerTurnToParent(session *AgentSession) {
	sm.mu.RLock()
	parent := session.Options.SpawnedBySessionID
	var worker *WorkerConfig
	if session.Options.Worker != nil {
		w := *session.Options.Worker
		worker = &w
	}
	model := sm.effectiveModel(session)
	dir := ""
	if session.Options.WorkingDirectory != nil {
		dir = *session.Options.WorkingDirectory
	}
	sm.mu.RUnlock()
	if parent == nil || worker == nil {
		return
	}

	session.inboxMu.Lock()
	if session.workerReports >= workerMaxReports {
		session.inboxMu.Unlock()
		logging.Warning("🧑‍🔧 Worker session %s: report limit reached (%d); not reporting this turn", session.ID, workerMaxReports)
		return
	}
	session.workerReports++
	n := session.workerReports
	session.inboxMu.Unlock()

	reply := strings.TrimSpace(sm.lastAssistantText(session.ID))
	name := worker.Mode + " worker " + shortID(session.ID)
	title := fmt.Sprintf("🧑‍🔧 Report from %s (%s)", name, model)

	var b strings.Builder
	fmt.Fprintf(&b, "Worker session %s (%s, %s) finished a turn (report %d of at most %d).\n", session.ID, worker.Mode, model, n, workerMaxReports)
	if reply == "" {
		b.WriteString("\nIt left no reply text; it may have been interrupted or have failed. Use get_loop_session to check on it.")
	} else {
		fmt.Fprintf(&b, "\nIts reply:\n%s\n", truncateRunes(reply, workerReportMaxChars))
	}

	switch {
	case worker.Mode == WorkerModeReview && reply != "":
		verdict := parseVerdict(reply)
		next, label := reviewNextStep(sm, session.ID, worker, verdict)
		b.WriteString("\n" + next)
		if label != "" {
			title += ": " + label
		}
	case worker.Mode == WorkerModeBuild && worker.Branch != "":
		fmt.Fprintf(&b, "\nIts work is in the worktree %s on branch %s (from %s); nothing is on your branch yet. Next: have it reviewed (start_worker_session with mode \"review\" and reviews_session_id %s), or give it more work with message_loop_session. When the work is approved, merge the branch.", dir, worker.Branch, worker.BaseBranch, session.ID)
	default:
		b.WriteString("\nUse message_loop_session to answer it or give it more work.")
	}

	item := InboxItem{FromSessionID: session.ID.String(), FromName: name, Kind: InboxKindWorkerReport, Text: b.String()}
	result := sm.deliverToSession(*parent, item, inboxNotice(title, reply))
	logging.Info("🧑‍🔧 Worker session %s: report %d to %s %s", session.ID, n, *parent, result)
}

// reviewNextStep records a review worker's verdict and returns what the
// proposer should do next, plus a short label for the chat note.
func reviewNextStep(sm *SessionManager, reviewerID uuid.UUID, worker *WorkerConfig, verdict string) (string, string) {
	if verdict == "" {
		return "It gave no verdict. Ask it (message_loop_session) to end its reply with <verdict>APPROVE</verdict> or <verdict>CHANGES_REQUESTED</verdict>.", "no verdict"
	}
	label := "approved"
	if verdict == VerdictChangesRequested {
		label = "changes requested"
	}
	if worker.ReviewsSessionID == nil {
		return "Verdict: " + strings.ToUpper(verdict) + ". Use message_loop_session to answer it or give it more work.", label
	}
	builderID := *worker.ReviewsSessionID
	state := sm.recordReviewVerdict(builderID, verdict)
	if state == nil {
		return "Verdict: " + strings.ToUpper(verdict) + ". The build worker it reviewed is no longer running.", label
	}
	round := fmt.Sprintf("review round %d of %d for build worker %s", state.ReviewRounds, state.MaxReviewRounds, builderID)
	label += fmt.Sprintf(" (round %d of %d)", state.ReviewRounds, state.MaxReviewRounds)
	where := "the build worker's folder"
	if state.WorktreePath != "" {
		where = fmt.Sprintf("the worktree %s (branch %s, from %s)", state.WorktreePath, state.Branch, state.BaseBranch)
	}
	switch {
	case verdict == VerdictApprove:
		return fmt.Sprintf("Verdict: APPROVE (%s). The approved work is in %s. Next: run `git status` in that folder and commit anything the builder left uncommitted there, merge the branch into yours, run the project's checks, and tell the user what was built. If the user did not ask you to merge, tell them where the work is instead.", round, where), label
	case reviewRoundsExhausted(state):
		return fmt.Sprintf("Verdict: CHANGES_REQUESTED (%s). That was the last round: do NOT send the build worker back again (it no longer takes your messages). Tell the user what is still open, where the work is (%s), and let them decide.", round, where), label
	default:
		return fmt.Sprintf("Verdict: CHANGES_REQUESTED (%s). Next: send the findings you agree with to the build worker (message_loop_session, session_id %s); when it reports back, ask this reviewer to look again (message_loop_session, session_id %s).", round, builderID, reviewerID), label
	}
}

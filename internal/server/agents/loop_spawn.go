package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/logging"
)

// Loop sessions started by agents.
//
// An agent can propose a new session that runs in loop mode with permission
// prompts off (YOLO), the way a user sets one up in Create Session. The user
// approves it on a dedicated card in the proposing session ("Start" / "Edit
// first" / "Deny"); only a signed-in user can answer it (the confirmation
// machinery in tool_confirmation.go), and nothing is created before that.
// YOLO and the verify command are otherwise refused for non-users
// (PrivilegedSessionOptions); this approval is what allows them here.
//
// The new session is linked to the proposer (SpawnedBySessionID): its final
// report goes to the proposer's inbox, the proposer can message it
// (message_loop_session) and read its state (get_loop_session), and it can
// message the proposer (message_parent_session). See session_inbox.go.
// Loop sessions are not sandboxed and always run on the Claude CLI.

// Card answers.
const (
	LoopSessionAnswerStart = "Start"
	LoopSessionAnswerEdit  = "Edit first"
	LoopSessionAnswerDeny  = "Deny"

	loopSessionCardKind   = "loop_session"
	loopSessionCardHeader = "Loop session"
)

// Limits for agent-proposed loops.
const (
	loopSpawnMaxIterations   = 50
	loopSpawnMaxTimeout      = 240 // minutes
	loopSpawnMaxGoalChars    = 8000
	loopSpawnMaxVerifyChars  = 2000
	loopSpawnMaxContextChars = 8000
	loopSpawnMaxRunning      = 3 // running loop sessions per proposing session

	messageToParentMinGap = 10 * time.Second
	messageToParentMax    = 30 // per loop session
)

// Errors returned to the calling agent.
var (
	ErrLoopNested         = errors.New("a loop session cannot start other loop sessions")
	ErrLoopForeignSession = errors.New("that session was not started by you: you can only reach loop sessions you started")
	ErrLoopNoParent       = errors.New("this session was not started by another agent session")
	ErrLoopTooMany        = fmt.Errorf("you already have %d loop sessions running; wait for one to finish or ask the user to stop one", loopSpawnMaxRunning)
)

// LoopSessionRequest is start_loop_session from eggify-tools.
type LoopSessionRequest struct {
	Goal             string `json:"goal"`
	VerifyCommand    string `json:"verify_command,omitempty"`
	WorkingDirectory string `json:"working_directory,omitempty"`
	MaxIterations    *int   `json:"max_iterations,omitempty"`
	TimeoutMinutes   *int   `json:"timeout_minutes,omitempty"`
	Model            string `json:"model,omitempty"`
	Provider         string `json:"provider,omitempty"`
	Context          string `json:"context,omitempty"` // extra notes for the loop's first turn
}

// LoopSessionProposal is what the approval card shows (and what "Edit first"
// prefills Create Session with).
type LoopSessionProposal struct {
	Goal             string `json:"goal"`
	VerifyCommand    string `json:"verify_command,omitempty"`
	WorkingDirectory string `json:"working_directory"`
	MaxIterations    int    `json:"max_iterations"`
	TimeoutMinutes   int    `json:"timeout_minutes"`
	Model            string `json:"model"`
	Provider         string `json:"provider"`
	Context          string `json:"context,omitempty"`
	YOLO             bool   `json:"yolo"`
	Sandboxed        bool   `json:"sandboxed"`
	ParentSessionID  string `json:"parent_session_id"`
	ProjectID        string `json:"project_id,omitempty"`
}

// LoopSessionResult is the reply to start_loop_session.
type LoopSessionResult struct {
	Status           string `json:"status"` // started | edit_requested
	SessionID        string `json:"session_id,omitempty"`
	WorkingDirectory string `json:"working_directory,omitempty"`
	Model            string `json:"model,omitempty"`
	MaxIterations    int    `json:"max_iterations,omitempty"`
	TimeoutMinutes   int    `json:"timeout_minutes,omitempty"`
	Note             string `json:"note"`
}

// StartLoopSessionForAgent validates an agent's proposal, asks the user, and
// on approval creates the loop session and sends it its first prompt. It
// blocks until the user answers.
func (sm *SessionManager) StartLoopSessionForAgent(ctx context.Context, caller uuid.UUID, req LoopSessionRequest) (*LoopSessionResult, error) {
	proposal, opts, err := sm.prepareLoopSession(caller, req)
	if err != nil {
		return nil, err
	}

	answer, detail, ok := sm.askLoopSessionApproval(ctx, caller, proposal)
	if !ok {
		logging.Info("🛑 Loop session proposed by %s not approved (%s)", caller, detail)
		msg := "The user did not approve the loop session, so nothing was started. Do not propose it again unless the user asks."
		if detail != "" {
			msg += " (" + detail + ")"
		}
		return nil, errors.New(msg)
	}
	switch answer {
	case LoopSessionAnswerStart:
	case LoopSessionAnswerEdit:
		logging.Info("✏️ Loop session proposed by %s: the user is editing it in Create Session", caller)
		return &LoopSessionResult{
			Status: "edit_requested",
			Note:   "The user opened the proposal in Eggify's Create Session dialog to adjust it and will start it themselves. Nothing was started yet; do not propose it again. If they start it, it is linked to you: its report arrives in your inbox and get_loop_session lists it.",
		}, nil
	default:
		logging.Info("🛑 Loop session proposed by %s denied", caller)
		return nil, errors.New("The user denied the loop session, so nothing was started. Do not propose it again unless the user asks; tell the user what you wanted to do instead.")
	}

	// Things may have changed while the card waited.
	if err := sm.checkLoopSpawnAllowed(caller); err != nil {
		return nil, err
	}

	childID := uuid.New()
	created, err := sm.CreateSession(childID, opts)
	if err != nil {
		return nil, fmt.Errorf("could not create the loop session: %w", err)
	}
	sm.mu.RLock()
	var owner *string
	if parent, ok := sm.sessions[caller]; ok {
		owner = parent.OwnerUserID
	}
	sm.mu.RUnlock()
	if owner != nil {
		if err := sm.SetSessionOwner(childID, owner); err != nil {
			logging.Warning("Loop session %s: could not set owner: %v", childID, err)
		}
	}
	logging.Info("🔁 Loop session %s created for %s (approved by the user): dir=%s model=%s", childID, caller, proposal.WorkingDirectory, created.ModelName)

	if parent, err := sm.GetSession(caller); err == nil {
		sm.emitSessionNotice(parent, fmt.Sprintf("🔁 Started loop session %s (approved by you): %s", shortID(childID), truncateRunes(oneLine(proposal.Goal), 200)))
	}
	if child, err := sm.GetSession(childID); err == nil {
		sm.emitSessionNotice(child, fmt.Sprintf("🔁 Loop session started by agent session %s, approved by the user. Permission prompts are off.", shortID(caller)))
		sm.broadcastSessionUpdate(child)
	}

	kickoff := buildLoopKickoffPrompt(caller, proposal)
	go func() {
		if err := sm.loopKickoffSend(childID, kickoff); err != nil {
			logging.Error("🔁 Loop session %s: first prompt failed: %v", childID, err)
		}
	}()

	return &LoopSessionResult{
		Status:           "started",
		SessionID:        childID.String(),
		WorkingDirectory: proposal.WorkingDirectory,
		Model:            created.ModelName,
		MaxIterations:    proposal.MaxIterations,
		TimeoutMinutes:   proposal.TimeoutMinutes,
		Note:             "The loop session is running. Its final report (completed, failed or stopped) arrives in your inbox, also if you are busy then. Use get_loop_session to check on it and message_loop_session to send it instructions.",
	}, nil
}

// loopKickoffSend sends the loop's first prompt (sendPromptHook in tests).
func (sm *SessionManager) loopKickoffSend(sessionID uuid.UUID, prompt string) error {
	if sm.sendPromptHook != nil {
		return sm.sendPromptHook(sessionID, prompt, true)
	}
	return sm.SendPrompt(sessionID, prompt, nil)
}

// prepareLoopSession validates the request and builds the card and the new
// session's options.
func (sm *SessionManager) prepareLoopSession(caller uuid.UUID, req LoopSessionRequest) (LoopSessionProposal, SessionOptions, error) {
	var p LoopSessionProposal
	if err := sm.checkLoopSpawnAllowed(caller); err != nil {
		return p, SessionOptions{}, err
	}

	goal := strings.TrimSpace(req.Goal)
	if goal == "" {
		return p, SessionOptions{}, errors.New("goal is required")
	}
	if len([]rune(goal)) > loopSpawnMaxGoalChars {
		return p, SessionOptions{}, fmt.Errorf("goal is too long (max %d characters)", loopSpawnMaxGoalChars)
	}
	verify := strings.TrimSpace(req.VerifyCommand)
	if len([]rune(verify)) > loopSpawnMaxVerifyChars {
		return p, SessionOptions{}, fmt.Errorf("verify_command is too long (max %d characters)", loopSpawnMaxVerifyChars)
	}
	extra := truncateRunes(strings.TrimSpace(req.Context), loopSpawnMaxContextChars)

	sm.mu.RLock()
	parent, ok := sm.sessions[caller]
	var parentDir, parentProvider, parentModel string
	var parentProject *string
	var parentConnectors []string
	var parentN0 *int64
	var parentPath ExecutionPath
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
		// The same n0 connection (workspace) as the parent, not the default one.
		if parent.Options.N0ConnectionID != nil {
			id := *parent.Options.N0ConnectionID
			parentN0 = &id
		}
		parentPath = sm.resolveExecutionPath(parent)
	}
	sm.mu.RUnlock()
	if !ok {
		return p, SessionOptions{}, errors.New("your session is not active")
	}

	dir, err := resolveLoopDir(req.WorkingDirectory, parentDir)
	if err != nil {
		return p, SessionOptions{}, err
	}

	provider, model := strings.TrimSpace(req.Provider), strings.TrimSpace(req.Model)
	if provider == "" && model == "" && parentPath == ExecutionPathClaudeSDK {
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
		if model == "" {
			model = sm.config.DefaultModel
		}
	}

	maxIter := clampRange(req.MaxIterations, DefaultLoopMaxIterations, loopSpawnMaxIterations)
	timeout := clampRange(req.TimeoutMinutes, DefaultLoopTimeoutMinutes, loopSpawnMaxTimeout)

	yes, no := true, false
	mode := SessionModeLoop
	systemPrompt := "You are a helpful AI assistant."
	opts := SessionOptions{
		WorkingDirectory:                &dir,
		SystemPrompt:                    &systemPrompt,
		DangerouslySkipPermissions:      &yes,
		AllowDangerouslySkipPermissions: &yes,
		Sandboxed:                       &no,
		Mode:                            &mode,
		Loop: &LoopConfig{
			Goal:           goal,
			VerifyCommand:  verify,
			MaxIterations:  &maxIter,
			TimeoutMinutes: &timeout,
		},
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
	if parentProject != nil && *parentProject != "" && dir == parentDir {
		opts.ProjectID = parentProject
	}

	// Loop sessions run on the Claude CLI only (Codex and OpenAI-compatible
	// models have their own loops or none).
	probe := &AgentSession{Session: Session{Options: opts, Provider: provider, ModelName: model}}
	if sm.resolveExecutionPath(probe) != ExecutionPathClaudeSDK {
		return p, SessionOptions{}, fmt.Errorf("loop sessions run on the Claude CLI; %q is not a Claude model or Anthropic-compatible provider. Leave provider and model empty to use the default", strings.Trim(provider+"/"+model, "/"))
	}
	// Pin both, so nothing filled in later can change what was approved.
	if model == "" {
		model = sm.effectiveModel(probe)
	}
	if provider == "" {
		provider = "claude"
	}
	opts.Provider = &provider
	if model != "" {
		opts.Model = &model
	}
	shownModel, shownProvider := model, provider

	p = LoopSessionProposal{
		Goal:             goal,
		VerifyCommand:    verify,
		WorkingDirectory: dir,
		MaxIterations:    maxIter,
		TimeoutMinutes:   timeout,
		Model:            shownModel,
		Provider:         shownProvider,
		Context:          extra,
		YOLO:             true,
		Sandboxed:        false,
		ParentSessionID:  caller.String(),
	}
	if opts.ProjectID != nil {
		p.ProjectID = *opts.ProjectID
	}
	return p, opts, nil
}

// checkLoopSpawnAllowed enforces: no nesting, and a cap on running loops.
func (sm *SessionManager) checkLoopSpawnAllowed(caller uuid.UUID) error {
	sm.mu.RLock()
	parent, ok := sm.sessions[caller]
	if !ok {
		sm.mu.RUnlock()
		return errors.New("your session is not active")
	}
	if parent.Options.SpawnedBySessionID != nil {
		sm.mu.RUnlock()
		return ErrLoopNested
	}
	var children []*AgentSession
	for _, s := range sm.sessions {
		if s.Options.SpawnedBySessionID != nil && *s.Options.SpawnedBySessionID == caller {
			children = append(children, s)
		}
	}
	statuses := make([]SessionStatus, len(children))
	for i, c := range children {
		statuses[i] = c.Status
	}
	sm.mu.RUnlock()

	running := 0
	for i, c := range children {
		if statuses[i] == SessionStatusProcessing || sm.isLoopActive(c) {
			running++
		}
	}
	if running >= loopSpawnMaxRunning {
		return ErrLoopTooMany
	}
	return nil
}

// askLoopSessionApproval shows the dedicated card and returns the answer.
func (sm *SessionManager) askLoopSessionApproval(ctx context.Context, caller uuid.UUID, p LoopSessionProposal) (string, string, bool) {
	question := loopSessionQuestion(p)
	input := map[string]interface{}{
		"questions": []interface{}{map[string]interface{}{
			"question": question,
			"header":   loopSessionCardHeader,
			"kind":     loopSessionCardKind,
			"proposal": p,
			"options": []interface{}{
				map[string]interface{}{"label": LoopSessionAnswerStart, "description": "Create the session and start the loop now, with permission prompts off"},
				map[string]interface{}{"label": LoopSessionAnswerEdit, "description": "Open these settings in Create Session to change them first"},
				map[string]interface{}{"label": LoopSessionAnswerDeny, "description": "Don't start it; the agent is told you declined"},
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

// loopSessionQuestion is the plain-text version of the card (clients without
// the dedicated card, e.g. iOS, show it as is).
func loopSessionQuestion(p LoopSessionProposal) string {
	var b strings.Builder
	b.WriteString("The agent wants to start a new loop session with permission prompts OFF (YOLO, not sandboxed). It works on its own until the check passes or a limit is hit.\n\n")
	fmt.Fprintf(&b, "Goal: %s\n", truncateRunes(p.Goal, 1200))
	if p.VerifyCommand != "" {
		fmt.Fprintf(&b, "Verify command: %s\n", p.VerifyCommand)
	} else {
		b.WriteString("Verify command: (none; runs until the agent reports done or the limits)\n")
	}
	fmt.Fprintf(&b, "Folder: %s\n", p.WorkingDirectory)
	fmt.Fprintf(&b, "Model: %s (%s)\n", p.Model, p.Provider)
	fmt.Fprintf(&b, "Limits: %d iterations, %d minutes", p.MaxIterations, p.TimeoutMinutes)
	return b.String()
}

// buildLoopKickoffPrompt is the loop session's first prompt.
func buildLoopKickoffPrompt(parent uuid.UUID, p LoopSessionProposal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are running in LOOP MODE: a session started by another agent session (%s) with the user's approval. Permission prompts are off.\n\n", parent)
	b.WriteString("## Goal\n")
	b.WriteString(p.Goal)
	b.WriteString("\n\n")
	if p.Context != "" {
		b.WriteString("## Notes from the session that started you\n")
		b.WriteString(p.Context)
		b.WriteString("\n\n")
	}
	b.WriteString("## How the loop works\n")
	if p.VerifyCommand != "" {
		fmt.Fprintf(&b, "After each of your turns Eggify runs `%s` in %s. When it exits 0 the loop is done; otherwise you get its output and continue. ", p.VerifyCommand, p.WorkingDirectory)
		b.WriteString("Do not weaken tests or edit the check itself.\n")
	} else {
		b.WriteString("After each of your turns you are prompted to continue. When the goal is fully complete, end your reply with `<promise>DONE</promise>`.\n")
	}
	fmt.Fprintf(&b, "Limits: %d iterations, %d minutes.\n", p.MaxIterations, p.TimeoutMinutes)
	b.WriteString("Keep notes in `.eggify/loop-progress.md` (what you tried, what you learned, what is left) and update it at the end of every turn.\n")
	b.WriteString("If you cannot continue without the user, end your reply with `<promise>BLOCKED: <reason></promise>` and the loop stops.\n\n")
	b.WriteString("## Talking to the session that started you\n")
	b.WriteString("Your final result is reported to it automatically. To ask it something or tell it about progress, use the eggify-tools tool `message_parent_session`; its answers arrive at the top of a later prompt.")
	return b.String()
}

// resolveLoopDir resolves the loop's folder (default: the caller's) to an
// absolute, existing directory.
func resolveLoopDir(requested, parentDir string) (string, error) {
	dir := strings.TrimSpace(requested)
	if dir == "" {
		dir = parentDir
	}
	if dir == "" {
		return "", errors.New("working_directory is required (your session has none)")
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	if !filepath.IsAbs(dir) {
		if parentDir == "" {
			return "", errors.New("working_directory must be an absolute path")
		}
		dir = filepath.Join(parentDir, dir)
	}
	dir = filepath.Clean(dir)
	if err := ValidateWorkingDirectory(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func clampRange(v *int, def, max int) int {
	if v == nil || *v <= 0 {
		return def
	}
	if *v > max {
		return max
	}
	return *v
}

func shortID(id uuid.UUID) string { return id.String()[:8] }

// --- Talking between the sessions ---

// LoopSessionInfo is get_loop_session's view of one loop session.
type LoopSessionInfo struct {
	SessionID        string `json:"session_id"`
	Kind             string `json:"kind"`                         // loop | worker
	Mode             string `json:"mode,omitempty"`               // worker: build | review
	Task             string `json:"task,omitempty"`               // worker
	Model            string `json:"model,omitempty"`              // worker
	Branch           string `json:"branch,omitempty"`             // build worker with a worktree
	BaseBranch       string `json:"base_branch,omitempty"`        // build worker with a worktree
	ReviewsSessionID string `json:"reviews_session_id,omitempty"` // review worker
	ReviewRounds     int    `json:"review_rounds,omitempty"`      // build worker
	MaxReviewRounds  int    `json:"max_review_rounds,omitempty"`  // build worker
	LastVerdict      string `json:"last_verdict,omitempty"`       // build worker
	Status           string `json:"status"`
	LoopActive       bool   `json:"loop_active"`
	Iteration        int    `json:"iteration"`
	MaxIterations    int    `json:"max_iterations"`
	Goal             string `json:"goal"`
	VerifyCommand    string `json:"verify_command,omitempty"`
	WorkingDirectory string `json:"working_directory,omitempty"`
	LastEvent        string `json:"last_event,omitempty"`
	LastReason       string `json:"last_reason,omitempty"`
	LastVerifyOutput string `json:"last_verify_output,omitempty"`
	LastReply        string `json:"last_reply,omitempty"`
	Ended            bool   `json:"ended"`
}

// GetLoopSessionsForAgent returns one of the caller's loop sessions, or all
// of them when id is uuid.Nil.
func (sm *SessionManager) GetLoopSessionsForAgent(caller, id uuid.UUID) ([]LoopSessionInfo, error) {
	sm.mu.RLock()
	var children []*AgentSession
	for _, s := range sm.sessions {
		if s.Options.SpawnedBySessionID != nil && *s.Options.SpawnedBySessionID == caller && (id == uuid.Nil || s.ID == id) {
			children = append(children, s)
		}
	}
	sm.mu.RUnlock()

	if id != uuid.Nil && len(children) == 0 {
		// Ended sessions are no longer in memory; look in the store.
		info, err := sm.storedLoopSessionInfo(caller, id)
		if err != nil {
			return nil, err
		}
		return []LoopSessionInfo{*info}, nil
	}

	out := make([]LoopSessionInfo, 0, len(children))
	for _, c := range children {
		out = append(out, sm.loopSessionInfo(c))
	}
	return out, nil
}

func (sm *SessionManager) loopSessionInfo(s *AgentSession) LoopSessionInfo {
	sm.mu.RLock()
	info := LoopSessionInfo{SessionID: s.ID.String(), Status: string(s.Status)}
	if s.Options.WorkingDirectory != nil {
		info.WorkingDirectory = *s.Options.WorkingDirectory
	}
	cfg := s.Options.Loop
	var worker *WorkerConfig
	if s.Options.Worker != nil {
		w := *s.Options.Worker
		worker = &w
	}
	model := sm.effectiveModel(s)
	sm.mu.RUnlock()
	info.Kind = "loop"
	if worker != nil {
		info.Kind, info.Mode, info.Task, info.Model = "worker", worker.Mode, truncateRunes(worker.Task, 500), model
		info.Branch, info.BaseBranch = worker.Branch, worker.BaseBranch
		info.ReviewRounds, info.MaxReviewRounds, info.LastVerdict = worker.ReviewRounds, worker.MaxReviewRounds, worker.LastVerdict
		if worker.ReviewsSessionID != nil {
			info.ReviewsSessionID = worker.ReviewsSessionID.String()
		}
	}
	if cfg != nil {
		info.Goal = truncateRunes(cfg.Goal, 500)
		info.VerifyCommand = cfg.VerifyCommand
		info.MaxIterations, _ = loopGuards(cfg)
	}
	s.loopMu.Lock()
	info.LoopActive = s.loopActive
	info.Iteration = s.loopIteration
	if ev := s.lastLoopEvent; ev != nil {
		info.LastEvent = string(ev.Type)
		info.LastReason = ev.Reason
		info.LastVerifyOutput = truncateLoopOutput(ev.VerifyOutput, 2000)
	}
	s.loopMu.Unlock()
	info.LastReply = truncateRunes(sm.lastAssistantText(s.ID), 3000)
	return info
}

func (sm *SessionManager) storedLoopSessionInfo(caller, id uuid.UUID) (*LoopSessionInfo, error) {
	if sm.Storage == nil {
		return nil, ErrLoopForeignSession
	}
	meta, err := sm.Storage.GetSession(id)
	if err != nil || meta == nil {
		return nil, ErrLoopForeignSession
	}
	opts, err := decodeOptions(meta.OptionsJSON)
	if err != nil || opts.SpawnedBySessionID == nil || *opts.SpawnedBySessionID != caller {
		return nil, ErrLoopForeignSession
	}
	info := &LoopSessionInfo{SessionID: id.String(), Kind: "loop", Status: meta.Status, Ended: true}
	if opts.Worker != nil {
		info.Kind, info.Mode, info.Task = "worker", opts.Worker.Mode, truncateRunes(opts.Worker.Task, 500)
	}
	if opts.WorkingDirectory != nil {
		info.WorkingDirectory = *opts.WorkingDirectory
	}
	if opts.Loop != nil {
		info.Goal = truncateRunes(opts.Loop.Goal, 500)
		info.VerifyCommand = opts.Loop.VerifyCommand
		info.MaxIterations, _ = loopGuards(opts.Loop)
	}
	info.LastReply = truncateRunes(sm.lastAssistantText(id), 3000)
	return info, nil
}

// MessageLoopSessionForAgent sends the caller's message to one of its loop
// sessions. A running loop gets it with its next iteration; an idle one is
// woken (without restarting its loop).
func (sm *SessionManager) MessageLoopSessionForAgent(caller, target uuid.UUID, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("text is required")
	}
	sm.mu.RLock()
	child, live := sm.sessions[target]
	owned := live && child.Options.SpawnedBySessionID != nil && *child.Options.SpawnedBySessionID == caller
	exhausted := owned && reviewRoundsExhausted(child.Options.Worker)
	sm.mu.RUnlock()
	if exhausted {
		// The build–review loop ends here; the user decides what happens next.
		return "", ErrReviewRoundsExhausted
	}
	if !live {
		if _, err := sm.storedLoopSessionInfo(caller, target); err != nil {
			return "", err
		}
	} else if !owned {
		logging.Warning("🛑 Session %s tried to message session %s, which it did not start", caller, target)
		return "", ErrLoopForeignSession
	}
	item := InboxItem{FromSessionID: caller.String(), FromName: "session that started you", Kind: InboxKindParentMessage, Text: text}
	return sm.deliverToSession(target, item, inboxNotice("📨 Message from the session that started this one ("+shortID(caller)+")", text)), nil
}

// MessageParentSessionForAgent sends a loop session's message to the session
// that started it (rate limited).
func (sm *SessionManager) MessageParentSessionForAgent(caller uuid.UUID, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("text is required")
	}
	sm.mu.RLock()
	self, ok := sm.sessions[caller]
	var parent *uuid.UUID
	name := "loop session " + shortID(caller)
	if ok {
		parent = self.Options.SpawnedBySessionID
		if w := self.Options.Worker; w != nil {
			name = w.Mode + " worker " + shortID(caller)
		}
	}
	sm.mu.RUnlock()
	if !ok {
		return "", errors.New("your session is not active")
	}
	if parent == nil {
		return "", ErrLoopNoParent
	}

	self.inboxMu.Lock()
	if self.messagesToParent >= messageToParentMax {
		self.inboxMu.Unlock()
		return "", fmt.Errorf("message limit reached (%d per session); your report is still sent when you finish", messageToParentMax)
	}
	if wait := messageToParentMinGap - time.Since(self.lastMessageToParent); wait > 0 {
		self.inboxMu.Unlock()
		return "", fmt.Errorf("too many messages: wait %ds before the next one", int(wait.Seconds())+1)
	}
	self.messagesToParent++
	self.lastMessageToParent = time.Now()
	self.inboxMu.Unlock()

	item := InboxItem{FromSessionID: caller.String(), FromName: name, Kind: InboxKindChildMessage, Text: text}
	return sm.deliverToSession(*parent, item, inboxNotice("📨 Message from "+name, text)), nil
}

// reportLoopToParent sends a loop's terminal result to the session that
// started it. Called from broadcastLoop.
func (sm *SessionManager) reportLoopToParent(session *AgentSession, msg LoopMessage) {
	sm.mu.RLock()
	parent := session.Options.SpawnedBySessionID
	sm.mu.RUnlock()
	if parent == nil {
		return
	}
	outcome := map[MessageType]string{
		MessageTypeLoopCompleted: "completed",
		MessageTypeLoopFailed:    "failed",
		MessageTypeLoopStopped:   "stopped",
	}[msg.Type]

	var b strings.Builder
	fmt.Fprintf(&b, "Loop session %s %s after %d of %d iterations: %s\n", session.ID, outcome, msg.Iteration, msg.MaxIterations, msg.Reason)
	if msg.VerifyCommand != "" {
		fmt.Fprintf(&b, "\nVerify command: %s\n", msg.VerifyCommand)
	}
	if out := strings.TrimSpace(msg.VerifyOutput); out != "" {
		fmt.Fprintf(&b, "Last verify output:\n%s\n", truncateLoopOutput(out, 1500))
	}
	if reply := strings.TrimSpace(sm.lastAssistantText(session.ID)); reply != "" {
		fmt.Fprintf(&b, "\nIts last reply:\n%s\n", truncateRunes(reply, 2500))
	}
	b.WriteString("\nUse get_loop_session for details or message_loop_session to give it more work.")

	item := InboxItem{FromSessionID: session.ID.String(), FromName: "loop session " + shortID(session.ID), Kind: InboxKindLoopReport, Text: b.String()}
	notice := inboxNotice(fmt.Sprintf("🔁 Loop session %s %s: %s", shortID(session.ID), outcome, msg.Reason), "")
	result := sm.deliverToSession(*parent, item, notice)
	logging.Info("🔁 Loop session %s: report to %s %s", session.ID, *parent, result)
}

func isTerminalLoopEvent(t MessageType) bool {
	return t == MessageTypeLoopCompleted || t == MessageTypeLoopFailed || t == MessageTypeLoopStopped
}

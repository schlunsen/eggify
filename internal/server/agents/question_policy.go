package agents

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
)

// Question policy: what happens when an agent asks the user a question
// (AskUserQuestion) and nobody answers. Confirmation cards (tool approvals,
// loop proposals; isUserConfirmation) never follow it: they always wait.
const (
	// QuestionPolicyWait waits without a limit.
	QuestionPolicyWait = "wait"
	// QuestionPolicyAutoAfter picks the recommended option after TimeoutSeconds.
	QuestionPolicyAutoAfter = "auto_after"
	// QuestionPolicyAuto picks the recommended option at once.
	QuestionPolicyAuto = "auto"

	// QuestionPolicySettingKey stores the global policy (user_settings, JSON).
	// Written only through PUT /api/agent/question-policy (IsReservedSettingKey).
	QuestionPolicySettingKey = "agent.question_policy"

	// QuestionPolicyAction names the privileged change for principal checks:
	// an agent holding the API key must not switch itself to auto-answering.
	QuestionPolicyAction = "Changing how agent questions are answered"

	minQuestionTimeoutSeconds = 10
	maxQuestionTimeoutSeconds = 24 * 60 * 60
)

// questionTimeoutUnit is one TimeoutSeconds; tests shorten it.
var questionTimeoutUnit = time.Second

// QuestionPolicy says whether unanswered agent questions wait or are
// answered with the recommended option.
type QuestionPolicy struct {
	Mode           string `json:"mode"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// DefaultQuestionPolicy applies until the user picks one: the agent carries
// on with the recommended option after two minutes.
var DefaultQuestionPolicy = QuestionPolicy{Mode: QuestionPolicyAutoAfter, TimeoutSeconds: 120}

// NormalizeQuestionPolicy validates p; TimeoutSeconds is dropped unless the
// mode uses it.
func NormalizeQuestionPolicy(p QuestionPolicy) (QuestionPolicy, error) {
	switch strings.TrimSpace(p.Mode) {
	case QuestionPolicyWait:
		return QuestionPolicy{Mode: QuestionPolicyWait}, nil
	case QuestionPolicyAuto:
		return QuestionPolicy{Mode: QuestionPolicyAuto}, nil
	case QuestionPolicyAutoAfter:
		if p.TimeoutSeconds < minQuestionTimeoutSeconds || p.TimeoutSeconds > maxQuestionTimeoutSeconds {
			return p, fmt.Errorf("timeout_seconds must be between %d and %d", minQuestionTimeoutSeconds, maxQuestionTimeoutSeconds)
		}
		return QuestionPolicy{Mode: QuestionPolicyAutoAfter, TimeoutSeconds: p.TimeoutSeconds}, nil
	default:
		return p, fmt.Errorf(`mode must be "wait", "auto_after" or "auto"`)
	}
}

// AutoAnswers reports whether the policy ever answers for the user.
func (p QuestionPolicy) AutoAnswers() bool {
	return p.Mode == QuestionPolicyAuto || p.Mode == QuestionPolicyAutoAfter
}

// delay is how long a question waits before it is answered; ok is false for wait.
func (p QuestionPolicy) delay() (time.Duration, bool) {
	switch p.Mode {
	case QuestionPolicyAuto:
		return 0, true
	case QuestionPolicyAutoAfter:
		return time.Duration(p.TimeoutSeconds) * questionTimeoutUnit, true
	}
	return 0, false
}

// autoReason is the why in "auto-picked: ..." for the agent and the chat notice.
func (p QuestionPolicy) autoReason() string {
	if p.Mode == QuestionPolicyAuto {
		return "questions in this session are answered automatically"
	}
	return "no answer from the user within " + formatQuestionTimeout(p.TimeoutSeconds)
}

func formatQuestionTimeout(seconds int) string {
	switch {
	case seconds%3600 == 0:
		if seconds == 3600 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", seconds/3600)
	case seconds%60 == 0:
		return fmt.Sprintf("%d min", seconds/60)
	default:
		return fmt.Sprintf("%d s", seconds)
	}
}

// LoadQuestionPolicy reads the global policy; isDefault is true when none (or
// an unreadable one) is stored.
func LoadQuestionPolicy(repo *database.Repository) (policy QuestionPolicy, isDefault bool) {
	repo = questionPolicyRepo(repo)
	if repo == nil {
		return DefaultQuestionPolicy, true
	}
	s, err := repo.GetUserSetting(QuestionPolicySettingKey)
	if err != nil || s == nil || strings.TrimSpace(s.Value) == "" {
		return DefaultQuestionPolicy, true
	}
	var p QuestionPolicy
	if err := json.Unmarshal([]byte(s.Value), &p); err != nil {
		logging.Warning("question policy: ignoring unreadable setting: %v", err)
		return DefaultQuestionPolicy, true
	}
	norm, err := NormalizeQuestionPolicy(p)
	if err != nil {
		logging.Warning("question policy: ignoring invalid setting: %v", err)
		return DefaultQuestionPolicy, true
	}
	return norm, false
}

// SaveQuestionPolicy validates and stores the global policy. Callers must
// have checked that a signed-in user asked for it.
func SaveQuestionPolicy(repo *database.Repository, p QuestionPolicy) (QuestionPolicy, error) {
	repo = questionPolicyRepo(repo)
	if repo == nil {
		return p, fmt.Errorf("database not available")
	}
	norm, err := NormalizeQuestionPolicy(p)
	if err != nil {
		return p, err
	}
	data, err := json.Marshal(norm)
	if err != nil {
		return p, err
	}
	if err := repo.SetUserSetting(&database.UserSetting{
		Key: QuestionPolicySettingKey, Value: string(data), ValueType: "json",
		Description: "What happens when an agent asks a question nobody answers",
	}); err != nil {
		return p, err
	}
	return norm, nil
}

// questionPolicyRepo falls back to the shared database when no repository
// was wired in.
func questionPolicyRepo(repo *database.Repository) *database.Repository {
	if repo != nil {
		return repo
	}
	if db := database.GetInstance(); db != nil {
		return database.NewRepository(db)
	}
	return nil
}

// QuestionPolicyDefault returns the global policy.
func (sm *SessionManager) QuestionPolicyDefault() QuestionPolicy {
	p, _ := LoadQuestionPolicy(sm.repo)
	return p
}

// EffectiveQuestionPolicy is the session's own policy, or the global one when
// it has none.
func (sm *SessionManager) EffectiveQuestionPolicy(session *AgentSession) QuestionPolicy {
	sm.mu.RLock()
	override := session.Options.QuestionPolicy
	sm.mu.RUnlock()
	return sm.effectiveQuestionPolicy(override)
}

func (sm *SessionManager) effectiveQuestionPolicy(override *QuestionPolicy) QuestionPolicy {
	if override != nil {
		if p, err := NormalizeQuestionPolicy(*override); err == nil {
			return p
		}
	}
	return sm.QuestionPolicyDefault()
}

// AddQuestionPolicies fills question_policy / question_policy_effective on
// session listings.
func (sm *SessionManager) AddQuestionPolicies(sessions []Session) {
	def := sm.QuestionPolicyDefault()
	for i := range sessions {
		sessions[i].QuestionPolicy, sessions[i].QuestionPolicyEffective = listedQuestionPolicy(sessions[i].Options.QuestionPolicy, def)
	}
}

func listedQuestionPolicy(override *QuestionPolicy, def QuestionPolicy) (*QuestionPolicy, *QuestionPolicy) {
	effective := def
	if override != nil {
		norm, err := NormalizeQuestionPolicy(*override)
		if err != nil {
			return nil, &effective
		}
		effective = norm
		return &norm, &effective
	}
	return nil, &effective
}

// QuestionPolicyAutoAction returns QuestionPolicyAction when opts would make
// the session answer questions for the user sooner than the global policy
// (which the user chose), else "". Waiting, or waiting longer, never needs a
// user; leaving the policy out follows the global one. Used where sessions
// are created (next to PrivilegedSessionOptions) and for handovers.
func QuestionPolicyAutoAction(opts *SessionOptions) string {
	if opts == nil || opts.QuestionPolicy == nil {
		return ""
	}
	p, err := NormalizeQuestionPolicy(*opts.QuestionPolicy)
	if err != nil {
		return QuestionPolicyAction
	}
	if answersSooner(p, func() QuestionPolicy { g, _ := LoadQuestionPolicy(nil); return g }()) {
		return QuestionPolicyAction
	}
	return ""
}

// answersSooner reports whether p answers for the user sooner than base.
func answersSooner(p, base QuestionPolicy) bool {
	d, auto := p.delay()
	if !auto {
		return false
	}
	baseDelay, baseAuto := base.delay()
	return !baseAuto || d < baseDelay
}

// SetSessionQuestionPolicy sets (or with nil clears) a session's own policy,
// persists it and re-times its waiting questions. Callers must have checked
// that a signed-in user asked for it.
func (sm *SessionManager) SetSessionQuestionPolicy(sessionID uuid.UUID, policy *QuestionPolicy) (override *QuestionPolicy, effective QuestionPolicy, err error) {
	if policy != nil {
		norm, err := NormalizeQuestionPolicy(*policy)
		if err != nil {
			return nil, effective, err
		}
		policy = &norm
	}
	updated, err := sm.UpdateSessionOptions(sessionID, func(opts *SessionOptions) {
		opts.QuestionPolicy = policy
	})
	if err != nil {
		// Not loaded (ended, or not resumed since a restart): change it in storage.
		stored, serr := sm.Storage.GetSession(sessionID)
		if serr != nil || stored == nil {
			return nil, effective, err
		}
		var opts SessionOptions
		if stored.OptionsJSON != "" {
			if err := json.Unmarshal([]byte(stored.OptionsJSON), &opts); err != nil {
				return nil, effective, fmt.Errorf("unreadable session options: %w", err)
			}
		}
		opts.QuestionPolicy = policy
		data, err := json.Marshal(opts)
		if err != nil {
			return nil, effective, err
		}
		if err := sm.Storage.UpdateSessionOptions(sessionID, string(data)); err != nil {
			return nil, effective, err
		}
		updated = &Session{ID: sessionID, Options: opts}
	}
	effective = sm.effectiveQuestionPolicy(updated.Options.QuestionPolicy)
	sm.broadcastToSessionConns(sessionID, sessionQuestionPolicyChanged(sessionID, updated.Options.QuestionPolicy, effective))
	if session, ok := sm.liveSession(sessionID); ok {
		session.retimeQuestions()
	}
	return updated.Options.QuestionPolicy, effective, nil
}

// QuestionPolicyDefaultChanged re-times the waiting questions of every live
// session that follows the global policy, and tells their clients.
func (sm *SessionManager) QuestionPolicyDefaultChanged(policy QuestionPolicy) {
	sm.mu.RLock()
	var follow, all []*AgentSession
	for _, s := range sm.sessions {
		all = append(all, s)
		if s.Options.QuestionPolicy == nil {
			follow = append(follow, s)
		}
	}
	sm.mu.RUnlock()
	for _, s := range all {
		sm.broadcastToSessionConns(s.ID, map[string]interface{}{
			"type":   string(MessageTypeQuestionPolicyDefaultChanged),
			"policy": policy,
		})
	}
	for _, s := range follow {
		sm.broadcastToSessionConns(s.ID, sessionQuestionPolicyChanged(s.ID, nil, policy))
		s.retimeQuestions()
	}
}

func sessionQuestionPolicyChanged(sessionID uuid.UUID, override *QuestionPolicy, effective QuestionPolicy) map[string]interface{} {
	return map[string]interface{}{
		"type":       string(MessageTypeSessionQuestionPolicyChanged),
		"session_id": sessionID.String(),
		"policy":     override,
		"effective":  effective,
	}
}

func (sm *SessionManager) liveSession(id uuid.UUID) (*AgentSession, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.sessions[id]
	return s, ok
}

// recommendedLabel matches Claude Code's convention for the suggested option:
// a label ending in "(Recommended)".
var recommendedLabel = regexp.MustCompile(`(?i)\(\s*recommended\s*\)\s*$`)

// recommendedOptions returns the options an auto-pick chooses: the ones
// marked "(Recommended)" (the first only for single-select), else the first.
func recommendedOptions(options []QuestionOption, multiSelect bool) []int {
	var marked []int
	for i, o := range options {
		if recommendedLabel.MatchString(o.Label) {
			marked = append(marked, i)
			if !multiSelect {
				break
			}
		}
	}
	if len(marked) > 0 {
		return marked
	}
	if len(options) == 0 {
		return []int{}
	}
	return []int{0}
}

// questionWait is the timer state of one waiting agent question.
type questionWait struct {
	askedAt time.Time
	held    bool          // "Keep waiting": no timer for this question
	changed chan struct{} // poked when the policy or held changes
}

func newQuestionWait(askedAt time.Time) *questionWait {
	return &questionWait{askedAt: askedAt, changed: make(chan struct{}, 1)}
}

func (w *questionWait) poke() {
	select {
	case w.changed <- struct{}{}:
	default:
	}
}

// deadline is when the question is answered for the user, or nil when it waits.
func (w *questionWait) deadline(p QuestionPolicy, held bool) *time.Time {
	if held {
		return nil
	}
	d, ok := p.delay()
	if !ok {
		return nil
	}
	t := w.askedAt.Add(d)
	return &t
}

// retimeQuestions makes every waiting agent question re-read the policy.
func (s *AgentSession) retimeQuestions() {
	s.questionMu.Lock()
	defer s.questionMu.Unlock()
	for _, w := range s.questionWaits {
		w.poke()
	}
}

// HoldQuestion stops the timer of one waiting question ("Keep waiting").
// Anyone may ask for it: it only makes the session wait for the user.
func (sm *SessionManager) HoldQuestion(sessionID uuid.UUID, questionID string) bool {
	session, ok := sm.liveSession(sessionID)
	if !ok {
		return false
	}
	session.questionMu.Lock()
	defer session.questionMu.Unlock()
	w, ok := session.questionWaits[questionID]
	if !ok {
		return false
	}
	w.held = true
	w.poke()
	return true
}

// sessionDisplayName is the name clients show for a session (toasts from
// other sessions): the agent name, else the avatar's.
func (sm *SessionManager) sessionDisplayName(s *Session) string {
	if s.Options.AgentName != nil && strings.TrimSpace(*s.Options.AgentName) != "" {
		return strings.TrimSpace(*s.Options.AgentName)
	}
	if s.SelectedAvatar != nil && s.SelectedAvatar.Name != "" {
		return s.SelectedAvatar.Name
	}
	if s.SelectedAvatarID != nil && sm.repo != nil {
		if a, err := sm.repo.GetAvatarByID(*s.SelectedAvatarID); err == nil && a != nil {
			return a.Name
		}
	}
	return ""
}

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/toolguard"
)

// Human confirmation for high-impact tools.
//
// Tools that spend money, upload data, deploy, send messages, sign, or write /
// delete things outside this machine (toolguard.Classify) always need the
// user's approval — yolo mode, bypassPermissions and always-allow rules do
// not apply. The question goes through the AskUserQuestion machinery, so it
// waits until the user answers or dismisses it, the turn is interrupted, or
// the session ends (no timeout), and it is re-sent when a client subscribes.
//
// The check runs in the Eggify server: the n0 proxy calls GuardTool before it
// forwards a call, and eggify-tools calls it over the loopback control endpoint
// before running its own GPU/deploy tools.

var (
	gpuLimiterOnce sync.Once
	gpuLimiter     *toolguard.Limiter
)

// GPULimiter is the server-wide GPU spending-cap tracker.
func GPULimiter() *toolguard.Limiter {
	gpuLimiterOnce.Do(func() {
		caps := toolguard.CapsFromEnv()
		logging.Info("GPU caps: %d running pod(s) per session, %d launches per day (0 = no cap)", caps.MaxGPUPodsPerSession, caps.MaxGPULaunchesPerDay)
		gpuLimiter = toolguard.NewLimiter(caps)
	})
	return gpuLimiter
}

// ToolGuardResult is the server's verdict on one tool call.
type ToolGuardResult struct {
	Allowed bool   `json:"allowed"`
	Message string `json:"message,omitempty"` // for the agent when not allowed
}

// GuardTool decides whether a session may run a tool call now, asking the
// user when the tool needs approval. It blocks until the user answers.
func (sm *SessionManager) GuardTool(ctx context.Context, sessionID uuid.UUID, tool string, args map[string]interface{}) ToolGuardResult {
	return sm.guardTool(ctx, sessionID, tool, args, GPULimiter())
}

func (sm *SessionManager) guardTool(ctx context.Context, sessionID uuid.UUID, tool string, args map[string]interface{}, limiter *toolguard.Limiter) ToolGuardResult {
	d := toolguard.Classify(tool)
	sid := sessionID.String()
	if !d.Confirm {
		if d.Effect == toolguard.EffectGPURelease {
			limiter.Release(sid)
		}
		return ToolGuardResult{Allowed: true}
	}

	if d.Effect == toolguard.EffectGPUStart {
		if err := limiter.Reserve(sid); err != nil {
			logging.Warning("🛑 %s blocked for session %s: %v", tool, sessionID, err)
			return ToolGuardResult{Message: err.Error()}
		}
	}
	approved, detail := sm.askToolApproval(ctx, sessionID, tool, args)
	if !approved {
		if d.Effect == toolguard.EffectGPUStart {
			limiter.Unreserve(sid)
		}
		logging.Info("🛑 %s not approved for session %s (%s)", tool, sessionID, detail)
		return ToolGuardResult{Message: toolguard.DeniedMessage(tool, detail)}
	}
	if d.Effect == toolguard.EffectGPURelease {
		limiter.Release(sid)
	}
	logging.Info("✅ User approved %s for session %s", tool, sessionID)
	return ToolGuardResult{Allowed: true}
}

// askToolApproval shows the approval question in the session and waits.
func (sm *SessionManager) askToolApproval(ctx context.Context, sessionID uuid.UUID, tool string, args map[string]interface{}) (bool, string) {
	answers, detail, ok := sm.askConfirmation(ctx, sessionID, toolguard.AskInput(tool, args))
	if !ok {
		return false, detail
	}
	question := toolguard.Question(tool, args)
	if answer, _ := answers[question].(string); answer != toolguard.AnswerApprove {
		return false, ""
	}
	return true, ""
}

// askConfirmation shows an AskUserQuestion-shaped confirmation in the session
// that only a signed-in user can answer, and waits. It returns the answers
// keyed by question text, or ok=false with the reason.
func (sm *SessionManager) askConfirmation(ctx context.Context, sessionID uuid.UUID, input map[string]interface{}) (map[string]interface{}, string, bool) {
	sm.mu.RLock()
	session, ok := sm.sessions[sessionID]
	var sessionCtx context.Context
	if ok {
		sessionCtx = session.ctx
	}
	sm.mu.RUnlock()
	if !ok {
		return nil, "the session is not active", false
	}
	sm.askUserQuestionMu.RLock()
	ask := sm.askUserQuestion
	sm.askUserQuestionMu.RUnlock()
	if ask == nil {
		return nil, "Eggify cannot ask the user for approval right now", false
	}

	// Stop waiting when the caller goes away or the turn is interrupted
	// (InterruptSession cancels the session context it had at this point).
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if sessionCtx != nil {
		stop := context.AfterFunc(sessionCtx, cancel)
		defer stop()
	}

	resp := ask(withUserConfirmation(waitCtx), session, input)
	if !resp.Approved {
		return nil, resp.DenyMessage, false
	}
	if resp.UpdatedInput == nil {
		return nil, "no answer", false
	}
	answers, _ := (*resp.UpdatedInput)["answers"].(map[string]interface{})
	return answers, "", true
}

// userConfirmationKey marks, on the server side only, a question that is a
// confirmation: an agent cannot set a context value, so its own
// AskUserQuestion calls never carry it.
type userConfirmationKey struct{}

func withUserConfirmation(ctx context.Context) context.Context {
	return context.WithValue(ctx, userConfirmationKey{}, true)
}

func isUserConfirmation(ctx context.Context) bool {
	v, _ := ctx.Value(userConfirmationKey{}).(bool)
	return v
}

// ErrConfirmationNeedsUser is returned when a connection that is not a
// signed-in user (e.g. one authenticated with the API key, which agents can
// read) tries to answer or dismiss a confirmation question.
var ErrConfirmationNeedsUser = errors.New("only a signed-in user can answer this approval request; it is still waiting")

// questionRequiresUser reports whether a pending question is a confirmation.
func (session *AgentSession) questionRequiresUser(questionID string) bool {
	session.questionMu.Lock()
	defer session.questionMu.Unlock()
	q := session.pendingQuestionData[questionID]
	return q != nil && q.RequiresUser
}

// n0ToolGuard adapts GuardTool to the n0 proxy's hook.
func (sm *SessionManager) n0ToolGuard(ctx context.Context, sessionID, tool string, rawArgs json.RawMessage) (bool, string) {
	id, err := uuid.Parse(sessionID)
	if err != nil {
		return false, fmt.Sprintf("%s needs the user's approval, but the session is unknown", tool)
	}
	var args map[string]interface{}
	if len(rawArgs) > 0 && string(rawArgs) != "null" {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return false, "invalid tool arguments"
		}
	}
	r := sm.GuardTool(ctx, id, tool, args)
	return r.Allowed, r.Message
}

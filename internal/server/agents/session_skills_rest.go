package agents

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/skills"
)

// Skills of a running (or stored) session, for the web's and iOS's Skills
// sheet: which approved skills the session gets and why, and adding a
// project's skill from another folder. Most approved skills apply to every
// session already; only project skills are limited to their folder.

// maxAddedSkills caps EnabledSkillIDs.
const maxAddedSkills = 50

// SessionSkillInfo is one approved skill as a session sees it.
type SessionSkillInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Scope         string `json:"scope"`
	ArgumentHint  string `json:"argument_hint,omitempty"`
	UserInvocable bool   `json:"user_invocable"`
	// Reach is why the session gets it: everywhere | folder | added, or "".
	Reach string `json:"reach"`
	// Added is true when it is in the session's own list (removable).
	Added bool `json:"added"`
}

// SessionSkillsState answers GET/PUT /api/agent/sessions/:id/skills.
type SessionSkillsState struct {
	SessionID string             `json:"session_id"`
	Runtime   string             `json:"runtime"` // claude-sdk | codex | direct ("" when not loaded)
	Skills    []SessionSkillInfo `json:"skills"`
	// RestartPending: the Claude CLI picks up the change before the next
	// prompt (a restart that stops running subagents).
	RestartPending   bool `json:"restart_pending"`
	SubagentsRunning int  `json:"subagents_running"`
}

// sessionOptionsFor returns a session's options, live or stored, and the live session (nil if not loaded).
func (sm *SessionManager) sessionOptionsFor(sessionID uuid.UUID) (*SessionOptions, *AgentSession, error) {
	if live, err := sm.GetSession(sessionID); err == nil {
		sm.mu.RLock()
		opts := live.Options
		sm.mu.RUnlock()
		return &opts, live, nil
	}
	if sm.Storage == nil {
		return nil, nil, fmt.Errorf("session not found: %s", sessionID)
	}
	meta, err := sm.Storage.GetSession(sessionID)
	if err != nil || meta == nil {
		return nil, nil, fmt.Errorf("session not found: %s", sessionID)
	}
	var opts SessionOptions
	if meta.OptionsJSON != "" {
		if err := json.Unmarshal([]byte(meta.OptionsJSON), &opts); err != nil {
			return nil, nil, fmt.Errorf("session options unreadable: %w", err)
		}
	}
	return &opts, nil, nil
}

// SessionSkills lists every approved skill and whether the session gets it.
func (h *AgentHandler) SessionSkills(sessionID uuid.UUID) (*SessionSkillsState, error) {
	sm := h.SessionManager
	opts, live, err := sm.sessionOptionsFor(sessionID)
	if err != nil {
		return nil, err
	}
	state := &SessionSkillsState{SessionID: sessionID.String(), Skills: []SessionSkillInfo{}}
	if sm.repo != nil {
		all, err := sm.repo.Skill.GetAllSkills("")
		if err != nil {
			return nil, fmt.Errorf("cannot list skills: %w", err)
		}
		added := map[string]bool{}
		for _, id := range opts.EnabledSkillIDs {
			added[id] = true
		}
		seen := map[string]bool{}
		for _, s := range all {
			if s.Status != database.ReviewStatusApproved || seen[s.Name] || !skills.ValidPluginSkillName(s.Name) {
				continue
			}
			seen[s.Name] = true
			state.Skills = append(state.Skills, SessionSkillInfo{
				ID: s.ID, Name: s.Name, Description: s.Description, Scope: s.Scope, ArgumentHint: s.ArgumentHint,
				UserInvocable: s.UserInvocable, Reach: skills.Reach(s, sessionWorkDir(opts), opts.EnabledSkillIDs),
				Added: added[s.ID] || added[s.Name],
			})
		}
		sort.Slice(state.Skills, func(i, j int) bool { return state.Skills[i].Name < state.Skills[j].Name })
	}
	if live != nil {
		state.Runtime = string(sm.resolveExecutionPath(live))
		if state.Runtime == string(ExecutionPathClaudeSDK) {
			_, key := sm.sessionSkillPlugin(opts)
			live.mu.Lock()
			state.RestartPending = live.client != nil && live.skillsKey != key
			live.mu.Unlock()
		}
	}
	for _, a := range h.SubagentsForSession(sessionID) {
		if a.Status == BackgroundAgentStatusRunning {
			state.SubagentsRunning++
		}
	}
	return state, nil
}

// SetSessionAddedSkills replaces the session's own skill list (EnabledSkillIDs)
// with the approved skills among ids, persists it, and tells the session's
// connections. The Claude CLI picks the change up before the next prompt.
func (h *AgentHandler) SetSessionAddedSkills(sessionID uuid.UUID, ids []string) (*SessionSkillsState, error) {
	sm := h.SessionManager
	if _, _, err := sm.sessionOptionsFor(sessionID); err != nil {
		return nil, err
	}
	// Keep only approved skills, once each, by id.
	clean := []string{}
	if sm.repo != nil && len(ids) > 0 {
		all, err := sm.repo.Skill.GetAllSkills("")
		if err != nil {
			return nil, fmt.Errorf("cannot list skills: %w", err)
		}
		approved := map[string]string{}
		for _, s := range all {
			if s.Status == database.ReviewStatusApproved {
				approved[s.ID] = s.ID
				if _, taken := approved[s.Name]; !taken {
					approved[s.Name] = s.ID
				}
			}
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if real, ok := approved[id]; ok && !seen[real] && len(clean) < maxAddedSkills {
				seen[real] = true
				clean = append(clean, real)
			}
		}
	}

	var optionsJSON []byte
	if live, err := sm.GetSession(sessionID); err == nil {
		sm.mu.Lock()
		live.Options.EnabledSkillIDs = clean
		optionsJSON, _ = json.Marshal(live.Options)
		sm.mu.Unlock()
	} else {
		meta, err := sm.Storage.GetSession(sessionID)
		if err != nil || meta == nil {
			return nil, fmt.Errorf("session not found: %s", sessionID)
		}
		var opts SessionOptions
		if meta.OptionsJSON != "" {
			if err := json.Unmarshal([]byte(meta.OptionsJSON), &opts); err != nil {
				return nil, fmt.Errorf("session options unreadable: %w", err)
			}
		}
		opts.EnabledSkillIDs = clean
		optionsJSON, _ = json.Marshal(opts)
	}
	if sm.Storage != nil {
		if err := sm.Storage.UpdateSessionOptions(sessionID, string(optionsJSON)); err != nil {
			logging.Warning("Failed to save skills of session %s: %v", sessionID, err)
			return nil, fmt.Errorf("failed to save the session's skills")
		}
	}
	logging.Info("🧩 Session %s: added skills set to %v", sessionID, clean)

	state, err := h.SessionSkills(sessionID)
	if err != nil {
		return nil, err
	}
	// To the session's open connections only (nothing is buffered for later).
	sm.broadcastToSessionConns(sessionID, map[string]interface{}{
		"type":       string(MessageTypeSessionSkillsUpdated),
		"session_id": sessionID.String(),
		"action":     "set",
		"skill_ids":  clean,
	})
	return state, nil
}

package agents

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
)

// Use the last model request, not cumulative billing usage across the thread.
// Cached input is already included in this total.
func codexContextReading(model string, tokens, window int) *types.ContextUsageResponse {
	if tokens <= 0 || window <= 0 {
		return nil
	}
	return &types.ContextUsageResponse{
		Model: model, TotalTokens: tokens, MaxTokens: window,
		Percentage: float64(tokens) / float64(window) * 100,
	}
}

func (sm *SessionManager) getCodexContextUsage(session *AgentSession) *types.ContextUsageResponse {
	sm.mu.RLock()
	threadID, model := codexStr(session.Options.CodexThreadID), session.ModelName
	sm.mu.RUnlock()
	session.codexTurnMu.Lock()
	reading := session.codexContextUsage
	if session.codexContextThread != threadID {
		reading = nil
	}
	session.codexTurnMu.Unlock()
	if reading != nil {
		copy := *reading
		return &copy
	}
	// Codex persists token_count events in its rollout. This also works for
	// sessions created before Eggify started retaining live usage notifications.
	if _, err := uuid.Parse(threadID); err != nil {
		return &types.ContextUsageResponse{Model: model}
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return &types.ContextUsageResponse{Model: model}
		}
		home = filepath.Join(userHome, ".codex")
	}
	for _, dir := range []string{"sessions", "archived_sessions"} {
		for _, pattern := range []string{
			filepath.Join(home, dir, "*", "*", "*", "rollout-*-"+threadID+".jsonl"),
			filepath.Join(home, dir, "rollout-*-"+threadID+".jsonl"),
		} {
			paths, _ := filepath.Glob(pattern)
			for _, path := range paths {
				if usage := readCodexContextUsage(path, model); usage != nil {
					return usage
				}
			}
		}
	}
	// A zero window explicitly means unavailable; never invent a model limit.
	return &types.ContextUsageResponse{Model: model}
}

func readCodexContextUsage(path, model string) *types.ContextUsageResponse {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var latest *types.ContextUsageResponse
	for scanner.Scan() {
		var event struct {
			Type    string `json:"type"`
			Payload struct {
				Type string `json:"type"`
				Info *struct {
					Last struct {
						TotalTokens int `json:"total_tokens"`
					} `json:"last_token_usage"`
					Window int `json:"model_context_window"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Type != "event_msg" || event.Payload.Type != "token_count" || event.Payload.Info == nil {
			continue
		}
		info := event.Payload.Info
		latest = codexContextReading(model, info.Last.TotalTokens, info.Window)
	}
	if scanner.Err() != nil {
		return nil
	}
	return latest
}

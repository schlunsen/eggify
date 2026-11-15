package agents

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/logging"
)

// Commit the session change, visible notice and inspectable handoff together.
// A failed switch must not leave a false transition in the attribution timeline.
func (sm *SessionManager) persistModelSwitch(session *Session, note, historyMode string) (uuid.UUID, error) {
	id := uuid.New()
	options, err := json.Marshal(session.Options)
	if err != nil {
		return uuid.Nil, err
	}
	tx, err := sm.db.Begin()
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE agent_sessions SET model_name = ?, provider = ?, options = ?,
		claude_session_id = ?, message_count = ?, updated_at = ? WHERE id = ?`,
		session.ModelName, session.Provider, string(options), session.ClaudeSessionID,
		session.MessageCount, session.UpdatedAt, session.ID.String())
	if err != nil {
		return uuid.Nil, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return uuid.Nil, fmt.Errorf("session not found: %s", session.ID)
	}
	if _, err = tx.Exec(`INSERT INTO agent_messages (id, session_id, sequence, role, content, timestamp)
		VALUES (?, ?, ?, 'system', ?, ?)`, id.String(), session.ID.String(), session.MessageCount, note, session.UpdatedAt); err != nil {
		return uuid.Nil, err
	}
	if _, err = tx.Exec(`INSERT INTO agent_model_switches (message_id, session_id, history_mode) VALUES (?, ?, ?)`,
		id.String(), session.ID.String(), historyMode); err != nil {
		return uuid.Nil, err
	}
	if err = tx.Commit(); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// Save only the first accepted request after a switch. The snapshot survives
// reloads and subsequent turns; it is never reconstructed from newer history.
func (sm *SessionManager) captureModelSwitchContext(sessionID uuid.UUID, beforeSequence int, content, format string) {
	if sm.db == nil || content == "" {
		return
	}
	_, err := sm.db.Exec(`UPDATE agent_model_switches SET context = ?, format = ?, preview = 0
		WHERE message_id = (SELECT ms.message_id FROM agent_model_switches ms
		JOIN agent_messages am ON am.id = ms.message_id
		WHERE ms.session_id = ? AND (? <= 0 OR am.sequence < ?)
		ORDER BY am.sequence DESC LIMIT 1)
		AND (context IS NULL OR preview = 1) AND history_mode IN ('replayed', 'rebuilt')`,
		content, format, sessionID.String(), beforeSequence, beforeSequence)
	if err != nil {
		logging.Warning("Failed to save switch context for session %s: %v", sessionID, err)
	}
}

type ModelSwitchContext struct {
	OmittedRecords int    `json:"omitted_records,omitempty"`
	HistoryMode    string `json:"history_mode"`
	Status         string `json:"status"`
	Context        string `json:"context,omitempty"`
	Format         string `json:"format"`
}

func (sm *SessionManager) GetModelSwitchContext(sessionID, messageID uuid.UUID) (*ModelSwitchContext, error) {
	var note string
	var mode, content, format sql.NullString
	var superseded, preview bool
	var sequence int
	err := sm.db.QueryRow(`SELECT am.content, am.sequence, ms.history_mode, ms.context, ms.format,
		COALESCE(ms.preview, 0),
		EXISTS (SELECT 1 FROM agent_messages newer WHERE newer.session_id = am.session_id
			AND newer.sequence > am.sequence AND newer.role = 'system' AND newer.content LIKE '🔀 Switched model: %')
		FROM agent_messages am LEFT JOIN agent_model_switches ms ON ms.message_id = am.id
		WHERE am.session_id = ? AND am.id = ? AND am.role = 'system'`,
		sessionID.String(), messageID.String()).Scan(&note, &sequence, &mode, &content, &format, &preview, &superseded)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(note, "🔀 Switched model: ") {
		return nil, sql.ErrNoRows
	}
	result := &ModelSwitchContext{HistoryMode: mode.String, Format: format.String, Status: "unavailable"}
	if mode.String == HistoryModeRebuilt && preview {
		var count int
		if err := sm.db.QueryRow(`SELECT COUNT(*) FROM agent_messages WHERE session_id = ? AND sequence < ? AND role IN ('user', 'assistant')`, sessionID.String(), sequence).Scan(&count); err != nil {
			return nil, err
		}
		if count > 200 {
			result.OmittedRecords = count - 200
		}
	}
	switch {
	case content.Valid && !preview:
		result.Status, result.Context = "sent", content.String
	case !mode.Valid:
		// Old versions did not store the actual handoff payload.
	case mode.String == HistoryModeResumed:
		result.Status = "resumed"
		// The provider replays its own stored conversation, so there is no
		// payload of ours to record. Render what preceded the switch instead:
		// bounded by this notice's sequence, it cannot drift as the session
		// continues, and it works for switches recorded before this existed.
		if content.Valid {
			result.Context = content.String
		} else if transcript, count, err := sm.buildConversationTranscript(sessionID, sequence, maxHistoryReplayChars); err == nil && count > 0 {
			result.Context = transcript
		}
	case mode.String == HistoryModeNone:
		result.Status = "empty"
	case superseded:
		// A preview only describes what *would* have been carried over. Another
		// switch landed first, so this handoff never actually happened; showing
		// its transcript would claim a request that was never made.
		result.Status = "superseded"
	case content.Valid:
		// Recorded at switch time so the handoff can be inspected right away;
		// replaced by the real payload once the next request goes out.
		result.Status, result.Context = "preview", content.String
	default:
		result.Status = "pending"
	}
	return result, nil
}

// previewModelSwitchContext records, at switch time, the transcript that the
// next request will carry over. Without it the handoff is only inspectable
// after the user sends another message, which is precisely when they are least
// likely to be looking. captureModelSwitchContext overwrites it with the real
// payload once that request is actually sent.
func (sm *SessionManager) previewModelSwitchContext(session *AgentSession, messageID uuid.UUID, historyMode string, beforeSequence int) {
	if sm.db == nil {
		return
	}
	var content string
	switch historyMode {
	case HistoryModeReplayed:
		content = sm.historyReplayPrefix(session, beforeSequence)
	case HistoryModeRebuilt:
		messages, err := sm.buildDirectConversationHistory(session.ID, beforeSequence)
		if err != nil {
			return
		}
		encoded, err := json.MarshalIndent(messages, "", "  ")
		if err != nil {
			return
		}
		content = string(encoded)
	default:
		return
	}
	if content == "" {
		return
	}
	format := "text"
	if historyMode == HistoryModeRebuilt {
		format = "json"
	}
	if _, err := sm.db.Exec(`UPDATE agent_model_switches SET context = ?, format = ?, preview = 1
		WHERE message_id = ? AND context IS NULL`, content, format, messageID.String()); err != nil {
		logging.Warning("Failed to save switch context preview for session %s: %v", session.ID, err)
	}
}

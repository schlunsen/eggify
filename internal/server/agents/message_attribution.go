package agents

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type messageAttribution struct {
	provider string
	model    string
}

// Old releases saved model switches as system messages, but did not stamp
// individual replies. Read the full switch timeline independently of message
// pagination so even a page without a switch note gets the original labels.
func (s *SQLiteSessionStorage) restoreMessageAttribution(sessionID uuid.UUID, messages []*MessageRecord) error {
	needsAttribution := false
	for _, msg := range messages {
		if msg.Role == "assistant" && msg.ModelName == "" {
			needsAttribution = true
			break
		}
	}
	if !needsAttribution {
		return nil
	}

	rows, err := s.db.Query(`SELECT sequence, content FROM agent_messages
		WHERE session_id = ? AND role = 'system' AND content LIKE '🔀 Switched model: %'
		ORDER BY sequence ASC`, sessionID.String())
	if err != nil {
		return fmt.Errorf("load message attribution: %w", err)
	}
	defer rows.Close()
	type transition struct {
		sequence      int
		before, after messageAttribution
	}
	var switches []transition
	for rows.Next() {
		var seq int
		var content string
		if err := rows.Scan(&seq, &content); err != nil {
			return err
		}
		before, after, ok := parseModelSwitchNote(content)
		if ok {
			switches = append(switches, transition{seq, before, after})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	var current messageAttribution
	if len(switches) == 0 {
		err := s.db.QueryRow(`SELECT COALESCE(provider, ''),
			COALESCE(NULLIF(model_name, ''), json_extract(options, '$.model'), '')
			FROM agent_sessions WHERE id = ?`, sessionID.String()).Scan(&current.provider, &current.model)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
	} else {
		current = switches[0].before
	}
	for _, msg := range messages {
		if msg.Role != "assistant" || msg.ModelName != "" {
			continue // Persisted per-message attribution always wins.
		}
		attribution := current
		for _, change := range switches {
			if change.sequence >= msg.Sequence {
				break
			}
			attribution = change.after
		}
		msg.Provider, msg.ModelName = attribution.provider, attribution.model
	}
	return nil
}

func parseModelSwitchNote(content string) (messageAttribution, messageAttribution, bool) {
	const prefix = "🔀 Switched model: "
	var empty messageAttribution
	if !strings.HasPrefix(content, prefix) {
		return empty, empty, false
	}
	before, after, ok := strings.Cut(strings.TrimPrefix(content, prefix), " → ")
	if !ok {
		return empty, empty, false
	}
	parse := func(value string) (messageAttribution, bool) {
		provider, model, ok := strings.Cut(value, "/")
		return messageAttribution{provider, model}, ok && model != ""
	}
	prev, prevOK := parse(before)
	next, nextOK := parse(after)
	return prev, next, prevOK && nextOK
}

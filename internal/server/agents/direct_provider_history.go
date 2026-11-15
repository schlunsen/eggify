package agents

// Tool results for the direct (OpenAI-compatible) provider path only.
//
// The direct path rebuilds the conversation from agent_messages on every
// prompt. Tool results used to be sent to the UI but never stored, so on the
// next prompt the model saw its own earlier tool calls with "(tool result not
// available from history)" and redid work. Results are now stored on their
// tool_use entry (tool_uses[].result), which buildDirectConversationHistory and
// the model-switch replay already read.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/llmclient"
)

const (
	// directToolResultMaxChars caps a tool result the model sees in the turn
	// that ran it (a huge file or log would otherwise fill the context).
	directToolResultMaxChars = 40000
	// directStoredResultMaxChars caps the result stored for later turns.
	directStoredResultMaxChars = 8000
	// directHistoryRecentResults: the newest results are replayed at stored size...
	directHistoryRecentResults = 12
	// ...older ones are cut to this.
	directHistoryOldResultMaxChars = 1500
)

// truncateMiddle keeps the start and the end of s (errors and exit codes
// usually sit at the end of command output) within max runes.
func truncateMiddle(s string, max int) string {
	r := []rune(s)
	if max <= 0 || len(r) <= max {
		return s
	}
	marker := fmt.Sprintf("\n…[%d characters omitted]…\n", len(r)-max)
	keep := max - len([]rune(marker))
	if keep < 2 {
		return string(r[:max])
	}
	head := keep * 3 / 5
	tail := keep - head
	return string(r[:head]) + marker + string(r[len(r)-tail:])
}

// saveDirectToolResult stores a tool result on the tool_use entry it answers.
// The tool_use row is written synchronously before the tool runs, so it exists.
func (sm *SessionManager) saveDirectToolResult(sessionID uuid.UUID, toolCallID, result string, isError bool) error {
	if sm.db == nil || toolCallID == "" {
		return nil
	}
	rows, err := sm.db.Query(`
		SELECT sequence, tool_uses FROM agent_messages
		WHERE session_id = ? AND role = 'assistant' AND instr(tool_uses, ?) > 0
		ORDER BY sequence DESC LIMIT 5`, sessionID.String(), toolCallID)
	if err != nil {
		return err
	}
	type candidate struct {
		seq      int
		toolUses string
	}
	var cands []candidate
	for rows.Next() {
		var c candidate
		if rows.Scan(&c.seq, &c.toolUses) == nil {
			cands = append(cands, c)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, c := range cands {
		var toolUses []map[string]interface{}
		if json.Unmarshal([]byte(c.toolUses), &toolUses) != nil {
			continue
		}
		found := false
		for _, tu := range toolUses {
			if id, _ := tu["id"].(string); id == toolCallID {
				tu["result"] = truncateMiddle(result, directStoredResultMaxChars)
				if isError {
					tu["is_error"] = true
				}
				found = true
			}
		}
		if !found {
			continue
		}
		updated, err := json.Marshal(toolUses)
		if err != nil {
			return err
		}
		_, err = sm.db.Exec(`UPDATE agent_messages SET tool_uses = ? WHERE session_id = ? AND sequence = ?`,
			string(updated), sessionID.String(), c.seq)
		return err
	}
	return fmt.Errorf("no stored tool call %s", toolCallID)
}

// trimOldToolResults cuts every tool result except the newest keep ones to
// maxChars, so long sessions replay recent output in full and old output briefly.
func trimOldToolResults(messages []llmclient.ChatMessage, keep, maxChars int) {
	seen := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "tool" {
			continue
		}
		seen++
		if seen > keep {
			if s, ok := messages[i].Content.(string); ok {
				messages[i].Content = truncateMiddle(s, maxChars)
			}
		}
	}
}

// historyResultText turns a stored tool_uses[].result into text.
func historyResultText(r interface{}) string {
	switch v := r.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		b, _ := json.Marshal(v)
		return strings.TrimSpace(string(b))
	}
}

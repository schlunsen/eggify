package agents

import (
	"fmt"
	"strings"

	"github.com/schlunsen/claude-agent-sdk-go/types"
)

// The Claude CLI streams many system frames that are status for a host, not
// conversation: thinking_tokens several times a second while the model thinks,
// status, hook_*, session_state_changed, post_turn_summary, turn_duration,
// init and dozens more. They used to be forwarded into the chat, where each
// one showed up as "SystemMessage: <subtype>" (and flipped an idle session
// back to processing). Only the subtypes below carry something for a person;
// their text is stored and shown as a notice, everything else stays internal.

// chatSystemText returns the chat text for a CLI system message, and false
// when the message is not meant for the transcript.
func chatSystemText(m *types.SystemMessage) (string, bool) {
	str := func(k string) string {
		v, _ := m.Data[k].(string)
		return strings.TrimSpace(v)
	}
	num := func(v interface{}) int {
		f, _ := v.(float64)
		return int(f)
	}

	var text string
	switch m.Subtype {
	case "local_command_output", "away_summary":
		// Output of a slash command run by the CLI itself (/cost, /context, ...).
		text = str("content")
	case "informational":
		// "info" is transcript-only noise in the CLI too.
		if str("level") != "info" {
			text = str("content")
		}
	case "notification":
		text = str("text")
	case "model_fallback":
		text = str("content")
		if text == "" && str("fallback_model") != "" {
			text = fmt.Sprintf("Switched to fallback model %s", str("fallback_model"))
		}
		if text != "" {
			text = "🔀 " + text
		}
	case "compact_boundary":
		meta, _ := m.Data["compact_metadata"].(map[string]interface{})
		text = "🗜️ Context compacted"
		if pre := num(meta["pre_tokens"]); pre > 0 {
			if post := num(meta["post_tokens"]); post > 0 {
				text += fmt.Sprintf(" (%s → %s tokens)", compactTokens(pre), compactTokens(post))
			} else {
				text += fmt.Sprintf(" (%s tokens before)", compactTokens(pre))
			}
		}
	case "memory_saved":
		paths, _ := m.Data["written_paths"].([]interface{})
		if len(paths) > 0 {
			verb := str("verb")
			if verb == "" {
				verb = "Saved"
			}
			noun := "memories"
			if len(paths) == 1 {
				noun = "memory"
			}
			text = fmt.Sprintf("🧠 %s %d %s", verb, len(paths), noun)
		}
	}
	return text, text != ""
}

func compactTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

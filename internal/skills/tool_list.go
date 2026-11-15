package skills

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ToolList is the allowed-tools frontmatter field. Claude Code accepts it as a
// string ("Read, Grep, Glob") or as a YAML list; either way it is kept as one
// comma-separated string, which is what the skills table stores
// (database.Skill.AllowedTools) and what the UI edits.
type ToolList string

// String returns the comma-separated list.
func (t ToolList) String() string { return string(t) }

func joinTools(items []string) ToolList {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it = strings.TrimSpace(it); it != "" {
			out = append(out, it)
		}
	}
	return ToolList(strings.Join(out, ", "))
}

// UnmarshalYAML accepts a scalar or a sequence of scalars.
func (t *ToolList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!null" {
			*t = ""
			return nil
		}
		*t = ToolList(strings.TrimSpace(n.Value))
		return nil
	case yaml.SequenceNode:
		var items []string
		if err := n.Decode(&items); err != nil {
			return fmt.Errorf("allowed-tools: list items must be tool names: %w", err)
		}
		*t = joinTools(items)
		return nil
	default:
		return fmt.Errorf("allowed-tools (line %d): expected a string or a list of tool names", n.Line)
	}
}

// UnmarshalJSON accepts a string, a list of strings or null.
func (t *ToolList) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*t = ToolList(strings.TrimSpace(s))
		return nil
	}
	var items []string
	if err := json.Unmarshal(b, &items); err != nil {
		return fmt.Errorf("allowed_tools must be a string or a list of strings")
	}
	*t = joinTools(items)
	return nil
}

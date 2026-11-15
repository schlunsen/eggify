package agents

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/schlunsen/eggify/internal/logging"
)

// MatchesRule checks if current request matches an always-allow rule.
// Rules that ValidateAlwaysAllowRule refuses never match, wherever they came from.
func MatchesRule(rule AlwaysAllowRule, toolName string, input map[string]interface{}) bool {
	// Tool name must always match
	if rule.Tool != toolName {
		return false
	}
	if err := ValidateAlwaysAllowRule(rule); err != nil {
		logging.Warning("⚠️ Ignoring unsafe always-allow rule (tool=%s, desc=%q): %v", rule.Tool, rule.Description, err)
		return false
	}

	switch rule.MatchMode {
	case RuleMatchExact:
		return parametersMatchExact(rule.Parameters, input)

	case RuleMatchPattern:
		return parametersMatchPattern(rule.Pattern, toolName, input)
	}

	return false
}

// parametersMatchExact performs deep equality check on parameters
func parametersMatchExact(ruleParams, requestParams map[string]interface{}) bool {
	// Convert both to JSON for deep comparison
	ruleJSON, err1 := json.Marshal(ruleParams)
	requestJSON, err2 := json.Marshal(requestParams)

	if err1 != nil || err2 != nil {
		return false
	}

	return string(ruleJSON) == string(requestJSON)
}

// parametersMatchPattern checks pattern-based matching
func parametersMatchPattern(pattern *RulePattern, toolName string, input map[string]interface{}) bool {
	if pattern == nil {
		return false
	}

	switch toolName {
	case "Bash":
		if pattern.CommandPrefix != nil {
			cmd, ok := input["command"].(string)
			if !ok {
				return false
			}
			// Whole words only, and never a compound command, expansion or redirection.
			return bashCommandMatchesPrefix(*pattern.CommandPrefix, cmd)
		}

	case "Read", "Write", "Edit":
		if pattern.DirectoryPath != nil {
			filePath, ok := input["file_path"].(string)
			if !ok {
				return false
			}
			// A directory rule never covers writes into .git: its config
			// and hooks make a later `git` command (and a git rule) run code.
			if toolName != "Read" && pathInsideGitDir(filePath) {
				return false
			}
			// Compared after resolving "..", "~" and symlinks.
			return filePathInDirectory(filePath, *pattern.DirectoryPath)
		}

	case "Grep":
		if pattern.PathPattern != nil {
			// Wildcard "*" means allow ALL grep operations
			if *pattern.PathPattern == "*" {
				return true
			}

			path, ok := input["path"].(string)
			if !ok {
				return false
			}
			matched, _ := filepath.Match(*pattern.PathPattern, path)
			return matched
		}

	case "Glob":
		if pattern.PathPattern != nil {
			// Wildcard "*" means allow ALL glob patterns
			if *pattern.PathPattern == "*" {
				return true
			}

			globPattern, ok := input["pattern"].(string)
			if !ok {
				return false
			}
			// Check if the glob pattern is within the allowed path pattern
			return strings.HasPrefix(globPattern, strings.TrimSuffix(*pattern.PathPattern, "*"))
		}
	}

	return false
}

// FormatPatternDescription generates human-readable description of a pattern
func FormatPatternDescription(pattern *RulePattern, toolName string) string {
	if pattern == nil {
		return ""
	}

	// Check if this is a wildcard rule (allow all)
	isWildcard := (pattern.CommandPrefix != nil && *pattern.CommandPrefix == "*") ||
		(pattern.DirectoryPath != nil && *pattern.DirectoryPath == "*") ||
		(pattern.PathPattern != nil && *pattern.PathPattern == "*")

	if isWildcard {
		// Wildcard pattern - allow ALL of this tool type
		switch toolName {
		case "Bash":
			return "All Bash commands"
		case "Read":
			return "All Read operations (any file)"
		case "Write":
			return "All Write operations (any file)"
		case "Edit":
			return "All Edit operations (any file)"
		case "Grep":
			return "All Grep operations"
		case "Glob":
			return "All Glob operations"
		default:
			return "All " + toolName + " operations"
		}
	}

	// Non-wildcard patterns (for future use)
	switch toolName {
	case "Bash":
		if pattern.CommandPrefix != nil {
			return "All commands starting with: " + *pattern.CommandPrefix
		}

	case "Read":
		if pattern.DirectoryPath != nil {
			return "All files in: " + *pattern.DirectoryPath + "/"
		}

	case "Write":
		if pattern.DirectoryPath != nil {
			return "All writes to: " + *pattern.DirectoryPath + "/"
		}

	case "Edit":
		if pattern.DirectoryPath != nil {
			return "All edits in: " + *pattern.DirectoryPath + "/"
		}

	case "Grep", "Glob":
		if pattern.PathPattern != nil {
			return "Pattern: " + *pattern.PathPattern
		}
	}

	return "Pattern match"
}

// CheckAlwaysAllowRules checks if a tool request matches any always-allow rules
// Returns (matched bool, ruleDescription string)
func CheckAlwaysAllowRules(rules []AlwaysAllowRule, toolName string, input map[string]interface{}) (bool, string) {
	logging.Info("🔍 CheckAlwaysAllowRules: checking %d rules for tool %s", len(rules), toolName)
	for i, rule := range rules {
		logging.Info("  Rule %d: tool=%s, mode=%s, desc=%s", i, rule.Tool, rule.MatchMode, rule.Description)
		if rule.Pattern != nil {
			logging.Info("    Pattern: cmd_prefix=%v, dir_path=%v, path_pattern=%v",
				rule.Pattern.CommandPrefix, rule.Pattern.DirectoryPath, rule.Pattern.PathPattern)
		}
		if MatchesRule(rule, toolName, input) {
			logging.Info("✅ AUTO-APPROVED via always-allow rule: %s (rule: %s, mode: %s)",
				toolName, rule.Description, rule.MatchMode)
			return true, rule.Description
		}
	}
	logging.Info("❌ No matching rule found")
	return false, ""
}

// Helper function to create string pointer
func stringPtr(s string) *string {
	return &s
}

package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/schlunsen/eggify/internal/logging"
)

// contextUsageFromJSONL is a fallback for when the SDK's GetContextUsage control
// protocol method is not supported by the CLI version. It reads the conversation
// JSONL file and extracts token usage from the last assistant message's API usage.
//
// The Claude CLI stores conversations at:
//   ~/.claude/projects/<project-hash>/<session-id>.jsonl
//
// Each assistant message includes a "usage" field with input_tokens, output_tokens,
// cache_creation_input_tokens, and cache_read_input_tokens.

// JSONLUsageData holds the parsed usage data from a conversation JSONL file.
type JSONLUsageData struct {
	Model         string
	InputTokens   int
	OutputTokens  int
	CacheCreation int
	CacheRead     int
	ContextWindow int // Derived from model
	TotalTokens   int // input_tokens + cache_creation + cache_read = total context
}

// jsonlMessage represents the minimal fields we need from a JSONL line.
type jsonlMessage struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
}

// jsonlAssistantMessage represents the nested message object for assistant messages.
type jsonlAssistantMessage struct {
	Role  string      `json:"role"`
	Model string      `json:"model"`
	Usage *jsonlUsage `json:"usage"`
}

// jsonlUsage represents the usage field in an assistant message.
type jsonlUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// GetContextUsageFromJSONL reads the conversation JSONL file and extracts
// token usage from the last assistant message. This is a fallback for CLI
// versions that don't support the get_context_usage control protocol subtype,
// and the answer for sessions whose CLI is not running.
func GetContextUsageFromJSONL(claudeSessionID string, workingDirectory string) (*JSONLUsageData, error) {
	if claudeSessionID == "" {
		return nil, fmt.Errorf("no Claude session ID available")
	}

	// Find the JSONL file
	jsonlPath, err := cachedConversationJSONL(claudeSessionID, workingDirectory)
	if err != nil {
		return nil, fmt.Errorf("could not find conversation JSONL: %w", err)
	}

	// Parse the last assistant message with usage data
	usage, err := parseLastUsageFromJSONL(jsonlPath)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		// The transcript moved or was removed: forget the cached path.
		forgetConversationJSONL(claudeSessionID)
	}
	return usage, err
}

// jsonlMissTTL is how long a failed transcript lookup is remembered. A miss
// scans every project directory, and the context-usage endpoint is polled.
const jsonlMissTTL = 30 * time.Second

type jsonlPathEntry struct {
	path     string
	missedAt time.Time // set for a remembered miss
}

var (
	jsonlPathMu    sync.Mutex
	jsonlPathCache = map[string]jsonlPathEntry{}
	// Swapped out in tests.
	jsonlPathFind = findConversationJSONL
)

// cachedConversationJSONL resolves the transcript path once per Claude session
// and remembers misses briefly, so a poll does not stat across all project
// directories every time.
func cachedConversationJSONL(claudeSessionID, workingDirectory string) (string, error) {
	jsonlPathMu.Lock()
	entry, ok := jsonlPathCache[claudeSessionID]
	jsonlPathMu.Unlock()
	if ok {
		if entry.path != "" {
			return entry.path, nil
		}
		if time.Since(entry.missedAt) < jsonlMissTTL {
			return "", fmt.Errorf("conversation JSONL not found for session %s", claudeSessionID)
		}
	}

	path, err := jsonlPathFind(claudeSessionID, workingDirectory)
	jsonlPathMu.Lock()
	if err != nil {
		jsonlPathCache[claudeSessionID] = jsonlPathEntry{missedAt: time.Now()}
	} else {
		jsonlPathCache[claudeSessionID] = jsonlPathEntry{path: path}
	}
	jsonlPathMu.Unlock()
	return path, err
}

func forgetConversationJSONL(claudeSessionID string) {
	jsonlPathMu.Lock()
	delete(jsonlPathCache, claudeSessionID)
	jsonlPathMu.Unlock()
}

// findConversationJSONL locates the conversation JSONL file.
// Claude stores conversations at: ~/.claude/projects/<project-hash>/<session-id>.jsonl
// The project hash is the working directory path with / replaced by -.
func findConversationJSONL(claudeSessionID string, workingDirectory string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not get home directory: %w", err)
	}

	claudeProjectsDir := filepath.Join(homeDir, ".claude", "projects")

	// If we have a working directory, compute the project hash directly.
	// The leading "/" becomes a leading "-", which is the CLI's convention.
	if workingDirectory != "" {
		projectHash := strings.ReplaceAll(workingDirectory, "/", "-")
		jsonlPath := filepath.Join(claudeProjectsDir, projectHash, claudeSessionID+".jsonl")
		if _, err := os.Stat(jsonlPath); err == nil {
			return jsonlPath, nil
		}
	}

	// Fallback: search all project directories for the session JSONL
	entries, err := os.ReadDir(claudeProjectsDir)
	if err != nil {
		return "", fmt.Errorf("could not read Claude projects directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		jsonlPath := filepath.Join(claudeProjectsDir, entry.Name(), claudeSessionID+".jsonl")
		if _, err := os.Stat(jsonlPath); err == nil {
			return jsonlPath, nil
		}
	}

	return "", fmt.Errorf("conversation JSONL not found for session %s", claudeSessionID)
}

// jsonlTailChunk is how much of the transcript is read per step from the end.
const jsonlTailChunk = 64 * 1024

// parseLastUsageFromJSONL returns usage data from the last assistant message
// that has a usage field. It reads the file backwards in chunks and stops at
// the first such line, so a long transcript costs about one chunk rather than
// a parse of every line.
func parseLastUsageFromJSONL(path string) (*JSONLUsageData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open JSONL file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("could not stat JSONL file: %w", err)
	}

	// carry holds the start of the earliest line seen so far, whose beginning
	// lies in a chunk not read yet.
	var carry []byte
	pos := info.Size()
	for pos > 0 {
		n := int64(jsonlTailChunk)
		if n > pos {
			n = pos
		}
		pos -= n
		buf := make([]byte, n, n+int64(len(carry)))
		if _, err := f.ReadAt(buf, pos); err != nil && err != io.EOF {
			return nil, fmt.Errorf("error reading JSONL: %w", err)
		}
		data := append(buf, carry...)

		lines := bytes.Split(data, []byte("\n"))
		first := 0
		if pos > 0 {
			// The first segment may be the tail of a line that starts earlier.
			carry = lines[0]
			first = 1
		} else {
			carry = nil
		}
		for i := len(lines) - 1; i >= first; i-- {
			if usage := parseUsageLine(lines[i]); usage != nil {
				logging.Debug("JSONL fallback: found usage data - model=%s, input_tokens=%d, context_window=%d",
					usage.Model, usage.TotalTokens, usage.ContextWindow)
				return usage, nil
			}
		}
	}

	return nil, fmt.Errorf("no assistant messages with usage data found")
}

// parseUsageLine returns the usage of an assistant transcript line, or nil.
func parseUsageLine(line []byte) *JSONLUsageData {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || !bytes.Contains(line, []byte(`"usage"`)) {
		return nil
	}

	var msg jsonlMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil // Skip malformed lines
	}
	if msg.Type != "assistant" || msg.Message == nil {
		return nil
	}

	var assistantMsg jsonlAssistantMessage
	if err := json.Unmarshal(msg.Message, &assistantMsg); err != nil || assistantMsg.Usage == nil {
		return nil
	}

	// Total context = input_tokens + cache_creation + cache_read
	// input_tokens alone is only the non-cached portion. The full context
	// sent to the model includes all cached tokens as well.
	totalContext := assistantMsg.Usage.InputTokens +
		assistantMsg.Usage.CacheCreationInputTokens +
		assistantMsg.Usage.CacheReadInputTokens

	return &JSONLUsageData{
		Model:         assistantMsg.Model,
		InputTokens:   assistantMsg.Usage.InputTokens,
		OutputTokens:  assistantMsg.Usage.OutputTokens,
		CacheCreation: assistantMsg.Usage.CacheCreationInputTokens,
		CacheRead:     assistantMsg.Usage.CacheReadInputTokens,
		TotalTokens:   totalContext,
		ContextWindow: getModelContextWindow(assistantMsg.Model),
	}
}

// getModelContextWindow returns the known context window size for a model.
func getModelContextWindow(model string) int {
	// Normalize model name for matching
	m := strings.ToLower(model)

	// Every 1M-context model must be matched before the generic "opus" /
	// "sonnet" / "haiku" cases below, which return 200000. A model that falls
	// through to those is not a missing entry that fails loudly — it silently
	// reports context usage against a window five times too small.
	switch {
	// Claude Opus 5 model
	case strings.Contains(m, "opus-5"):
		return 1000000
	// Claude Fable 5 models. The substring also covers claude-fable-5-1 and claude-fable-5-5.
	case strings.Contains(m, "fable-5"):
		return 1000000
	// Claude Mythos 5 models (claude-mythos-5-1, claude-mythos-5-2): the same model as Fable 5.
	case strings.Contains(m, "mythos-5"):
		return 1000000
	// Claude Sonnet 5. Distinct from claude-sonnet-4-5, which is 200K.
	case strings.Contains(m, "sonnet-5"):
		return 1000000
	// Claude 4.6+ models have 1M token context windows
	case strings.Contains(m, "opus-4-8"):
		return 1000000
	case strings.Contains(m, "opus-4-7"):
		return 1000000
	case strings.Contains(m, "opus-4-6"):
		return 1000000
	case strings.Contains(m, "sonnet-4-6"):
		return 1000000
	// Older Claude models
	case strings.Contains(m, "opus"):
		return 200000
	case strings.Contains(m, "sonnet"):
		return 200000
	case strings.Contains(m, "haiku"):
		return 200000
	case strings.Contains(m, "claude-3"):
		return 200000
	case strings.Contains(m, "claude"):
		return 200000
	case strings.Contains(m, "deepseek"):
		return 64000
	// OpenAI GPT-6 family (Astra / Sol / Luna) — 1.05M context
	case strings.Contains(m, "gpt-6"):
		return 1050000
	case strings.Contains(m, "gpt-4"):
		return 128000
	default:
		return 200000 // Safe default for Claude models
	}
}

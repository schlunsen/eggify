package agents

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/installer"
	"github.com/schlunsen/eggify/internal/logging"
)

// Session digests: a one-line summary and a few topic tags per agent session,
// written by a small model and cached in session_digests, for the agent
// overview.
//
// Digests are generated only when the overview asks for a session whose
// current state (message count and status, the "version") differs from the
// cached one, and at most once per digestMinInterval per session, so a busy
// agent does not trigger a model call per message. Generation never touches
// the session itself: it reads the stored transcript and runs a separate,
// isolated one-shot `claude -p` (see claudeDigestGenerator).

const (
	digestMinInterval   = 3 * time.Minute  // between regenerations of one session
	digestRetryInterval = 5 * time.Minute  // after a failed attempt
	digestTimeout       = 90 * time.Second // per model call
	digestWorkers       = 2
	digestQueueSize     = 64
	digestMinMessages   = 2 // nothing worth summarizing before this
	digestMaxTags       = 4
	digestSummaryMax    = 160
	digestTaskChars     = 1200
	digestActivityChars = 6000
)

// SessionDigest is the cached summary of one session.
type SessionDigest struct {
	SessionID string    `json:"session_id"`
	Summary   string    `json:"summary"`
	Tags      []string  `json:"tags"`
	Version   string    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	Pending   bool      `json:"pending,omitempty"` // a newer digest is being written
}

// DigestRequest names a session and its current version.
type DigestRequest struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// digestInput is everything the model sees about a session.
type digestInput struct {
	Project   string
	Branch    string
	Status    string
	Task      string // the first thing the user asked
	Activity  string // the most recent messages and tool calls
	KnownTags []string
}

// DigestGenerator writes a summary and tags for a session.
type DigestGenerator func(ctx context.Context, in digestInput) (summary string, tags []string, err error)

type digestJob struct {
	id      uuid.UUID
	version string
}

// SessionDigestService caches digests and generates missing or stale ones in
// the background.
type SessionDigestService struct {
	db       *sql.DB
	storage  SessionStorage
	generate DigestGenerator

	mu       sync.Mutex
	pending  map[uuid.UUID]bool
	attempts map[uuid.UUID]time.Time // last generation attempt, for throttling
	queue    chan digestJob
	stop     chan struct{}
	once     sync.Once
}

// NewSessionDigestService starts the background workers. generate may be nil
// to use the Claude CLI.
func NewSessionDigestService(db *sql.DB, storage SessionStorage, generate DigestGenerator) *SessionDigestService {
	if generate == nil {
		generate = claudeDigestGenerator
	}
	s := &SessionDigestService{
		db:       db,
		storage:  storage,
		generate: generate,
		pending:  map[uuid.UUID]bool{},
		attempts: map[uuid.UUID]time.Time{},
		queue:    make(chan digestJob, digestQueueSize),
		stop:     make(chan struct{}),
	}
	for i := 0; i < digestWorkers; i++ {
		go s.work()
	}
	return s
}

// Close stops the workers.
func (s *SessionDigestService) Close() {
	s.once.Do(func() { close(s.stop) })
}

// Digests returns the cached digests for the requested sessions. When
// generate is set, sessions whose digest is missing or out of date are queued
// for a new one (subject to throttling) and marked pending.
func (s *SessionDigestService) Digests(requests []DigestRequest, generate bool) map[string]SessionDigest {
	out := make(map[string]SessionDigest, len(requests))
	ids := make([]uuid.UUID, 0, len(requests))
	for _, req := range requests {
		if id, err := uuid.Parse(req.ID); err == nil {
			ids = append(ids, id)
		}
	}
	cached := s.loadMany(ids)
	for _, req := range requests {
		id, err := uuid.Parse(req.ID)
		if err != nil {
			continue
		}
		digest, found := cached[id]
		if generate && s.shouldRegenerate(id, req.Version, digest, found) {
			digest.Pending = s.enqueue(id, req.Version)
		} else {
			s.mu.Lock()
			digest.Pending = s.pending[id]
			s.mu.Unlock()
		}
		if found || digest.Pending {
			digest.SessionID = req.ID
			out[req.ID] = digest
		}
	}
	return out
}

func (s *SessionDigestService) shouldRegenerate(id uuid.UUID, version string, digest SessionDigest, found bool) bool {
	if messages := versionMessageCount(version); messages < digestMinMessages {
		return false
	}
	if found && digest.Version == version {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[id] {
		return false
	}
	if last, ok := s.attempts[id]; ok {
		wait := digestMinInterval
		if !found {
			wait = digestRetryInterval // the last attempt failed
		}
		if time.Since(last) < wait {
			return false
		}
	}
	if found && time.Since(digest.UpdatedAt) < digestMinInterval {
		return false
	}
	return true
}

// versionMessageCount reads the message count from a "<count>:<status>" version.
func versionMessageCount(version string) int {
	var n int
	_, _ = fmt.Sscanf(version, "%d:", &n)
	return n
}

func (s *SessionDigestService) enqueue(id uuid.UUID, version string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case s.queue <- digestJob{id: id, version: version}:
		s.pending[id] = true
		s.attempts[id] = time.Now()
		return true
	default:
		return false // full; asked again on the next poll
	}
}

func (s *SessionDigestService) work() {
	for {
		select {
		case <-s.stop:
			return
		case job := <-s.queue:
			s.run(job)
		}
	}
}

func (s *SessionDigestService) run(job digestJob) {
	defer func() {
		if r := recover(); r != nil {
			logging.Error("Session digest panic for %s: %v", job.id, r)
		}
		s.mu.Lock()
		delete(s.pending, job.id)
		s.mu.Unlock()
	}()
	in, err := s.buildInput(job.id)
	if err != nil {
		logging.Debug("Session digest: cannot read session %s: %v", job.id, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), digestTimeout)
	defer cancel()
	summary, tags, err := s.generate(ctx, in)
	if err != nil {
		logging.Warning("Session digest for %s failed: %v", job.id, err)
		return
	}
	summary, tags = cleanDigest(summary, tags)
	if summary == "" {
		return
	}
	if err := s.save(job.id, summary, tags, job.version); err != nil {
		logging.Warning("Failed to save session digest for %s: %v", job.id, err)
	}
}

// loadMany loads the cached digests of many sessions in one query.
func (s *SessionDigestService) loadMany(ids []uuid.UUID) map[uuid.UUID]SessionDigest {
	out := make(map[uuid.UUID]SessionDigest, len(ids))
	if len(ids) == 0 {
		return out
	}
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id.String()
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := s.db.Query(`SELECT session_id, summary, tags, source_version, updated_at
		FROM session_digests WHERE session_id IN (`+placeholders+`)`, args...)
	if err != nil {
		logging.Debug("Failed to load session digests: %v", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var d SessionDigest
		var idStr, tags string
		if err := rows.Scan(&idStr, &d.Summary, &tags, &d.Version, &d.UpdatedAt); err != nil {
			continue
		}
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(tags), &d.Tags)
		if d.Tags == nil {
			d.Tags = []string{}
		}
		out[id] = d
	}
	return out
}

func (s *SessionDigestService) save(id uuid.UUID, summary string, tags []string, version string) error {
	encoded, _ := json.Marshal(tags)
	_, err := s.db.Exec(`INSERT INTO session_digests (session_id, summary, tags, source_version, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET summary = excluded.summary, tags = excluded.tags,
			source_version = excluded.source_version, updated_at = excluded.updated_at`,
		id.String(), summary, string(encoded), version, time.Now().UTC())
	return err
}

// knownTags returns the most used tags, so the model reuses them and tag
// filters keep grouping related sessions.
func (s *SessionDigestService) knownTags(limit int) []string {
	rows, err := s.db.Query(`SELECT tags FROM session_digests ORDER BY updated_at DESC LIMIT 200`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var raw string
		var tags []string
		if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &tags) == nil {
			for _, tag := range tags {
				counts[tag]++
			}
		}
	}
	all := make([]string, 0, len(counts))
	for tag := range counts {
		all = append(all, tag)
	}
	sort.Slice(all, func(i, j int) bool {
		if counts[all[i]] != counts[all[j]] {
			return counts[all[i]] > counts[all[j]]
		}
		return all[i] < all[j]
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

// buildInput reads what the model needs from the stored session.
func (s *SessionDigestService) buildInput(id uuid.UUID) (digestInput, error) {
	meta, err := s.storage.GetSession(id)
	if err != nil {
		return digestInput{}, err
	}
	in := digestInput{Branch: meta.GitBranch, Status: meta.Status, KnownTags: s.knownTags(30)}
	var options SessionOptions
	if meta.OptionsJSON != "" && json.Unmarshal([]byte(meta.OptionsJSON), &options) == nil && options.WorkingDirectory != nil {
		in.Project = filepath.Base(*options.WorkingDirectory)
	}
	if first, _, err := s.storage.GetMessages(id, 5, 0); err == nil {
		for _, m := range first {
			if m.Role == "user" {
				if text := messageText(m); text != "" {
					in.Task = clipText(text, digestTaskChars)
					break
				}
			}
		}
	}
	latest, _, err := s.storage.GetLatestMessages(id, 40, 0)
	if err != nil {
		return digestInput{}, err
	}
	in.Activity = transcriptTail(latest, digestActivityChars)
	return in, nil
}

// transcriptTail renders the newest messages as plain lines, oldest first,
// keeping the most recent up to max characters.
func transcriptTail(messages []*MessageRecord, max int) string {
	var lines []string
	for _, m := range messages {
		if text := messageText(m); text != "" {
			role := map[string]string{"user": "User", "assistant": "Agent", "system": "System"}[m.Role]
			if role == "" {
				role = m.Role
			}
			lines = append(lines, role+": "+clipText(text, 600))
		}
		var tools []struct {
			Name  string                 `json:"name"`
			Input map[string]interface{} `json:"input"`
		}
		if len(m.ToolUses) > 0 && json.Unmarshal(m.ToolUses, &tools) == nil {
			for _, tool := range tools {
				line := "Tool: " + tool.Name
				if detail := toolInputSummary(tool.Input); detail != "" {
					line += " " + detail
				}
				lines = append(lines, line)
			}
		}
	}
	var kept []string
	total := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if total+len(lines[i]) > max && len(kept) > 0 {
			break
		}
		kept = append([]string{lines[i]}, kept...)
		total += len(lines[i]) + 1
	}
	return strings.Join(kept, "\n")
}

// messageText returns a stored message's readable text: plain content, or the
// text blocks of a JSON content array.
func messageText(m *MessageRecord) string {
	content := strings.TrimSpace(m.Content)
	if strings.HasPrefix(content, "[") {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(content), &blocks) == nil {
			var parts []string
			for _, b := range blocks {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					parts = append(parts, b.Text)
				}
			}
			content = strings.Join(parts, "\n")
		}
	}
	return strings.Join(strings.Fields(content), " ")
}

func clipText(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// digestPrompt is the user message sent to the model.
func digestPrompt(in digestInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Project: %s", orDash(in.Project))
	if in.Branch != "" {
		fmt.Fprintf(&b, " (branch %s)", in.Branch)
	}
	fmt.Fprintf(&b, "\nStatus: %s\n", orDash(in.Status))
	if len(in.KnownTags) > 0 {
		fmt.Fprintf(&b, "Tags already used on other sessions (reuse one when it fits): %s\n", strings.Join(in.KnownTags, ", "))
	}
	fmt.Fprintf(&b, "\n<first_request>\n%s\n</first_request>\n\n<recent_activity>\n%s\n</recent_activity>\n\n", orDash(in.Task), orDash(in.Activity))
	b.WriteString(`Write a JSON object with:
- "summary": one sentence, at most 120 characters, saying what the agent is working on and where it is at, e.g. "Fixing flaky Codex tests; race found in codex_subagents.go, patch in progress". No preamble, no "The agent".
- "tags": 2 to 4 short lowercase topic tags (one or two words, hyphenated), such as "auth", "deploy", "frontend", "bug-fix". Describe the work, not the status.
Reply with only the JSON object.`)
	return b.String()
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

const digestSystemPrompt = `You label coding-agent sessions for a dashboard. You read a transcript excerpt and reply with only a JSON object {"summary": string, "tags": string[]}.`

var digestJSON = regexp.MustCompile(`(?s)\{.*\}`)

// parseDigest reads the model's reply, tolerating code fences and prose around
// the JSON object.
func parseDigest(reply string) (string, []string, error) {
	match := digestJSON.FindString(reply)
	if match == "" {
		return "", nil, fmt.Errorf("no JSON object in reply")
	}
	var out struct {
		Summary string   `json:"summary"`
		Tags    []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(match), &out); err != nil {
		return "", nil, err
	}
	return out.Summary, out.Tags, nil
}

var tagJunk = regexp.MustCompile(`[^a-z0-9-]+`)

// cleanDigest trims the summary and normalizes tags: lowercase, hyphenated,
// short, unique, at most digestMaxTags.
func cleanDigest(summary string, tags []string) (string, []string) {
	summary = strings.Join(strings.Fields(summary), " ")
	if r := []rune(summary); len(r) > digestSummaryMax {
		summary = strings.TrimSpace(string(r[:digestSummaryMax-1])) + "…"
	}
	seen := map[string]bool{}
	clean := []string{}
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(tag), "#")))
		tag = strings.Trim(tagJunk.ReplaceAllString(strings.ReplaceAll(tag, " ", "-"), ""), "-")
		if tag == "" || len(tag) > 24 || seen[tag] {
			continue
		}
		seen[tag] = true
		clean = append(clean, tag)
		if len(clean) == digestMaxTags {
			break
		}
	}
	return summary, clean
}

// claudeDigestGenerator asks Haiku through the Claude CLI, fully isolated from
// the user's setup: no hooks, MCP servers, tools, settings files, project
// instructions, extended thinking or saved session. About 1k tokens per call.
func claudeDigestGenerator(ctx context.Context, in digestInput) (string, []string, error) {
	path, err := installer.FindClaudePath()
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "eggify-digest-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(dir)
	cmd := exec.CommandContext(ctx, path, "-p",
		"--model", "haiku",
		"--no-session-persistence",
		"--setting-sources", "",
		"--settings", `{"disableAllHooks":true,"alwaysThinkingEnabled":false}`,
		"--strict-mcp-config",
		"--tools", "",
		"--system-prompt", digestSystemPrompt,
		"--output-format", "json",
	)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "MAX_THINKING_TOKENS=0")
	cmd.Stdin = strings.NewReader(digestPrompt(in))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", nil, fmt.Errorf("claude: %v: %s", err, clipText(strings.TrimSpace(stderr.String()), 300))
	}
	var result struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return "", nil, fmt.Errorf("unreadable claude output: %v", err)
	}
	if result.IsError {
		return "", nil, fmt.Errorf("claude: %s", clipText(result.Result, 300))
	}
	return parseDigest(result.Result)
}

package agents

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeDigests records what it was asked and answers with a fixed digest.
type fakeDigests struct {
	mu     sync.Mutex
	inputs []digestInput
	fail   bool
}

func (f *fakeDigests) generate(_ context.Context, in digestInput) (string, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, in)
	if f.fail {
		return "", nil, errors.New("model unavailable")
	}
	return "  Fixing flaky Codex tests;   race found, patch in progress ", []string{"Testing", "#bug fix", "testing", "go", "codex", "extra"}, nil
}

func (f *fakeDigests) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.inputs)
}

func newDigestHarness(t *testing.T) (*SessionDigestService, *fakeDigests, *AgentSession) {
	t.Helper()
	sm, cleanup := newModelSwitchTestManager(t)
	t.Cleanup(cleanup)
	session := seedSwitchSession(t, sm, "claude", "opus", func(s *Session) { s.GitBranch = "fix/flaky" })
	seedMessages(t, sm, session,
		[2]string{"user", "The Codex tests are flaky, please fix them"},
		[2]string{"assistant", `[{"type":"text","text":"Running the tests to reproduce."}]`},
		[2]string{"assistant", "Found a data race in codex_subagents.go."})
	fake := &fakeDigests{}
	service := NewSessionDigestService(sm.db, sm.Storage, fake.generate)
	t.Cleanup(service.Close)
	return service, fake, session
}

func waitForDigest(t *testing.T, s *SessionDigestService, req DigestRequest) SessionDigest {
	t.Helper()
	var digest SessionDigest
	require.Eventually(t, func() bool {
		d, ok := s.Digests([]DigestRequest{req}, false)[req.ID]
		digest = d
		return ok && !d.Pending && d.Summary != ""
	}, 5*time.Second, 10*time.Millisecond)
	return digest
}

func TestSessionDigestIsGeneratedOnceAndCached(t *testing.T) {
	s, fake, session := newDigestHarness(t)
	req := DigestRequest{ID: session.ID.String(), Version: "3:idle"}

	first := s.Digests([]DigestRequest{req}, true)
	require.True(t, first[req.ID].Pending, "a missing digest is queued")

	digest := waitForDigest(t, s, req)
	require.Equal(t, "Fixing flaky Codex tests; race found, patch in progress", digest.Summary)
	require.Equal(t, []string{"testing", "bug-fix", "go", "codex"}, digest.Tags)
	require.Equal(t, "3:idle", digest.Version)

	// What the model saw: the task, recent activity (JSON content flattened) and the branch.
	require.Equal(t, 1, fake.calls())
	in := fake.inputs[0]
	require.Equal(t, "The Codex tests are flaky, please fix them", in.Task)
	require.Contains(t, in.Activity, "Agent: Running the tests to reproduce.")
	require.Contains(t, in.Activity, "Agent: Found a data race in codex_subagents.go.")
	require.Equal(t, "fix/flaky", in.Branch)

	// Same version: served from the cache, no new call.
	again := s.Digests([]DigestRequest{req}, true)[req.ID]
	require.False(t, again.Pending)
	// Moved on, but within the minimum interval: still no new call.
	moved := s.Digests([]DigestRequest{{ID: req.ID, Version: "9:processing"}}, true)[req.ID]
	require.False(t, moved.Pending)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 1, fake.calls())
}

func TestSessionDigestSkipsEmptySessionsAndReadOnlyRequests(t *testing.T) {
	s, fake, session := newDigestHarness(t)
	id := session.ID.String()
	require.Empty(t, s.Digests([]DigestRequest{{ID: id, Version: "1:idle"}}, true), "too little to summarize")
	require.Empty(t, s.Digests([]DigestRequest{{ID: id, Version: "3:idle"}}, false), "generate=false never calls the model")
	require.Empty(t, s.Digests([]DigestRequest{{ID: "not-a-uuid", Version: "3:idle"}}, true))
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 0, fake.calls())
}

func TestFailedSessionDigestIsNotRetriedStraightAway(t *testing.T) {
	s, fake, session := newDigestHarness(t)
	fake.fail = true
	req := DigestRequest{ID: session.ID.String(), Version: "3:idle"}
	require.True(t, s.Digests([]DigestRequest{req}, true)[req.ID].Pending)
	require.Eventually(t, func() bool { return fake.calls() == 1 && len(s.Digests([]DigestRequest{req}, false)) == 0 }, 5*time.Second, 10*time.Millisecond)
	require.Empty(t, s.Digests([]DigestRequest{req}, true), "throttled after a failure")
	require.Equal(t, 1, fake.calls())
}

func TestParseAndCleanDigest(t *testing.T) {
	summary, tags, err := parseDigest("Sure!\n```json\n{\"summary\": \"Deploying the preview\", \"tags\": [\"Deploy\", \"CI/CD\"]}\n```")
	require.NoError(t, err)
	summary, tags = cleanDigest(summary, tags)
	require.Equal(t, "Deploying the preview", summary)
	require.Equal(t, []string{"deploy", "cicd"}, tags)

	_, _, err = parseDigest("I could not do that")
	require.Error(t, err)

	long, _ := cleanDigest(strings.Repeat("word ", 60), nil)
	require.LessOrEqual(t, len([]rune(long)), digestSummaryMax)
	require.True(t, strings.HasSuffix(long, "…"))
}

func TestDigestPromptCarriesContextAndKnownTags(t *testing.T) {
	prompt := digestPrompt(digestInput{Project: "eggify", Branch: "main", Status: "idle", Task: "Fix it", Activity: "Tool: Bash go test", KnownTags: []string{"testing", "deploy"}})
	require.Contains(t, prompt, "Project: eggify (branch main)")
	require.Contains(t, prompt, "reuse one when it fits): testing, deploy")
	require.Contains(t, prompt, "<first_request>\nFix it\n</first_request>")
	require.Contains(t, prompt, "Tool: Bash go test")
}

// Runs the real Claude CLI (a Haiku call, about $0.001). Opt in with
// EGGIFY_LIVE_DIGEST=1.
func TestClaudeDigestGeneratorLive(t *testing.T) {
	if os.Getenv("EGGIFY_LIVE_DIGEST") == "" {
		t.Skip("set EGGIFY_LIVE_DIGEST=1 to call the Claude CLI")
	}
	summary, tags, err := claudeDigestGenerator(context.Background(), digestInput{
		Project: "eggify", Branch: "fix/flaky", Status: "processing",
		Task:     "The Codex tests are flaky, please fix them",
		Activity: "Tool: Bash go test ./internal/server/agents -run Codex -race\nAgent: Found a data race in codex_subagents.go; fixing the map access.\nTool: Edit internal/server/agents/codex_subagents.go",
	})
	require.NoError(t, err)
	summary, tags = cleanDigest(summary, tags)
	t.Logf("summary=%q tags=%v", summary, tags)
	require.NotEmpty(t, summary)
	require.NotEmpty(t, tags)
}

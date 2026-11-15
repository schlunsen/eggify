package agents

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/stretchr/testify/require"
)

// swapGitStatusCompute replaces the git status computation and clock for one test.
func swapGitStatusCompute(t *testing.T, compute func(string) (*GitStatusData, error), now func() time.Time) {
	t.Helper()
	oldCompute, oldNow := gitStatusCompute, gitStatusNow
	gitStatusCompute, gitStatusNow = compute, now
	gitStatusMu.Lock()
	gitStatusCache = map[string]gitStatusCacheEntry{}
	gitStatusMu.Unlock()
	t.Cleanup(func() {
		gitStatusCompute, gitStatusNow = oldCompute, oldNow
		gitStatusMu.Lock()
		gitStatusCache = map[string]gitStatusCacheEntry{}
		gitStatusMu.Unlock()
	})
}

func TestGitStatusCacheServesRepeatedCallsWithinTTL(t *testing.T) {
	var calls int32
	clock := time.Unix(1_000_000, 0)
	swapGitStatusCompute(t, func(dir string) (*GitStatusData, error) {
		atomic.AddInt32(&calls, 1)
		return &GitStatusData{Branch: "main", Modified: []string{"a.go"}}, nil
	}, func() time.Time { return clock })

	first, err := GetGitStatus("/repo")
	require.NoError(t, err)
	// Callers enrich the result; that must not leak into the cache.
	first.Modified[0] = "mutated"
	first.SourceBranch = "mutated"

	second, err := GetGitStatus("/repo")
	require.NoError(t, err)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
	require.Equal(t, []string{"a.go"}, second.Modified)
	require.Empty(t, second.SourceBranch)

	clock = clock.Add(gitStatusCacheTTL)
	_, err = GetGitStatus("/repo")
	require.NoError(t, err)
	require.Equal(t, int32(2), atomic.LoadInt32(&calls), "expired entry must be recomputed")

	_, err = GetGitStatusFresh("/repo")
	require.NoError(t, err)
	require.Equal(t, int32(3), atomic.LoadInt32(&calls), "fresh read must bypass the cache")

	InvalidateGitStatus("/repo")
	_, err = GetGitStatus("/repo")
	require.NoError(t, err)
	require.Equal(t, int32(4), atomic.LoadInt32(&calls), "invalidated entry must be recomputed")
}

func TestGitStatusCacheSingleFlight(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	swapGitStatusCompute(t, func(dir string) (*GitStatusData, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return &GitStatusData{Branch: "main"}, nil
	}, time.Now)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, err := GetGitStatus("/repo")
			require.NoError(t, err)
			require.Equal(t, "main", status.Branch)
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

func TestGitStatusCacheDoesNotKeepErrors(t *testing.T) {
	var calls int32
	swapGitStatusCompute(t, func(dir string) (*GitStatusData, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return nil, errors.New("boom")
		}
		return &GitStatusData{Branch: "main"}, nil
	}, time.Now)

	_, err := GetGitStatus("/repo")
	require.Error(t, err)
	status, err := GetGitStatus("/repo")
	require.NoError(t, err)
	require.Equal(t, "main", status.Branch)
}

func TestStatusHeaderBranch(t *testing.T) {
	for header, want := range map[string]string{
		"## main...origin/main [ahead 1]\n M a.go": "main",
		"## feature/x\n":              "feature/x",
		"## HEAD (no branch)\n":       "HEAD",
		"## No commits yet on main\n": "",
		"":                            "",
	} {
		require.Equal(t, want, statusHeaderBranch(header), header)
	}
}

func TestComputeGitStatusMatchesRepository(t *testing.T) {
	repo := setupTestRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new"), 0644))

	status, err := computeGitStatus(repo)
	require.NoError(t, err)
	require.Equal(t, GetGitBranch(repo), status.Branch)
	require.Contains(t, status.Modified, "README.md")
	require.Contains(t, status.Untracked, "new.txt")
	require.False(t, status.IsWorktree)

	_, err = computeGitStatus(t.TempDir())
	require.EqualError(t, err, "not a git repository")
}

func TestInspectSessionWorkspaceSingleCallAndUnbornBranch(t *testing.T) {
	repo := setupTestRepo(t)
	ws, err := inspectSessionWorkspace(repo)
	require.NoError(t, err)
	require.Equal(t, GetGitBranch(repo), ws.Branch)
	require.Empty(t, ws.WorktreePath)

	unborn := t.TempDir()
	cmd := exec.Command("git", "init", "-q", ".")
	cmd.Dir = unborn
	require.NoError(t, cmd.Run())
	ws, err = inspectSessionWorkspace(unborn)
	require.NoError(t, err, "a repository without commits still has a workspace")
	require.Empty(t, ws.Branch)
}

func writeJSONL(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644))
	return path
}

func usageLine(input int) string {
	return fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-6","content":[],"usage":{"input_tokens":%d,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`, input)
}

func TestParseLastUsageFromJSONLReadsFromTheEnd(t *testing.T) {
	big := strings.Repeat("x", 3*jsonlTailChunk+123)
	bigUser := `{"type":"user","message":{"role":"user","content":"` + big + `"}}`

	t.Run("usage line spans a chunk boundary", func(t *testing.T) {
		// Pad so the usage line straddles the boundary of the last chunk.
		pad := strings.Repeat("y", jsonlTailChunk-40)
		path := writeJSONL(t, usageLine(1), usageLine(4242), `{"type":"user","message":{"role":"user","content":"`+pad+`"}}`)
		usage, err := parseLastUsageFromJSONL(path)
		require.NoError(t, err)
		require.Equal(t, 4242, usage.InputTokens)
	})

	t.Run("huge trailing line without newline", func(t *testing.T) {
		path := writeJSONL(t, usageLine(1), usageLine(7), bigUser)
		usage, err := parseLastUsageFromJSONL(path)
		require.NoError(t, err)
		require.Equal(t, 7, usage.InputTokens)
	})

	t.Run("usage line larger than a chunk", func(t *testing.T) {
		line := strings.Replace(usageLine(99), `"content":[]`, `"content":[{"type":"text","text":"`+big+`"}]`, 1)
		path := writeJSONL(t, usageLine(1), line, `{"type":"user","message":{"role":"user","content":"hi"}}`, "")
		usage, err := parseLastUsageFromJSONL(path)
		require.NoError(t, err)
		require.Equal(t, 99, usage.InputTokens)
	})

	t.Run("first line of the file", func(t *testing.T) {
		path := writeJSONL(t, usageLine(5), bigUser, "not json", "")
		usage, err := parseLastUsageFromJSONL(path)
		require.NoError(t, err)
		require.Equal(t, 5, usage.InputTokens)
	})

	t.Run("no usage at all", func(t *testing.T) {
		path := writeJSONL(t, bigUser, bigUser)
		_, err := parseLastUsageFromJSONL(path)
		require.Error(t, err)
	})
}

func TestConversationJSONLPathIsCached(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(usageLine(11)+"\n"), 0644))

	var finds int32
	found := true
	old := jsonlPathFind
	jsonlPathFind = func(id, wd string) (string, error) {
		atomic.AddInt32(&finds, 1)
		if !found {
			return "", errors.New("missing")
		}
		return path, nil
	}
	t.Cleanup(func() { jsonlPathFind = old })

	id := "cache-" + uuid.NewString()
	for i := 0; i < 3; i++ {
		usage, err := GetContextUsageFromJSONL(id, "/wd")
		require.NoError(t, err)
		require.Equal(t, 11, usage.InputTokens)
	}
	require.Equal(t, int32(1), atomic.LoadInt32(&finds))

	// A vanished transcript drops the cached path and is looked up again.
	require.NoError(t, os.Remove(path))
	_, err := GetContextUsageFromJSONL(id, "/wd")
	require.Error(t, err)
	found = false
	_, err = GetContextUsageFromJSONL(id, "/wd")
	require.Error(t, err)
	require.Equal(t, int32(2), atomic.LoadInt32(&finds))
	// Misses are remembered too.
	_, err = GetContextUsageFromJSONL(id, "/wd")
	require.Error(t, err)
	require.Equal(t, int32(2), atomic.LoadInt32(&finds))
}

func TestGetContextUsageDoesNotStartClient(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	session := seedSwitchSession(t, sm, "claude", "sonnet", nil)

	usage, err := sm.GetContextUsage(session.ID)
	require.Nil(t, usage)
	require.ErrorIs(t, err, ErrNoActiveClient)
	require.Nil(t, session.GetClient(), "a read-only query must not spawn a CLI")
}

func TestObserveWorkspaceMessageRefreshSignals(t *testing.T) {
	repo := setupTestRepo(t)
	id := uuid.New()
	session := &AgentSession{Session: Session{ID: id, Options: SessionOptions{WorkingDirectory: &repo}}}
	sm := &SessionManager{sessions: map[uuid.UUID]*AgentSession{id: session}}

	result := func(toolID string, failed bool) *types.UserMessage {
		return &types.UserMessage{Content: []types.ContentBlock{&types.ToolResultBlock{Type: "tool_result", ToolUseID: toolID, IsError: &failed}}}
	}
	use := func(toolID, name, command string) *types.AssistantMessage {
		return &types.AssistantMessage{Content: []types.ContentBlock{&types.ToolUseBlock{ID: toolID, Name: name, Input: map[string]interface{}{"command": command}}}}
	}

	// Nothing pending: tool results are skipped without a refresh.
	require.False(t, sm.observeWorkspaceMessage(session, result("unknown", false)))

	// Non-shell tools are not tracked.
	sm.observeWorkspaceMessage(session, use("r1", "Read", ""))
	require.Empty(t, session.workspaceTools)

	// A plain shell command may switch branches in place: refresh.
	sm.observeWorkspaceMessage(session, use("b1", "Bash", "git checkout -b other"))
	require.True(t, sm.observeWorkspaceMessage(session, result("b1", false)))
	require.Empty(t, session.workspaceTools)

	// A failed shell command also refreshes, since it may have switched first.
	sm.observeWorkspaceMessage(session, use("b2", "mcp__eggify-tools__Bash", "git checkout main && false"))
	require.True(t, sm.observeWorkspaceMessage(session, result("b2", true)))

	// A command with an explicit directory is inspected directly.
	sm.observeWorkspaceMessage(session, use("b3", "Bash", "cd "+repo+" && git status"))
	require.False(t, sm.observeWorkspaceMessage(session, result("b3", false)))
	require.NotNil(t, session.Options.Workspace)

	// Plain-map content (reconstructed messages) is understood too.
	sm.observeWorkspaceMessage(session, use("b4", "Bash", "ls"))
	mapResult := &types.UserMessage{Content: []interface{}{map[string]interface{}{"type": "tool_result", "tool_use_id": "b4"}}}
	require.True(t, sm.observeWorkspaceMessage(session, mapResult))

	// Pending calls are dropped when they can no longer complete.
	sm.observeWorkspaceMessage(session, use("b5", "Bash", "sleep 100"))
	require.Len(t, session.workspaceTools, 1)
	sm.mu.Lock()
	session.clearWorkspaceTools()
	sm.mu.Unlock()
	require.Empty(t, session.workspaceTools)
}

func TestGitRefreshDue(t *testing.T) {
	session := &AgentSession{}
	require.True(t, gitRefreshDue(session))
	session.lastGitRefresh.Store(time.Now().UnixNano())
	require.False(t, gitRefreshDue(session))
	session.lastGitRefresh.Store(time.Now().Add(-gitBranchRefreshInterval).UnixNano())
	require.True(t, gitRefreshDue(session))
}

func TestPersistSessionStateWritesLatestState(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	session := seedSwitchSession(t, sm, "claude", "sonnet", nil)

	sm.mu.Lock()
	session.Status = SessionStatusProcessing
	session.UpdatedAt = time.Now()
	sm.mu.Unlock()
	sm.persistSessionState(session)

	meta, err := sm.Storage.GetSession(session.ID)
	require.NoError(t, err)
	require.Equal(t, string(SessionStatusProcessing), meta.Status)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sm.mu.Lock()
			if i%2 == 0 {
				session.Status = SessionStatusIdle
			} else {
				session.Status = SessionStatusProcessing
			}
			session.UpdatedAt = time.Now()
			sm.mu.Unlock()
			sm.persistSessionState(session)
		}(i)
	}
	wg.Wait()

	sm.mu.RLock()
	want := string(session.Status)
	sm.mu.RUnlock()
	meta, err = sm.Storage.GetSession(session.ID)
	require.NoError(t, err)
	require.Equal(t, want, meta.Status, "the database must end on the in-memory state")
}

func TestGitStatusQuickAnswersStaleWhileRefreshing(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	var clockMu sync.Mutex
	clock := time.Unix(1_000_000, 0)
	now := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	advance := func(d time.Duration) { clockMu.Lock(); clock = clock.Add(d); clockMu.Unlock() }
	swapGitStatusCompute(t, func(dir string) (*GitStatusData, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 2 {
			<-release // the background refresh is slow
		}
		return &GitStatusData{Branch: fmt.Sprintf("v%d", n)}, nil
	}, now)

	first, err := GetGitStatusQuick("/repo")
	require.NoError(t, err)
	require.Equal(t, "v1", first.Branch)

	advance(gitStatusCacheTTL)
	stale, err := GetGitStatusQuick("/repo")
	require.NoError(t, err)
	require.Equal(t, "v1", stale.Branch, "an expired entry is answered at once")
	close(release)
	require.Eventually(t, func() bool {
		s, ok := lookupGitStatus("/repo")
		return ok && s.Branch == "v2"
	}, 2*time.Second, 5*time.Millisecond, "refreshed in the background")

	advance(gitStatusStaleTTL)
	fresh, err := GetGitStatusQuick("/repo")
	require.NoError(t, err)
	require.Equal(t, "v3", fresh.Branch, "too old to serve: computed now")
}

func TestBranchFilesQuickCachesAndInvalidates(t *testing.T) {
	var calls int32
	clock := time.Unix(1_000_000, 0)
	swapGitStatusCompute(t, func(string) (*GitStatusData, error) { return &GitStatusData{}, nil }, func() time.Time { return clock })
	old := branchFilesCompute
	branchFilesCompute = func(dir, source string) ([]BranchFileChange, error) {
		atomic.AddInt32(&calls, 1)
		return []BranchFileChange{{Path: "a.go", Status: "modified"}}, nil
	}
	t.Cleanup(func() {
		branchFilesCompute = old
		branchFilesMu.Lock()
		branchFilesCache = map[string]branchFilesEntry{}
		branchFilesMu.Unlock()
	})

	files, err := GetBranchChangedFilesQuick("/repo", "main")
	require.NoError(t, err)
	files[0].Path = "mutated"
	again, err := GetBranchChangedFilesQuick("/repo", "main")
	require.NoError(t, err)
	require.Equal(t, "a.go", again[0].Path)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))

	InvalidateGitStatus("/repo")
	_, err = GetBranchChangedFilesQuick("/repo", "main")
	require.NoError(t, err)
	require.Equal(t, int32(2), atomic.LoadInt32(&calls), "invalidated with the git status")
}

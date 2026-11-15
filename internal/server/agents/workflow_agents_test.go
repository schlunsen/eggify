package agents

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The fixtures are a real Claude Code 2.1.288 run of a two-agent workflow:
// testdata/workflow_parent_stream.jsonl is the parent's stream-json (the
// Workflow call, its launch result and the task_* events; transcriptDir
// replaced by {{TRANSCRIPT_DIR}}), testdata/workflow_run the run's folder
// (journal, and the agents' transcripts without attachment lines).

const (
	wfAgentOne = "a352d514bd0ff5c77"
	wfAgentTwo = "a552081303c7f8da0"
)

func init() { workflowPollInterval = 10 * time.Millisecond }

// workflowRunDir makes an empty run folder laid out like the CLI's.
func workflowRunDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "session", "subagents", "workflows", "wf_f2cceca6-61c")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return dir
}

func fixtureLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	require.NoError(t, sc.Err())
	return lines
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer f.Close()
	for _, line := range lines {
		_, err := f.WriteString(line + "\n")
		require.NoError(t, err)
	}
}

func parentStream(t *testing.T, dir string) []string {
	t.Helper()
	lines := fixtureLines(t, "testdata/workflow_parent_stream.jsonl")
	for i := range lines {
		lines[i] = strings.ReplaceAll(lines[i], "{{TRANSCRIPT_DIR}}", dir)
	}
	return lines
}

// launchIndex is the position of the Workflow launch result in the stream.
func launchIndex(t *testing.T, lines []string) int {
	t.Helper()
	for i, line := range lines {
		if strings.Contains(line, `"taskType":"local_workflow"`) {
			return i
		}
	}
	t.Fatal("no workflow launch result in the stream")
	return -1
}

func (x *subagentHarness) waitStatus(t *testing.T, id string, status BackgroundAgentStatus) *BackgroundAgent {
	t.Helper()
	require.Eventually(t, func() bool {
		agent, ok := x.mgr.GetAgent(id)
		return ok && agent.Status == status
	}, 3*time.Second, 5*time.Millisecond, "agent %s never became %s", id, status)
	return x.agent(t, id)
}

func (x *subagentHarness) workflowWatching(taskID string) bool {
	tracker := x.h.workflows()
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	_, ok := tracker.byTask[taskID]
	return ok
}

func TestWorkflowAgents_RecordedRun(t *testing.T) {
	x := newSubagentHarness(t)
	dir := workflowRunDir(t)
	for _, name := range []string{"journal.jsonl", "agent-" + wfAgentOne + ".jsonl", "agent-" + wfAgentTwo + ".jsonl"} {
		appendLines(t, filepath.Join(dir, name), fixtureLines(t, filepath.Join("testdata/workflow_run", name))...)
	}
	stream := parentStream(t, dir)
	launch := launchIndex(t, stream)

	parent := x.feed(t, stream[:launch+1]...)
	// The Workflow call and its launch result stay in the parent chat.
	require.Len(t, parent, 2)

	for id, want := range map[string]string{wfAgentOne: "hello-one", wfAgentTwo: "hello-two"} {
		agent := x.waitStatus(t, id, BackgroundAgentStatusCompleted)
		require.Equal(t, x.session.ID, agent.ParentSessionID)
		require.Equal(t, workflowSubagentType, agent.SubagentType)
		require.Equal(t, want, agent.LastOutput)
		require.Equal(t, "Run the bash command 'echo "+want+"' and reply with its output only.", agent.Prompt)
		require.Contains(t, agent.RecentOutput, "TOOL:Bash: echo "+want)
		require.Equal(t, "claude-sonnet-5-5", agent.Model, "from the transcript's assistant lines")
		require.Equal(t, ModelSourceActual, agent.ModelSource)
	}
	require.Equal(t, "a:one", x.agent(t, wfAgentOne).Description)
	require.Equal(t, "a:two", x.agent(t, wfAgentTwo).Description)

	// The task's progress and end are taken out of the parent stream and
	// stop the watcher.
	require.Empty(t, x.feed(t, stream[launch+1:]...))
	require.Eventually(t, func() bool { return !x.workflowWatching("wxlrnjyfp") }, 3*time.Second, 5*time.Millisecond)
}

func TestWorkflowAgents_FollowsRunAsItGrows(t *testing.T) {
	x := newSubagentHarness(t)
	dir := workflowRunDir(t)
	journal := filepath.Join(dir, "journal.jsonl")
	recorded := fixtureLines(t, "testdata/workflow_run/journal.jsonl")
	transcriptOne := fixtureLines(t, "testdata/workflow_run/agent-"+wfAgentOne+".jsonl")
	stream := parentStream(t, dir)
	launch := launchIndex(t, stream)

	// Launched before anything is on disk.
	x.feed(t, stream[:launch+1]...)
	require.Eventually(t, func() bool { return x.workflowWatching("wxlrnjyfp") }, time.Second, 5*time.Millisecond)

	// Both start; agent one's task and tool call arrive, the last line half written.
	appendLines(t, journal, recorded[:3]...)
	pathOne := filepath.Join(dir, "agent-"+wfAgentOne+".jsonl")
	appendLines(t, pathOne, transcriptOne[:2]...)
	f, err := os.OpenFile(pathOne, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	half := len(transcriptOne[2]) / 2
	_, err = f.WriteString(transcriptOne[2][:half])
	require.NoError(t, err)

	one := x.waitStatus(t, wfAgentOne, BackgroundAgentStatusRunning)
	require.NotEmpty(t, one.Prompt)
	require.Eventually(t, func() bool {
		return strings.Contains(strings.Join(x.agent(t, wfAgentOne).RecentOutput, "\n"), "TOOL:Bash: echo hello-one")
	}, time.Second, 5*time.Millisecond)
	x.waitStatus(t, wfAgentTwo, BackgroundAgentStatusRunning)

	// The rest of the line, the reply, then its result.
	_, err = f.WriteString(transcriptOne[2][half:] + "\n" + transcriptOne[3] + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	appendLines(t, journal, recorded[3])
	one = x.waitStatus(t, wfAgentOne, BackgroundAgentStatusCompleted)
	require.Equal(t, "hello-one", one.LastOutput)
	require.Contains(t, one.RecentOutput, "hello-one")

	// The workflow ends while agent two never reported a result.
	x.feed(t, stream[launch+1:]...)
	two := x.waitStatus(t, wfAgentTwo, BackgroundAgentStatusFailed)
	require.True(t, two.Interrupted, two.ErrorMessage)
	require.Eventually(t, func() bool { return !x.workflowWatching("wxlrnjyfp") }, time.Second, 5*time.Millisecond)
	// Agent one keeps its result.
	require.Equal(t, BackgroundAgentStatusCompleted, x.agent(t, wfAgentOne).Status)
}

func TestWorkflowAgents_FailedEntry(t *testing.T) {
	x := newSubagentHarness(t)
	dir := workflowRunDir(t)
	x.h.watchWorkflow(x.session.ID, "task1", dir)
	appendLines(t, filepath.Join(dir, "journal.jsonl"),
		`{"type":"launched"}`,
		`{"type":"started","key":"k","agentId":"`+wfAgentOne+`","label":"build:x","phase":"Build"}`,
		`{"type":"failed","key":"k","agentId":"`+wfAgentOne+`"}`,
		`{"type":"failed","key":"k2","agentId":""}`,
		`{"type":"started","key":"k3","agentId":"../../etc","label":"evil"}`,
	)
	agent := x.waitStatus(t, wfAgentOne, BackgroundAgentStatusFailed)
	require.Equal(t, "build:x", agent.Description)
	require.False(t, agent.Interrupted)
	_, tracked := x.mgr.GetAgent("../../etc")
	require.False(t, tracked)
	require.True(t, x.h.finishWorkflow("task1", "completed"))
	require.Eventually(t, func() bool { return !x.workflowWatching("task1") }, time.Second, 5*time.Millisecond)
}

func TestWorkflowAgents_CLIClosedStopsWatcher(t *testing.T) {
	x := newSubagentHarness(t)
	dir := workflowRunDir(t)
	journal := filepath.Join(dir, "journal.jsonl")
	recorded := fixtureLines(t, "testdata/workflow_run/journal.jsonl")
	x.h.watchWorkflow(x.session.ID, "task1", dir)
	appendLines(t, journal, recorded[:2]...)
	x.waitStatus(t, wfAgentOne, BackgroundAgentStatusRunning)

	x.h.stopSessionSubagents(x.session.ID)
	require.False(t, x.workflowWatching("task1"))
	agent := x.agent(t, wfAgentOne)
	require.Equal(t, BackgroundAgentStatusFailed, agent.Status)
	require.True(t, agent.Interrupted)

	// A result written afterwards no longer reaches the stopped agent.
	appendLines(t, journal, recorded[3])
	time.Sleep(5 * workflowPollInterval)
	require.Equal(t, BackgroundAgentStatusFailed, x.agent(t, wfAgentOne).Status)
}

func TestWorkflowAgents_ResumedRunKeepsFinishedAgents(t *testing.T) {
	x := newSubagentHarness(t)
	dir := workflowRunDir(t)
	journal := filepath.Join(dir, "journal.jsonl")
	recorded := fixtureLines(t, "testdata/workflow_run/journal.jsonl")
	appendLines(t, journal, recorded...)
	appendLines(t, filepath.Join(dir, "agent-"+wfAgentOne+".jsonl"), fixtureLines(t, "testdata/workflow_run/agent-"+wfAgentOne+".jsonl")...)

	x.h.watchWorkflow(x.session.ID, "run1", dir)
	first := x.waitStatus(t, wfAgentOne, BackgroundAgentStatusCompleted)
	x.h.finishWorkflow("run1", "completed")
	require.Eventually(t, func() bool { return !x.workflowWatching("run1") }, time.Second, 5*time.Millisecond)

	// Resuming the run replays the same journal under a new task.
	x.h.watchWorkflow(x.session.ID, "run2", dir)
	time.Sleep(5 * workflowPollInterval)
	again := x.agent(t, wfAgentOne)
	require.Equal(t, BackgroundAgentStatusCompleted, again.Status)
	require.Equal(t, first.OutputLines, again.OutputLines, "transcript read twice")
	x.h.finishWorkflow("run2", "completed")
}

func TestIsWorkflowRunDir(t *testing.T) {
	for dir, want := range map[string]bool{
		"/Users/me/.claude/projects/p/s/subagents/workflows/wf_1578f74a-28b":  true,
		"/Users/me/.claude/projects/p/s/subagents/workflows/wf_1578f74a-28b/": false,
		"relative/subagents/workflows/wf_1":                                   false,
		"/x/subagents/workflows/../workflows/wf_1":                            false,
		"/x/workflows/wf_1":            false,
		"/x/subagents/workflows/notwf": false,
		"/":                            false,
		"":                             false,
	} {
		require.Equal(t, want, isWorkflowRunDir(dir), dir)
	}
}

func TestWorkflowTaskText(t *testing.T) {
	framed := "[Workflow harness — computed task] The task text below was computed at runtime. " +
		"The computed task text follows:\n  Build it.\n\n  Then:\n    - test it\n"
	require.Equal(t, "Build it.\n\nThen:\n  - test it", workflowTaskText(framed))
	require.Equal(t, "plain prompt", workflowTaskText("plain prompt"))
	require.Equal(t, "", workflowResultText(nil))
	require.Equal(t, "done", workflowResultText([]byte(`"done"`)))
	require.Equal(t, "{\n  \"ok\": true\n}", workflowResultText([]byte(`{"ok":true}`)))
}

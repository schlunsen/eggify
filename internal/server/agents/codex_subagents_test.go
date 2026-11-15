package agents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	codextypes "github.com/schlunsen/codex-sdk-go/types"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/stretchr/testify/require"
)

const (
	codexFixtureParent = "01a0ef19-78af-7071-8e69-0281e69f1858"
	codexFixtureChild  = "01a0ef19-a269-7c80-80c5-a3dd9df232d5"
	codexFixtureTurn   = "01a0ef19-7966-7011-9ada-6db543cdd725"
)

// codexSubagentRun replays an app-server notification stream through
// runCodexApp with the subagent tracker wired in, and returns what reached the
// parent chat.
type codexSubagentRun struct {
	sm         *SessionManager
	mu         sync.Mutex
	mgr        *BackgroundAgentManager
	session    *AgentSession
	parent     []types.Message
	broadcasts []interface{}
}

// newCodexSubagentSession creates a Codex session whose subagent traffic
// feeds a tracker, the way NewAgentHandler wires it.
func newCodexSubagentSession(t *testing.T, dir string) *codexSubagentRun {
	t.Helper()
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	sm, err := NewSessionManager(&Config{Model: "gpt-6-astra", MaxConcurrentSessions: 5}, db.GetDB())
	require.NoError(t, err)

	x := &codexSubagentRun{sm: sm, mgr: NewBackgroundAgentManager()}
	x.mgr.SetBroadcastCallback(func(_ uuid.UUID, msg interface{}) {
		x.mu.Lock()
		defer x.mu.Unlock()
		x.broadcasts = append(x.broadcasts, msg)
	})
	h := &AgentHandler{BackgroundAgentMgr: x.mgr}
	sm.SetSubagentCallback(h.handleSubagentTraffic)

	provider, model := CodexProviderID, "gpt-6-astra"
	sess, err := sm.CreateSession(uuid.New(), SessionOptions{Provider: &provider, Model: &model, WorkingDirectory: &dir})
	require.NoError(t, err)
	x.session, err = sm.GetSession(sess.ID)
	require.NoError(t, err)
	// Nothing reads the chat in this test; keep sends from waiting.
	x.session.responseChan = make(chan types.Message, 100)
	return x
}

func (x *codexSubagentRun) agent(t *testing.T, id string) *BackgroundAgent {
	t.Helper()
	agent, ok := x.mgr.GetAgent(id)
	require.True(t, ok, "agent %s not tracked", id)
	return agent
}

func runCodexSubagentFixture(t *testing.T, stream []string) *codexSubagentRun {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 needed for fake CLI")
	}
	dir := t.TempDir()
	streamPath := filepath.Join(dir, "stream.jsonl")
	require.NoError(t, os.WriteFile(streamPath, []byte(strings.Join(stream, "\n")+"\n"), 0600))
	script := `#!PYTHON
import json,sys,os
for line in sys.stdin:
 r=json.loads(line)
 if 'id' not in r: continue
 result={}
 if r['method'] in ('thread/start','thread/resume'): result={'thread':{'id':'PARENT'}}
 if r['method']=='turn/start': result={'turn':{'id':'TURN'}}
 print(json.dumps({'id':r['id'],'result':result}),flush=True)
 if r['method']=='turn/start':
  for l in open(os.environ['EGGIFY_FAKE_STREAM']):
   if l.strip(): print(l.strip(),flush=True)
`
	script = strings.NewReplacer("PYTHON", python, "PARENT", codexFixtureParent, "TURN", codexFixtureTurn).Replace(script)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("EGGIFY_FAKE_STREAM", streamPath)
	t.Setenv("EGGIFY_AGENT_ENV_ALLOW", "EGGIFY_FAKE_LOG,EGGIFY_FAKE_MODE,EGGIFY_FAKE_STREAM") // the fake codex reads these; agent children no longer inherit EGGIFY_*

	x := newCodexSubagentSession(t, dir)
	sm := x.sm

	ctx, cancel := context.WithTimeout(x.session.ctx, 20*time.Second)
	defer cancel()
	stop := make(chan struct{})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case m := <-x.session.responseChan:
				x.parent = append(x.parent, m)
			case <-stop:
				for {
					select {
					case m := <-x.session.responseChan:
						x.parent = append(x.parent, m)
					default:
						return
					}
				}
			}
		}
	}()
	err = sm.runCodexApp(x.session, &codexRun{ctx: ctx}, []codextypes.UserInput{codextypes.TextInput("spawn a child")}, 0)
	close(stop)
	<-drained
	require.NoError(t, err)
	return x
}

// parentTools lists the tool_use names and tool_result ids in the parent chat.
func (x *codexSubagentRun) parentTools() (uses []string, results map[string]*types.ToolResultBlock) {
	results = map[string]*types.ToolResultBlock{}
	for _, m := range x.parent {
		switch msg := m.(type) {
		case *types.AssistantMessage:
			for _, b := range msg.Content {
				if tool, ok := b.(*types.ToolUseBlock); ok {
					uses = append(uses, tool.Name)
				}
			}
		case *types.UserMessage:
			blocks, _ := msg.Content.([]types.ContentBlock)
			for _, b := range blocks {
				if r, ok := b.(*types.ToolResultBlock); ok {
					results[r.ToolUseID] = r
				}
			}
		}
	}
	return uses, results
}

func (x *codexSubagentRun) subagentMessages(agentID string) []SubagentMessageData {
	x.mu.Lock()
	defer x.mu.Unlock()
	var out []SubagentMessageData
	for _, b := range x.broadcasts {
		if m, ok := b.(SubagentMessageData); ok && m.AgentID == agentID {
			out = append(out, m)
		}
	}
	return out
}

// The fixture is a real codex-cli 0.155.1 app-server recording of a parent
// spawning one sub-agent that runs `ls` and `echo hello from child`.
func TestCodexSubagentStreamIsRoutedToTracker(t *testing.T) {
	x := runCodexSubagentFixture(t, readFixture(t, "codex_app_subagent_stream.jsonl"))
	agentID := codexToolID(codexFixtureChild)

	agent, ok := x.mgr.GetAgent(agentID)
	require.True(t, ok, "sub-agent not tracked")
	require.Equal(t, "shell_child", agent.SubagentType)
	require.Equal(t, BackgroundAgentStatusCompleted, agent.Status)
	require.Contains(t, agent.LastOutput, "hello from child")
	require.Len(t, x.mgr.GetAgentsForSession(x.session.ID), 1)

	// The child's own work reaches its agent, not the parent chat.
	var childTools []string
	for _, m := range x.subagentMessages(agentID) {
		if m.MessageType == "tool_use" {
			childTools = append(childTools, m.ToolName)
		}
	}
	require.Equal(t, []string{"Bash", "Bash"}, childTools)

	uses, results := x.parentTools()
	require.Equal(t, []string{"Agent", codexSubagentToolName}, uses)
	require.Contains(t, results, agentID)
	require.False(t, *results[agentID].IsError)
	require.Contains(t, toolResultText(results[agentID].Content), "hello from child")

	// Only the parent thread's token usage counts toward the session's context.
	require.Equal(t, codexFixtureParent, x.session.codexContextThread)
}

// A child still running when the parent's turn ends dies with the app-server,
// so it must not be left showing as running.
func TestCodexSubagentLeftRunningIsStopped(t *testing.T) {
	var stream []string
	for _, line := range readFixture(t, "codex_app_subagent_stream.jsonl") {
		if strings.Contains(line, `"threadId":"`+codexFixtureChild+`"`) ||
			strings.Contains(line, `"kind":"completed"`) {
			continue
		}
		stream = append(stream, line)
	}
	x := runCodexSubagentFixture(t, stream)
	agent, ok := x.mgr.GetAgent(codexToolID(codexFixtureChild))
	require.True(t, ok)
	require.Equal(t, BackgroundAgentStatusFailed, agent.Status)
	require.Contains(t, agent.ErrorMessage, "stopped")
}

func TestCodexCollabSpawnAndAgentStates(t *testing.T) {
	x := newCodexSubagentSession(t, t.TempDir())
	sub := newCodexSubagents(x.sm, x.session, "root")
	state := newCodexTurnState()
	sub.handleCollabItem("root", "item/completed", []byte(`{"type":"collabAgentToolCall","id":"c1","tool":"spawnAgent","status":"completed","senderThreadId":"root","receiverThreadIds":["kid"],"prompt":"Review the diff\nthoroughly","agentsStates":{"kid":{"status":"running"}}}`), state)
	agentID := codexToolID("kid")
	agent := x.agent(t, agentID)
	require.Equal(t, "Review the diff", agent.Description)
	require.Equal(t, BackgroundAgentStatusRunning, agent.Status)

	// A grandchild spawned by the child nests under it.
	sub.handleCollabItem("kid", "item/started", []byte(`{"type":"subAgentActivity","id":"c2","kind":"started","agentThreadId":"grandkid","agentPath":"/root/kid/helper"}`), newCodexTurnState())
	require.Equal(t, 1, x.mgr.Depth(codexToolID("grandkid")))
	require.Equal(t, "helper", x.agent(t, codexToolID("grandkid")).SubagentType)

	sub.childEmits("kid", []codexEmit{{Kind: codexEmitText, Text: "LGTM"}})
	sub.handleCollabItem("root", "item/completed", []byte(`{"type":"collabAgentToolCall","id":"c3","tool":"wait","status":"completed","senderThreadId":"root","receiverThreadIds":["kid"],"agentsStates":{"kid":{"status":"completed","message":"done"},"grandkid":{"status":"errored","message":"boom"}}}`), state)
	agent = x.agent(t, agentID)
	require.Equal(t, BackgroundAgentStatusCompleted, agent.Status)
	require.Equal(t, "LGTM", agent.LastOutput)
	grandkid := x.agent(t, codexToolID("grandkid"))
	require.Equal(t, BackgroundAgentStatusFailed, grandkid.Status)
	require.Contains(t, grandkid.ErrorMessage, "boom")
}

// The CLI's snake_case collab_tool_call (typed by codex-sdk-go v0.2.0 as
// CollabToolCallItem) is handled like the app-server's collabAgentToolCall.
func TestCodexCollabSnakeCaseItem(t *testing.T) {
	require.True(t, isCollabItem([]byte(`{"type":"collab_tool_call"}`)))

	x := newCodexSubagentSession(t, t.TempDir())
	sub := newCodexSubagents(x.sm, x.session, "root")
	state := newCodexTurnState()
	sub.handleCollabItem("root", "", []byte(`{"type":"collab_tool_call","id":"c1","tool":"spawn_agent","status":"completed","sender_thread_id":"root","receiver_thread_ids":["kid"],"prompt":"Check the tests","agents_states":{"kid":{"status":"running"}}}`), state)
	agent := x.agent(t, codexToolID("kid"))
	require.Equal(t, "Check the tests", agent.Description)
	require.Equal(t, BackgroundAgentStatusRunning, agent.Status)

	sub.handleCollabItem("root", "", []byte(`{"type":"collab_tool_call","id":"c2","tool":"wait","status":"completed","sender_thread_id":"root","receiver_thread_ids":["kid"],"agents_states":{"kid":{"status":"not_found","message":"gone"}}}`), state)
	agent = x.agent(t, codexToolID("kid"))
	require.Equal(t, BackgroundAgentStatusFailed, agent.Status)
	require.Contains(t, agent.ErrorMessage, "not found")
}

func TestParseCodexCollabCallBothShapes(t *testing.T) {
	camel, model, err := parseCodexCollabCall([]byte(`{"type":"collabAgentToolCall","id":"a","tool":"sendInput","status":"inProgress","senderThreadId":"root","receiverThreadIds":["k"],"model":"gpt-6-luna","agentsStates":{"k":{"status":"pendingInit"}}}`))
	require.NoError(t, err)
	require.Equal(t, codextypes.CollabToolSendInput, camel.Tool)
	require.Equal(t, codextypes.CollabToolCallInProgress, camel.Status)
	require.Equal(t, "root", camel.SenderThreadID)
	require.Equal(t, []string{"k"}, camel.ReceiverThreadIDs)
	require.Equal(t, codextypes.CollabAgentPendingInit, camel.AgentsStates["k"].Status)
	require.Equal(t, "gpt-6-luna", model)

	snake, _, err := parseCodexCollabCall([]byte(`{"type":"collab_tool_call","id":"b","tool":"close_agent","status":"failed","sender_thread_id":"root","receiver_thread_ids":["k"],"agents_states":{"k":{"status":"shutdown"}}}`))
	require.NoError(t, err)
	require.Equal(t, codextypes.CollabToolCloseAgent, snake.Tool)
	require.Equal(t, codextypes.CollabToolCallFailed, snake.Status)
	require.Equal(t, codextypes.CollabAgentShutdown, snake.AgentsStates["k"].Status)
}

// A finished Codex child the parent messages again (send_input/resume_agent)
// runs a new turn: it shows as running, and the end of that turn finishes it
// again without a second result on the parent's Agent card.
func TestCodexResumedChildFinishesAgain(t *testing.T) {
	x := newCodexSubagentSession(t, t.TempDir())
	sub := newCodexSubagents(x.sm, x.session, "root")
	sub.handleCollabItem("root", "item/completed", []byte(`{"type":"collabAgentToolCall","id":"c1","tool":"spawnAgent","status":"completed","senderThreadId":"root","receiverThreadIds":["kid"],"prompt":"Review","agentsStates":{"kid":{"status":"running"}}}`), newCodexTurnState())
	agentID := codexToolID("kid")
	sub.childEmits("kid", []codexEmit{{Kind: codexEmitText, Text: "first"}})
	sub.childTurnCompleted("kid", "completed", "")
	require.Equal(t, BackgroundAgentStatusCompleted, x.agent(t, agentID).Status)
	sentBefore := len(x.session.responseChan)

	sub.childEmits("kid", []codexEmit{{Kind: codexEmitText, Text: "second"}})
	require.Equal(t, BackgroundAgentStatusRunning, x.agent(t, agentID).Status)

	sub.childTurnCompleted("kid", "completed", "")
	agent := x.agent(t, agentID)
	require.Equal(t, BackgroundAgentStatusCompleted, agent.Status)
	require.Equal(t, "second", agent.LastOutput)
	require.Equal(t, sentBefore, len(x.session.responseChan), "no second tool result in the parent chat")
}

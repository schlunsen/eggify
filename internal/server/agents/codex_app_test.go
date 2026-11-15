package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	codextypes "github.com/schlunsen/codex-sdk-go/types"
	"github.com/schlunsen/eggify/internal/codexapp"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/stretchr/testify/require"
)

func TestCodexNetworkThreadStartAndResume(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 needed for fake CLI")
	}
	for _, resume := range []bool{false, true} {
		for _, network := range []bool{false, true} {
			t.Run(fmt.Sprintf("resume=%t/network=%t", resume, network), func(t *testing.T) {
				dir := t.TempDir()
				script := `#!PYTHON
import json,sys,os
for line in sys.stdin:
 r=json.loads(line)
 with open(os.environ['EGGIFY_FAKE_LOG'],'a') as f: f.write(line)
 if 'id' not in r: continue
 if r['method']=='turn/start':
  print(json.dumps({'id':r['id'],'error':{'code':-32600,'message':'probe complete'}}),flush=True)
 else:
  result={'thread':{'id':'thread-probe'}} if r['method'] in ('thread/start','thread/resume') else {}
  print(json.dumps({'id':r['id'],'result':result}),flush=True)
`
				require.NoError(t, os.WriteFile(filepath.Join(dir, "codex"), []byte(strings.Replace(script, "PYTHON", python, 1)), 0700))
				t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
				logPath := filepath.Join(dir, "requests.jsonl")
				t.Setenv("EGGIFY_FAKE_LOG", logPath)
				t.Setenv("EGGIFY_AGENT_ENV_ALLOW", "EGGIFY_FAKE_LOG,EGGIFY_FAKE_MODE,EGGIFY_FAKE_STREAM") // the fake codex reads these; agent children no longer inherit EGGIFY_*
				sm, cleanup := newModelSwitchTestManager(t)
				defer cleanup()
				s := seedSwitchSession(t, sm, "codex", "gpt-6-astra", func(s *Session) {
					s.Options.Sandboxed = boolp(true)
					s.Options.CodexNetworkAccess = network
					s.Options.WorkingDirectory = &dir
					if resume {
						s.Options.CodexThreadID = strp("old-thread")
					}
				})
				if network {
					s.WorktreePath = t.TempDir()
				}
				ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
				defer cancel()
				err := sm.runCodexApp(s, &codexRun{ctx: ctx}, []codextypes.UserInput{codextypes.TextInput("probe")}, 1)
				require.ErrorContains(t, err, "probe complete")
				data, err := os.ReadFile(logPath)
				require.NoError(t, err)
				found := false
				for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
					var r struct {
						Method string
						Params map[string]any
					}
					require.NoError(t, json.Unmarshal([]byte(line), &r))
					if r.Method != "thread/start" && r.Method != "thread/resume" {
						continue
					}
					found = true
					require.Equal(t, map[bool]string{false: "thread/start", true: "thread/resume"}[resume], r.Method)
					require.Equal(t, "never", r.Params["approvalPolicy"])
					require.Equal(t, codexSandboxProfile, r.Params["permissions"])
					require.Equal(t, codexWorkingDir(s), r.Params["cwd"])
					require.Equal(t, []any{codexWorkingDir(s)}, r.Params["runtimeWorkspaceRoots"])
					require.NotContains(t, r.Params, "sandbox")
					cfg := r.Params["config"].(map[string]any)
					profile := cfg["permissions."+codexSandboxProfile].(map[string]any)
					require.Equal(t, network, profile["network"].(map[string]any)["enabled"])
					require.Equal(t, ":workspace", profile["extends"])
					home, err := os.UserHomeDir()
					require.NoError(t, err)
					require.Equal(t, "deny", profile["filesystem"].(map[string]any)[filepath.Join(home, ".claude", "eggify", ".secret")])
				}
				require.True(t, found)
			})
		}
	}
}

func TestCodexRejectedStartKeepsHandoffForRetry(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 needed for fake CLI")
	}
	dir := t.TempDir()
	script := `#!PYTHON
import json,sys,os
for line in sys.stdin:
 r=json.loads(line)
 with open(os.environ['EGGIFY_FAKE_LOG'],'a') as f: f.write(line)
 if 'id' not in r: continue
 method=r['method']
 result={}
 if method in ('thread/start','thread/resume'): result={'thread':{'id':'thread-retry'}}
 if method=='turn/start':
  print(json.dumps({'id':r['id'],'error':{'code':-32600,'message':'rejected turn'}}),flush=True)
 else: print(json.dumps({'id':r['id'],'result':result}),flush=True)
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "codex"), []byte(strings.Replace(script, "PYTHON", python, 1)), 0700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "requests.jsonl")
	t.Setenv("EGGIFY_FAKE_LOG", logPath)
	t.Setenv("EGGIFY_AGENT_ENV_ALLOW", "EGGIFY_FAKE_LOG,EGGIFY_FAKE_MODE,EGGIFY_FAKE_STREAM") // the fake codex reads these; agent children no longer inherit EGGIFY_*
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	s := seedSwitchSession(t, sm, "claude", "opus", nil)
	seedMessages(t, sm, s, [2]string{"user", "remember this history"})
	change, err := sm.ChangeSessionModel(s.ID, "codex", "gpt-6-astra", nil)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		err := sm.runCodexApp(s, &codexRun{ctx: ctx}, []codextypes.UserInput{codextypes.TextInput("continue")}, change.Sequence+1)
		require.ErrorContains(t, err, "rejected turn")
		require.Empty(t, codexStr(s.Options.CodexThreadID))
		require.True(t, sm.historyReplayPending(s))
	}
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	starts := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var request struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &request))
		require.NotEqual(t, "thread/resume", request.Method)
		if request.Method == "turn/start" {
			starts++
			require.Contains(t, string(request.Params), "remember this history")
		}
	}
	require.Equal(t, 2, starts)
}

func TestCodexAppFollowUps(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 needed for fake CLI")
	}
	for _, mode := range []string{"steer", "completion-race", "startup"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			script := `#!PYTHON
import json,sys,os
count=0
mode=os.environ['EGGIFY_FAKE_MODE']
def emit(v):
 print(json.dumps(v),flush=True)
def done():
 emit({'method':'thread/tokenUsage/updated','params':{'threadId':'thread-1','tokenUsage':{'last':{'totalTokens':12345,'inputTokens':12000,'cachedInputTokens':8000,'outputTokens':345},'total':{'totalTokens':999999},'modelContextWindow':258400}}})
 emit({'method':'item/completed','params':{'threadId':'thread-1','turnId':'turn-1','item':{'type':'agentMessage','id':'answer','text':'follow-ups applied'}}})
 emit({'method':'turn/completed','params':{'threadId':'thread-1','turn':{'id':'turn-1','status':'completed'}}})
for line in sys.stdin:
 r=json.loads(line)
 with open(os.environ['EGGIFY_FAKE_LOG'],'a') as f: f.write(line)
 if 'id' not in r: continue
 method=r['method']
 result={}
 if method in ('thread/start','thread/resume'): result={'thread':{'id':'thread-1'}}
 if method=='turn/start': result={'turn':{'id':'turn-1'}}
 if method=='turn/steer' and mode=='completion-race':
  emit({'id':r['id'],'error':{'code':-32600,'message':'No active turn'}})
  continue
 emit({'id':r['id'],'result':result})
 if method=='turn/start':
  count+=1
  if mode=='completion-race' and count==2: done()
  elif count==1:
   emit({'method':'item/started','params':{'threadId':'thread-1','turnId':'turn-1','item':{'type':'commandExecution','id':'cmd','command':'echo waiting','status':'inProgress'}}})
 if method=='turn/steer':
  count+=1
  if count==3: done()
`
			script = strings.Replace(script, "PYTHON", python, 1)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0700))
			logPath := filepath.Join(dir, "requests.jsonl")
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("EGGIFY_FAKE_MODE", mode)
			t.Setenv("EGGIFY_FAKE_LOG", logPath)
			t.Setenv("EGGIFY_AGENT_ENV_ALLOW", "EGGIFY_FAKE_LOG,EGGIFY_FAKE_MODE,EGGIFY_FAKE_STREAM") // the fake codex reads these; agent children no longer inherit EGGIFY_*
			database.ResetInstance()
			db, err := database.Initialize(t.TempDir())
			require.NoError(t, err)
			defer db.Close()
			sm, err := NewSessionManager(&Config{Model: "gpt-6-astra", MaxConcurrentSessions: 5}, db.GetDB())
			require.NoError(t, err)
			provider, model := CodexProviderID, "gpt-6-astra"
			sess, err := sm.CreateSession(uuid.New(), SessionOptions{Provider: &provider, Model: &model, WorkingDirectory: &dir})
			require.NoError(t, err)
			agent, err := sm.GetSession(sess.ID)
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(agent.ctx, 10*time.Second)
			defer cancel()
			agent.ctx = ctx
			require.NoError(t, sm.SendPrompt(sess.ID, "initial task", nil))
			if mode != "startup" {
				select {
				case <-agent.responseChan:
				case <-ctx.Done():
					t.Fatal("initial turn never started")
				}
			}
			require.NoError(t, sm.SendPrompt(sess.ID, "first correction", nil))
			if mode != "completion-race" {
				require.NoError(t, sm.SendPrompt(sess.ID, "second correction", nil))
			}
			agent.codexTurnMu.Lock()
			run := agent.codexRun
			agent.codexTurnMu.Unlock()
			for {
				select {
				case m := <-agent.responseChan:
					if result, ok := m.(*types.ResultMessage); ok {
						require.False(t, result.IsError)
						goto finished
					}
				case <-ctx.Done():
					t.Fatal("follow-up did not complete active run")
				}
			}
		finished:
			select {
			case <-run.done:
			case <-ctx.Done():
				t.Fatal("run did not shut down")
			}
			usage, err := sm.GetContextUsage(sess.ID)
			require.NoError(t, err)
			require.Equal(t, 12345, usage.TotalTokens)
			require.Equal(t, 258400, usage.MaxTokens)
			data, err := os.ReadFile(logPath)
			require.NoError(t, err)
			var methods []string
			var corrections []string
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var r struct {
					Method string `json:"method"`
					Params struct {
						Expected string `json:"expectedTurnId"`
						Input    []struct {
							Text string `json:"text"`
						} `json:"input"`
					} `json:"params"`
				}
				require.NoError(t, json.Unmarshal([]byte(line), &r))
				methods = append(methods, r.Method)
				if r.Method == "turn/steer" {
					require.Equal(t, "turn-1", r.Params.Expected)
					corrections = append(corrections, r.Params.Input[0].Text)
				}
			}
			require.NotContains(t, methods, "account/login/start")
			require.Equal(t, "first correction", corrections[0])
			if mode != "completion-race" {
				require.Equal(t, []string{"first correction", "second correction"}, corrections)
			}
			require.Equal(t, SessionStatusIdle, agent.Status)
		})
	}
}

func TestCodexAppItems(t *testing.T) {
	state := newCodexTurnState()
	emits, err := state.handleAppItem("item/started", json.RawMessage(`{"type":"commandExecution","id":"cmd","command":"ls","status":"inProgress"}`))
	require.NoError(t, err)
	require.Len(t, emits, 1)
	id := emits[0].ToolID
	emits, err = state.handleAppItem("item/completed", json.RawMessage(`{"type":"commandExecution","id":"cmd","command":"ls","status":"completed","aggregatedOutput":"files","exitCode":0}`))
	require.NoError(t, err)
	require.Len(t, emits, 1)
	require.Equal(t, id, emits[0].ToolID)
	require.Equal(t, "files", emits[0].Text)
	emits, err = state.handleAppItem("item/completed", json.RawMessage(`{"type":"fileChange","id":"patch","status":"completed","changes":[{"path":"a.go","kind":{"type":"update"}}]}`))
	require.NoError(t, err)
	require.Equal(t, "update a.go", emits[1].Text)
	emits, err = state.handleAppItem("item/completed", json.RawMessage(`{"type":"reasoning","id":"r","summary":["thinking"]}`))
	require.NoError(t, err)
	require.Equal(t, "thinking", emits[0].Text)
	require.False(t, codexNoActiveTurn(context.DeadlineExceeded))
	require.False(t, codexNoActiveTurn(&codexapp.RPCError{Message: "invalid input"}))
}

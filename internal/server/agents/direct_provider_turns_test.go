package agents

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/llmclient"
	"github.com/schlunsen/eggify/internal/mcpclient"
)

// fakeOpenAI is an OpenAI-compatible streaming endpoint. The first request
// blocks until release is closed, so a test can send prompts mid-turn.
type fakeOpenAI struct {
	srv      *httptest.Server
	release  chan struct{}
	started  chan struct{}
	mu       sync.Mutex
	requests [][]llmclient.ChatMessage
	inFlight atomic.Int32
	maxIn    atomic.Int32
}

func newFakeOpenAI(t *testing.T) *fakeOpenAI {
	f := &fakeOpenAI{release: make(chan struct{}), started: make(chan struct{}, 8)}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := f.inFlight.Add(1)
		defer f.inFlight.Add(-1)
		for {
			m := f.maxIn.Load()
			if n <= m || f.maxIn.CompareAndSwap(m, n) {
				break
			}
		}
		var req llmclient.ChatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.requests = append(f.requests, req.Messages)
		first := len(f.requests) == 1
		f.mu.Unlock()
		f.started <- struct{}{}
		if first {
			<-f.release
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := map[string]interface{}{"choices": []map[string]interface{}{{
			"index": 0, "delta": map[string]interface{}{"content": fmt.Sprintf("reply %d", len(f.requests))},
		}}}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOpenAI) snapshot() [][]llmclient.ChatMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]llmclient.ChatMessage{}, f.requests...)
}

func lastUserText(msgs []llmclient.ChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			s, _ := msgs[i].Content.(string)
			return s
		}
	}
	return ""
}

func noDirectMCP(t *testing.T) {
	prev := directMCPClientFor
	directMCPClientFor = func(*SessionManager, *AgentSession) *mcpclient.Client { return nil }
	t.Cleanup(func() { directMCPClientFor = prev })
}

// What went wrong in a real session: a prompt sent while a direct turn was
// running started a second loop. Now it joins the running turn: one request
// at a time, and the follow-up request carries the new prompt.
func TestDirectTurn_PromptMidTurnJoinsRunningTurn(t *testing.T) {
	noDirectMCP(t)
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	fake := newFakeOpenAI(t)
	base, key := fake.srv.URL+"/v1", "test-key"
	s := seedSwitchSession(t, sm, "litellm", "qwen3.8", func(sess *Session) {
		sess.Options.BaseURL, sess.Options.APIKey = &base, &key
	})
	done := make(chan struct{})
	go func() { // stand in for the UI's streamer
		for {
			select {
			case <-s.responseChan:
			case <-done:
				return
			}
		}
	}()
	defer close(done)

	if err := sm.sendPromptDirect(s, s.ID, "first question", 0); err != nil {
		t.Fatal(err)
	}
	<-fake.started // the first model call is in flight
	if !s.directTurnRunning() {
		t.Fatal("turn must be marked running")
	}
	if err := sm.sendPromptDirect(s, s.ID, "also check this", 0); err != nil {
		t.Fatal(err)
	}
	close(fake.release)

	deadline := time.After(10 * time.Second)
	for s.directTurnRunning() {
		select {
		case <-deadline:
			t.Fatal("turn never finished")
		case <-time.After(10 * time.Millisecond):
		}
	}
	reqs := fake.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("want 2 model calls (turn + joined prompt), got %d", len(reqs))
	}
	if got := lastUserText(reqs[0]); got != "first question" {
		t.Fatalf("first call ends with %q", got)
	}
	if got := lastUserText(reqs[1]); got != "also check this" {
		t.Fatalf("second call must carry the joined prompt, ends with %q", got)
	}
	if fake.maxIn.Load() != 1 {
		t.Fatalf("model calls overlapped (%d at once): two loops ran", fake.maxIn.Load())
	}
	sm.mu.RLock()
	status := s.Status
	sm.mu.RUnlock()
	if status != SessionStatusIdle {
		t.Fatalf("status = %s after the turn, want idle", status)
	}
}

func TestDirectTurn_JoinStartFinish(t *testing.T) {
	s := &AgentSession{}
	if s.joinOrStartDirectTurn("a") {
		t.Fatal("first prompt starts a turn")
	}
	if !s.joinOrStartDirectTurn("b") || !s.joinOrStartDirectTurn("c") {
		t.Fatal("prompts during a turn join it")
	}
	if got := s.finishDirectTurn(false); strings.Join(got, ",") != "b,c" || !s.directTurnRunning() {
		t.Fatalf("waiting prompts keep the turn running, got %v", got)
	}
	if got := s.finishDirectTurn(false); got != nil || s.directTurnRunning() {
		t.Fatal("no waiting prompts ends the turn")
	}
	s.joinOrStartDirectTurn("x")
	s.joinOrStartDirectTurn("y")
	if got := s.finishDirectTurn(true); got != nil || s.directTurnRunning() {
		t.Fatal("a failed turn ends and drops waiting prompts")
	}
}

func TestDirectClear_HistoryStartsAfterMarker(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	s := seedSwitchSession(t, sm, "litellm", "qwen3.8", nil)
	seedMessages(t, sm, s, [2]string{"user", "old question"}, [2]string{"assistant", "old answer"})
	s.rememberDirectLoadedTools(map[string]bool{"gpu_launch": true})

	if !isDirectClear("  /clear \n") || isDirectClear("/clear all") {
		t.Fatal("only a bare /clear clears")
	}
	if err := sm.clearDirectContext(s, s.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.directLoadedToolNames()) != 0 {
		t.Fatal("/clear resets loaded tools")
	}
	s.MessageCount++ // seedMessages numbers from 1; continue after the marker instead
	if err := sm.saveMessageToDB(s.ID, s.MessageCount, "user", "new question", "", nil, nil); err != nil {
		t.Fatal(err)
	}

	history, err := sm.buildDirectConversationHistory(s.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Content != "new question" {
		t.Fatalf("history after /clear = %+v, want only the new question", history)
	}
	var stored int
	_ = sm.db.QueryRow(`SELECT COUNT(*) FROM agent_messages WHERE session_id = ? AND content = 'old answer'`, s.ID.String()).Scan(&stored)
	if stored != 1 {
		t.Fatal("/clear must keep the stored transcript")
	}
}

// A linked-session message (a loop's report) that arrives while a direct turn
// runs is queued; the end of that turn wakes the session to read it.
func TestDirectTurn_InboxDrainedWhenTurnEnds(t *testing.T) {
	noDirectMCP(t)
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	fake := newFakeOpenAI(t)
	base, key := fake.srv.URL+"/v1", "test-key"
	s := seedSwitchSession(t, sm, "litellm", "qwen3.8", func(sess *Session) {
		sess.Options.BaseURL, sess.Options.APIKey = &base, &key
	})
	woken := make(chan string, 4)
	sm.sendPromptHook = func(id uuid.UUID, prompt string, _ bool) error {
		if id == s.ID {
			woken <- prompt
		}
		return nil
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-s.responseChan:
			case <-done:
				return
			}
		}
	}()
	defer close(done)

	if err := sm.sendPromptDirect(s, s.ID, "first question", 0); err != nil {
		t.Fatal(err)
	}
	<-fake.started
	sm.mu.Lock() // as sendPromptInternal does before handing the prompt over
	s.Status = SessionStatusProcessing
	sm.mu.Unlock()
	sm.deliverToSession(s.ID, InboxItem{FromSessionID: uuid.NewString(), Kind: "report", Text: "loop finished"}, "📬 report")
	select {
	case p := <-woken:
		t.Fatalf("woken mid-turn with %q", p)
	case <-time.After(100 * time.Millisecond):
	}
	close(fake.release)

	select {
	case p := <-woken:
		if p != inboxWakePrompt {
			t.Fatalf("woken with %q", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the end of the turn did not deliver the queued report")
	}
}

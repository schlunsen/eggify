package agentruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schlunsen/eggify/internal/codexapp"
)

// fakeClaudeProc stands in for `claude auth login`: the test writes its
// output and reads what the manager sends to stdin.
type fakeClaudeProc struct {
	outR    *io.PipeReader
	outW    *io.PipeWriter
	inR     *io.PipeReader
	inW     *io.PipeWriter
	exit    chan error
	args    []string
	ctxDone <-chan struct{}
}

func newFakeClaudeProc(ctx context.Context, args []string) *fakeClaudeProc {
	p := &fakeClaudeProc{exit: make(chan error, 1), args: args, ctxDone: ctx.Done()}
	p.outR, p.outW = io.Pipe()
	p.inR, p.inW = io.Pipe()
	go func() { // like exec.CommandContext: cancel kills the process
		<-ctx.Done()
		p.finish(errors.New("signal: killed"))
	}()
	return p
}

func (p *fakeClaudeProc) Output() io.Reader     { return p.outR }
func (p *fakeClaudeProc) Stdin() io.WriteCloser { return p.inW }
func (p *fakeClaudeProc) Wait() error           { return <-p.exit }

var finishMu sync.Mutex

func (p *fakeClaudeProc) finish(err error) {
	finishMu.Lock()
	defer finishMu.Unlock()
	select {
	case p.exit <- err:
		_ = p.outW.Close()
	default:
	}
}

func waitFlow(t *testing.T, m *LoginManager, rt string, cond func(Flow) bool) Flow {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f, ok := m.Get(rt); ok && cond(f) {
			return f
		}
		time.Sleep(10 * time.Millisecond)
	}
	f, _ := m.Get(rt)
	t.Fatalf("flow never reached the condition: %+v", f)
	return f
}

func claudeTestManager(t *testing.T) (*LoginManager, chan *fakeClaudeProc) {
	d := newTestDetector(&fakeRunner{}, fakeEnv{onPath: map[string]string{"claude": "/c", "codex": "/x"}})
	m := NewLoginManager(d)
	procs := make(chan *fakeClaudeProc, 4)
	m.startClaude = func(ctx context.Context, path string, args []string) (claudeProc, error) {
		if path != "/c" {
			t.Errorf("path = %q", path)
		}
		p := newFakeClaudeProc(ctx, args)
		procs <- p
		return p, nil
	}
	t.Cleanup(m.Close)
	return m, procs
}

const claudeLoginOutput = "Opening browser to sign in…\nIf the browser didn't open, visit: \x1b]8;;https://claude.com/cai/oauth/authorize?code=true&client_id=abc&state=xyz\x1b\\https://claude.com/cai/oauth/authorize?code=true&client_id=abc&state=xyz\x1b]8;;\x1b\\\nPaste code here if prompted > "

func TestClaudeLoginCodeFlowSucceeds(t *testing.T) {
	m, procs := claudeTestManager(t)
	f, err := m.Start(Claude, "")
	if err != nil || f.State != FlowWaiting || !f.BrowserOpened {
		t.Fatalf("start: %+v %v", f, err)
	}
	p := <-procs
	if strings.Join(p.args, " ") != "auth login --claudeai" {
		t.Fatalf("args = %v", p.args)
	}
	if err := m.SubmitCode(Claude, "too-early"); !errors.Is(err, ErrNoCode) {
		t.Fatalf("code before the prompt: %v", err)
	}
	go func() { _, _ = io.WriteString(p.outW, claudeLoginOutput) }()
	f = waitFlow(t, m, Claude, func(f Flow) bool { return f.AcceptsCode })
	if f.AuthURL != "https://claude.com/cai/oauth/authorize?code=true&client_id=abc&state=xyz" {
		t.Fatalf("auth url = %q", f.AuthURL)
	}

	got := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(p.inR).ReadString('\n')
		got <- line
	}()
	if err := m.SubmitCode(Claude, "bad\ncode"); !errors.Is(err, ErrBadCode) {
		t.Fatalf("multi-line code must be refused: %v", err)
	}
	if err := m.SubmitCode(Claude, "  abc#def  "); err != nil {
		t.Fatal(err)
	}
	if line := <-got; line != "abc#def\n" {
		t.Fatalf("stdin got %q", line)
	}
	p.finish(nil)
	f = waitFlow(t, m, Claude, func(f Flow) bool { return f.State.Done() })
	if f.State != FlowSucceeded {
		t.Fatalf("%+v", f)
	}
}

func TestClaudeLoginFailureMessageHasNoURL(t *testing.T) {
	m, procs := claudeTestManager(t)
	if _, err := m.Start(Claude, ModeConsole); err != nil {
		t.Fatal(err)
	}
	p := <-procs
	if strings.Join(p.args, " ") != "auth login --console" {
		t.Fatalf("args = %v", p.args)
	}
	go func() {
		_, _ = io.WriteString(p.outW, claudeLoginOutput+"Login failed: Request failed with status code 400\n")
		p.finish(errors.New("exit status 1"))
	}()
	f := waitFlow(t, m, Claude, func(f Flow) bool { return f.State.Done() })
	if f.State != FlowFailed || f.Message != "Login failed: Request failed with status code 400" {
		t.Fatalf("%+v", f)
	}
}

func TestClaudeLoginCancelAndTimeout(t *testing.T) {
	m, procs := claudeTestManager(t)
	if _, err := m.Start(Claude, ""); err != nil {
		t.Fatal(err)
	}
	<-procs
	f, ok := m.Cancel(Claude)
	if !ok || f.State != FlowCancelled {
		t.Fatalf("cancel: %+v", f)
	}
	if err := m.SubmitCode(Claude, "x"); !errors.Is(err, ErrNoFlow) {
		t.Fatalf("code after cancel: %v", err)
	}

	m.Timeout = 50 * time.Millisecond
	if _, err := m.Start(Claude, ""); err != nil {
		t.Fatal(err)
	}
	<-procs
	f = waitFlow(t, m, Claude, func(f Flow) bool { return f.State.Done() })
	if f.State != FlowTimedOut {
		t.Fatalf("%+v", f)
	}
}

func TestLoginStartValidation(t *testing.T) {
	m, _ := claudeTestManager(t)
	if _, err := m.Start("gemini", ""); !errors.Is(err, ErrUnknownRuntime) {
		t.Fatal(err)
	}
	if _, err := m.Start(Claude, ModeDevice); !errors.Is(err, ErrBadMode) {
		t.Fatal(err)
	}
	if _, err := m.Start(Codex, ModeConsole); !errors.Is(err, ErrBadMode) {
		t.Fatal(err)
	}
	m.Detector = newTestDetector(&fakeRunner{}, fakeEnv{})
	if _, err := m.Start(Claude, ""); !errors.Is(err, ErrNotInstalled) {
		t.Fatal(err)
	}
}

// fakeCodexApp answers account/login/start and lets the test emit
// notifications.
type fakeCodexApp struct {
	mu     sync.Mutex
	calls  []string
	params []any
	events chan codexapp.Message
	closed chan struct{}
	once   sync.Once
}

func newFakeCodexApp() *fakeCodexApp {
	return &fakeCodexApp{events: make(chan codexapp.Message, 4), closed: make(chan struct{})}
}

func (a *fakeCodexApp) Call(_ context.Context, method string, params, result any) error {
	a.mu.Lock()
	a.calls = append(a.calls, method)
	a.params = append(a.params, params)
	a.mu.Unlock()
	if method == "account/login/start" && result != nil {
		typ := params.(map[string]any)["type"]
		var raw string
		if typ == "chatgptDeviceCode" {
			raw = `{"type":"chatgptDeviceCode","loginId":"L1","verificationUrl":"https://auth.openai.com/codex/device","userCode":"ABCD-1234"}`
		} else {
			raw = `{"type":"chatgpt","loginId":"L1","authUrl":"https://auth.openai.com/oauth/authorize?x=1"}`
		}
		return json.Unmarshal([]byte(raw), result)
	}
	return nil
}
func (a *fakeCodexApp) Notify(string, any) error        { return nil }
func (a *fakeCodexApp) Events() <-chan codexapp.Message { return a.events }
func (a *fakeCodexApp) Close()                          { a.once.Do(func() { close(a.closed) }) }
func (a *fakeCodexApp) methods() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.Join(a.calls, ",")
}

func codexTestManager(t *testing.T) (*LoginManager, chan *fakeCodexApp) {
	d := newTestDetector(&fakeRunner{}, fakeEnv{onPath: map[string]string{"codex": "/x"}})
	m := NewLoginManager(d)
	apps := make(chan *fakeCodexApp, 4)
	m.startCodex = func(ctx context.Context, path string) (codexApp, error) {
		a := newFakeCodexApp()
		apps <- a
		return a, nil
	}
	t.Cleanup(m.Close)
	return m, apps
}

func TestCodexLoginBrowserFlow(t *testing.T) {
	m, apps := codexTestManager(t)
	f, err := m.Start(Codex, "")
	if err != nil || f.State != FlowWaiting || f.AuthURL != "https://auth.openai.com/oauth/authorize?x=1" || f.BrowserOpened {
		t.Fatalf("%+v %v", f, err)
	}
	a := <-apps
	a.events <- codexapp.Message{Method: "account/login/completed", Params: json.RawMessage(`{"loginId":"other","success":true}`)}
	a.events <- codexapp.Message{Method: "account/login/completed", Params: json.RawMessage(`{"loginId":"L1","success":true}`)}
	f = waitFlow(t, m, Codex, func(f Flow) bool { return f.State.Done() })
	if f.State != FlowSucceeded {
		t.Fatalf("%+v", f)
	}
	<-a.closed
}

func TestCodexLoginDeviceFlowFailsAndCancel(t *testing.T) {
	m, apps := codexTestManager(t)
	f, err := m.Start(Codex, ModeDevice)
	if err != nil || f.UserCode != "ABCD-1234" || f.VerificationURL != "https://auth.openai.com/codex/device" {
		t.Fatalf("%+v %v", f, err)
	}
	a := <-apps
	a.events <- codexapp.Message{Method: "account/login/completed", Params: json.RawMessage(`{"loginId":"L1","success":false,"error":"Login was not completed"}`)}
	f = waitFlow(t, m, Codex, func(f Flow) bool { return f.State.Done() })
	if f.State != FlowFailed || f.Message != "Login failed: Login was not completed" {
		t.Fatalf("%+v", f)
	}

	if _, err := m.Start(Codex, ""); err != nil {
		t.Fatal(err)
	}
	a = <-apps
	f, _ = m.Cancel(Codex)
	if f.State != FlowCancelled {
		t.Fatalf("%+v", f)
	}
	<-a.closed
	if !strings.Contains(a.methods(), "account/login/cancel") {
		t.Fatalf("cancel must reach codex: %s", a.methods())
	}
}

func TestCodexAppExitFailsFlow(t *testing.T) {
	m, apps := codexTestManager(t)
	if _, err := m.Start(Codex, ""); err != nil {
		t.Fatal(err)
	}
	a := <-apps
	close(a.events)
	f := waitFlow(t, m, Codex, func(f Flow) bool { return f.State.Done() })
	if f.State != FlowFailed {
		t.Fatalf("%+v", f)
	}
}

func TestParseClaudeLoginOutput(t *testing.T) {
	u, wants := ParseClaudeLoginOutput("Opening browser…\n")
	if u != "" || wants {
		t.Fatal("nothing yet")
	}
	u, wants = ParseClaudeLoginOutput("\x1b[1mvisit:\x1b[0m https://example.com/a?b=c.\n")
	if u != "https://example.com/a?b=c" || wants {
		t.Fatalf("%q", u)
	}
}

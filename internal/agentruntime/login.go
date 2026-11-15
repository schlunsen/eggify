package agentruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Login flows run each CLI's own sign-in on behalf of a signed-in user. They
// are privileged (routes require principal.RequireUser) and have no MCP tool,
// so an agent cannot start one.
//
// Claude Code: `claude auth login [--console]` runs without a TTY. It opens
// the system browser itself (on the machine running Eggify) with a localhost
// callback, prints a fallback URL whose page shows a code, and reads that code
// from stdin ("Paste code here if prompted >"). The UI shows the fallback URL
// and a field that writes the code to the CLI's stdin. The CLI stores the
// credentials itself (keychain / ~/.claude); Eggify never sees them.
//
// Codex: `codex app-server` JSON-RPC account/login/start. type "chatgpt"
// returns an auth URL (callback on localhost:1455 of the Eggify machine; the
// UI opens it), type "chatgptDeviceCode" returns a verification URL and a user
// code (works when the browser is on another machine). Completion arrives as
// account/login/completed; cancel is account/login/cancel. Codex writes
// ~/.codex/auth.json itself.

// FlowState is the state of a login flow.
type FlowState string

const (
	FlowStarting  FlowState = "starting"
	FlowWaiting   FlowState = "waiting"
	FlowSucceeded FlowState = "succeeded"
	FlowFailed    FlowState = "failed"
	FlowCancelled FlowState = "cancelled"
	FlowTimedOut  FlowState = "timed_out"
)

// Done reports whether the flow has ended.
func (s FlowState) Done() bool {
	return s == FlowSucceeded || s == FlowFailed || s == FlowCancelled || s == FlowTimedOut
}

// Login modes.
const (
	ModeBrowser = "browser" // default: Claude subscription / ChatGPT in the browser
	ModeConsole = "console" // Claude: Anthropic Console (API billing)
	ModeDevice  = "device"  // Codex: device code
)

// Flow is the public state of a login flow.
type Flow struct {
	ID              string    `json:"id"`
	Runtime         string    `json:"runtime"`
	Mode            string    `json:"mode"`
	State           FlowState `json:"state"`
	AuthURL         string    `json:"auth_url,omitempty"`
	BrowserOpened   bool      `json:"browser_opened"` // the CLI opened the system browser itself
	AcceptsCode     bool      `json:"accepts_code"`   // POST …/login/code is accepted
	UserCode        string    `json:"user_code,omitempty"`
	VerificationURL string    `json:"verification_url,omitempty"`
	Message         string    `json:"message,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// Errors returned to the handler.
var (
	ErrUnknownRuntime = errors.New("unknown runtime")
	ErrNotInstalled   = errors.New("the CLI is not installed")
	ErrBadMode        = errors.New("unsupported login mode")
	ErrNoFlow         = errors.New("no login in progress")
	ErrNoCode         = errors.New("this login does not accept a code")
	ErrBadCode        = errors.New("invalid code")
)

// claudeProc is a running `claude auth login`.
type claudeProc interface {
	Output() io.Reader
	Stdin() io.WriteCloser
	Wait() error
}

type execClaudeProc struct {
	out   io.Reader
	stdin io.WriteCloser
	done  chan error
}

func (p *execClaudeProc) Output() io.Reader     { return p.out }
func (p *execClaudeProc) Stdin() io.WriteCloser { return p.stdin }
func (p *execClaudeProc) Wait() error           { return <-p.done }

func startClaudeLogin(ctx context.Context, path string, args []string) (claudeProc, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = cliEnv()
	cmd.Dir = homeDir()
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &execClaudeProc{out: pr, stdin: stdin, done: make(chan error, 1)}
	go func() {
		err := cmd.Wait()
		_ = pw.Close() // EOF for the reader once all output is copied
		p.done <- err
	}()
	return p, nil
}

type flowRun struct {
	flow     Flow
	cancel   context.CancelFunc
	stdin    io.WriteCloser
	cancelBy string // "user" when Cancel was called
}

// LoginManager runs at most one login flow per runtime.
type LoginManager struct {
	Detector *Detector
	Timeout  time.Duration

	startClaude func(ctx context.Context, path string, args []string) (claudeProc, error)
	startCodex  func(ctx context.Context, path string) (codexApp, error)

	mu    sync.Mutex
	flows map[string]*flowRun
}

// NewLoginManager returns a manager that finds the CLIs with d.
func NewLoginManager(d *Detector) *LoginManager {
	return &LoginManager{
		Detector:    d,
		Timeout:     10 * time.Minute,
		startClaude: startClaudeLogin,
		startCodex:  func(ctx context.Context, path string) (codexApp, error) { return startCodexApp(ctx, path) },
		flows:       map[string]*flowRun{},
	}
}

func newFlowID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Get returns the current (or last) flow of a runtime.
func (m *LoginManager) Get(runtimeID string) (Flow, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.flows[runtimeID]
	if r == nil {
		return Flow{}, false
	}
	return r.flow, true
}

// Start begins a login flow, cancelling a previous one of the same runtime.
func (m *LoginManager) Start(runtimeID, mode string) (Flow, error) {
	if !IsRuntime(runtimeID) {
		return Flow{}, ErrUnknownRuntime
	}
	if mode == "" {
		mode = ModeBrowser
	}
	switch {
	case runtimeID == Claude && (mode == ModeBrowser || mode == ModeConsole):
	case runtimeID == Codex && (mode == ModeBrowser || mode == ModeDevice):
	default:
		return Flow{}, ErrBadMode
	}
	path, _ := m.Detector.locate(runtimeID)
	if path == "" {
		return Flow{}, ErrNotInstalled
	}

	m.Cancel(runtimeID)

	now := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), m.Timeout)
	run := &flowRun{cancel: cancel, flow: Flow{
		ID: newFlowID(), Runtime: runtimeID, Mode: mode, State: FlowStarting,
		StartedAt: now, UpdatedAt: now, ExpiresAt: now.Add(m.Timeout),
	}}
	m.mu.Lock()
	m.flows[runtimeID] = run
	m.mu.Unlock()

	var err error
	if runtimeID == Claude {
		err = m.runClaude(ctx, run, path, mode)
	} else {
		err = m.runCodex(ctx, run, path, mode)
	}
	if err != nil {
		cancel()
		m.update(run, func(f *Flow) { f.State, f.Message = FlowFailed, "Could not start the login: "+shortErr(err) })
		f, _ := m.Get(runtimeID)
		return f, err
	}
	f, _ := m.Get(runtimeID)
	return f, nil
}

// Cancel stops the runtime's flow if it is still running.
func (m *LoginManager) Cancel(runtimeID string) (Flow, bool) {
	m.mu.Lock()
	r := m.flows[runtimeID]
	if r == nil {
		m.mu.Unlock()
		return Flow{}, false
	}
	if !r.flow.State.Done() {
		r.cancelBy = "user"
	}
	m.mu.Unlock()
	r.cancel()
	// Wait briefly so the caller sees the final state.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f, _ := m.Get(runtimeID); f.State.Done() {
			return f, true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return m.Get(runtimeID)
}

// SubmitCode sends the code shown on Claude's fallback page to the CLI.
func (m *LoginManager) SubmitCode(runtimeID, code string) error {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 4096 || strings.ContainsAny(code, "\r\n\x00") {
		return ErrBadCode
	}
	m.mu.Lock()
	r := m.flows[runtimeID]
	if r == nil || r.flow.State.Done() {
		m.mu.Unlock()
		return ErrNoFlow
	}
	if r.stdin == nil || !r.flow.AcceptsCode {
		m.mu.Unlock()
		return ErrNoCode
	}
	stdin := r.stdin
	m.mu.Unlock()
	if _, err := io.WriteString(stdin, code+"\n"); err != nil {
		return err
	}
	m.update(r, func(f *Flow) { f.Message = "Code sent, finishing sign-in…" })
	return nil
}

// Close cancels every running flow (server shutdown).
func (m *LoginManager) Close() {
	m.mu.Lock()
	runs := make([]*flowRun, 0, len(m.flows))
	for _, r := range m.flows {
		runs = append(runs, r)
	}
	m.mu.Unlock()
	for _, r := range runs {
		r.cancel()
	}
}

func (m *LoginManager) update(r *flowRun, fn func(*Flow)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.flow.State.Done() {
		return
	}
	fn(&r.flow)
	r.flow.UpdatedAt = time.Now()
}

// end records the final state of a flow from how its context ended.
func (m *LoginManager) end(ctx context.Context, r *flowRun, ok bool, failMsg string) {
	m.mu.Lock()
	by := r.cancelBy
	m.mu.Unlock()
	m.update(r, func(f *Flow) {
		switch {
		case ok:
			f.State, f.Message = FlowSucceeded, "Signed in."
		case by == "user":
			f.State, f.Message = FlowCancelled, "Login cancelled."
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			f.State, f.Message = FlowTimedOut, "The login timed out. Start it again when you are ready."
		default:
			f.State, f.Message = FlowFailed, failMsg
		}
	})
	r.cancel()
	if m.Detector != nil {
		m.Detector.Invalidate()
	}
}

// ---------- claude ----------

var (
	ansiRe     = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	osc8Re     = regexp.MustCompile(`\x1b\]8;[^;\x07\x1b]*;([^\x07\x1b]*)(?:\x07|\x1b\\)`)
	oscOtherRe = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	urlRe      = regexp.MustCompile(`https://[^\s\x1b\x07"'<>]+`)
)

// stripTerminal removes ANSI colour codes and OSC sequences, keeping the
// target of OSC 8 hyperlinks as text.
func stripTerminal(s string) string {
	s = osc8Re.ReplaceAllString(s, " $1 ")
	s = oscOtherRe.ReplaceAllString(s, "")
	return ansiRe.ReplaceAllString(s, "")
}

// ParseClaudeLoginOutput extracts the fallback sign-in URL and whether the
// CLI asks for a pasted code.
func ParseClaudeLoginOutput(raw string) (authURL string, wantsCode bool) {
	s := stripTerminal(raw)
	if u := urlRe.FindString(s); u != "" {
		authURL = strings.TrimRight(u, ".,)")
	}
	l := strings.ToLower(s)
	wantsCode = strings.Contains(l, "paste code") || strings.Contains(l, "paste the code") || strings.Contains(l, "enter code")
	return authURL, wantsCode
}

// lastLine returns the last non-empty output line without URLs, for errors.
func lastLine(raw string) string {
	lines := strings.Split(stripTerminal(raw), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(urlRe.ReplaceAllString(lines[i], "<url>"))
		if idx := strings.LastIndex(l, ">"); strings.HasPrefix(strings.ToLower(l), "paste code") && idx >= 0 {
			l = strings.TrimSpace(l[idx+1:])
		}
		if l != "" {
			if len(l) > 200 {
				l = l[:200] + "…"
			}
			return l
		}
	}
	return ""
}

func (m *LoginManager) runClaude(ctx context.Context, r *flowRun, path, mode string) error {
	args := []string{"auth", "login"}
	if mode == ModeConsole {
		args = append(args, "--console")
	} else {
		args = append(args, "--claudeai")
	}
	proc, err := m.startClaude(ctx, path, args)
	if err != nil {
		return err
	}
	m.mu.Lock()
	r.stdin = proc.Stdin()
	m.mu.Unlock()
	m.update(r, func(f *Flow) {
		f.State = FlowWaiting
		f.BrowserOpened = true
		f.Message = "Waiting for you to finish signing in in the browser…"
	})

	go func() {
		var buf strings.Builder
		chunk := make([]byte, 4096)
		out := proc.Output()
		for {
			n, rerr := out.Read(chunk)
			if n > 0 && buf.Len() < 64*1024 {
				buf.Write(chunk[:n])
				u, wants := ParseClaudeLoginOutput(buf.String())
				m.update(r, func(f *Flow) {
					if u != "" {
						f.AuthURL = u
					}
					if wants {
						f.AcceptsCode = true
					}
				})
			}
			if rerr != nil {
				break
			}
		}
		werr := proc.Wait()
		_ = proc.Stdin().Close()
		msg := lastLine(buf.String())
		if msg == "" && werr != nil {
			msg = "claude auth login exited: " + shortErr(werr)
		}
		m.end(ctx, r, werr == nil && ctx.Err() == nil, "Login failed: "+strings.TrimPrefix(msg, "Login failed: "))
	}()
	return nil
}

// ---------- codex ----------

type codexLoginStart struct {
	Type            string `json:"type"`
	LoginID         string `json:"loginId"`
	AuthURL         string `json:"authUrl"`
	VerificationURL string `json:"verificationUrl"`
	UserCode        string `json:"userCode"`
}

func (m *LoginManager) runCodex(ctx context.Context, r *flowRun, path, mode string) error {
	app, err := m.startCodex(ctx, path)
	if err != nil {
		return err
	}
	typ := "chatgpt"
	if mode == ModeDevice {
		typ = "chatgptDeviceCode"
	}
	var res codexLoginStart
	callCtx, cancelCall := context.WithTimeout(ctx, 30*time.Second)
	err = app.Call(callCtx, "account/login/start", map[string]any{"type": typ}, &res)
	cancelCall()
	if err != nil {
		app.Close()
		return err
	}
	if res.LoginID == "" {
		app.Close()
		return errors.New("codex app-server did not return a login id")
	}
	m.update(r, func(f *Flow) {
		f.State = FlowWaiting
		if strings.HasPrefix(res.AuthURL, "https://") {
			f.AuthURL = res.AuthURL
		}
		if strings.HasPrefix(res.VerificationURL, "https://") {
			f.VerificationURL = res.VerificationURL
		}
		f.UserCode = res.UserCode
		if mode == ModeDevice {
			f.Message = "Open the verification page and enter the code…"
		} else {
			f.Message = "Waiting for you to finish signing in in the browser…"
		}
	})

	go func() {
		defer app.Close()
		ok, failMsg := false, "The Codex login ended without signing in."
	loop:
		for {
			select {
			case msg, open := <-app.Events():
				if !open {
					failMsg = "codex app-server exited before the login finished."
					break loop
				}
				if msg.Method != "account/login/completed" {
					continue
				}
				c, parsed := parseCodexLoginCompleted(msg.Params)
				if !parsed || (c.LoginID != "" && c.LoginID != res.LoginID) {
					continue
				}
				ok = c.Success
				if !ok && c.Error != "" {
					failMsg = "Login failed: " + c.Error
				}
				break loop
			case <-ctx.Done():
				// Tell Codex to stop its callback server before we close it.
				cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = app.Call(cctx, "account/login/cancel", map[string]any{"loginId": res.LoginID}, nil)
				cancel()
				break loop
			}
		}
		m.end(ctx, r, ok, failMsg)
	}()
	return nil
}

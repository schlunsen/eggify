// Package agentruntime checks whether the agent CLIs Eggify drives (Claude
// Code via claude-agent-sdk-go, OpenAI Codex via codex-sdk-go) are installed,
// recent enough and logged in, and runs their own login flows on behalf of a
// signed-in user (login.go).
//
// Detection only runs read-only status commands:
//
//	claude --version          → "2.1.280 (Claude Code)"
//	claude auth status --json → {"loggedIn":true,"authMethod":"claude.ai","subscriptionType":"max",...}
//	codex --version           → "codex-cli 0.155.1"
//	codex login status        → "Logged in using ChatGPT" / "Logged in using an API key - sk-…" / "Not logged in"
//	codex app-server          → JSON-RPC account/read (plan type and e-mail, best effort)
//
// Nothing here prints or returns tokens or keys: e-mail addresses are masked,
// API-key suffixes printed by the CLIs are dropped.
package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	claude "github.com/schlunsen/claude-agent-sdk-go"
)

// Runtime ids.
const (
	Claude = "claude"
	Codex  = "codex"
)

// IsRuntime reports whether id names a supported runtime.
func IsRuntime(id string) bool { return id == Claude || id == Codex }

// AuthState is whether the CLI has usable credentials of its own.
type AuthState string

const (
	LoggedIn    AuthState = "logged_in"
	NotLoggedIn AuthState = "not_logged_in"
	Unknown     AuthState = "unknown"
)

// AuthMethod is how the runtime authenticates.
type AuthMethod string

const (
	MethodNone         AuthMethod = ""
	MethodSubscription AuthMethod = "subscription"   // Claude.ai / ChatGPT login
	MethodAPIKey       AuthMethod = "api_key"        // key the CLI itself holds (login --with-api-key, console, env)
	MethodProviderKey  AuthMethod = "provider_key"   // key in Eggify's Settings → Providers
	MethodCloud        AuthMethod = "cloud_provider" // Bedrock / Vertex / Foundry
	MethodOther        AuthMethod = "other"
)

// RecommendedClaudeVersion is the Claude Code version the newest models in
// the registry need (claude-fable-5-2 needs 2.1.321, claude-sonnet-5-5 2.1.284, claude-opus-5-5 2.1.280; older
// CLIs reject them with a 400).
const RecommendedClaudeVersion = "2.1.321"

// Auth describes a runtime's credentials. Account is a masked e-mail.
type Auth struct {
	State   AuthState  `json:"state"`
	Method  AuthMethod `json:"method,omitempty"`
	Detail  string     `json:"detail,omitempty"` // e.g. "claude.ai", "ChatGPT"
	Plan    string     `json:"plan,omitempty"`   // e.g. "max", "plus"
	Account string     `json:"account,omitempty"`
}

// Install tells the user how to install the CLI. Eggify never runs these.
type Install struct {
	Commands []string `json:"commands"`
	DocsURL  string   `json:"docs_url"`
}

// Step is an actionable next step for the onboarding UI.
type Step struct {
	Kind    string `json:"kind"` // install, update, login, add_to_path, provider_key
	Text    string `json:"text"`
	Command string `json:"command,omitempty"`
}

// Status is the state of one runtime.
type Status struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Installed          bool      `json:"installed"`
	Path               string    `json:"path,omitempty"`
	OnPath             bool      `json:"on_path"`
	Version            string    `json:"version,omitempty"`
	MinVersion         string    `json:"min_version,omitempty"`
	RecommendedVersion string    `json:"recommended_version,omitempty"`
	VersionOK          bool      `json:"version_ok"`
	MeetsRecommended   bool      `json:"meets_recommended"`
	Auth               Auth      `json:"auth"`
	ProviderKeys       []string  `json:"provider_keys,omitempty"`
	Ready              bool      `json:"ready"`
	LoginCommand       string    `json:"login_command"`
	Install            Install   `json:"install"`
	NextSteps          []Step    `json:"next_steps"`
	Error              string    `json:"error,omitempty"`
	CheckedAt          time.Time `json:"checked_at"`
}

// Runner runs a CLI and returns its combined output. Unit tests replace it;
// the default runs the real binary with a timeout.
type Runner interface {
	Run(ctx context.Context, path string, args ...string) (output []byte, exitCode int, err error)
}

// ExecRunner runs commands with os/exec.
type ExecRunner struct{}

// Run implements Runner. A non-zero exit is reported through exitCode, not err.
func (ExecRunner) Run(ctx context.Context, path string, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = cliEnv()
	cmd.Stdin = nil
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return out, ee.ExitCode(), nil
	}
	if err != nil {
		return out, -1, err
	}
	return out, 0, nil
}

// cliEnv is the environment the status and login commands run with: the
// server's own, minus variables that make a nested Claude Code behave as a
// child of another session. Claude Code looks its keychain item up by USER;
// a stripped environment without it reports "not logged in", so it is filled
// in from the OS when missing.
func cliEnv() []string {
	env := os.Environ()
	out := env[:0:0]
	hasUser := false
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT":
			continue
		case "USER":
			hasUser = true
		}
		out = append(out, kv)
	}
	if !hasUser {
		if u, err := user.Current(); err == nil && u.Username != "" {
			out = append(out, "USER="+u.Username)
		}
	}
	return out
}

// CodexAccount is what codex app-server's account/read reports.
type CodexAccount struct {
	Type     string `json:"type"`
	Email    string `json:"email"`
	PlanType string `json:"planType"`
}

// KeyLookup reports which of the given provider ids have an API key stored in
// Eggify (Settings → Providers).
type KeyLookup func(providerIDs ...string) []string

// Detector checks the runtimes. The zero value is not usable; use NewDetector.
type Detector struct {
	Runner Runner
	// LookPath and IsFile are os/exec.LookPath and a regular-file stat.
	LookPath func(string) (string, error)
	IsFile   func(string) bool
	Glob     func(string) ([]string, error)
	Home     func() string
	Getenv   func(string) string
	// Keys reports stored provider keys; nil means none.
	Keys KeyLookup
	// CodexAccount reads account details over codex app-server (best effort);
	// nil skips it.
	CodexAccount func(ctx context.Context, path string) (*CodexAccount, error)
	// Timeout bounds each CLI call.
	Timeout time.Duration
	// TTL is how long a result is cached.
	TTL time.Duration

	mu       sync.Mutex
	cached   []Status
	cachedAt time.Time
	inflight chan struct{}

	// Codex plan usage (codex_usage.go), cached.
	usageMu    sync.Mutex
	usage      *CodexUsage
	usageAt    time.Time
	usageErrAt time.Time
}

// NewDetector returns a Detector for the real environment.
func NewDetector(keys KeyLookup) *Detector {
	return &Detector{
		Runner:       ExecRunner{},
		LookPath:     exec.LookPath,
		IsFile:       isFile,
		Glob:         filepath.Glob,
		Home:         homeDir,
		Getenv:       os.Getenv,
		Keys:         keys,
		CodexAccount: readCodexAccount,
		Timeout:      8 * time.Second,
		TTL:          20 * time.Second,
	}
}

func isFile(p string) bool {
	info, err := os.Stat(p) //nolint:gosec // statting candidate install paths is the point
	return err == nil && !info.IsDir()
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

// Invalidate drops the cached result (after a login flow ends).
func (d *Detector) Invalidate() {
	d.mu.Lock()
	d.cachedAt = time.Time{}
	d.mu.Unlock()
}

// Check returns the status of both runtimes, from cache when fresh. Concurrent
// callers share one check.
func (d *Detector) Check(ctx context.Context, refresh bool) []Status {
	for {
		d.mu.Lock()
		if !refresh && d.cached != nil && time.Since(d.cachedAt) < d.TTL {
			out := append([]Status(nil), d.cached...)
			d.mu.Unlock()
			return out
		}
		if d.inflight != nil {
			wait := d.inflight
			d.mu.Unlock()
			select {
			case <-wait:
				refresh = false // use what the other caller just computed
				continue
			case <-ctx.Done():
				return d.lastOrEmpty()
			}
		}
		done := make(chan struct{})
		d.inflight = done
		d.mu.Unlock()

		// Detached from the request: a client that goes away must not leave a
		// half-finished result, and the CLI calls have their own timeouts.
		res := d.checkAll(context.Background())

		d.mu.Lock()
		d.cached, d.cachedAt, d.inflight = res, time.Now(), nil
		d.mu.Unlock()
		close(done)
		return append([]Status(nil), res...)
	}
}

func (d *Detector) lastOrEmpty() []Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Status(nil), d.cached...)
}

func (d *Detector) checkAll(ctx context.Context) []Status {
	out := make([]Status, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); out[0] = d.CheckClaude(ctx) }()
	go func() { defer wg.Done(); out[1] = d.CheckCodex(ctx) }()
	wg.Wait()
	return out
}

func (d *Detector) run(ctx context.Context, path string, args ...string) ([]byte, int, error) {
	t := d.Timeout
	if t <= 0 {
		t = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	return d.Runner.Run(ctx, path, args...)
}

func (d *Detector) keys(ids ...string) []string {
	if d.Keys == nil {
		return nil
	}
	return d.Keys(ids...)
}

// ---------- discovery ----------

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// candidates lists install locations to try after PATH. The first ones are
// what the SDKs themselves search (so a CLI found there also works for
// sessions); the rest cover GUI-launched apps whose PATH lacks the user's
// shell setup (Homebrew, nvm, volta, bun, the standalone installers).
func (d *Detector) candidates(name string) (sdk []string, extra []string) {
	home := d.Home()
	j := func(p ...string) string { return filepath.Join(append([]string{home}, p...)...) }
	n := exeName(name)
	switch name {
	case Claude:
		// claude-agent-sdk-go transport.FindCLI
		sdk = []string{j(".claude", "local", n), j(".npm-global", "bin", n), "/usr/local/bin/" + n,
			j(".local", "bin", n), j("node_modules", ".bin", n), j(".yarn", "bin", n)}
	case Codex:
		// codex-sdk-go transport.FindCLI
		sdk = []string{j(".npm-global", "bin", n), "/opt/homebrew/bin/" + n, "/usr/local/bin/" + n,
			j(".local", "bin", n), j(".bun", "bin", n), j(".cargo", "bin", n), j("node_modules", ".bin", n), j(".yarn", "bin", n)}
	}
	extra = []string{"/opt/homebrew/bin/" + n, "/usr/local/bin/" + n, j(".local", "bin", n), j(".bun", "bin", n),
		j(".volta", "bin", n), j(".npm-global", "bin", n), j("Library", "pnpm", n), j(".local", "share", "pnpm", n)}
	if name == Codex {
		extra = append(extra, j(".codex", "packages", "standalone", "current", "bin", n))
	}
	// nvm: newest node version first.
	if d.Glob != nil {
		if m, _ := d.Glob(j(".nvm", "versions", "node", "*", "bin", n)); len(m) > 0 {
			sort.Sort(sort.Reverse(sort.StringSlice(m)))
			extra = append(extra, m...)
		}
	}
	return sdk, extra
}

// locate finds a CLI. onPath is true when PATH (or an SDK search location)
// resolves it, i.e. when sessions will find the same binary.
func (d *Detector) locate(name string) (path string, onPath bool) {
	if name == Codex && d.Getenv != nil {
		if p := d.Getenv("CODEX_PATH"); p != "" && d.IsFile(p) {
			return p, true
		}
	}
	if p, err := d.LookPath(exeName(name)); err == nil && p != "" {
		return p, true
	}
	sdk, extra := d.candidates(name)
	for _, p := range sdk {
		if d.IsFile(p) {
			return p, true
		}
	}
	for _, p := range extra {
		if d.IsFile(p) {
			return p, false
		}
	}
	return "", false
}

// ---------- versions ----------

var semverRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ParseVersion extracts the first x.y.z from CLI output ("2.1.280 (Claude
// Code)", "codex-cli 0.155.1").
func ParseVersion(out string) string {
	return semverRe.FindString(out)
}

// CompareVersions compares x.y.z strings: -1, 0 or 1. Unparsable sorts lowest.
func CompareVersions(a, b string) int {
	pa, pb := semverRe.FindStringSubmatch(a), semverRe.FindStringSubmatch(b)
	if pa == nil || pb == nil {
		switch {
		case pa == nil && pb == nil:
			return 0
		case pa == nil:
			return -1
		default:
			return 1
		}
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// ---------- claude ----------

// claudeAuthJSON is the subset of `claude auth status --json` we read. orgName
// and orgId are deliberately not read (orgName usually contains the e-mail).
type claudeAuthJSON struct {
	LoggedIn         *bool  `json:"loggedIn"`
	AuthMethod       string `json:"authMethod"`
	APIProvider      string `json:"apiProvider"`
	Email            string `json:"email"`
	SubscriptionType string `json:"subscriptionType"`
}

// ParseClaudeAuthStatus interprets `claude auth status --json` output.
func ParseClaudeAuthStatus(out []byte) (Auth, bool) {
	s := strings.TrimSpace(string(out))
	if i := strings.Index(s, "{"); i >= 0 {
		s = s[i:]
	}
	var st claudeAuthJSON
	if err := json.Unmarshal([]byte(s), &st); err != nil || st.LoggedIn == nil {
		return Auth{State: Unknown}, false
	}
	if !*st.LoggedIn {
		return Auth{State: NotLoggedIn}, true
	}
	a := Auth{State: LoggedIn, Detail: st.AuthMethod, Plan: st.SubscriptionType, Account: MaskEmail(st.Email)}
	m := strings.ToLower(st.AuthMethod)
	switch {
	case st.APIProvider != "" && st.APIProvider != "firstParty":
		a.Method, a.Detail = MethodCloud, st.APIProvider
	case m == "claude.ai" || strings.Contains(m, "oauth") || strings.Contains(m, "subscription"):
		a.Method = MethodSubscription
	case strings.Contains(m, "key") || m == "console":
		a.Method = MethodAPIKey
	default:
		a.Method = MethodOther
	}
	return a, true
}

// claudeProviderIDs are the provider rows whose keys let the Claude CLI run
// (the Anthropic-compatible providers go through the same CLI).
var claudeProviderIDs = []string{"claude", "anthropic", "deepseek", "glm", "kimi"}

// CheckClaude checks Claude Code.
func (d *Detector) CheckClaude(ctx context.Context) Status {
	st := Status{
		ID: Claude, Name: "Claude Code",
		MinVersion: claude.MinimumCLIVersion, RecommendedVersion: RecommendedClaudeVersion,
		LoginCommand: "claude auth login",
		Install: Install{
			Commands: []string{"npm install -g @anthropic-ai/claude-code", "brew install --cask claude-code"},
			DocsURL:  "https://docs.claude.com/en/docs/claude-code/setup",
		},
		Auth:      Auth{State: Unknown},
		CheckedAt: time.Now(),
	}
	st.ProviderKeys = d.keys(claudeProviderIDs...)
	if d.Getenv != nil && (d.Getenv("ANTHROPIC_API_KEY") != "" || d.Getenv("ANTHROPIC_AUTH_TOKEN") != "") {
		st.ProviderKeys = appendUnique(st.ProviderKeys, "env")
	}

	st.Path, st.OnPath = d.locate(Claude)
	if st.Path == "" {
		finish(&st)
		return st
	}
	st.Installed = true

	if out, code, err := d.run(ctx, st.Path, "--version"); err == nil && code == 0 {
		st.Version = ParseVersion(string(out))
	}
	if st.Version != "" {
		st.VersionOK = CompareVersions(st.Version, st.MinVersion) >= 0
		st.MeetsRecommended = CompareVersions(st.Version, st.RecommendedVersion) >= 0
	} else {
		st.VersionOK = true // can't tell; let the SDK decide (it does the same)
	}

	out, _, err := d.run(ctx, st.Path, "auth", "status", "--json")
	switch {
	case err != nil:
		st.Error = "could not run `claude auth status`: " + shortErr(err)
	default:
		if a, ok := ParseClaudeAuthStatus(out); ok {
			st.Auth = a
		} else {
			st.Error = "`claude auth status` is not supported by this Claude Code version; update it, or run `claude` and type /login"
		}
	}
	finish(&st)
	return st
}

// ---------- codex ----------

// ParseCodexLoginStatus interprets `codex login status` output. The API-key
// suffix the CLI prints is never kept.
func ParseCodexLoginStatus(out []byte, exitCode int) Auth {
	for _, line := range strings.Split(string(out), "\n") {
		l := strings.ToLower(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(l, "not logged in"):
			return Auth{State: NotLoggedIn}
		case strings.HasPrefix(l, "logged in using chatgpt"):
			return Auth{State: LoggedIn, Method: MethodSubscription, Detail: "ChatGPT"}
		case strings.HasPrefix(l, "logged in using an api key"), strings.HasPrefix(l, "logged in using api key"):
			return Auth{State: LoggedIn, Method: MethodAPIKey, Detail: "API key"}
		case strings.HasPrefix(l, "logged in using an access token"):
			return Auth{State: LoggedIn, Method: MethodOther, Detail: "access token"}
		case strings.HasPrefix(l, "logged in"):
			return Auth{State: LoggedIn, Method: MethodOther}
		}
	}
	if exitCode != 0 {
		return Auth{State: NotLoggedIn}
	}
	return Auth{State: Unknown}
}

// CheckCodex checks the OpenAI Codex CLI.
func (d *Detector) CheckCodex(ctx context.Context) Status {
	st := Status{
		ID: Codex, Name: "OpenAI Codex",
		LoginCommand: "codex login",
		Install: Install{
			Commands: []string{"npm install -g @openai/codex", "brew install codex"},
			DocsURL:  "https://developers.openai.com/codex/cli",
		},
		Auth:      Auth{State: Unknown},
		CheckedAt: time.Now(),
		VersionOK: true,
	}
	st.ProviderKeys = d.keys("codex", "openai")
	if d.Getenv != nil && (d.Getenv("CODEX_API_KEY") != "" || d.Getenv("OPENAI_API_KEY") != "") {
		st.ProviderKeys = appendUnique(st.ProviderKeys, "env")
	}

	st.Path, st.OnPath = d.locate(Codex)
	if st.Path == "" {
		st.VersionOK = false
		finish(&st)
		return st
	}
	st.Installed = true
	if out, code, err := d.run(ctx, st.Path, "--version"); err == nil && code == 0 {
		st.Version = ParseVersion(string(out))
	}
	st.MeetsRecommended = true

	out, code, err := d.run(ctx, st.Path, "login", "status")
	if err != nil {
		st.Error = "could not run `codex login status`: " + shortErr(err)
	} else {
		st.Auth = ParseCodexLoginStatus(out, code)
	}
	if st.Auth.State == LoggedIn && st.Auth.Method == MethodSubscription && d.CodexAccount != nil {
		actx, cancel := context.WithTimeout(ctx, d.Timeout)
		if acct, err := d.CodexAccount(actx, st.Path); err == nil && acct != nil {
			st.Auth.Plan = acct.PlanType
			st.Auth.Account = MaskEmail(acct.Email)
		}
		cancel()
	}
	finish(&st)
	return st
}

// ---------- shared ----------

// finish computes Ready and the next steps.
func finish(st *Status) {
	hasKey := len(st.ProviderKeys) > 0
	if st.Auth.State != LoggedIn && hasKey && st.Installed {
		st.Auth.Method = MethodProviderKey
	}
	st.Ready = st.Installed && st.VersionOK && (st.Auth.State == LoggedIn || hasKey)
	st.NextSteps = nil
	if !st.Installed {
		st.NextSteps = append(st.NextSteps, Step{Kind: "install", Text: "Install " + st.Name, Command: st.Install.Commands[0]})
		return
	}
	if !st.OnPath {
		st.NextSteps = append(st.NextSteps, Step{Kind: "add_to_path",
			Text: fmt.Sprintf("Found %s at %s, but it is not on the PATH Eggify starts agents with. Add %s to your PATH (e.g. in ~/.zshrc) and restart Eggify.", st.Name, st.Path, filepath.Dir(st.Path))})
	}
	if !st.VersionOK {
		st.NextSteps = append(st.NextSteps, Step{Kind: "update",
			Text: fmt.Sprintf("%s %s is too old; %s or newer is required.", st.Name, st.Version, st.MinVersion), Command: st.Install.Commands[0] + "@latest"})
	} else if st.ID == Claude && st.Version != "" && !st.MeetsRecommended {
		st.NextSteps = append(st.NextSteps, Step{Kind: "update",
			Text: fmt.Sprintf("Update Claude Code to %s or newer to use the newest models.", st.RecommendedVersion), Command: st.Install.Commands[0] + "@latest"})
	}
	if st.Auth.State != LoggedIn && !hasKey {
		st.NextSteps = append(st.NextSteps,
			Step{Kind: "login", Text: "Log in with your " + map[string]string{Claude: "Claude", Codex: "ChatGPT"}[st.ID] + " account", Command: st.LoginCommand},
			Step{Kind: "provider_key", Text: "Or add an API key in Settings → Providers"})
	}
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

// MaskEmail keeps the first two characters of the local part and the domain:
// "dave@example.com" → "da•••@example.com". Anything else returns "".
func MaskEmail(email string) string {
	local, domain, ok := strings.Cut(strings.TrimSpace(email), "@")
	if !ok || local == "" || domain == "" || strings.ContainsAny(email, " \t\n") {
		return ""
	}
	keep := 2
	if len([]rune(local)) <= 2 {
		keep = 1
	}
	r := []rune(local)
	return string(r[:keep]) + "•••@" + domain
}

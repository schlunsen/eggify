package agentruntime

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRunner answers "<path> <args...>" with canned output. Unit tests never
// run a real CLI.
type fakeRunner struct {
	out   map[string]string
	codes map[string]int
	errs  map[string]error
	calls atomic.Int32
}

func (f *fakeRunner) Run(_ context.Context, path string, args ...string) ([]byte, int, error) {
	f.calls.Add(1)
	key := path + " " + strings.Join(args, " ")
	if err := f.errs[key]; err != nil {
		return nil, -1, err
	}
	out, ok := f.out[key]
	if !ok {
		return []byte("error: unknown command"), 1, nil
	}
	return []byte(out), f.codes[key], nil
}

type fakeEnv struct {
	onPath map[string]string // name → path
	files  map[string]bool
	env    map[string]string
	keys   []string
}

func newTestDetector(r *fakeRunner, e fakeEnv) *Detector {
	return &Detector{
		Runner: r,
		LookPath: func(name string) (string, error) {
			if p, ok := e.onPath[name]; ok {
				return p, nil
			}
			return "", exec.ErrNotFound
		},
		IsFile: func(p string) bool { return e.files[p] },
		Glob:   func(string) ([]string, error) { return nil, nil },
		Home:   func() string { return "/home/u" },
		Getenv: func(k string) string { return e.env[k] },
		Keys: func(ids ...string) []string {
			var out []string
			for _, id := range ids {
				for _, k := range e.keys {
					if k == id {
						out = append(out, id)
					}
				}
			}
			return out
		},
		Timeout: time.Second,
		TTL:     time.Minute,
	}
}

const claudeLoggedIn = `{
  "loggedIn": true,
  "authMethod": "claude.ai",
  "apiProvider": "firstParty",
  "email": "dave@example.com",
  "orgId": "390ef70d",
  "orgName": "dave@example.com's Organization",
  "subscriptionType": "max"
}`

const claudeLoggedOut = `{"loggedIn": false, "authMethod": "none", "apiProvider": "firstParty"}`

func TestCheckClaudeLoggedIn(t *testing.T) {
	r := &fakeRunner{out: map[string]string{
		"/opt/homebrew/bin/claude --version":          "2.1.321 (Claude Code)\n",
		"/opt/homebrew/bin/claude auth status --json": claudeLoggedIn,
	}}
	d := newTestDetector(r, fakeEnv{onPath: map[string]string{"claude": "/opt/homebrew/bin/claude"}})
	st := d.CheckClaude(context.Background())
	if !st.Installed || !st.OnPath || st.Version != "2.1.321" || !st.VersionOK || !st.MeetsRecommended {
		t.Fatalf("install/version: %+v", st)
	}
	if st.Auth.State != LoggedIn || st.Auth.Method != MethodSubscription || st.Auth.Plan != "max" {
		t.Fatalf("auth: %+v", st.Auth)
	}
	if st.Auth.Account != "da•••@example.com" {
		t.Fatalf("account not masked: %q", st.Auth.Account)
	}
	if !st.Ready || len(st.NextSteps) != 0 {
		t.Fatalf("ready=%v steps=%v", st.Ready, st.NextSteps)
	}
}

func TestCheckClaudeNotLoggedInAndOld(t *testing.T) {
	r := &fakeRunner{
		out: map[string]string{
			"/usr/local/bin/claude --version":          "2.1.100 (Claude Code)",
			"/usr/local/bin/claude auth status --json": claudeLoggedOut,
		},
		codes: map[string]int{"/usr/local/bin/claude auth status --json": 1},
	}
	d := newTestDetector(r, fakeEnv{onPath: map[string]string{"claude": "/usr/local/bin/claude"}})
	st := d.CheckClaude(context.Background())
	if st.Auth.State != NotLoggedIn || st.Ready {
		t.Fatalf("want not logged in, not ready: %+v", st)
	}
	if !st.VersionOK || st.MeetsRecommended {
		t.Fatalf("2.1.100 is supported but below the recommended version: %+v", st)
	}
	kinds := stepKinds(st)
	if kinds != "update,login,provider_key" {
		t.Fatalf("steps = %s", kinds)
	}
}

func TestCheckClaudeProviderKeyCountsAsReady(t *testing.T) {
	r := &fakeRunner{out: map[string]string{
		"/c --version":          "2.1.280",
		"/c auth status --json": claudeLoggedOut,
	}}
	d := newTestDetector(r, fakeEnv{onPath: map[string]string{"claude": "/c"}, keys: []string{"claude"}})
	st := d.CheckClaude(context.Background())
	if !st.Ready || st.Auth.Method != MethodProviderKey || st.Auth.State != NotLoggedIn {
		t.Fatalf("provider key should make it ready: %+v", st)
	}
	if strings.Contains(stepKinds(st), "login") {
		t.Fatalf("no login step needed with a key: %v", st.NextSteps)
	}
}

func TestCheckClaudeTooOld(t *testing.T) {
	r := &fakeRunner{out: map[string]string{"/c --version": "1.0.99 (Claude Code)"}}
	d := newTestDetector(r, fakeEnv{onPath: map[string]string{"claude": "/c"}})
	st := d.CheckClaude(context.Background())
	if st.VersionOK || st.Ready {
		t.Fatalf("1.0.99 must be refused: %+v", st)
	}
	if st.Auth.State != Unknown || st.Error == "" {
		t.Fatalf("unsupported auth status should be unknown with a hint: %+v", st)
	}
}

func TestCheckClaudeNotInstalled(t *testing.T) {
	d := newTestDetector(&fakeRunner{}, fakeEnv{})
	st := d.CheckClaude(context.Background())
	if st.Installed || st.Ready || stepKinds(st) != "install" {
		t.Fatalf("%+v", st)
	}
	if st.NextSteps[0].Command != "npm install -g @anthropic-ai/claude-code" {
		t.Fatalf("install command: %q", st.NextSteps[0].Command)
	}
}

func TestLocateFallsBackToInstallLocations(t *testing.T) {
	// GUI apps often start without the shell PATH: an SDK location counts as
	// on PATH (sessions find it too), an extra location gets an add_to_path step.
	d := newTestDetector(&fakeRunner{}, fakeEnv{files: map[string]bool{"/home/u/.claude/local/claude": true}})
	if p, on := d.locate(Claude); p != "/home/u/.claude/local/claude" || !on {
		t.Fatalf("sdk location: %q %v", p, on)
	}
	d = newTestDetector(&fakeRunner{}, fakeEnv{files: map[string]bool{"/home/u/.volta/bin/claude": true}})
	if p, on := d.locate(Claude); p != "/home/u/.volta/bin/claude" || on {
		t.Fatalf("extra location: %q %v", p, on)
	}
	d.Glob = func(string) ([]string, error) {
		return []string{"/home/u/.nvm/versions/node/v20.1.0/bin/codex", "/home/u/.nvm/versions/node/v24.12.0/bin/codex"}, nil
	}
	d.IsFile = func(p string) bool { return strings.Contains(p, ".nvm") }
	if p, _ := d.locate(Codex); p != "/home/u/.nvm/versions/node/v24.12.0/bin/codex" {
		t.Fatalf("nvm: newest version first, got %q", p)
	}
	d = newTestDetector(&fakeRunner{}, fakeEnv{env: map[string]string{"CODEX_PATH": "/x/codex"}, files: map[string]bool{"/x/codex": true}})
	if p, on := d.locate(Codex); p != "/x/codex" || !on {
		t.Fatalf("CODEX_PATH: %q", p)
	}
}

func TestCheckCodexStates(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		code      int
		want      AuthState
		method    AuthMethod
	}{
		{"chatgpt", "Logged in using ChatGPT\n", 0, LoggedIn, MethodSubscription},
		{"apikey", "WARNING: something\nLogged in using an API key - sk-proj-***ABCDE\n", 0, LoggedIn, MethodAPIKey},
		{"none", "Not logged in\n", 1, NotLoggedIn, MethodNone},
		{"garbage", "???", 0, Unknown, MethodNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRunner{
				out:   map[string]string{"/b/codex --version": "codex-cli 0.155.1", "/b/codex login status": tc.out},
				codes: map[string]int{"/b/codex login status": tc.code},
			}
			d := newTestDetector(r, fakeEnv{onPath: map[string]string{"codex": "/b/codex"}})
			d.CodexAccount = func(context.Context, string) (*CodexAccount, error) {
				return &CodexAccount{Type: "chatgpt", Email: "someone@example.com", PlanType: "plus"}, nil
			}
			st := d.CheckCodex(context.Background())
			if st.Version != "0.155.1" || st.Auth.State != tc.want || st.Auth.Method != tc.method {
				t.Fatalf("%+v", st)
			}
			if strings.Contains(st.Auth.Detail+st.Auth.Account, "ABCDE") {
				t.Fatal("API key suffix leaked")
			}
			if tc.name == "chatgpt" && (st.Auth.Plan != "plus" || st.Auth.Account != "so•••@example.com") {
				t.Fatalf("account: %+v", st.Auth)
			}
			if st.Ready != (tc.want == LoggedIn) {
				t.Fatalf("ready = %v", st.Ready)
			}
		})
	}
}

func TestCheckCodexEnvKeyAndAccountFailure(t *testing.T) {
	r := &fakeRunner{
		out:   map[string]string{"/b/codex login status": "Not logged in"},
		codes: map[string]int{"/b/codex login status": 1},
	}
	d := newTestDetector(r, fakeEnv{onPath: map[string]string{"codex": "/b/codex"}, env: map[string]string{"OPENAI_API_KEY": "sk-x"}})
	d.CodexAccount = func(context.Context, string) (*CodexAccount, error) { return nil, errors.New("boom") }
	st := d.CheckCodex(context.Background())
	if !st.Ready || st.Auth.Method != MethodProviderKey {
		t.Fatalf("env key should count: %+v", st)
	}
	for _, k := range st.ProviderKeys {
		if strings.HasPrefix(k, "sk-") {
			t.Fatal("key value leaked")
		}
	}
}

func TestCheckRunErrorIsReported(t *testing.T) {
	r := &fakeRunner{errs: map[string]error{"/c auth status --json": context.DeadlineExceeded}, out: map[string]string{"/c --version": "2.1.280"}}
	d := newTestDetector(r, fakeEnv{onPath: map[string]string{"claude": "/c"}})
	st := d.CheckClaude(context.Background())
	if st.Auth.State != Unknown || !strings.Contains(st.Error, "claude auth status") {
		t.Fatalf("%+v", st)
	}
}

func TestCheckCachesAndSharesResult(t *testing.T) {
	r := &fakeRunner{out: map[string]string{"/c --version": "2.1.280", "/c auth status --json": claudeLoggedIn}}
	d := newTestDetector(r, fakeEnv{onPath: map[string]string{"claude": "/c"}})
	first := d.Check(context.Background(), false)
	n := r.calls.Load()
	second := d.Check(context.Background(), false)
	if r.calls.Load() != n || len(first) != 2 || len(second) != 2 || first[0].ID != Claude || first[1].ID != Codex {
		t.Fatalf("cache miss: calls %d → %d", n, r.calls.Load())
	}
	d.Check(context.Background(), true)
	if r.calls.Load() == n {
		t.Fatal("refresh must re-run the checks")
	}
	d.Invalidate()
	m := r.calls.Load()
	d.Check(context.Background(), false)
	if r.calls.Load() == m {
		t.Fatal("Invalidate must drop the cache")
	}
}

func TestParseHelpers(t *testing.T) {
	if v := ParseVersion("codex-cli 0.155.1"); v != "0.155.1" {
		t.Fatal(v)
	}
	if CompareVersions("2.1.280", "2.1.99") != 1 || CompareVersions("2.0.0", "2.0.0") != 0 || CompareVersions("", "1.0.0") != -1 {
		t.Fatal("CompareVersions")
	}
	for in, want := range map[string]string{"ab@x.io": "a•••@x.io", "dave@x.io": "da•••@x.io", "nope": "", "": ""} {
		if got := MaskEmail(in); got != want {
			t.Errorf("MaskEmail(%q) = %q, want %q", in, got, want)
		}
	}
	a, ok := ParseClaudeAuthStatus([]byte(`{"loggedIn":true,"authMethod":"api_key","apiProvider":"firstParty"}`))
	if !ok || a.Method != MethodAPIKey {
		t.Fatalf("%+v", a)
	}
	a, _ = ParseClaudeAuthStatus([]byte(`{"loggedIn":true,"authMethod":"none","apiProvider":"bedrock"}`))
	if a.Method != MethodCloud || a.Detail != "bedrock" {
		t.Fatalf("%+v", a)
	}
	if _, ok := ParseClaudeAuthStatus([]byte("error: unknown command 'auth'")); ok {
		t.Fatal("non-JSON must not parse")
	}
}

func stepKinds(st Status) string {
	var k []string
	for _, s := range st.NextSteps {
		k = append(k, s.Kind)
	}
	return strings.Join(k, ",")
}

package agentenv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/stretchr/testify/require"
)

// serverEnv is what a server started from a developer's shell may hold.
func serverEnv() []string {
	return []string{
		"PATH=/usr/bin", "HOME=/Users/x", "USER=x", "LANG=en_US.UTF-8", "LC_ALL=C", "XDG_CONFIG_HOME=/c", "TERM=xterm",
		"SSH_AUTH_SOCK=/tmp/agent", "HTTPS_PROXY=http://p", "NODE_EXTRA_CA_CERTS=/ca.pem", "GOPATH=/g", "CARGO_HOME=/c", "NVM_DIR=/n",
		"HOMEBREW_PREFIX=/opt/homebrew", "CONDA_PREFIX=/c", "DEVELOPER_DIR=/d", "TMPDIR=/t",
		"EGGIFY_NGROK_AUTHTOKEN=ngrok", "NGROK_AUTHTOKEN=ngrok", "EGGIFY_CONTROL_PLANE_TOKEN=cp",
		"EGGIFY_RELAY_ADDR=r", "EGGIFY_AUTH_USER_AUTH_ENABLED=false", "EGGIFY_API_KEY=k", "EGGIFY_CODEX_API_KEY=k",
		"AWS_ACCESS_KEY_ID=a", "AWS_SECRET_ACCESS_KEY=s", "GOOGLE_APPLICATION_CREDENTIALS=/g.json", "AZURE_CLIENT_SECRET=z",
		"GITHUB_TOKEN=gh", "GH_TOKEN=gh", "STRIPE_API_KEY=s", "DB_PASSWORD=p", "GITHUB_OAUTH_CLIENT_SECRET=o", "SLACK_BOT_TOKEN=x",
		"ANTHROPIC_API_KEY=sk-ant", "CLAUDE_CODE_OAUTH_TOKEN=oat", "CLAUDE_CONFIG_DIR=/cc", "OPENAI_API_KEY=sk-oa", "CODEX_HOME=/cx",
		"HOMEBREW_GITHUB_API_TOKEN=hb",
	}
}

func names(env []string) map[string]bool {
	m := map[string]bool{}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		m[k] = true
	}
	return m
}

func TestRedTeam_AgentEnv_ServerSecretsNeverReachAgents(t *testing.T) {
	t.Setenv(ModeEnv, "")
	t.Setenv(AllowEnv, "")
	secrets := []string{
		"EGGIFY_NGROK_AUTHTOKEN", "NGROK_AUTHTOKEN", "EGGIFY_CONTROL_PLANE_TOKEN", "EGGIFY_RELAY_ADDR",
		"EGGIFY_AUTH_USER_AUTH_ENABLED", "EGGIFY_API_KEY", "EGGIFY_CODEX_API_KEY", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
		"GOOGLE_APPLICATION_CREDENTIALS", "AZURE_CLIENT_SECRET", "GITHUB_TOKEN", "GH_TOKEN", "STRIPE_API_KEY", "DB_PASSWORD",
		"GITHUB_OAUTH_CLIENT_SECRET", "SLACK_BOT_TOKEN", "HOMEBREW_GITHUB_API_TOKEN",
	}
	for _, rt := range []Runtime{Generic, Claude, Codex, Login} {
		got := names(Filter(serverEnv(), rt))
		for _, s := range secrets {
			require.False(t, got[s], "runtime %d got %s", rt, s)
		}
		for _, keep := range []string{"PATH", "HOME", "USER", "LANG", "LC_ALL", "XDG_CONFIG_HOME", "TERM", "SSH_AUTH_SOCK", "HTTPS_PROXY",
			"NODE_EXTRA_CA_CERTS", "GOPATH", "CARGO_HOME", "NVM_DIR", "HOMEBREW_PREFIX", "CONDA_PREFIX", "DEVELOPER_DIR", "TMPDIR"} {
			require.True(t, got[keep], "runtime %d lost %s", rt, keep)
		}
	}
}

func TestAgentEnv_VendorVariablesOnlyForTheirRuntime(t *testing.T) {
	t.Setenv(ModeEnv, "")
	t.Setenv(AllowEnv, "")
	gen := names(Filter(serverEnv(), Generic))
	for _, n := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CONFIG_DIR", "OPENAI_API_KEY", "CODEX_HOME"} {
		require.False(t, gen[n], n)
	}
	cl := names(Filter(serverEnv(), Claude))
	require.True(t, cl["ANTHROPIC_API_KEY"] && cl["CLAUDE_CODE_OAUTH_TOKEN"] && cl["CLAUDE_CONFIG_DIR"])
	require.False(t, cl["OPENAI_API_KEY"] || cl["CODEX_HOME"])
	cx := names(Filter(serverEnv(), Codex))
	require.True(t, cx["OPENAI_API_KEY"] && cx["CODEX_HOME"])
	require.False(t, cx["ANTHROPIC_API_KEY"] || cx["CLAUDE_CONFIG_DIR"])
	lg := names(Filter(serverEnv(), Login))
	require.True(t, lg["ANTHROPIC_API_KEY"] && lg["OPENAI_API_KEY"] && lg["CODEX_HOME"] && lg["CLAUDE_CONFIG_DIR"])
}

func TestAgentEnv_BedrockKeepsAWSForClaudeOnly(t *testing.T) {
	t.Setenv(ModeEnv, "")
	t.Setenv(AllowEnv, "")
	env := append(serverEnv(), "CLAUDE_CODE_USE_BEDROCK=1")
	require.True(t, names(Filter(env, Claude))["AWS_ACCESS_KEY_ID"])
	require.False(t, names(Filter(env, Generic))["AWS_ACCESS_KEY_ID"])
	require.False(t, names(Filter(serverEnv(), Claude))["AWS_ACCESS_KEY_ID"])
}

func TestAgentEnv_InheritAndAllowOverrides(t *testing.T) {
	t.Setenv(ModeEnv, "inherit")
	require.ElementsMatch(t, serverEnv(), Filter(serverEnv(), Generic))
	t.Setenv(ModeEnv, "")
	t.Setenv(AllowEnv, " GITHUB_TOKEN , DB_PASSWORD,")
	got := names(Filter(serverEnv(), Generic))
	require.True(t, got["GITHUB_TOKEN"] && got["DB_PASSWORD"])
	require.False(t, got["GH_TOKEN"] || got["AWS_ACCESS_KEY_ID"])
}

func TestAgentEnv_ChildKeepsExplicitEntriesLast(t *testing.T) {
	t.Setenv(ModeEnv, "")
	t.Setenv("EGGIFY_NGROK_AUTHTOKEN", "secret")
	t.Setenv("PATH", "/usr/bin")
	env := Child(Generic, "EGGIFY_SESSION_ID=s1", "CI=1")
	require.Equal(t, "CI=1", env[len(env)-1])
	require.Contains(t, env, "EGGIFY_SESSION_ID=s1")
	require.False(t, names(env)["EGGIFY_NGROK_AUTHTOKEN"])
}

// fakeClaude is a "claude" that prints its environment, found on PATH.
func fakeClaude(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n/usr/bin/env\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
}

// runAsSDK starts the CLI the way claude-agent-sdk-go does: the server's whole
// environment plus opts.Env, appended.
func runAsSDK(t *testing.T, opts *types.ClaudeAgentOptions) map[string]string {
	t.Helper()
	path := "claude"
	if opts.CLIPath != nil {
		path = *opts.CLIPath
	}
	cmd := exec.Command(path)
	cmd.Env = os.Environ()
	for k, v := range opts.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run %s: %v", path, err)
	}
	m := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			m[k] = v
		}
	}
	return m
}

// The SDK inherits the whole environment and cannot unset: the launcher must
// really remove secrets, pass the variables whose empty value would break
// tools, and keep what Eggify sets on purpose.
func TestRedTeam_AgentEnv_ClaudeCLIDoesNotGetServerSecrets(t *testing.T) {
	fakeClaude(t)
	t.Setenv(ModeEnv, "")
	t.Setenv(AllowEnv, "")
	for k, v := range map[string]string{
		"EGGIFY_NGROK_AUTHTOKEN": "ngrok", "AWS_SECRET_ACCESS_KEY": "aws", "GITHUB_TOKEN": "gh", "STRIPE_API_KEY": "s",
		"GIT_DIR": "/x", "GIT_WORK_TREE": "/y", "BASH_ENV": "/evil", "NODE_OPTIONS": "--require /evil", "DYLD_INSERT_LIBRARIES": "/e", "LD_PRELOAD": "/e",
		"GIT_AUTHOR_NAME": "Ada", "GIT_COMMITTER_EMAIL": "a@x", "GIT_SSH_COMMAND": "ssh -i k", "ZDOTDIR": "/z", "DOCKER_HOST": "unix:///d.sock",
		"KUBECONFIG": "/k", "GNUPGHOME": "/g", "LESS": "-R", "MISE_DATA_DIR": "/m", "ASDF_DIR": "/a", "DIRENV_DIR": "/d", "FORCE_COLOR": "1",
		"ANTHROPIC_API_KEY": "sk-ant",
		"my.token":          "odd", "deploy-key": "odd", "it's": "odd",
	} {
		t.Setenv(k, v)
	}
	opts := types.NewClaudeAgentOptions().WithEnvVar("GITHUB_TOKEN", "from-connector")
	opts = ScrubClaudeOptions(opts)
	if opts.CLIPath == nil {
		t.Fatal("no launcher was made")
	}
	env := runAsSDK(t, opts)
	for _, gone := range []string{"EGGIFY_NGROK_AUTHTOKEN", "AWS_SECRET_ACCESS_KEY", "STRIPE_API_KEY", "GIT_DIR", "GIT_WORK_TREE", "BASH_ENV", "NODE_OPTIONS", "DYLD_INSERT_LIBRARIES", "LD_PRELOAD", "my.token", "deploy-key", "it's"} {
		if v, ok := env[gone]; ok {
			t.Errorf("%s reached the CLI (%q)", gone, v)
		}
	}
	if env["GITHUB_TOKEN"] != "from-connector" {
		t.Errorf("connector value lost: %q", env["GITHUB_TOKEN"])
	}
	for k, want := range map[string]string{"GIT_AUTHOR_NAME": "Ada", "GIT_COMMITTER_EMAIL": "a@x", "GIT_SSH_COMMAND": "ssh -i k", "ZDOTDIR": "/z",
		"DOCKER_HOST": "unix:///d.sock", "KUBECONFIG": "/k", "GNUPGHOME": "/g", "LESS": "-R", "MISE_DATA_DIR": "/m", "ASDF_DIR": "/a", "DIRENV_DIR": "/d", "FORCE_COLOR": "1", "ANTHROPIC_API_KEY": "sk-ant"} {
		if env[k] != want {
			t.Errorf("%s = %q, want it unchanged (%q)", k, env[k], want)
		}
	}
	// Nothing is ever passed as an empty value.
	for k, v := range env {
		if v == "" && (k == "GIT_DIR" || k == "ZDOTDIR" || strings.HasPrefix(k, "GIT_")) {
			t.Errorf("%s was set empty", k)
		}
	}
	for k, v := range opts.Env {
		if v == "" {
			t.Errorf("launcher mode must not blank %s", k)
		}
	}
	// Scrubbing the same options again does not wrap the launcher again.
	again := ScrubClaudeOptions(opts)
	if *again.CLIPath != *opts.CLIPath {
		t.Error("launcher wrapped twice")
	}
}

// Without a claude to wrap, only secret-shaped names are blanked and every
// other variable is left alone (a blank GIT_AUTHOR_NAME breaks commits).
func TestAgentEnv_WithoutLauncherOnlySecretsAreBlanked(t *testing.T) {
	t.Setenv(ModeEnv, "")
	t.Setenv(AllowEnv, "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no claude
	for k, v := range map[string]string{"EGGIFY_NGROK_AUTHTOKEN": "n", "STRIPE_API_KEY": "s", "AWS_SECRET_ACCESS_KEY": "a", "GIT_DIR": "/x", "GIT_AUTHOR_NAME": "Ada", "ZDOTDIR": "/z", "BASH_ENV": "/e"} {
		t.Setenv(k, v)
	}
	opts := ScrubClaudeOptions(types.NewClaudeAgentOptions())
	if opts.CLIPath != nil {
		t.Skip("a claude was found")
	}
	for _, n := range []string{"EGGIFY_NGROK_AUTHTOKEN", "STRIPE_API_KEY", "AWS_SECRET_ACCESS_KEY"} {
		if v, ok := opts.Env[n]; !ok || v != "" {
			t.Errorf("%s not blanked", n)
		}
	}
	for _, n := range []string{"GIT_DIR", "GIT_AUTHOR_NAME", "ZDOTDIR", "BASH_ENV"} {
		if _, ok := opts.Env[n]; ok {
			t.Errorf("%s was overridden although it is not secret-shaped", n)
		}
	}
	// Every blanked name is secret-shaped.
	for n, v := range opts.Env {
		if v == "" && !secretShaped(n) {
			t.Errorf("%s blanked but not secret-shaped", n)
		}
	}
}

func TestAgentEnv_NeverPassedNamesAreDroppedForOtherRuntimes(t *testing.T) {
	t.Setenv(ModeEnv, "")
	t.Setenv(AllowEnv, "")
	env := []string{"GIT_DIR=/x", "GIT_WORK_TREE=/y", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=a", "GIT_ASKPASS=/a", "SSH_ASKPASS=/a", "BASH_ENV=/e", "ENV=/e",
		"PROMPT_COMMAND=x", "NODE_OPTIONS=--x", "PYTHONSTARTUP=/p", "DYLD_INSERT_LIBRARIES=/d", "LD_PRELOAD=/l", "GIT_AUTHOR_NAME=Ada", "ZDOTDIR=/z", "DOCKER_HOST=x"}
	for _, rt := range []Runtime{Generic, Codex, Claude, Login} {
		got := names(Filter(env, rt))
		for _, n := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_ASKPASS", "SSH_ASKPASS", "BASH_ENV", "ENV", "PROMPT_COMMAND", "NODE_OPTIONS", "PYTHONSTARTUP", "DYLD_INSERT_LIBRARIES", "LD_PRELOAD"} {
			if got[n] {
				t.Errorf("runtime %d passes %s", rt, n)
			}
		}
		for _, n := range []string{"GIT_AUTHOR_NAME", "ZDOTDIR", "DOCKER_HOST"} {
			if !got[n] {
				t.Errorf("runtime %d dropped %s", rt, n)
			}
		}
	}
}

func TestAgentEnv_CloudSelectorsAndSwitches(t *testing.T) {
	t.Setenv(ModeEnv, "")
	t.Setenv(AllowEnv, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	env := []string{"AWS_PROFILE=p", "AWS_REGION=eu", "AWS_DEFAULT_REGION=eu", "AWS_CONFIG_FILE=/c", "AWS_SHARED_CREDENTIALS_FILE=/s", "CLOUD_ML_REGION=r",
		"GOOGLE_CLOUD_PROJECT=g", "ANTHROPIC_VERTEX_PROJECT_ID=v", "AWS_ACCESS_KEY_ID=a", "AWS_SECRET_ACCESS_KEY=s"}
	got := names(Filter(env, Claude))
	for _, n := range []string{"AWS_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "CLOUD_ML_REGION", "GOOGLE_CLOUD_PROJECT", "ANTHROPIC_VERTEX_PROJECT_ID"} {
		if !got[n] {
			t.Errorf("selector %s dropped for Claude", n)
		}
	}
	if got["AWS_ACCESS_KEY_ID"] || got["AWS_SECRET_ACCESS_KEY"] {
		t.Error("AWS credentials passed without the Bedrock switch")
	}
	if names(Filter(env, Generic))["AWS_PROFILE"] {
		t.Error("selector passed to a generic child")
	}
	// The switch in ~/.claude/settings.json's env block counts.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"env":{"CLAUDE_CODE_USE_BEDROCK":"1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !names(Filter(env, Claude))["AWS_ACCESS_KEY_ID"] {
		t.Error("Bedrock switch in settings.json ignored")
	}
	// ...and so does one in the options.
	os.Remove(filepath.Join(home, ".claude", "settings.json"))
	for _, n := range []string{"AWS_ACCESS_KEY_ID"} {
		t.Setenv(n, "a")
	}
	if u := Unset(Claude, map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}, nil); contains(u, "AWS_ACCESS_KEY_ID") {
		t.Error("Bedrock switch in options ignored")
	}
	if u := Unset(Claude, nil, nil); !contains(u, "AWS_ACCESS_KEY_ID") {
		t.Error("AWS key kept without a switch")
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestAgentEnv_LogPolicyNamesNeverValues(t *testing.T) {
	t.Setenv(ModeEnv, "")
	t.Setenv("EGGIFY_NGROK_AUTHTOKEN", "ngrok-VALUE-123")
	logOnce = sync.Once{}
	var line string
	LogPolicy(func(f string, a ...any) { line = fmt.Sprintf(f, a...) })
	if !strings.Contains(line, "EGGIFY_NGROK_AUTHTOKEN") || strings.Contains(line, "VALUE-123") || !strings.Contains(line, AllowEnv) || !strings.Contains(line, ModeEnv+"=inherit") {
		t.Fatalf("log line: %q", line)
	}
}

package agents

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A verify command is written by the agent that started the loop: it must not
// see the server's secrets.
func TestRedTeam_AgentEnv_LoopVerifyCommandDoesNotSeeServerSecrets(t *testing.T) {
	t.Setenv("EGGIFY_NGROK_AUTHTOKEN", "ngrok-secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-secret")
	t.Setenv("EGGIFY_AGENT_ENV", "")
	t.Setenv("EGGIFY_AGENT_ENV_ALLOW", "")
	wd := t.TempDir()
	sm := &SessionManager{}
	s := &AgentSession{}
	s.Options.WorkingDirectory = &wd
	ok, out := sm.runLoopVerify(s, &LoopConfig{VerifyCommand: `test -z "$EGGIFY_NGROK_AUTHTOKEN$AWS_SECRET_ACCESS_KEY" && echo clean`})
	require.True(t, ok, out)
	require.Contains(t, out, "clean")
	require.False(t, strings.Contains(out, "secret"))
}

func TestAgentEnv_EggifyToolsGetsItsHostedSandboxVariablesOnPurpose(t *testing.T) {
	t.Setenv("EGGIFY_CONTROL_PLANE_URL", "https://cp.example")
	t.Setenv("EGGIFY_CONTROL_PLANE_TOKEN", "tok")
	t.Setenv("EGGIFY_SANDBOX_ID", "sb1")
	t.Setenv("EGGIFY_SECRET_STORE", "file")
	t.Setenv("EGGIFY_NGROK_AUTHTOKEN", "ngrok-secret")
	env := (&SessionManager{}).eggifyToolsEnv(uuid.New())
	require.Equal(t, "tok", env["EGGIFY_CONTROL_PLANE_TOKEN"])
	require.Equal(t, "sb1", env["EGGIFY_SANDBOX_ID"])
	require.Equal(t, "file", env["EGGIFY_SECRET_STORE"])
	_, leaked := env["EGGIFY_NGROK_AUTHTOKEN"]
	require.False(t, leaked)
}

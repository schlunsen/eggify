package providers

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schlunsen/eggify/internal/database"
)

// Attack: values stored with a provider (model name, key, URL) contain shell
// syntax, so sourcing ~/.claude/provider-env.sh would run it.
func TestRedTeam_EnvScriptQuoting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	marker := filepath.Join(home, "pwned")

	// Unsafe model names are refused.
	for _, m := range []string{`x"; touch ` + marker + `; "`, "$(touch " + marker + ")", "`id`", "a b", "a\nb", "-x"} {
		mm := m
		if err := GenerateEnvScript(&database.ProviderConfig{ProviderID: "claude", ModelName: &mm, UpdatedAt: time.Now()}); err == nil {
			t.Errorf("model name %q accepted", m)
		}
	}
	for _, m := range []string{"claude-opus-5-5[1m]", "qwen2.5:7b", "anthropic/claude-3.5-sonnet", "gpt-6-astra"} {
		if !ValidModelName(m) {
			t.Errorf("model name %q refused", m)
		}
	}

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	evil := "k$(touch " + marker + ")`touch " + marker + "`'\"\n;touch " + marker + ";`id`"
	url := "https://x.example/$(touch " + marker + ")'`id`"
	model := "claude-opus-5-5[1m]"
	for _, cfg := range []*database.ProviderConfig{
		{ProviderID: "claude", ModelName: &model, UpdatedAt: time.Now()},
		{ProviderID: "custom", APIKey: &evil, CustomURL: &url, ModelName: &model, UpdatedAt: time.Now()},
		{ProviderID: "custom", APIKey: &evil, CustomURL: &url, UpdatedAt: time.Now()},
	} {
		if err := GenerateEnvScript(cfg); err != nil {
			t.Fatalf("%s: %v", cfg.ProviderID, err)
		}
		script := GetEnvScriptPath()
		out, err := exec.Command(bash, "-c", `. "$1" >/dev/null; printf '%s\n%s\n%s' "$ANTHROPIC_AUTH_TOKEN" "$ANTHROPIC_BASE_URL" "$ANTHROPIC_MODEL"`, "x", script).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: sourcing the script failed: %v\n%s", cfg.ProviderID, err, out)
		}
		if _, err := os.Stat(marker); err == nil {
			b, _ := os.ReadFile(script)
			t.Fatalf("%s: sourcing the env script ran a command:\n%s", cfg.ProviderID, b)
		}
		if cfg.ProviderID == "custom" {
			if !strings.HasPrefix(string(out), evil+"\n"+url+"\n") {
				t.Fatalf("values not preserved literally: %q", out)
			}
		}
	}
}

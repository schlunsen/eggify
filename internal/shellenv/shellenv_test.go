package shellenv

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestParseSkipsNoiseBeforeMarker(t *testing.T) {
	out := []byte("Welcome!\nlast login\n" + marker + "\x00PATH=/opt/bin:/usr/bin\x00MULTI=a\nb\x00EMPTY=\x00broken\x00=nokey\x00")
	env := Parse(out)
	if env["PATH"] != "/opt/bin:/usr/bin" {
		t.Errorf("PATH = %q", env["PATH"])
	}
	if env["MULTI"] != "a\nb" {
		t.Errorf("MULTI = %q", env["MULTI"])
	}
	if v, ok := env["EMPTY"]; !ok || v != "" {
		t.Errorf("EMPTY = %q, %v", v, ok)
	}
	if _, ok := env["broken"]; ok {
		t.Error("entry without '=' should be skipped")
	}
	if _, ok := env[""]; ok {
		t.Error("entry without key should be skipped")
	}
}

func TestApplyKeepsExplicitVarsButReplacesPath(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("EGGIFY_SHELLENV_EXPLICIT", "launcher")
	os.Unsetenv("EGGIFY_SHELLENV_NEW")
	t.Cleanup(func() { os.Unsetenv("EGGIFY_SHELLENV_NEW") })

	Apply(map[string]string{
		"PATH":                     "/opt/homebrew/bin:/usr/bin",
		"EGGIFY_SHELLENV_EXPLICIT": "shell",
		"EGGIFY_SHELLENV_NEW":      "shell",
		"SHLVL":                    "3",
	})

	if got := os.Getenv("PATH"); got != "/opt/homebrew/bin:/usr/bin" {
		t.Errorf("PATH = %q, want shell PATH", got)
	}
	if got := os.Getenv("EGGIFY_SHELLENV_EXPLICIT"); got != "launcher" {
		t.Errorf("explicit var overwritten: %q", got)
	}
	if got := os.Getenv("EGGIFY_SHELLENV_NEW"); got != "shell" {
		t.Errorf("new var = %q", got)
	}
}

func TestReadFromRealShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX shell")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	t.Setenv("EGGIFY_SHELLENV_PROBE", "probe-value")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	env, err := Read(ctx, "/bin/sh")
	if err != nil {
		t.Skipf("shell not usable here: %v", err)
	}
	if env["EGGIFY_SHELLENV_PROBE"] != "probe-value" {
		t.Errorf("probe var missing from shell env (got %d vars)", len(env))
	}
}

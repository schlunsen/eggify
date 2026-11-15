package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// An agent can write ./.env into a project folder; starting Eggify from there
// must not turn it into server configuration.
func TestRedTeam_EnvFile_CurrentDirectoryIsNeverLoaded(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("EGGIFY_RT_PLANTED=1\nEGGIFY_AUTH_USER_AUTH_ENABLED=false\n"), 0o600))
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EGGIFY_ENV_FILE", "")
	os.Unsetenv("EGGIFY_RT_PLANTED")
	loadEnvFile()
	require.Empty(t, os.Getenv("EGGIFY_RT_PLANTED"))
}

func TestEnvFile_ExplicitFilesAreLoadedWithoutOverriding(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude", "eggify"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".claude", "eggify", ".env"), []byte("# c\nEGGIFY_RT_HOME=home\nEGGIFY_RT_KEPT=file\n"), 0o600))
	explicit := filepath.Join(t.TempDir(), "my.env")
	require.NoError(t, os.WriteFile(explicit, []byte("EGGIFY_RT_EXPLICIT=yes\n"), 0o600))
	t.Setenv("EGGIFY_ENV_FILE", explicit)
	t.Setenv("EGGIFY_RT_KEPT", "process")
	os.Unsetenv("EGGIFY_RT_HOME")
	os.Unsetenv("EGGIFY_RT_EXPLICIT")
	t.Cleanup(func() { os.Unsetenv("EGGIFY_RT_HOME"); os.Unsetenv("EGGIFY_RT_EXPLICIT") })
	loadEnvFile()
	require.Equal(t, "home", os.Getenv("EGGIFY_RT_HOME"))
	require.Equal(t, "yes", os.Getenv("EGGIFY_RT_EXPLICIT"))
	require.Equal(t, "process", os.Getenv("EGGIFY_RT_KEPT"))
}

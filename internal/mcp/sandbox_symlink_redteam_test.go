package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func text(r CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}

// symlinkWorld: a project folder and a "secret" file outside it, reachable
// from the project through links an agent could have created with Bash.
func symlinkWorld(t *testing.T) (work, secretDir, secret string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	work = filepath.Join(base, "project")
	secretDir = filepath.Join(base, "home", ".ssh")
	require.NoError(t, os.MkdirAll(work, 0o755))
	require.NoError(t, os.MkdirAll(secretDir, 0o700))
	secret = filepath.Join(secretDir, "id_ed25519")
	require.NoError(t, os.WriteFile(secret, []byte("PRIVATE KEY MATERIAL\n"), 0o600))
	require.NoError(t, os.Symlink(secret, filepath.Join(work, "key")))
	require.NoError(t, os.Symlink(secretDir, filepath.Join(work, "sshdir")))
	return
}

func TestRedTeam_MCP_CoreFileToolsDoNotFollowSymlinksOutOfTheWorkingDirectory(t *testing.T) {
	work, secretDir, secret := symlinkWorld(t)

	// Read through a file link and through a folder link.
	for _, p := range []string{"key", "sshdir/id_ed25519"} {
		r := handleReadTool(work, map[string]interface{}{"file_path": p})
		require.True(t, isErr(r), p)
		require.NotContains(t, text(r), "PRIVATE KEY")
	}
	// Write and Edit through the links must not touch the target.
	for _, p := range []string{"key", "sshdir/id_ed25519", "sshdir/new_file"} {
		r := handleWriteTool(work, map[string]interface{}{"file_path": p, "content": "pwned"})
		require.True(t, isErr(r), p)
	}
	r := handleEditTool(work, map[string]interface{}{"file_path": "key", "old_string": "PRIVATE", "new_string": "pwned"})
	require.True(t, isErr(r))
	data, err := os.ReadFile(secret)
	require.NoError(t, err)
	require.Equal(t, "PRIVATE KEY MATERIAL\n", string(data))
	_, err = os.Stat(filepath.Join(secretDir, "new_file"))
	require.True(t, os.IsNotExist(err))

	// A dangling link must not be written through either (it would create its target).
	target := filepath.Join(filepath.Dir(secretDir), "created_by_write")
	require.NoError(t, os.Symlink(target, filepath.Join(work, "dangling")))
	r = handleWriteTool(work, map[string]interface{}{"file_path": "dangling", "content": "x"})
	require.True(t, isErr(r))
	_, err = os.Stat(target)
	require.True(t, os.IsNotExist(err))

	// Glob and Grep name no file behind a link, and refuse a link as the search root.
	require.NoError(t, os.WriteFile(filepath.Join(work, "main.go"), []byte("PRIVATE in project\n"), 0o644))
	g := handleGrepTool(work, map[string]interface{}{"pattern": "PRIVATE"})
	require.NotContains(t, text(g), "KEY MATERIAL")
	require.Contains(t, text(g), "main.go")
	require.True(t, isErr(handleGrepTool(work, map[string]interface{}{"pattern": "PRIVATE", "path": "sshdir"})))
	require.True(t, isErr(handleGlobTool(work, map[string]interface{}{"pattern": "*", "path": "sshdir"})))
	gl := handleGlobTool(work, map[string]interface{}{"pattern": "**/*"})
	require.NotContains(t, text(gl), "id_ed25519")
	gl = handleGlobTool(work, map[string]interface{}{"pattern": "*"})
	require.NotContains(t, text(gl), "id_ed25519")
}

func TestRedTeam_MCP_CoreFileToolsRefuseProtectedPathsInsideTheWorkingDirectory(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude", "eggify"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".ssh", "id_rsa"), []byte("RSA SECRET"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".claude", "eggify", ".secret"), []byte("API KEY"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(home, "notes.txt"), []byte("RSA notes"), 0o644))

	// The session works in $HOME (it is allowed: the folder rules are in the tools).
	for _, p := range []string{".ssh/id_rsa", ".claude/eggify/.secret"} {
		r := handleReadTool(home, map[string]interface{}{"file_path": p})
		require.True(t, isErr(r), p)
		require.NotContains(t, text(r), "SECRET")
		require.True(t, isErr(handleWriteTool(home, map[string]interface{}{"file_path": p, "content": "x"})), p)
	}
	require.True(t, isErr(handleWriteTool(home, map[string]interface{}{"file_path": ".claude/settings.json", "content": "{}"})))
	g := handleGrepTool(home, map[string]interface{}{"pattern": "RSA|API|SECRET"})
	require.NotContains(t, text(g), "SECRET")
	require.NotContains(t, text(g), "API KEY")
	require.Contains(t, text(handleGrepTool(home, map[string]interface{}{"pattern": "RSA"})), "notes.txt")
	require.NotContains(t, text(handleGlobTool(home, map[string]interface{}{"pattern": "id_rsa"})), "id_rsa")
	require.NotContains(t, text(handleGlobTool(home, map[string]interface{}{"pattern": "**/*"})), ".secret")
}

func TestCoreFileTools_OrdinaryWorkStillWorks(t *testing.T) {
	work := t.TempDir()
	require.False(t, isErr(handleWriteTool(work, map[string]interface{}{"file_path": "a/b/c.txt", "content": "hello world\n"})))
	r := handleReadTool(work, map[string]interface{}{"file_path": "a/b/c.txt"})
	require.Contains(t, text(r), "hello world")
	require.False(t, isErr(handleEditTool(work, map[string]interface{}{"file_path": "a/b/c.txt", "old_string": "world", "new_string": "there"})))
	require.Contains(t, text(handleReadTool(work, map[string]interface{}{"file_path": "a/b/c.txt"})), "hello there")
	require.Contains(t, text(handleGrepTool(work, map[string]interface{}{"pattern": "there", "glob": "*.txt"})), "c.txt")
	require.Contains(t, text(handleGlobTool(work, map[string]interface{}{"pattern": "*.txt"})), "c.txt")
	// A link that stays inside the project is readable but never written through.
	require.NoError(t, os.Symlink(filepath.Join(work, "a", "b", "c.txt"), filepath.Join(work, "link.txt")))
	require.Contains(t, text(handleReadTool(work, map[string]interface{}{"file_path": "link.txt"})), "hello there")
	require.True(t, isErr(handleReadTool(work, map[string]interface{}{"file_path": "../outside"})))
}

// On a case-insensitive volume (APFS, the macOS default) ".SSH" is ".ssh": the
// protected-path rules must hold for every spelling, for all five tools.
func TestRedTeam_MCP_CoreFileToolsRefuseProtectedPathsInEveryCaseAndNormalisation(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude", "eggify"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(home, "Library", "Keychains"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".ssh", "id_rsa"), []byte("RSABODY"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".claude", "eggify", ".secret"), []byte("APIBODY"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(home, "Library", "Keychains", "login.keychain"), []byte("KCBODY"), 0o600))
	if _, err := os.Stat(filepath.Join(home, ".SSH")); err != nil {
		t.Skip("case-sensitive volume: case variants name other files")
	}
	settings := filepath.Join(home, ".claude", "settings.json")

	reads := []string{".SSH/id_rsa", ".ssh/ID_RSA", ".Ssh/Id_Rsa", ".claude/eggify/.SECRET", ".Claude/eggify/.secret", ".CLAUDE/EGGIFY/.SECRET", "./.SSH/../.SSH/id_rsa"}
	// "K" as the Kelvin sign and a decomposed spelling normalise to the same name.
	if _, err := os.Stat(filepath.Join(home, "Library", "Keychains")); err == nil {
		reads = append(reads, "Library/Keychains/login.keychain")
	}
	for _, p := range reads {
		r := handleReadTool(home, map[string]interface{}{"file_path": p})
		require.True(t, isErr(r), p)
		require.NotContains(t, text(r), "BODY")
		require.NotContains(t, text(r), "APIBODY")
		require.True(t, isErr(handleWriteTool(home, map[string]interface{}{"file_path": p, "content": "pwned"})), p)
		require.True(t, isErr(handleEditTool(home, map[string]interface{}{"file_path": p, "old_string": "B", "new_string": "X"})), p)
	}
	for _, p := range []string{".CLAUDE/settings.json", ".Claude/Settings.json", ".CLAUDE/hooks/x.sh"} {
		require.True(t, isErr(handleWriteTool(home, map[string]interface{}{"file_path": p, "content": "{}"})), p)
	}
	_, err = os.Stat(settings)
	require.True(t, os.IsNotExist(err), "~/.claude/settings.json was created through a case variant")
	_, err = os.Stat(filepath.Join(home, ".claude", "hooks"))
	require.True(t, os.IsNotExist(err))
	for f, want := range map[string]string{filepath.Join(home, ".ssh", "id_rsa"): "RSABODY", filepath.Join(home, ".claude", "eggify", ".secret"): "APIBODY"} {
		got, _ := os.ReadFile(f)
		require.Equal(t, want, string(got))
	}
	// Glob and Grep with the folder typed in another case, or as the search root.
	for _, d := range []string{".SSH", ".Claude/eggify", ".CLAUDE"} {
		require.True(t, isErr(handleGrepTool(home, map[string]interface{}{"pattern": "BODY", "path": d})) || !strings.Contains(text(handleGrepTool(home, map[string]interface{}{"pattern": "BODY", "path": d})), "SECRET"), d)
		require.NotContains(t, text(handleGlobTool(home, map[string]interface{}{"pattern": "*", "path": d})), "id_rsa")
		require.NotContains(t, text(handleGlobTool(home, map[string]interface{}{"pattern": "*", "path": d})), ".secret")
	}
	g := handleGrepTool(home, map[string]interface{}{"pattern": "BODY"})
	require.NotContains(t, text(g), "BODY")
	require.NotContains(t, text(g), "APIBODY")
	require.NotContains(t, text(handleGlobTool(home, map[string]interface{}{"pattern": "**/ID_RSA"})), "id_rsa")
}

func TestFoldPath(t *testing.T) {
	if !foldPaths {
		t.Skip("case-sensitive platform")
	}
	for _, c := range [][2]string{
		{"/h/.SSH/id_rsa", "/h/.ssh/id_rsa"},
		{"/h/Library/Keychains", "/h/library/keychains"}, // Kelvin sign
		{"/h/café/x", "/h/café/x"},                      // decomposed é
		{"/h/ﬁle", "/h/ﬁle"},                             // ligature stays itself
	} {
		if foldPath(c[0]) != foldPath(c[1]) {
			t.Errorf("%q and %q fold differently", c[0], c[1])
		}
	}
}

func TestGrep_SkipsGitAndNodeModulesAndSaysWhenCapped(t *testing.T) {
	work := t.TempDir()
	for f, c := range map[string]string{"src/a.go": "needle\n", ".git/config": "needle\n", "node_modules/pkg/i.js": "needle\n"} {
		require.NoError(t, os.MkdirAll(filepath.Join(work, filepath.Dir(f)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(work, f), []byte(c), 0o644))
	}
	out := text(handleGrepTool(work, map[string]interface{}{"pattern": "needle"}))
	require.Contains(t, out, "a.go")
	require.NotContains(t, out, ".git")
	require.NotContains(t, out, "node_modules")
	// Searching inside node_modules is allowed.
	require.Contains(t, text(handleGrepTool(work, map[string]interface{}{"pattern": "needle", "path": "node_modules"})), "i.js")

	pol, err := newPathPolicy(work)
	require.NoError(t, err)
	files, capped := pol.regularFiles(work, "")
	require.False(t, capped)
	require.Len(t, files, 1)
	big := t.TempDir()
	for i := 0; i < maxGrepFiles+5; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(big, fmt.Sprintf("f%d.txt", i)), nil, 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(big, "zz.txt"), []byte("needle"), 0o644))
	out = text(handleGrepTool(big, map[string]interface{}{"pattern": "needle"}))
	require.Contains(t, out, "search stopped after 20000 files; narrow the path")
}

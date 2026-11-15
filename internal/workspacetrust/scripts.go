package workspacetrust

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The settings JSON names commands, but the commands often run scripts that
// live in the repository (".claude/hooks/fmt.sh", "node mcp/server.js",
// "$CLAUDE_PROJECT_DIR/scripts/key.sh"). Trusting the JSON alone would let an
// agent keep a folder trusted while rewriting what its hooks execute, so the
// fingerprint also covers the content of every file inside the folder that
// the trust-relevant config refers to, plus everything under .claude/hooks.

// FingerprintVersion prefixes fingerprints that cover referenced scripts.
// Records without it were trusted before scripts were fingerprinted (see
// Checker.Decide, which upgrades them once).
const FingerprintVersion = "v2:"

// HooksDir is hashed in full whenever the folder has trust-relevant config.
var HooksDir = filepath.Join(".claude", "hooks")

// Bounds on what a scan reads. A folder over them never matches a stored
// fingerprint (it counts as changed every time), so Eggify asks instead of
// trusting content it did not hash.
const (
	maxScriptFiles = 256
	maxScriptBytes = 16 << 20
)

// ScriptFile is a file inside the folder that the config runs or reads.
type ScriptFile struct {
	Path   string `json:"path"`   // relative to the folder, slash-separated
	SHA256 string `json:"sha256"` // content hash, or "missing"
}

// scriptScan collects referenced files.
type scriptScan struct {
	root    string
	files   map[string]string // rel path -> sha256 or "missing"
	watch   map[string]bool   // absolute paths to stat for cache invalidation
	bytes   int64
	overCap bool
}

func newScriptScan(root string) *scriptScan {
	return &scriptScan{root: root, files: map[string]string{}, watch: map[string]bool{}}
}

// candidateTokens splits a command (or any config string) into words that
// might be paths: shell punctuation and quotes are separators, the value of
// --opt=value / KEY=value is used, and $CLAUDE_PROJECT_DIR becomes the folder.
func (s *scriptScan) candidateTokens(text string) []string {
	for _, v := range []string{"${CLAUDE_PROJECT_DIR}", "$CLAUDE_PROJECT_DIR"} {
		text = strings.ReplaceAll(text, v, s.root)
	}
	// Quotes join the text around them in a shell word ("$DIR"/x.sh).
	text = strings.NewReplacer(`"`, "", `'`, "", "\\", "").Replace(text)
	fields := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ';', '|', '&', '(', ')', '<', '>', '`', ',', '{', '}', '[', ']':
			return true
		}
		return false
	})
	var out []string
	for _, f := range fields {
		if i := strings.LastIndexByte(f, '='); i >= 0 {
			f = f[i+1:]
		}
		// "Bash(./scripts/test.sh:*)" and "file.sh:arg" style suffixes.
		if i := strings.IndexByte(f, ':'); i >= 0 {
			f = f[:i]
		}
		f = strings.TrimRight(f, "*")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// addReferences records the files inside the folder that text refers to.
func (s *scriptScan) addReferences(text string) {
	for _, tok := range s.candidateTokens(text) {
		if strings.HasPrefix(tok, "~") || strings.HasPrefix(tok, "-") || strings.Contains(tok, "://") {
			continue
		}
		p := tok
		if !filepath.IsAbs(p) {
			p = filepath.Join(s.root, p)
		}
		p = filepath.Clean(p)
		rel, err := filepath.Rel(s.root, p)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue // outside the folder (or the folder itself)
		}
		fi, err := os.Stat(p)
		switch {
		case err == nil && fi.Mode().IsRegular():
			s.hashFile(p, rel)
		case err == nil:
			// a directory or device: not a script
		case strings.ContainsRune(tok, '/') && (strings.HasPrefix(tok, ".") || filepath.IsAbs(tok)):
			// A path that does not exist yet: creating it later must change
			// the fingerprint.
			s.record(rel, "missing")
			s.watch[p] = true
		}
	}
}

func (s *scriptScan) record(rel, sum string) {
	if _, seen := s.files[filepath.ToSlash(rel)]; seen {
		return
	}
	if len(s.files) >= maxScriptFiles {
		s.overCap = true
		return
	}
	s.files[filepath.ToSlash(rel)] = sum
}

func (s *scriptScan) hashFile(abs, rel string) {
	key := filepath.ToSlash(rel)
	if _, seen := s.files[key]; seen || s.overCap {
		return
	}
	s.watch[abs] = true
	f, err := os.Open(abs)
	if err != nil {
		s.record(rel, "unreadable")
		return
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxScriptBytes-s.bytes+1))
	s.bytes += n
	if s.bytes > maxScriptBytes {
		s.overCap = true
		return
	}
	if err != nil {
		s.record(rel, "unreadable")
		return
	}
	s.record(rel, hex.EncodeToString(h.Sum(nil)))
}

// addHooksDir hashes every file under .claude/hooks.
func (s *scriptScan) addHooksDir() {
	dir := filepath.Join(s.root, HooksDir)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || s.overCap {
			return nil
		}
		if d.IsDir() {
			s.watch[p] = true // a new file changes the directory's mtime
			return nil
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return nil
		}
		// Symlinks are followed by os.Stat/Open: their target is what runs.
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			s.hashFile(p, rel)
		} else if err != nil {
			s.record(rel, "missing")
		}
		return nil
	})
}

// referencedText lists the config strings that may name files: hook
// commands, MCP server commands and args, env values, allow rules and
// command-like settings (apiKeyHelper, statusLine, ...).
func referencedText(inv *Inventory) []string {
	var out []string
	for _, h := range inv.Hooks {
		out = append(out, h.Command)
	}
	for _, m := range inv.MCPServers {
		out = append(out, m.Command)
		out = append(out, m.Args...)
	}
	for _, e := range inv.Env {
		out = append(out, e.Value)
	}
	for _, r := range inv.Allow {
		out = append(out, r.Rule)
	}
	for _, o := range inv.Other {
		out = append(out, o.Value)
	}
	return out
}

// scanScripts fills inv.Scripts and returns the fingerprint material for the
// referenced files ("" when there is none) and whether the scan hit its caps.
func scanScripts(root string, inv *Inventory) (map[string]string, bool, []string) {
	s := newScriptScan(root)
	for _, text := range referencedText(inv) {
		s.addReferences(text)
	}
	s.addHooksDir()

	paths := make([]string, 0, len(s.files))
	for p := range s.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		inv.Scripts = append(inv.Scripts, ScriptFile{Path: p, SHA256: s.files[p]})
	}
	watch := make([]string, 0, len(s.watch))
	for p := range s.watch {
		watch = append(watch, p)
	}
	sort.Strings(watch)
	return s.files, s.overCap, watch
}

// unmatchableNonce makes a fingerprint that no stored record can equal.
func unmatchableNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
)

// Claude sessions get their approved skills natively: Eggify builds a plugin
// directory from the skills' snapshots and starts the CLI with
// `--plugin-dir <dir>`. The CLI then lists each skill by name and
// description and loads the body only when the skill is used (progressive
// disclosure), runs it with the snapshot folder as its base directory, and
// honours its frontmatter (allowed-tools, model, ...). Plugin skills are
// namespaced by the plugin name ("eggify:<skill>"); the CLI also resolves the
// plain "/<skill>" (verified against Claude Code 2.1.288).
//
// `--plugin-dir` is a flag, so it applies with `--setting-sources ""`
// (untrusted folders) too, and ~/.claude/skills is never loaded by the CLI
// itself (Eggify runs it without the "user" setting source): only reviewed
// snapshots reach a session.

// PluginName namespaces the skills Eggify hands to the Claude CLI.
const PluginName = "eggify"

// pluginKeepFor is how long an unused plugin directory is kept (a running
// CLI keeps reading the one it was started with).
const pluginKeepFor = 7 * 24 * time.Hour

var pluginSkillName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidPluginSkillName reports whether name can be a skill folder in the
// plugin (and a slash command).
func ValidPluginSkillName(name string) bool {
	return pluginSkillName.MatchString(name) && !strings.Contains(name, "..")
}

// ApplicableSkills picks the approved skills a session gets:
//   - skills created in Eggify or by agents (no folder) and personal skills
//     (~/.claude/skills): every session;
//   - project skills (a project's .claude/skills): sessions whose working
//     directory contains that folder;
//   - any approved skill whose id or name is in enabled (the session's
//     EnabledSkillIDs, seeded from the project's default skills).
//
// Pending and rejected skills never apply. Names that cannot be a plugin
// folder are left out (and logged).
func ApplicableSkills(all []*database.Skill, workDir string, enabled []string) []*database.Skill {
	want := map[string]bool{}
	for _, e := range enabled {
		want[e] = true
	}
	wd := resolvedDir(workDir)
	var out []*database.Skill
	seen := map[string]bool{}
	for _, s := range all {
		if s.Status != database.ReviewStatusApproved || seen[s.Name] {
			continue
		}
		if reach(s, wd, want) == "" {
			continue
		}
		if !ValidPluginSkillName(s.Name) {
			logging.Warning("Skill %q is approved but its name cannot be used as a Claude skill; rename it to use it", s.Name)
			continue
		}
		seen[s.Name] = true
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Why an approved skill applies to a session (Reach).
const (
	ReachEverywhere = "everywhere" // not a project skill: every session gets it
	ReachFolder     = "folder"     // a project skill whose folder is inside the working directory
	ReachAdded      = "added"      // enabled for the session (EnabledSkillIDs)
)

// Reach says why an approved skill applies to a session with this working
// directory and enabled list, or "" when it doesn't. A skill that applies on
// its own is reported as such even when it is also in the enabled list.
func Reach(s *database.Skill, workDir string, enabled []string) string {
	if s == nil || s.Status != database.ReviewStatusApproved {
		return ""
	}
	want := map[string]bool{}
	for _, e := range enabled {
		want[e] = true
	}
	return reach(s, resolvedDir(workDir), want)
}

func reach(s *database.Skill, wd string, want map[string]bool) string {
	switch {
	case s.Path == "" || s.Scope != "project":
		return ReachEverywhere
	case wd != "" && within(resolvedDir(filepath.Dir(s.Path)), wd):
		return ReachFolder
	case want[s.ID] || want[s.Name]:
		return ReachAdded
	}
	return ""
}

func resolvedDir(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p)
}

func within(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// PluginSetKey content-addresses a set of skill versions ("" for none).
func PluginSetKey(list []*database.Skill) string {
	if len(list) == 0 {
		return ""
	}
	parts := make([]string, 0, len(list))
	for _, s := range list {
		parts = append(parts, s.Name+"\x00"+s.ID+"\x00"+s.SnapshotKey())
	}
	sort.Strings(parts)
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// EnsurePlugin returns a plugin directory holding the snapshots of list,
// building it when missing, and the set's key. Skills whose snapshot cannot be
// built (their folder changed since approval and nothing was staged) are left
// out and logged; the key then names the skills actually included.
func (st *Store) EnsurePlugin(list []*database.Skill) (dir, key string, err error) {
	type entry struct {
		skill *database.Skill
		snap  string
	}
	var entries []entry
	var included []*database.Skill
	for _, s := range list {
		snap, err := st.EnsureSnapshot(s)
		if err != nil {
			logging.Warning("Skill %q is approved but cannot be loaded: %v", s.Name, err)
			continue
		}
		entries = append(entries, entry{s, snap})
		included = append(included, s)
	}
	key = PluginSetKey(included)
	if key == "" {
		return "", "", nil
	}
	r, err := st.root()
	if err != nil {
		return "", "", err
	}
	base := filepath.Join(r, "skill-plugins")
	dir = filepath.Join(base, key)
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		now := time.Now()
		_ = os.Chtimes(dir, now, now) // in use: keep it
		return dir, key, nil
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", "", fmt.Errorf("cannot create the skills plugin folder: %w", err)
	}
	tmp, err := os.MkdirTemp(base, ".tmp-")
	if err != nil {
		return "", "", fmt.Errorf("cannot create the skills plugin folder: %w", err)
	}
	defer os.RemoveAll(tmp)

	manifest := map[string]interface{}{
		"name":        PluginName,
		"version":     "1.0.0",
		"description": "Skills reviewed and approved in Eggify",
		"author":      map[string]string{"name": "Eggify"},
	}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.MkdirAll(filepath.Join(tmp, ".claude-plugin"), 0o700); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, ".claude-plugin", "plugin.json"), data, 0o644); err != nil {
		return "", "", err
	}
	for _, e := range entries {
		if err := copyTree(e.snap, filepath.Join(tmp, "skills", e.skill.Name)); err != nil {
			return "", "", fmt.Errorf("cannot copy skill %q into the plugin: %w", e.skill.Name, err)
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		if fi, statErr := os.Stat(dir); statErr == nil && fi.IsDir() {
			return dir, key, nil
		}
		return "", "", fmt.Errorf("cannot store the skills plugin: %w", err)
	}
	st.prunePlugins(base, key)
	return dir, key, nil
}

// prunePlugins removes plugin directories unused for pluginKeepFor.
func (st *Store) prunePlugins(base, keep string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-pluginKeepFor)
	for _, e := range entries {
		if e.Name() == keep {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(base, e.Name()))
	}
}

// copyTree copies a snapshot (regular files and folders only) to dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o700)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		return copyRegularFile(path, to, mode)
	})
}

// SkillIndex is the "Available skills" list for runtimes without native
// skills (Codex, direct OpenAI-compatible sessions): names and one-line
// descriptions; the body is loaded with the invoke_skill tool.
func SkillIndex(list []*database.Skill) string {
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Available skills\n\n")
	b.WriteString("These skills were reviewed and approved by the user. When a task matches one (or the user types /<name>), ")
	b.WriteString("call the invoke_skill tool with its name to load its instructions; the result also names the folder ")
	b.WriteString("its files are in, so resolve the skill's relative paths (scripts/, templates/) against that folder.\n\n")
	for _, s := range list {
		desc := strings.Join(strings.Fields(s.Description), " ")
		if len(desc) > 250 {
			desc = desc[:247] + "..."
		}
		if desc == "" {
			fmt.Fprintf(&b, "- %s\n", s.Name)
		} else {
			fmt.Fprintf(&b, "- %s: %s\n", s.Name, desc)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

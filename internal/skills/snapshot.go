package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schlunsen/eggify/internal/database"
	"gopkg.in/yaml.v3"
)

// Approved skills run from snapshots: Eggify-owned copies of exactly the
// reviewed version, at <store>/skill-snapshots/<skill id>/<snapshot key>/.
// The key content-addresses the version (name, description, frontmatter, body
// and folder fingerprint, database.SkillSnapshotKey), so editing the source
// folder after approval changes nothing a session sees until the new version
// is approved. A discovered folder is staged when it is queued for review, so
// the reviewer approves the staged copy, not whatever is on disk later.
//
// Each snapshot has a manifest next to it (<key>.manifest.json) with the
// folder's file list and fingerprint, used to reuse the files for a new
// version of the same folder (a reviewer's edit of the body) and to show the
// reviewer what they approve.
//
// SKILL.md in a snapshot is generated from the database row (the approved
// text, with any reviewer edits), never copied from disk.

// StoreDirEnv overrides the store root (tests, sandboxes).
const StoreDirEnv = "EGGIFY_SKILL_STORE_DIR"

// ErrFolderChanged means a snapshot could not be built because the skill's
// folder no longer matches the version in the database (it changed since it
// was queued, or it is over the size limits).
var ErrFolderChanged = errors.New("the skill's folder changed since it was queued for review; run skill discovery again and review the new version")

// Store holds skill snapshots and session plugin directories. The zero value
// uses $EGGIFY_SKILL_STORE_DIR, else ~/.claude/eggify, resolved on each call.
type Store struct {
	Root string
}

// DefaultStore returns the store under ~/.claude/eggify.
func DefaultStore() *Store { return &Store{} }

func (st *Store) root() (string, error) {
	if st != nil && st.Root != "" {
		return st.Root, nil
	}
	if dir := os.Getenv(StoreDirEnv); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find the home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "eggify"), nil
}

func (st *Store) snapshotsDir() (string, error) {
	r, err := st.root()
	if err != nil {
		return "", err
	}
	return filepath.Join(r, "skill-snapshots"), nil
}

// snapshotManifest is stored next to each snapshot.
type snapshotManifest struct {
	FolderHash string          `json:"folder_hash"`
	FilesHash  string          `json:"files_hash"`
	Folder     *FolderManifest `json:"folder"`
}

// SnapshotDir returns where the skill's current version is (or would be)
// stored. It does not create it.
func (st *Store) SnapshotDir(s *database.Skill) (string, error) {
	if !isSafeSegment(s.ID) {
		return "", fmt.Errorf("invalid skill id %q", s.ID)
	}
	base, err := st.snapshotsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, s.ID, s.SnapshotKey()), nil
}

// EnsureSnapshot returns the snapshot directory of the skill's current
// version, building it when missing. A skill with a folder (FolderHash set)
// gets its files from an earlier snapshot of the same folder, or from the
// source folder when that still has exactly the fingerprinted files;
// otherwise ErrFolderChanged. A skill without a folder gets SKILL.md only.
func (st *Store) EnsureSnapshot(s *database.Skill) (string, error) {
	dir, err := st.SnapshotDir(s)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return dir, nil
	}
	if strings.HasPrefix(s.FolderHash, database.FolderHashOverLimitPrefix) {
		return "", ErrFolderChanged
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("cannot create the skill snapshot folder: %w", err)
	}
	tmp, err := os.MkdirTemp(parent, ".tmp-")
	if err != nil {
		return "", fmt.Errorf("cannot create the skill snapshot folder: %w", err)
	}
	defer os.RemoveAll(tmp)

	var manifest *snapshotManifest
	if s.FolderHash != "" {
		manifest, err = st.copyFolderFiles(s, parent, tmp)
		if err != nil {
			return "", err
		}
	} else {
		manifest = &snapshotManifest{Folder: &FolderManifest{Files: []FolderFile{}}}
	}
	if err := os.WriteFile(filepath.Join(tmp, "SKILL.md"), []byte(RenderSkillMD(s)), 0o644); err != nil {
		return "", fmt.Errorf("cannot write the skill snapshot: %w", err)
	}
	if err := os.Rename(tmp, dir); err != nil {
		if fi, statErr := os.Stat(dir); statErr == nil && fi.IsDir() {
			return dir, nil // built concurrently by another process
		}
		return "", fmt.Errorf("cannot store the skill snapshot: %w", err)
	}
	if data, err := json.Marshal(manifest); err == nil {
		_ = os.WriteFile(dir+".manifest.json", data, 0o600)
	}
	return dir, nil
}

// copyFolderFiles copies the folder files of version s into tmp and returns
// their manifest.
func (st *Store) copyFolderFiles(s *database.Skill, snapshotsOfSkill, tmp string) (*snapshotManifest, error) {
	// An earlier snapshot of the same folder (the staged copy, or the
	// previous approved version when only the text changed).
	if entries, err := os.ReadDir(snapshotsOfSkill); err == nil {
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".manifest.json") {
				continue
			}
			prev, err := readManifest(filepath.Join(snapshotsOfSkill, name))
			if err != nil || prev.FolderHash != s.FolderHash {
				continue
			}
			src := filepath.Join(snapshotsOfSkill, strings.TrimSuffix(name, ".manifest.json"))
			if err := copyVerified(src, tmp, prev.FilesHash); err == nil {
				return prev, nil
			}
		}
	}
	// The source folder, if it still has exactly the fingerprinted files.
	src := folderOf(s.Path)
	if src == "" {
		return nil, ErrFolderChanged
	}
	current := ScanFolder(src)
	if current.OverLimit || current.Hash() != s.FolderHash {
		return nil, ErrFolderChanged
	}
	root, err := filepath.EvalSymlinks(src)
	if err != nil {
		return nil, ErrFolderChanged
	}
	filesHash := current.filesHash(false)
	if err := copyVerified(root, tmp, filesHash); err != nil {
		return nil, err
	}
	return &snapshotManifest{FolderHash: s.FolderHash, FilesHash: filesHash, Folder: current}, nil
}

// copyVerified copies the regular files of src into dst (SKILL.md, symlinks
// and skipped names excluded), then checks that what landed in dst hashes to
// want, so a file swapped while it was copied is caught.
func copyVerified(src, dst, want string) error {
	m := ScanFolder(src)
	if m.OverLimit {
		return ErrFolderChanged
	}
	for _, f := range m.Files {
		if f.Symlink != "" {
			continue
		}
		from := filepath.Join(src, filepath.FromSlash(f.Path))
		to := filepath.Join(dst, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if f.Executable {
			mode = 0o755
		}
		if err := copyRegularFile(from, to, mode); err != nil {
			return err
		}
	}
	got := ScanFolder(dst)
	if got.OverLimit || got.filesHash(false) != want {
		return ErrFolderChanged
	}
	return nil
}

func copyRegularFile(from, to string, mode os.FileMode) error {
	// Lstat: never follow a link that replaced the file after the scan.
	fi, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return ErrFolderChanged
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(in, MaxFolderBytes+1)); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(to, mode)
}

func readManifest(path string) (*snapshotManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m snapshotManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.Folder == nil {
		m.Folder = &FolderManifest{Files: []FolderFile{}}
	}
	return &m, nil
}

// FolderFiles returns the files of the skill's current version for the review
// queue: from its snapshot's manifest (what will be used), else from the
// source folder. ok is false when the skill has no folder.
func (st *Store) FolderFiles(s *database.Skill) (m *FolderManifest, ok bool) {
	if s.Path == "" && s.FolderHash == "" {
		return nil, false
	}
	if dir, err := st.SnapshotDir(s); err == nil {
		if man, err := readManifest(dir + ".manifest.json"); err == nil {
			return man.Folder, true
		}
	}
	if s.FolderHash != "" {
		if _, err := st.EnsureSnapshot(s); err == nil {
			if dir, err := st.SnapshotDir(s); err == nil {
				if man, err := readManifest(dir + ".manifest.json"); err == nil {
					return man.Folder, true
				}
			}
		}
	}
	if src := folderOf(s.Path); src != "" {
		cur := ScanFolder(src)
		if s.FolderHash != "" && cur.Hash() != s.FolderHash && !cur.OverLimit {
			cur.Problem = "the folder changed since this version was queued; run skill discovery again"
		}
		return cur, true
	}
	return nil, false
}

// PruneSnapshots removes the skill's snapshots other than keepKey that are
// older than an hour (a session may still read one it was handed).
func (st *Store) PruneSnapshots(skillID, keepKey string) {
	if !isSafeSegment(skillID) {
		return
	}
	base, err := st.snapshotsDir()
	if err != nil {
		return
	}
	dir := filepath.Join(base, skillID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-time.Hour)
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".manifest.json")
		if name == keepKey || strings.HasPrefix(name, ".tmp-") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

// RenderSkillMD renders the skill's SKILL.md from its database row: the
// approved name, description, frontmatter and body.
func RenderSkillMD(s *database.Skill) string {
	var fm SkillFrontmatter
	if s.FrontmatterJSON != "" {
		_ = json.Unmarshal([]byte(s.FrontmatterJSON), &fm)
	}
	m := map[string]interface{}{"name": s.Name}
	setStr := func(key, v string) {
		if v = strings.TrimSpace(v); v != "" {
			m[key] = v
		}
	}
	setStr("description", firstNonEmpty(s.Description, fm.Description))
	setStr("argument-hint", firstNonEmpty(s.ArgumentHint, fm.ArgumentHint))
	setStr("allowed-tools", firstNonEmpty(s.AllowedTools, string(fm.AllowedTools)))
	setStr("model", firstNonEmpty(s.Model, fm.Model))
	setStr("effort", firstNonEmpty(s.Effort, fm.Effort))
	setStr("context", firstNonEmpty(s.Context, fm.Context))
	setStr("agent", firstNonEmpty(s.Agent, fm.Agent))
	setStr("shell", firstNonEmpty(s.Shell, fm.Shell))
	if s.DisableModelInvocation || fm.DisableModelInvocation {
		m["disable-model-invocation"] = true
	}
	if !s.UserInvocable {
		m["user-invocable"] = false
	}
	if len(fm.Hooks) > 0 {
		// Through JSON, whose tags drop empty fields.
		var hooks interface{}
		if b, err := json.Marshal(fm.Hooks); err == nil && json.Unmarshal(b, &hooks) == nil {
			m["hooks"] = hooks
		}
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		out = []byte(fmt.Sprintf("name: %q\n", s.Name))
	}
	return "---\n" + string(out) + "---\n\n" + strings.TrimSpace(s.Body) + "\n"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

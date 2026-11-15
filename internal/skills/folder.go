package skills

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schlunsen/eggify/internal/database"
)

// A skill is its folder: SKILL.md plus the scripts, templates and references
// next to it. Agents can write skill folders (~/.claude/skills and a
// project's .claude/skills), so the review must cover every file in the
// folder, not only SKILL.md; otherwise an agent could edit an approved skill's
// script unnoticed. ScanFolder fingerprints the folder for that review, with
// the same bounds as workspace trust (internal/workspacetrust/scripts.go).

const (
	// MaxFolderFiles is the most files a skill folder may hold.
	MaxFolderFiles = 256
	// MaxFolderBytes is the most bytes a skill folder's files may hold.
	MaxFolderBytes = 16 << 20
)

// skippedFolderNames are never part of a skill: version control, dependency
// caches and Finder litter. They are neither fingerprinted nor copied into the
// snapshot, so skipping them cannot smuggle anything into a session.
var skippedFolderNames = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__pycache__":  true,
	".DS_Store":    true,
}

// FolderFile is one entry of a skill folder.
type FolderFile struct {
	// Path is slash-separated and relative to the skill folder.
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable,omitempty"`
	// Symlink is the link's target. Links are fingerprinted (so changing one
	// counts as a change) but never followed or copied into a snapshot.
	Symlink string `json:"symlink,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
}

// FolderManifest lists a skill folder's files, SKILL.md excluded (its content
// is the skill's name, description, frontmatter and body, hashed separately).
type FolderManifest struct {
	Files      []FolderFile `json:"files"`
	TotalBytes int64        `json:"total_bytes"`
	// OverLimit is set when the folder has more than MaxFolderFiles files or
	// MaxFolderBytes bytes, or could not be read completely. Such a folder
	// never matches a stored fingerprint (Hash is random).
	OverLimit bool   `json:"over_limit,omitempty"`
	Problem   string `json:"problem,omitempty"`
}

// ScanFolder fingerprints the skill folder dir (symlinks to dir itself are
// resolved; links inside it are not followed).
func ScanFolder(dir string) *FolderManifest {
	m := &FolderManifest{Files: []FolderFile{}}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		m.fail(fmt.Sprintf("cannot resolve skill folder: %v", err))
		return m
	}
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		name := d.Name()
		if skippedFolderNames[name] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			return nil
		}
		if rel == "SKILL.md" {
			return nil
		}
		if len(m.Files) >= MaxFolderFiles {
			return errOverLimit
		}
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			m.Files = append(m.Files, FolderFile{Path: rel, Symlink: target})
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // sockets, devices: not part of a skill
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if m.TotalBytes+info.Size() > MaxFolderBytes {
			return errOverLimit
		}
		sum, n, err := hashFile(path)
		if err != nil {
			return err
		}
		m.TotalBytes += n
		if m.TotalBytes > MaxFolderBytes {
			return errOverLimit
		}
		m.Files = append(m.Files, FolderFile{
			Path:       rel,
			Size:       n,
			Executable: info.Mode().Perm()&0o111 != 0,
			SHA256:     sum,
		})
		return nil
	})
	switch {
	case walkErr == errOverLimit:
		m.fail(fmt.Sprintf("the skill folder has more than %d files or %d MiB; it cannot be approved until it is smaller", MaxFolderFiles, MaxFolderBytes>>20))
	case walkErr != nil:
		m.fail(fmt.Sprintf("cannot read the skill folder: %v", walkErr))
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	return m
}

var errOverLimit = fmt.Errorf("skill folder over limit")

func (m *FolderManifest) fail(problem string) {
	m.OverLimit = true
	m.Problem = problem
}

// Hash is the folder fingerprint stored as database.Skill.FolderHash. A
// folder over the limits gets a random hash, so it never matches.
func (m *FolderManifest) Hash() string {
	if m.OverLimit {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		return database.FolderHashOverLimitPrefix + hex.EncodeToString(b)
	}
	return "f1:" + m.filesHash(true)
}

// filesHash hashes the entries (symlinks only when withLinks).
func (m *FolderManifest) filesHash(withLinks bool) string {
	h := sha256.New()
	for _, f := range m.Files {
		if f.Symlink != "" {
			if withLinks {
				fmt.Fprintf(h, "L\x00%s\x00%s\n", f.Path, f.Symlink)
			}
			continue
		}
		exec := "-"
		if f.Executable {
			exec = "x"
		}
		fmt.Fprintf(h, "F\x00%s\x00%s\x00%s\n", f.Path, exec, f.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, MaxFolderBytes+1))
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// folderOf returns the folder of a discovered skill (its SKILL.md's folder).
func folderOf(skillPath string) string {
	if skillPath == "" {
		return ""
	}
	return filepath.Dir(skillPath)
}

// isSafeSegment reports whether s can be used as one path segment.
func isSafeSegment(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`) && !strings.HasPrefix(s, ".")
}

package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schlunsen/eggify/internal/logging"
)

// maxNestedLevels is how many levels below a skills directory's own entries
// discovery looks for a SKILL.md when a folder has none of its own, e.g.
// ~/.claude/skills/synced/<uuid>/<skill>/SKILL.md (two extra levels).
const maxNestedLevels = 2

// maxVisitedDirs bounds one discovery run (a symlink into a huge tree).
const maxVisitedDirs = 5000

// DiscoveryIssue is a skill file that could not be used, or a folder that
// could not be read. Name is set when the skill's name is known.
type DiscoveryIssue struct {
	Path    string `json:"path"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
}

func (i DiscoveryIssue) String() string {
	if i.Name != "" {
		return fmt.Sprintf("%s (%s): %s", i.Path, i.Name, i.Message)
	}
	return fmt.Sprintf("%s: %s", i.Path, i.Message)
}

// DiscoveryResult is what one discovery run found.
type DiscoveryResult struct {
	Skills []*ParsedSkill
	// Errors lists SKILL.md files that failed to parse and folders that could
	// not be read.
	Errors []DiscoveryIssue
}

// discoverer walks skills directories. visited holds the real (symlink
// resolved) path of every folder seen, so a folder reached twice (a symlink
// to a sibling, a cycle, a project that is also $HOME) is read once.
type discoverer struct {
	visited map[string]bool
	result  DiscoveryResult
}

func newDiscoverer() *discoverer {
	return &discoverer{visited: map[string]bool{}}
}

// Discover finds skills in ~/.claude/skills and, when projectDir is set, in
// projectDir/.claude/skills, and reports the files it could not use.
func Discover(projectDir string) DiscoveryResult {
	d := newDiscoverer()
	if homeDir, err := os.UserHomeDir(); err == nil {
		d.scanRoot(filepath.Join(homeDir, ".claude", "skills"), "personal")
	}
	if projectDir != "" {
		d.scanRoot(filepath.Join(projectDir, ".claude", "skills"), "project")
	}
	return d.result
}

// DiscoverAll discovers skills from all standard locations. Files that could
// not be used are logged; Discover returns them.
func DiscoverAll(projectDir string) ([]*ParsedSkill, error) {
	res := Discover(projectDir)
	logIssues(res.Errors)
	return res.Skills, nil
}

// DiscoverPersonal discovers skills from ~/.claude/skills/
func DiscoverPersonal() ([]*ParsedSkill, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	return discoverFromDir(filepath.Join(homeDir, ".claude", "skills"), "personal")
}

// DiscoverProject discovers skills from a project's .claude/skills/ directory
func DiscoverProject(projectDir string) ([]*ParsedSkill, error) {
	if projectDir == "" {
		return nil, fmt.Errorf("project directory is required")
	}
	return discoverFromDir(filepath.Join(projectDir, ".claude", "skills"), "project")
}

// DiscoverDir scans one skills directory (tests and callers that know the
// directory).
func DiscoverDir(dir, scope string) DiscoveryResult {
	d := newDiscoverer()
	d.scanRoot(dir, scope)
	return d.result
}

func discoverFromDir(dir, scope string) ([]*ParsedSkill, error) {
	res := DiscoverDir(dir, scope)
	logIssues(res.Errors)
	return res.Skills, nil
}

func logIssues(issues []DiscoveryIssue) {
	for _, i := range issues {
		logging.Warning("Skill discovery: %s", i)
	}
}

func (d *discoverer) issue(path, msg string) {
	d.result.Errors = append(d.result.Errors, DiscoveryIssue{Path: path, Message: msg})
}

func (d *discoverer) scanRoot(dir, scope string) {
	info, err := os.Stat(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			d.issue(dir, fmt.Sprintf("cannot read skills folder: %v", err))
		}
		return
	}
	if !info.IsDir() {
		return
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		d.issue(dir, fmt.Sprintf("cannot resolve skills folder: %v", err))
		return
	}
	if d.visited[real] {
		return
	}
	d.visited[real] = true
	d.scanChildren(dir, scope, 0)
}

// scanChildren looks at each folder in dir (following symlinks): a folder
// with a SKILL.md is a skill; one without is searched again, at most
// maxNestedLevels deeper. Hidden folders and node_modules are skipped.
func (d *discoverer) scanChildren(dir, scope string, level int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		d.issue(dir, fmt.Sprintf("cannot read folder: %v", err))
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		full := filepath.Join(dir, name)
		isLink := entry.Type()&os.ModeSymlink != 0
		if !entry.IsDir() && !isLink {
			continue
		}
		info, err := os.Stat(full) // follows symlinks
		if err != nil {
			if isLink {
				d.issue(full, fmt.Sprintf("broken symlink: %v", err))
			}
			continue
		}
		if !info.IsDir() {
			continue
		}
		real, err := filepath.EvalSymlinks(full)
		if err != nil {
			d.issue(full, fmt.Sprintf("cannot resolve folder: %v", err))
			continue
		}
		if d.visited[real] {
			continue // already seen (symlink to a sibling, or a cycle)
		}
		if len(d.visited) >= maxVisitedDirs {
			d.issue(full, fmt.Sprintf("stopped: more than %d folders under the skills folder", maxVisitedDirs))
			return
		}
		d.visited[real] = true

		skillFile := filepath.Join(full, "SKILL.md")
		fi, err := os.Stat(skillFile)
		if err == nil && !fi.IsDir() {
			d.parse(skillFile, name, scope)
			continue
		}
		if level < maxNestedLevels {
			d.scanChildren(full, scope, level+1)
		}
	}
}

func (d *discoverer) parse(skillFile, dirName, scope string) {
	skill, err := ParseFile(skillFile)
	if err != nil {
		d.issue(skillFile, strings.TrimPrefix(unwrapParseError(err, skillFile), ": "))
		return
	}
	skill.Scope = scope
	if skill.Frontmatter.Name == "" {
		skill.Frontmatter.Name = dirName
	}
	d.result.Skills = append(d.result.Skills, skill)
}

// unwrapParseError drops the "failed to parse skill file <path>" prefix that
// ParseFile adds, since the issue already carries the path.
func unwrapParseError(err error, path string) string {
	msg := err.Error()
	for _, prefix := range []string{"failed to parse skill file " + path, "failed to read skill file " + path} {
		if strings.HasPrefix(msg, prefix) {
			return msg[len(prefix):]
		}
	}
	return msg
}

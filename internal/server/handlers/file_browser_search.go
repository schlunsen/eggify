package handlers

import (
	"bytes"
	"errors"
	"github.com/schlunsen/eggify/internal/gitexec"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// projectFilesTTL is how long a project's file list is reused. Search
	// fires on every keystroke, so this saves a `git ls-files` per request.
	projectFilesTTL = 5 * time.Second
	// maxWalkFiles caps the filesystem walk used when a project is not a git
	// repository, so a huge directory (a home dir, a dataset) cannot stall
	// the request.
	maxWalkFiles = 20000
	// maxSymbolFileSize skips large files (bundles, generated code, data)
	// when looking for a symbol definition.
	maxSymbolFileSize = 1 << 20
)

type projectFilesEntry struct {
	files []string
	at    time.Time
}

var (
	projectFilesMu    sync.Mutex
	projectFilesCache = map[string]projectFilesEntry{}
	errWalkLimit      = errors.New("walk limit reached")
)

// projectFileList returns the project's files relative to dir: `git ls-files`
// when dir is a git work tree, otherwise a capped walk that skips hidden
// directories and skipDirs. Results are cached for projectFilesTTL; the
// returned slice is shared and must not be modified.
func projectFileList(dir string) []string {
	now := time.Now()
	projectFilesMu.Lock()
	if entry, ok := projectFilesCache[dir]; ok && now.Sub(entry.at) < projectFilesTTL {
		projectFilesMu.Unlock()
		return entry.files
	}
	projectFilesMu.Unlock()

	files := listProjectFiles(dir)

	projectFilesMu.Lock()
	for key, entry := range projectFilesCache {
		if now.Sub(entry.at) >= projectFilesTTL {
			delete(projectFilesCache, key)
		}
	}
	projectFilesCache[dir] = projectFilesEntry{files: files, at: now}
	projectFilesMu.Unlock()
	return files
}

func listProjectFiles(dir string) []string {
	cmd := gitexec.Command("ls-files")
	cmd.Dir = dir
	gitexec.Harden(cmd)
	if output, err := cmd.Output(); err == nil {
		var files []string
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				files = append(files, line)
			}
		}
		return files
	}

	var files []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if path != dir && (skipDirs[base] || strings.HasPrefix(base, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil
		}
		files = append(files, rel)
		if len(files) >= maxWalkFiles {
			return errWalkLimit
		}
		return nil
	})
	return files
}

// SymbolMatch is one definition of a symbol found in a file.
type SymbolMatch struct {
	Line    int
	Column  int
	Kind    string
	Context string
}

// findSymbolInFile returns up to max definitions of symbol in data, using
// the language's patterns. Files that cannot contain the symbol are rejected
// before any regex runs, and line numbers come from a line index built once
// per file.
func findSymbolInFile(data []byte, symbol string, patterns []*regexp.Regexp, max int) []SymbolMatch {
	if max <= 0 || !bytes.Contains(data, []byte(symbol)) || isBinaryContent(data) {
		return nil
	}
	content := string(data)
	var lineStarts []int // offsets where each line starts, built on the first match
	var out []SymbolMatch
	for _, pattern := range patterns {
		nameIdx := pattern.SubexpIndex("name")
		if nameIdx < 0 {
			continue
		}
		for _, match := range pattern.FindAllStringSubmatchIndex(content, -1) {
			if len(match) <= nameIdx*2+1 || match[nameIdx*2] < 0 {
				continue
			}
			nameStart, nameEnd := match[nameIdx*2], match[nameIdx*2+1]
			if content[nameStart:nameEnd] != symbol {
				continue
			}
			if lineStarts == nil {
				lineStarts = lineStartOffsets(content)
			}
			// Line of the match start (1-based)
			line := sort.Search(len(lineStarts), func(i int) bool { return lineStarts[i] > match[0] })
			// Column of the name within its own line (1-based, in bytes)
			nameLine := sort.Search(len(lineStarts), func(i int) bool { return lineStarts[i] > nameStart })
			col := nameStart - lineStarts[nameLine-1] + 1

			lineStart := lineStarts[line-1]
			lineEnd := len(content)
			if line < len(lineStarts) {
				lineEnd = lineStarts[line] - 1
			}
			contextLine := strings.TrimSpace(content[lineStart:lineEnd])
			if len(contextLine) > 120 {
				contextLine = contextLine[:120] + "..."
			}

			out = append(out, SymbolMatch{
				Line:    line,
				Column:  col,
				Kind:    symbolKind(pattern),
				Context: contextLine,
			})
			if len(out) >= max {
				return out
			}
		}
	}
	return out
}

// lineStartOffsets returns the byte offset at which each line starts.
func lineStartOffsets(content string) []int {
	starts := make([]int, 1, strings.Count(content, "\n")+1)
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// symbolKind derives the kind of definition a pattern matches.
func symbolKind(pattern *regexp.Regexp) string {
	patternStr := pattern.String()
	switch {
	case strings.Contains(patternStr, "func") || strings.Contains(patternStr, "function") || strings.Contains(patternStr, "def ") || strings.Contains(patternStr, "\\bfn\\b"):
		return "function"
	case strings.Contains(patternStr, "class"):
		return "class"
	case strings.Contains(patternStr, "type") || strings.Contains(patternStr, "interface") || strings.Contains(patternStr, "struct") || strings.Contains(patternStr, "enum") || strings.Contains(patternStr, "trait"):
		return "type"
	case strings.Contains(patternStr, "var") || strings.Contains(patternStr, "const") || strings.Contains(patternStr, "let"):
		return "variable"
	case strings.Contains(patternStr, "module"):
		return "module"
	}
	return "symbol"
}

// readSymbolCandidate reads a file for symbol search, skipping files larger
// than maxSymbolFileSize.
func readSymbolCandidate(path string) ([]byte, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSymbolFileSize {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// Package changelog parses the Keep a Changelog file embedded in the binary.
package changelog

import (
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/schlunsen/eggify"
)

// Section is one "### Added" style block of a release.
type Section struct {
	Title string   `json:"title"`
	Items []string `json:"items"`
}

// Release is one "## [2.21.0] - 2026-11-14" block.
type Release struct {
	Version string `json:"version"`
	Date    string `json:"date"`
	Name    string `json:"name,omitempty"`
	// Unreleased marks the "[Unreleased]" block (Version is "Unreleased", no date).
	Unreleased bool `json:"unreleased,omitempty"`
	// Note is the text between the heading and the first "###" section.
	Note     string    `json:"note,omitempty"`
	Sections []Section `json:"sections"`
}

var (
	releaseHeading = regexp.MustCompile(`^##\s+\[([^\]]+)\](?:\s*-\s*(.*))?$`)
	dateSuffix     = regexp.MustCompile(`(\d{4}-\d{2}-\d{2})\s*$`)
	linkRef        = regexp.MustCompile(`^\[[^\]]+\]:\s*\S+`)
	bulletStart    = regexp.MustCompile(`^[-*]\s+`)
)

// Parse reads changelog markdown into releases, newest (first in the file) first.
// An Unreleased block without items is skipped; link reference lines are ignored.
func Parse(src []byte) []Release {
	var (
		releases []Release
		cur      *Release
		sec      *Section
		item     []string
	)
	flushItem := func() {
		if sec != nil && len(item) > 0 {
			sec.Items = append(sec.Items, strings.TrimSpace(strings.Join(item, "\n")))
		}
		item = nil
	}
	flushSection := func() {
		flushItem()
		if cur != nil && sec != nil && len(sec.Items) > 0 {
			cur.Sections = append(cur.Sections, *sec)
		}
		sec = nil
	}
	var note []string
	flushRelease := func() {
		flushSection()
		if cur != nil {
			cur.Note = strings.TrimSpace(strings.Join(note, "\n"))
		}
		note = nil
		if cur != nil && (len(cur.Sections) > 0 || !cur.Unreleased) {
			releases = append(releases, *cur)
		}
		cur = nil
	}

	for _, raw := range strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n") {
		line := strings.TrimRight(raw, " \t")
		switch {
		case strings.HasPrefix(line, "## "):
			flushRelease()
			if m := releaseHeading.FindStringSubmatch(line); m != nil {
				cur = &Release{Version: strings.TrimPrefix(strings.TrimSpace(m[1]), "v"), Sections: []Section{}}
				if strings.EqualFold(cur.Version, "unreleased") {
					cur.Version, cur.Unreleased = "Unreleased", true
				}
				rest := strings.TrimSpace(m[2])
				if d := dateSuffix.FindStringSubmatch(rest); d != nil {
					cur.Date = d[1]
					rest = strings.TrimSpace(strings.TrimSuffix(rest, d[1]))
				}
				cur.Name = strings.TrimSpace(strings.TrimRight(rest, "- "))
			}
		case cur == nil:
		case strings.HasPrefix(line, "### "):
			flushSection()
			sec = &Section{Title: strings.TrimSpace(line[4:]), Items: []string{}}
		case linkRef.MatchString(line):
		case sec == nil:
			if strings.TrimSpace(line) != "" || len(note) > 0 {
				note = append(note, line)
			}
		case bulletStart.MatchString(line):
			flushItem()
			item = []string{bulletStart.ReplaceAllString(line, "")}
		case strings.TrimSpace(line) == "":
			// A blank line ends nothing: continuation lines may follow it, but
			// an indented one is needed to belong to the bullet.
			if len(item) > 0 {
				item = append(item, "")
			}
		case len(item) > 0 && (line[0] == ' ' || line[0] == '\t'):
			// Indentation relative to the bullet's text (two columns) is kept,
			// so nested lists stay nested.
			item = append(item, trimIndent(line, 2))
		case len(item) > 0:
			item = append(item, line)
		}
	}
	flushRelease()
	return releases
}

var (
	once   sync.Once
	cached []Release
)

// All returns the releases of the embedded CHANGELOG.md, parsed once.
func All() []Release {
	once.Do(func() { cached = Parse(eggify.Changelog) })
	return cached
}

// versionPattern is what the API accepts: dotted numbers, an optional "v" and an optional "-prerelease".
var versionPattern = regexp.MustCompile(`^v?\d+(\.\d+)*(-[0-9A-Za-z.-]+)?$`)

// ValidVersion reports whether v is a version CompareVersions understands.
func ValidVersion(v string) bool { return versionPattern.MatchString(strings.TrimSpace(v)) }

// CompareVersions compares dotted numeric versions. A "-prerelease" sorts before
// the release; its dot-separated identifiers compare like semver (numbers
// numerically and below words, so rc2 < rc10). It returns -1, 0 or 1.
func CompareVersions(a, b string) int {
	an, apre := splitVersion(a)
	bn, bpre := splitVersion(b)
	for i := 0; i < 3 || i < len(an) || i < len(bn); i++ {
		var x, y int
		if i < len(an) {
			x = an[i]
		}
		if i < len(bn) {
			y = bn[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case apre == bpre:
		return 0
	case apre == "":
		return 1
	case bpre == "":
		return -1
	}
	return comparePrerelease(apre, bpre)
}

func comparePrerelease(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, y := as[i], bs[i]
		xn, xerr := strconv.Atoi(x)
		yn, yerr := strconv.Atoi(y)
		switch {
		case xerr == nil && yerr == nil:
			if xn != yn {
				if xn < yn {
					return -1
				}
				return 1
			}
		case xerr == nil:
			return -1
		case yerr == nil:
			return 1
		default:
			if c := compareNatural(x, y); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}

func splitVersion(v string) ([]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	var nums []int
	for _, p := range strings.Split(v, ".") {
		n, _ := strconv.Atoi(p)
		nums = append(nums, n)
	}
	return nums, pre
}

// trimIndent removes up to n leading spaces.
func trimIndent(line string, n int) string {
	i := 0
	for i < n && i < len(line) && line[i] == ' ' {
		i++
	}
	return line[i:]
}

// compareNatural compares "rc2" and "rc10" by their text and then their
// trailing number, so rc2 < rc10.
func compareNatural(a, b string) int {
	at, an := splitTrailingNumber(a)
	bt, bn := splitTrailingNumber(b)
	switch {
	case at != bt:
		if at < bt {
			return -1
		}
		return 1
	case an < bn:
		return -1
	case an > bn:
		return 1
	}
	return 0
}

func splitTrailingNumber(s string) (string, int) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	n, err := strconv.Atoi(s[i:])
	if err != nil {
		return s, -1
	}
	return s[:i], n
}

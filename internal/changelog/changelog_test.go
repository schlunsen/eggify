package changelog

import (
	"regexp"
	"testing"
)

const fixture = `# Changelog

## [Unreleased]

## [1.2.0] - 2026-02-01

### Added
- **One**: first line
  continued line
- Two

### Fixed
- Three

## [1.1.0] - Some Name - 2026-01-01

### Changed
- Four

## [1.0.0] - 2025-12-31

### Added
- Five

[1.2.0]: https://example.com/compare/v1.1.0...v1.2.0
[1.0.0]: https://example.com/releases/v1.0.0
`

func TestParseFixture(t *testing.T) {
	rs := Parse([]byte(fixture))
	if len(rs) != 3 {
		t.Fatalf("got %d releases: %+v", len(rs), rs)
	}
	r := rs[0]
	if r.Version != "1.2.0" || r.Date != "2026-02-01" || len(r.Sections) != 2 {
		t.Fatalf("bad release %+v", r)
	}
	if r.Sections[0].Title != "Added" || r.Sections[1].Title != "Fixed" {
		t.Fatalf("section order %+v", r.Sections)
	}
	if got := r.Sections[0].Items; len(got) != 2 || got[0] != "**One**: first line\ncontinued line" || got[1] != "Two" {
		t.Fatalf("items %q", got)
	}
	if rs[1].Name != "Some Name" || rs[1].Date != "2026-01-01" {
		t.Fatalf("named heading %+v", rs[1])
	}
	last := rs[2].Sections[0].Items
	if len(last) != 1 || last[0] != "Five" {
		t.Fatalf("link lines leaked into %q", last)
	}
}

func TestUnreleasedKeptWhenItHasItems(t *testing.T) {
	rs := Parse([]byte("## [Unreleased]\n\n### Added\n- x\n\n## [1.0.0] - 2026-01-01\n\n### Fixed\n- y\n"))
	if len(rs) != 2 || rs[0].Version != "Unreleased" || !rs[0].Unreleased || rs[1].Unreleased {
		t.Fatalf("%+v", rs)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2.21.0", "2.20.21", 1}, {"2.9.0", "2.10.0", -1}, {"1.0.0", "1.0.0", 0},
		{"v1.0", "1.0.0", 0}, {"1.0.0-rc1", "1.0.0", -1},
		{"1.0.0-rc2", "1.0.0-rc10", -1}, {"1.0.0-rc.2", "1.0.0-rc.10", -1}, {"1.0.0-1", "1.0.0-a", -1}, {"1.0.0-a", "1.0.0-a.1", -1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("%s vs %s = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestValidVersion(t *testing.T) {
	for v, want := range map[string]bool{"2.21.0": true, "v1.2": true, "1.0.0-rc.1": true, "garbage": false, "": false, "1..2": false, "1.0.0-": false, "../x": false} {
		if got := ValidVersion(v); got != want {
			t.Errorf("ValidVersion(%q) = %v", v, got)
		}
	}
}

func TestNoteBeforeFirstSectionAndNestedBullets(t *testing.T) {
	rs := Parse([]byte("## [1.0.0] - 2026-01-01\n\n(Never published.)\n\n### Added\n- Parent\n  - child\n    - grandchild\n  more text\n- Next\n"))
	if rs[0].Note != "(Never published.)" {
		t.Fatalf("note %q", rs[0].Note)
	}
	items := rs[0].Sections[0].Items
	if len(items) != 2 || items[0] != "Parent\n- child\n  - grandchild\nmore text" {
		t.Fatalf("items %q", items)
	}
}

func TestEmbeddedChangelogParses(t *testing.T) {
	rs := All()
	if len(rs) == 0 {
		t.Fatal("no releases")
	}
	// A non-empty Unreleased block may lead; it is not a version.
	first := rs[0]
	if first.Unreleased {
		if len(rs) < 2 {
			t.Fatal("only Unreleased")
		}
		first = rs[1]
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+`).MatchString(first.Version) {
		t.Fatalf("newest version %q", first.Version)
	}
	for _, r := range rs {
		if len(r.Sections) == 0 {
			t.Errorf("%s has no sections", r.Version)
		}
	}
}

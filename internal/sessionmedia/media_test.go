package sessionmedia

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// mp4Bytes is the start of an MP4 file (size + "ftyp" box).
var mp4Bytes = []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2")

// mp3Bytes is an ID3-tagged MP3 start.
var mp3Bytes = []byte("ID3\x04\x00\x00\x00\x00\x00\x00\xff\xfb\x90\x00")

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func tools(t *testing.T, calls ...map[string]interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(calls)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// testRoots builds a home with a project and a Claude scratchpad, all real
// (symlink-resolved) paths so /var → /private/var on macOS doesn't matter.
func testRoots(t *testing.T) (Roots, string, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	project := filepath.Join(home, "projects", "film")
	tmp := filepath.Join(base, "tmp")
	for _, d := range []string{project, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	scratch := filepath.Join(tmp, "claude-501", claudeSlug(project), "sess-1", "scratchpad")
	return Roots{Home: home, WorkingDir: project, ProjectDir: project, TempDirs: []string{tmp}, UID: 501}, project, scratch
}

func TestFindsMediaInCommandsRepliesAndShowMedia(t *testing.T) {
	msgs := []Message{
		{ID: "u1", Sequence: 1, Role: "user", Content: "make /etc/secret.mp4 please"},
		{ID: "a1", Sequence: 2, Role: "assistant", ToolUses: tools(t,
			map[string]interface{}{"id": "t1", "name": "Bash", "input": map[string]interface{}{"command": "ffmpeg -i frames/%04d.png -c:v libx264 out/render.mp4 && ls"}},
		)},
		{ID: "a2", Sequence: 3, Role: "assistant", Content: "Done: the song is at ~/music/song.mp3."},
		{ID: "a3", Sequence: 4, Role: "assistant", ToolUses: tools(t,
			map[string]interface{}{"id": "t2", "name": "mcp__eggify-tools__show_media", "input": map[string]interface{}{"path": "/tmp/my clip.mov", "title": "Final cut"}},
		)},
	}
	refs := FindReferences(msgs)
	got := map[string]Reference{}
	for _, r := range refs {
		got[r.Path] = r
	}
	if _, ok := got["/etc/secret.mp4"]; ok {
		t.Error("a path the user typed must not count")
	}
	if r, ok := got["out/render.mp4"]; !ok || r.ToolUseID != "t1" {
		t.Errorf("ffmpeg output not found with its tool id: %+v", refs)
	}
	if _, ok := got["~/music/song.mp3"]; !ok {
		t.Errorf("reply path not found: %+v", refs)
	}
	if r, ok := got["/tmp/my clip.mov"]; !ok || !r.Shown || r.Title != "Final cut" {
		t.Errorf("show_media path (with a space) not taken whole: %+v", refs)
	}
}

func TestCollectServesOnlyTheSessionsOwnFolders(t *testing.T) {
	roots, project, scratch := testRoots(t)
	write(t, filepath.Join(project, "out", "render.mp4"), mp4Bytes)
	write(t, filepath.Join(scratch, "music", "song.mp3"), mp3Bytes)
	write(t, filepath.Join(roots.Home, "Downloads", "other.mp4"), mp4Bytes)

	refs := []Reference{
		{Path: "out/render.mp4", ToolUseID: "t1", Sequence: 2},
		{Path: filepath.Join(scratch, "music", "song.mp3"), ToolUseID: "t2", Sequence: 3},
		{Path: "~/Downloads/other.mp4", ToolUseID: "t3", Sequence: 4},
		{Path: "out/render.mp4", ToolUseID: "t4", Sequence: 5, Shown: true, Title: "Render"},
	}
	items := Collect(refs, roots)
	if len(items) != 2 {
		t.Fatalf("want the render and the scratchpad song, got %+v", items)
	}
	render := items[0]
	if render.Name != "render.mp4" || render.Kind != Video || !render.Shown || render.Title != "Render" ||
		render.ToolUseID != "t4" || render.Path != filepath.Join("out", "render.mp4") || !render.IOSPlays {
		t.Errorf("render item wrong: %+v", render)
	}
	if len(render.ToolUseIDs) != 2 {
		t.Errorf("both tool calls that named the render should be kept: %v", render.ToolUseIDs)
	}
	if items[1].Name != "song.mp3" || items[1].Kind != Audio {
		t.Errorf("scratchpad song wrong: %+v", items[1])
	}
	if items[0].ID != MediaID(items[0].RealPath) {
		t.Error("id must come from the real path")
	}
}

func TestRedTeam_SessionMedia_RefusesWhatIsNotTheSessionsMedia(t *testing.T) {
	roots, project, _ := testRoots(t)
	// A secret named like a video, inside the project.
	write(t, filepath.Join(project, "fake.mp4"), []byte("SQLite format 3\x00"))
	// A symlink in the project, named .mp4, to the Eggify database.
	db := filepath.Join(roots.Home, ".claude", "eggify", "eggify.db")
	write(t, db, []byte("SQLite format 3\x00"))
	if err := os.Symlink(db, filepath.Join(project, "db.mp4")); err != nil {
		t.Fatal(err)
	}
	// A real video in a protected folder, reached through a symlink in the project.
	protectedVid := filepath.Join(roots.Home, ".claude", "secret.mp4")
	write(t, protectedVid, mp4Bytes)
	if err := os.Symlink(protectedVid, filepath.Join(project, "link.mp4")); err != nil {
		t.Fatal(err)
	}
	// A real video in an app's WebKit data (where the desktop app's session
	// cookie lives), reached the same way.
	webkitVid := filepath.Join(roots.Home, "Library", "WebKit", "com.eggify.desktop.wails", "clip.mp4")
	write(t, webkitVid, mp4Bytes)
	if err := os.Symlink(webkitVid, filepath.Join(project, "webkit.mp4")); err != nil {
		t.Fatal(err)
	}
	cookieVid := filepath.Join(roots.Home, "Library", "HTTPStorages", "clip.mp4")
	write(t, cookieVid, mp4Bytes)
	if err := os.Symlink(cookieVid, filepath.Join(project, "cookies.mp4")); err != nil {
		t.Fatal(err)
	}
	// Another session's scratchpad (a different working directory's slug).
	other := filepath.Join(roots.TempDirs[0], "claude-501", "-other-project", "s", "scratchpad", "x.mp4")
	write(t, other, mp4Bytes)

	cases := map[string]error{
		"fake.mp4":                 ErrNotMedia,
		"db.mp4":                   ErrNotMedia,
		"link.mp4":                 ErrProtected,
		"webkit.mp4":               ErrProtected,
		"cookies.mp4":              ErrProtected,
		"../../../etc/hosts":       ErrNotMedia,
		"../../.claude/secret.mp4": ErrProtected,
		other:                      ErrOutside,
		"/etc/passwd.mp4":          ErrMissing,
	}
	for ref, want := range cases {
		if _, _, err := roots.Resolve(ref); err != want {
			t.Errorf("%s: got %v, want %v", ref, err, want)
		}
	}
	if items := Collect([]Reference{{Path: "fake.mp4"}, {Path: "db.mp4"}, {Path: "link.mp4"}, {Path: other}}, roots); len(items) != 0 {
		t.Errorf("nothing should be listed: %+v", items)
	}
}

func TestRedTeam_SessionMedia_BroadRootsAreIgnored(t *testing.T) {
	roots, _, _ := testRoots(t)
	write(t, filepath.Join(roots.Home, "Movies", "private.mp4"), mp4Bytes)
	// A session working in the home folder must not open up the whole home.
	roots.WorkingDir, roots.ProjectDir = roots.Home, roots.Home
	if _, _, err := roots.Resolve("Movies/private.mp4"); err != ErrOutside {
		t.Errorf("home as working directory: got %v, want ErrOutside", err)
	}
}

func TestSniff(t *testing.T) {
	ok := [][]byte{mp4Bytes, mp3Bytes, []byte("RIFF\x00\x00\x00\x00WAVEfmt "), []byte("\x1a\x45\xdf\xa3\x01"), []byte("fLaC\x00"), []byte("OggS\x00"), {0xFF, 0xF1, 0x50}}
	for _, b := range ok {
		if !sniff(b) {
			t.Errorf("should look like media: %q", b)
		}
	}
	for _, b := range [][]byte{[]byte("SQLite format 3\x00"), []byte("-----BEGIN"), []byte("{\"a\":1}"), nil} {
		if sniff(b) {
			t.Errorf("should not look like media: %q", b)
		}
	}
}

// Package sessionmedia finds the audio and video files an agent session made
// or pointed at, so the chat (web and iOS) can play them.
//
// A file counts when the session referenced it: a path with a media extension
// in a tool call's input (an ffmpeg command, a Write, eggify-tools'
// show_media) or in the agent's reply. It is served only when it still exists,
// lies inside one of the session's folders (working directory, project, the
// Claude scratchpad of that working directory), is not in a protected place,
// and its first bytes are really audio or video.
package sessionmedia

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Kind is "video" or "audio".
type Kind string

const (
	Video Kind = "video"
	Audio Kind = "audio"
)

// MaxFileSize is the largest file served (a long 4K render still fits).
const MaxFileSize = 8 << 30

// ShowMediaTool is eggify-tools' tool for presenting a file on purpose.
const ShowMediaTool = "show_media"

type format struct {
	kind Kind
	mime string
	// iosPlays is whether AVPlayer plays it without conversion.
	iosPlays bool
}

var formats = map[string]format{
	".mp4":  {Video, "video/mp4", true},
	".m4v":  {Video, "video/x-m4v", true},
	".mov":  {Video, "video/quicktime", true},
	".webm": {Video, "video/webm", false},
	".mkv":  {Video, "video/x-matroska", false},
	".mp3":  {Audio, "audio/mpeg", true},
	".m4a":  {Audio, "audio/mp4", true},
	".aac":  {Audio, "audio/aac", true},
	".wav":  {Audio, "audio/wav", true},
	".aif":  {Audio, "audio/aiff", true},
	".aiff": {Audio, "audio/aiff", true},
	".flac": {Audio, "audio/flac", true},
	".ogg":  {Audio, "audio/ogg", false},
	".oga":  {Audio, "audio/ogg", false},
	".opus": {Audio, "audio/ogg", false},
}

// IsMediaPath reports whether the name has an audio/video extension we handle.
func IsMediaPath(name string) bool {
	_, ok := formats[strings.ToLower(filepath.Ext(name))]
	return ok
}

// MimeType returns the Content-Type for a media file name.
func MimeType(name string) string {
	return formats[strings.ToLower(filepath.Ext(name))].mime
}

// Reference is one place in the conversation that named a media file.
type Reference struct {
	// Path as written (absolute, ~/…, or relative to the working directory).
	Path      string
	Title     string // from show_media
	Shown     bool   // presented with show_media
	MessageID string
	ToolUseID string
	Sequence  int
	At        time.Time
}

// Message is the part of a stored message the scanner reads.
type Message struct {
	ID       string
	Sequence int
	Role     string
	Content  string
	ToolUses json.RawMessage
	At       time.Time
}

// pathRE finds media paths in free text and commands: an optional ~/ ./ ../
// or / start, then characters a path usually has, ending in a media extension.
// Paths with spaces are only found through show_media.
var pathRE = regexp.MustCompile(`(?i)(?:~/|\.{1,2}/|/)?[A-Za-z0-9_@%+=,.\-/]*[A-Za-z0-9_\-]\.(?:mp4|m4v|mov|webm|mkv|mp3|m4a|aac|wav|aiff?|flac|ogg|oga|opus)\b`)

// FindReferences lists every media path the messages name, oldest first.
// User messages are skipped: a path the user typed is not the agent's output.
func FindReferences(messages []Message) []Reference {
	var refs []Reference
	for _, m := range messages {
		if m.Role == "user" {
			continue
		}
		var tools []struct {
			ID    string                 `json:"id"`
			Name  string                 `json:"name"`
			Input map[string]interface{} `json:"input"`
		}
		if len(m.ToolUses) > 0 {
			_ = json.Unmarshal(m.ToolUses, &tools)
		}
		for _, t := range tools {
			base := m
			if isShowMedia(t.Name) {
				if p, _ := t.Input["path"].(string); strings.TrimSpace(p) != "" {
					title, _ := t.Input["title"].(string)
					refs = append(refs, Reference{Path: strings.TrimSpace(p), Title: strings.TrimSpace(title), Shown: true,
						MessageID: base.ID, ToolUseID: t.ID, Sequence: base.Sequence, At: base.At})
				}
				continue
			}
			for _, s := range stringsIn(t.Input) {
				for _, p := range pathRE.FindAllString(s, -1) {
					refs = append(refs, Reference{Path: p, MessageID: base.ID, ToolUseID: t.ID, Sequence: base.Sequence, At: base.At})
				}
			}
		}
		for _, p := range pathRE.FindAllString(m.Content, -1) {
			refs = append(refs, Reference{Path: p, MessageID: m.ID, Sequence: m.Sequence, At: m.At})
		}
	}
	return refs
}

func isShowMedia(name string) bool {
	return name == ShowMediaTool || strings.HasSuffix(name, "__"+ShowMediaTool)
}

// stringsIn returns every string value in a decoded JSON value.
func stringsIn(v interface{}) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case map[string]interface{}:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []string
		for _, k := range keys {
			out = append(out, stringsIn(x[k])...)
		}
		return out
	case []interface{}:
		var out []string
		for _, e := range x {
			out = append(out, stringsIn(e)...)
		}
		return out
	}
	return nil
}

// Roots are the folders a session's media may live in.
type Roots struct {
	Home       string
	WorkingDir string
	ProjectDir string
	// TempDirs are where Claude Code keeps scratchpads (/tmp, /private/tmp).
	TempDirs []string
	UID      int
}

// allowed returns the real (symlink-resolved) folders media may be served from.
func (r Roots) allowed() []string {
	var dirs []string
	add := func(d string) {
		if d == "" || !filepath.IsAbs(d) {
			return
		}
		if real, err := filepath.EvalSymlinks(d); err == nil && !tooBroad(real, r.Home) {
			dirs = append(dirs, real)
		}
	}
	add(r.WorkingDir)
	add(r.ProjectDir)
	// Claude Code's scratchpad for this working directory:
	// <tmp>/claude-<uid>/<working dir with every non-alphanumeric as ->/<session>/scratchpad
	if r.WorkingDir != "" {
		slug := claudeSlug(r.WorkingDir)
		for _, t := range r.TempDirs {
			add(filepath.Join(t, fmt.Sprintf("claude-%d", r.UID), slug))
		}
	}
	return dirs
}

var slugRE = regexp.MustCompile(`[^A-Za-z0-9]`)

func claudeSlug(dir string) string { return slugRE.ReplaceAllString(dir, "-") }

// tooBroad refuses folders that would open up everything (/, home, system folders).
func tooBroad(dir, home string) bool {
	switch dir {
	case "/", "/Users", "/home", "/private", "/tmp", "/private/tmp", "/var", "/private/var", "/etc", "/private/etc",
		"/System", "/Library", "/usr", "/opt", "/Applications", "/Volumes":
		return true
	}
	return home != "" && (dir == home || dir == filepath.Dir(home))
}

// protected are places nothing is served from, wherever the roots point.
func (r Roots) protected() []string {
	if r.Home == "" {
		return nil
	}
	var out []string
	// Library/HTTPStorages, Cookies and WebKit hold apps' cookies and website
	// data, the desktop app's session cookie among them.
	for _, rel := range []string{".claude", ".codex", ".ssh", ".gnupg", ".aws", ".config", "Library/Keychains",
		"Library/HTTPStorages", "Library/Cookies", "Library/WebKit"} {
		p := filepath.Join(r.Home, rel)
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		out = append(out, p)
	}
	return out
}

func within(root, target string) bool {
	if target == root {
		return true
	}
	return strings.HasPrefix(target, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}

// Errors from Resolve.
var (
	ErrNotMedia  = errors.New("not an audio or video file")
	ErrOutside   = errors.New("outside the session's folders")
	ErrProtected = errors.New("in a protected folder")
	ErrMissing   = errors.New("file not found")
	ErrTooLarge  = errors.New("file too large")
)

// Resolve turns a referenced path into the real file to serve, or says why not.
func (r Roots) Resolve(ref string) (string, os.FileInfo, error) {
	return r.resolve(ref, IsMediaPath, LooksLikeMedia, MaxFileSize, ErrNotMedia)
}

// resolve applies the folder rules shared by media and images: the named
// kind of file (by extension, before and after resolving symlinks, and by its
// first bytes), inside an allowed folder, never in a protected one, at most max.
func (r Roots) resolve(ref string, isKind func(string) bool, looksLike func(string) bool, max int64, errKind error) (string, os.FileInfo, error) {
	p := strings.TrimSpace(ref)
	if !isKind(p) {
		return "", nil, errKind
	}
	switch {
	case strings.HasPrefix(p, "~/"):
		if r.Home == "" {
			return "", nil, ErrOutside
		}
		p = filepath.Join(r.Home, p[2:])
	case !filepath.IsAbs(p):
		if r.WorkingDir == "" {
			return "", nil, ErrOutside
		}
		p = filepath.Join(r.WorkingDir, p)
	}
	p = filepath.Clean(p)
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", nil, ErrMissing
	}
	// The resolved file must still be that kind (a symlink named x.mp4 or
	// x.png pointing at a database is not).
	if !isKind(real) {
		return "", nil, errKind
	}
	for _, d := range r.protected() {
		if within(d, real) {
			return "", nil, ErrProtected
		}
	}
	ok := false
	for _, d := range r.allowed() {
		if within(d, real) {
			ok = true
			break
		}
	}
	if !ok {
		return "", nil, ErrOutside
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil, ErrMissing
	}
	if info.Size() > max {
		return "", nil, ErrTooLarge
	}
	if !looksLike(real) {
		return "", nil, errKind
	}
	return real, info, nil
}

// LooksLikeMedia checks the file's first bytes against audio/video signatures.
func LooksLikeMedia(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 16)
	n, _ := io.ReadFull(f, head)
	return sniff(head[:n])
}

func sniff(b []byte) bool {
	has := func(off int, sig string) bool {
		return len(b) >= off+len(sig) && string(b[off:off+len(sig)]) == sig
	}
	switch {
	case has(4, "ftyp"): // mp4, m4v, mov, m4a
		return true
	case has(4, "moov"), has(4, "mdat"), has(4, "wide"), has(4, "free"): // older QuickTime
		return true
	case has(0, "\x1a\x45\xdf\xa3"): // webm, mkv
		return true
	case has(0, "ID3"): // mp3 with tags
		return true
	case has(0, "RIFF") && has(8, "WAVE"):
		return true
	case has(0, "FORM") && (has(8, "AIFF") || has(8, "AIFC")):
		return true
	case has(0, "fLaC"), has(0, "OggS"):
		return true
	case len(b) >= 2 && b[0] == 0xFF && b[1]&0xE0 == 0xE0: // mp3 / ADTS aac frame sync
		return true
	}
	return false
}

// Item is one playable file of a session.
type Item struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Title    string    `json:"title,omitempty"`
	Path     string    `json:"path"` // for display: relative to the working directory, or ~/…
	Kind     Kind      `json:"kind"`
	MimeType string    `json:"mime_type"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	// Shown is true when the agent presented it with show_media.
	Shown bool `json:"shown"`
	// Where it was last named, so clients can put the player under that call.
	MessageID  string   `json:"message_id,omitempty"`
	ToolUseID  string   `json:"tool_use_id,omitempty"`
	ToolUseIDs []string `json:"tool_use_ids,omitempty"`
	Sequence   int      `json:"sequence"`
	// IOSPlays is false for formats iPhone's player can't open (WebM, MKV, Ogg).
	IOSPlays bool `json:"ios_plays"`

	RealPath string `json:"-"`
}

// MediaID is a file's id: stable for its real path.
func MediaID(realPath string) string {
	sum := sha256.Sum256([]byte(realPath))
	return hex.EncodeToString(sum[:])[:20]
}

// Collect resolves the references into items, newest reference first, one per file.
func Collect(refs []Reference, roots Roots) []Item {
	byID := map[string]*Item{}
	failed := map[string]bool{}
	for _, ref := range refs {
		if failed[ref.Path] {
			continue
		}
		real, info, err := roots.Resolve(ref.Path)
		if err != nil {
			failed[ref.Path] = true
			continue
		}
		id := MediaID(real)
		it := byID[id]
		if it == nil {
			f := formats[strings.ToLower(filepath.Ext(real))]
			it = &Item{ID: id, Name: filepath.Base(real), Path: displayPath(real, roots), Kind: f.kind,
				MimeType: f.mime, Size: info.Size(), Modified: info.ModTime().UTC(), IOSPlays: f.iosPlays, RealPath: real}
			byID[id] = it
		}
		if ref.Shown {
			it.Shown = true
			if ref.Title != "" {
				it.Title = ref.Title
			}
		}
		if ref.ToolUseID != "" && !contains(it.ToolUseIDs, ref.ToolUseID) {
			it.ToolUseIDs = append(it.ToolUseIDs, ref.ToolUseID)
		}
		if ref.Sequence >= it.Sequence {
			it.Sequence = ref.Sequence
			it.MessageID = ref.MessageID
			if ref.ToolUseID != "" {
				it.ToolUseID = ref.ToolUseID
			}
		}
	}
	items := make([]Item, 0, len(byID))
	for _, it := range byID {
		items = append(items, *it)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Sequence != items[j].Sequence {
			return items[i].Sequence > items[j].Sequence
		}
		return items[i].Modified.After(items[j].Modified)
	})
	return items
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func displayPath(real string, r Roots) string {
	if r.WorkingDir != "" {
		if wd, err := filepath.EvalSymlinks(r.WorkingDir); err == nil && within(wd, real) {
			if rel, err := filepath.Rel(wd, real); err == nil {
				return rel
			}
		}
	}
	if r.Home != "" && within(r.Home, real) {
		return "~/" + strings.TrimPrefix(real, strings.TrimSuffix(r.Home, "/")+"/")
	}
	return real
}

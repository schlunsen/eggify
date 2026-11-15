package handlers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// sendCheckedFile sends a file whose path the caller already validated.
//
// SECURITY: fiber's SendFile turns the path into a request URI: it
// percent-decodes it and cuts it at "?" and "#", so a checked ".../secret?x.mp4"
// is served as ".../secret" (possibly a symlink to the database). A checked
// path therefore never goes through SendFile. The file is opened here with
// O_NOFOLLOW on its last element (callers pass the path with symlinks already
// resolved), must still be the regular file at that path, and is streamed
// with Range support. The caller sets Content-Type and Content-Disposition
// first; sendCheckedFile only sets them when they are empty.
func sendCheckedFile(c *fiber.Ctx, absPath string) error {
	f, err := os.OpenFile(absPath, os.O_RDONLY|openNoFollow, 0)
	if err != nil {
		return fiber.ErrNotFound
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return fiber.ErrNotFound
	}
	if lst, err := os.Lstat(absPath); err != nil || !os.SameFile(info, lst) {
		_ = f.Close()
		return fiber.ErrNotFound
	}
	size := info.Size()
	if len(c.Response().Header.ContentType()) == 0 {
		c.Set(fiber.HeaderContentType, fiber.MIMEOctetStream)
	}
	c.Set(fiber.HeaderAcceptRanges, "bytes")
	if size == 0 {
		// An empty file (.gitkeep, __init__.py) has no byte range to stream.
		_ = f.Close()
		return c.Status(fiber.StatusOK).Send([]byte{})
	}

	start, end, partial, ok := parseByteRange(c.Get(fiber.HeaderRange), size)
	if !ok {
		_ = f.Close()
		c.Set(fiber.HeaderContentRange, fmt.Sprintf("bytes */%d", size))
		return c.SendStatus(fiber.StatusRequestedRangeNotSatisfiable)
	}
	if partial {
		c.Status(fiber.StatusPartialContent)
		c.Set(fiber.HeaderContentRange, fmt.Sprintf("bytes %d-%d/%d", start, end, size))
	}
	// SendStream closes f when the body has been written.
	return c.SendStream(sectionFile{io.NewSectionReader(f, start, end-start+1), f}, int(end-start+1))
}

// sectionFile is a byte range of a file that closes the file.
type sectionFile struct {
	*io.SectionReader
	f *os.File
}

func (s sectionFile) Close() error { return s.f.Close() }

// parseByteRange reads a single "bytes=a-b", "bytes=a-" or "bytes=-n" range.
// No header, or one it does not understand, is the whole file. ok is false
// for a range outside the file.
func parseByteRange(h string, size int64) (start, end int64, partial, ok bool) {
	whole := func() (int64, int64, bool, bool) { return 0, max(size-1, 0), false, true }
	h = strings.TrimSpace(h)
	if h == "" || !strings.HasPrefix(h, "bytes=") || strings.Contains(h, ",") {
		return whole()
	}
	a, b, found := strings.Cut(strings.TrimPrefix(h, "bytes="), "-")
	if !found {
		return whole()
	}
	if size == 0 {
		return whole()
	}
	if a == "" { // suffix: the last n bytes
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n <= 0 {
			return whole()
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true, true
	}
	s, err := strconv.ParseInt(a, 10, 64)
	if err != nil || s < 0 {
		return whole()
	}
	e := size - 1
	if b != "" {
		if v, err := strconv.ParseInt(b, 10, 64); err == nil && v >= s {
			e = min(v, size-1)
		} else {
			return whole()
		}
	}
	if s >= size {
		return 0, 0, false, false
	}
	return s, e, true, true
}

// resolvedProjectFile returns the path with symlinks resolved, for
// sendCheckedFile. resolveProjectPath checked containment on an earlier
// resolve; a link swapped since then would otherwise be followed, so the
// path sent is checked again against the project's real root.
func resolvedProjectFile(projectRoot, absPath string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", err
	}
	if !pathWithin(realRoot, real) {
		return "", fmt.Errorf("path outside project root")
	}
	return real, nil
}

// safeFileTypes are the Content-Types the file routes keep for a project file:
// images (no SVG), audio, video, PDF, plain text and JSON. Everything else is
// text/plain (text-like content) or application/octet-stream, so scripts,
// markup, SVG and any type a browser could run or render from the dashboard's
// origin never keep their own.
var safeFileTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".avif": "image/avif", ".bmp": "image/bmp", ".ico": "image/x-icon",
	".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime", ".ogv": "video/ogg",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg", ".flac": "audio/flac",
	".aac": "audio/aac", ".m4a": "audio/mp4", ".opus": "audio/opus",
	".pdf":  "application/pdf",
	".txt":  "text/plain; charset=utf-8",
	".md":   "text/plain; charset=utf-8",
	".log":  "text/plain; charset=utf-8",
	".csv":  "text/plain; charset=utf-8",
	".vtt":  "text/vtt; charset=utf-8",
	".json": "application/json",
}

// projectFileContentType decides the Content-Type of a project file sent by
// the file routes: the allowlisted type when both the requested name and the
// resolved file have the same safe extension, else text/plain for text-like
// content and application/octet-stream for the rest.
func projectFileContentType(name, realPath string) string {
	t := safeFileTypes[strings.ToLower(filepath.Ext(name))]
	if t != "" && t == safeFileTypes[strings.ToLower(filepath.Ext(realPath))] {
		return t
	}
	f, err := os.Open(realPath)
	if err != nil {
		return fiber.MIMEOctetStream
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	if strings.HasPrefix(http.DetectContentType(buf[:n]), "text/") {
		return "text/plain; charset=utf-8"
	}
	return fiber.MIMEOctetStream
}

// attachmentDisposition builds a Content-Disposition for name with quotes,
// backslashes and control characters removed from the filename.
func attachmentDisposition(kind, name string) string {
	clean := strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, filepath.Base(name))
	return kind + `; filename="` + clean + `"`
}

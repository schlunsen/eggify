package sessionmedia

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Images an agent shows in its reply (![alt](output/chart.png)) are served
// by the same folder rules as audio and video (Roots), one file per request.

// MaxImageSize is the largest image served.
const MaxImageSize = 50 << 20

// ErrNotImage is returned for a path that is not a supported image.
var ErrNotImage = errors.New("not an image file")

var imageTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".bmp":  "image/bmp",
	".svg":  "image/svg+xml",
}

// IsImagePath reports whether name has a supported image extension.
func IsImagePath(name string) bool {
	_, ok := imageTypes[strings.ToLower(filepath.Ext(name))]
	return ok
}

// ImageMimeType returns the Content-Type for an image file name.
func ImageMimeType(name string) string {
	return imageTypes[strings.ToLower(filepath.Ext(name))]
}

// ResolveImage turns an image path from a reply into the real file to serve.
func (r Roots) ResolveImage(ref string) (string, os.FileInfo, error) {
	return r.resolve(ref, IsImagePath, LooksLikeImage, MaxImageSize, ErrNotImage)
}

// LooksLikeImage checks the file's first bytes against image signatures (an
// SVG must start with markup and contain <svg early on).
func LooksLikeImage(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 1024)
	n, _ := io.ReadFull(f, head)
	return sniffImage(head[:n], strings.ToLower(filepath.Ext(path)) == ".svg")
}

func sniffImage(b []byte, svg bool) bool {
	if svg {
		t := bytes.TrimLeft(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), " \t\r\n")
		return bytes.HasPrefix(t, []byte("<")) && bytes.Contains(bytes.ToLower(b), []byte("<svg"))
	}
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")),
		bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}),
		bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")),
		bytes.HasPrefix(b, []byte("BM")):
		return true
	case len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return true
	case len(b) >= 12 && bytes.Equal(b[4:8], []byte("ftyp")) && (bytes.Equal(b[8:12], []byte("avif")) || bytes.Equal(b[8:12], []byte("avis"))):
		return true
	}
	return false
}

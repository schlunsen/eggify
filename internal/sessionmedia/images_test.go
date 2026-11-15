package sessionmedia

import (
	"os"
	"path/filepath"
	"testing"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func TestResolveImageServesTheSessionsOwnImages(t *testing.T) {
	roots, project, scratch := testRoots(t)
	write(t, filepath.Join(project, "output", "imagegen", "pop-art-woman.png"), pngBytes)
	write(t, filepath.Join(scratch, "chart.png"), pngBytes)
	write(t, filepath.Join(project, "logo.svg"), []byte("<?xml version=\"1.0\"?>\n<svg xmlns=\"http://www.w3.org/2000/svg\"/>"))
	for _, ref := range []string{"output/imagegen/pop-art-woman.png", filepath.Join(scratch, "chart.png"), "logo.svg"} {
		if _, _, err := roots.ResolveImage(ref); err != nil {
			t.Errorf("%s: %v", ref, err)
		}
	}
	if got := ImageMimeType("a.JPG"); got != "image/jpeg" {
		t.Errorf("mime %q", got)
	}
}

func TestRedTeam_SessionImage_RefusesWhatIsNotTheSessionsImage(t *testing.T) {
	roots, project, _ := testRoots(t)
	write(t, filepath.Join(project, "fake.png"), []byte("SQLite format 3\x00"))
	db := filepath.Join(roots.Home, ".claude", "eggify", "eggify.db")
	write(t, db, []byte("SQLite format 3\x00"))
	if err := os.Symlink(db, filepath.Join(project, "db.png")); err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(roots.Home, ".ssh", "id_ed25519.png")
	write(t, shot, pngBytes)
	if err := os.Symlink(shot, filepath.Join(project, "key.png")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(roots.Home, "Pictures", "private.png"), pngBytes)
	write(t, filepath.Join(project, "page.svg"), []byte("hello, not markup"))

	cases := map[string]error{
		"fake.png":                  ErrNotImage,
		"db.png":                    ErrNotImage,
		"key.png":                   ErrProtected,
		"../../.ssh/id_ed25519.png": ErrProtected,
		"~/Pictures/private.png":    ErrOutside,
		"../../../etc/hosts":        ErrNotImage,
		"page.svg":                  ErrNotImage,
		"output/does-not-exist.png": ErrMissing,
	}
	for ref, want := range cases {
		if _, _, err := roots.ResolveImage(ref); err != want {
			t.Errorf("%s: got %v, want %v", ref, err, want)
		}
	}
	// A video is not an image, and an image is not media.
	write(t, filepath.Join(project, "clip.mp4"), mp4Bytes)
	if _, _, err := roots.ResolveImage("clip.mp4"); err != ErrNotImage {
		t.Errorf("clip.mp4 as image: %v", err)
	}
	if _, _, err := roots.Resolve("output/imagegen/x.png"); err != ErrNotMedia {
		t.Errorf("png as media: %v", err)
	}
}

func TestSniffImage(t *testing.T) {
	ok := [][]byte{pngBytes, {0xFF, 0xD8, 0xFF, 0xE0}, []byte("GIF89a"), []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), []byte("\x00\x00\x00\x1cftypavif"), []byte("BM\x00\x00")}
	for _, b := range ok {
		if !sniffImage(b, false) {
			t.Errorf("not recognised: %q", b)
		}
	}
	if sniffImage([]byte("SQLite format 3"), false) || sniffImage([]byte("<html>"), true) {
		t.Error("non-images recognised")
	}
}

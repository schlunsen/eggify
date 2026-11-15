package handlers

import (
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
)

func fileServingApp(t *testing.T, files map[string]string) (*fiber.App, string) {
	t.Helper()
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.ResetInstance)
	repo := database.NewRepository(db)
	root := t.TempDir()
	for name, data := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.CreateProject(&database.Project{ID: "p1", Name: "p", Path: root}); err != nil {
		t.Fatal(err)
	}
	h := NewFileBrowserHandler(repo)
	app := fiber.New()
	app.Get("/projects/:id/files/raw", h.HandleRawFile)
	app.Get("/projects/:id/files/download", h.HandleDownloadFile)
	return app, "p1"
}

// TestRedTeam_FileRoutes_ScriptsAndMarkupAreNeverServedAsActiveContent: an
// agent-written .js (or .html/.svg) used to come back as text/javascript from
// the dashboard origin, where script-src 'self' lets a page run it.
func TestRedTeam_FileRoutes_ScriptsAndMarkupAreNeverServedAsActiveContent(t *testing.T) {
	names := []string{"a.js", "a.mjs", "a.html", "a.htm", "a.svg", "a.xhtml", "a.xml", "a.ecma", "a.es", "a.jsx", "a.wasm"}
	files := map[string]string{}
	for _, n := range names {
		files[n] = "alert(1)"
	}
	app, pid := fileServingApp(t, files)
	for _, route := range []string{"raw", "download"} {
		for _, n := range names {
			resp, err := app.Test(httptest.NewRequest("GET", "/projects/"+pid+"/files/"+route+"?path="+url.QueryEscape(n), nil))
			if err != nil {
				t.Fatal(err)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
				t.Errorf("%s %s: Content-Type %q, want text/plain", route, n, ct)
			}
			if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("%s %s: missing nosniff", route, n)
			}
			if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
				t.Errorf("%s %s: Content-Disposition %q, want attachment", route, n, cd)
			}
		}
	}
}

// TestRedTeam_FileRoutes_PercentInFileNameCannotMoveTheServedPath: fiber's
// SendFile decodes %xx again after the path was checked, so a file named
// "x%41.txt" was served as "xA.txt" (and "%2e%2e%2f" names could climb).
func TestRedTeam_FileRoutes_PercentInFileNameCannotMoveTheServedPath(t *testing.T) {
	// "secret" stands for a file the name would be cut down to ("?" and "#"
	// end the request URI SendFile builds); "xA.txt" for the decoded name.
	app, pid := fileServingApp(t, map[string]string{
		"x%41.txt":    "the checked file",
		"xA.txt":      "another file",
		"secret?.mp4": "checked question mark",
		"secret":      "SECRET (cut at ?)",
		"a#b.mp4":     "checked hash",
		"a":           "SECRET (cut at #)",
	})
	for _, route := range []string{"raw", "download"} {
		for name, want := range map[string]string{
			"x%41.txt": "the checked file", "secret?.mp4": "checked question mark", "a#b.mp4": "checked hash",
		} {
			resp, err := app.Test(httptest.NewRequest("GET", "/projects/"+pid+"/files/"+route+"?path="+url.QueryEscape(name), nil))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			if string(b) != want {
				t.Errorf("%s %q: served %q (status %d), want the file that was checked", route, name, b, resp.StatusCode)
			}
		}
	}
}

// TestRedTeam_FileRoutes_CheckedFileIsOpenedNoFollowAndStreamed: sendCheckedFile
// refuses a symlink at the checked path (swapped in after the check), keeps
// Range support, and the types outside the allowlist lose their own type.
func TestRedTeam_FileRoutes_CheckedFileIsOpenedNoFollowAndStreamed(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret.db")
	real := filepath.Join(dir, "clip.mp4")
	link := filepath.Join(dir, "link.mp4")
	for p, c := range map[string]string{secret: "SECRET", real: "0123456789"} {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/f", func(c *fiber.Ctx) error { return sendCheckedFile(c, c.Query("p")) })
	get := func(p, rng string) (int, string, string) {
		r := httptest.NewRequest("GET", "/f?p="+url.QueryEscape(p), nil)
		if rng != "" {
			r.Header.Set("Range", rng)
		}
		resp, err := app.Test(r)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header.Get("Content-Range")
	}
	if code, body, _ := get(link, ""); code != 404 || strings.Contains(body, "SECRET") {
		t.Fatalf("symlink at the checked path was followed: %d %q", code, body)
	}
	if code, body, _ := get(real, ""); code != 200 || body != "0123456789" {
		t.Fatalf("whole file: %d %q", code, body)
	}
	if code, body, cr := get(real, "bytes=2-4"); code != 206 || body != "234" || cr != "bytes 2-4/10" {
		t.Fatalf("range: %d %q %q", code, body, cr)
	}
	if code, body, _ := get(real, "bytes=-3"); code != 206 || body != "789" {
		t.Fatalf("suffix range: %d %q", code, body)
	}
	if code, _, _ := get(real, "bytes=50-"); code != 416 {
		t.Fatalf("range past the end: %d", code)
	}
	// An empty file is an empty 200, with or without a Range.
	empty := filepath.Join(dir, ".gitkeep")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rng := range []string{"", "bytes=0-", "bytes=-5"} {
		if code, body, _ := get(empty, rng); code != 200 || body != "" {
			t.Fatalf("empty file (Range %q): %d %q", rng, code, body)
		}
	}

	// Allowlist: json keeps its type, everything else loses it.
	app2, pid := fileServingApp(t, map[string]string{"a.json": "{}", "b.ecma": "x=1", "c.bin": "\x00\x01\x02\x03", "d.png": "\x89PNG"})
	for name, want := range map[string]string{"a.json": "application/json", "b.ecma": "text/plain", "c.bin": "application/octet-stream", "d.png": "image/png"} {
		resp, err := app2.Test(httptest.NewRequest("GET", "/projects/"+pid+"/files/download?path="+url.QueryEscape(name), nil))
		if err != nil {
			t.Fatal(err)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, want) {
			t.Errorf("%s: Content-Type %q, want %s", name, ct, want)
		}
	}
}

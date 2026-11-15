package handlers

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/git"
)

func stubCloneResolver(t *testing.T) {
	t.Helper()
	prev := git.LookupIP
	git.LookupIP = func(_ context.Context, host string) ([]net.IP, error) {
		if host == "evil.example" {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		}
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { git.LookupIP = prev })
}

// Attack: POST /api/git/clone at this machine (SSRF): odd IPv4 notations and a
// name that resolves to 127.0.0.1. All refused before git runs.
func TestRedTeam_GitCloneSSRF(t *testing.T) {
	stubCloneResolver(t)
	base := t.TempDir()
	h := NewGitHandler(nil, t.TempDir())
	app := fiber.New()
	app.Post("/api/git/clone", h.HandleCloneRepository)
	for _, u := range []string{"https://127.1/x", "https://127.1/o/r", "https://2130706433/o/r", "https://evil.example/o/r", "http://github.com/o/r"} {
		body := `{"clone_url":"` + u + `","owner":"o","repo_name":"r","custom_path":"` + base + `"}`
		req := httptest.NewRequest("POST", "/api/git/clone", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 200 {
			t.Errorf("clone of %s allowed: %s", u, b)
		}
		if _, err := os.Stat(filepath.Join(base, "o", "r")); err == nil {
			t.Errorf("clone of %s created the target folder", u)
		}
	}

	// The new-project flow refuses the same, literal forms at parse time and
	// resolved names when the clone runs.
	for _, u := range []string{"https://127.1/o/r", "https://2130706433/o/r", "https://0x7f000001/o/r"} {
		if _, err := parseCloneSource(u); err == nil {
			t.Errorf("parseCloneSource(%q) allowed", u)
		}
	}
	if err := runGitClone(context.Background(), "https://evil.example/o/r.git", filepath.Join(base, "x"), 0, func(string) {}); !errors.Is(err, git.ErrCloneHostBlocked) {
		t.Errorf("runGitClone to a name resolving to 127.0.0.1 = %v, want ErrCloneHostBlocked", err)
	}
}

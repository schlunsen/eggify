package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// TestRedTeam_SetupRefusesCrossSiteForms: until an account exists the setup
// route is public; a page the user visits must not be able to post a form to
// http://127.0.0.1:3335/api/auth/setup and choose the admin password.
func TestRedTeam_SetupRefusesCrossSiteForms(t *testing.T) {
	s := &Server{}
	app := fiber.New()
	app.Post("/api/auth/setup", s.handleInitialSetup)
	for name, req := range map[string]struct{ ctype, origin string }{
		"form, cross-site":  {"application/x-www-form-urlencoded", "https://evil.example"},
		"form, no origin":   {"application/x-www-form-urlencoded", ""},
		"text/plain json":   {"text/plain", "https://evil.example"},
		"json, cross-site":  {"application/json", "https://evil.example"},
		"json, null origin": {"application/json", "null"},
		"json, other port":  {"application/json", "http://127.0.0.1:8080"},
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:3335/api/auth/setup", strings.NewReader(`username=x&password=12345678`))
		r.Header.Set("Content-Type", req.ctype)
		if req.origin != "" {
			r.Header.Set("Origin", req.origin)
		}
		resp, err := app.Test(r)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, resp.StatusCode)
		}
	}
}

func TestSameOriginRequest(t *testing.T) {
	app := fiber.New()
	app.Post("/", func(c *fiber.Ctx) error {
		if sameOriginRequest(c) {
			return c.SendStatus(200)
		}
		return c.SendStatus(403)
	})
	for origin, want := range map[string]int{
		"":                         200,
		"http://127.0.0.1:3335":    200,
		"https://example.ngrok.io": 403,
		"null":                     403,
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:3335/", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		resp, _ := app.Test(r)
		if resp.StatusCode != want {
			t.Errorf("origin %q: %d, want %d", origin, resp.StatusCode, want)
		}
	}
	r := httptest.NewRequest("POST", "https://box.example/", nil)
	r.Header.Set("Origin", "https://box.example")
	if resp, _ := app.Test(r); resp.StatusCode != 200 {
		t.Errorf("https default port: %d", resp.StatusCode)
	}
}

// TestRedTeam_SetupRefusesRebindingHosts: over the loopback listener a Host
// that is a DNS name (not localhost or an IP) means DNS rebinding.
func TestRedTeam_SetupRefusesRebindingHosts(t *testing.T) {
	s := &Server{userStore: NewInMemoryUserStore()}
	app := fiber.New()
	app.Post("/api/auth/setup", s.handleInitialSetup)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	addr := ln.Addr().String()
	_, port, _ := net.SplitHostPort(addr)

	post := func(host string) int {
		req, _ := http.NewRequest("POST", "http://"+addr+"/api/auth/setup", strings.NewReader(`{}`))
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://"+host)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("evil.example:" + port); code != fiber.StatusForbidden {
		t.Fatalf("rebinding host: %d, want 403", code)
	}
	// Loopback names get past the host check (and fail later on the empty
	// body or the missing user store, not with the 403).
	for _, h := range []string{"127.0.0.1:" + port, "localhost:" + port} {
		if code := post(h); code == fiber.StatusForbidden {
			t.Fatalf("%s refused", h)
		}
	}
}

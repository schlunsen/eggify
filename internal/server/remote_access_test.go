package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/schlunsen/eggify/internal/remoteaccess"
)

// loopbackTunnelProvider stands in for ngrok: each Open is a fresh loopback
// listener (no network, no ngrok account).
type loopbackTunnelProvider struct {
	mu  sync.Mutex
	lns []net.Listener
}

type loopbackTunnel struct{ net.Listener }

func (l loopbackTunnel) URL() string    { return "https://fake.ngrok.test" }
func (l loopbackTunnel) Region() string { return "eu" }

func (p *loopbackTunnelProvider) Name() string { return "ngrok" }
func (p *loopbackTunnelProvider) Open(ctx context.Context, _ remoteaccess.OpenConfig) (remoteaccess.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.lns = append(p.lns, ln)
	p.mu.Unlock()
	return loopbackTunnel{ln}, nil
}

func (p *loopbackTunnelProvider) addr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lns[len(p.lns)-1].Addr().String()
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func httpGet(t *testing.T, addr, path string) (int, string) {
	t.Helper()
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Get("http://" + addr + path)
	if err != nil {
		t.Fatalf("GET %s%s: %v", addr, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// newRemoteAccessTestServer builds a Server with the real tunnel plumbing
// (path guard, serveTunnel on the app's fasthttp server) and a fake provider.
func newRemoteAccessTestServer(t *testing.T) (*Server, *loopbackTunnelProvider, net.Listener) {
	t.Helper()
	s := &Server{app: fiber.New(fiber.Config{DisableStartupMessage: true}), claudeDir: t.TempDir(), quiet: true}
	s.app.Use(s.tunnelPathGuard)
	ok := func(c *fiber.Ctx) error { return c.SendString("ok " + c.IP()) }
	s.app.Get("/wails/runtime.js", ok)
	s.app.Get("/__desktop/bootstrap", ok)
	s.app.Get("/internal/apps", ok)
	s.app.Get("/api/health", ok)

	prov := &loopbackTunnelProvider{}
	store, err := remoteaccess.NewStore(filepath.Join(s.claudeDir, "remote_access.json"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	s.remoteAccess = remoteaccess.NewManager(remoteaccess.Config{
		Provider: prov, Serve: s.serveTunnel, Store: store,
		Env: remoteaccess.EnvSettings{AuthToken: "env_token_1234567890"},
	})

	mainLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.app.Listener(mainLn) }()
	t.Cleanup(func() { _ = s.app.Shutdown() })
	return s, prov, mainLn
}

func TestRemoteAccessRunsAsExtraListener(t *testing.T) {
	s, prov, mainLn := newRemoteAccessTestServer(t)
	main := mainLn.Addr().String()
	if code, _ := httpGet(t, main, "/api/health"); code != 200 {
		t.Fatalf("main listener: %d", code)
	}
	for round := 0; round < 2; round++ {
		if err := s.remoteAccess.Start(); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, "tunnel connected", func() bool { return s.remoteAccess.Status().Status == remoteaccess.StateConnected })
		tun := prov.addr()
		if code, body := httpGet(t, tun, "/api/health"); code != 200 || body == "ok 127.0.0.1" {
			t.Fatalf("tunnel /api/health = %d %q (tunnel clients must not look like loopback)", code, body)
		}
		if err := s.remoteAccess.Stop(); err != nil {
			t.Fatal(err)
		}
		if _, err := net.DialTimeout("tcp", tun, 200*time.Millisecond); err == nil {
			t.Fatal("tunnel listener still open after Stop")
		}
		if code, _ := httpGet(t, main, "/api/health"); code != 200 {
			t.Fatalf("main listener after tunnel stop (round %d): %d", round, code)
		}
	}
	// fasthttp closes every listener it ever served on shutdown: a tunnel that
	// was stopped earlier must not make that fail.
	if err := s.app.Shutdown(); err != nil {
		t.Fatalf("Shutdown after tunnel stops: %v", err)
	}
}

func TestTunnelCannotReachLocalOnlyPaths(t *testing.T) {
	s, prov, mainLn := newRemoteAccessTestServer(t)
	if err := s.remoteAccess.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.remoteAccess.Stop()
	waitUntil(t, "tunnel connected", func() bool { return s.remoteAccess.Status().Status == remoteaccess.StateConnected })
	for _, p := range []string{"/wails/runtime.js", "/__desktop/bootstrap", "/internal/apps", "/WAILS/runtime.js", "//wails/runtime.js"} {
		if code, _ := httpGet(t, prov.addr(), p); code != fiber.StatusNotFound {
			t.Errorf("tunnel %s = %d, want 404", p, code)
		}
		// Locally they still work.
		if p != "/WAILS/runtime.js" && p != "//wails/runtime.js" {
			if code, _ := httpGet(t, mainLn.Addr().String(), p); code == fiber.StatusNotFound {
				t.Errorf("local %s = %d", p, code)
			}
		}
	}
}

func TestRemoteAccessGuard(t *testing.T) {
	s := &Server{}
	var g *remoteaccess.GuardError
	if err := s.remoteAccessGuard(); !errors.As(err, &g) || g.Code != "user_auth_disabled" {
		t.Fatalf("user auth off: %v", err)
	}
	us := NewInMemoryUserStore()
	s.userStore = us
	s.sessionAuthMiddleware = NewSessionAuthMiddleware(us, true, true)
	if err := s.remoteAccessGuard(); !errors.As(err, &g) || g.Code != "setup_required" {
		t.Fatalf("no admin yet: %v", err)
	}
	us.users["alice"] = &User{Username: "alice", IsAdmin: true}
	if err := s.remoteAccessGuard(); err != nil {
		t.Fatalf("user auth on with admin: %v", err)
	}
}

// Without user auth the tunnel refuses to start, also at boot via --tunnel.
func TestRemoteAccessRefusedWithoutUserAuth(t *testing.T) {
	s, prov, _ := newRemoteAccessTestServer(t)
	store, _ := remoteaccess.NewStore(filepath.Join(s.claudeDir, "ra2.json"), make([]byte, 32))
	s.remoteAccess = remoteaccess.NewManager(remoteaccess.Config{
		Provider: prov, Serve: s.serveTunnel, Store: store,
		Env:   remoteaccess.EnvSettings{AuthToken: "env_token_1234567890"},
		Guard: s.remoteAccessGuard,
	})
	s.config = &Config{}
	s.config.Tunnel.Enabled = true
	err := s.autostartRemoteAccess()
	var g *remoteaccess.GuardError
	if !errors.As(err, &g) || g.Code != "user_auth_disabled" {
		t.Fatalf("--tunnel without user auth: %v", err)
	}
	if len(prov.lns) != 0 || s.remoteAccess.Status().Running {
		t.Fatal("tunnel opened without user auth")
	}
	// Settings autostart (no CLI flag) only logs.
	s.config.Tunnel.Enabled = false
	if err := s.autostartRemoteAccess(); err != nil {
		t.Fatalf("settings autostart should not fail the server: %v", err)
	}
}

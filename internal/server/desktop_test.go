package server

import (
	"net"
	"testing"
)

func TestServeRequiresDesktopModeAndLoopback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	s := NewServerWithOptions(t.TempDir(), 0, true, false)
	if err := s.Serve(ln); err == nil {
		t.Fatal("Serve must refuse outside desktop mode")
	}

	s.EnableDesktopMode(DesktopOptions{})
	all, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind 0.0.0.0: %v", err)
	}
	defer all.Close()
	if err := s.Serve(all); err == nil {
		t.Fatal("Serve must refuse a non-loopback listener")
	}
}

func TestDesktopConfigForcesLoopbackHTTP(t *testing.T) {
	cfg := &Config{}
	cfg.TLS.Enabled = true
	cfg.Server.Host = "0.0.0.0"
	cfg.Tunnel.Enabled = true

	cli := NewServerWithOptions(t.TempDir(), 0, true, false)
	cli.applyDesktopConfig(cfg)
	if !cfg.TLS.Enabled || cfg.Server.Host != "0.0.0.0" || !cfg.Tunnel.Enabled {
		t.Fatal("CLI config must be untouched")
	}

	d := NewServerWithOptions(t.TempDir(), 0, true, false)
	d.EnableDesktopMode(DesktopOptions{})
	d.applyDesktopConfig(cfg)
	if cfg.TLS.Enabled || cfg.Server.Host != "127.0.0.1" || cfg.Tunnel.Enabled {
		t.Fatalf("desktop overrides not applied: tls=%v host=%q tunnel=%v", cfg.TLS.Enabled, cfg.Server.Host, cfg.Tunnel.Enabled)
	}
	if d.tlsConfig == nil || d.tlsConfig.Enabled {
		t.Fatal("desktop TLS config must be disabled")
	}
}

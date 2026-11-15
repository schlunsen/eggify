// eggify-relay is Eggify's remote-access relay (internal/relay): it forwards
// visitors' TLS connections to Eggify installs by SNI and can't decrypt them.
//
//	eggify-relay -listen :443 -control relay.example.com -suffix r.example.com \
//	  -cert /path/fullchain.pem -key /path/privkey.pem
//
// DNS: relay.example.com and *.r.example.com point at this server. The
// certificate covers the control name only (installs check it); visitors'
// TLS is served by the installs themselves. The files are re-read when they
// change, so certificate renewals need no restart.
package main

import (
	"crypto/ed25519"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/schlunsen/eggify/internal/relay"
)

func main() {
	listen := flag.String("listen", ":443", "address to listen on")
	control := flag.String("control", "", "the relay's own host name (installs connect to it), e.g. relay.example.com (required)")
	suffix := flag.String("suffix", "", "parent domain of install addresses (<id>.<suffix>), e.g. r.example.com (required)")
	certFile := flag.String("cert", "", "certificate chain (PEM) for the control name")
	keyFile := flag.String("key", "", "private key (PEM) for the control name")
	publicPort := flag.Int("public-port", 0, "port in install URLs (default: the listen port)")
	maxInstalls := flag.Int("max-installs", 10000, "maximum connected installs")
	allow := flag.String("allow", "", "file of install ids always admitted (one per line, # comments; re-read when it changes)")
	licenseKey := flag.String("license-key", "", "Eggify license server public key (raw Ed25519, standard base64; several separated by commas, for key rotation); with it, installs need a license covering the relay")
	// Per-source-address limits (IPv6 by /64); 0 = default, negative = off.
	pendingPerIP := flag.Int("max-pending-per-ip", 0, "connections per source address waiting for their ClientHello (default 32, -1 = no limit)")
	connsPerMin := flag.Int("max-conns-per-minute-per-ip", 0, "new connections per minute per source address (default 600, -1 = no limit)")
	streamsPerIP := flag.Int("max-streams-per-ip", 0, "concurrent visitor streams per source address (default 128, -1 = no limit)")
	controlPerIP := flag.Int("max-installs-per-ip", 0, "concurrently connected installs per source address (default 64, -1 = no limit)")
	controlPendingPerIP := flag.Int("max-install-handshakes-per-ip", 0, "install handshakes in flight per source address (default 64, -1 = no limit)")
	controlPerMin := flag.Int("max-install-conns-per-minute-per-ip", 0, "new install connections per minute per source address (default 120, -1 = no limit)")
	flag.Parse()
	if *control == "" || *suffix == "" {
		log.Fatal("-control and -suffix are required (for example -control relay.example.com -suffix r.example.com)")
	}
	if *certFile == "" || *keyFile == "" {
		log.Fatal("-cert and -key are required")
	}

	certs := &certReloader{certFile: *certFile, keyFile: *keyFile}
	if _, err := certs.get(nil); err != nil {
		log.Fatalf("loading certificate: %v", err)
	}
	port := *publicPort
	if port == 0 {
		if _, p, err := net.SplitHostPort(*listen); err == nil {
			port, _ = net.LookupPort("tcp", p)
		}
	}
	// Who may connect: allowlisted installs and licensed ones. Without
	// either flag the relay is open to every install.
	var allowlist *relay.Allowlist
	if *allow != "" {
		allowlist = &relay.Allowlist{Path: *allow}
	}
	var licensePubs []ed25519.PublicKey
	for _, part := range strings.Split(*licenseKey, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(part)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			log.Fatal("-license-key must be raw Ed25519 public keys in standard base64")
		}
		licensePubs = append(licensePubs, raw)
	}
	if allowlist == nil && len(licensePubs) == 0 {
		log.Print("warning: no -allow or -license-key; every install may use this relay")
	}
	srv, err := relay.NewServer(relay.ServerConfig{
		ControlName: *control,
		ControlTLS:  &tls.Config{GetCertificate: certs.get, MinVersion: tls.VersionTLS12},
		Suffix:      *suffix,
		PublicPort:  port,
		MaxInstalls: *maxInstalls,
		Allowlist:   allowlist,
		LicenseKeys: licensePubs,
		IPLimits: relay.IPLimits{
			PendingPerIP: *pendingPerIP, ConnsPerMinutePerIP: *connsPerMin, StreamsPerIP: *streamsPerIP,
			ControlPerIP: *controlPerIP, ControlPendingPerIP: *controlPendingPerIP, ControlPerMinutePerIP: *controlPerMin,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("eggify-relay listening on %s (control %s, installs *.%s)", *listen, *control, *suffix)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Print("shutting down")
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Fatal(err)
	}
}

// certReloader serves the certificate files, re-reading them at most once a
// minute when their modification time changed.
type certReloader struct {
	certFile, keyFile string

	mu      sync.Mutex
	cert    *tls.Certificate
	mtime   time.Time
	checked time.Time
}

func (r *certReloader) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cert != nil && time.Since(r.checked) < time.Minute {
		return r.cert, nil
	}
	r.checked = time.Now()
	st, err := os.Stat(r.certFile)
	if err != nil {
		if r.cert != nil {
			return r.cert, nil
		}
		return nil, err
	}
	if r.cert != nil && st.ModTime().Equal(r.mtime) {
		return r.cert, nil
	}
	c, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		if r.cert != nil {
			log.Printf("keeping the old certificate: %v", err)
			return r.cert, nil
		}
		return nil, err
	}
	if r.cert != nil {
		log.Print("certificate reloaded")
	}
	r.cert, r.mtime = &c, st.ModTime()
	return r.cert, nil
}

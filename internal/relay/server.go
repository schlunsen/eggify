package relay

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// ServerConfig configures a relay.
type ServerConfig struct {
	// ControlName is the relay's own host name (installs connect to it with
	// TLS validated against ControlTLS's certificate), e.g. relay.example.com.
	ControlName string
	// ControlTLS serves ControlName (a real certificate).
	ControlTLS *tls.Config
	// Suffix is the parent domain of install names: <id>.<Suffix>.
	Suffix string
	// PublicPort goes into install URLs when it isn't 443.
	PublicPort int
	// MaxInstalls caps connected installs (default 10000).
	MaxInstalls int
	// MaxStreamsPerInstall caps concurrent visitors per install (default 256).
	MaxStreamsPerInstall int
	// HelloTimeout bounds the wait for a visitor's ClientHello (default 10s).
	HelloTimeout time.Duration
	// Logf receives one line per install connect/disconnect (never traffic).
	Logf func(format string, args ...any)
	// Allowlist and LicenseKey limit who may connect (admission.go): an
	// install on the allowlist, or one with a valid license token signed by
	// LicenseKey. With neither set, every install is admitted.
	Allowlist  *Allowlist
	LicenseKey ed25519.PublicKey
	// LicenseKeys are further accepted license server keys (key rotation).
	LicenseKeys []ed25519.PublicKey
	// MaxIDsPerLicense caps the relay addresses (install ids) one license
	// install may hold at once (default DefaultMaxIDsPerLicense).
	MaxIDsPerLicense int
	// RequireBoundLicenseProof refuses the plain (v1) license proof of apps
	// that predate the bound one; off while those apps are still around.
	RequireBoundLicenseProof bool
	// AdmissionRecheck is how often admissions are re-checked (default 1 minute).
	AdmissionRecheck time.Duration
}

// Server is a running relay.
type Server struct {
	cfg ServerConfig

	mu       sync.Mutex
	installs map[string]*install
	closed   bool
	lns      []net.Listener
	// handshakes limits concurrent control handshakes.
	handshakes chan struct{}

	stop      chan struct{}
	watchOnce sync.Once
}

type install struct {
	id      string
	session *yamux.Session
	streams chan struct{}
	// until ends the admission (zero: never); viaAllowlist installs are
	// re-checked against the file; licenseInstall counts toward the cap per license.
	until          time.Time
	viaAllowlist   bool
	licenseInstall string
}

// NewServer validates cfg and returns a relay; call Serve.
func NewServer(cfg ServerConfig) (*Server, error) {
	cfg.ControlName = strings.ToLower(strings.TrimSuffix(cfg.ControlName, "."))
	cfg.Suffix = strings.ToLower(strings.Trim(cfg.Suffix, "."))
	if cfg.ControlName == "" || cfg.Suffix == "" || cfg.ControlTLS == nil {
		return nil, errors.New("relay: ControlName, ControlTLS and Suffix are required")
	}
	if cfg.MaxInstalls <= 0 {
		cfg.MaxInstalls = 10000
	}
	if cfg.MaxStreamsPerInstall <= 0 {
		cfg.MaxStreamsPerInstall = 256
	}
	if cfg.HelloTimeout <= 0 {
		cfg.HelloTimeout = 10 * time.Second
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	if cfg.MaxIDsPerLicense <= 0 {
		cfg.MaxIDsPerLicense = DefaultMaxIDsPerLicense
	}
	if cfg.AdmissionRecheck <= 0 {
		cfg.AdmissionRecheck = time.Minute
	}
	return &Server{cfg: cfg, installs: map[string]*install{}, handshakes: make(chan struct{}, 256), stop: make(chan struct{})}, nil
}

// URLFor is the public address of install id.
func (s *Server) URLFor(id string) string {
	host := id + "." + s.cfg.Suffix
	if s.cfg.PublicPort != 0 && s.cfg.PublicPort != 443 {
		host = net.JoinHostPort(host, strconv.Itoa(s.cfg.PublicPort))
	}
	return "https://" + host
}

// Serve accepts connections on ln until it is closed.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return net.ErrClosed
	}
	s.lns = append(s.lns, ln)
	s.mu.Unlock()
	for {
		c, err := ln.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		go s.handle(c)
	}
}

// Close stops the listeners and drops every install.
func (s *Server) Close() error {
	s.mu.Lock()
	if !s.closed {
		close(s.stop)
	}
	s.closed = true
	lns, installs := s.lns, s.installs
	s.lns, s.installs = nil, map[string]*install{}
	s.mu.Unlock()
	for _, ln := range lns {
		_ = ln.Close()
	}
	for _, in := range installs {
		_ = in.session.Close()
	}
	return nil
}

// Installs is the number of connected installs.
func (s *Server) Installs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.installs)
}

func (s *Server) handle(c net.Conn) {
	name, hello, err := peekServerName(c, s.cfg.HelloTimeout)
	if err != nil {
		_ = c.Close()
		return
	}
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	pc := newPrefixConn(c, hello)
	if name == s.cfg.ControlName {
		s.handleControl(pc)
		return
	}
	id, ok := strings.CutSuffix(name, "."+s.cfg.Suffix)
	if !ok || !ValidID(id) {
		_ = c.Close()
		return
	}
	s.handleVisitor(id, pc)
}

func (s *Server) handleControl(raw net.Conn) {
	select {
	case s.handshakes <- struct{}{}:
	default:
		_ = raw.Close()
		return
	}
	released := false
	release := func() {
		if !released {
			released = true
			<-s.handshakes
		}
	}
	defer release()

	c := tls.Server(raw, s.cfg.ControlTLS)
	_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
	fail := func(code, msg string, permanent bool) {
		_ = writeLine(c, resultMsg{Error: msg, Permanent: permanent, Code: code})
		_ = c.Close()
	}
	br := bufio.NewReaderSize(c, maxLineBytes)
	var hello helloMsg
	if err := readLine(br, &hello); err != nil {
		_ = c.Close()
		return
	}
	if hello.V != ProtocolVersion {
		fail(CodeBadVersion, "unsupported relay protocol version; update Eggify", true)
		return
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		_ = c.Close()
		return
	}
	if err := writeLine(c, challengeMsg{Nonce: nonce}); err != nil {
		_ = c.Close()
		return
	}
	var proof proofMsg
	if err := readLine(br, &proof); err != nil {
		_ = c.Close()
		return
	}
	if err := verifyProof(hello.SPKI, s.cfg.ControlName, nonce, proof.Sig); err != nil {
		fail(CodeBadProof, "identity proof failed: "+err.Error(), true)
		return
	}
	id := IDFromSPKI(hello.SPKI)
	adm := s.admit(id, hello, proof, nonce, time.Now())
	if !adm.ok {
		s.cfg.Logf("relay: install %s refused from %s: %s", id, remoteIP(raw), adm.reason)
		fail(adm.code, adm.message, adm.permanent)
		return
	}

	s.mu.Lock()
	_, replacing := s.installs[id]
	full := !replacing && len(s.installs) >= s.cfg.MaxInstalls
	overCap := s.overLicenseCapLocked(id, adm.licenseInstall)
	s.mu.Unlock()
	if full {
		fail(CodeRelayFull, "relay is full, try again later", false)
		return
	}
	if overCap {
		s.cfg.Logf("relay: install %s refused from %s: license already holds %d relay addresses", id, remoteIP(raw), s.cfg.MaxIDsPerLicense)
		fail(CodeTooManyInstalls, tooManyInstallsMessage, false)
		return
	}
	if err := writeLine(c, resultMsg{OK: true, ID: id, URL: s.URLFor(id)}); err != nil {
		_ = c.Close()
		return
	}
	_ = c.SetDeadline(time.Time{})

	ycfg := yamux.DefaultConfig()
	ycfg.LogOutput = io.Discard
	session, err := yamux.Client(&bufferedConn{Conn: c, r: br}, ycfg)
	if err != nil {
		_ = c.Close()
		return
	}
	in := &install{id: id, session: session, streams: make(chan struct{}, s.cfg.MaxStreamsPerInstall),
		until: adm.until, viaAllowlist: adm.viaAllowlist, licenseInstall: adm.licenseInstall}
	s.mu.Lock()
	if s.closed || s.overLicenseCapLocked(id, adm.licenseInstall) {
		// Closed, or concurrent handshakes of one license filled the cap
		// since the check above: the install reconnects and gets the refusal.
		s.mu.Unlock()
		_ = session.Close()
		return
	}
	old := s.installs[id]
	s.installs[id] = in
	s.mu.Unlock()
	if old != nil {
		// The newest connection wins (e.g. Eggify restarted before the old
		// session timed out).
		_ = old.session.Close()
	}
	release()
	s.cfg.Logf("relay: install %s connected from %s", id, remoteIP(raw))
	if !in.until.IsZero() || in.viaAllowlist {
		s.watchOnce.Do(func() { go s.watchAdmissions(s.cfg.AdmissionRecheck) })
	}

	<-session.CloseChan()
	s.mu.Lock()
	if s.installs[id] == in {
		delete(s.installs, id)
	}
	s.mu.Unlock()
	s.cfg.Logf("relay: install %s disconnected", id)
}

// overLicenseCapLocked reports whether admitting id would give the license
// install more than MaxIDsPerLicense relay addresses. Caller holds s.mu.
func (s *Server) overLicenseCapLocked(id, licenseInstall string) bool {
	if licenseInstall == "" {
		return false
	}
	n := 0
	for other, in := range s.installs {
		if other != id && in.licenseInstall == licenseInstall {
			n++
		}
	}
	return n >= s.cfg.MaxIDsPerLicense
}

func (s *Server) handleVisitor(id string, c net.Conn) {
	s.mu.Lock()
	in := s.installs[id]
	s.mu.Unlock()
	if in == nil {
		_ = c.Close()
		return
	}
	select {
	case in.streams <- struct{}{}:
	default:
		_ = c.Close()
		return
	}
	defer func() { <-in.streams }()

	stream, err := in.session.OpenStream()
	if err != nil {
		_ = c.Close()
		return
	}
	if err := writeLine(stream, streamHeader{Addr: c.RemoteAddr().String()}); err != nil {
		_ = stream.Close()
		_ = c.Close()
		return
	}
	proxy(c, stream)
}

// proxy copies both ways until either side ends, then closes both.
func proxy(a, b io.ReadWriteCloser) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = a.Close()
			_ = b.Close()
		})
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); closeBoth(); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); closeBoth(); done <- struct{}{} }()
	<-done
	<-done
}

func remoteIP(c net.Conn) string {
	if a, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		return a.IP.String()
	}
	return c.RemoteAddr().String()
}

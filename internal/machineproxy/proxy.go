package machineproxy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/perfmon"
)

// Host is the proxies' host name. Not 127.0.0.1, where the local dashboard
// lives: cookies and SameSite go by host, not port, so on 127.0.0.1 another
// machine's page would be the same site as this Mac's dashboard and its
// requests would carry this Mac's Lax session cookie. localhost is another
// site, and still a secure context (microphone, WebGPU).
const Host = "localhost"

const (
	// GateCookie is the prefix of the gate cookie's name; the proxy's port is
	// appended (GateCookieName). The cookie proves a request comes from the
	// app's own window: it is set by BootstrapPath, its value is derived from
	// the per-launch gate token (known only to the desktop process) and the
	// proxy's own origin, so a cookie for one proxy opens no other. Cookies
	// ignore ports, so every proxy and every page on localhost still sends it:
	// ServeHTTP therefore also checks where the request comes from (fetchOK).
	GateCookie    = "eggify_machine_gate"
	BootstrapPath = "/__desktop/machine-bootstrap"
	wailsPrefix   = "/wails/"
	// BridgeMarkerHeader is the per-launch header only the desktop's own
	// bridge adds to Wails runtime calls. A request from a page or from the
	// machine must never carry it onward.
	BridgeMarkerHeader = "X-Eggify-Bridge"
)

// Config sets up a Proxy.
type Config struct {
	Machine Machine
	// Token returns the paired session token for the machine.
	Token func() string
	// Gate is the per-launch secret the window gets through BootstrapPath.
	Gate string
	// Local serves /wails/* (Wails' runtime and bindings) for the page, so
	// the desktop bridge keeps working on another machine's page. It must
	// do its own checks (the bridge cookie); the proxy adds the gate and
	// Origin checks.
	Local http.Handler
	// Transport replaces the pinned transport (tests only).
	Transport http.RoundTripper
	// OnUnauthorized is called when the machine no longer accepts the
	// token (401 on an /api request), at most once a minute.
	OnUnauthorized func()
	// OnSignIn is called with the session token of a successful sign-in on
	// the machine's own login page (password, or the MFA step), before the
	// response reaches the page. The caller keeps it as the machine's token:
	// cookies never reach the window, so without this a machine that signed
	// this Mac out showed its login page in a loop.
	OnSignIn func(token string)
	// BootstrapCookies are set with the gate cookie (e.g. the desktop
	// marker the frontend reads).
	BootstrapCookies []*http.Cookie
}

// Proxy serves another machine's Eggify to the window on a loopback port.
type Proxy struct {
	cfg    Config
	target *url.URL
	rp     *httputil.ReverseProxy

	tr     http.RoundTripper
	closed chan struct{}
	once   sync.Once

	mu         sync.Mutex
	origin     string // http://localhost:<port>, set by Serve
	srv        *http.Server
	lastUnauth time.Time
	reach      reachability
	// logWindow/logCount cap logRequest at 20 lines a minute.
	logWindow time.Time
	logCount  int
}

// New builds a Proxy for cfg.Machine.
func New(cfg Config) (*Proxy, error) {
	if cfg.Token == nil || cfg.Gate == "" {
		return nil, errors.New("machineproxy: token and gate are required")
	}
	if err := cfg.Machine.Validate(); err != nil {
		return nil, err
	}
	target, _ := url.Parse(cfg.Machine.origin())
	tr := cfg.Transport
	if tr == nil {
		pt, err := newTransport(cfg.Machine)
		if err != nil {
			return nil, err
		}
		tr = pt
	}
	p := &Proxy{cfg: cfg, target: target, tr: tr, closed: make(chan struct{})}
	p.rp = &httputil.ReverseProxy{
		Transport:      tr,
		FlushInterval:  -1, // stream (media, long polls)
		Rewrite:        p.rewrite,
		ModifyResponse: p.modifyResponse,
		ErrorHandler:   p.errorPage,
	}
	return p, nil
}

// Origin is the proxy's own origin (empty before Serve).
func (p *Proxy) Origin() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.origin
}

// BootstrapURL is where the window goes to switch to this machine: it sets
// the gate cookie, then redirects to next (a local path).
func (p *Proxy) BootstrapURL(next string) string {
	return p.Origin() + BootstrapPath + "?t=" + url.QueryEscape(p.cfg.Gate) + "&next=" + url.QueryEscape(safePath(next))
}

// Listen opens the proxy's loopback listeners on one free port: 127.0.0.1
// and, when available, [::1] (localhost may resolve to either).
func Listen() ([]net.Listener, error) { return ListenPort(0) }

// ListenPort is Listen on port when it is free, else on any free port. A
// machine keeps its port across launches so its page keeps its local
// storage (localStorage is per origin, and the port is part of it).
func ListenPort(port int) ([]net.Listener, error) {
	v4, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil && port != 0 {
		v4, err = net.Listen("tcp4", "127.0.0.1:0")
	}
	if err != nil {
		return nil, err
	}
	lns := []net.Listener{v4}
	port = v4.Addr().(*net.TCPAddr).Port
	if v6, err := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", port)); err == nil {
		lns = append(lns, v6)
	}
	return lns, nil
}

// Serve serves the proxy on loopback listeners that share one port (see
// Listen) until Close.
func (p *Proxy) Serve(lns ...net.Listener) error {
	if len(lns) == 0 {
		return errors.New("machineproxy: no listener")
	}
	srv := &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	p.mu.Lock()
	p.origin = fmt.Sprintf("http://%s:%d", Host, lns[0].Addr().(*net.TCPAddr).Port)
	p.srv = srv
	p.mu.Unlock()
	errs := make(chan error, len(lns))
	for _, ln := range lns {
		go func(ln net.Listener) { errs <- srv.Serve(ln) }(ln)
	}
	err := <-errs
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Close stops the proxy and drops open connections (WebSockets included).
func (p *Proxy) Close() error {
	p.once.Do(func() { close(p.closed) })
	p.mu.Lock()
	srv := p.srv
	p.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Close()
}

// Warm opens up to n connections to the machine (TCP + TLS through the
// relay) and leaves them idle in the transport's pool, so the page's next
// requests skip the handshakes. It asks for the public /api/health with no
// credentials. It returns the first error, if every attempt failed.
func (p *Proxy) Warm(ctx context.Context, n int) error {
	if n < 1 {
		n = 1
	}
	if n > 6 {
		n = 6
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			req, err := http.NewRequestWithContext(ctx, http.MethodHead, p.target.String()+"/api/health", nil)
			if err != nil {
				errs <- err
				return
			}
			req.Host = p.target.Host
			resp, err := p.tr.RoundTrip(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
			}
			errs <- err
		}()
	}
	var first error
	ok := false
	for i := 0; i < n; i++ {
		if err := <-errs; err == nil {
			ok = true
		} else if first == nil {
			first = err
		}
	}
	if ok {
		p.reach.ok()
		return nil
	}
	p.reach.failed(time.Now())
	return first
}

// KeepWarm calls Warm every interval until Close, backing off (up to 5
// minutes) while the machine is unreachable, so a switch after a quiet
// spell doesn't start with fresh handshakes through the relay.
func (p *Proxy) KeepWarm(every time.Duration, conns int) {
	wait := every
	for {
		select {
		case <-p.closed:
			return
		case <-time.After(wait):
		}
		if err := p.Warm(context.Background(), conns); err != nil {
			wait *= 2
			if wait > 5*time.Minute {
				wait = 5 * time.Minute
			}
			continue
		}
		wait = every
	}
}

// GateCookieName is the gate cookie's name for this proxy: GateCookie plus its port.
func (p *Proxy) GateCookieName() string {
	if u, err := url.Parse(p.Origin()); err == nil && u.Port() != "" {
		return GateCookie + "_" + u.Port()
	}
	return GateCookie
}

// gateValue is this proxy's gate cookie value: HMAC-SHA256 of its origin under
// the launch gate token, so it is useless at any other port.
func (p *Proxy) gateValue() string {
	m := hmac.New(sha256.New, []byte(p.cfg.Gate))
	m.Write([]byte("eggify-machine-gate\n" + p.Origin()))
	return hex.EncodeToString(m.Sum(nil))
}

func (p *Proxy) gateOK(r *http.Request) bool {
	c, err := r.Cookie(p.GateCookieName())
	return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(p.gateValue())) == 1
}

// fetchOK decides by where a request comes from, since the gate cookie is sent
// by every page on localhost (cookies are not scoped by port):
//   - an Origin header must be the proxy's own;
//   - WebSocket upgrades and every method except GET/HEAD must carry that
//     Origin (no Origin, no state change);
//   - when the browser sends Sec-Fetch-Site it must say same-origin (or none:
//     the user typed or opened the address), never same-site or cross-site.
//
// A GET/HEAD without Origin and without Sec-Fetch-Site (an old WebKit) passes.
func (p *Proxy) fetchOK(r *http.Request) (bool, string) {
	own := p.Origin()
	origin := r.Header.Get("Origin")
	if origin != "" && origin != own {
		return false, "Origin " + origin
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false, "Sec-Fetch-Site " + site
	}
	readOnly := (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.Header.Get("Upgrade") == ""
	if !readOnly && origin == "" {
		return false, "no Origin on " + r.Method
	}
	return true, ""
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !cleanRequestPath(r) {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == BootstrapPath {
		p.bootstrap(w, r)
		return
	}
	if !p.gateOK(r) {
		p.refuse(w, r, "no gate cookie")
		return
	}
	// No other page drives this machine, not even another proxy's (same
	// host, so it holds the gate cookie too).
	if ok, why := p.fetchOK(r); !ok {
		p.refuse(w, r, why)
		return
	}
	if strings.HasPrefix(r.URL.Path, wailsPrefix) {
		if p.cfg.Local == nil {
			http.NotFound(w, r)
			return
		}
		p.cfg.Local.ServeHTTP(w, r)
		return
	}
	if blockedPath(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	if pause := p.reach.pausedFor(time.Now()); pause > 0 && (strings.HasPrefix(r.URL.Path, "/api/") || r.Header.Get("Upgrade") != "") {
		// Known to be unreachable just now: answer at once instead of
		// waiting on the relay again (page loads still try, see health.go).
		w.Header().Set("Retry-After", fmt.Sprint(int(pause.Seconds())+1))
		http.Error(w, p.machineName()+" is not reachable", http.StatusBadGateway)
		return
	}
	p.rp.ServeHTTP(w, withConnTrace(r))
}

func (p *Proxy) machineName() string {
	if p.cfg.Machine.Name == "" {
		return "The other machine"
	}
	return p.cfg.Machine.Name
}

// SlowAfter is when a forwarded request counts as slow (time to the
// machine's response headers) and is logged.
const SlowAfter = 2 * time.Second

type startKey struct{}

// warnf is logging.Warning (swapped in tests).
var warnf = logging.Warning

// elapsed is how long ago ServeHTTP handed r to the machine.
func elapsed(r *http.Request) time.Duration {
	if t, ok := r.Context().Value(startKey{}).(time.Time); ok {
		return time.Since(t)
	}
	return 0
}

// logRequest records a failed or slow request (method, path, never the query
// or any header), at most 20 lines a minute so a dead link can't flood the log.
func (p *Proxy) logRequest(r *http.Request, what string) {
	p.mu.Lock()
	now := time.Now()
	if now.Sub(p.logWindow) > time.Minute {
		p.logWindow, p.logCount = now, 0
	}
	p.logCount++
	n := p.logCount
	p.mu.Unlock()
	if n > 20 {
		return
	}
	kind := r.Method
	if r.Header.Get("Upgrade") != "" {
		kind = "upgrade"
	}
	how := ""
	if t := traceOf(r); t != nil {
		how = " (" + t.String() + ")"
	}
	warnf("Machine proxy %s: %s %s %s after %s%s", p.cfg.Machine.Name, kind, r.URL.Path, what, elapsed(r).Round(time.Millisecond), how)
}

// refuse answers 403 and logs why (never the cookie or token values).
func (p *Proxy) refuse(w http.ResponseWriter, r *http.Request, why string) {
	logging.Warning("Machine proxy %s: refused %s %s (%s)", p.cfg.Machine.Name, r.Method, r.URL.Path, why)
	http.Error(w, "forbidden", http.StatusForbidden)
}

// bootstrap sets the gate cookie, then sends the window on with a page of
// its own instead of a redirect. The switch starts on this Mac's dashboard
// (127.0.0.1, another site), and WebKit may treat a redirect chain that a
// cross-site page started as cross-site and leave the cookie out; a
// navigation the proxy's own page starts is same-site.
func (p *Proxy) bootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(p.cfg.Gate)) != 1 {
		p.refuse(w, r, "bad bootstrap token")
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	// Lax: sent on the top-level navigations that open the machine; the
	// Origin check keeps other pages' fetches and posts out.
	http.SetCookie(w, &http.Cookie{Name: p.GateCookieName(), Value: p.gateValue(), Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	for _, c := range p.cfg.BootstrapCookies {
		http.SetCookie(w, c)
	}
	next := html.EscapeString(safePath(r.URL.Query().Get("next")))
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=%s"><title>Eggify</title><body style="background:#0a0a1a"></body>`, next)
}

// rewrite sends the request to the machine as a signed-in user: no local
// cookies or credentials, the paired token as Bearer, and Host/Origin as
// the machine's own so its same-origin checks hold.
func (p *Proxy) rewrite(pr *httputil.ProxyRequest) {
	pr.SetURL(p.target)
	pr.Out.Host = p.target.Host
	h := pr.Out.Header
	h.Del("Cookie")
	h.Del("Authorization")
	h.Del("Referer")
	h.Del("Proxy-Authorization")
	h.Del(BridgeMarkerHeader)
	if h.Get("Origin") != "" {
		h.Set("Origin", p.target.Scheme+"://"+p.target.Host)
	}
	// The page could put a key on a WebSocket URL (?token=); it never goes
	// to the machine, only the paired token does.
	if q := pr.Out.URL.Query(); q.Has("token") {
		q.Del("token")
		pr.Out.URL.RawQuery = q.Encode()
	}
	h.Set("Authorization", "Bearer "+p.cfg.Token())
}

func (p *Proxy) modifyResponse(resp *http.Response) error {
	p.reach.ok()
	if d := elapsed(resp.Request); resp.StatusCode >= 500 || d >= SlowAfter {
		p.logRequest(resp.Request, fmt.Sprintf("answered %d", resp.StatusCode))
	}
	if tok := signInToken(resp); tok != "" && p.cfg.OnSignIn != nil {
		p.cfg.OnSignIn(tok)
	}
	// The machine cannot set cookies on this Mac's loopback host (they would
	// be shared with the local dashboard), nor HSTS.
	resp.Header.Del("Set-Cookie")
	resp.Header.Del("Strict-Transport-Security")
	if loc := resp.Header.Get("Location"); loc != "" {
		if rest, ok := strings.CutPrefix(loc, p.cfg.Machine.origin()); ok && (rest == "" || strings.ContainsAny(rest[:1], "/?#")) {
			resp.Header.Set("Location", p.Origin()+rest)
		}
	}
	if resp.StatusCode == http.StatusUnauthorized && strings.HasPrefix(resp.Request.URL.Path, "/api/") && p.cfg.OnUnauthorized != nil {
		// A refused token fails every request the page makes; report once.
		p.mu.Lock()
		report := time.Since(p.lastUnauth) > time.Minute
		if report {
			p.lastUnauth = time.Now()
		}
		p.mu.Unlock()
		if report {
			go p.cfg.OnUnauthorized()
		}
	}
	return nil
}

// signInPaths answer a finished sign-in with the session cookie.
var signInPaths = map[string]bool{
	"/api/auth/login":      true, // without MFA
	"/api/auth/mfa/verify": true,
}

// signInToken is the session token a successful sign-in sets, or "".
func signInToken(resp *http.Response) string {
	req := resp.Request
	if req == nil || req.Method != http.MethodPost || !signInPaths[req.URL.Path] ||
		resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ""
	}
	for _, c := range resp.Cookies() {
		if c.Name != "session_token" || c.MaxAge < 0 || len(c.Value) < 32 || len(c.Value) > 256 {
			continue
		}
		if strings.Trim(c.Value, "0123456789abcdefABCDEF") != "" {
			continue
		}
		return c.Value
	}
	return ""
}

func (p *Proxy) errorPage(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() == nil { // not the page going away
		p.logRequest(r, "failed ("+err.Error()+")")
		p.reach.failed(time.Now())
	}
	name := p.machineName()
	if strings.HasPrefix(r.URL.Path, "/api/") || r.Header.Get("Upgrade") != "" {
		http.Error(w, name+" is not reachable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusBadGateway)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%[1]s</title>
<body style="font:15px -apple-system,sans-serif;padding:48px;color:#333">
<h2>%[1]s is not reachable</h2><p>Check that it is awake and that remote access (Eggify relay) is on.</p>
<p><a href="">Try again</a></p><p style="color:#888;font-size:12px">%[2]s</p>`,
		html.EscapeString(name), html.EscapeString(err.Error()))
}

// cleanRequestPath refuses paths that could mean something else after
// cleaning (the machine's tunnel guard refuses them too).
func cleanRequestPath(r *http.Request) bool {
	raw := r.URL.EscapedPath()
	lower := strings.ToLower(raw)
	if !strings.HasPrefix(raw, "/") || strings.Contains(raw, "//") || strings.Contains(raw, `\`) ||
		strings.Contains(lower, "%2e") || strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") {
		return false
	}
	for _, seg := range strings.Split(r.URL.Path, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// blockedPath: desktop-only and internal routes are never forwarded.
func blockedPath(p string) bool {
	lp := strings.ToLower(p)
	for _, deny := range []string{"/__desktop", "/wails", "/internal"} {
		if lp == deny || strings.HasPrefix(lp, deny+"/") {
			return true
		}
	}
	return false
}

// safePath keeps redirects on pages of the machine.
func safePath(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") || blockedPath(next) {
		return "/"
	}
	return next
}

// FetchUsage asks the machine for its CPU and memory (GET /api/system/usage,
// the meter next to the machine chip) over the proxy's warm connections, as
// the paired user. Only this one fixed path is ever requested.
func (p *Proxy) FetchUsage(ctx context.Context) (perfmon.Usage, error) {
	var u perfmon.Usage
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.target.String()+"/api/system/usage", nil)
	if err != nil {
		return u, err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.Token())
	resp, err := p.tr.RoundTrip(req)
	if err != nil {
		return u, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 404: that Mac runs a version without the meter.
		return u, fmt.Errorf("usage: %s", resp.Status)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&u)
	return u, err
}

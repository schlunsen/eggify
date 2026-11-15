package machineproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"
)

// A machine that is asleep or offline fails every request the page makes,
// and each one waits for the relay (a dial timeout, or an EOF once the relay
// sees the machine isn't connected). After unreachableAfter connection
// failures in a row the proxy answers the page's API calls and WebSocket
// upgrades at once for a while (1 s, doubling up to 15 s); page loads still
// go through, so "Try again" and the next real request probe the machine,
// and any answer from it ends the pause.
const (
	unreachableAfter    = 3
	unreachablePauseMin = time.Second
	unreachablePauseMax = 15 * time.Second
)

type reachability struct {
	mu        sync.Mutex
	failures  int
	downUntil time.Time
}

func (r *reachability) failed(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures++
	if r.failures < unreachableAfter {
		return
	}
	pause := unreachablePauseMin << min(r.failures-unreachableAfter, 4)
	if pause > unreachablePauseMax {
		pause = unreachablePauseMax
	}
	r.downUntil = now.Add(pause)
}

func (r *reachability) ok() {
	r.mu.Lock()
	r.failures, r.downUntil = 0, time.Time{}
	r.mu.Unlock()
}

// pausedFor is how long requests are still answered without trying (0: try).
func (r *reachability) pausedFor(now time.Time) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Before(r.downUntil) {
		return r.downUntil.Sub(now)
	}
	return 0
}

// connTrace records how a forwarded request got its connection to the
// machine, so a slow request's log line says whether the time went into
// reaching the machine (DNS, relay, TLS) or into the machine's answer.
type connTrace struct {
	mu                        sync.Mutex
	start                     time.Time
	got, reused               bool
	dnsStart, dnsDone         time.Time
	connectStart, connectDone time.Time
	tlsStart, tlsDone         time.Time
}

func (t *connTrace) clientTrace() *httptrace.ClientTrace {
	at := func(f *time.Time) func() {
		return func() { t.mu.Lock(); *f = time.Now(); t.mu.Unlock() }
	}
	return &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { at(&t.dnsStart)() },
		DNSDone:           func(httptrace.DNSDoneInfo) { at(&t.dnsDone)() },
		ConnectStart:      func(string, string) { at(&t.connectStart)() },
		ConnectDone:       func(string, string, error) { at(&t.connectDone)() },
		TLSHandshakeStart: at(&t.tlsStart),
		TLSHandshakeDone:  func(tls.ConnectionState, error) { at(&t.tlsDone)() },
		GotConn: func(info httptrace.GotConnInfo) {
			t.mu.Lock()
			t.got, t.reused = true, info.Reused
			t.mu.Unlock()
		},
	}
}

// String is e.g. "reused connection" or "new connection: dns 3ms, relay
// 48ms, tls 101ms".
func (t *connTrace) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.got && t.reused {
		return "reused connection"
	}
	var parts []string
	span := func(name string, a, b time.Time) {
		if !a.IsZero() && !b.IsZero() {
			parts = append(parts, fmt.Sprintf("%s %s", name, b.Sub(a).Round(time.Millisecond)))
		}
	}
	span("dns", t.dnsStart, t.dnsDone)
	span("relay", t.connectStart, t.connectDone)
	span("tls", t.tlsStart, t.tlsDone)
	what := "new connection"
	if !t.got {
		what = "no connection"
	}
	if len(parts) == 0 {
		return what
	}
	return what + ": " + strings.Join(parts, ", ")
}

type traceKey struct{}

func withConnTrace(r *http.Request) *http.Request {
	t := &connTrace{start: time.Now()}
	ctx := context.WithValue(r.Context(), traceKey{}, t)
	ctx = context.WithValue(ctx, startKey{}, t.start)
	return r.WithContext(httptrace.WithClientTrace(ctx, t.clientTrace()))
}

func traceOf(r *http.Request) *connTrace {
	t, _ := r.Context().Value(traceKey{}).(*connTrace)
	return t
}

// Every machine's address (<id>.<relay suffix>) is the relay. When looking it
// up fails (a flaky resolver, DNS not back yet after wake), the address the
// last connection went to is used, so a DNS hiccup doesn't take the machine
// away.
var (
	dnsMu       sync.Mutex
	dnsLastGood = map[string]string{} // lower-case host:port → ip:port
)

func dialWithDNSFallback(dial func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		key := strings.ToLower(addr)
		c, err := dial(ctx, network, addr)
		if err == nil {
			if ta, ok := c.RemoteAddr().(*net.TCPAddr); ok {
				dnsMu.Lock()
				dnsLastGood[key] = ta.String()
				dnsMu.Unlock()
			}
			return c, nil
		}
		var dnsErr *net.DNSError
		if !errors.As(err, &dnsErr) {
			return nil, err
		}
		dnsMu.Lock()
		last := dnsLastGood[key]
		dnsMu.Unlock()
		if last == "" || ctx.Err() != nil {
			return nil, err
		}
		return dial(ctx, network, last)
	}
}

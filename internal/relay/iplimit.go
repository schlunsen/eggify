package relay

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Per-source-address limits. A relay is reachable by anyone, and every
// connection costs it a goroutine and, for installs, a TLS handshake: one
// address must not be able to fill the handshake slots, the stream slots of an
// install or the whole relay. IPv6 sources count by /64 (one subscriber gets a
// /64 or more). Over a limit the connection is closed at once, before anything
// is read or logged per connection.
//
// What is limited, per source:
//   - connections that were accepted but not yet classified by their SNI
//     (the ClientHello wait: a slow-loris holds these for HelloTimeout),
//   - new connections per minute (all kinds),
//   - concurrent visitor streams,
//   - install (control) connections: concurrently open, handshakes in flight
//     and new per minute. These have their own, higher caps: several installs
//     can share an address (an office, CGNAT) and reconnect together.
//
// A limit of 0 uses the default, a negative limit turns that check off.

// Default limits (ServerConfig.IPLimits).
const (
	DefaultPendingPerIP          = 32
	DefaultConnsPerMinutePerIP   = 600
	DefaultStreamsPerIP          = 128
	DefaultControlPerIP          = 64
	DefaultControlPendingPerIP   = 64
	DefaultControlPerMinutePerIP = 120
)

// IPLimits configures the per-source limits.
type IPLimits struct {
	// PendingPerIP caps connections waiting for their ClientHello.
	PendingPerIP int
	// ConnsPerMinutePerIP caps new connections of any kind.
	ConnsPerMinutePerIP int
	// StreamsPerIP caps concurrent visitor streams.
	StreamsPerIP int
	// ControlPerIP caps concurrently open install connections.
	ControlPerIP int
	// ControlPendingPerIP caps install handshakes in flight.
	ControlPendingPerIP int
	// ControlPerMinutePerIP caps new install connections.
	ControlPerMinutePerIP int
}

func limitOrDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func (l IPLimits) withDefaults() IPLimits {
	l.PendingPerIP = limitOrDefault(l.PendingPerIP, DefaultPendingPerIP)
	l.ConnsPerMinutePerIP = limitOrDefault(l.ConnsPerMinutePerIP, DefaultConnsPerMinutePerIP)
	l.StreamsPerIP = limitOrDefault(l.StreamsPerIP, DefaultStreamsPerIP)
	l.ControlPerIP = limitOrDefault(l.ControlPerIP, DefaultControlPerIP)
	l.ControlPendingPerIP = limitOrDefault(l.ControlPendingPerIP, DefaultControlPendingPerIP)
	l.ControlPerMinutePerIP = limitOrDefault(l.ControlPerMinutePerIP, DefaultControlPerMinutePerIP)
	return l
}

// ipKey is the limiter's source key: the address, an IPv6 address masked to its /64.
func ipKey(addr net.Addr) string {
	var ip net.IP
	switch a := addr.(type) {
	case *net.TCPAddr:
		ip = a.IP
	default:
		host, _, err := net.SplitHostPort(addr.String())
		if err != nil {
			return addr.String()
		}
		ip = net.ParseIP(host)
		if ip == nil {
			return host
		}
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

type window struct {
	start time.Time
	n     int
}

type ipState struct {
	pending, streams, control, controlPending int
	conns, controlConns                       window
}

func (st *ipState) idle(now time.Time) bool {
	return st.pending == 0 && st.streams == 0 && st.control == 0 && st.controlPending == 0 &&
		now.Sub(st.conns.start) >= time.Minute && now.Sub(st.controlConns.start) >= time.Minute
}

type ipLimiter struct {
	lim IPLimits
	now func() time.Time

	mu     sync.Mutex
	by     map[string]*ipState
	lastGC time.Time

	rejected atomic.Int64
}

func newIPLimiter(l IPLimits, now func() time.Time) *ipLimiter {
	if now == nil {
		now = time.Now
	}
	return &ipLimiter{lim: l.withDefaults(), now: now, by: map[string]*ipState{}}
}

func over(limit, n int) bool { return limit >= 0 && n >= limit }

// tick counts one event in a one-minute window and reports whether it is over limit.
func tick(w *window, now time.Time, limit int) bool {
	if now.Sub(w.start) >= time.Minute {
		w.start, w.n = now, 0
	}
	if over(limit, w.n) {
		return true
	}
	w.n++
	return false
}

func (l *ipLimiter) state(key string, now time.Time) *ipState {
	if now.Sub(l.lastGC) >= time.Minute {
		l.lastGC = now
		for k, st := range l.by {
			if st.idle(now) {
				delete(l.by, k)
			}
		}
	}
	st := l.by[key]
	if st == nil {
		st = &ipState{}
		l.by[key] = st
	}
	return st
}

// accept admits a new connection into the "waiting for its ClientHello"
// state. The caller must call classified exactly once if it returns true.
func (l *ipLimiter) accept(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	st := l.state(key, now)
	if over(l.lim.PendingPerIP, st.pending) || tick(&st.conns, now, l.lim.ConnsPerMinutePerIP) {
		l.rejected.Add(1)
		return false
	}
	st.pending++
	return true
}

// classified ends the ClientHello wait of a connection admitted by accept.
func (l *ipLimiter) classified(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if st := l.by[key]; st != nil && st.pending > 0 {
		st.pending--
	}
}

// beginStream admits a visitor stream; endStream releases it.
func (l *ipLimiter) beginStream(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.state(key, l.now())
	if over(l.lim.StreamsPerIP, st.streams) {
		l.rejected.Add(1)
		return false
	}
	st.streams++
	return true
}

func (l *ipLimiter) endStream(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if st := l.by[key]; st != nil && st.streams > 0 {
		st.streams--
	}
}

// beginControl admits an install connection (handshake in flight and open);
// handshakeDone ends the in-flight part, endControl the whole.
func (l *ipLimiter) beginControl(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	st := l.state(key, now)
	if over(l.lim.ControlPerIP, st.control) || over(l.lim.ControlPendingPerIP, st.controlPending) ||
		tick(&st.controlConns, now, l.lim.ControlPerMinutePerIP) {
		l.rejected.Add(1)
		return false
	}
	st.control++
	st.controlPending++
	return true
}

func (l *ipLimiter) handshakeDone(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if st := l.by[key]; st != nil && st.controlPending > 0 {
		st.controlPending--
	}
}

func (l *ipLimiter) endControl(key string, handshakeFinished bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if st := l.by[key]; st != nil {
		if st.control > 0 {
			st.control--
		}
		if !handshakeFinished && st.controlPending > 0 {
			st.controlPending--
		}
	}
}

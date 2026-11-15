package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Messages from machine_transition.js. The page posts them to the window's
// raw message handler ("eggify-machine:<event> <a> <b>"). Any page the
// window shows can post there, the other Macs' pages included, so they are
// parsed strictly (a known event, numbers, a checked path),
// taken only from the main frame of the machine they are about, and only
// for the switch in progress. Everything else is ignored.

const machineMsgPrefix = "eggify-machine:"

const maxLeftPathLen = 512

type machineMsg struct {
	event string // left | ready | reveal | cancel
	seq   int    // the switch
	ms    int    // ready / reveal: ms since "in" started (-1: gave up)
	path  string // left
}

// parseMachineMessage parses one message; ok is false for anything that is
// not exactly a machine switch message.
func parseMachineMessage(raw string) (machineMsg, bool) {
	var m machineMsg
	if len(raw) > maxLeftPathLen*3+128 || !strings.HasPrefix(raw, machineMsgPrefix) {
		return m, false
	}
	f := strings.Split(raw[len(machineMsgPrefix):], " ")
	if len(f) != 3 {
		return m, false
	}
	n, ok := smallInt(f[1], 1_000_000_000)
	if !ok {
		return m, false
	}
	m.event, m.seq = f[0], n
	switch m.event {
	case "ready", "reveal", "cancel":
		if f[2] == "-1" {
			m.ms = -1
		} else if m.ms, ok = smallInt(f[2], 600_000); !ok {
			return m, false
		}
		return m, m.seq > 0
	case "left":
		p, err := url.PathUnescape(f[2])
		if err != nil || !rememberablePath(p) {
			return m, false
		}
		m.path = p
		return m, m.seq > 0
	}
	return m, false
}

// smallInt parses a plain non-negative decimal up to max.
func smallInt(s string, max int) (int, bool) {
	if s == "" || len(s) > 10 {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n > max {
		return 0, false
	}
	return n, true
}

// rememberablePath: a dashboard page worth coming back to.
func rememberablePath(p string) bool {
	if p == "" || len(p) > maxLeftPathLen || safeNext(p) != p {
		return false
	}
	for _, c := range p {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	for _, skip := range []string{"/login", "/setup", "/oauth-callback", "/access-denied", "/__"} {
		if p == skip || strings.HasPrefix(p, skip+"/") || strings.HasPrefix(p, skip+"?") || (skip == "/__" && strings.HasPrefix(p, skip)) {
			return false
		}
	}
	return true
}

// pageOrigin is scheme://host of a frame URL.
func pageOrigin(frameURL string) string {
	u, err := url.Parse(frameURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// handleMessage takes a raw message from the window. It returns false when
// the message is not a machine switch message (another handler may log it).
func (s *machineSwitcher) handleMessage(frameURL string, mainFrame bool, raw string) bool {
	if !strings.HasPrefix(raw, machineMsgPrefix) {
		return false
	}
	m, ok := parseMachineMessage(raw)
	if !ok || !mainFrame {
		return true
	}
	origin := pageOrigin(frameURL)
	if origin == "" {
		return true
	}
	s.mu.Lock()
	from := func(i int) bool {
		return i >= 0 && i < len(s.targets) && s.targets[i].origin != nil && s.targets[i].origin() == origin
	}
	tr := s.pending
	if tr == nil || tr.Seq != m.seq {
		s.mu.Unlock()
		return true
	}
	switch m.event {
	case "left":
		if from(tr.from) && !tr.navigated {
			if s.lastPath == nil {
				s.lastPath = map[string]string{}
			}
			s.lastPath[s.targets[tr.from].id] = m.path
		}
		s.mu.Unlock()
	case "ready":
		if from(tr.to) && !tr.tCommit.IsZero() {
			tr.readyMS, tr.readyKnown = m.ms, true
		}
		s.mu.Unlock()
	case "reveal":
		if !from(tr.to) || tr.tCommit.IsZero() {
			s.mu.Unlock()
			return true
		}
		tr.revealed = true
		s.pending = nil
		line := tr.timingLine("")
		s.mu.Unlock()
		s.log(line)
	case "cancel":
		// "Stay on …": on the old page while waiting, or on the new one.
		if !(from(tr.to) || from(tr.from)) || tr.revealed {
			s.mu.Unlock()
			return true
		}
		back := tr.from
		line := tr.timingLine("cancelled by the owner")
		s.pending = nil
		dir := -tr.Dir
		s.mu.Unlock()
		s.log(line)
		s.switchToDir(back, s.pathFor(back), dir)
	default:
		s.mu.Unlock()
	}
	return true
}

// timingLine is the one log line per switch, e.g.
// "Machine switch → Studio: out 201ms, navigate→commit 640ms, commit→mounted 1.10s, reveal at 1.95s total".
func (tr *machineTransition) timingLine(end string) string {
	now := time.Now()
	parts := []string{}
	if !tr.tNav.IsZero() {
		parts = append(parts, "out "+fmtDur(tr.tNav.Sub(tr.t0)))
	}
	switch {
	case !tr.tCommit.IsZero():
		c := "navigate→commit " + fmtDur(tr.tCommit.Sub(tr.tNav))
		if tr.tFirst.Before(tr.tCommit) {
			c += " (bootstrap page " + fmtDur(tr.tFirst.Sub(tr.tNav)) + ")"
		}
		parts = append(parts, c)
	case !tr.tFirst.IsZero():
		parts = append(parts, "bootstrap page "+fmtDur(tr.tFirst.Sub(tr.tNav))+", no dashboard commit")
	case !tr.tNav.IsZero():
		parts = append(parts, "no commit")
	}
	if tr.readyKnown {
		if tr.readyMS < 0 {
			parts = append(parts, "dashboard mount timed out")
		} else {
			parts = append(parts, "commit→mounted "+fmtDur(time.Duration(tr.readyMS)*time.Millisecond))
		}
	}
	if end == "" {
		parts = append(parts, "reveal at "+fmtDur(now.Sub(tr.t0))+" total")
	} else {
		parts = append(parts, end+" after "+fmtDur(now.Sub(tr.t0)))
	}
	return fmt.Sprintf("Machine switch → %s: %s", tr.Name, strings.Join(parts, ", "))
}

func fmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
	}
	return strconv.FormatFloat(d.Seconds(), 'f', 2, 64) + "s"
}

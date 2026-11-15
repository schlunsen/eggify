package main

import (
	_ "embed"
	"encoding/json"
	"hash/fnv"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Switching the window between this Mac and paired machines (ADR 014).
//
// The transition is native, not part of the dashboard: the other machine's
// page is its own frontend (maybe an older version), so the app injects it
// with ExecJS. The old page fades into a neutral surface naming the
// destination. After navigation commits, the surface covers the dashboard
// until it mounts, then fades away after two painted frames. Only opacity
// animates, with shorter fades for Reduce Motion.
//
// Each switch logs one line with its timings (machine_switch_timing.go).

//go:embed machine_transition.js
var machineTransitionJS string

// switchOutDelay is how long the "out" phase plays before navigating; the
// fade covers the page by then (OUT_MS in machine_transition.js).
const switchOutDelay = 150 * time.Millisecond

// switchGiveUp: with no page after this long the switch stops waiting (the
// card offers "Stay on …" after 5 s).
const switchGiveUp = 30 * time.Second

// switchWindow is what the switcher needs from the window (tests fake it).
type switchWindow interface {
	ExecJS(js string)
	SetURL(url string) application.Window
	SetTitle(title string) application.Window
}

// machineTarget is one machine the window can show; targets[0] is this Mac.
type machineTarget struct {
	id   string // relay install id; "" for this Mac
	name string
	hue  int
	// open is the URL of path on this machine (the window is already there).
	open func(path string) string
	// enter is the URL that switches the window to this machine.
	enter func(path string) string
	// origin is the machine's page origin in the window (messages from the
	// page are only taken from it).
	origin func() string
	// bootstrapHops is the number of pages enter goes through before the
	// dashboard (the machine proxy's bootstrap page: 1).
	bootstrapHops int
	// warm opens a connection to the machine ahead of the navigation.
	warm func()
}

type machineSwitcher struct {
	window *application.WebviewWindow // Show/Focus (nil in tests)
	view   switchWindow

	mu       sync.Mutex
	targets  []machineTarget
	current  int
	seq      int
	pending  *machineTransition // waits for the new page to commit / reveal
	lastPath map[string]string  // machine id -> the page the owner left it on

	// onChange is called after the machine shown changes (tray checks).
	onChange func()
	// onUpdate is called after the list or the machine shown changes
	// (the page's machine chip, MachinesEvent).
	onUpdate func()
	// logf writes the per-switch timing line (the server log).
	logf func(format string, args ...any)
}

// machineTransition is the parameter of machine_transition.js.
type machineTransition struct {
	Phase string `json:"phase"` // out | in
	Name  string `json:"name"`
	Hue   int    `json:"hue"`
	Dir   int    `json:"dir"` // 1: the machine comes from the right
	Here  bool   `json:"here"`
	Seq   int    `json:"seq"`
	Start int64  `json:"start"` // unix ms when the switch began (shared clock)
	From  string `json:"from"`  // the machine we come from ("Stay on …")
	// Hold: "in" only covers the page (a newer switch navigates next).
	Hold bool `json:"hold,omitempty"`

	to, from   int
	navigated  bool
	hopsLeft   int
	revealed   bool
	t0, tNav   time.Time
	tCommit    time.Time
	tFirst     time.Time // first commit (the bootstrap page, if any)
	readyMS    int       // commit -> mounted, -1: gave up waiting
	readyKnown bool
}

func newMachineSwitcher(window *application.WebviewWindow, local machineTarget) *machineSwitcher {
	s := &machineSwitcher{window: window, view: window, targets: []machineTarget{local}}
	window.OnWindowEvent(events.Mac.WebViewDidCommitNavigation, func(*application.WindowEvent) { s.onCommit() })
	return s
}

func (s *machineSwitcher) add(t machineTarget) {
	s.mu.Lock()
	s.targets = append(s.targets, t)
	s.mu.Unlock()
	s.updated()
}

func (s *machineSwitcher) updated() {
	if s.onUpdate != nil {
		go s.onUpdate()
	}
}

// MachineInfo is one machine the window can show, as the page sees it (no
// URLs or tokens).
type MachineInfo struct {
	ID   string `json:"id"` // "" for this Mac
	Name string `json:"name"`
	Hue  int    `json:"hue"`
	Here bool   `json:"here"` // this Mac
}

// MachineList is what DesktopService.Machines returns and MachinesEvent
// carries: every machine and the id of the one the window shows.
type MachineList struct {
	Current  string        `json:"current"`
	Machines []MachineInfo `json:"machines"`
}

// list returns the machines for the page.
func (s *machineSwitcher) list() MachineList {
	targets, cur := s.snapshot()
	out := MachineList{Machines: make([]MachineInfo, 0, len(targets))}
	for i, t := range targets {
		out.Machines = append(out.Machines, MachineInfo{ID: t.id, Name: t.name, Hue: t.hue, Here: i == 0})
		if i == cur {
			out.Current = t.id
		}
	}
	return out
}

// index returns the position of the machine with id ("" = this Mac).
func (s *machineSwitcher) index(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return 0
	}
	for i, t := range s.targets {
		if i > 0 && t.id == id {
			return i
		}
	}
	return -1
}

// snapshot returns the targets and the index of the one shown.
func (s *machineSwitcher) snapshot() ([]machineTarget, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]machineTarget(nil), s.targets...), s.current
}

// remove drops the machine with id, switching back to this Mac first when
// the window shows it.
func (s *machineSwitcher) remove(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	idx := -1
	for i, t := range s.targets {
		if t.id == id {
			idx = i
		}
	}
	shown := idx == s.current
	s.mu.Unlock()
	if idx < 1 {
		return
	}
	if shown {
		s.switchTo(0, s.pathFor(0))
	}
	s.mu.Lock()
	s.targets = append(s.targets[:idx], s.targets[idx+1:]...)
	if s.current > idx {
		s.current--
	}
	if s.pending != nil {
		// Indexes past the removed one moved down.
		for _, p := range []*int{&s.pending.to, &s.pending.from} {
			if *p > idx {
				*p--
			} else if *p == idx {
				*p = 0
			}
		}
	}
	delete(s.lastPath, id)
	s.mu.Unlock()
	s.updated()
}

func (s *machineSwitcher) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.targets)
}

// pathFor is where machine i opens: the page the owner left it on, else
// the agents page.
func (s *machineSwitcher) pathFor(i int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.targets) {
		return "/agents"
	}
	if p, ok := s.lastPath[s.targets[i].id]; ok {
		return p
	}
	return "/agents"
}

// switchToMachine switches to machine i where the owner left it.
func (s *machineSwitcher) switchToMachine(i int) { s.switchTo(i, s.pathFor(i)) }

// step switches to the next (dir 1) or previous (-1) machine, wrapping
// around.
func (s *machineSwitcher) step(dir int) {
	s.mu.Lock()
	n, cur := len(s.targets), s.current
	s.mu.Unlock()
	if n > 1 {
		// The machine comes from the side of the key pressed, also when the
		// list wraps around.
		i := ((cur+dir)%n + n) % n
		s.switchToDir(i, s.pathFor(i), dir)
	}
}

// switchTo shows machine i at path with the transition.
func (s *machineSwitcher) switchTo(i int, path string) {
	s.switchToDir(i, path, 0)
}

// switchToDir is switchTo with the side the machine comes from (0: from
// its place in the list).
func (s *machineSwitcher) switchToDir(i int, path string, dir int) {
	s.mu.Lock()
	if i < 0 || i >= len(s.targets) {
		s.mu.Unlock()
		return
	}
	if i == s.current && s.pending == nil {
		url := s.targets[i].open(path)
		s.mu.Unlock()
		s.view.SetURL(url)
		return
	}
	if dir == 0 {
		dir = 1
		if i < s.current {
			dir = -1
		}
	}
	t, from := s.targets[i], s.targets[s.current]
	prev := s.current
	s.current = i
	s.seq++
	now := time.Now()
	tr := &machineTransition{
		Name: t.name, Hue: t.hue, Dir: dir, Here: i == 0, Seq: s.seq,
		Start: now.UnixMilli(), From: from.name,
		to: i, from: prev, hopsLeft: t.bootstrapHops, t0: now,
	}
	if s.pending != nil && !s.pending.revealed {
		// A switch while one is still on its way: "Stay on" goes back to
		// where the owner started.
		tr.from = s.pending.from
		tr.From = s.targets[tr.from].name
	}
	s.pending = tr
	seq := s.seq
	s.mu.Unlock()

	// The connection to the machine opens while "out" plays.
	if t.warm != nil {
		go t.warm()
	}
	if s.onChange != nil {
		go s.onChange()
	}
	s.updated()
	s.view.ExecJS(transitionScript(*tr, "out"))
	time.AfterFunc(switchOutDelay, func() {
		s.mu.Lock()
		if s.seq != seq { // switched again meanwhile; the newer one navigates
			s.mu.Unlock()
			return
		}
		tr.navigated = true
		tr.tNav = time.Now()
		s.mu.Unlock()
		s.view.SetURL(t.enter(path))
		if i == 0 {
			s.view.SetTitle(defaultWindowTitle)
		} else {
			s.view.SetTitle("Eggify — " + t.name)
		}
	})
	// Without a commit or a reveal (other platforms, a failed load) the next
	// page just shows; don't play "in" on some later navigation.
	time.AfterFunc(switchGiveUp, func() {
		s.mu.Lock()
		if s.pending != tr {
			s.mu.Unlock()
			return
		}
		s.pending = nil
		line := tr.timingLine("gave up waiting")
		s.mu.Unlock()
		s.log(line)
	})
}

func (s *machineSwitcher) onCommit() {
	s.mu.Lock()
	tr := s.pending
	if tr != nil && !tr.navigated {
		// The page of an earlier switch arrived while a newer one plays
		// "out": keep it covered until the newer one navigates.
		arg := *tr
		arg.Hold = true
		s.mu.Unlock()
		s.view.ExecJS(transitionScript(arg, "in"))
		return
	}
	if tr == nil || tr.tCommit != (time.Time{}) {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	if tr.tFirst.IsZero() {
		tr.tFirst = now
	}
	if tr.hopsLeft > 0 {
		// The bootstrap page: covered there too, the dashboard comes next.
		tr.hopsLeft--
	} else {
		tr.tCommit = now
	}
	arg := *tr
	s.mu.Unlock()
	s.view.ExecJS(transitionScript(arg, "in"))
}

// openHere shows path on the machine the window is on (tray pages, ⌘,).
func (s *machineSwitcher) openHere(path string) {
	s.mu.Lock()
	url := s.targets[s.current].open(path)
	s.mu.Unlock()
	s.view.SetURL(url)
}

// openLocal shows path on this Mac (deep links and notifications are about
// this Mac's sessions), switching back if needed.
func (s *machineSwitcher) openLocal(path string) {
	s.mu.Lock()
	cur := s.current
	s.mu.Unlock()
	if cur == 0 {
		s.openHere(path)
		return
	}
	s.switchTo(0, path)
}

func (s *machineSwitcher) log(line string) {
	if s.logf != nil {
		s.logf("%s", line)
	}
}

func transitionScript(tr machineTransition, phase string) string {
	tr.Phase = phase
	arg, _ := json.Marshal(tr)
	return "(" + machineTransitionJS + ")(" + string(arg) + ");"
}

// machineHue gives each machine a stable colour from its name.
func machineHue(name string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return int(h.Sum32() % 360)
}

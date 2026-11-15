package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func testSwitcher(current int) *machineSwitcher {
	return &machineSwitcher{
		targets: []machineTarget{
			{name: "This Mac", hue: 252},
			{id: "abc", name: "Studio", hue: machineHue("Studio")},
			{id: "def", name: "Laptop", hue: machineHue("Laptop")},
		},
		current: current,
	}
}

func TestMachineSwitcher_ListForThePage(t *testing.T) {
	l := testSwitcher(1).list()
	if l.Current != "abc" {
		t.Fatalf("current = %q, want abc", l.Current)
	}
	if len(l.Machines) != 3 || !l.Machines[0].Here || l.Machines[0].ID != "" || l.Machines[1].Here {
		t.Fatalf("machines = %+v", l.Machines)
	}
	if l.Machines[1].Name != "Studio" || l.Machines[1].Hue != machineHue("Studio") {
		t.Fatalf("studio = %+v", l.Machines[1])
	}
	if got := testSwitcher(0).list().Current; got != "" {
		t.Fatalf("on this Mac current = %q, want \"\"", got)
	}
	// Only id, name, hue, here reach the page.
	b, _ := json.Marshal(l.Machines[1])
	if string(b) != `{"id":"abc","name":"Studio","hue":`+itoa(machineHue("Studio"))+`,"here":false}` {
		t.Fatalf("json = %s", b)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestMachineSwitcher_Index(t *testing.T) {
	s := testSwitcher(0)
	for id, want := range map[string]int{"": 0, "abc": 1, "def": 2, "nope": -1} {
		if got := s.index(id); got != want {
			t.Errorf("index(%q) = %d, want %d", id, got, want)
		}
	}
}

func TestDesktopService_Machines(t *testing.T) {
	var svc DesktopService
	if l := svc.Machines(); l.Machines == nil || len(l.Machines) != 0 {
		t.Fatalf("before the window exists: %+v", l)
	}
	if err := svc.SwitchMachine("abc"); err == nil {
		t.Fatal("switch before ready should fail")
	}
	if err := svc.AddMachineFromClipboard(); err == nil {
		t.Fatal("add before ready should fail")
	}
	svc.machines = testSwitcher(1)
	if err := svc.SwitchMachine("nope"); err == nil {
		t.Fatal("unknown machine should fail")
	}
	// The machine already shown: nothing to do (no window needed).
	if err := svc.SwitchMachine("abc"); err != nil {
		t.Fatal(err)
	}
	called := make(chan struct{}, 1)
	svc.addMachine = func() { called <- struct{}{} }
	if err := svc.AddMachineFromClipboard(); err != nil {
		t.Fatal(err)
	}
	<-called
}

// fakeView records what the switcher does to the window.
type fakeView struct {
	mu      sync.Mutex
	scripts []string
	urls    []string
}

func (f *fakeView) ExecJS(js string) { f.mu.Lock(); f.scripts = append(f.scripts, js); f.mu.Unlock() }
func (f *fakeView) SetURL(u string) application.Window {
	f.mu.Lock()
	f.urls = append(f.urls, u)
	f.mu.Unlock()
	return nil
}
func (f *fakeView) SetTitle(string) application.Window { return nil }

// phases returns the transition parameters passed to the scripts so far.
func (f *fakeView) phases(t *testing.T) []machineTransition {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []machineTransition
	for _, s := range f.scripts {
		i := strings.LastIndex(s, ")({")
		var tr machineTransition
		if i < 0 || json.Unmarshal([]byte(strings.TrimSuffix(s[i+2:], ");")), &tr) != nil {
			t.Fatalf("script without a parameter: %.80s", s)
		}
		out = append(out, tr)
	}
	return out
}

func (f *fakeView) lastURL() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.urls) == 0 {
		return ""
	}
	return f.urls[len(f.urls)-1]
}

const (
	localOrigin  = "http://127.0.0.1:3335"
	studioOrigin = "http://localhost:41001"
)

func liveSwitcher() (*machineSwitcher, *fakeView, *[]string) {
	v := &fakeView{}
	var logs []string
	var lmu sync.Mutex
	s := &machineSwitcher{view: v, logf: func(f string, a ...any) {
		lmu.Lock()
		logs = append(logs, fmt.Sprintf(f, a...))
		lmu.Unlock()
	}}
	s.targets = []machineTarget{
		{name: "This Mac", hue: 252, origin: func() string { return localOrigin },
			open: func(p string) string { return localOrigin + p }, enter: func(p string) string { return localOrigin + p }},
		{id: "abc", name: "Studio", hue: 210, origin: func() string { return studioOrigin }, bootstrapHops: 1,
			open:  func(p string) string { return studioOrigin + p },
			enter: func(p string) string { return studioOrigin + "/__desktop/machine-bootstrap?next=" + p }},
	}
	return s, v, &logs
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestParseMachineMessage(t *testing.T) {
	good := map[string]machineMsg{
		"eggify-machine:ready 3 412":              {event: "ready", seq: 3, ms: 412},
		"eggify-machine:ready 3 -1":               {event: "ready", seq: 3, ms: -1},
		"eggify-machine:reveal 12 640":            {event: "reveal", seq: 12, ms: 640},
		"eggify-machine:cancel 2 0":               {event: "cancel", seq: 2},
		"eggify-machine:left 4 %2Fagents%3Fs%3D1": {event: "left", seq: 4, path: "/agents?s=1"},
	}
	for raw, want := range good {
		got, ok := parseMachineMessage(raw)
		if !ok || got != want {
			t.Errorf("%q = %+v, %v; want %+v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{
		"", "eggify-diag:page x", "eggify-machine:", "eggify-machine:ready", "eggify-machine:ready 1",
		"eggify-machine:ready 1 2 3", "eggify-machine:explode 1 2", "eggify-machine:ready -1 5",
		"eggify-machine:ready 0 5", "eggify-machine:ready 1 5.5", "eggify-machine:ready 1 1e3",
		"eggify-machine:ready 1 999999999", "eggify-machine:ready 1  5", "eggify-machine:reveal x 1",
		"eggify-machine:left 1 https%3A%2F%2Fevil.example", "eggify-machine:left 1 %2F%2Fevil.example",
		"eggify-machine:left 1 %2Fapi%2Fconfig", "eggify-machine:left 1 %2Flogin%3Fredirect%3D%2F",
		"eggify-machine:left 1 %2F__desktop%2Fbootstrap", "eggify-machine:left 1 %2Fa%0Ab",
		"eggify-machine:left 1 %2Fa%20b", "eggify-machine:left 1 %zz",
		"eggify-machine:backdrop 210 data:image/jpeg;base64,/9j/4AECAw==", // retired event
		"eggify-machine:left 1 " + strings.Repeat("A", 2048),
	} {
		if m, ok := parseMachineMessage(raw); ok {
			t.Errorf("%q accepted as %+v", raw, m)
		}
	}
}

func TestMachineSwitch_TimingLineAndBootstrapHop(t *testing.T) {
	s, v, logs := liveSwitcher()
	s.switchTo(1, "/agents")
	ph := v.phases(t)
	if len(ph) != 1 || ph[0].Phase != "out" || ph[0].Seq != 1 || ph[0].From != "This Mac" || ph[0].Start == 0 || ph[0].Dir != 1 {
		t.Fatalf("out = %+v", ph)
	}
	waitFor(t, "navigation", func() bool { return v.lastURL() != "" })
	// The bootstrap page commits first: covered, but not the dashboard yet.
	s.onCommit()
	s.mu.Lock()
	committed := !s.pending.tCommit.IsZero()
	s.mu.Unlock()
	if committed {
		t.Fatal("the bootstrap page counted as the dashboard")
	}
	// A reveal before the dashboard committed is ignored.
	s.handleMessage(studioOrigin+"/__desktop/machine-bootstrap", true, "eggify-machine:reveal 1 5")
	if s.pending == nil {
		t.Fatal("reveal on the bootstrap page ended the switch")
	}
	s.onCommit()
	ph = v.phases(t)
	if len(ph) != 3 || ph[1].Phase != "in" || ph[2].Phase != "in" {
		t.Fatalf("phases = %+v", ph)
	}
	// Messages from the wrong page or a subframe change nothing.
	s.handleMessage(localOrigin+"/agents", true, "eggify-machine:reveal 1 300")
	s.handleMessage(studioOrigin+"/agents", false, "eggify-machine:reveal 1 300")
	s.handleMessage(studioOrigin+"/agents", true, "eggify-machine:reveal 2 300")
	if s.pending == nil {
		t.Fatal("a foreign reveal ended the switch")
	}
	if !s.handleMessage(studioOrigin+"/agents", true, "eggify-machine:ready 1 412") {
		t.Fatal("machine message not consumed")
	}
	s.handleMessage(studioOrigin+"/agents?x=1", true, "eggify-machine:reveal 1 450")
	if s.pending != nil {
		t.Fatal("reveal did not end the switch")
	}
	if len(*logs) != 1 {
		t.Fatalf("logs = %q", *logs)
	}
	line := (*logs)[0]
	for _, want := range []string{"Machine switch → Studio: out ", "navigate→commit ", "(bootstrap page ", "commit→mounted 412ms", "total"} {
		if !strings.Contains(line, want) {
			t.Errorf("log line %q lacks %q", line, want)
		}
	}
	if s.handleMessage(studioOrigin, true, "eggify-diag:page x") {
		t.Fatal("other raw messages must be left alone")
	}
}

func TestMachineSwitch_RemembersThePage(t *testing.T) {
	s, v, _ := liveSwitcher()
	s.switchTo(1, "/agents")
	// The page left on this Mac, reported by "out" before navigating.
	s.handleMessage(studioOrigin+"/x", true, "eggify-machine:left 1 %2Fevil") // wrong page
	s.handleMessage(localOrigin+"/agents/s1", true, "eggify-machine:left 1 %2Fagents%2Fs1%3Fview%3Dzen")
	waitFor(t, "navigation", func() bool { return v.lastURL() != "" })
	if got := s.pathFor(0); got != "/agents/s1?view=zen" {
		t.Fatalf("remembered %q", got)
	}
	// Back with ⌃⇧Tab: this Mac comes from the left, on the remembered page.
	s.mu.Lock()
	s.pending = nil
	s.mu.Unlock()
	s.step(-1)
	ph := v.phases(t)
	if last := ph[len(ph)-1]; last.Phase != "out" || last.Dir != -1 || !last.Here {
		t.Fatalf("back = %+v", last)
	}
	waitFor(t, "navigation back", func() bool { return v.lastURL() == localOrigin+"/agents/s1?view=zen" })
}

func TestMachineSwitch_StayOnCancels(t *testing.T) {
	s, v, logs := liveSwitcher()
	s.switchTo(1, "/agents")
	waitFor(t, "navigation", func() bool { return v.lastURL() != "" })
	// A cancel for another switch, or from a third page, does nothing.
	s.handleMessage(studioOrigin, true, "eggify-machine:cancel 9 0")
	s.handleMessage("http://localhost:9/", true, "eggify-machine:cancel 1 0")
	if _, cur := s.snapshot(); cur != 1 {
		t.Fatal("a foreign cancel switched back")
	}
	s.handleMessage(localOrigin+"/agents", true, "eggify-machine:cancel 1 0")
	if _, cur := s.snapshot(); cur != 0 {
		t.Fatalf("cancel did not go back (current %d)", cur)
	}
	ph := v.phases(t)
	if last := ph[len(ph)-1]; last.Phase != "out" || !last.Here || last.Dir != -1 || last.Seq != 2 {
		t.Fatalf("back = %+v", last)
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "cancelled by the owner") {
		t.Fatalf("logs = %q", *logs)
	}
	waitFor(t, "navigation back", func() bool { return v.lastURL() == localOrigin+"/agents" })
}

// Two quick presses: the first switch's page commits while the second one
// still plays "out"; it stays covered (no flash of an uncovered page).
func TestMachineSwitch_QuickPressesKeepThePageCovered(t *testing.T) {
	s, v, _ := liveSwitcher()
	s.switchTo(1, "/agents")
	waitFor(t, "navigation", func() bool { return v.lastURL() != "" })
	s.switchTo(0, "/agents") // before the first page committed
	s.onCommit()
	ph := v.phases(t)
	last := ph[len(ph)-1]
	if last.Phase != "in" || !last.Hold || last.Seq != 2 {
		t.Fatalf("commit during the second out = %+v", last)
	}
	waitFor(t, "second navigation", func() bool { return v.lastURL() == localOrigin+"/agents" })
	s.onCommit()
	ph = v.phases(t)
	if last := ph[len(ph)-1]; last.Phase != "in" || last.Hold || !last.Here {
		t.Fatalf("final in = %+v", last)
	}
}

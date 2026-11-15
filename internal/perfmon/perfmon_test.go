package perfmon

import (
	"testing"
	"time"
)

func series(n int, step time.Duration, f func(i int) Sample) []Sample {
	t0 := time.Now().Add(-time.Duration(n) * step)
	out := make([]Sample, n)
	for i := range out {
		out[i] = f(i)
		out[i].Time = t0.Add(time.Duration(i) * step)
	}
	return out
}

func ids(fs []Finding) map[string]bool {
	m := map[string]bool{}
	for _, f := range fs {
		m[f.ID] = true
	}
	return m
}

func TestAnalyzeFlagsGrowth(t *testing.T) {
	// 40 samples, 1 minute apart; memory grows 2 MB/min = 120 MB/h, goroutines flat.
	s := series(40, time.Minute, func(i int) Sample { return Sample{RSSMB: 200 + float64(i)*2, Goroutines: 80, OpenFDs: 30} })
	got := ids(Analyze(s, nil))
	if !got["server-memory-growth"] {
		t.Fatalf("expected memory growth, got %v", got)
	}
	if got["goroutine-growth"] || got["fd-growth"] {
		t.Fatalf("flat series must not warn: %v", got)
	}
}

func TestAnalyzeNeedsEnoughHistory(t *testing.T) {
	s := series(5, time.Minute, func(i int) Sample { return Sample{RSSMB: 100 + float64(i)*500} })
	if got := Analyze(s, nil); len(got) != 0 {
		t.Fatalf("5 minutes is too short to call a trend: %v", got)
	}
}

func TestAnalyzeSpikeIsNotGrowth(t *testing.T) {
	s := series(40, time.Minute, func(i int) Sample {
		v := 300.0
		if i == 20 {
			v = 900
		}
		return Sample{RSSMB: v}
	})
	if ids(Analyze(s, nil))["server-memory-growth"] {
		t.Fatal("a single spike is not a trend")
	}
}

func TestAnalyzeThresholdsAndClient(t *testing.T) {
	s := []Sample{{Time: time.Now(), WALMB: 250, Children: 3, ChildrenRSSMB: 5000}}
	c := []ClientSample{
		{Time: time.Now(), Visible: true, DOMNodes: 40000, Animations: 60, FPS: 12},
		{Time: time.Now(), Visible: false, FPS: 1}, // hidden tab: ignored
	}
	got := ids(Analyze(s, c))
	for _, id := range []string{"wal-size", "children-memory", "dom-size", "animations", "low-fps"} {
		if !got[id] {
			t.Errorf("missing %s in %v", id, got)
		}
	}
}

func TestAnalyzeMainThreadStallsAndLongTasks(t *testing.T) {
	now := time.Now()
	calm := []ClientSample{{Time: now, Visible: true, FPS: 60, LongTasks: 2, MaxStallMS: 120}}
	if got := ids(Analyze(nil, calm)); got["main-thread-stall"] || got["long-tasks"] {
		t.Fatalf("a few short long tasks must not warn: %v", got)
	}
	frozen := []ClientSample{
		{Time: now, Visible: true, FPS: 60, LongTasks: 30, MaxStallMS: 200},
		{Time: now, Visible: true, FPS: 60, LongTasks: 40, MaxStallMS: 4200},
	}
	got := ids(Analyze(nil, frozen))
	if !got["main-thread-stall"] || !got["long-tasks"] {
		t.Fatalf("expected main-thread-stall and long-tasks, got %v", got)
	}
	// A hidden tab's timers are throttled: its "stalls" are not freezes.
	hidden := []ClientSample{{Time: now, Visible: false, LongTasks: 99, MaxStallMS: 60000}}
	if got := Analyze(nil, hidden); len(got) != 0 {
		t.Fatalf("hidden page must not be judged: %v", got)
	}
}

func TestHiddenTabNeverWarns(t *testing.T) {
	c := []ClientSample{{Time: time.Now(), Visible: false, DOMNodes: 99999, Animations: 99, FPS: 1}}
	if got := Analyze(nil, c); len(got) != 0 {
		t.Fatalf("hidden page must not be judged: %v", got)
	}
}

func TestParsePSAndDescendants(t *testing.T) {
	rows := parsePS("  10     1  1000  1.5\n  11    10  2048  0.0\n  12    11  4096  2.0\n  13     1   500  0.0\nbad line\n")
	if len(rows) != 4 {
		t.Fatalf("rows = %d", len(rows))
	}
}

func TestSamplerCollectAndBound(t *testing.T) {
	s := New(Options{Extra: func() map[string]float64 { return map[string]float64{"ws_clients": 2} }})
	smp := s.Collect()
	if smp.Goroutines == 0 || smp.Extra["ws_clients"] != 2 {
		t.Fatalf("bad sample: %+v", smp)
	}
	for i := 0; i < MaxClientSamples+10; i++ {
		s.AddClient(ClientSample{DOMNodes: i})
	}
	r := s.Report()
	if len(r.Client) != MaxClientSamples || r.Client[len(r.Client)-1].DOMNodes != MaxClientSamples+9 {
		t.Fatalf("client ring not bounded: %d", len(r.Client))
	}
	if len(r.Samples) != 1 {
		t.Fatalf("samples = %d", len(r.Samples))
	}
}

func TestLogsEachFindingOnce(t *testing.T) {
	var lines int
	s := New(Options{Logf: func(string, ...any) { lines++ }})
	fs := []Finding{{ID: "x", Message: "m"}}
	s.logNewFindings(fs)
	s.logNewFindings(fs)
	if lines != 1 {
		t.Fatalf("logged %d times", lines)
	}
	s.logNewFindings(nil)
	s.logNewFindings(fs)
	if lines != 2 {
		t.Fatalf("should log again after it cleared, got %d", lines)
	}
}

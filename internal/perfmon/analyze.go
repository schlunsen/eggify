package perfmon

import (
	"fmt"
	"sort"
	"time"
)

// Finding is something worth a look.
type Finding struct {
	ID       string `json:"id"`
	Severity string `json:"severity"` // "warn" | "info"
	Message  string `json:"message"`
}

// Thresholds. Growth is per hour over the observed window; a window shorter
// than minWindow is too short to call a trend.
const (
	minWindow        = 10 * time.Minute
	rssGrowthMBHour  = 60.0
	goroutineGrowth  = 100.0
	fdGrowthHour     = 50.0
	walWarnMB        = 100.0
	domNodesWarn     = 15000
	domGrowthHour    = 3000.0
	animationsWarn   = 25
	fpsWarn          = 30.0
	childrenRSSWarn  = 4096.0 // MB
	jsHeapGrowthHour = 150.0
	stallWarnMS      = 1000.0 // one main-thread task this long is a visible freeze
	longTasksWarn    = 20     // long tasks per sample (30 s), averaged over recent samples
)

// Analyze looks at the history and returns what looks wrong.
func Analyze(samples []Sample, client []ClientSample) []Finding {
	var out []Finding
	warn := func(id, format string, args ...any) {
		out = append(out, Finding{ID: id, Severity: "warn", Message: fmt.Sprintf(format, args...)})
	}

	if n := len(samples); n >= 2 && samples[n-1].Time.Sub(samples[0].Time) >= minWindow {
		span := func(get func(Sample) float64) (perHour float64) {
			return slopePerHour(len(samples), func(i int) (time.Time, float64) { return samples[i].Time, get(samples[i]) })
		}
		if g := span(func(s Sample) float64 { return s.RSSMB }); g > rssGrowthMBHour {
			warn("server-memory-growth", "Server memory is growing by about %.0f MB/hour (now %.0f MB).", g, samples[n-1].RSSMB)
		}
		if g := span(func(s Sample) float64 { return float64(s.Goroutines) }); g > goroutineGrowth {
			warn("goroutine-growth", "Goroutines are growing by about %.0f/hour (now %d): something is not being cleaned up.", g, samples[n-1].Goroutines)
		}
		if g := span(func(s Sample) float64 { return float64(s.OpenFDs) }); g > fdGrowthHour {
			warn("fd-growth", "Open file descriptors are growing by about %.0f/hour (now %d).", g, samples[n-1].OpenFDs)
		}
	}
	if n := len(samples); n > 0 {
		last := samples[n-1]
		if last.WALMB > walWarnMB {
			warn("wal-size", "The database write-ahead log is %.0f MB; a checkpoint is not completing.", last.WALMB)
		}
		if last.ChildrenRSSMB > childrenRSSWarn {
			warn("children-memory", "Agent processes use %.1f GB across %d processes.", last.ChildrenRSSMB/1024, last.Children)
		}
	}

	// Dashboard: judge only samples taken while the page was visible.
	var visible []ClientSample
	for _, c := range client {
		if c.Visible {
			visible = append(visible, c)
		}
	}
	if n := len(visible); n > 0 {
		last := visible[n-1]
		if last.DOMNodes > domNodesWarn {
			warn("dom-size", "The page has %d DOM nodes; long conversations may need to be trimmed or virtualised.", last.DOMNodes)
		}
		if last.Animations > animationsWarn {
			warn("animations", "%d animations are running at once; each keeps the GPU process busy.", last.Animations)
		}
		// Average the newest few so one slow frame does not trigger it.
		if recent := tail(visible, 4); averageFPS(recent) > 0 && averageFPS(recent) < fpsWarn {
			warn("low-fps", "The page is rendering at %.0f fps.", averageFPS(recent))
		}
		recent := tail(visible, 4)
		var worstStall float64
		longTasks := 0
		for _, c := range recent {
			longTasks += c.LongTasks
			if c.MaxStallMS > worstStall {
				worstStall = c.MaxStallMS
			}
		}
		if worstStall >= stallWarnMS {
			warn("main-thread-stall", "The page's main thread froze for %.1f s; while frozen it cannot read its WebSocket or respond to input.", worstStall/1000)
		}
		if avg := float64(longTasks) / float64(len(recent)); avg > longTasksWarn {
			warn("long-tasks", "The page runs about %.0f long tasks (over 50 ms) every 30 s.", avg)
		}
		if n >= 2 && last.Time.Sub(visible[0].Time) >= minWindow {
			if g := slopePerHour(n, func(i int) (time.Time, float64) { return visible[i].Time, float64(visible[i].DOMNodes) }); g > domGrowthHour {
				warn("dom-growth", "The page's DOM is growing by about %.0f nodes/hour.", g)
			}
			if g := slopePerHour(n, func(i int) (time.Time, float64) { return visible[i].Time, visible[i].JSHeapMB }); g > jsHeapGrowthHour {
				warn("js-heap-growth", "The page's JavaScript memory is growing by about %.0f MB/hour.", g)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func tail[T any](list []T, n int) []T {
	if len(list) <= n {
		return list
	}
	return list[len(list)-n:]
}

func averageFPS(list []ClientSample) float64 {
	var sum float64
	var n int
	for _, c := range list {
		if c.FPS > 0 {
			sum += c.FPS
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// slopePerHour is the least-squares slope of the values per hour. Using a fit
// rather than last minus first keeps one spike from looking like growth.
func slopePerHour(n int, at func(i int) (time.Time, float64)) float64 {
	if n < 2 {
		return 0
	}
	t0, _ := at(0)
	var sx, sy, sxx, sxy float64
	for i := 0; i < n; i++ {
		t, y := at(i)
		x := t.Sub(t0).Hours()
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	fn := float64(n)
	den := fn*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return (fn*sxy - sx*sy) / den
}

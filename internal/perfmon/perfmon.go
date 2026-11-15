// Package perfmon records how the server (and the dashboard in front of it)
// behaves over time, so slow growth that nobody notices in a single minute
// shows up: memory, goroutines, file descriptors, child agent processes, the
// SQLite WAL, and the UI's DOM size, running animations and frame rate.
//
// Samples live in memory in a ring buffer (about 24 h at the default interval)
// and are served by GET /api/perf together with findings from Analyze.
package perfmon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultInterval is how often the server is sampled.
	DefaultInterval = 30 * time.Second
	// MaxSamples keeps about 24 h at DefaultInterval.
	MaxSamples = 2880
	// MaxClientSamples keeps the newest dashboard samples.
	MaxClientSamples = 2880
)

// Sample is one reading of the server process.
type Sample struct {
	Time          time.Time `json:"time"`
	Goroutines    int       `json:"goroutines"`
	HeapMB        float64   `json:"heap_mb"`
	SysMB         float64   `json:"sys_mb"`
	GCCount       uint32    `json:"gc_count"`
	RSSMB         float64   `json:"rss_mb"`
	CPUPercent    float64   `json:"cpu_percent"`
	OpenFDs       int       `json:"open_fds"`
	Children      int       `json:"children"`
	ChildrenRSSMB float64   `json:"children_rss_mb"`
	DBMB          float64   `json:"db_mb"`
	WALMB         float64   `json:"wal_mb"`
	// Extra holds app counters (WebSocket clients, running sessions, ...).
	Extra map[string]float64 `json:"extra,omitempty"`
}

// ClientSample is one reading of the dashboard, posted by the page.
type ClientSample struct {
	Time       time.Time `json:"time"`
	DOMNodes   int       `json:"dom_nodes"`
	Animations int       `json:"animations"`
	FPS        float64   `json:"fps"`
	Messages   int       `json:"messages"`
	Sessions   int       `json:"sessions"`
	JSHeapMB   float64   `json:"js_heap_mb"`
	// LongTasks counts main-thread tasks over 50 ms since the previous sample;
	// MaxStallMS is the longest one (or the longest timer delay where the
	// browser has no long-task API, as in WKWebView). A frozen page stops
	// reading its WebSocket, so this is the early sign of that.
	LongTasks  int     `json:"long_tasks"`
	MaxStallMS float64 `json:"max_stall_ms"`
	Visible    bool    `json:"visible"`
	Page       string  `json:"page,omitempty"`
}

// Options configures a Sampler.
type Options struct {
	Interval time.Duration
	// DBPath is the SQLite file; its -wal sibling is measured too.
	DBPath string
	// Extra returns app counters added to every sample (may be nil).
	Extra func() map[string]float64
	// Logf receives warnings when a new finding appears (may be nil).
	Logf func(format string, args ...any)
}

// Sampler collects samples until its context ends.
type Sampler struct {
	opts      Options
	startedAt time.Time

	mu      sync.RWMutex
	samples []Sample
	client  []ClientSample
	logged  map[string]bool
}

// New creates a sampler. Call Run to start it.
func New(opts Options) *Sampler {
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	return &Sampler{opts: opts, startedAt: time.Now(), logged: map[string]bool{}}
}

// Run samples now and then every interval until ctx ends.
func (s *Sampler) Run(ctx context.Context) {
	s.Collect()
	t := time.NewTicker(s.opts.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Collect()
		}
	}
}

// Collect takes one reading and runs the checks.
func (s *Sampler) Collect() Sample {
	smp := readSample(s.opts)
	s.mu.Lock()
	s.samples = appendBounded(s.samples, smp, MaxSamples)
	snapshot := append([]Sample(nil), s.samples...)
	clientSnapshot := append([]ClientSample(nil), s.client...)
	s.mu.Unlock()
	s.logNewFindings(Analyze(snapshot, clientSnapshot))
	return smp
}

// AddClient stores a dashboard sample.
func (s *Sampler) AddClient(c ClientSample) {
	if c.Time.IsZero() {
		c.Time = time.Now()
	}
	s.mu.Lock()
	s.client = appendBounded(s.client, c, MaxClientSamples)
	s.mu.Unlock()
}

// Report is what GET /api/perf returns.
type Report struct {
	StartedAt time.Time      `json:"started_at"`
	Interval  float64        `json:"interval_seconds"`
	Samples   []Sample       `json:"samples"`
	Client    []ClientSample `json:"client"`
	Findings  []Finding      `json:"findings"`
}

// Report returns the history and the current findings.
func (s *Sampler) Report() Report {
	s.mu.RLock()
	samples := append([]Sample(nil), s.samples...)
	client := append([]ClientSample(nil), s.client...)
	s.mu.RUnlock()
	return Report{
		StartedAt: s.startedAt,
		Interval:  s.opts.Interval.Seconds(),
		Samples:   samples,
		Client:    client,
		Findings:  Analyze(samples, client),
	}
}

func (s *Sampler) logNewFindings(fs []Finding) {
	if s.opts.Logf == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	active := map[string]bool{}
	for _, f := range fs {
		active[f.ID] = true
		if !s.logged[f.ID] {
			s.logged[f.ID] = true
			s.opts.Logf("perf: %s: %s", f.ID, f.Message)
		}
	}
	for id := range s.logged {
		if !active[id] {
			delete(s.logged, id) // log again if it comes back
		}
	}
}

func appendBounded[T any](list []T, v T, max int) []T {
	list = append(list, v)
	if len(list) > max {
		list = append([]T(nil), list[len(list)-max:]...)
	}
	return list
}

const mb = 1024 * 1024

func readSample(o Options) Sample {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	smp := Sample{
		Time:       time.Now(),
		Goroutines: runtime.NumGoroutine(),
		HeapMB:     float64(ms.HeapAlloc) / mb,
		SysMB:      float64(ms.Sys) / mb,
		GCCount:    ms.NumGC,
		OpenFDs:    countFDs(),
	}
	smp.RSSMB, smp.CPUPercent, smp.Children, smp.ChildrenRSSMB = readProcesses(os.Getpid())
	if o.DBPath != "" {
		smp.DBMB = fileMB(o.DBPath)
		smp.WALMB = fileMB(o.DBPath + "-wal")
	}
	if o.Extra != nil {
		smp.Extra = o.Extra()
	}
	return smp
}

func fileMB(path string) float64 {
	st, err := os.Stat(filepath.Clean(path))
	if err != nil {
		return 0
	}
	return float64(st.Size()) / mb
}

func countFDs() int {
	for _, dir := range []string{"/proc/self/fd", "/dev/fd"} {
		if ents, err := os.ReadDir(dir); err == nil {
			return len(ents)
		}
	}
	return 0
}

type procRow struct {
	pid, ppid int
	rssKB     float64
	cpu       float64
}

// readProcesses returns the RSS and CPU of pid and the count and RSS of all
// its descendants (the agent CLIs), from one `ps` call. Zeros where ps is not
// available.
func readProcesses(pid int) (rssMB, cpu float64, children int, childrenRSSMB float64) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,rss=,pcpu=").Output() // #nosec G204 -- fixed arguments
	if err != nil {
		return
	}
	rows := parsePS(string(out))
	byParent := map[int][]procRow{}
	var self procRow
	for _, r := range rows {
		byParent[r.ppid] = append(byParent[r.ppid], r)
		if r.pid == pid {
			self = r
		}
	}
	rssMB, cpu = self.rssKB/1024, self.cpu
	seen := map[int]bool{pid: true}
	queue := []int{pid}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range byParent[cur] {
			if seen[c.pid] {
				continue
			}
			seen[c.pid] = true
			children++
			childrenRSSMB += c.rssKB / 1024
			queue = append(queue, c.pid)
		}
	}
	return
}

func parsePS(out string) []procRow {
	var rows []procRow
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		rss, e3 := strconv.ParseFloat(f[2], 64)
		cpu, e4 := strconv.ParseFloat(f[3], 64)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			continue
		}
		rows = append(rows, procRow{pid, ppid, rss, cpu})
	}
	return rows
}

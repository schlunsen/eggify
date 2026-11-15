package perfmon

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Usage is this machine's CPU and memory right now, plus Eggify's share
// (the server and its agent processes). Shown next to the machine chip, like
// a menu-bar meter, for this Mac and for the Macs it can switch to.
type Usage struct {
	Time time.Time `json:"time"`
	// CPUPercent is the whole machine, 0..100 (all cores together).
	CPUPercent float64 `json:"cpu_percent"`
	// MemUsedBytes is memory in use as Activity Monitor counts it (app
	// memory + wired + compressed on macOS; total - available on Linux).
	MemUsedBytes  uint64 `json:"mem_used_bytes"`
	MemTotalBytes uint64 `json:"mem_total_bytes"`
	Cores         int    `json:"cores"`
	// Eggify: the server process and everything it started.
	EggifyCPUPercent float64 `json:"eggify_cpu_percent"`
	EggifyRSSBytes   uint64  `json:"eggify_rss_bytes"`
	EggifyProcesses  int     `json:"eggify_processes"`
	// Available is false where the machine figures could not be read.
	Available bool `json:"available"`
}

// usageMaxAge is how long a reading is reused: one ps call per caller burst.
const usageMaxAge = 3 * time.Second

var usageCache struct {
	sync.Mutex
	last     Usage
	prevStat cpuTicks // Linux: the previous /proc/stat reading
}

// ReadUsage returns the current Usage, at most usageMaxAge old.
func ReadUsage() Usage {
	usageCache.Lock()
	defer usageCache.Unlock()
	if time.Since(usageCache.last.Time) < usageMaxAge {
		return usageCache.last
	}
	u := Usage{Time: time.Now(), Cores: runtime.NumCPU()}
	rows := psRows()
	u.EggifyCPUPercent, u.EggifyRSSBytes, u.EggifyProcesses = treeUsage(rows, os.Getpid(), u.Cores)
	switch runtime.GOOS {
	case "darwin":
		u.CPUPercent = psMachineCPU(rows, u.Cores)
		u.MemUsedBytes, u.MemTotalBytes = darwinMemory()
	case "linux":
		var now cpuTicks
		u.CPUPercent, now = linuxCPU(usageCache.prevStat)
		usageCache.prevStat = now
		u.MemUsedBytes, u.MemTotalBytes = linuxMemory()
	}
	u.Available = u.MemTotalBytes > 0
	usageCache.last = u
	return u
}

func psRows() []procRow {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,rss=,pcpu=").Output() // #nosec G204 -- fixed arguments
	if err != nil {
		return nil
	}
	return parsePS(string(out))
}

// treeUsage sums CPU (as a share of the whole machine) and RSS over pid and
// all its descendants.
func treeUsage(rows []procRow, pid, cores int) (cpu float64, rss uint64, n int) {
	byParent := map[int][]procRow{}
	for _, r := range rows {
		byParent[r.ppid] = append(byParent[r.ppid], r)
		if r.pid == pid {
			cpu, rss, n = r.cpu, uint64(r.rssKB*1024), 1
		}
	}
	if n == 0 {
		return 0, 0, 0
	}
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
			cpu += c.cpu
			rss += uint64(c.rssKB * 1024)
			n++
			queue = append(queue, c.pid)
		}
	}
	return clampPercent(cpu / float64(max(cores, 1))), rss, n
}

// psMachineCPU is the sum of every process's %CPU (ps counts one core as
// 100) over the core count. macOS ps reports a short decaying average, close
// to what menu-bar meters show.
func psMachineCPU(rows []procRow, cores int) float64 {
	var sum float64
	for _, r := range rows {
		sum += r.cpu
	}
	return clampPercent(sum / float64(max(cores, 1)))
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return float64(int(v*10+0.5)) / 10
}

// darwinMemory reads hw.memsize and vm_stat: used = app memory (anonymous
// minus purgeable) + wired + compressed, as Activity Monitor shows it.
func darwinMemory() (used, total uint64) {
	if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil { // #nosec G204 -- fixed arguments
		total, _ = strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	}
	out, err := exec.Command("vm_stat").Output() // #nosec G204 -- fixed arguments
	if err != nil || total == 0 {
		return 0, total
	}
	return parseVMStat(string(out)), total
}

func parseVMStat(out string) uint64 {
	pageSize := uint64(4096)
	pages := map[string]uint64{}
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "page size of "); i >= 0 {
			if f := strings.Fields(line[i+len("page size of "):]); len(f) > 0 {
				if v, err := strconv.ParseUint(f[0], 10, 64); err == nil && v > 0 {
					pageSize = v
				}
			}
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(v), "."), 10, 64)
		if err == nil {
			pages[strings.TrimSpace(k)] = n
		}
	}
	app := pages["Anonymous pages"]
	if p := pages["Pages purgeable"]; p < app {
		app -= p
	}
	return (app + pages["Pages wired down"] + pages["Pages occupied by compressor"]) * pageSize
}

func linuxMemory() (used, total uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	var avail uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = v * 1024
		case "MemAvailable:":
			avail = v * 1024
		}
	}
	if avail > total {
		avail = total
	}
	return total - avail, total
}

type cpuTicks struct{ idle, total uint64 }

// linuxCPU is the busy share since the previous reading (0 on the first).
func linuxCPU(prev cpuTicks) (float64, cpuTicks) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, prev
	}
	line, _, _ := strings.Cut(string(data), "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, prev
	}
	var now cpuTicks
	for i, s := range f[1:] {
		v, _ := strconv.ParseUint(s, 10, 64)
		now.total += v
		if i == 3 || i == 4 { // idle, iowait
			now.idle += v
		}
	}
	if prev.total == 0 || now.total <= prev.total {
		return 0, now
	}
	dt, di := now.total-prev.total, now.idle-prev.idle
	return clampPercent(100 * float64(dt-di) / float64(dt)), now
}

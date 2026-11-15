package perfmon

import (
	"runtime"
	"testing"
)

func TestParseVMStat(t *testing.T) {
	out := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                               12345.
Anonymous pages:                         600000.
Pages purgeable:                          10000.
Pages wired down:                        200000.
Pages occupied by compressor:             50000.
`
	if got, want := parseVMStat(out), uint64(600000-10000+200000+50000)*16384; got != want {
		t.Fatalf("used = %d, want %d", got, want)
	}
}

func TestTreeUsageSumsDescendantsAsShareOfMachine(t *testing.T) {
	rows := []procRow{
		{pid: 10, ppid: 1, rssKB: 1024, cpu: 50},
		{pid: 11, ppid: 10, rssKB: 2048, cpu: 150}, // an agent CLI on 1.5 cores
		{pid: 12, ppid: 11, rssKB: 1024, cpu: 0},
		{pid: 13, ppid: 1, rssKB: 9999, cpu: 300}, // not ours
	}
	cpu, rss, n := treeUsage(rows, 10, 4)
	if cpu != 50 || rss != 4096*1024 || n != 3 {
		t.Fatalf("cpu=%v rss=%d n=%d", cpu, rss, n)
	}
	if got := psMachineCPU(rows, 4); got != 100 {
		t.Fatalf("machine cpu = %v", got)
	}
}

func TestReadUsageOnThisMachine(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("darwin/linux only")
	}
	u := ReadUsage()
	if !u.Available || u.MemTotalBytes == 0 || u.MemUsedBytes == 0 || u.MemUsedBytes > u.MemTotalBytes || u.EggifyProcesses < 1 {
		t.Fatalf("%+v", u)
	}
	t.Logf("cpu %.1f%%, ram %.1f / %.1f GB, eggify %.1f%% %.0f MB in %d processes",
		u.CPUPercent, float64(u.MemUsedBytes)/1e9, float64(u.MemTotalBytes)/1e9,
		u.EggifyCPUPercent, float64(u.EggifyRSSBytes)/1e6, u.EggifyProcesses)
}
